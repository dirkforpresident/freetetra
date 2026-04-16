package service

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"

	"github.com/freetetra/server/internal/config"
)

type NetstackBridge struct {
	cfg    config.Config
	logger *log.Logger
	svc    *Service

	client mqtt.Client

	routes []netstackRoute

	mu             sync.RWMutex
	callsByID      map[string]*bridgeCall
	callsByTraffic map[string]*bridgeCall
	routeLocks     map[string]*routeLock
}

type routeLock struct {
	CallID    string
	ExpiresAt time.Time
}

type bridgeCall struct {
	NetstackID string
	BrewID     uuid.UUID
	SourceISSI uint32
	BrewGSSI   uint32
	TrafficID  string
	MCC        int
	MNC        int

	State         string
	CreatedAt     time.Time
	LastEventAt   time.Time
	FrameCount    uint64
	RouteKey      string
	Started       bool
	PendingFrames [][]byte
}

type netstackCallPayload struct {
	UUID            string          `json:"UUID"`
	MNI             uint32          `json:"MNI"`
	InitialSSI      uint32          `json:"InitialSSI"`
	GroupSSIs       []uint32        `json:"GroupSSIs"`
	CallingPartySSI json.RawMessage `json:"CallingPartySSI"`
	Traffic         *struct {
		UUID string `json:"UUID"`
	} `json:"Traffic"`
}

type netstackRoute struct {
	Name string `json:"name"`
	MCC  int    `json:"mcc"`
	MNC  int    `json:"mnc"`

	// Preferred explicit mapping fields.
	NetstackGSSI    uint32 `json:"netstack_gssi"`
	NetstackGSSIMin uint32 `json:"netstack_gssi_min"`
	NetstackGSSIMax uint32 `json:"netstack_gssi_max"`
	BrewGSSI        uint32 `json:"brew_gssi"`

	// Backward-compatible aliases.
	GSSI      uint32 `json:"gssi"`
	GSSIMin   uint32 `json:"gssi_min"`
	GSSIMax   uint32 `json:"gssi_max"`
	Talkgroup uint32 `json:"talkgroup"`
}

type routeEnvelope struct {
	Routes   []netstackRoute `json:"routes"`
	Mappings []netstackRoute `json:"mappings"`
}

func NewNetstackBridge(cfg config.Config, logger *log.Logger, svc *Service) (*NetstackBridge, error) {
	routes, err := loadRoutes(cfg.Netstack.RouteFile)
	if err != nil {
		return nil, err
	}
	return &NetstackBridge{
		cfg:            cfg,
		logger:         logger,
		svc:            svc,
		routes:         routes,
		callsByID:      make(map[string]*bridgeCall),
		callsByTraffic: make(map[string]*bridgeCall),
		routeLocks:     make(map[string]*routeLock),
	}, nil
}

func (b *NetstackBridge) Start(ctx context.Context) error {
	opts := mqtt.NewClientOptions().
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(2 * time.Second).
		SetClientID(b.cfg.MQTT.ClientID).
		AddBroker(b.cfg.MQTT.Broker)

	if strings.TrimSpace(b.cfg.MQTT.Username) != "" {
		opts.SetUsername(b.cfg.MQTT.Username)
		opts.SetPassword(b.cfg.MQTT.Password)
	}

	opts.OnConnect = func(c mqtt.Client) {
		if err := b.subscribe(c); err != nil {
			b.logger.Printf("netstack bridge subscribe error: %v", err)
		}
	}
	opts.OnConnectionLost = func(_ mqtt.Client, err error) {
		if isIgnoredMQTTDisconnect(err) {
			return
		}
		b.logger.Printf("netstack bridge mqtt disconnected: %v", err)
	}

	b.client = mqtt.NewClient(opts)
	token := b.client.Connect()
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("mqtt connect: %w", token.Error())
	}

	b.logger.Printf(
		"netstack bridge enabled broker=%s callTopic=%s trafficTopic=%s routes=%d minTrafficFrames=%d pendingMaxAge=%s routeTimeout=%s",
		b.cfg.MQTT.Broker,
		b.cfg.Netstack.CallTopic,
		b.cfg.Netstack.TrafficTopic,
		len(b.routes),
		b.minTrafficFrames(),
		b.pendingMaxAge().String(),
		b.routeTimeout().String(),
	)

	go func() {
		<-ctx.Done()
		b.Stop()
	}()
	go b.expiryLoop(ctx)
	return nil
}

