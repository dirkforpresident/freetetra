package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/freetetra/server/internal/brew"
	"github.com/freetetra/server/internal/config"
	"github.com/freetetra/server/internal/federation"
)

// federationBridge integrates the federation hub with the Brew service.
type federationBridge struct {
	cfg    config.Config
	logger *log.Logger
	svc    *Service
	hub    *federation.Hub
}

func newFederationBridge(cfg config.Config, logger *log.Logger, svc *Service) *federationBridge {
	fb := &federationBridge{
		cfg:    cfg,
		logger: logger,
		svc:    svc,
	}
	fb.hub = federation.NewHub(
		cfg.Federation.Name,
		cfg.Federation.Key,
		cfg.Federation.SelfURL,
		cfg.Federation.RPCListenAddr,
		fb,
		logger,
	)
	if sameListenerEndpoint(cfg.HTTPListenAddr, cfg.Federation.RPCListenAddr) {
		svc.server.SetGRPCServer(fb.hub.NewGRPCServer())
		fb.hub.UseSharedPortRPC()
		logger.Printf("federation: multiplexing HTTP, APIs and gRPC on %s", cfg.HTTPListenAddr)
	}
	return fb
}

func sameListenerEndpoint(httpAddr, rpcAddr string) bool {
	hHost, hPort, ok := splitListenAddr(httpAddr)
	if !ok {
		return false
	}
	rHost, rPort, ok := splitListenAddr(rpcAddr)
	if !ok || hPort != rPort {
		return false
	}
	return hostsEquivalent(hHost, rHost)
}

func splitListenAddr(addr string) (string, string, bool) {
	a := strings.TrimSpace(addr)
	if a == "" {
		return "", "", false
	}
	host, port, err := net.SplitHostPort(a)
	if err != nil {
		if strings.HasPrefix(a, ":") {
			return "", strings.TrimPrefix(a, ":"), true
		}
		return "", "", false
	}
	return host, port, true
}

func hostsEquivalent(a, b string) bool {
	normalize := func(host string) string {
		h := strings.TrimSpace(strings.ToLower(host))
		switch h {
		case "", "0.0.0.0", "::", "[::]":
			return "*"
		default:
			return h
		}
	}
	return normalize(a) == normalize(b)
}

func (fb *federationBridge) start(ctx context.Context) {
	// Build peer configs from environment
	peers := make([]federation.PeerConfig, 0, len(fb.cfg.Federation.Peers))
	for i, url := range fb.cfg.Federation.Peers {
		peers = append(peers, federation.PeerConfig{
			Name: fmt.Sprintf("peer-%d", i),
			URL:  url,
			Key:  fb.cfg.Federation.Key,
		})
	}

	// Start outgoing peer connections
	fb.hub.Start(ctx, peers)
	fb.logger.Printf("federation: started with %d peer(s) configured", len(peers))

	// Periodic anti-entropy sync — alle 30 Sek den kompletten lokalen
	// Subscriber-State an alle Peers schicken. Damit konvergieren Peers nach
	// Restart, Netzaussetzer oder neuer Connection automatisch — ohne dass
	// User auf seinem Funkgeraet was machen muss.
	go fb.syncLoop(ctx)
}

func (fb *federationBridge) syncLoop(ctx context.Context) {
	// Initial delay damit die Peer-Verbindungen erstmal aufbauen koennen.
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}
	fb.syncAllSubscribers()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fb.syncAllSubscribers()
		}
	}
}

func (fb *federationBridge) syncAllSubscribers() {
	if fb.hub == nil {
		return
	}
	clients := fb.svc.server.SnapshotClients()
	subCount := 0
	for _, c := range clients {
		for _, sub := range c.Subscribers {
			gssis := append([]uint32(nil), sub.Groups...)
			fb.hub.BroadcastSubscriber(sub.Number, "register", gssis)
			subCount++
		}
	}
	// Stations mitsynchronisieren — falls ein Peer-Server neu connectet
	// oder nach Netzaussetzer, kriegt er den aktuellen Stations-Stand.
	stCount := 0
	if fb.svc.stationStore != nil {
		for _, st := range fb.svc.stationStore.All() {
			st := st // copy
			fb.NotifyStationUpdate(&st)
			stCount++
		}
	}
	if subCount > 0 || stCount > 0 {
		fb.logger.Printf("federation: anti-entropy sync sent %d subscribers, %d stations", subCount, stCount)
	}
}

