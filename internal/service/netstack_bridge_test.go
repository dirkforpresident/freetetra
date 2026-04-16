package service

import (
	"io"
	"log"
	"testing"
	"time"

	"github.com/freetetra/server/internal/config"
)

func TestMapNetstackGSSIToBrewGSSI(t *testing.T) {
	bridge := &NetstackBridge{
		routes: []netstackRoute{
			{
				MCC:          204,
				MNC:          1,
				NetstackGSSI: 10001,
				BrewGSSI:     20001,
			},
			{
				MCC:             204,
				MNC:             1,
				NetstackGSSIMin: 11000,
				NetstackGSSIMax: 11999,
				BrewGSSI:        91000,
			},
		},
	}

	if got := bridge.mapNetstackGSSIToBrewGSSI(204, 1, 10001); got != 20001 {
		t.Fatalf("exact mapping failed: got %d", got)
	}
	if got := bridge.mapNetstackGSSIToBrewGSSI(204, 1, 11042); got != 91000 {
		t.Fatalf("range mapping failed: got %d", got)
	}
	if got := bridge.mapNetstackGSSIToBrewGSSI(204, 1, 50000); got != 50000 {
		t.Fatalf("fallback mapping failed: got %d", got)
	}
}

func TestLegacyRouteAliasesStillWork(t *testing.T) {
	bridge := &NetstackBridge{
		routes: []netstackRoute{
			{
				MCC:       204,
				MNC:       2,
				GSSIMin:   1000,
				GSSIMax:   1999,
				Talkgroup: 12345,
			},
		},
	}
	if got := bridge.mapNetstackGSSIToBrewGSSI(204, 2, 1500); got != 12345 {
		t.Fatalf("legacy alias mapping failed: got %d", got)
	}
}

func TestRegisterPendingCallBlocksRoute(t *testing.T) {
	bridge := &NetstackBridge{
		cfg: config.Config{
			Netstack: config.NetstackConfig{
				MinTrafficFrames: 3,
				PendingMaxAge:    2 * time.Second,
				RouteCallTimeout: 30 * time.Second,
			},
		},
		logger:         log.New(io.Discard, "", 0),
		callsByID:      make(map[string]*bridgeCall),
		callsByTraffic: make(map[string]*bridgeCall),
		routeLocks:     make(map[string]*routeLock),
	}

	first := &bridgeCall{
		NetstackID: "call-1",
		RouteKey:   buildRouteKey(204, 1, 10001),
		TrafficID:  "trf-1",
	}
	added, _, blockedBy, _ := bridge.registerPendingCall(first)
	if !added {
		t.Fatalf("first call should be accepted, blockedBy=%s", blockedBy)
	}

	second := &bridgeCall{
		NetstackID: "call-2",
		RouteKey:   buildRouteKey(204, 1, 10001),
		TrafficID:  "trf-2",
	}
	added, _, blockedBy, _ = bridge.registerPendingCall(second)
	if added {
		t.Fatalf("second call should be blocked on same route")
	}
	if blockedBy != "call-1" {
		t.Fatalf("unexpected blocker: %s", blockedBy)
	}
}

func TestOnTrafficFrameThresholdActivation(t *testing.T) {
	bridge := &NetstackBridge{
		cfg: config.Config{
			Netstack: config.NetstackConfig{
				MinTrafficFrames: 3,
				PendingMaxAge:    2 * time.Second,
				RouteCallTimeout: 30 * time.Second,
			},
		},
		logger:         log.New(io.Discard, "", 0),
		callsByID:      make(map[string]*bridgeCall),
		callsByTraffic: make(map[string]*bridgeCall),
		routeLocks:     make(map[string]*routeLock),
	}

	call := &bridgeCall{
		NetstackID:    "call-1",
		RouteKey:      buildRouteKey(204, 1, 10001),
		TrafficID:     "trf-1",
		State:         "new",
		CreatedAt:     time.Now(),
		PendingFrames: make([][]byte, 0, 3),
	}
	added, _, _, _ := bridge.registerPendingCall(call)
	if !added {
		t.Fatalf("call should be accepted")
	}

	for i := 0; i < 2; i++ {
		snapshot, flush, activate := bridge.onTrafficFrame("call-1", []byte{0x01})
		if snapshot == nil {
			t.Fatalf("missing snapshot at frame %d", i+1)
		}
		if activate {
			t.Fatalf("should not activate early at frame %d", i+1)
		}
		if len(flush) != 0 {
			t.Fatalf("unexpected flush at frame %d", i+1)
		}
	}

	snapshot, flush, activate := bridge.onTrafficFrame("call-1", []byte{0x02})
	if snapshot == nil {
		t.Fatalf("missing snapshot at threshold frame")
	}
	if !activate {
		t.Fatalf("expected activation at threshold frame")
	}
	if len(flush) != 3 {
		t.Fatalf("expected 3 buffered frames, got %d", len(flush))
	}
}