func (b *NetstackBridge) Stop() {
	if b.client != nil && b.client.IsConnected() {
		b.client.Disconnect(200)
	}
}

func isIgnoredMQTTDisconnect(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "pingresp not received")
}

func (b *NetstackBridge) subscribe(c mqtt.Client) error {
	callToken := c.Subscribe(b.cfg.Netstack.CallTopic, b.cfg.MQTT.QoS, b.onCallMessage)
	if callToken.Wait() && callToken.Error() != nil {
		return callToken.Error()
	}
	trafficToken := c.Subscribe(b.cfg.Netstack.TrafficTopic, b.cfg.MQTT.QoS, b.onTrafficMessage)
	if trafficToken.Wait() && trafficToken.Error() != nil {
		return trafficToken.Error()
	}
	return nil
}

func (b *NetstackBridge) onCallMessage(_ mqtt.Client, msg mqtt.Message) {
	b.evictExpiredCalls()

	parts := strings.Split(msg.Topic(), "/")
	if len(parts) < 6 {
		return
	}
	mcc := parseInt(parts[1])
	mnc := parseInt(parts[2])
	callID := parts[4]
	mode := parts[5]

	switch mode {
	case "new":
		var call netstackCallPayload
		if err := json.Unmarshal(msg.Payload(), &call); err != nil {
			b.logger.Printf("netstack call/new json decode error: %v", err)
			return
		}

		destination := call.InitialSSI
		if destination == 0 && len(call.GroupSSIs) > 0 {
			destination = call.GroupSSIs[0]
		}
		if destination == 0 {
			return
		}
		brewGSSI := b.mapNetstackGSSIToBrewGSSI(mcc, mnc, destination)

		source := parseCallingParty(call.CallingPartySSI)
		brewID := parseUUIDPrefer(callID, call.UUID)
		now := time.Now()
		session := &bridgeCall{
			NetstackID:    callID,
			BrewID:        brewID,
			SourceISSI:    source,
			BrewGSSI:      brewGSSI,
			MCC:           mcc,
			MNC:           mnc,
			State:         "new",
			CreatedAt:     now,
			LastEventAt:   now,
			RouteKey:      buildRouteKey(mcc, mnc, brewGSSI),
			Started:       false,
			PendingFrames: make([][]byte, 0, b.minTrafficFrames()),
		}
		if call.Traffic != nil {
			session.TrafficID = call.Traffic.UUID
		}

		added, staleCalls, blockedBy, blockedFor := b.registerPendingCall(session)
		for _, stale := range staleCalls {
			if stale.Started {
				b.logger.Printf(
					"netstack timeout-evict call=%s route=%s frames=%d age=%s -> release",
					stale.NetstackID,
					stale.RouteKey,
					stale.FrameCount,
					time.Since(stale.CreatedAt).Truncate(time.Millisecond),
				)
				b.svc.netstackReleaseCall(stale.BrewID, b.cfg.Netstack.ReleaseCause)
			} else {
				b.logger.Printf(
					"netstack timeout-evict call=%s route=%s frames=%d age=%s -> drop",
					stale.NetstackID,
					stale.RouteKey,
					stale.FrameCount,
					time.Since(stale.CreatedAt).Truncate(time.Millisecond),
				)
			}
		}
		if !added {
			b.logger.Printf(
				"netstack call/new blocked call=%s mcc=%d mnc=%d netstack_gssi=%d brew_gssi=%d route=%s blocked_by=%s wait=%s",
				callID,
				mcc,
				mnc,
				destination,
				brewGSSI,
				session.RouteKey,
				blockedBy,
				blockedFor.Truncate(time.Millisecond),
			)
			return
		}

		b.logger.Printf(
			"netstack transition call=%s new -> pending route=%s netstack_gssi=%d brew_gssi=%d threshold_frames=%d max_age=%s",
			callID,
			session.RouteKey,
			destination,
			brewGSSI,
			b.minTrafficFrames(),
			b.pendingMaxAge().String(),
		)

	case "tx-ceased":
		session, prevState := b.transitionCallState(callID, "tx-ceased")
		if session == nil {
			b.logger.Printf("netstack transition call=%s missing state=tx-ceased", callID)
			return
		}
		if !session.Started {
			b.logger.Printf(
				"netstack transition call=%s %s -> tx-ceased (drop low-traffic) frames=%d threshold=%d",
				callID,
				prevState,
				session.FrameCount,
				b.minTrafficFrames(),
			)
			b.dropCall(session)
			return
		}
		b.logger.Printf(
			"netstack transition call=%s %s -> tx-ceased tg=%d frames=%d age=%s",
			callID,
			prevState,
			session.BrewGSSI,
			session.FrameCount,
			time.Since(session.CreatedAt).Truncate(time.Millisecond),
		)
		b.svc.netstackIdleCall(session.BrewID, b.cfg.Netstack.ReleaseCause)

	case "released":
		session, prevState := b.transitionCallState(callID, "released")
		if session == nil {
			b.logger.Printf("netstack transition call=%s missing state=released", callID)
			return
		}
		if !session.Started {
			b.logger.Printf(
				"netstack transition call=%s %s -> released (drop low-traffic) frames=%d threshold=%d",
				callID,
				prevState,
				session.FrameCount,
				b.minTrafficFrames(),
			)
			b.dropCall(session)
			return
		}
		b.logger.Printf(
			"netstack transition call=%s %s -> released tg=%d frames=%d age=%s",
			callID,
			prevState,
			session.BrewGSSI,
			session.FrameCount,
			time.Since(session.CreatedAt).Truncate(time.Millisecond),
		)
		b.svc.netstackReleaseCall(session.BrewID, b.cfg.Netstack.ReleaseCause)
		b.dropCall(session)
	}
}

