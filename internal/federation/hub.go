package federation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	federationv2pb "github.com/freetetra/server/internal/federation/proto/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	federationSubpathHeader = "x-brew-subpath"

	// ProtocolVersion advertised in the Hello handshake.
	ProtocolVersion = 2
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
//
//	1-9:    Local — only this cell, never forwarded between servers
//	10-90:  FreeTetra global — shared between all FreeTetra servers
//	91+:    BrandMeister-compatible — shared between FreeTetra servers AND
//	        bridged to DMR/BrandMeister on servers where dmrbridge is configured.
//	        TG numbers map 1:1 (e.g. TG 262 = DL, TG 2621 = DL Cluster Nord).
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
	federationv2pb.UnimplementedFederationTransportV2Server

	serverName string
	peerKey    string // shared key for incoming peer auth
	rpcListen  string
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

	ctx context.Context

	// UDP-Voice-Plane (optional, nil wenn disabled). Voice-Frames laufen
	// dann ueber UDP statt im TCP-WS-Stream.
	udpVoice *UDPVoice

	// Outbound tokens: pro Peer der token den WIR von ihm in eingehenden
	// UDP-Voice-Packets erwarten — wird beim Hello-Send generiert + an den
	// Peer geschickt. Empfang validiert ueber UDPVoice.byToken.
	udpInTokenMu sync.RWMutex
	udpInTokens  map[string]string // peerName -> hex token (was wir von ihm erwarten)

	serveStandaloneRPC bool
}

// NewHub creates a new federation hub.
func NewHub(serverName, peerKey, selfURL, rpcListen string, handler CallHandler, logger *log.Logger) *Hub {
	if rpcListen == "" {
		rpcListen = ":8092"
	}
	return &Hub{
		serverName:         serverName,
		peerKey:            peerKey,
		selfURL:            selfURL,
		rpcListen:          rpcListen,
		handler:            handler,
		logger:             logger,
		peers:              make(map[string]*Peer),
		activeCalls:        make(map[string]map[string]bool),
		knownPeers:         make(map[string]string),
		udpInTokens:        make(map[string]string),
		mesh:               newMeshRouter(serverName),
		serveStandaloneRPC: true,
	}
}

// UseSharedPortRPC disables the dedicated RPC listener. Incoming federation
// gRPC traffic must then be served via NewGRPCServer on the HTTP listener.
func (h *Hub) UseSharedPortRPC() {
	h.serveStandaloneRPC = false
}

// NewGRPCServer returns a gRPC server with the federation service registered.
func (h *Hub) NewGRPCServer() *grpc.Server {
	server := grpc.NewServer()
	federationv2pb.RegisterFederationTransportV2Server(server, h)
	return server
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
	if h.serveStandaloneRPC {
		go h.serveRPC(ctx)
	}

	// Add configured peers to known list
	for _, pc := range peerConfigs {
		h.knownMu.Lock()
		h.knownPeers[pc.Name] = pc.URL
		h.knownMu.Unlock()
		go h.maintainOutgoingPeer(ctx, pc)
	}
}

func (h *Hub) serveRPC(ctx context.Context) {
	lis, err := net.Listen("tcp", h.rpcListen)
	if err != nil {
		h.logger.Printf("federation: RPC listen failed on %s: %v", h.rpcListen, err)
		return
	}
	server := grpc.NewServer()
	federationv2pb.RegisterFederationTransportV2Server(server, h)
	h.logger.Printf("federation: protobuf RPC listening on %s", h.rpcListen)

	go func() {
		<-ctx.Done()
		server.GracefulStop()
		_ = lis.Close()
	}()

	if err := server.Serve(lis); err != nil && ctx.Err() == nil {
		h.logger.Printf("federation: RPC server stopped: %v", err)
	}
}

