package federation

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// PeerConfig is the configuration for a federation peer.
type PeerConfig struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Key  string `json:"key"`
}

// CallHandler is the interface that the Brew service must implement
// to receive federation events.
type CallHandler interface {
	OnPeerCallStart(peerName string, callUUID string, sourceISSI, destGSSI uint32, priority uint8, service uint16)
	OnPeerCallEnd(peerName string, callUUID string, cause uint8)
	OnPeerVoiceFrame(peerName string, callUUID string, frameData []byte)
	OnPeerSDSRelay(peerName string, sourceISSI, destISSI uint32, sdsDataHex string)
	OnPeerPositionSample(peerName string, issi uint32, lat, lon float64, repeater string)
	OnPeerStationUpdate(peerName string, station map[string]any)
	GetLocalSubscribers() map[uint32][]uint32

	// Users DB sync (optional — return empty if not available)
	GetUsersDBInfo() (timestamp string, count int)
	// DownloadUsersDBFrom fetches the users DB from a peer's URL
	DownloadUsersDBFrom(url string) error
}

// FreeTetra GSSI Schema:
//   1-9:    Local — only this cell, never forwarded between servers
//   10-90:  FreeTetra global — shared between all FreeTetra servers
//   91+:    BrandMeister-compatible — shared between FreeTetra servers AND
//           bridged to DMR/BrandMeister on servers where dmrbridge is configured.
//           TG numbers map 1:1 (e.g. TG 262 = DL, TG 2621 = DL Cluster Nord).
const (
	federationGSSIMin uint32 = 10
)

// isFederatedGSSI returns true if a GSSI should be shared between FreeTetra
// servers. TG 91+ is also federated; whether it's additionally bridged to
// BrandMeister depends on per-server dmrbridge config.
func isFederatedGSSI(gssi uint32) bool {
	return gssi >= federationGSSIMin
}

// Hub manages all federation peer connections.
type Hub struct {
	serverName string
	peerKey    string // shared key for incoming peer auth
	handler    CallHandler
	logger     *log.Logger

	mu    sync.RWMutex
	peers map[string]*Peer // peer name -> Peer

	// Active calls routed to peers
	callMu      sync.RWMutex
	activeCalls map[string]map[string]bool // callUUID -> set of peer names

	// Gossip: known peer URLs (discovered via peer exchange)
	knownMu    sync.RWMutex
	knownPeers map[string]string // name -> URL (all peers we've ever heard of)

	// Our own public URL for advertising to peers
	selfURL string

	// Mesh routing: deduplication and TTL
	mesh *MeshRouter

	upgrader websocket.Upgrader

	ctx context.Context

	// UDP-Voice-Plane (optional, nil wenn disabled). Voice-Frames laufen
	// dann ueber UDP statt im TCP-WS-Stream.
	udpVoice *UDPVoice

	// Outbound tokens: pro Peer der token den WIR von ihm in eingehenden
	// UDP-Voice-Packets erwarten — wird beim Hello-Send generiert + an den
	// Peer geschickt. Empfang validiert ueber UDPVoice.byToken.
	udpInTokenMu sync.RWMutex
	udpInTokens  map[string]string // peerName -> hex token (was wir von ihm erwarten)
}