func (b *NetstackBridge) onTrafficMessage(_ mqtt.Client, msg mqtt.Message) {
	b.evictExpiredCalls()

	parts := strings.Split(msg.Topic(), "/")
	if len(parts) < 5 {
		return
	}
	trafficID := parts[4]

	session := b.getCallByTraffic(trafficID)
	if session == nil {
		return
	}

	frames, err := b.decodeTraffic(msg.Payload())
	if err != nil {
		b.logger.Printf("netstack traffic decode error: %v", err)
		return
	}
	if len(frames) == 0 {
		b.logger.Printf("netstack traffic dropped call=%s traffic=%s reason=empty-frame", session.NetstackID, trafficID)
		return
	}

	if len(frames) > 1 {
		b.logger.Printf(
			"netstack traffic call=%s traffic=%s contains_frames=%d",
			session.NetstackID,
			trafficID,
			len(frames),
		)
	}

	for _, frame := range frames {
		snapshot, flushFrames, activate := b.onTrafficFrame(session.NetstackID, frame)
		if snapshot == nil {
			return
		}

		if activate {
			if !b.svc.netstackStartCall(snapshot.BrewID, snapshot.SourceISSI, snapshot.BrewGSSI) {
				b.logger.Printf(
					"netstack transition call=%s pending -> dropped reason=no-subscribers route=%s",
					snapshot.NetstackID,
					snapshot.RouteKey,
				)
				b.dropCall(snapshot)
				return
			}
			b.markCallActivated(snapshot.NetstackID)
			b.logger.Printf(
				"netstack transition call=%s pending -> active tg=%d frames=%d buffered=%d",
				snapshot.NetstackID,
				snapshot.BrewGSSI,
				snapshot.FrameCount,
				len(flushFrames),
			)
			for _, pending := range flushFrames {
				b.svc.netstackVoiceFrame(snapshot.BrewID, pending)
			}
			continue
		}

		if snapshot.Started {
			b.svc.netstackVoiceFrame(snapshot.BrewID, frame)
		}
	}
}

