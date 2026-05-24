package service

import (
	"context"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/freetetra/server/internal/brew"
	"github.com/freetetra/server/internal/config"
)

type fakeEvent struct {
	kind    string // "setup-req", "setup-accept", "setup-reject", "connect-req", "release", "voice"
	callID  uuid.UUID
	source  uint32
	dest    uint32
	duplex  uint8
	cause   uint8
	data    []byte
}

type fakeProxyPlane struct {
	mu     sync.Mutex
	events []fakeEvent
}

func (f *fakeProxyPlane) SendSetupRequest(callID uuid.UUID, p brew.CircularCallPayload) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fakeEvent{
		kind:   "setup-req",
		callID: callID,
		source: p.Source,
		dest:   p.Destination,
		duplex: p.Duplex,
	})
	return true
}

func (f *fakeProxyPlane) SendSetupAccept(callID uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fakeEvent{kind: "setup-accept", callID: callID})
	return true
}

func (f *fakeProxyPlane) SendSetupReject(callID uuid.UUID, cause uint8) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fakeEvent{kind: "setup-reject", callID: callID, cause: cause})
	return true
}

func (f *fakeProxyPlane) SendConnectRequest(callID uuid.UUID, p brew.CircularCallPayload) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fakeEvent{
		kind:   "connect-req",
		callID: callID,
		source: p.Source,
		dest:   p.Destination,
		duplex: p.Duplex,
	})
	return true
}

func (f *fakeProxyPlane) SendCallRelease(callID uuid.UUID, cause uint8) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fakeEvent{kind: "release", callID: callID, cause: cause})
	return true
}

func (f *fakeProxyPlane) InjectedVoiceFrame(_ string, callID uuid.UUID, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fakeEvent{kind: "voice", callID: callID, data: append([]byte(nil), data...)})
}

func (f *fakeProxyPlane) snapshot() []fakeEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeEvent, len(f.events))
	copy(out, f.events)
	return out
}