// buildHello constructs the Hello control message advertised to peers.
// UDP voice advertisement is preserved here for as long as the UDP plane
// is wired (removed wholesale in Task 6). The token must remain STABLE
// per peer name across reconnects — see Hub.udpInTokens.
func (h *Hub) buildHello(peerName string) *federationv2pb.Control {
	hello := &federationv2pb.Hello{}
	if h.udpVoice != nil && h.udpVoice.Advertised() != "" {
		h.udpInTokenMu.Lock()
		token := h.udpInTokens[peerName]
		if token == "" {
			token = NewToken()
			h.udpInTokens[peerName] = token
		}
		h.udpInTokenMu.Unlock()
		hello.UdpAddr = h.udpVoice.Advertised()
		hello.UdpToken = token
	}
	return &federationv2pb.Control{
		Origin:          h.serverName,
		ProtocolVersion: ProtocolVersion,
		Payload: &federationv2pb.Control_Hello{
			Hello: hello,
		},
	}
}

// maintainOutgoingPeer keeps a persistent connection to an outgoing peer.
// Reconnects with exponential backoff: 10s → 20s → 40s → … capped at 15min.
// Backoff resets after a stream is successfully established.
func (h *Hub) maintainOutgoingPeer(ctx context.Context, pc PeerConfig) {
	const (
		baseDelay = 10 * time.Second
		maxDelay  = 15 * time.Minute
	)
	delay := baseDelay
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		h.logger.Printf("federation: connecting to peer %s at %s", pc.Name, pc.URL)
		target, err := normalizeRPCTarget(pc.URL)
		if err != nil {
			// Config-level error — no amount of retrying fixes a malformed URL.
			h.logger.Printf("federation: invalid peer target %s: %v", pc.URL, err)
			return
		}

		conn, err := grpc.NewClient(
			target,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			h.logger.Printf("federation: failed to connect to %s: %v (retry in %s)", pc.Name, err, delay)
			if !waitBackoff(ctx, &delay, baseDelay, maxDelay) {
				return
			}
			continue
		}

		callCtx, cancel := context.WithCancel(ctx)
		md := metadata.New(map[string]string{
			"x-brew-peer":           h.serverName,
			"x-brew-key":            pc.Key,
			federationSubpathHeader: federationSubpathForKey(pc.Key),
		})
		stream, err := federationv2pb.NewFederationTransportV2Client(conn).Connect(metadata.NewOutgoingContext(callCtx, md))
		if err != nil {
			cancel()
			_ = conn.Close()
			if isIncompatibleGRPCEndpoint(err) {
				h.logger.Printf("federation: peer %s is not a compatible gRPC endpoint (likely reverse-proxy not h2c): %v (retry in %s)", pc.Name, err, delay)
			} else {
				h.logger.Printf("federation: failed to open stream to %s: %v (retry in %s)", pc.Name, err, delay)
			}
			if !waitBackoff(ctx, &delay, baseDelay, maxDelay) {
				return
			}
			continue
		}

		peer := newPeer(pc.Name, "outgoing", stream, cancel, h.logger)
		h.registerPeer(peer)
		h.logger.Printf("federation: connected to %s", pc.Name)
		// A successful stream means the endpoint is healthy — reset backoff.
		delay = baseDelay

		_ = peer.SendControl(h.buildHello(pc.Name))
		h.sendFullSync(peer)

		go peer.writeLoop()
		h.readLoop(peer)
		_ = conn.Close()

		// Cleanup and reconnect
		h.unregisterPeer(peer)
		h.logger.Printf("federation: reconnecting to %s in %s...", pc.Name, delay)

		if !waitBackoff(ctx, &delay, baseDelay, maxDelay) {
			return
		}
	}
}

// waitBackoff sleeps for *delay, then doubles it up to max. Returns false if
// the context was canceled during the sleep. delay is mutated in place so the
// caller can reset it on success.
func waitBackoff(ctx context.Context, delay *time.Duration, base, max time.Duration) bool {
	current := *delay
	if current < base {
		current = base
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(current):
	}
	next := current * 2
	if next > max {
		next = max
	}
	*delay = next
	return true
}