func (b *NetstackBridge) decodeTraffic(payload []byte) ([][]byte, error) {
	var raw []byte
	switch b.cfg.Netstack.Encoding {
	case "hex":
		decoded, err := hex.DecodeString(strings.TrimSpace(string(payload)))
		if err != nil {
			return nil, err
		}
		raw = decoded
	default:
		raw = append([]byte(nil), payload...)
	}
	return decodeRawTrafficFrames(raw)
}

func decodeRawTrafficFrames(raw []byte) ([][]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	// Already STE payload: 36-byte frames.
	if len(raw)%36 == 0 {
		out := make([][]byte, 0, len(raw)/36)
		for off := 0; off < len(raw); off += 36 {
			out = append(out, append([]byte(nil), raw[off:off+36]...))
		}
		return out, nil
	}

	// Packed channel bits without STE header: 35 bytes per frame.
	if len(raw)%35 == 0 {
		out := make([][]byte, 0, len(raw)/35)
		for off := 0; off < len(raw); off += 35 {
			out = append(out, channelPacked35ToSTE(raw[off:off+35]))
		}
		return out, nil
	}

	// One-bit-per-byte channel bits: 274 bytes per frame.
	if len(raw)%274 == 0 && allBytesAreBits(raw) {
		out := make([][]byte, 0, len(raw)/274)
		for off := 0; off < len(raw); off += 274 {
			out = append(out, channelBitsToSTE(raw[off:off+274]))
		}
		return out, nil
	}

	// Fallback for netstack payloads carrying soft/expanded channel frames.
	// Commonly seen as 1380 bytes (2 * 690) or 690 bytes.
	if len(raw)%1380 == 0 {
		out := make([][]byte, 0, (len(raw)/1380)*2)
		for off := 0; off < len(raw); off += 1380 {
			out = append(out, channelSoft690ToSTE(raw[off:off+690]))
			out = append(out, channelSoft690ToSTE(raw[off+690:off+1380]))
		}
		return out, nil
	}
	if len(raw)%690 == 0 {
		out := make([][]byte, 0, len(raw)/690)
		for off := 0; off < len(raw); off += 690 {
			out = append(out, channelSoft690ToSTE(raw[off:off+690]))
		}
		return out, nil
	}

	return nil, fmt.Errorf("unsupported traffic payload length=%d", len(raw))
}

func channelPacked35ToSTE(packed []byte) []byte {
	bits := unpackPacked274Bits(packed)
	return channelBitsToSTE(bits)
}

func channelBitsToSTE(channelBits []byte) []byte {
	var channel [274]byte
	copy(channel[:], channelBits)
	codecBits := channelToCodec(channel)

	ste := make([]byte, 36)
	ste[0] = 0x00
	for i := 0; i < 35; i++ {
		var v byte
		for bit := 0; bit < 8; bit++ {
			idx := i*8 + bit
			if idx < 274 {
				v |= (codecBits[idx] & 1) << (7 - bit)
			}
		}
		ste[i+1] = v
	}
	return ste
}

func unpackPacked274Bits(packed []byte) []byte {
	out := make([]byte, 274)
	for i := 0; i < 274; i++ {
		b := packed[i/8]
		shift := 7 - (i % 8)
		out[i] = (b >> shift) & 1
	}
	return out
}

func allBytesAreBits(data []byte) bool {
	for _, v := range data {
		if v != 0 && v != 1 {
			return false
		}
	}
	return true
}

func channelToCodec(channel [274]byte) [274]byte {
	var codec [274]byte
	inIdx := 0

	for _, pos := range class0Pos {
		p := pos - 1
		codec[p] = channel[inIdx]
		codec[137+p] = channel[inIdx+1]
		inIdx += 2
	}
	for _, pos := range class1Pos {
		p := pos - 1
		codec[p] = channel[inIdx]
		codec[137+p] = channel[inIdx+1]
		inIdx += 2
	}
	for _, pos := range class2Pos {
		p := pos - 1
		codec[p] = channel[inIdx]
		codec[137+p] = channel[inIdx+1]
		inIdx += 2
	}
	return codec
}

