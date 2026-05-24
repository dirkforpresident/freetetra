package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/freetetra/server/internal/brew"
	"github.com/freetetra/server/internal/config"
)

// ProxyPlane is the narrow surface of BrewModulePlane that ProxyBridge needs.
// Kept small so tests can supply a fake.
//
// SendSetupRequest / SendConnectRequest take a full CircularCallPayload so the
// proxy can mirror inbound call-mode flags (Duplex, Method, Service,
// Communication, Priority) onto the outbound leg. A naive (source, dest)-only
// API would default everything to zero — making the outbound leg simplex even
// when the inbound caller dialed duplex.
type ProxyPlane interface {
	SendSetupRequest(callID uuid.UUID, payload brew.CircularCallPayload) bool
	SendSetupAccept(callID uuid.UUID) bool
	SendSetupReject(callID uuid.UUID, cause uint8) bool
	SendConnectRequest(callID uuid.UUID, payload brew.CircularCallPayload) bool
	SendCallRelease(callID uuid.UUID, cause uint8) bool
	InjectedVoiceFrame(origin string, callID uuid.UUID, data []byte)
}

const (
	proxyCauseNormal uint8 = 0
	proxyCauseBusy   uint8 = 17
)

type proxyState uint8

const (
	proxyStateDialing proxyState = iota
	proxyStateConnected
	proxyStateReleasing
)

type proxySession struct {
	inboundCallID  uuid.UUID
	outboundCallID uuid.UUID
	callerISSI     uint32
	state          proxyState
	startedAt      time.Time
	lastFrameAt    time.Time
	// callMode captures the inbound SetupRequest's call-mode flags
	// (Duplex, Method, Service, Communication, Priority) so the outbound
	// leg's SetupRequest + ConnectRequest mirror them. Source/Destination
	// are NOT taken from here — they're substituted with bridge/target.
	callMode brew.CircularCallPayload
}

// ProxyBridge auto-accepts inbound private calls to a bridge ISSI and dials
// a configured target ISSI, relaying voice and call-control between the two
// legs. Federation routing is transparent: the outbound leg uses whatever the
// hub's routing logic decides (local subscriber, federated peer, etc).
type ProxyBridge struct {
	cfg    config.Config
	logger *log.Logger
	plane  ProxyPlane

	bridgeISSI    uint32
	targetISSI    uint32
	dialTimeout   time.Duration
	idleTimeout   time.Duration
	maxConcurrent int

	mu       sync.Mutex
	sessions map[uuid.UUID]*proxySession // indexed by BOTH inbound and outbound call IDs

	cancel context.CancelFunc
	done   chan struct{}
	now    func() time.Time
	tick   time.Duration
}

func NewProxyBridge(cfg config.Config, logger *log.Logger, plane ProxyPlane) (*ProxyBridge, error) {
	if cfg.Proxy.BridgeISSI == 0 {
		return nil, fmt.Errorf("PROXY_BRIDGE_ISSI must be > 0")
	}
	if cfg.Proxy.TargetISSI == 0 {
		return nil, fmt.Errorf("PROXY_TARGET_ISSI must be > 0")
	}
	dial := cfg.Proxy.DialTimeout
	if dial <= 0 {
		dial = 10 * time.Second
	}
	idle := cfg.Proxy.IdleTimeout
	if idle <= 0 {
		idle = 60 * time.Second
	}
	maxc := cfg.Proxy.MaxConcurrent
	if maxc <= 0 {
		maxc = 4
	}
	return &ProxyBridge{
		cfg:           cfg,
		logger:        logger,
		plane:         plane,
		bridgeISSI:    cfg.Proxy.BridgeISSI,
		targetISSI:    cfg.Proxy.TargetISSI,
		dialTimeout:   dial,
		idleTimeout:   idle,
		maxConcurrent: maxc,
		sessions:      make(map[uuid.UUID]*proxySession),
		now:           time.Now,
		tick:          1 * time.Second,
	}, nil
}

func (b *ProxyBridge) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	if b.cancel != nil {
		b.mu.Unlock()
		cancel()
		return fmt.Errorf("proxy bridge already started")
	}
	b.cancel = cancel
	b.done = make(chan struct{})
	done := b.done
	b.mu.Unlock()

	go func() {
		defer close(done)
		b.watchdog(runCtx)
	}()
	b.logger.Printf(
		"proxy bridge enabled bridge_issi=%d target_issi=%d dial_timeout=%s idle_timeout=%s max_concurrent=%d",
		b.bridgeISSI,
		b.targetISSI,
		b.dialTimeout.String(),
		b.idleTimeout.String(),
		b.maxConcurrent,
	)
	return nil
}