// readLoop reads messages from a peer.
func (h *Hub) readLoop(peer *Peer) {
	defer func() {
		h.unregisterPeer(peer)
	}()

	for {
		frame, err := peer.stream.Recv()
		if err != nil {
			if err != io.EOF {
				h.logger.Printf("federation: %s read error: %v", peer.Name, err)
			} else {
				h.logger.Printf("federation: %s disconnected", peer.Name)
			}
			return
		}

		if ctrl := frame.GetControl(); ctrl != nil {
			h.handleControlMessage(peer, ctrl)
			continue
		}
		if vf := frame.GetVoiceFrame(); vf != nil {
			h.handleVoiceFrame(peer, vf.GetCallUuid(), vf.GetFrameData())
		}
	}
}

// Connect handles an incoming protobuf RPC stream from another federation peer.
func (h *Hub) Connect(stream grpc.BidiStreamingServer[federationv2pb.StreamFrame, federationv2pb.StreamFrame]) error {
	md, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return fmt.Errorf("missing metadata")
	}
	peerName := firstMD(md, "x-brew-peer")
	peerKey := firstMD(md, "x-brew-key")
	peerSubpath := firstMD(md, federationSubpathHeader)
	if peerName == "" || peerKey == "" {
		return fmt.Errorf("missing peer credentials")
	}
	if peerKey != h.peerKey {
		h.logger.Printf("federation: rejected incoming peer %s (invalid key)", peerName)
		return fmt.Errorf("invalid peer key")
	}
	expectedSubpath := federationSubpathForKey(h.peerKey)
	if peerSubpath == "" || peerSubpath != expectedSubpath {
		h.logger.Printf("federation: rejected incoming peer %s (invalid federation subpath)", peerName)
		return fmt.Errorf("invalid federation subpath")
	}

	peer := newPeer(peerName, "incoming", stream, nil, h.logger)
	h.registerPeer(peer)
	h.logger.Printf("federation: accepted incoming peer %s", peerName)

	_ = peer.SendControl(h.buildHello(peerName))
	h.sendFullSync(peer)

	go peer.writeLoop()
	h.readLoop(peer)
	return nil
}

func (h *Hub) handleControlMessage(peer *Peer, ctrl *federationv2pb.Control) {
	if ctrl == nil {
		return
	}

	// Mesh routing gate — preserves the exact set of message types treated
	// as control-plane (origin-only check) by the legacy handleJSONMessage.
	// NOTE: UsersDbOffer / UsersDbRequest fall into the default (data-plane)
	// branch here, matching pre-refactor behavior. The sender does not call
	// PrepareOutgoing on those messages, so TTL stays 0 and ShouldProcess
	// drops them. That is a pre-existing bug in users-DB federation, not a
	// regression introduced here; out of scope for this refactor.
	switch ctrl.GetPayload().(type) {
	case *federationv2pb.Control_Hello,
		*federationv2pb.Control_SyncRequest,
		*federationv2pb.Control_SyncResponse,
		*federationv2pb.Control_PeerExchange:
		if ctrl.GetOrigin() == h.serverName {
			return
		}
	default:
		if !h.mesh.ShouldProcess(ctrl) {
			return
		}
	}

	switch p := ctrl.GetPayload().(type) {
	case *federationv2pb.Control_Hello:
		h.handleHello(peer, ctrl, p.Hello)
	case *federationv2pb.Control_SyncRequest:
		h.sendFullSync(peer)
	case *federationv2pb.Control_SyncResponse:
		h.handleSyncResponse(peer, p.SyncResponse)
	case *federationv2pb.Control_UsersDbOffer:
		h.handleUsersDBOffer(peer, p.UsersDbOffer)
	case *federationv2pb.Control_UsersDbRequest:
		h.sendUsersDBOffer(peer)
	case *federationv2pb.Control_PeerExchange:
		h.handlePeerExchange(peer, p.PeerExchange)
	case *federationv2pb.Control_SubscriberUpdate:
		h.handleSubscriberUpdate(peer, ctrl, p.SubscriberUpdate)
	case *federationv2pb.Control_AffiliateUpdate:
		h.handleAffiliateUpdate(peer, ctrl, p.AffiliateUpdate)
	case *federationv2pb.Control_CallStart:
		h.handleCallStart(peer, ctrl, p.CallStart)
	case *federationv2pb.Control_CallEnd:
		h.handleCallEnd(peer, ctrl, p.CallEnd)
	case *federationv2pb.Control_SdsRelay:
		h.handleSdsRelay(peer, ctrl, p.SdsRelay)
	case *federationv2pb.Control_PositionSample:
		h.handlePositionSample(peer, ctrl, p.PositionSample)
	case *federationv2pb.Control_StationUpdate:
		h.handleStationUpdate(peer, ctrl, p.StationUpdate)
	}
}