func channelSoft690ToSTE(data []byte) []byte {
	bits := make([]byte, 274)

	// Best-effort path: signed 16-bit soft samples.
	if len(data)%2 == 0 && len(data)/2 >= 274 {
		for i := 0; i < 274; i++ {
			s := int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
			if s > 0 {
				bits[i] = 1
			}
		}
		return channelBitsToSTE(bits)
	}

	// Fallback path: 8-bit values.
	limit := 274
	if len(data) < limit {
		limit = len(data)
	}
	for i := 0; i < limit; i++ {
		v := data[i]
		if v == 0 || v == 1 {
			bits[i] = v
			continue
		}
		if v >= 128 {
			bits[i] = 1
		}
	}
	return channelBitsToSTE(bits)
}

var class0Pos = []int{
	35, 36, 37, 38, 39, 40, 41, 42, 43, 47, 48, 56, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 74, 75, 83, 88, 89, 90, 91, 92, 93, 94, 95, 96,
	97, 101, 102, 110, 115, 116, 117, 118, 119, 120, 121, 122, 123, 124, 128, 129, 137,
}

var class1Pos = []int{
	58, 85, 112, 54, 81, 108, 135, 50, 77, 104, 131, 45, 72, 99, 126, 55, 82, 109, 136, 5, 13, 34, 8, 16, 17, 22, 23, 24, 25, 26, 6, 14, 7,
	15, 60, 87, 114, 46, 73, 100, 127, 44, 71, 98, 125, 33, 49, 76, 103, 130, 59, 86, 113, 57, 84, 111,
}

var class2Pos = []int{
	18, 19, 20, 21, 31, 32, 53, 80, 107, 134, 1, 2, 3, 4, 9, 10, 11, 12, 27, 28, 29, 30, 52, 79, 106, 133, 51, 78, 105, 132,
}

func (b *NetstackBridge) mapNetstackGSSIToBrewGSSI(mcc, mnc int, netstackGSSI uint32) uint32 {
	for _, route := range b.routes {
		if route.MCC != 0 && route.MCC != mcc {
			continue
		}
		if route.MNC != 0 && route.MNC != mnc {
			continue
		}
		if !routeMatchesNetstackGSSI(route, netstackGSSI) {
			continue
		}
		if mapped := routeTargetBrewGSSI(route); mapped != 0 {
			return mapped
		}
	}
	return netstackGSSI
}

func (b *NetstackBridge) getCall(id string) *bridgeCall {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.callsByID[id]
}

func (b *NetstackBridge) getCallByTraffic(trafficID string) *bridgeCall {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.callsByTraffic[trafficID]
}

func (b *NetstackBridge) transitionCallState(callID, nextState string) (*bridgeCall, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	call := b.callsByID[callID]
	if call == nil {
		return nil, ""
	}
	prev := call.State
	call.State = nextState
	call.LastEventAt = time.Now()
	b.touchRouteLockNoLock(call.RouteKey, call.NetstackID)
	return call, prev
}

func (b *NetstackBridge) dropCall(call *bridgeCall) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dropCallNoLock(call)
	b.releaseRouteLockNoLock(call.RouteKey, call.NetstackID)
}

func (b *NetstackBridge) dropCallNoLock(call *bridgeCall) {
	delete(b.callsByID, call.NetstackID)
	if call.TrafficID != "" {
		delete(b.callsByTraffic, call.TrafficID)
	}
}

