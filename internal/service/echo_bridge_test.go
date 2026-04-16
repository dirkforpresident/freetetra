package service

import (
	"bytes"
	"context"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/freetetra/server/internal/brew"
	"github.com/freetetra/server/internal/config"
)

type echoStartEvent struct {
	callID     uuid.UUID
	sourceISSI uint32
	destGSSI   uint32
	priority   uint8
	access     uint8
	service    uint16
}

type echoReleaseEvent struct {
	callID uuid.UUID
	cause  uint8
}

type echoPlaneStub struct {
	mu          sync.Mutex
	voiceByCall map[uuid.UUID][][]byte
	starts      []echoStartEvent
	idles       []echoReleaseEvent
	releases    []echoReleaseEvent
}

func newEchoPlaneStub() *echoPlaneStub {
	return &echoPlaneStub{voiceByCall: make(map[uuid.UUID][][]byte)}
}

func (s *echoPlaneStub) StartInjectedCall(_ string, callID uuid.UUID, source uint32, dest uint32) bool {
	return s.StartInjectedGroupTX("", callID, source, dest, 0, 0, 0)
}

func (s *echoPlaneStub) StartInjectedGroupTX(
	_ string,
	callID uuid.UUID,
	source uint32,
	dest uint32,
	priority uint8,
	access uint8,
	service uint16,
) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, echoStartEvent{
		callID:     callID,
		sourceISSI: source,
		destGSSI:   dest,
		priority:   priority,
		access:     access,
		service:    service,
	})
	return true
}

func (s *echoPlaneStub) IdleInjectedCall(_ string, callID uuid.UUID, cause uint8) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idles = append(s.idles, echoReleaseEvent{callID: callID, cause: cause})
}

func (s *echoPlaneStub) ReleaseInjectedCall(_ string, callID uuid.UUID, cause uint8) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases = append(s.releases, echoReleaseEvent{callID: callID, cause: cause})
}

func (s *echoPlaneStub) InjectedVoiceFrame(_ string, callID uuid.UUID, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.voiceByCall[callID] = append(s.voiceByCall[callID], append([]byte(nil), data...))
}

func (s *echoPlaneStub) InjectedPacketFrame(_ string, _ uuid.UUID, _ []byte) {}
func (s *echoPlaneStub) GroupSubscriberCount(_ uint32) int                   { return 0 }

func (s *echoPlaneStub) snapshot() (starts []echoStartEvent, idles []echoReleaseEvent, releases []echoReleaseEvent, voice map[uuid.UUID][][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	starts = append(starts, s.starts...)
	idles = append(idles, s.idles...)
	releases = append(releases, s.releases...)
	voice = make(map[uuid.UUID][][]byte, len(s.voiceByCall))
	for callID, frames := range s.voiceByCall {
		copied := make([][]byte, 0, len(frames))
		for _, f := range frames {
			copied = append(copied, append([]byte(nil), f...))
		}
		voice[callID] = copied
	}
	return starts, idles, releases, voice
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	if fn() {
		return
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestEchoBridgePlaybackAfterCallEnd(t *testing.T) {
	cfg := config.Config{Echo: config.EchoConfig{Talkgroup: 10002, PlaybackDelay: 0, FrameInterval: time.Millisecond, ReleaseCause: 7, MaxFrames: 64}}
	plane := newEchoPlaneStub()
	logger := log.New(bytes.NewBuffer(nil), "", 0)

	bridge, err := NewEchoBridge(cfg, logger, plane)
	if err != nil {
		t.Fatalf("new bridge: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer bridge.Stop()

	originCallID := uuid.New()
	bridge.OnBrewCallControl(&brew.CallControlMessage{
		CallState:  brew.CallStateGroupTX,
		Identifier: originCallID,
		Payload:    brew.GroupTransmissionPayload{Source: 501, Destination: 10002, Priority: 2, Access: 3, Service: 5},
	})

	f1 := []byte{0x80, 0x01, 0x02}
	f2 := []byte{0x80, 0x03, 0x04}
	bridge.OnBrewFrame(originCallID, brew.FrameTypeTrafficChannel, f1)
	bridge.OnBrewFrame(originCallID, brew.FrameTypeTrafficChannel, f2)

	bridge.OnBrewCallControl(&brew.CallControlMessage{CallState: brew.CallStateCallRelease, Identifier: originCallID})

	waitFor(t, 500*time.Millisecond, func() bool {
		starts, idles, releases, voice := plane.snapshot()
		if len(starts) != 1 || len(idles) != 1 || len(releases) != 1 {
			return false
		}
		pbCall := starts[0].callID
		frames := voice[pbCall]
		return len(frames) == 2
	})

	starts, idles, releases, voice := plane.snapshot()
	if starts[0].sourceISSI != 501 {
		t.Fatalf("unexpected playback source %d", starts[0].sourceISSI)
	}
	if starts[0].destGSSI != 10002 {
		t.Fatalf("unexpected playback destination %d", starts[0].destGSSI)
	}
	if starts[0].priority != 2 || starts[0].access != 3 || starts[0].service != 5 {
		t.Fatalf("unexpected group tx params priority=%d access=%d service=%d", starts[0].priority, starts[0].access, starts[0].service)
	}
	if idles[0].cause != 7 {
		t.Fatalf("unexpected idle cause %d", idles[0].cause)
	}
	if releases[0].cause != 7 {
		t.Fatalf("unexpected release cause %d", releases[0].cause)
	}
	playbackCall := starts[0].callID
	frames := voice[playbackCall]
	if len(frames) != 2 {
		t.Fatalf("expected 2 playback frames, got %d", len(frames))
	}
	if !bytes.Equal(frames[0], f1) || !bytes.Equal(frames[1], f2) {
		t.Fatalf("unexpected playback frame data")
	}
}

func TestEchoBridgeRequiresTalkgroup(t *testing.T) {
	cfg := config.Config{}
	logger := log.New(bytes.NewBuffer(nil), "", 0)
	_, err := NewEchoBridge(cfg, logger, newEchoPlaneStub())
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestEchoBridgeDoesNotPlaybackWithoutTraffic(t *testing.T) {
	cfg := config.Config{Echo: config.EchoConfig{Talkgroup: 10002, PlaybackDelay: 0, FrameInterval: time.Millisecond}}
	plane := newEchoPlaneStub()
	logger := log.New(bytes.NewBuffer(nil), "", 0)

	bridge, err := NewEchoBridge(cfg, logger, plane)
	if err != nil {
		t.Fatalf("new bridge: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer bridge.Stop()

	callID := uuid.New()
	bridge.OnBrewCallControl(&brew.CallControlMessage{
		CallState:  brew.CallStateGroupTX,
		Identifier: callID,
		Payload:    brew.GroupTransmissionPayload{Source: 501, Destination: 10002},
	})
	bridge.OnBrewCallControl(&brew.CallControlMessage{CallState: brew.CallStateGroupIdle, Identifier: callID})

	time.Sleep(25 * time.Millisecond)
	starts, _, releases, _ := plane.snapshot()
	if len(starts) != 0 || len(releases) != 0 {
		t.Fatalf("expected no playback events, got starts=%d releases=%d", len(starts), len(releases))
	}
}