func firstMD(md metadata.MD, key string) string {
	vals := md.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func normalizeRPCTarget(raw string) (string, error) {
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		if u.Host == "" {
			return "", fmt.Errorf("empty host")
		}
		return u.Host, nil
	}
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("empty target")
	}
	return raw, nil
}

func isIncompatibleGRPCEndpoint(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "failed reading server preface") ||
		strings.Contains(s, "frame too large") ||
		strings.Contains(s, "http/1.1")
}

func federationSubpathForKey(key string) string {
	normalized := strings.TrimSpace(key)
	sum := sha256.Sum256([]byte(normalized))
	return "/federation/" + hex.EncodeToString(sum[:12])
}

func (h *Hub) handleHello(peer *Peer, ctrl *federationv2pb.Control, hello *federationv2pb.Hello) {
	h.logger.Printf("federation: hello from %s (version %d)", ctrl.GetOrigin(), ctrl.GetProtocolVersion())
	if origin := ctrl.GetOrigin(); origin != "" && origin != peer.Name {
		old := peer.Name
		h.renamePeer(peer, origin)
		h.logger.Printf("federation: renamed peer %s -> %s", old, origin)
	}

	// UDP-Voice-Handshake: if the peer advertises a UDP address + token,
	// we can send voice frames over UDP. Also we now know our own
	// outbound-token for this peer (= what we expect when they send to us)
	// — that was generated and stored in buildHello.
	if h.udpVoice != nil && hello.GetUdpAddr() != "" && hello.GetUdpToken() != "" {
		h.udpInTokenMu.RLock()
		myInToken := h.udpInTokens[peer.Name]
		h.udpInTokenMu.RUnlock()
		if myInToken == "" {
			myInToken = NewToken()
			h.udpInTokenMu.Lock()
			h.udpInTokens[peer.Name] = myInToken
			h.udpInTokenMu.Unlock()
		}
		h.udpVoice.RegisterPeer(peer.Name, hello.GetUdpAddr(), hello.GetUdpToken(), myInToken)
	}

	h.sendPeerExchange(peer)
	h.sendUsersDBOffer(peer)
}

func (h *Hub) handleSyncResponse(peer *Peer, sr *federationv2pb.SyncResponse) {
	for issiStr, info := range sr.GetSubscribers() {
		var issi uint32
		fmt.Sscanf(issiStr, "%d", &issi)
		peer.RegisterISSI(issi)
		peer.AffiliateISSI(issi, info.GetGssis())
	}
	h.logger.Printf("federation: synced %d subscribers from %s", len(sr.GetSubscribers()), peer.Name)
}

func (h *Hub) handleUsersDBOffer(peer *Peer, off *federationv2pb.UsersDbOffer) {
	if h.handler == nil || off.GetUrl() == "" {
		return
	}
	ourTS, _ := h.handler.GetUsersDBInfo()
	if ourTS == "" || off.GetTimestamp() > ourTS {
		h.logger.Printf("federation: downloading users DB from %s (%d users, ts=%s)",
			peer.Name, off.GetCount(), off.GetTimestamp())
		if err := h.handler.DownloadUsersDBFrom(off.GetUrl()); err != nil {
			h.logger.Printf("federation: users DB download failed: %v", err)
		} else {
			h.logger.Printf("federation: users DB updated from %s", peer.Name)
		}
	}
}