// NewHub creates a new federation hub.
func NewHub(serverName, peerKey, selfURL string, handler CallHandler, logger *log.Logger) *Hub {
	return &Hub{
		serverName:  serverName,
		peerKey:     peerKey,
		selfURL:     selfURL,
		handler:     handler,
		logger:      logger,
		peers:       make(map[string]*Peer),
		activeCalls: make(map[string]map[string]bool),
		knownPeers:  make(map[string]string),
		udpInTokens: make(map[string]string),
		mesh:        newMeshRouter(serverName),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

// EnableUDPVoice initialisiert die UDP-Voice-Plane. udpPort=0 = disabled.
// advertised ist die "host:port"-Adresse die Peers in unserem Hello sehen
// (oeffentliche Adresse).
func (h *Hub) EnableUDPVoice(udpPort int, advertised string) error {
	uv, err := NewUDPVoice(udpPort, advertised, h.logger, func(peerName, callUUID string, frameData []byte) {
		if h.handler != nil {
			h.handler.OnPeerVoiceFrame(peerName, callUUID, frameData)
		}
	})
	if err != nil {
		return err
	}
	h.udpVoice = uv
	return nil
}

// Start connects to all configured peers and begins the federation loop.
func (h *Hub) Start(ctx context.Context, peerConfigs []PeerConfig) {
	h.ctx = ctx

	// Add configured peers to known list
	for _, pc := range peerConfigs {
		h.knownMu.Lock()
		h.knownPeers[pc.Name] = pc.URL
		h.knownMu.Unlock()
		go h.maintainOutgoingPeer(ctx, pc)
	}
}

// HandleIncoming handles an incoming HTTP request for peer connections (/peer/).
func (h *Hub) HandleIncoming(w http.ResponseWriter, r *http.Request) {
	peerName := r.Header.Get("X-Brew-Peer")
	peerKey := r.Header.Get("X-Brew-Key")

	if peerName == "" || peerKey == "" {
		http.Error(w, "missing peer credentials", http.StatusForbidden)
		return
	}
	if peerKey != h.peerKey {
		h.logger.Printf("federation: rejected incoming peer %s (invalid key)", peerName)
		http.Error(w, "invalid peer key", http.StatusForbidden)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Printf("federation: upgrade failed for %s: %v", peerName, err)
		return
	}

	peer := newPeer(peerName, "incoming", conn, h.logger)
	h.registerPeer(peer)
	h.logger.Printf("federation: accepted incoming peer %s", peerName)

	// Send hello (mit UDP-Voice-Handshake falls UDP aktiviert)
	peer.SendJSON(h.buildHello(peerName))

	// Send full sync
	h.sendFullSync(peer)

	go peer.writeLoop()
	h.readLoop(peer)
}

// buildHello konstruiert eine Hello-Message. Wenn UDP-Voice aktiv ist,
// nimmt es einen stabilen pro-Peer-Token + unsere UDP-Adresse mit auf,
// sodass der Peer voice-frames per UDP zu uns schicken kann.
//
// WICHTIG: Token muss STABIL pro Peer-Name bleiben (nicht bei jedem
// Hello neu generieren). Sonst Race: outgoing + incoming Connection
// gleichzeitig schreiben verschiedene Tokens, Sender und Receiver sind
// out-of-sync → Pakete werden silent verworfen.
func (h *Hub) buildHello(peerName string) *Message {
	msg := &Message{
		Type:    MsgHello,
		Origin:  h.serverName,
		Version: ProtocolVersion,
	}
	if h.udpVoice != nil && h.udpVoice.Advertised() != "" {
		h.udpInTokenMu.Lock()
		token := h.udpInTokens[peerName]
		if token == "" {
			token = NewToken()
			h.udpInTokens[peerName] = token
		}
		h.udpInTokenMu.Unlock()
		msg.UDPAddr = h.udpVoice.Advertised()
		msg.UDPToken = token
	}
	return msg
}

// maintainOutgoingPeer keeps a persistent connection to an outgoing peer.
func (h *Hub) maintainOutgoingPeer(ctx context.Context, pc PeerConfig) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		h.logger.Printf("federation: connecting to peer %s at %s", pc.Name, pc.URL)

		header := http.Header{}
		header.Set("X-Brew-Peer", h.serverName)
		header.Set("X-Brew-Key", pc.Key)

		conn, _, err := websocket.DefaultDialer.Dial(pc.URL, header)
		if err != nil {
			h.logger.Printf("federation: failed to connect to %s: %v", pc.Name, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
				continue
			}
		}

		peer := newPeer(pc.Name, "outgoing", conn, h.logger)
		h.registerPeer(peer)
		h.logger.Printf("federation: connected to %s", pc.Name)

		peer.SendJSON(h.buildHello(pc.Name))
		h.sendFullSync(peer)

		go peer.writeLoop()
		h.readLoop(peer)

		// Cleanup and reconnect
		h.unregisterPeer(peer)
		h.logger.Printf("federation: reconnecting to %s in 10s...", pc.Name)

		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}

// readLoop reads messages from a peer.
func (h *Hub) readLoop(peer *Peer) {
	defer func() {
		h.unregisterPeer(peer)
	}()

	for {
		msgType, data, err := peer.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				h.logger.Printf("federation: %s read error: %v", peer.Name, err)
			} else {
				h.logger.Printf("federation: %s disconnected", peer.Name)
			}
			return
		}

		switch msgType {
		case websocket.TextMessage:
			h.handleJSONMessage(peer, data)
		case websocket.BinaryMessage:
			h.handleBinaryMessage(peer, data)
		}
	}
}