func (b *NetstackBridge) onTrafficFrame(callID string, frame []byte) (*bridgeCall, [][]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	call := b.callsByID[callID]
	if call == nil {
		return nil, nil, false
	}
	now := time.Now()
	call.FrameCount++
	call.LastEventAt = now

	if !call.Started && now.Sub(call.CreatedAt) > b.pendingMaxAge() {
		b.logger.Printf(
			"netstack transition call=%s pending -> dropped reason=low-throughput frames=%d threshold=%d age=%s max_age=%s",
			call.NetstackID,
			call.FrameCount,
			b.minTrafficFrames(),
			now.Sub(call.CreatedAt).Truncate(time.Millisecond),
			b.pendingMaxAge().String(),
		)
		b.dropCallNoLock(call)
		b.releaseRouteLockNoLock(call.RouteKey, call.NetstackID)
		return nil, nil, false
	}

	if call.Started {
		b.touchRouteLockNoLock(call.RouteKey, call.NetstackID)
	}

	if call.FrameCount == 1 || call.FrameCount%100 == 0 {
		b.logger.Printf(
			"netstack traffic call=%s state=%s tg=%d frame=%d size=%dB",
			call.NetstackID,
			call.State,
			call.BrewGSSI,
			call.FrameCount,
			len(frame),
		)
	}

	if call.Started {
		return call, nil, false
	}

	call.PendingFrames = append(call.PendingFrames, append([]byte(nil), frame...))
	if int(call.FrameCount) < b.minTrafficFrames() {
		return call, nil, false
	}

	flush := append([][]byte(nil), call.PendingFrames...)
	call.PendingFrames = nil
	call.State = "activating"
	return call, flush, true
}

func (b *NetstackBridge) markCallActivated(callID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	call := b.callsByID[callID]
	if call == nil {
		return
	}
	call.Started = true
	call.State = "active"
	call.LastEventAt = time.Now()
	b.armRouteLockNoLock(call.RouteKey, call.NetstackID, b.routeTimeout())
}

func (b *NetstackBridge) registerPendingCall(session *bridgeCall) (bool, []*bridgeCall, string, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	stale := make([]*bridgeCall, 0, 2)

	if existingLock, ok := b.routeLocks[session.RouteKey]; ok && existingLock.CallID != session.NetstackID {
		if now.Before(existingLock.ExpiresAt) {
			return false, nil, existingLock.CallID, time.Until(existingLock.ExpiresAt)
		}
		if staleCall := b.callsByID[existingLock.CallID]; staleCall != nil {
			stale = append(stale, staleCall)
			b.dropCallNoLock(staleCall)
		}
		delete(b.routeLocks, session.RouteKey)
	}

	if prev := b.callsByID[session.NetstackID]; prev != nil {
		stale = append(stale, prev)
		b.dropCallNoLock(prev)
		b.releaseRouteLockNoLock(prev.RouteKey, prev.NetstackID)
	}

	b.callsByID[session.NetstackID] = session
	if session.TrafficID != "" {
		b.callsByTraffic[session.TrafficID] = session
	}
	b.armRouteLockNoLock(session.RouteKey, session.NetstackID, b.pendingMaxAge())
	return true, stale, "", 0
}

func (b *NetstackBridge) evictExpiredCalls() {
	now := time.Now()
	toRelease := make([]*bridgeCall, 0)
	toDrop := make([]*bridgeCall, 0)

	b.mu.Lock()
	for route, lock := range b.routeLocks {
		if now.Before(lock.ExpiresAt) {
			continue
		}
		call := b.callsByID[lock.CallID]
		if call != nil {
			if call.Started {
				toRelease = append(toRelease, call)
			} else {
				toDrop = append(toDrop, call)
			}
			b.dropCallNoLock(call)
		}
		delete(b.routeLocks, route)
	}
	b.mu.Unlock()

	for _, call := range toDrop {
		b.logger.Printf(
			"netstack timeout drop call=%s route=%s frames=%d threshold=%d",
			call.NetstackID,
			call.RouteKey,
			call.FrameCount,
			b.minTrafficFrames(),
		)
	}
	for _, call := range toRelease {
		b.logger.Printf(
			"netstack timeout release call=%s route=%s frames=%d",
			call.NetstackID,
			call.RouteKey,
			call.FrameCount,
		)
		b.svc.netstackReleaseCall(call.BrewID, b.cfg.Netstack.ReleaseCause)
	}
}

func (b *NetstackBridge) expiryLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.evictExpiredCalls()
		}
	}
}

func (b *NetstackBridge) touchRouteLockNoLock(routeKey, callID string) {
	lock, ok := b.routeLocks[routeKey]
	if !ok || lock.CallID != callID {
		return
	}
	lock.ExpiresAt = time.Now().Add(b.routeTimeout())
}