// ==================================================================
// CallHandler interface implementation (called by federation hub)
// ==================================================================

func (fb *federationBridge) OnPeerCallStart(peerName string, callUUID string, sourceISSI, destGSSI uint32, priority uint8, service uint16) {
	uid, err := uuid.Parse(callUUID)
	if err != nil {
		fb.logger.Printf("federation: invalid call UUID from %s: %s", peerName, callUUID)
		return
	}

	// Build GROUP_TX wire message and broadcast to local clients
	wire := brew.BuildGroupTX(uid, sourceISSI, destGSSI, priority, service)
	n := fb.svc.server.BroadcastToGroup(destGSSI, wire, "")
	fb.logger.Printf("federation: relayed GROUP_TX from %s ISSI=%d->GSSI=%d to %d local clients", peerName, sourceISSI, destGSSI, n)

	// Track the call locally
	fb.svc.callMu.Lock()
	fb.svc.calls[uid] = &activeCall{
		ID:              uid,
		SourceISSI:      sourceISSI,
		DestinationGSI:  destGSSI,
		DestinationType: destinationTypeGroup,
		OriginClientID:  "federation:" + peerName,
	}
	fb.svc.callMu.Unlock()
	if fb.svc.lastHeard != nil {
		fb.svc.lastHeard.Start(uid, sourceISSI, destGSSI, "peer:"+peerName)
	}
}

// OnPeerPrivateCallStart receives a federated subscriber-to-subscriber call
// setup and delivers it to the local destination subscriber. The activeCall
// record uses DestinationGSI to hold the destination ISSI (matching the
// overload at service.go ~line 500 where DestinationType == subscriber
// implies DestinationGSI carries the dest ISSI).
func (fb *federationBridge) OnPeerPrivateCallStart(peerName string, callUUID string, sourceISSI, destISSI uint32, priority uint8, service uint16) {
	uid, err := uuid.Parse(callUUID)
	if err != nil {
		fb.logger.Printf("federation: invalid private-call UUID from %s: %s", peerName, callUUID)
		return
	}

	// Private (subscriber-to-subscriber) calls are duplex by TETRA convention:
	// CMCE on the destination BS will only emit a duplex D-SETUP if the inbound
	// CircularCallPayload has Duplex=1. The federation v2 CallStart proto
	// currently doesn't carry the duplex/method/communication flags across
	// the wire, so we set them here based on the call being a private call.
	// Mirrors the same value the SIP bridge uses for its outbound P2P legs
	// (sip_bridge.go: Duplex=1).
	wire := brew.BuildSetupRequest(uid, brew.CircularCallPayload{
		Source:      sourceISSI,
		Destination: destISSI,
		Priority:    priority,
		Service:     uint8(service),
		Duplex:      1,
	})
	n := fb.svc.server.BroadcastToSubscriber(destISSI, wire, "")
	fb.logger.Printf("federation: relayed private SETUP from %s ISSI=%d->ISSI=%d to %d local clients", peerName, sourceISSI, destISSI, n)

	fb.svc.callMu.Lock()
	fb.svc.calls[uid] = &activeCall{
		ID:              uid,
		SourceISSI:      sourceISSI,
		DestinationGSI:  destISSI,
		DestinationType: destinationTypeSubscriber,
		OriginClientID:  "federation:" + peerName,
	}
	fb.svc.callMu.Unlock()
	if fb.svc.lastHeard != nil {
		fb.svc.lastHeard.Start(uid, sourceISSI, destISSI, "peer:"+peerName)
	}
}

func (fb *federationBridge) OnPeerCallEnd(peerName string, callUUID string, cause uint8) {
	uid, err := uuid.Parse(callUUID)
	if err != nil {
		return
	}

	fb.svc.callMu.RLock()
	call := fb.svc.calls[uid]
	fb.svc.callMu.RUnlock()

	if call == nil {
		return
	}

	wire := brew.BuildCallRelease(uid, cause)
	var n int
	if call.DestinationType == destinationTypeSubscriber {
		n = fb.svc.server.BroadcastToSubscriber(call.DestinationGSI, wire, "")
		if call.SourceISSI != 0 && call.SourceISSI != call.DestinationGSI {
			n += fb.svc.server.BroadcastToSubscriber(call.SourceISSI, wire, "")
		}
		fb.logger.Printf("federation: relayed private RELEASE from %s ISSI=%d to %d local clients", peerName, call.DestinationGSI, n)
	} else {
		n = fb.svc.server.BroadcastToGroup(call.DestinationGSI, wire, "")
		fb.logger.Printf("federation: relayed GROUP_IDLE from %s GSSI=%d to %d local clients", peerName, call.DestinationGSI, n)
	}

	fb.svc.callMu.Lock()
	delete(fb.svc.calls, uid)
	fb.svc.callMu.Unlock()
	if fb.svc.lastHeard != nil {
		fb.svc.lastHeard.End(uid)
	}
}