func (b *ProxyBridge) Stop() {
	b.mu.Lock()
	cancel := b.cancel
	done := b.done
	b.cancel = nil
	b.done = nil
	// Release all active sessions; collect unique sessions first.
	seen := make(map[*proxySession]struct{}, len(b.sessions))
	releases := make([][2]uuid.UUID, 0, len(b.sessions))
	for _, s := range b.sessions {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		releases = append(releases, [2]uuid.UUID{s.inboundCallID, s.outboundCallID})
	}
	b.sessions = make(map[uuid.UUID]*proxySession)
	b.mu.Unlock()

	for _, pair := range releases {
		b.plane.SendCallRelease(pair[0], proxyCauseNormal)
		b.plane.SendCallRelease(pair[1], proxyCauseNormal)
	}

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (b *ProxyBridge) OnBrewCallControl(m *brew.CallControlMessage) {
	if m == nil {
		return
	}
	switch m.CallState {
	case brew.CallStateSetupRequest:
		p, ok := m.Payload.(brew.CircularCallPayload)
		if !ok || p.Destination != b.bridgeISSI {
			return
		}
		b.handleInboundSetup(m.Identifier, p)
	case brew.CallStateSetupAccept:
		b.handleOutboundAccept(m.Identifier)
	case brew.CallStateSetupReject:
		cause := uint8(proxyCauseNormal)
		if cp, ok := m.Payload.(brew.CausePayload); ok {
			cause = cp.Cause
		}
		b.handleLegFailure(m.Identifier, cause, "setup-reject")
	case brew.CallStateCallRelease:
		cause := uint8(proxyCauseNormal)
		if cp, ok := m.Payload.(brew.CausePayload); ok {
			cause = cp.Cause
		}
		b.handleLegFailure(m.Identifier, cause, "release")
	}
}

func (b *ProxyBridge) handleInboundSetup(inboundCallID uuid.UUID, p brew.CircularCallPayload) {
	b.mu.Lock()
	// If we already track this call ID (duplicate), ignore.
	if _, exists := b.sessions[inboundCallID]; exists {
		b.mu.Unlock()
		return
	}
	// Concurrency cap based on unique sessions.
	if b.uniqueSessionCountLocked() >= b.maxConcurrent {
		b.mu.Unlock()
		b.logger.Printf(
			"proxy reject inbound caller=%d call=%s reason=max-concurrent",
			p.Source,
			inboundCallID.String(),
		)
		b.plane.SendSetupReject(inboundCallID, proxyCauseBusy)
		return
	}
	outboundCallID := uuid.New()
	now := b.now()
	s := &proxySession{
		inboundCallID:  inboundCallID,
		outboundCallID: outboundCallID,
		callerISSI:     p.Source,
		state:          proxyStateDialing,
		startedAt:      now,
		lastFrameAt:    now,
		callMode:       p,
	}
	b.sessions[inboundCallID] = s
	b.sessions[outboundCallID] = s
	b.mu.Unlock()

	b.logger.Printf(
		"proxy inbound setup caller=%d bridge_issi=%d inbound_call=%s outbound_call=%s target_issi=%d duplex=%d",
		p.Source,
		b.bridgeISSI,
		inboundCallID.String(),
		outboundCallID.String(),
		b.targetISSI,
		p.Duplex,
	)
	b.plane.SendSetupAccept(inboundCallID)
	b.plane.SendSetupRequest(outboundCallID, b.outboundPayload(s))
}

// outboundPayload returns the CircularCallPayload to use for outbound
// SetupRequest / ConnectRequest. It mirrors the inbound call-mode flags
// (Duplex, Method, Service, Communication, Priority) but substitutes
// bridge-as-source / target-as-destination. Number / Grant / Permission /
// Timeout / Ownership / Queued reset to zero — they're inbound-only
// concepts and not meaningful to forward.
func (b *ProxyBridge) outboundPayload(s *proxySession) brew.CircularCallPayload {
	return brew.CircularCallPayload{
		Source:        b.bridgeISSI,
		Destination:   b.targetISSI,
		Priority:      s.callMode.Priority,
		Service:       s.callMode.Service,
		Mode:          s.callMode.Mode,
		Duplex:        s.callMode.Duplex,
		Method:        s.callMode.Method,
		Communication: s.callMode.Communication,
	}
}

func (b *ProxyBridge) handleOutboundAccept(callID uuid.UUID) {
	b.mu.Lock()
	s := b.sessions[callID]
	if s == nil || callID != s.outboundCallID {
		b.mu.Unlock()
		return
	}
	if s.state != proxyStateDialing {
		b.mu.Unlock()
		return
	}
	s.state = proxyStateConnected
	s.lastFrameAt = b.now()
	inbound := s.inboundCallID
	outbound := s.outboundCallID
	payload := b.outboundPayload(s)
	b.mu.Unlock()

	b.logger.Printf(
		"proxy outbound accept inbound_call=%s outbound_call=%s",
		inbound.String(),
		outbound.String(),
	)
	b.plane.SendConnectRequest(outbound, payload)
}

func (b *ProxyBridge) handleLegFailure(callID uuid.UUID, cause uint8, reason string) {
	b.mu.Lock()
	s := b.sessions[callID]
	if s == nil {
		b.mu.Unlock()
		return
	}
	if s.state == proxyStateReleasing {
		// Already releasing — just drop the entry.
		delete(b.sessions, callID)
		// Both entries gone? sessions already cleared on first release path.
		b.mu.Unlock()
		return
	}
	s.state = proxyStateReleasing
	otherCallID := s.inboundCallID
	if callID == s.inboundCallID {
		otherCallID = s.outboundCallID
	}
	delete(b.sessions, s.inboundCallID)
	delete(b.sessions, s.outboundCallID)
	inbound := s.inboundCallID
	outbound := s.outboundCallID
	b.mu.Unlock()

	b.logger.Printf(
		"proxy leg %s call=%s cause=%d inbound_call=%s outbound_call=%s release_other=%s",
		reason,
		callID.String(),
		cause,
		inbound.String(),
		outbound.String(),
		otherCallID.String(),
	)
	b.plane.SendCallRelease(otherCallID, cause)
}

func (b *ProxyBridge) OnBrewFrame(callID uuid.UUID, frameType uint8, data []byte) {
	if frameType != brew.FrameTypeTrafficChannel {
		return
	}
	b.mu.Lock()
	s := b.sessions[callID]
	if s == nil {
		b.mu.Unlock()
		return
	}
	var target uuid.UUID
	if callID == s.inboundCallID {
		target = s.outboundCallID
	} else {
		target = s.inboundCallID
	}
	s.lastFrameAt = b.now()
	b.mu.Unlock()

	frameCopy := append([]byte(nil), data...)
	b.plane.InjectedVoiceFrame("proxy", target, frameCopy)
}

func (b *ProxyBridge) watchdog(ctx context.Context) {
	t := time.NewTicker(b.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.sweep()
		}
	}
}

