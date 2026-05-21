package federation

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"sync"
)

// UDP-Voice-Plane: Voice-Frames laufen ueber UDP statt im TCP-WebSocket-Stream,
// um TCP-head-of-line-blocking zu vermeiden (verlorene Frames blockieren sonst
// nachfolgende → Burst-Empfang → Audio-Schleppe am Call-Ende).
//
// Packet-Format (alle Bytes raw, big-endian):
//   [16 byte token][36 byte call_uuid ASCII][frame_data...]
//
// Token: pro Peer ein 16-byte random secret, ausgetauscht beim WS-Hello.
//        Schuetzt vor Spoofing — wer den Token nicht hat, kommt nicht durch.
//        Wechselt automatisch bei jedem neuen Hello (= Reconnect).
const (
	udpTokenLen = 16
	udpUUIDLen  = 36
	udpHdrLen   = udpTokenLen + udpUUIDLen
)

// VoiceHandler wird vom UDP-Listener aufgerufen bei jedem gueltigen
// Voice-Frame (Token-validiert).
type VoiceHandler func(peerName string, callUUID string, frameData []byte)

// UDPVoice managed den UDP-Listener + Senden zu peers.
type UDPVoice struct {
	logger      *log.Logger
	conn        *net.UDPConn
	localPort   int
	advertised  string // "host:port" das wir Peers ankuendigen

	tokensMu    sync.RWMutex
	byToken     map[string]string // hex(token) -> peerName (inbound auth)

	peersMu     sync.RWMutex
	peerSenders map[string]*peerUDPSender // peerName -> sender info
}

type peerUDPSender struct {
	addr  *net.UDPAddr
	token []byte // remote peer's inbound token (what they expect from us)
}

// NewUDPVoice oeffnet einen UDP-Listener auf dem konfigurierten Port.
// Port 0 = disabled, gibt nil zurueck.
func NewUDPVoice(port int, advertised string, logger *log.Logger, handler VoiceHandler) (*UDPVoice, error) {
	if port == 0 {
		return nil, nil
	}
	addr := &net.UDPAddr{Port: port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("udp listen :%d: %w", port, err)
	}
	uv := &UDPVoice{
		logger:      logger,
		conn:        conn,
		localPort:   port,
		advertised:  advertised,
		byToken:     make(map[string]string),
		peerSenders: make(map[string]*peerUDPSender),
	}
	go uv.readLoop(handler)
	logger.Printf("federation: UDP voice listening on :%d (advertised: %s)", port, advertised)
	return uv, nil
}

// Advertised gibt die "host:port"-Adresse die wir Peers melden.
func (uv *UDPVoice) Advertised() string {
	if uv == nil {
		return ""
	}
	return uv.advertised
}