// handleJSONMessage processes a JSON federation message.
func (h *Hub) handleJSONMessage(peer *Peer, data []byte) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		h.logger.Printf("federation: invalid JSON from %s: %v", peer.Name, err)
		return
	}

	// Mesh routing: check if we should process this message
	switch msg.Type {
	case MsgHello, MsgSyncRequest, MsgSyncResponse, MsgPeerExchange:
		// Control messages: simple origin check, no mesh routing
		if msg.Origin == h.serverName {
			return
		}
	default:
		// Data messages: full mesh dedup/TTL/loop check
		if !h.mesh.ShouldProcess(&msg) {
			return
		}
	}

	switch msg.Type {
	case MsgHello:
		h.logger.Printf("federation: hello from %s (version %d)", msg.Origin, msg.Version)
		// Bootstrap peers are named from config (e.g. "peer-0"); after Hello
		// the remote's real name becomes known. Rename + re-register so the
		// gossip-based dedup (by name) doesn't create a duplicate connection
		// to the same remote under its real identity.
		if msg.Origin != "" && msg.Origin != peer.Name {
			old := peer.Name
			h.renamePeer(peer, msg.Origin)
			h.logger.Printf("federation: renamed peer %s -> %s", old, msg.Origin)
		}

		// UDP-Voice-Handshake: wenn der Peer eine UDP-Adresse + Token sendet,
		// koennen wir Voice-Frames per UDP zu ihm schicken. Plus wir kennen
		// jetzt unser eigenes outbound-token fuer diesen Peer (= was wir
		// erwarten wenn er an uns sendet) — das hatten wir im buildHello
		// generiert und gespeichert.
		if h.udpVoice != nil && msg.UDPAddr != "" && msg.UDPToken != "" {
			h.udpInTokenMu.RLock()
			myInToken := h.udpInTokens[peer.Name]
			h.udpInTokenMu.RUnlock()
			if myInToken == "" {
				// Reconnect zwischen incoming/outgoing — generieren neu.
				myInToken = NewToken()
				h.udpInTokenMu.Lock()
				h.udpInTokens[peer.Name] = myInToken
				h.udpInTokenMu.Unlock()
			}
			// Wir registrieren: zum Senden an peer → msg.UDPAddr + msg.UDPToken.
			// Zum Empfangen von peer → myInToken (was wir ihm via unser Hello
			// sagen wird).
			h.udpVoice.RegisterPeer(peer.Name, msg.UDPAddr, msg.UDPToken, myInToken)
		}

		h.sendPeerExchange(peer)
		h.sendUsersDBOffer(peer)

	case MsgSyncRequest:
		h.sendFullSync(peer)

	case MsgSyncResponse:
		for issiStr, info := range msg.Subscribers {
			var issi uint32
			fmt.Sscanf(issiStr, "%d", &issi)
			peer.RegisterISSI(issi)
			peer.AffiliateISSI(issi, info.GSSIs)
		}
		h.logger.Printf("federation: synced %d subscribers from %s", len(msg.Subscribers), peer.Name)

	case MsgUsersDBOffer:
		h.handleUsersDBOffer(peer, &msg)

	case MsgUsersDBRequest:
		h.sendUsersDBOffer(peer)

	case MsgPeerExchange:
		newPeers := 0
		for _, gp := range msg.Peers {
			if gp.Name == h.serverName || gp.URL == "" {
				continue
			}
			if h.tryAddDiscoveredPeer(gp.Name, gp.URL) {
				newPeers++
			}
		}
		if newPeers > 0 {
			h.logger.Printf("federation: discovered %d new peer(s) via %s", newPeers, peer.Name)
		}

	case MsgSubscriberUpdate:
		switch msg.Action {
		case "register":
			peer.RegisterISSI(msg.ISSI)
			peer.AffiliateISSI(msg.ISSI, msg.GSSIs)
			h.logger.Printf("federation: %s registered ISSI %d (GSSIs=%v) [ttl=%d path=%v]", peer.Name, msg.ISSI, msg.GSSIs, msg.TTL, msg.Path)
		case "deregister":
			peer.DeregisterISSI(msg.ISSI)
			h.logger.Printf("federation: %s deregistered ISSI %d [ttl=%d]", peer.Name, msg.ISSI, msg.TTL)
		}
		// Mesh relay to all other peers
		if h.mesh.ShouldRelay(&msg) {
			relay := h.mesh.PrepareRelay(&msg)
			h.relayToPeers(relay, peer.Name)
		}

	case MsgAffiliateUpdate:
		switch msg.Action {
		case "affiliate":
			peer.AffiliateISSI(msg.ISSI, msg.GSSIs)
			h.logger.Printf("federation: %s affiliated ISSI %d -> GSSIs %v [ttl=%d]", peer.Name, msg.ISSI, msg.GSSIs, msg.TTL)
		case "deaffiliate":
			peer.DeaffiliateISSI(msg.ISSI, msg.GSSIs)
			h.logger.Printf("federation: %s deaffiliated ISSI %d from GSSIs %v [ttl=%d]", peer.Name, msg.ISSI, msg.GSSIs, msg.TTL)
		}
		if h.mesh.ShouldRelay(&msg) {
			relay := h.mesh.PrepareRelay(&msg)
			h.relayToPeers(relay, peer.Name)
		}

	case MsgCallStart:
		// Forward to local Brew service
		if h.handler != nil {
			h.handler.OnPeerCallStart(peer.Name, msg.UUID, msg.SourceISSI, msg.DestGSSI, msg.Priority, msg.Service)
		}
		// Track call and relay to all other peers (mesh-wide)
		h.callMu.Lock()
		if h.activeCalls[msg.UUID] == nil {
			h.activeCalls[msg.UUID] = make(map[string]bool)
		}
		h.callMu.Unlock()

		if h.mesh.ShouldRelay(&msg) {
			relay := h.mesh.PrepareRelay(&msg)
			h.mu.RLock()
			for _, p := range h.peers {
				if p.Name != peer.Name && !IsInPath(&msg, p.Name) {
					p.SendJSON(relay)
					h.callMu.Lock()
					h.activeCalls[msg.UUID][p.Name] = true
					h.callMu.Unlock()
				}
			}
			h.mu.RUnlock()
		}

	case MsgCallEnd:
		// Forward to local Brew service
		if h.handler != nil {
			h.handler.OnPeerCallEnd(peer.Name, msg.UUID, msg.Cause)
		}
		// Relay to all other peers
		h.callMu.Lock()
		delete(h.activeCalls, msg.UUID)
		h.callMu.Unlock()

		if h.mesh.ShouldRelay(&msg) {
			relay := h.mesh.PrepareRelay(&msg)
			h.mu.RLock()
			for _, p := range h.peers {
				if p.Name != peer.Name && !IsInPath(&msg, p.Name) {
					p.SendJSON(relay)
				}
			}
			h.mu.RUnlock()
		}

	case MsgSDSRelay:
		if h.handler != nil {
			h.handler.OnPeerSDSRelay(peer.Name, msg.SourceISSI, msg.DestISSI, msg.SDSData)
		}
		if h.mesh.ShouldRelay(&msg) {
			relay := h.mesh.PrepareRelay(&msg)
			h.relayToPeers(relay, peer.Name)
		}

	case MsgPositionSample:
		if h.handler != nil {
			h.handler.OnPeerPositionSample(peer.Name, msg.ISSI, msg.Lat, msg.Lon, msg.Repeater)
		}
		if h.mesh.ShouldRelay(&msg) {
			relay := h.mesh.PrepareRelay(&msg)
			h.relayToPeers(relay, peer.Name)
		}

	case MsgStationUpdate:
		if h.handler != nil {
			h.handler.OnPeerStationUpdate(peer.Name, msg.Station)
		}
		if h.mesh.ShouldRelay(&msg) {
			relay := h.mesh.PrepareRelay(&msg)
			h.relayToPeers(relay, peer.Name)
		}
	}
}

