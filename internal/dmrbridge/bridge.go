// Package dmrbridge ties two Brew clients together to forward group-call
// traffic between FreeTetra and BrandMeister (via the TetraPack core).
//
// Both sides connect as virtual subscribers, affiliated to the same set of
// talkgroups. When a call arrives on one side, a mirror call is injected on
// the other. Voice frames flow through unchanged (both ends speak ACELP).
package dmrbridge

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/freetetra/server/internal/brew"
	"github.com/freetetra/server/internal/service"
)

// Bridge mirrors group calls between two Brew planes.
type Bridge struct {
	logger *log.Logger
	ft     *service.BrewModulePlane
	bm     *service.BrewModulePlane

	talkgroups map[uint32]struct{}

	mu sync.Mutex
	// Map original call ID -> mirror call info on the OTHER side.
	ftCalls map[uuid.UUID]*mirror // call ID seen on FT, mirrored to BM
	bmCalls map[uuid.UUID]*mirror // call ID seen on BM, mirrored to FT
	// Mirror call IDs we created on each side, to ignore self-echo.
	mineFT map[uuid.UUID]struct{}
	mineBM map[uuid.UUID]struct{}
}

type mirror struct {
	id        uuid.UUID
	startedAt time.Time
}

// New constructs a bridge that mirrors `talkgroups` between the two planes.
// `ft` connects to FreeTetra, `bm` connects to the BrandMeister-facing
// TetraPack core.
func New(logger *log.Logger, ft, bm *service.BrewModulePlane, talkgroups []uint32) *Bridge {
	tgs := make(map[uint32]struct{}, len(talkgroups))
	for _, tg := range talkgroups {
		if tg != 0 {
			tgs[tg] = struct{}{}
		}
	}
	b := &Bridge{
		logger:     logger,
		ft:         ft,
		bm:         bm,
		talkgroups: tgs,
		ftCalls:    make(map[uuid.UUID]*mirror),
		bmCalls:    make(map[uuid.UUID]*mirror),
		mineFT:     make(map[uuid.UUID]struct{}),
		mineBM:     make(map[uuid.UUID]struct{}),
	}
	ft.SetMessageHandlers(b.onFTCall, b.onFTFrame, nil)
	bm.SetMessageHandlers(b.onBMCall, b.onBMFrame, nil)
	return b
}

// Start runs both planes until ctx is cancelled.
func (b *Bridge) Start(ctx context.Context) error {
	tgList := make([]uint32, 0, len(b.talkgroups))
	for tg := range b.talkgroups {
		tgList = append(tgList, tg)
	}
	b.logger.Printf("dmrbridge: starting, talkgroups=%v", tgList)

	go func() {
		if err := b.ft.Run(ctx); err != nil {
			b.logger.Printf("dmrbridge: FT plane stopped: %v", err)
		}
	}()
	go func() {
		if err := b.bm.Run(ctx); err != nil {
			b.logger.Printf("dmrbridge: BM plane stopped: %v", err)
		}
	}()

	go b.gcLoop(ctx)
	<-ctx.Done()
	return nil
}

// gcLoop sweeps stale call mappings older than 60 s (call probably orphaned).
func (b *Bridge) gcLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.mu.Lock()
			cutoff := time.Now().Add(-60 * time.Second)
			for id, m := range b.ftCalls {
				if m.startedAt.Before(cutoff) {
					delete(b.ftCalls, id)
					delete(b.mineBM, m.id)
				}
			}
			for id, m := range b.bmCalls {
				if m.startedAt.Before(cutoff) {
					delete(b.bmCalls, id)
					delete(b.mineFT, m.id)
				}
			}
			b.mu.Unlock()
		}
	}
}

// --- FreeTetra side: incoming events get mirrored to BM ---

func (b *Bridge) onFTCall(m *brew.CallControlMessage) {
	b.handleCall(m, "FT", "BM", b.ftCalls, b.bmCalls, b.mineFT, b.mineBM, b.bm)
}

func (b *Bridge) onFTFrame(m *brew.FrameMessage) {
	b.handleFrame(m, "FT", "BM", b.ftCalls, b.mineFT, b.bm)
}