func (fb *federationBridge) OnPeerVoiceFrame(peerName string, callUUID string, frameData []byte) {
	uid, err := uuid.Parse(callUUID)
	if err != nil {
		return
	}

	fb.svc.callMu.RLock()
	call := fb.svc.calls[uid]
	fb.svc.callMu.RUnlock()

	if call == nil {
		return
	}

	// Reconstruct Brew FRAME_TRAFFIC and broadcast
	lengthBits := uint16(len(frameData) * 8)
	wire := brew.BuildFrame(brew.FrameTypeTrafficChannel, uid, lengthBits, frameData)
	if call.DestinationType == destinationTypeSubscriber {
		// Mirror the local subscriber-call voice path (service.go ~line 500):
		// deliver to the dest ISSI and to the source ISSI (the latter may
		// not be local on this server — BroadcastToSubscriber is a no-op
		// if no client owns that ISSI).
		fb.svc.server.BroadcastToSubscriber(call.DestinationGSI, wire, "")
		if call.SourceISSI != 0 && call.SourceISSI != call.DestinationGSI {
			fb.svc.server.BroadcastToSubscriber(call.SourceISSI, wire, "")
		}
		return
	}
	fb.svc.server.BroadcastToGroup(call.DestinationGSI, wire, "")
}

// OnPeerStationUpdate wird vom Federation-Hub aufgerufen, wenn ein Peer einen
// BlueStation-Heartbeat (Station-Push) weiterreicht. Wir uebernehmen die Station
// in unseren lokalen stationStore.
func (fb *federationBridge) OnPeerStationUpdate(peerName string, stationMap map[string]any) {
	if fb.svc.stationStore == nil || stationMap == nil {
		return
	}
	b, err := json.Marshal(stationMap)
	if err != nil {
		return
	}
	var st Station
	if err := json.Unmarshal(b, &st); err != nil {
		return
	}
	if _, err := fb.svc.stationStore.Upsert(st); err != nil {
		fb.logger.Printf("federation: station upsert from %s failed: %v", peerName, err)
	}
}

// OnPeerPositionSample wird vom Federation-Hub aufgerufen, wenn ein Peer
// einen Position-Sample meldet (Coverage-Federation). Wir speichern den
// Sample in der lokalen Coverage-DB damit unsere Map die Gesamt-Welt zeigt.
func (fb *federationBridge) OnPeerPositionSample(peerName string, issi uint32, lat, lon float64, tmoSite string) {
	if fb.svc.coverageDB == nil {
		return
	}
	// TMO-Site-Tag = der Server-Name der den Sample empfangen hat. Wenn der
	// Origin-Tag leer war, fallback auf peer-Name.
	if tmoSite == "" {
		tmoSite = peerName
	}
	_ = fb.svc.coverageDB.Insert(issi, lat, lon, nil, nil, tmoSite)
	if fb.svc.positionStore != nil {
		fb.svc.positionStore.Update(issi, lat, lon)
	}
}

func (fb *federationBridge) OnPeerSDSRelay(peerName string, sourceISSI, destISSI uint32, sdsDataHex string) {
	sdsData, err := hex.DecodeString(sdsDataHex)
	if err != nil {
		fb.logger.Printf("federation: invalid SDS hex from %s: %v", peerName, err)
		return
	}

	callUUID := uuid.New()

	// Build SHORT_TRANSFER + SDS_TRANSFER and send to target
	shortTransfer := brew.BuildShortData(callUUID, brew.ShortDataPayload{
		Source:      sourceISSI,
		Destination: destISSI,
	})
	lengthBits := uint16(len(sdsData) * 8)
	sdsFrame := brew.BuildFrame(brew.FrameTypeSDSTransfer, callUUID, lengthBits, sdsData)

	n := fb.svc.server.BroadcastToSubscriber(destISSI, shortTransfer, "")
	fb.svc.server.BroadcastToSubscriber(destISSI, sdsFrame, "")
	fb.logger.Printf("federation: delivered SDS from %s: %d->%d to %d local clients", peerName, sourceISSI, destISSI, n)
}