func (b *ProxyBridge) sweep() {
	now := b.now()
	type expired struct {
		inbound  uuid.UUID
		outbound uuid.UUID
		reason   string
	}
	var toRelease []expired

	b.mu.Lock()
	seen := make(map[*proxySession]struct{}, len(b.sessions))
	for _, s := range b.sessions {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		if s.state == proxyStateReleasing {
			continue
		}
		switch s.state {
		case proxyStateDialing:
			if now.Sub(s.startedAt) > b.dialTimeout {
				s.state = proxyStateReleasing
				toRelease = append(toRelease, expired{s.inboundCallID, s.outboundCallID, "dial-timeout"})
				delete(b.sessions, s.inboundCallID)
				delete(b.sessions, s.outboundCallID)
			}
		case proxyStateConnected:
			if now.Sub(s.lastFrameAt) > b.idleTimeout {
				s.state = proxyStateReleasing
				toRelease = append(toRelease, expired{s.inboundCallID, s.outboundCallID, "idle-timeout"})
				delete(b.sessions, s.inboundCallID)
				delete(b.sessions, s.outboundCallID)
			}
		}
	}
	b.mu.Unlock()

	for _, e := range toRelease {
		b.logger.Printf(
			"proxy watchdog release reason=%s inbound_call=%s outbound_call=%s",
			e.reason,
			e.inbound.String(),
			e.outbound.String(),
		)
		b.plane.SendCallRelease(e.inbound, proxyCauseNormal)
		b.plane.SendCallRelease(e.outbound, proxyCauseNormal)
	}
}

func (b *ProxyBridge) uniqueSessionCountLocked() int {
	seen := make(map[*proxySession]struct{}, len(b.sessions))
	for _, s := range b.sessions {
		seen[s] = struct{}{}
	}
	return len(seen)
}
