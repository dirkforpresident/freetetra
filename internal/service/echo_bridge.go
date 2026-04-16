package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/freetetra/server/internal/brew"
	"github.com/freetetra/server/internal/config"
)

// EchoBridge records traffic-channel audio on one talkgroup and plays it back
// after the source call ends.
type EchoBridge struct {
	cfg    config.Config
	logger *log.Logger
	plane  InjectionPlane

	talkgroup uint32
	maxFrames int

	mu        sync.Mutex
	captures  map[uuid.UUID]*echoCapture
	playbackQ chan echoPlayback
	cancel    context.CancelFunc
	done      chan struct{}

	echoed atomic.Uint64
}

type echoCapture struct {
	sourceISSI uint32
	priority   uint8
	access     uint8
	service    uint16
	frames     [][]byte
}

type echoPlayback struct {
	originCall uuid.UUID
	sourceISSI uint32
	priority   uint8
	access     uint8
	service    uint16
	frames     [][]byte
}

func NewEchoBridge(cfg config.Config, logger *log.Logger, plane InjectionPlane) (*EchoBridge, error) {
	tg := cfg.Echo.Talkgroup
	if tg == 0 {
		return nil, fmt.Errorf("ECHO_TALKGROUP must be > 0")
	}
	maxFrames := cfg.Echo.MaxFrames
	if maxFrames <= 0 {
		maxFrames = 2000
	}
	return &EchoBridge{
		cfg:       cfg,
		logger:    logger,
		plane:     plane,
		talkgroup: tg,
		maxFrames: maxFrames,
		captures:  make(map[uuid.UUID]*echoCapture),
		playbackQ: make(chan echoPlayback, 16),
	}, nil
}

func (e *EchoBridge) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	if e.cancel != nil {
		e.mu.Unlock()
		cancel()
		return fmt.Errorf("echo bridge already started")
	}
	e.cancel = cancel
	e.done = make(chan struct{})
	done := e.done
	e.mu.Unlock()

	go func() {
		defer close(done)
		e.playbackLoop(runCtx)
	}()
	e.logger.Printf(
		"echo bridge enabled tg=%d source=%d max_frames=%d frame_interval=%s",
		e.talkgroup,
		e.playbackSourceISSI(0),
		e.maxFrames,
		e.frameInterval().String(),
	)
	return nil
}

func (e *EchoBridge) Stop() {
	e.mu.Lock()
	cancel := e.cancel
	done := e.done
	e.cancel = nil
	e.done = nil
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (e *EchoBridge) OnBrewCallControl(m *brew.CallControlMessage) {
	if m == nil {
		return
	}
	switch m.CallState {
	case brew.CallStateGroupTX:
		p, ok := m.Payload.(brew.GroupTransmissionPayload)
		if !ok {
			return
		}
		if p.Destination != e.talkgroup {
			return
		}
		e.mu.Lock()
		cap := e.ensureCaptureLocked(m.Identifier)
		cap.sourceISSI = p.Source
		cap.priority = p.Priority
		cap.access = p.Access
		cap.service = p.Service
		e.mu.Unlock()
		e.logger.Printf(
			"echo capture start call=%s source=%d tg=%d priority=%d access=%d service=%d",
			m.Identifier.String(),
			p.Source,
			p.Destination,
			p.Priority,
			p.Access,
			p.Service,
		)
	case brew.CallStateGroupIdle, brew.CallStateCallRelease:
		e.finalizeCapture(m.Identifier, m.CallState)
	}
}

func (e *EchoBridge) OnBrewFrame(callID uuid.UUID, frameType uint8, data []byte) {
	if frameType != brew.FrameTypeTrafficChannel {
		return
	}
	e.mu.Lock()
	cap := e.ensureCaptureLocked(callID)
	if len(cap.frames) >= e.maxFrames {
		e.mu.Unlock()
		return
	}
	frameCopy := append([]byte(nil), data...)
	cap.frames = append(cap.frames, frameCopy)
	e.mu.Unlock()
}

func (e *EchoBridge) ensureCaptureLocked(callID uuid.UUID) *echoCapture {
	cap := e.captures[callID]
	if cap == nil {
		cap = &echoCapture{}
		e.captures[callID] = cap
	}
	return cap
}

func (e *EchoBridge) finalizeCapture(callID uuid.UUID, state uint8) {
	e.mu.Lock()
	cap := e.captures[callID]
	if cap == nil {
		e.mu.Unlock()
		return
	}
	delete(e.captures, callID)
	frames := make([][]byte, 0, len(cap.frames))
	for _, f := range cap.frames {
		frames = append(frames, append([]byte(nil), f...))
	}
	source := cap.sourceISSI
	priority := cap.priority
	access := cap.access
	service := cap.service
	e.mu.Unlock()

	if len(frames) == 0 {
		e.logger.Printf("echo capture drop call=%s state=%d reason=no-traffic", callID.String(), state)
		return
	}
	req := echoPlayback{
		originCall: callID,
		sourceISSI: source,
		priority:   priority,
		access:     access,
		service:    service,
		frames:     frames,
	}
	select {
	case e.playbackQ <- req:
		e.logger.Printf(
			"echo capture finalize call=%s state=%d queued_frames=%d tg=%d",
			callID.String(),
			state,
			len(frames),
			e.talkgroup,
		)
	default:
		e.logger.Printf(
			"echo capture drop call=%s state=%d reason=playback-queue-full frames=%d",
			callID.String(),
			state,
			len(frames),
		)
	}
}

func (e *EchoBridge) playbackLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-e.playbackQ:
			e.playbackRequest(ctx, req)
		}
	}
}

