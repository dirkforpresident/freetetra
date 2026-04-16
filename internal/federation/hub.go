package federation

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
	// OnPeerCallStart is called when a peer starts a group call.
	OnPeerCallStart(peerName string, callUUID string, sourceISSI, destGSSI uint32, priority uint8, service uint16)
	// OnPeerCallEnd is called when a peer ends a group call.
	OnPeerCallEnd(peerName string, callUUID string, cause uint8)
	// OnPeerVoiceFrame is called when a peer sends a voice frame.
	OnPeerVoiceFrame(peerName string, callUUID string, frameData []byte)
	// OnPeerSDSRelay is called when a peer relays an SDS message.
	OnPeerSDSRelay(peerName string, sourceISSI, destISSI uint32, sdsDataHex string)
	// GetLocalSubscribers returns all locally registered ISSIs with their GSSI affiliations.
	GetLocalSubscribers() map[uint32][]uint32
}

// FreeTetra GSSI Schema:
//   1-4:   Local (voice, no services)
//   5-22:  Local (services: radio, echo, etc.)
//   23-90: Federation (shared between servers)
//   91+:   Reserved (future DMR bridge)
const (
	federationGSSIMin uint32 = 23
	federationGSSIMax uint32 = 90
)

// isFederatedGSSI returns true if a GSSI should be shared between servers.
func isFederatedGSSI(gssi uint32) bool {
	return gssi >= federationGSSIMin && gssi <= federationGSSIMax
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

	upgrader websocket.Upgrader
}