func (f *fakeProxyPlane) findKind(kind string) []fakeEvent {
	out := []fakeEvent{}
	for _, e := range f.snapshot() {
		if e.kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func newTestProxy(t *testing.T, bridgeISSI, targetISSI uint32, dialTimeout, idleTimeout time.Duration, maxConcurrent int) (*ProxyBridge, *fakeProxyPlane) {
	t.Helper()
	plane := &fakeProxyPlane{}
	cfg := config.Config{
		Proxy: config.ProxyConfig{
			BridgeISSI:    bridgeISSI,
			TargetISSI:    targetISSI,
			DialTimeout:   dialTimeout,
			IdleTimeout:   idleTimeout,
			MaxConcurrent: maxConcurrent,
		},
	}
	b, err := NewProxyBridge(cfg, log.New(io.Discard, "", 0), plane)
	if err != nil {
		t.Fatalf("NewProxyBridge: %v", err)
	}
	return b, plane
}

func TestProxyBridge_HappyPath(t *testing.T) {
	b, plane := newTestProxy(t, 999, 1002, 10*time.Second, 60*time.Second, 4)

	inboundCallID := uuid.New()
	b.OnBrewCallControl(&brew.CallControlMessage{
		CallState:  brew.CallStateSetupRequest,
		Identifier: inboundCallID,
		Payload:    brew.CircularCallPayload{Source: 1001, Destination: 999, Duplex: 1},
	})

	accepts := plane.findKind("setup-accept")
	if len(accepts) != 1 || accepts[0].callID != inboundCallID {
		t.Fatalf("expected SetupAccept on inbound, got %#v", accepts)
	}
	setupReqs := plane.findKind("setup-req")
	if len(setupReqs) != 1 {
		t.Fatalf("expected one outbound SetupRequest, got %d", len(setupReqs))
	}
	outboundCallID := setupReqs[0].callID
	if setupReqs[0].source != 999 || setupReqs[0].dest != 1002 {
		t.Fatalf("outbound SetupRequest src/dst: src=%d dst=%d", setupReqs[0].source, setupReqs[0].dest)
	}
	if setupReqs[0].duplex != 1 {
		t.Fatalf("outbound SetupRequest duplex flag not mirrored: got %d want 1", setupReqs[0].duplex)
	}
	if outboundCallID == inboundCallID {
		t.Fatalf("outbound and inbound call IDs collide")
	}

	// Outbound accepted -> expect ConnectRequest.
	b.OnBrewCallControl(&brew.CallControlMessage{
		CallState:  brew.CallStateSetupAccept,
		Identifier: outboundCallID,
		Payload:    brew.EmptyPayload{},
	})
	connects := plane.findKind("connect-req")
	if len(connects) != 1 || connects[0].callID != outboundCallID || connects[0].source != 999 || connects[0].dest != 1002 {
		t.Fatalf("expected ConnectRequest on outbound 999->1002, got %#v", connects)
	}
	if connects[0].duplex != 1 {
		t.Fatalf("outbound ConnectRequest duplex flag not mirrored: got %d want 1", connects[0].duplex)
	}

	// Voice on inbound -> relayed to outbound.
	b.OnBrewFrame(inboundCallID, brew.FrameTypeTrafficChannel, []byte{0x01, 0x02, 0x03})
	// Voice on outbound -> relayed to inbound.
	b.OnBrewFrame(outboundCallID, brew.FrameTypeTrafficChannel, []byte{0x04, 0x05})

	voices := plane.findKind("voice")
	if len(voices) != 2 {
		t.Fatalf("expected 2 voice events, got %d", len(voices))
	}
	if voices[0].callID != outboundCallID || string(voices[0].data) != string([]byte{0x01, 0x02, 0x03}) {
		t.Fatalf("first voice mis-routed: %#v", voices[0])
	}
	if voices[1].callID != inboundCallID || string(voices[1].data) != string([]byte{0x04, 0x05}) {
		t.Fatalf("second voice mis-routed: %#v", voices[1])
	}

	// Non-traffic frames are ignored.
	plane.mu.Lock()
	prev := len(plane.events)
	plane.mu.Unlock()
	b.OnBrewFrame(inboundCallID, brew.FrameTypeSDSTransfer, []byte{0xaa})
	plane.mu.Lock()
	after := len(plane.events)
	plane.mu.Unlock()
	if after != prev {
		t.Fatalf("non-traffic frame produced events; before=%d after=%d", prev, after)
	}

	// Release on inbound -> release outbound with same cause.
	b.OnBrewCallControl(&brew.CallControlMessage{
		CallState:  brew.CallStateCallRelease,
		Identifier: inboundCallID,
		Payload:    brew.CausePayload{Cause: 7},
	})
	releases := plane.findKind("release")
	if len(releases) != 1 || releases[0].callID != outboundCallID || releases[0].cause != 7 {
		t.Fatalf("expected single release on outbound with cause=7, got %#v", releases)
	}
}

func TestProxyBridge_OutboundReject(t *testing.T) {
	b, plane := newTestProxy(t, 999, 1002, 10*time.Second, 60*time.Second, 4)

	inboundCallID := uuid.New()
	b.OnBrewCallControl(&brew.CallControlMessage{
		CallState:  brew.CallStateSetupRequest,
		Identifier: inboundCallID,
		Payload:    brew.CircularCallPayload{Source: 1001, Destination: 999},
	})
	setupReqs := plane.findKind("setup-req")
	if len(setupReqs) != 1 {
		t.Fatalf("expected outbound setup")
	}
	outboundCallID := setupReqs[0].callID

	b.OnBrewCallControl(&brew.CallControlMessage{
		CallState:  brew.CallStateSetupReject,
		Identifier: outboundCallID,
		Payload:    brew.CausePayload{Cause: 5},
	})

	releases := plane.findKind("release")
	if len(releases) != 1 || releases[0].callID != inboundCallID || releases[0].cause != 5 {
		t.Fatalf("expected release on inbound cause=5, got %#v", releases)
	}

	// Subsequent voice on either call ID should be dropped (session gone).
	b.OnBrewFrame(inboundCallID, brew.FrameTypeTrafficChannel, []byte{0x01})
	b.OnBrewFrame(outboundCallID, brew.FrameTypeTrafficChannel, []byte{0x02})
	if v := plane.findKind("voice"); len(v) != 0 {
		t.Fatalf("expected no voice after release, got %d", len(v))
	}
}

func TestProxyBridge_DialTimeout(t *testing.T) {
	b, plane := newTestProxy(t, 999, 1002, 50*time.Millisecond, 60*time.Second, 4)
	b.tick = 10 * time.Millisecond

	inboundCallID := uuid.New()
	b.OnBrewCallControl(&brew.CallControlMessage{
		CallState:  brew.CallStateSetupRequest,
		Identifier: inboundCallID,
		Payload:    brew.CircularCallPayload{Source: 1001, Destination: 999},
	})
	setupReqs := plane.findKind("setup-req")
	if len(setupReqs) != 1 {
		t.Fatalf("expected outbound setup")
	}
	outboundCallID := setupReqs[0].callID

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer b.Stop()

	// Wait up to 500ms for the watchdog to fire.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(plane.findKind("release")) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	releases := plane.findKind("release")
	if len(releases) < 2 {
		t.Fatalf("expected both legs released, got %d", len(releases))
	}
	gotInbound := false
	gotOutbound := false
	for _, r := range releases {
		if r.callID == inboundCallID {
			gotInbound = true
		}
		if r.callID == outboundCallID {
			gotOutbound = true
		}
	}
	if !gotInbound || !gotOutbound {
		t.Fatalf("missing release for one leg: inbound=%v outbound=%v", gotInbound, gotOutbound)
	}
}

func TestProxyBridge_ConcurrencyCap(t *testing.T) {
	b, plane := newTestProxy(t, 999, 1002, 10*time.Second, 60*time.Second, 1)

	first := uuid.New()
	b.OnBrewCallControl(&brew.CallControlMessage{
		CallState:  brew.CallStateSetupRequest,
		Identifier: first,
		Payload:    brew.CircularCallPayload{Source: 1001, Destination: 999},
	})
	if len(plane.findKind("setup-req")) != 1 {
		t.Fatalf("first session not started")
	}
	if len(plane.findKind("setup-accept")) != 1 {
		t.Fatalf("first session not accepted")
	}

	second := uuid.New()
	b.OnBrewCallControl(&brew.CallControlMessage{
		CallState:  brew.CallStateSetupRequest,
		Identifier: second,
		Payload:    brew.CircularCallPayload{Source: 1003, Destination: 999},
	})

	rejects := plane.findKind("setup-reject")
	if len(rejects) != 1 || rejects[0].callID != second || rejects[0].cause != proxyCauseBusy {
		t.Fatalf("expected SetupReject(busy) on second call, got %#v", rejects)
	}
	// First session still has just one outbound setup-req (no second).
	if reqs := plane.findKind("setup-req"); len(reqs) != 1 {
		t.Fatalf("second session should not have triggered outbound setup; got %d", len(reqs))
	}
	// First session still alive (no releases emitted by reject path).
	if rel := plane.findKind("release"); len(rel) != 0 {
		t.Fatalf("first session unexpectedly released: %#v", rel)
	}
}