func (h *Hub) handlePeerExchange(peer *Peer, px *federationv2pb.PeerExchange) {
	newPeers := 0
	for _, gp := range px.GetPeers() {
		if gp.GetName() == h.serverName || gp.GetUrl() == "" {
			continue
		}
		if h.tryAddDiscoveredPeer(gp.GetName(), gp.GetUrl()) {
			newPeers++
		}
	}
	if newPeers > 0 {
		h.logger.Printf("federation: discovered %d new peer(s) via %s", newPeers, peer.Name)
	}
}

func (h *Hub) handleSubscriberUpdate(peer *Peer, ctrl *federationv2pb.Control, up *federationv2pb.SubscriberUpdate) {
	switch up.GetAction() {
	case federationv2pb.SubscriberUpdate_ACTION_REGISTER:
		peer.RegisterISSI(up.GetIssi())
		peer.AffiliateISSI(up.GetIssi(), up.GetGssis())
		h.logger.Printf("federation: %s registered ISSI %d (GSSIs=%v) [ttl=%d path=%v]",
			peer.Name, up.GetIssi(), up.GetGssis(), ctrl.GetTtl(), ctrl.GetPath())
	case federationv2pb.SubscriberUpdate_ACTION_DEREGISTER:
		peer.DeregisterISSI(up.GetIssi())
		h.logger.Printf("federation: %s deregistered ISSI %d [ttl=%d]", peer.Name, up.GetIssi(), ctrl.GetTtl())
	}
	if h.mesh.ShouldRelay(ctrl) {
		h.relayToPeers(h.mesh.PrepareRelay(ctrl), peer.Name)
	}
}

func (h *Hub) handleAffiliateUpdate(peer *Peer, ctrl *federationv2pb.Control, up *federationv2pb.AffiliateUpdate) {
	switch up.GetAction() {
	case federationv2pb.AffiliateUpdate_ACTION_AFFILIATE:
		peer.AffiliateISSI(up.GetIssi(), up.GetGssis())
		h.logger.Printf("federation: %s affiliated ISSI %d -> GSSIs %v [ttl=%d]",
			peer.Name, up.GetIssi(), up.GetGssis(), ctrl.GetTtl())
	case federationv2pb.AffiliateUpdate_ACTION_DEAFFILIATE:
		peer.DeaffiliateISSI(up.GetIssi(), up.GetGssis())
		h.logger.Printf("federation: %s deaffiliated ISSI %d from GSSIs %v [ttl=%d]",
			peer.Name, up.GetIssi(), up.GetGssis(), ctrl.GetTtl())
	}
	if h.mesh.ShouldRelay(ctrl) {
		h.relayToPeers(h.mesh.PrepareRelay(ctrl), peer.Name)
	}
}

func (h *Hub) handleCallStart(peer *Peer, ctrl *federationv2pb.Control, cs *federationv2pb.CallStart) {
	if h.handler != nil {
		h.handler.OnPeerCallStart(peer.Name, cs.GetUuid(), cs.GetSourceIssi(),
			cs.GetDestGssi(), uint8(cs.GetPriority()), uint16(cs.GetService()))
	}
	h.callMu.Lock()
	if h.activeCalls[cs.GetUuid()] == nil {
		h.activeCalls[cs.GetUuid()] = make(map[string]bool)
	}
	h.callMu.Unlock()

	if !h.mesh.ShouldRelay(ctrl) {
		return
	}
	relay := h.mesh.PrepareRelay(ctrl)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, p := range h.peers {
		if p.Name == peer.Name || IsInPath(ctrl, p.Name) {
			continue
		}
		_ = p.SendControl(relay)
		h.callMu.Lock()
		h.activeCalls[cs.GetUuid()][p.Name] = true
		h.callMu.Unlock()
	}
}

