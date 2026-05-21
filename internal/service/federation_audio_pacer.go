package service

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// audioPacer entkoppelt Empfang von Voice-Frames (per TCP, bursty) vom
// Ausspielen ueber den lokalen Brew-Stream (60ms-Pacing). Pro Call ein
// bounded Channel als Jitter-Buffer + Worker-Goroutine die mit 60ms
// Ticker auspielt.
//
// Ohne diesen Pacer staute sich bei Netzwerk-Hops ein "Schleppe" am Ende
// jedes Durchgangs auf (Tobi hoert auf zu sprechen, aber der Buffer hat
// noch 3-4 Frames die mit 60ms/Frame nachhaengen).
type audioPacer struct {
	mu     sync.Mutex
	pacers map[uuid.UUID]*callPacer
}

type callPacer struct {
	ch     chan []byte
	cancel context.CancelFunc
}

const (
	// 4 Frames = 240ms maximale Latenz vom Empfang bis Ausspielen.
	// Genug fuer normale TCP-Jitter, klein genug um die Nachhaeng-Schleppe
	// am Call-Ende auf <240ms zu begrenzen.
	pacerBufferDepth = 4
	pacerFrameDelay  = 60 * time.Millisecond
)

func newAudioPacer() *audioPacer {
	return &audioPacer{pacers: make(map[uuid.UUID]*callPacer)}
}

// Start startet einen Pacer fuer den Call. send wird mit jedem Frame im
// 60ms-Takt aufgerufen. Mehrfach-Start fuer dieselbe Call-ID ist No-op.
func (ap *audioPacer) Start(callID uuid.UUID, send func(data []byte)) {
	ap.mu.Lock()
	if _, exists := ap.pacers[callID]; exists {
		ap.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan []byte, pacerBufferDepth)
	ap.pacers[callID] = &callPacer{ch: ch, cancel: cancel}
	ap.mu.Unlock()
	go ap.worker(ctx, ch, send)
}

// Push fuegt einen Frame in den Pacer-Buffer ein. Wenn der Buffer voll
// ist (Netzwerk-Burst), wird der aelteste Frame verworfen.
func (ap *audioPacer) Push(callID uuid.UUID, frame []byte) {
	ap.mu.Lock()
	p, ok := ap.pacers[callID]
	ap.mu.Unlock()
	if !ok {
		return
	}
	select {
	case p.ch <- frame:
	default:
		// Buffer voll — drop oldest, push new.
		select {
		case <-p.ch:
		default:
		}
		select {
		case p.ch <- frame:
		default:
		}
	}
}

// Stop beendet den Pacer fuer den Call und verwirft den Buffer.
// Wichtig: SOFORT nach CallEnd aufrufen, sonst spielt der Worker noch
// bis zu pacerBufferDepth * 60ms an Frames aus → Schleppe-Effekt.
func (ap *audioPacer) Stop(callID uuid.UUID) {
	ap.mu.Lock()
	if p, ok := ap.pacers[callID]; ok {
		p.cancel()
		delete(ap.pacers, callID)
	}
	ap.mu.Unlock()
}

func (ap *audioPacer) worker(ctx context.Context, ch chan []byte, send func([]byte)) {
	ticker := time.NewTicker(pacerFrameDelay)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			select {
			case frame := <-ch:
				send(frame)
			default:
				// Kein Frame im Buffer — tick verfaellt, naechster
				// Frame der reinkommt wird sofort beim naechsten Tick
				// ausgespielt.
			}
		}
	}
}