func (e *EchoBridge) playbackRequest(ctx context.Context, req echoPlayback) {
	delay := e.playbackDelay()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}

	callID := uuid.New()
	source := e.playbackSourceISSI(req.sourceISSI)
	if !e.plane.StartInjectedGroupTX("echo", callID, source, e.talkgroup, req.priority, req.access, req.service) {
		e.logger.Printf(
			"echo playback drop origin_call=%s reason=start-failed source=%d tg=%d priority=%d access=%d service=%d",
			req.originCall.String(),
			source,
			e.talkgroup,
			req.priority,
			req.access,
			req.service,
		)
		return
	}

	e.logger.Printf(
		"echo playback start origin_call=%s playback_call=%s source=%d tg=%d priority=%d access=%d service=%d frames=%d",
		req.originCall.String(),
		callID.String(),
		source,
		e.talkgroup,
		req.priority,
		req.access,
		req.service,
		len(req.frames),
	)
	interval := e.frameInterval()
	for i, frame := range req.frames {
		select {
		case <-ctx.Done():
			e.plane.IdleInjectedCall("echo", callID, e.releaseCause())
			e.plane.ReleaseInjectedCall("echo", callID, e.releaseCause())
			return
		default:
		}
		e.plane.InjectedVoiceFrame("echo", callID, frame)
		total := e.echoed.Add(1)
		if total == 1 || total%50 == 0 {
			e.logger.Printf("echo playback mirrored_total=%d playback_call=%s", total, callID.String())
		}
		if interval > 0 && i < len(req.frames)-1 {
			select {
			case <-ctx.Done():
				e.plane.IdleInjectedCall("echo", callID, e.releaseCause())
				e.plane.ReleaseInjectedCall("echo", callID, e.releaseCause())
				return
			case <-time.After(interval):
			}
		}
	}
	e.plane.IdleInjectedCall("echo", callID, e.releaseCause())
	e.plane.ReleaseInjectedCall("echo", callID, e.releaseCause())
	e.logger.Printf("echo playback end playback_call=%s frames=%d", callID.String(), len(req.frames))
}

func (e *EchoBridge) playbackSourceISSI(fallback uint32) uint32 {
	if e.cfg.Echo.SourceISSI != 0 {
		return e.cfg.Echo.SourceISSI
	}
	if e.cfg.Echo.BrewISSI != 0 {
		return e.cfg.Echo.BrewISSI
	}
	return fallback
}

func (e *EchoBridge) playbackDelay() time.Duration {
	if e.cfg.Echo.PlaybackDelay < 0 {
		return 0
	}
	return e.cfg.Echo.PlaybackDelay
}

func (e *EchoBridge) frameInterval() time.Duration {
	if e.cfg.Echo.FrameInterval <= 0 {
		return 60 * time.Millisecond
	}
	return e.cfg.Echo.FrameInterval
}

func (e *EchoBridge) releaseCause() uint8 {
	return e.cfg.Echo.ReleaseCause
}