// handleBinaryMessage processes binary federation data (voice frames).
// Format: callUUID (36 bytes ASCII) + frame payload
func (h *Hub) handleBinaryMessage(peer *Peer, data []byte) {
	if len(data) < 36 {
		return
	}

	callUUID := string(data[:36])
	frameData := data[36:]

	// Forward to local Brew service
	if h.handler != nil {
		h.handler.OnPeerVoiceFrame(peer.Name, callUUID, frameData)
	}

	// Mesh relay: forward to ALL other connected peers (not just call participants)
	// This ensures voice reaches servers that are only indirectly connected
	h.mu.RLock()
	for _, p := range h.peers {
		if p.Name != peer.Name {
			p.SendBinary(data)
		}
	}
	h.mu.RUnlock()
}

// ==================================================================
// Broadcasting to peers (called by the Brew service)
// ==================================================================

// BroadcastSubscriber notifies all peers about a subscriber change.
func (h *Hub) BroadcastSubscriber(issi uint32, action string, gssis []uint32) {
	msg := &Message{
		Type:   MsgSubscriberUpdate,
		Origin: h.serverName,
		ISSI:   issi,
		Action: action,
		GSSIs:  gssis,
	}
	h.mesh.PrepareOutgoing(msg)
	h.broadcastToAllPeers(msg)
}

