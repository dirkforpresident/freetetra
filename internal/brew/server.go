package brew

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/freetetra/server/internal/config"
)

type EventHandler interface {
	OnConnect(client *Client)
	OnDisconnect(client *Client)
	OnMessage(client *Client, msg ParsedMessage)
}

type Server struct {
	cfg     config.Config
	logger  *log.Logger
	handler EventHandler

	httpServer *http.Server
	upgrader   websocket.Upgrader

	clientsMu sync.RWMutex
	clients   map[string]*Client

	nonceMu sync.Mutex
	nonces  map[string]time.Time

	tokenMu sync.Mutex
	tokens  map[string]time.Time

	extraHandlers []httpHandlerRegistration
}

type httpHandlerRegistration struct {
	pattern string
	handler http.HandlerFunc
}

func NewServer(cfg config.Config, logger *log.Logger, handler EventHandler) *Server {
	return &Server{
		cfg:     cfg,
		logger:  logger,
		handler: handler,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
			Subprotocols: []string{"brew"},
		},
		clients: make(map[string]*Client),
		nonces:  make(map[string]time.Time),
		tokens:  make(map[string]time.Time),
	}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	basePath := s.cfg.Server.Path
	wsPrefix := basePath + "/ws/"
	mux.HandleFunc(basePath, s.handleDiscovery)
	mux.HandleFunc(basePath+"/", s.handleDiscovery)
	mux.HandleFunc(wsPrefix, s.handleWebsocket)
	for _, h := range s.extraHandlers {
		mux.HandleFunc(h.pattern, h.handler)
	}

	s.httpServer = &http.Server{
		Addr:    s.cfg.HTTPListenAddr,
		Handler: mux,
	}

	listener, err := net.Listen("tcp", s.cfg.HTTPListenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.logger.Printf("brew server listening on %s (discovery path %s)", s.cfg.HTTPListenAddr, s.cfg.Server.Path)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(shutdownCtx)
	}()

	err = s.httpServer.Serve(listener)
	if err == nil || err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) BroadcastToGroup(gssi uint32, payload []byte, excludeClientID string) int {
	clients := s.snapshotClients()
	delivered := 0
	for _, c := range clients {
		if c.ID == excludeClientID {
			continue
		}
		if !c.IsAttachedTo(gssi) {
			continue
		}
		if c.Enqueue(payload) {
			delivered++
		}
	}
	return delivered
}

func (s *Server) BroadcastAll(payload []byte, excludeClientID string) int {
	clients := s.snapshotClients()
	delivered := 0
	for _, c := range clients {
		if c.ID == excludeClientID {
			continue
		}
		if c.Enqueue(payload) {
			delivered++
		}
	}
	return delivered
}

func (s *Server) BroadcastToSubscriber(number uint32, payload []byte, excludeClientID string) int {
	clients := s.snapshotClients()
	delivered := 0
	for _, c := range clients {
		if c.ID == excludeClientID {
			continue
		}
		if !c.HasSubscriber(number) {
			continue
		}
		if c.Enqueue(payload) {
			delivered++
		}
	}
	return delivered
}

func (s *Server) SendToClient(clientID string, payload []byte) bool {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return false
	}
	s.clientsMu.RLock()
	client := s.clients[clientID]
	s.clientsMu.RUnlock()
	if client == nil {
		return false
	}
	return client.Enqueue(payload)
}

func (s *Server) CountAttachedToGroup(gssi uint32) int {
	clients := s.snapshotClients()
	count := 0
	for _, c := range clients {
		if c.IsAttachedTo(gssi) {
			count++
		}
	}
	return count
}

func (s *Server) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	if websocket.IsWebSocketUpgrade(r) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.authEnabled() {
		if !s.authorizeRequest(r) {
			s.issueDigestChallenge(w)
			return
		}
	}

	token := s.issueToken()
	endpoint := fmt.Sprintf("%s/ws/%s", s.cfg.Server.Path, token)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(endpoint))
}

func (s *Server) handleWebsocket(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, s.cfg.Server.Path+"/ws/")
	if token == "" || token == r.URL.Path {
		http.Error(w, "missing endpoint token", http.StatusUnauthorized)
		return
	}
	if !s.consumeToken(token) {
		http.Error(w, "invalid endpoint token", http.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Printf("ws upgrade failed: %v", err)
		return
	}
	id := randHex(8)
	client := newClient(id, r.RemoteAddr, conn)
	s.registerClient(client)
	if s.handler != nil {
		s.handler.OnConnect(client)
	}
	s.logger.Printf("client connected id=%s remote=%s", id, r.RemoteAddr)

	go s.writeLoop(client)
	s.readLoop(client)
}

func (s *Server) RegisterHTTPHandler(pattern string, handler http.HandlerFunc) {
	if strings.TrimSpace(pattern) == "" || handler == nil {
		return
	}
	s.extraHandlers = append(s.extraHandlers, httpHandlerRegistration{
		pattern: pattern,
		handler: handler,
	})
}

func (s *Server) SnapshotClients() []ClientSnapshot {
	clients := s.snapshotClients()
	out := make([]ClientSnapshot, 0, len(clients))
	for _, c := range clients {
		out = append(out, c.Snapshot())
	}
	return out
}