// NewToken generiert einen neuen 16-byte token (hex-codiert fuer Transport).
func NewToken() string {
	b := make([]byte, udpTokenLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RegisterPeer speichert die UDP-Adresse + token (= was wir senden) eines
// Peers nach erfolgreichem Hello-Handshake. peerInToken = token den DIESER
// Peer von uns erwartet (geht in unsere ausgehenden Pakete). peerOutToken
// = der token den WIR von ihm erwarten (geht in unseren byToken-map zur
// inbound-auth).
func (uv *UDPVoice) RegisterPeer(peerName, remoteUDP string, peerInTokenHex, peerOutTokenHex string) {
	if uv == nil {
		return
	}
	addr, err := net.ResolveUDPAddr("udp", remoteUDP)
	if err != nil {
		uv.logger.Printf("federation: invalid UDP addr for peer %s: %s (%v)", peerName, remoteUDP, err)
		return
	}
	inToken, err := hex.DecodeString(peerInTokenHex)
	if err != nil || len(inToken) != udpTokenLen {
		uv.logger.Printf("federation: invalid UDP in-token for peer %s", peerName)
		return
	}
	outToken, err := hex.DecodeString(peerOutTokenHex)
	if err != nil || len(outToken) != udpTokenLen {
		uv.logger.Printf("federation: invalid UDP out-token for peer %s", peerName)
		return
	}
	uv.peersMu.Lock()
	uv.peerSenders[peerName] = &peerUDPSender{addr: addr, token: inToken}
	uv.peersMu.Unlock()

	uv.tokensMu.Lock()
	uv.byToken[hex.EncodeToString(outToken)] = peerName
	uv.tokensMu.Unlock()

	uv.logger.Printf("federation: UDP peer registered name=%s addr=%s send_token=%s recv_token=%s",
		peerName, remoteUDP, peerInTokenHex[:8], peerOutTokenHex[:8])
}

// UnregisterPeer entfernt einen Peer (z.B. bei Disconnect).
func (uv *UDPVoice) UnregisterPeer(peerName string) {
	if uv == nil {
		return
	}
	uv.peersMu.Lock()
	delete(uv.peerSenders, peerName)
	uv.peersMu.Unlock()

	uv.tokensMu.Lock()
	for k, v := range uv.byToken {
		if v == peerName {
			delete(uv.byToken, k)
		}
	}
	uv.tokensMu.Unlock()
}

// SendVoice sendet einen Voice-Frame an einen bestimmten Peer per UDP.
// Wenn der Peer keine UDP-Adresse hat, return false (Caller faellt auf
// TCP-WS-Pfad zurueck).
func (uv *UDPVoice) SendVoice(peerName string, callUUID string, frameData []byte) bool {
	if uv == nil {
		return false
	}
	uv.peersMu.RLock()
	s := uv.peerSenders[peerName]
	uv.peersMu.RUnlock()
	if s == nil {
		return false
	}
	if len(callUUID) != udpUUIDLen {
		return false
	}
	pkt := make([]byte, 0, udpHdrLen+len(frameData))
	pkt = append(pkt, s.token...)
	pkt = append(pkt, []byte(callUUID)...)
	pkt = append(pkt, frameData...)
	_, err := uv.conn.WriteToUDP(pkt, s.addr)
	if err != nil {
		uv.logger.Printf("federation: UDP send to %s failed: %v", peerName, err)
		return false
	}
	return true
}

// Peers gibt die Liste der peers mit UDP-Adresse zurueck (fuer Broadcast).
func (uv *UDPVoice) Peers() []string {
	if uv == nil {
		return nil
	}
	uv.peersMu.RLock()
	defer uv.peersMu.RUnlock()
	out := make([]string, 0, len(uv.peerSenders))
	for name := range uv.peerSenders {
		out = append(out, name)
	}
	return out
}

func (uv *UDPVoice) readLoop(handler VoiceHandler) {
	buf := make([]byte, 1500)
	unknownTokenCount := 0
	for {
		n, src, err := uv.conn.ReadFromUDP(buf)
		if err != nil {
			if isNetClosed(err) {
				return
			}
			uv.logger.Printf("federation: UDP read error: %v", err)
			continue
		}
		if n < udpHdrLen {
			continue
		}
		token := buf[:udpTokenLen]
		uv.tokensMu.RLock()
		peerName, ok := uv.byToken[hex.EncodeToString(token)]
		knownTokens := len(uv.byToken)
		uv.tokensMu.RUnlock()
		if !ok {
			unknownTokenCount++
			if unknownTokenCount <= 3 || unknownTokenCount%50 == 0 {
				uv.logger.Printf("federation: UDP unknown-token packet from %s (token=%s, known_tokens=%d, drop count=%d)",
					src.String(), hex.EncodeToString(token)[:8], knownTokens, unknownTokenCount)
			}
			continue
		}
		callUUID := string(buf[udpTokenLen : udpTokenLen+udpUUIDLen])
		frameData := make([]byte, n-udpHdrLen)
		copy(frameData, buf[udpHdrLen:n])
		if handler != nil {
			handler(peerName, callUUID, frameData)
		}
	}
}

// Close stoppt den Listener.
func (uv *UDPVoice) Close() {
	if uv == nil || uv.conn == nil {
		return
	}
	_ = uv.conn.Close()
}

// isNetClosed erkennt den "use of closed network connection" Fehler
// (Go-Idiom — kein exported error fuer den Fall).
func isNetClosed(err error) bool {
	return err != nil && (err.Error() == "use of closed network connection" ||
		(err != nil && err.Error() != "" && stringContains(err.Error(), "closed network")))
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