// BroadcastAffiliate notifies all peers about an affiliation change.
// Only federated GSSIs (23-90) are shared.
func (h *Hub) BroadcastAffiliate(issi uint32, action string, gssis []uint32) {
	fedGSSIs := make([]uint32, 0, len(gssis))
	for _, g := range gssis {
		if isFederatedGSSI(g) {
			fedGSSIs = append(fedGSSIs, g)
		}
	}
	if len(fedGSSIs) == 0 {
		return
	}
	msg := &Message{
		Type:   MsgAffiliateUpdate,
		Origin: h.serverName,
		ISSI:   issi,
		Action: action,
		GSSIs:  fedGSSIs,
	}
	h.mesh.PrepareOutgoing(msg)
	h.broadcastToAllPeers(msg)
}

// BroadcastCallStart notifies peers that have subscribers on the target GSSI.
// Only federated GSSIs (23-90) are shared between servers.
func (h *Hub) BroadcastCallStart(callUUID string, sourceISSI, destGSSI uint32, priority uint8, service uint16) {
	if !isFederatedGSSI(destGSSI) {
		return
	}
	msg := &Message{
		Type:       MsgCallStart,
		Origin:     h.serverName,
		UUID:       callUUID,
		SourceISSI: sourceISSI,
		DestGSSI:   destGSSI,
		Priority:   priority,
		Service:    service,
	}
	h.mesh.PrepareOutgoing(msg)

	// Broadcast to ALL peers (mesh relay — they forward further)
	h.callMu.Lock()
	if h.activeCalls[callUUID] == nil {
		h.activeCalls[callUUID] = make(map[string]bool)
	}
	h.callMu.Unlock()

	h.mu.RLock()
	for _, peer := range h.peers {
		peer.SendJSON(msg)
		h.callMu.Lock()
		h.activeCalls[callUUID][peer.Name] = true
		h.callMu.Unlock()
	}
	h.mu.RUnlock()
}