func (b *NetstackBridge) armRouteLockNoLock(routeKey, callID string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	b.routeLocks[routeKey] = &routeLock{
		CallID:    callID,
		ExpiresAt: time.Now().Add(ttl),
	}
}

func (b *NetstackBridge) releaseRouteLockNoLock(routeKey, callID string) {
	lock, ok := b.routeLocks[routeKey]
	if !ok {
		return
	}
	if callID != "" && lock.CallID != callID {
		return
	}
	delete(b.routeLocks, routeKey)
}

func (b *NetstackBridge) minTrafficFrames() int {
	if b.cfg.Netstack.MinTrafficFrames < 1 {
		return 1
	}
	return b.cfg.Netstack.MinTrafficFrames
}

func (b *NetstackBridge) routeTimeout() time.Duration {
	if b.cfg.Netstack.RouteCallTimeout <= 0 {
		return 30 * time.Second
	}
	return b.cfg.Netstack.RouteCallTimeout
}

func (b *NetstackBridge) pendingMaxAge() time.Duration {
	if b.cfg.Netstack.PendingMaxAge <= 0 {
		return 2 * time.Second
	}
	return b.cfg.Netstack.PendingMaxAge
}

func buildRouteKey(mcc, mnc int, brewGSSI uint32) string {
	return fmt.Sprintf("%d:%d:%d", mcc, mnc, brewGSSI)
}

func parseCallingParty(raw json.RawMessage) uint32 {
	if len(raw) == 0 {
		return 0
	}

	var asNumber uint32
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		return asNumber
	}

	var asSigned int32
	if err := json.Unmarshal(raw, &asSigned); err == nil && asSigned > 0 {
		return uint32(asSigned)
	}

	var asNullInt struct {
		Int32 int32 `json:"Int32"`
		Valid bool  `json:"Valid"`
	}
	if err := json.Unmarshal(raw, &asNullInt); err == nil && asNullInt.Valid && asNullInt.Int32 > 0 {
		return uint32(asNullInt.Int32)
	}
	return 0
}

func parseUUIDPrefer(values ...string) uuid.UUID {
	for _, v := range values {
		id, err := uuid.Parse(v)
		if err == nil {
			return id
		}
	}
	return uuid.New()
}

func parseInt(v string) int {
	var out int
	_, _ = fmt.Sscanf(v, "%d", &out)
	return out
}

func loadRoutes(path string) ([]netstackRoute, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read route file %q: %w", path, err)
	}

	var list []netstackRoute
	if err := json.Unmarshal(content, &list); err == nil {
		return list, nil
	}

	var wrapped routeEnvelope
	if err := json.Unmarshal(content, &wrapped); err == nil {
		if len(wrapped.Routes) > 0 {
			return wrapped.Routes, nil
		}
		if len(wrapped.Mappings) > 0 {
			return wrapped.Mappings, nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("invalid route file format: %s", path)
}

func routeMatchesNetstackGSSI(route netstackRoute, netstackGSSI uint32) bool {
	if route.NetstackGSSI > 0 && route.NetstackGSSI != netstackGSSI {
		return false
	}
	if route.GSSI > 0 && route.GSSI != netstackGSSI {
		return false
	}

	min := route.NetstackGSSIMin
	max := route.NetstackGSSIMax
	if min == 0 {
		min = route.GSSIMin
	}
	if max == 0 {
		max = route.GSSIMax
	}
	if min == 0 && max == 0 {
		return route.NetstackGSSI > 0 || route.GSSI > 0 || (route.NetstackGSSI == 0 && route.GSSI == 0)
	}
	if max == 0 {
		max = min
	}
	if min == 0 {
		min = max
	}
	return netstackGSSI >= min && netstackGSSI <= max
}

func routeTargetBrewGSSI(route netstackRoute) uint32 {
	if route.BrewGSSI > 0 {
		return route.BrewGSSI
	}
	// Backward compatibility with old key name.
	if route.Talkgroup > 0 {
		return route.Talkgroup
	}
	return 0
}