func (h *Hub) handleCallEnd(peer *Peer, ctrl *federationv2pb.Control, ce *federationv2pb.CallEnd) {
	if h.handler != nil {
		h.handler.OnPeerCallEnd(peer.Name, ce.GetUuid(), uint8(ce.GetCause()))
	}
	h.callMu.Lock()
	delete(h.activeCalls, ce.GetUuid())
	h.callMu.Unlock()

	if !h.mesh.ShouldRelay(ctrl) {
		return
	}
	relay := h.mesh.PrepareRelay(ctrl)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, p := range h.peers {
		if p.Name == peer.Name || IsInPath(ctrl, p.Name) {
			continue
		}
		_ = p.SendControl(relay)
	}
}

func (h *Hub) handleSdsRelay(peer *Peer, ctrl *federationv2pb.Control, sr *federationv2pb.SdsRelay) {
	if h.handler != nil {
		h.handler.OnPeerSDSRelay(peer.Name, sr.GetSourceIssi(), sr.GetDestIssi(), hex.EncodeToString(sr.GetSdsData()))
	}
	if h.mesh.ShouldRelay(ctrl) {
		h.relayToPeers(h.mesh.PrepareRelay(ctrl), peer.Name)
	}
}

func (h *Hub) handlePositionSample(peer *Peer, ctrl *federationv2pb.Control, ps *federationv2pb.PositionSample) {
	if h.handler != nil {
		h.handler.OnPeerPositionSample(peer.Name, ps.GetIssi(), ps.GetLat(), ps.GetLon(), ps.GetRepeater())
	}
	if h.mesh.ShouldRelay(ctrl) {
		h.relayToPeers(h.mesh.PrepareRelay(ctrl), peer.Name)
	}
}

func (h *Hub) handleStationUpdate(peer *Peer, ctrl *federationv2pb.Control, su *federationv2pb.StationUpdate) {
	if h.handler != nil && su.GetStation() != nil {
		h.handler.OnPeerStationUpdate(peer.Name, su.GetStation().AsMap())
	}
	if h.mesh.ShouldRelay(ctrl) {
		h.relayToPeers(h.mesh.PrepareRelay(ctrl), peer.Name)
	}
}

// handleBinaryMessage processes binary federation data (voice frames).
// Format: callUUID (36 bytes ASCII) + frame payload
func (h *Hub) handleBinaryMessage(peer *Peer, data []byte) {
	if len(data) < 36 {
		return
	}

	h.handleVoiceFrame(peer, string(data[:36]), data[36:])
}

func (h *Hub) handleVoiceFrame(peer *Peer, callUUID string, frameData []byte) {
	if len(callUUID) != 36 {
		return
	}

	// Forward to local Brew service
	if h.handler != nil {
		h.handler.OnPeerVoiceFrame(peer.Name, callUUID, frameData)
	}

	// Mesh relay: forward to ALL other connected peers (not just call participants)
	// This ensures voice reaches servers that are only indirectly connected
	h.mu.RLock()
	for _, p := range h.peers {
		if p.Name != peer.Name {
			p.SendVoiceFrame(callUUID, frameData)
		}
	}
	h.mu.RUnlock()
}

// ==================================================================
// Broadcasting to peers (called by the Brew service)
// ==================================================================

// BroadcastSubscriber notifies all peers about a subscriber change.
func (h *Hub) BroadcastSubscriber(issi uint32, action string, gssis []uint32) {
	subAction := federationv2pb.SubscriberUpdate_ACTION_UNSPECIFIED
	switch action {
	case "register":
		subAction = federationv2pb.SubscriberUpdate_ACTION_REGISTER
	case "deregister":
		subAction = federationv2pb.SubscriberUpdate_ACTION_DEREGISTER
	}
	ctrl := &federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_SubscriberUpdate{
			SubscriberUpdate: &federationv2pb.SubscriberUpdate{
				Issi:   issi,
				Action: subAction,
				Gssis:  append([]uint32(nil), gssis...),
			},
		},
	}
	h.mesh.PrepareOutgoing(ctrl)
	h.broadcastToAllPeers(ctrl)
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
	affAction := federationv2pb.AffiliateUpdate_ACTION_UNSPECIFIED
	switch action {
	case "affiliate":
		affAction = federationv2pb.AffiliateUpdate_ACTION_AFFILIATE
	case "deaffiliate":
		affAction = federationv2pb.AffiliateUpdate_ACTION_DEAFFILIATE
	}
	ctrl := &federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_AffiliateUpdate{
			AffiliateUpdate: &federationv2pb.AffiliateUpdate{
				Issi:   issi,
				Action: affAction,
				Gssis:  append([]uint32(nil), fedGSSIs...),
			},
		},
	}
	h.mesh.PrepareOutgoing(ctrl)
	h.broadcastToAllPeers(ctrl)
}