// BroadcastStation sendet einen BlueStation-Heartbeat an alle Peers
// (Stations-Federation). Damit zeigen alle Server die gleiche Station-Liste.
func (h *Hub) BroadcastStation(station map[string]any) {
	msg := &Message{
		Type:    MsgStationUpdate,
		Origin:  h.serverName,
		Station: station,
	}
	h.mesh.PrepareOutgoing(msg)
	h.broadcastToAllPeers(msg)
}

// BroadcastPositionSample sendet einen empfangenen Position-Sample an alle Peers
// (Coverage-Federation). Mesh-Router dedupliziert + relayed.
func (h *Hub) BroadcastPositionSample(issi uint32, lat, lon float64, repeater string) {
	msg := &Message{
		Type:     MsgPositionSample,
		Origin:   h.serverName,
		ISSI:     issi,
		Lat:      lat,
		Lon:      lon,
		Repeater: repeater,
	}
	h.mesh.PrepareOutgoing(msg)
	h.broadcastToAllPeers(msg)
}

// BroadcastCallEnd notifies all peers about a call ending.
func (h *Hub) BroadcastCallEnd(callUUID string, cause uint8) {
	h.callMu.Lock()
	delete(h.activeCalls, callUUID)
	h.callMu.Unlock()

	msg := &Message{
		Type:   MsgCallEnd,
		Origin: h.serverName,
		UUID:   callUUID,
		Cause:  cause,
	}
	h.mesh.PrepareOutgoing(msg)
	h.broadcastToAllPeers(msg)
}

// BroadcastVoiceFrame sends a voice frame to all connected peers.
// Bevorzugt UDP (vermeidet TCP-head-of-line-blocking + Audio-Schleppe);
// fuer Peers ohne UDP-Setup fallback auf binary WebSocket.
func (h *Hub) BroadcastVoiceFrame(callUUID string, frameData []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, peer := range h.peers {
		// Versuche UDP zuerst — keine Schleppe, niedrige Latenz.
		if h.udpVoice != nil && h.udpVoice.SendVoice(peer.Name, callUUID, frameData) {
			continue
		}
		// Fallback: binary WS (TCP).
		data := append([]byte(callUUID), frameData...)
		peer.SendBinary(data)
	}
}

// BroadcastSDS relays an SDS message through the mesh.
func (h *Hub) BroadcastSDS(sourceISSI, destISSI uint32, sdsDataHex string) {
	msg := &Message{
		Type:       MsgSDSRelay,
		Origin:     h.serverName,
		SourceISSI: sourceISSI,
		DestISSI:   destISSI,
		SDSData:    sdsDataHex,
	}
	h.mesh.PrepareOutgoing(msg)
	h.broadcastToAllPeers(msg)
}

// FindPeerForISSI returns the peer that has the given ISSI, or nil.
func (h *Hub) FindPeerForISSI(issi uint32) *Peer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, peer := range h.peers {
		if peer.HasISSI(issi) {
			return peer
		}
	}
	return nil
}

// HasPeersForGSSI returns true if any peer has subscribers on the GSSI.
func (h *Hub) HasPeersForGSSI(gssi uint32) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, peer := range h.peers {
		if peer.HasSubscribersOnGSSI(gssi) {
			return true
		}
	}
	return false
}

// PeerCount returns the number of connected peers.
func (h *Hub) PeerCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.peers)
}

// PeerSnapshots returns info about all connected peers.
func (h *Hub) PeerSnapshots() []PeerSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]PeerSnapshot, 0, len(h.peers))
	for _, p := range h.peers {
		out = append(out, PeerSnapshot{
			Name:      p.Name,
			Direction: p.Direction,
			ISSIs:     p.ISSIs(),
			GSSIs:     p.GSSIs(),
		})
	}
	return out
}