// --- BrandMeister side: incoming events get mirrored to FT ---

func (b *Bridge) onBMCall(m *brew.CallControlMessage) {
	b.handleCall(m, "BM", "FT", b.bmCalls, b.ftCalls, b.mineBM, b.mineFT, b.ft)
}

func (b *Bridge) onBMFrame(m *brew.FrameMessage) {
	b.handleFrame(m, "BM", "FT", b.bmCalls, b.mineBM, b.ft)
}

// handleCall processes a CallControl from `srcLabel` and mirrors to `dstPlane`.
//
// `srcCalls`: map of call IDs the bridge has seen entering from this side.
// `dstCalls`: ditto for the other side (used to suppress reflexive ACKs).
// `mineSrc`/`mineDst`: sets of call IDs the bridge ITSELF created on each side
// (used to ignore self-echo when an injected call is broadcast back to us).
func (b *Bridge) handleCall(
	m *brew.CallControlMessage,
	srcLabel, dstLabel string,
	srcCalls, dstCalls map[uuid.UUID]*mirror,
	mineSrc, mineDst map[uuid.UUID]struct{},
	dstPlane *service.BrewModulePlane,
) {
	id := m.Identifier

	b.mu.Lock()
	if _, isMine := mineSrc[id]; isMine {
		b.mu.Unlock()
		return // self-echo, ignore
	}
	b.mu.Unlock()

	switch m.CallState {
	case brew.CallStateGroupTX:
		gp, ok := m.Payload.(brew.GroupTransmissionPayload)
		if !ok {
			return
		}
		if !b.bridges(gp.Destination) {
			return
		}
		mirrorID := uuid.New()
		b.mu.Lock()
		srcCalls[id] = &mirror{id: mirrorID, startedAt: time.Now()}
		mineDst[mirrorID] = struct{}{}
		b.mu.Unlock()

		ok2 := dstPlane.StartInjectedGroupTX("dmrbridge",
			mirrorID, gp.Source, gp.Destination,
			gp.Priority, gp.Access, gp.Service)
		if ok2 {
			b.logger.Printf("dmrbridge: %s→%s call start TG=%d src=%d (id %s→%s)",
				srcLabel, dstLabel, gp.Destination, gp.Source, shortID(id), shortID(mirrorID))
		} else {
			b.logger.Printf("dmrbridge: %s→%s call start REJECTED (TG=%d busy?)",
				srcLabel, dstLabel, gp.Destination)
		}

	case brew.CallStateGroupIdle, brew.CallStateCallRelease:
		b.mu.Lock()
		mir, ok := srcCalls[id]
		if ok {
			delete(srcCalls, id)
			delete(mineDst, mir.id)
		}
		b.mu.Unlock()
		if ok {
			dstPlane.IdleInjectedCall("dmrbridge", mir.id, 0)
			dstPlane.ReleaseInjectedCall("dmrbridge", mir.id, 0)
			b.logger.Printf("dmrbridge: %s→%s call end (id %s)", srcLabel, dstLabel, shortID(id))
		}
	}
}

func (b *Bridge) handleFrame(
	m *brew.FrameMessage,
	srcLabel, dstLabel string,
	srcCalls map[uuid.UUID]*mirror,
	mineSrc map[uuid.UUID]struct{},
	dstPlane *service.BrewModulePlane,
) {
	id := m.Identifier
	b.mu.Lock()
	if _, isMine := mineSrc[id]; isMine {
		b.mu.Unlock()
		return // own injected frame echoed back
	}
	mir, ok := srcCalls[id]
	b.mu.Unlock()
	if !ok {
		return // no active mirror for this call
	}
	dstPlane.InjectedVoiceFrame("dmrbridge", mir.id, m.Data)
}

func (b *Bridge) bridges(tg uint32) bool {
	_, ok := b.talkgroups[tg]
	return ok
}

func shortID(id uuid.UUID) string {
	s := id.String()
	if len(s) >= 8 {
		return s[:8]
	}
	return s
}