// BroadcastCallStart notifies peers that have subscribers on the target GSSI.
// Only federated GSSIs (23-90) are shared between servers.
func (h *Hub) BroadcastCallStart(callUUID string, sourceISSI, destGSSI uint32, priority uint8, service uint16) {
	if !isFederatedGSSI(destGSSI) {
		return
	}
	ctrl := &federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_CallStart{
			CallStart: &federationv2pb.CallStart{
				Uuid:       callUUID,
				SourceIssi: sourceISSI,
				DestGssi:   destGSSI,
				Priority:   uint32(priority),
				Service:    uint32(service),
			},
		},
	}
	h.mesh.PrepareOutgoing(ctrl)

	h.callMu.Lock()
	if h.activeCalls[callUUID] == nil {
		h.activeCalls[callUUID] = make(map[string]bool)
	}
	h.callMu.Unlock()

	h.mu.RLock()
	for _, peer := range h.peers {
		_ = peer.SendControl(ctrl)
		h.callMu.Lock()
		h.activeCalls[callUUID][peer.Name] = true
		h.callMu.Unlock()
	}
	h.mu.RUnlock()
}

// BroadcastStation sendet einen BlueStation-Heartbeat an alle Peers
// (Stations-Federation). Damit zeigen alle Server die gleiche Station-Liste.
func (h *Hub) BroadcastStation(station map[string]any) {
	st, err := structpb.NewStruct(station)
	if err != nil {
		h.logger.Printf("federation: cannot encode station map: %v", err)
		return
	}
	ctrl := &federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_StationUpdate{
			StationUpdate: &federationv2pb.StationUpdate{Station: st},
		},
	}
	h.mesh.PrepareOutgoing(ctrl)
	h.broadcastToAllPeers(ctrl)
}

// BroadcastPositionSample sendet einen empfangenen Position-Sample an alle Peers
// (Coverage-Federation). Mesh-Router dedupliziert + relayed.
func (h *Hub) BroadcastPositionSample(issi uint32, lat, lon float64, repeater string) {
	ctrl := &federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_PositionSample{
			PositionSample: &federationv2pb.PositionSample{
				Issi:     issi,
				Lat:      lat,
				Lon:      lon,
				Repeater: repeater,
			},
		},
	}
	h.mesh.PrepareOutgoing(ctrl)
	h.broadcastToAllPeers(ctrl)
}

// BroadcastCallEnd notifies all peers about a call ending.
func (h *Hub) BroadcastCallEnd(callUUID string, cause uint8) {
	h.callMu.Lock()
	delete(h.activeCalls, callUUID)
	h.callMu.Unlock()

	ctrl := &federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_CallEnd{
			CallEnd: &federationv2pb.CallEnd{
				Uuid:  callUUID,
				Cause: uint32(cause),
			},
		},
	}
	h.mesh.PrepareOutgoing(ctrl)
	h.broadcastToAllPeers(ctrl)
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
		peer.SendVoiceFrame(callUUID, frameData)
	}
}