// PeerSnapshot is a read-only snapshot of a peer's state.
type PeerSnapshot struct {
	Name      string              `json:"name"`
	Direction string              `json:"direction"`
	ISSIs     []uint32            `json:"issis"`
	GSSIs     map[uint32][]uint32 `json:"gssis"`
}

// ==================================================================
// Internal helpers
// ==================================================================

// sendUsersDBOffer tells the peer when our users.txt was last updated.
// The peer can decide to download ours if it's newer than theirs.
func (h *Hub) sendUsersDBOffer(peer *Peer) {
	if h.handler == nil || h.selfURL == "" {
		return
	}
	ts, count := h.handler.GetUsersDBInfo()
	if ts == "" || count == 0 {
		return
	}
	// Convert wss://host/peer/ → https://host/api/users.txt
	baseURL := h.selfURL
	baseURL = strings.Replace(baseURL, "wss://", "https://", 1)
	baseURL = strings.Replace(baseURL, "ws://", "http://", 1)
	baseURL = strings.TrimSuffix(baseURL, "/peer/")
	baseURL = strings.TrimSuffix(baseURL, "/peer")
	dbURL := baseURL + "/api/users.txt"

	peer.SendJSON(&Message{
		Type:             MsgUsersDBOffer,
		Origin:           h.serverName,
		UsersDBTimestamp: ts,
		UsersDBURL:       dbURL,
		UsersDBCount:     count,
	})
}

// handleUsersDBOffer processes an offer from a peer.
// Downloads if peer's DB is newer than ours.
func (h *Hub) handleUsersDBOffer(peer *Peer, msg *Message) {
	if h.handler == nil || msg.UsersDBURL == "" {
		return
	}
	ourTS, _ := h.handler.GetUsersDBInfo()

	// If we have no DB or theirs is newer, download
	if ourTS == "" || msg.UsersDBTimestamp > ourTS {
		h.logger.Printf("federation: downloading users DB from %s (%d users, ts=%s)",
			peer.Name, msg.UsersDBCount, msg.UsersDBTimestamp)
		if err := h.handler.DownloadUsersDBFrom(msg.UsersDBURL); err != nil {
			h.logger.Printf("federation: users DB download failed: %v", err)
		} else {
			h.logger.Printf("federation: users DB updated from %s", peer.Name)
		}
	}
}

// sendPeerExchange sends our list of known peers to a peer.
func (h *Hub) sendPeerExchange(peer *Peer) {
	h.knownMu.RLock()
	peers := make([]GossipPeer, 0, len(h.knownPeers)+1)
	// Include ourselves so the other side knows our URL
	if h.selfURL != "" {
		peers = append(peers, GossipPeer{Name: h.serverName, URL: h.selfURL})
	}
	for name, url := range h.knownPeers {
		if name != peer.Name { // Don't tell a peer about itself
			peers = append(peers, GossipPeer{Name: name, URL: url})
		}
	}
	h.knownMu.RUnlock()

	if len(peers) > 0 {
		peer.SendJSON(&Message{
			Type:   MsgPeerExchange,
			Origin: h.serverName,
			Peers:  peers,
		})
		h.logger.Printf("federation: sent %d known peer(s) to %s", len(peers), peer.Name)
	}
}

// tryAddDiscoveredPeer adds a newly discovered peer and connects to it.
// Returns true if the peer was new.
func (h *Hub) tryAddDiscoveredPeer(name, url string) bool {
	// Self-Check: niemals zu sich selbst connecten (sonst Geister-Peer
	// "HH-Cluster incoming" auf eigenem Server).
	if name == h.serverName || url == h.selfURL {
		return false
	}

	h.knownMu.Lock()
	// Dedup by name OR by URL — a bootstrap peer (e.g. "peer-0") may already
	// point at the same URL under a different label.
	if _, exists := h.knownPeers[name]; exists {
		h.knownMu.Unlock()
		return false
	}
	for _, existingURL := range h.knownPeers {
		if existingURL == url {
			h.knownMu.Unlock()
			return false
		}
	}
	h.knownPeers[name] = url
	h.knownMu.Unlock()

	// Check if already connected (by name)
	h.mu.RLock()
	alreadyConnected := false
	for _, p := range h.peers {
		if p.Name == name {
			alreadyConnected = true
			break
		}
	}
	h.mu.RUnlock()

	if alreadyConnected {
		return false
	}

	h.logger.Printf("federation: auto-connecting to discovered peer %s at %s", name, url)
	go h.maintainOutgoingPeer(h.ctx, PeerConfig{
		Name: name,
		URL:  url,
		Key:  h.peerKey,
	})
	return true
}