func TestPendingCallDropsWhenTooOld(t *testing.T) {
	bridge := &NetstackBridge{
		cfg: config.Config{
			Netstack: config.NetstackConfig{
				MinTrafficFrames: 8,
				PendingMaxAge:    20 * time.Millisecond,
				RouteCallTimeout: 30 * time.Second,
			},
		},
		logger:         log.New(io.Discard, "", 0),
		callsByID:      make(map[string]*bridgeCall),
		callsByTraffic: make(map[string]*bridgeCall),
		routeLocks:     make(map[string]*routeLock),
	}

	call := &bridgeCall{
		NetstackID:    "call-old",
		RouteKey:      buildRouteKey(204, 1, 10001),
		TrafficID:     "trf-old",
		State:         "pending",
		CreatedAt:     time.Now().Add(-100 * time.Millisecond),
		PendingFrames: make([][]byte, 0, 8),
	}
	added, _, _, _ := bridge.registerPendingCall(call)
	if !added {
		t.Fatalf("call should be accepted")
	}

	snapshot, flush, activate := bridge.onTrafficFrame("call-old", []byte{0x01})
	if snapshot != nil || len(flush) != 0 || activate {
		t.Fatalf("expired pending call should be dropped")
	}
	if bridge.getCall("call-old") != nil {
		t.Fatalf("expired pending call should be removed from call map")
	}
}

func TestPendingLockExpiresAndNextCallCanEnter(t *testing.T) {
	bridge := &NetstackBridge{
		cfg: config.Config{
			Netstack: config.NetstackConfig{
				MinTrafficFrames: 8,
				PendingMaxAge:    50 * time.Millisecond,
				RouteCallTimeout: 30 * time.Second,
			},
		},
		logger:         log.New(io.Discard, "", 0),
		callsByID:      make(map[string]*bridgeCall),
		callsByTraffic: make(map[string]*bridgeCall),
		routeLocks:     make(map[string]*routeLock),
	}

	first := &bridgeCall{
		NetstackID: "call-1",
		RouteKey:   buildRouteKey(204, 1, 10001),
		TrafficID:  "trf-1",
	}
	added, _, _, _ := bridge.registerPendingCall(first)
	if !added {
		t.Fatalf("first call should be accepted")
	}

	time.Sleep(70 * time.Millisecond)

	second := &bridgeCall{
		NetstackID: "call-2",
		RouteKey:   buildRouteKey(204, 1, 10001),
		TrafficID:  "trf-2",
	}
	added, stale, blockedBy, _ := bridge.registerPendingCall(second)
	if !added {
		t.Fatalf("second call should enter after pending lock expiry, blockedBy=%s", blockedBy)
	}
	if len(stale) == 0 || stale[0].NetstackID != "call-1" {
		t.Fatalf("expected first call to be evicted as stale pending lock holder")
	}
}

func TestDecodeRawTrafficFramesDualPacked35(t *testing.T) {
	frameA := make([]byte, 35)
	frameB := make([]byte, 35)
	for i := range frameA {
		frameA[i] = 0xAA
		frameB[i] = 0x55
	}
	raw := append(frameA, frameB...)

	frames, err := decodeRawTrafficFrames(raw)
	if err != nil {
		t.Fatalf("decodeRawTrafficFrames error: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames))
	}
	for i, fr := range frames {
		if len(fr) != 36 {
			t.Fatalf("frame %d len=%d want 36", i, len(fr))
		}
	}
}

func TestDecodeRawTrafficFramesDualBitPerByte274(t *testing.T) {
	raw := make([]byte, 548)
	for i := 0; i < len(raw); i++ {
		raw[i] = byte(i % 2)
	}

	frames, err := decodeRawTrafficFrames(raw)
	if err != nil {
		t.Fatalf("decodeRawTrafficFrames error: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames))
	}
	for i, fr := range frames {
		if len(fr) != 36 {
			t.Fatalf("frame %d len=%d want 36", i, len(fr))
		}
	}
}

func TestDecodeRawTrafficFrames1380Fallback(t *testing.T) {
	raw := make([]byte, 1380)
	// Simulate signed 16-bit soft bits for two 690-byte chunks.
	for i := 0; i < 690/2; i++ {
		v := int16(-1000)
		if i%2 == 0 {
			v = 1000
		}
		raw[i*2] = byte(v)
		raw[i*2+1] = byte(v >> 8)
	}
	for i := 0; i < 690/2; i++ {
		v := int16(-1000)
		if i%3 == 0 {
			v = 1000
		}
		off := 690 + i*2
		raw[off] = byte(v)
		raw[off+1] = byte(v >> 8)
	}

	frames, err := decodeRawTrafficFrames(raw)
	if err != nil {
		t.Fatalf("decodeRawTrafficFrames error: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames))
	}
	for i, fr := range frames {
		if len(fr) != 36 {
			t.Fatalf("frame %d len=%d want 36", i, len(fr))
		}
	}
}