// NewHub creates a new federation hub.
func NewHub(serverName, peerKey string, handler CallHandler, logger *log.Logger) *Hub {
	return &Hub{
		serverName:  serverName,
		peerKey:     peerKey,
		handler:     handler,
		logger:      logger,
		peers:       make(map[string]*Peer),
		activeCalls: make(map[string]map[string]bool),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

// Start connects to all configured peers and begins the federation loop.
func (h *Hub) Start(ctx context.Context, peerConfigs []PeerConfig) {
	for _, pc := range peerConfigs {
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

	// Send hello
	peer.SendJSON(&Message{
		Type:    MsgHello,
		Origin:  h.serverName,
		Version: ProtocolVersion,
	})

	// Send full sync
	h.sendFullSync(peer)

	go peer.writeLoop()
	h.readLoop(peer)
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

		peer.SendJSON(&Message{
			Type:    MsgHello,
			Origin:  h.serverName,
			Version: ProtocolVersion,
		})
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

	// Loop prevention
	if msg.Origin == h.serverName {
		return
	}

	switch msg.Type {
	case MsgHello:
		h.logger.Printf("federation: hello from %s (version %d)", msg.Origin, msg.Version)

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

	case MsgSubscriberUpdate:
		switch msg.Action {
		case "register":
			peer.RegisterISSI(msg.ISSI)
			peer.AffiliateISSI(msg.ISSI, msg.GSSIs)
			h.logger.Printf("federation: %s registered ISSI %d (GSSIs=%v)", peer.Name, msg.ISSI, msg.GSSIs)
		case "deregister":
			peer.DeregisterISSI(msg.ISSI)
			h.logger.Printf("federation: %s deregistered ISSI %d", peer.Name, msg.ISSI)
		}
		h.relayToPeers(&msg, peer.Name)

	case MsgAffiliateUpdate:
		switch msg.Action {
		case "affiliate":
			peer.AffiliateISSI(msg.ISSI, msg.GSSIs)
			h.logger.Printf("federation: %s affiliated ISSI %d -> GSSIs %v", peer.Name, msg.ISSI, msg.GSSIs)
		case "deaffiliate":
			peer.DeaffiliateISSI(msg.ISSI, msg.GSSIs)
			h.logger.Printf("federation: %s deaffiliated ISSI %d from GSSIs %v", peer.Name, msg.ISSI, msg.GSSIs)
		}
		h.relayToPeers(&msg, peer.Name)

	case MsgCallStart:
		// Track which peers are involved in this call
		targets := h.findPeersForGSSI(msg.DestGSSI, peer.Name)
		if len(targets) > 0 {
			h.callMu.Lock()
			if h.activeCalls[msg.UUID] == nil {
				h.activeCalls[msg.UUID] = make(map[string]bool)
			}
			for _, t := range targets {
				h.activeCalls[msg.UUID][t.Name] = true
				t.SendJSON(&msg)
			}
			h.callMu.Unlock()
		}
		// Forward to local Brew service
		if h.handler != nil {
			h.handler.OnPeerCallStart(peer.Name, msg.UUID, msg.SourceISSI, msg.DestGSSI, msg.Priority, msg.Service)
		}

	case MsgCallEnd:
		// Forward to peers in this call
		h.callMu.Lock()
		peerNames := h.activeCalls[msg.UUID]
		delete(h.activeCalls, msg.UUID)
		h.callMu.Unlock()

		for name := range peerNames {
			if name != peer.Name {
				if p := h.getPeer(name); p != nil {
					p.SendJSON(&msg)
				}
			}
		}
		// Forward to local Brew service
		if h.handler != nil {
			h.handler.OnPeerCallEnd(peer.Name, msg.UUID, msg.Cause)
		}

	case MsgSDSRelay:
		if h.handler != nil {
			h.handler.OnPeerSDSRelay(peer.Name, msg.SourceISSI, msg.DestISSI, msg.SDSData)
		}
		// If not deliverable locally, relay to other peers
		h.relayToPeers(&msg, peer.Name)
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

	// Forward to other peers in this call
	h.callMu.RLock()
	peerNames := h.activeCalls[callUUID]
	h.callMu.RUnlock()

	for name := range peerNames {
		if name != peer.Name {
			if p := h.getPeer(name); p != nil {
				p.SendBinary(data)
			}
		}
	}
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
	h.broadcastToAllPeers(msg)
}

// BroadcastAffiliate notifies all peers about an affiliation change.
// Only federated GSSIs (23-90) are shared.
func (h *Hub) BroadcastAffiliate(issi uint32, action string, gssis []uint32) {
	// Filter to only federated GSSIs
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

	targets := h.findPeersForGSSI(destGSSI, "")
	if len(targets) > 0 {
		h.callMu.Lock()
		if h.activeCalls[callUUID] == nil {
			h.activeCalls[callUUID] = make(map[string]bool)
		}
		for _, peer := range targets {
			h.activeCalls[callUUID][peer.Name] = true
			peer.SendJSON(msg)
		}
		h.callMu.Unlock()
	}
}

// BroadcastCallEnd notifies peers about a call ending.
func (h *Hub) BroadcastCallEnd(callUUID string, cause uint8) {
	h.callMu.Lock()
	peerNames := h.activeCalls[callUUID]
	delete(h.activeCalls, callUUID)
	h.callMu.Unlock()

	msg := &Message{
		Type:   MsgCallEnd,
		Origin: h.serverName,
		UUID:   callUUID,
		Cause:  cause,
	}
	for name := range peerNames {
		if p := h.getPeer(name); p != nil {
			p.SendJSON(msg)
		}
	}
}

// BroadcastVoiceFrame sends a voice frame to peers involved in a call.
func (h *Hub) BroadcastVoiceFrame(callUUID string, frameData []byte) {
	h.callMu.RLock()
	peerNames := h.activeCalls[callUUID]
	h.callMu.RUnlock()

	if len(peerNames) == 0 {
		return
	}

	// Binary format: callUUID (36 bytes ASCII) + frame payload
	data := append([]byte(callUUID), frameData...)
	for name := range peerNames {
		if p := h.getPeer(name); p != nil {
			p.SendBinary(data)
		}
	}
}

// BroadcastSDS relays an SDS message to peers that have the target ISSI.
func (h *Hub) BroadcastSDS(sourceISSI, destISSI uint32, sdsDataHex string) {
	msg := &Message{
		Type:       MsgSDSRelay,
		Origin:     h.serverName,
		SourceISSI: sourceISSI,
		DestISSI:   destISSI,
		SDSData:    sdsDataHex,
	}

	// Find peer that has the target ISSI
	h.mu.RLock()
	for _, peer := range h.peers {
		if peer.HasISSI(destISSI) {
			peer.SendJSON(msg)
			h.mu.RUnlock()
			return
		}
	}
	h.mu.RUnlock()

	// If no specific peer found, broadcast to all
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

func (h *Hub) registerPeer(peer *Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := peer.Name
	if peer.Direction == "incoming" {
		key = peer.Name + ":in"
	}
	// Close existing peer with same key if any
	if old, ok := h.peers[key]; ok {
		old.Close()
	}
	h.peers[key] = peer
}

func (h *Hub) unregisterPeer(peer *Peer) {
	peer.Cleanup()
	peer.Close()
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, p := range h.peers {
		if p == peer {
			delete(h.peers, key)
			break
		}
	}
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