func (h *Hub) registerPeer(peer *Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := peerKey(peer.Name, peer.Direction)
	if old, ok := h.peers[key]; ok {
		old.Close()
	}
	h.peers[key] = peer
}

// renamePeer updates a peer's Name and moves it to the correct map slot.
// Used when a bootstrap-configured name differs from the Hello-reported Origin.
func (h *Hub) renamePeer(peer *Peer, newName string) {
	h.mu.Lock()
	oldName := peer.Name
	oldKey := peerKey(oldName, peer.Direction)
	newKey := peerKey(newName, peer.Direction)
	if oldKey != newKey {
		if existing, ok := h.peers[newKey]; ok && existing != peer {
			existing.Close()
		}
		delete(h.peers, oldKey)
		peer.Name = newName
		h.peers[newKey] = peer
	}
	h.mu.Unlock()

	// Also relabel in the known-peers map so future gossip dedup works.
	h.knownMu.Lock()
	if url, ok := h.knownPeers[oldName]; ok {
		delete(h.knownPeers, oldName)
		if _, exists := h.knownPeers[newName]; !exists {
			h.knownPeers[newName] = url
		}
	}
	h.knownMu.Unlock()
}

func peerKey(name, direction string) string {
	if direction == "incoming" {
		return name + ":in"
	}
	return name
}

func (h *Hub) unregisterPeer(peer *Peer) {
	peer.Cleanup()
	peer.Close()
	h.mu.Lock()
	for key, p := range h.peers {
		if p == peer {
			delete(h.peers, key)
			break
		}
	}
	h.mu.Unlock()
	// UDP-Voice-Plane: Peer aus Token-Maps entfernen.
	if h.udpVoice != nil {
		h.udpVoice.UnregisterPeer(peer.Name)
	}
	h.udpInTokenMu.Lock()
	delete(h.udpInTokens, peer.Name)
	h.udpInTokenMu.Unlock()
}

func (h *Hub) getPeer(name string) *Peer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if p, ok := h.peers[name]; ok {
		return p
	}
	// Try with :in suffix
	return h.peers[name+":in"]
}

func (h *Hub) findPeersForGSSI(gssi uint32, excludeName string) []*Peer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var targets []*Peer
	seen := make(map[string]bool)
	for _, peer := range h.peers {
		if peer.Name == excludeName || seen[peer.Name] {
			continue
		}
		if peer.HasSubscribersOnGSSI(gssi) {
			targets = append(targets, peer)
			seen[peer.Name] = true
		}
	}
	return targets
}

func (h *Hub) sendFullSync(peer *Peer) {
	if h.handler == nil {
		return
	}
	localSubs := h.handler.GetLocalSubscribers()
	subscribers := make(map[string]SyncSubscriber, len(localSubs))
	for issi, gssis := range localSubs {
		subscribers[fmt.Sprintf("%d", issi)] = SyncSubscriber{GSSIs: gssis}
	}
	peer.SendJSON(&Message{
		Type:        MsgSyncResponse,
		Origin:      h.serverName,
		Subscribers: subscribers,
	})
	h.logger.Printf("federation: sent sync to %s (%d subscribers)", peer.Name, len(subscribers))
}

func (h *Hub) broadcastToAllPeers(msg *Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, peer := range h.peers {
		peer.SendJSON(msg)
	}
}

func (h *Hub) relayToPeers(msg *Message, excludeName string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, peer := range h.peers {
		if peer.Name != excludeName {
			peer.SendJSON(msg)
		}
	}
}