func (fb *federationBridge) GetUsersDBInfo() (string, int) {
	if fb.svc.radioIDAuth == nil {
		return "", 0
	}
	return fb.svc.radioIDAuth.LocalDBInfo()
}

func (fb *federationBridge) DownloadUsersDBFrom(url string) error {
	if fb.svc.radioIDAuth == nil {
		return fmt.Errorf("radioid auth not enabled")
	}
	return fb.svc.radioIDAuth.DownloadFromURL(url)
}

func (fb *federationBridge) GetLocalSubscribers() map[uint32][]uint32 {
	clients := fb.svc.server.SnapshotClients()
	result := make(map[uint32][]uint32)
	for _, snap := range clients {
		for _, sub := range snap.Subscribers {
			result[sub.Number] = sub.Groups
		}
	}
	return result
}

// ==================================================================
// Methods called by the service when local events happen
// ==================================================================

// NotifySubscriberUpdate notifies peers about a local subscriber change.
func (fb *federationBridge) NotifySubscriberUpdate(issi uint32, action string, gssis []uint32) {
	if fb.hub == nil {
		return
	}
	fb.hub.BroadcastSubscriber(issi, action, gssis)
}

// NotifyPositionSample sendet einen empfangenen LIP-Sample (lat/lon) an alle
// Federation-Peers — fuer geteilte Coverage-Map.
func (fb *federationBridge) NotifyPositionSample(issi uint32, lat, lon float64, tmoSite string) {
	if fb.hub == nil {
		return
	}
	fb.hub.BroadcastPositionSample(issi, lat, lon, tmoSite)
}

// NotifyStationUpdate broadcasted einen BlueStation-Heartbeat an alle Peers.
func (fb *federationBridge) NotifyStationUpdate(st *Station) {
	if fb.hub == nil || st == nil {
		return
	}
	// Station -> map[string]any via JSON-Roundtrip (vermeidet harte Coupling
	// zwischen federation und service Paketen).
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return
	}
	fb.hub.BroadcastStation(m)
}

// NotifyCallStart notifies peers about a local group call start.
func (fb *federationBridge) NotifyCallStart(callUUID string, sourceISSI, destGSSI uint32, priority uint8, service uint16) {
	if fb.hub == nil {
		return
	}
	fb.hub.BroadcastCallStart(callUUID, sourceISSI, destGSSI, priority, service)
}

// NotifyPrivateCallStart routes a subscriber-to-subscriber CallStart to the
// single peer that owns destISSI. If no peer owns the ISSI the call is
// purely local — federation does nothing.
func (fb *federationBridge) NotifyPrivateCallStart(callUUID string, sourceISSI, destISSI uint32, priority uint8, service uint16) {
	if fb.hub == nil {
		return
	}
	peerName, ok := fb.hub.RouteCallStartToPeerForISSI(callUUID, sourceISSI, destISSI, priority, service)
	if !ok {
		return
	}
	fb.logger.Printf("federation: routed private call=%s %d->%d via peer %s", callUUID, sourceISSI, destISSI, peerName)
}

// NotifyCallEnd notifies peers about a local call ending. Private calls go
// to the single peer recorded for the call; group calls broadcast.
func (fb *federationBridge) NotifyCallEnd(callUUID string, cause uint8) {
	if fb.hub == nil {
		return
	}
	fb.hub.RouteCallEndForCall(callUUID, cause)
}

// NotifyVoiceFrame sends a voice frame to peers involved in a call. Private
// calls go to the single peer recorded for the call; group calls broadcast.
func (fb *federationBridge) NotifyVoiceFrame(callUUID string, frameData []byte) {
	if fb.hub == nil {
		return
	}
	fb.hub.RouteVoiceFrameForCall(callUUID, frameData)
}

// PeerCount returns the number of connected federation peers.
func (fb *federationBridge) PeerCount() int {
	if fb.hub == nil {
		return 0
	}
	return fb.hub.PeerCount()
}

// PeerSnapshots returns info about connected peers.
func (fb *federationBridge) PeerSnapshots() []federation.PeerSnapshot {
	if fb.hub == nil {
		return nil
	}
	return fb.hub.PeerSnapshots()
}