// BroadcastSDS relays an SDS message through the mesh.
func (h *Hub) BroadcastSDS(sourceISSI, destISSI uint32, sdsDataHex string) {
	raw, err := hex.DecodeString(sdsDataHex)
	if err != nil {
		h.logger.Printf("federation: invalid local SDS hex: %v", err)
		return
	}
	ctrl := &federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_SdsRelay{
			SdsRelay: &federationv2pb.SdsRelay{
				SourceIssi: sourceISSI,
				DestIssi:   destISSI,
				SdsData:    raw,
			},
		},
	}
	h.mesh.PrepareOutgoing(ctrl)
	h.broadcastToAllPeers(ctrl)
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

	_ = peer.SendControl(&federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_UsersDbOffer{
			UsersDbOffer: &federationv2pb.UsersDbOffer{
				Timestamp: ts,
				Url:       dbURL,
				Count:     uint32(count),
			},
		},
	})
}

// sendPeerExchange sends our list of known peers to a peer.
func (h *Hub) sendPeerExchange(peer *Peer) {
	h.knownMu.RLock()
	gp := make([]*federationv2pb.GossipPeer, 0, len(h.knownPeers)+1)
	if h.selfURL != "" {
		gp = append(gp, &federationv2pb.GossipPeer{Name: h.serverName, Url: h.selfURL})
	}
	for name, u := range h.knownPeers {
		if name != peer.Name {
			gp = append(gp, &federationv2pb.GossipPeer{Name: name, Url: u})
		}
	}
	h.knownMu.RUnlock()

	if len(gp) == 0 {
		return
	}
	_ = peer.SendControl(&federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_PeerExchange{
			PeerExchange: &federationv2pb.PeerExchange{Peers: gp},
		},
	})
	h.logger.Printf("federation: sent %d known peer(s) to %s", len(gp), peer.Name)
}

// tryAddDiscoveredPeer adds a newly discovered peer and connects to it.
// Returns true if the peer was new.
func (h *Hub) tryAddDiscoveredPeer(name, url string) bool {
	// Self-Check: niemals zu sich selbst connecten (sonst Geister-Peer
	// "HH-Cluster incoming" auf eigenem Server).
	if name == h.serverName || url == h.selfURL {
		return false
	}
	// Legacy websocket gossip URLs (e.g. .../peer/) are not valid for
	// protobuf gRPC federation transport.
	if strings.Contains(strings.ToLower(url), "/peer") {
		h.logger.Printf("federation: skipping discovered non-gRPC peer %s at %s", name, url)
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

	// UDP-Token-Cache: vorher hatten wir den Token unter "peer-0" gecached,
	// jetzt muss er unter dem echten Namen ("freetetra.de" / "HH-Cluster")
	// gefunden werden, sonst generiert das naechste buildHello einen neuen
	// Token und der Sender sendet mit altem, der Receiver erwartet neuen.
	h.udpInTokenMu.Lock()
	if tok, ok := h.udpInTokens[oldName]; ok {
		delete(h.udpInTokens, oldName)
		if _, exists := h.udpInTokens[newName]; !exists {
			h.udpInTokens[newName] = tok
		}
	}
	h.udpInTokenMu.Unlock()
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
	subs := make(map[string]*federationv2pb.SyncSubscriber, len(localSubs))
	for issi, gssis := range localSubs {
		subs[fmt.Sprintf("%d", issi)] = &federationv2pb.SyncSubscriber{
			Gssis: append([]uint32(nil), gssis...),
		}
	}
	_ = peer.SendControl(&federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_SyncResponse{
			SyncResponse: &federationv2pb.SyncResponse{Subscribers: subs},
		},
	})
	h.logger.Printf("federation: sent sync to %s (%d subscribers)", peer.Name, len(subs))
}

func (h *Hub) broadcastToAllPeers(ctrl *federationv2pb.Control) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, peer := range h.peers {
		_ = peer.SendControl(ctrl)
	}
}

func (h *Hub) relayToPeers(ctrl *federationv2pb.Control, excludeName string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, peer := range h.peers {
		if peer.Name != excludeName {
			_ = peer.SendControl(ctrl)
		}
	}
}