func (s *Server) readLoop(client *Client) {
	defer s.disconnectClient(client)

	extendReadDeadline := func() {
		if s.cfg.Server.PongTimeout <= 0 {
			return
		}
		_ = client.conn.SetReadDeadline(time.Now().Add(s.cfg.Server.PongTimeout))
	}
	extendReadDeadline()
	client.conn.SetPingHandler(func(appData string) error {
		extendReadDeadline()
		return client.conn.WriteControl(
			websocket.PongMessage,
			[]byte(appData),
			time.Now().Add(s.cfg.Server.WriteTimeout),
		)
	})
	client.conn.SetPongHandler(func(string) error {
		extendReadDeadline()
		return nil
	})

	for {
		msgType, payload, err := client.conn.ReadMessage()
		if err != nil {
			if closeErr, ok := err.(*websocket.CloseError); ok {
				s.logger.Printf(
					"client=%s read closed code=%d text=%q",
					client.ID,
					closeErr.Code,
					closeErr.Text,
				)
			} else {
				s.logger.Printf("client=%s read error: %v", client.ID, err)
			}
			return
		}
		extendReadDeadline()
		if msgType != websocket.BinaryMessage {
			continue
		}
		msg, err := ParseMessage(payload)
		if err != nil {
			s.logger.Printf("client=%s invalid brew packet: %v", client.ID, err)
			continue
		}
		if sub, ok := msg.(*SubscriberMessage); ok {
			client.ApplySubscriber(sub)
		}
		if s.handler != nil {
			s.handler.OnMessage(client, msg)
		}
	}
}

func (s *Server) writeLoop(client *Client) {
	ticker := time.NewTicker(s.cfg.Server.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case payload, ok := <-client.send:
			if !ok {
				_ = client.conn.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(s.cfg.Server.WriteTimeout))
				return
			}
			_ = client.conn.SetWriteDeadline(time.Now().Add(s.cfg.Server.WriteTimeout))
			if err := client.conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
				s.logger.Printf("client=%s write error: %v", client.ID, err)
				return
			}
			_ = client.conn.SetWriteDeadline(time.Time{})
		case <-ticker.C:
			if err := client.conn.WriteControl(websocket.PingMessage, []byte("hb"), time.Now().Add(s.cfg.Server.WriteTimeout)); err != nil {
				s.logger.Printf("client=%s ping error: %v", client.ID, err)
				return
			}
		}
	}
}

func (s *Server) disconnectClient(client *Client) {
	s.unregisterClient(client.ID)
	_ = client.Close()
	s.logger.Printf("client disconnected id=%s", client.ID)
	if s.handler != nil {
		s.handler.OnDisconnect(client)
	}
}

func (s *Server) registerClient(c *Client) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	s.clients[c.ID] = c
}

func (s *Server) unregisterClient(id string) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	delete(s.clients, id)
}

func (s *Server) snapshotClients() []*Client {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	out := make([]*Client, 0, len(s.clients))
	for _, c := range s.clients {
		out = append(out, c)
	}
	return out
}

func (s *Server) authEnabled() bool {
	return strings.TrimSpace(s.cfg.Server.Username) != "" || strings.TrimSpace(s.cfg.Server.Password) != ""
}

func (s *Server) authorizeRequest(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(header), "digest ") {
		return false
	}
	params := parseDigestHeader(header)
	if params["username"] != s.cfg.Server.Username {
		return false
	}
	if params["realm"] != s.cfg.Server.Realm {
		return false
	}

	nonce := params["nonce"]
	if nonce == "" || !s.nonceValid(nonce) {
		return false
	}

	uri := params["uri"]
	response := params["response"]
	qop := params["qop"]
	nc := params["nc"]
	cnonce := params["cnonce"]

	ha1 := md5Hex(fmt.Sprintf("%s:%s:%s", s.cfg.Server.Username, s.cfg.Server.Realm, s.cfg.Server.Password))
	ha2 := md5Hex(fmt.Sprintf("%s:%s", r.Method, uri))

	var expected string
	if qop != "" {
		expected = md5Hex(fmt.Sprintf("%s:%s:%s:%s:%s:%s", ha1, nonce, nc, cnonce, qop, ha2))
	} else {
		expected = md5Hex(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
	}

	return subtle.ConstantTimeCompare([]byte(strings.ToLower(response)), []byte(strings.ToLower(expected))) == 1
}

func (s *Server) issueDigestChallenge(w http.ResponseWriter) {
	nonce := randHex(16)
	opaque := randHex(8)

	expiresAt := time.Now().Add(s.cfg.Server.TokenTTL)
	s.nonceMu.Lock()
	s.nonces[nonce] = expiresAt
	s.nonceMu.Unlock()

	header := fmt.Sprintf(
		`Digest realm="%s", qop="auth", nonce="%s", opaque="%s"`,
		s.cfg.Server.Realm,
		nonce,
		opaque,
	)
	w.Header().Set("WWW-Authenticate", header)
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte("unauthorized"))
}

func (s *Server) nonceValid(nonce string) bool {
	s.nonceMu.Lock()
	defer s.nonceMu.Unlock()
	exp, ok := s.nonces[nonce]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.nonces, nonce)
		return false
	}
	return true
}

func (s *Server) issueToken() string {
	token := randHex(16)
	expiresAt := time.Now().Add(s.cfg.Server.TokenTTL)
	s.tokenMu.Lock()
	s.tokens[token] = expiresAt
	s.tokenMu.Unlock()
	return token
}

func (s *Server) consumeToken(token string) bool {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	exp, ok := s.tokens[token]
	if !ok {
		return false
	}
	delete(s.tokens, token)
	if time.Now().After(exp) {
		return false
	}
	return true
}

func parseDigestHeader(header string) map[string]string {
	header = strings.TrimSpace(header)
	header = strings.TrimPrefix(header, "Digest ")
	header = strings.TrimPrefix(header, "digest ")

	params := make(map[string]string)
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(pair[0]))
		val := strings.Trim(strings.TrimSpace(pair[1]), `"`)
		params[key] = val
	}
	return params
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
