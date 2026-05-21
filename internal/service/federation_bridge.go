package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
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
	pacer  *audioPacer
}

func newFederationBridge(cfg config.Config, logger *log.Logger, svc *Service) *federationBridge {
	fb := &federationBridge{
		cfg:    cfg,
		logger: logger,
		svc:    svc,
		pacer:  newAudioPacer(),
	}
	fb.hub = federation.NewHub(cfg.Federation.Name, cfg.Federation.Key, cfg.Federation.SelfURL, fb, logger)
	return fb
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

	// Register HTTP handler for incoming peer connections
	fb.svc.server.RegisterHTTPHandler("/peer/", fb.hub.HandleIncoming)

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
		DestinationType: "group",
		OriginClientID:  "federation:" + peerName,
	}
	fb.svc.callMu.Unlock()
	if fb.svc.lastHeard != nil {
		fb.svc.lastHeard.Start(uid, sourceISSI, destGSSI, "peer:"+peerName)
	}

	// Audio-Pacer fuer diesen Call starten. Voice-Frames werden mit 60ms-Pacing
	// ausgespielt statt direkt durchgereicht (sonst Burst-/Schleppe-Effekt am
	// Ende des Durchgangs).
	gssi := destGSSI
	fb.pacer.Start(uid, func(frameData []byte) {
		lengthBits := uint16(len(frameData) * 8)
		wire := brew.BuildFrame(brew.FrameTypeTrafficChannel, uid, lengthBits, frameData)
		fb.svc.server.BroadcastToGroup(gssi, wire, "")
	})
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
	n := fb.svc.server.BroadcastToGroup(call.DestinationGSI, wire, "")
	fb.logger.Printf("federation: relayed GROUP_IDLE from %s GSSI=%d to %d local clients", peerName, call.DestinationGSI, n)

	fb.svc.callMu.Lock()
	delete(fb.svc.calls, uid)
	fb.svc.callMu.Unlock()
	if fb.svc.lastHeard != nil {
		fb.svc.lastHeard.End(uid)
	}

	// Pacer SOFORT stoppen — sonst spielt der Worker noch bis zu
	// pacerBufferDepth*60ms an gestauten Frames aus (= Audio-Schleppe).
	fb.pacer.Stop(uid)
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

	// Frame in den Pacer — der spielt im 60ms-Takt aus. Bei Burst-Empfang
	// (TCP-Jitter) werden alte Frames automatisch gedropped (Drop-Oldest),
	// damit die Latenz nicht aufschaukelt.
	fb.pacer.Push(uid, frameData)
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
func (fb *federationBridge) OnPeerPositionSample(peerName string, issi uint32, lat, lon float64, repeater string) {
	if fb.svc.coverageDB == nil {
		return
	}
	// Repeater-Tag = der Server-Name der den Sample empfangen hat. Wenn der
	// Origin-Repeater leer war, fallback auf peer-Name.
	if repeater == "" {
		repeater = peerName
	}
	_ = fb.svc.coverageDB.Insert(issi, lat, lon, nil, nil, repeater)
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
func (fb *federationBridge) NotifyPositionSample(issi uint32, lat, lon float64, repeater string) {
	if fb.hub == nil {
		return
	}
	fb.hub.BroadcastPositionSample(issi, lat, lon, repeater)
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

// NotifyCallEnd notifies peers about a local call ending.
func (fb *federationBridge) NotifyCallEnd(callUUID string, cause uint8) {
	if fb.hub == nil {
		return
	}
	fb.hub.BroadcastCallEnd(callUUID, cause)
}

// NotifyVoiceFrame sends a voice frame to peers involved in a call.
func (fb *federationBridge) NotifyVoiceFrame(callUUID string, frameData []byte) {
	if fb.hub == nil {
		return
	}
	fb.hub.BroadcastVoiceFrame(callUUID, frameData)
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

