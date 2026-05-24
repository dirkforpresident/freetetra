package federation

import (
	"context"
	"log"
	"sync"
	"sync/atomic"

	federationv2pb "github.com/freetetra/server/internal/federation/proto/v2"
)

// Peer represents a connected federation peer (another Brew server).
type Peer struct {
	Name      string
	Direction string // "outgoing" or "incoming"

	mu     sync.RWMutex
	stream rpcStream
	cancel context.CancelFunc

	// Remote subscriber state. issis maps each ISSI registered on this peer
	// to its origin — the server where the subscriber is physically attached.
	// For ISSIs this peer relayed for someone else (multi-hop federation),
	// origin is the original sender's name from Control.origin; for ISSIs
	// this peer owns directly, origin == peer.Name.
	issis            map[uint32]string          // ISSI -> origin server name
	gssiAffiliations map[uint32]map[uint32]bool // GSSI -> set of ISSIs

	send   chan *federationv2pb.StreamFrame
	done   chan struct{}
	logger *log.Logger

	// closeErr carries a rejection reason set by a handler when the peer
	// should be dropped mid-stream. The readLoop checks it after every
	// handleControlMessage; the Connect handler returns it to gRPC so the
	// status code travels back to the dialer. nil means normal close.
	closeErrMu sync.Mutex
	closeErr   error

	// Per-peer send statistics. Tracked with atomics so any goroutine
	// (writeLoop, broadcast fan-out callers, the stats logger) can read
	// them without taking a lock.
	sentControl    atomic.Uint64
	sentVoice      atomic.Uint64
	droppedControl atomic.Uint64
	droppedVoice   atomic.Uint64
}

// PeerBufferStats is a snapshot of a peer's send-channel occupancy and
// lifetime counters. QueueLen and QueueCap reflect the Go-channel side
// (256 slots by default); the gRPC HTTP/2 flow-control window is a
// separate buffer the channel feeds into and isn't visible from here.
type PeerBufferStats struct {
	QueueLen       int    `json:"queue_len"`
	QueueCap       int    `json:"queue_cap"`
	SentControl    uint64 `json:"sent_control"`
	SentVoice      uint64 `json:"sent_voice"`
	DroppedControl uint64 `json:"dropped_control"`
	DroppedVoice   uint64 `json:"dropped_voice"`
}

// BufferStats returns the current send-side telemetry for this peer.
func (p *Peer) BufferStats() PeerBufferStats {
	return PeerBufferStats{
		QueueLen:       len(p.send),
		QueueCap:       cap(p.send),
		SentControl:    p.sentControl.Load(),
		SentVoice:      p.sentVoice.Load(),
		DroppedControl: p.droppedControl.Load(),
		DroppedVoice:   p.droppedVoice.Load(),
	}
}

type rpcStream interface {
	Send(*federationv2pb.StreamFrame) error
	Recv() (*federationv2pb.StreamFrame, error)
	Context() context.Context
}

func newPeer(name, direction string, stream rpcStream, cancel context.CancelFunc, logger *log.Logger) *Peer {
	return &Peer{
		Name:             name,
		Direction:        direction,
		stream:           stream,
		cancel:           cancel,
		issis:            make(map[uint32]string),
		gssiAffiliations: make(map[uint32]map[uint32]bool),
		send:             make(chan *federationv2pb.StreamFrame, 256),
		done:             make(chan struct{}),
		logger:           logger,
	}
}

// SetCloseErr records why this peer is being dropped. The first call wins so
// the original reason (e.g. version mismatch) isn't overwritten by a generic
// downstream "context canceled" once Close() takes effect. Safe to call from
// any goroutine.
func (p *Peer) SetCloseErr(err error) {
	if err == nil {
		return
	}
	p.closeErrMu.Lock()
	if p.closeErr == nil {
		p.closeErr = err
	}
	p.closeErrMu.Unlock()
}

// CloseErr returns the recorded rejection reason, or nil for a normal close.
func (p *Peer) CloseErr() error {
	p.closeErrMu.Lock()
	defer p.closeErrMu.Unlock()
	return p.closeErr
}

// controlPayloadKind returns a short string describing which oneof case
// is set on a Control. Used for log context.
func controlPayloadKind(ctrl *federationv2pb.Control) string {
	switch ctrl.GetPayload().(type) {
	case *federationv2pb.Control_Hello:
		return "hello"
	case *federationv2pb.Control_SubscriberUpdate:
		return "subscriber_update"
	case *federationv2pb.Control_AffiliateUpdate:
		return "affiliate_update"
	case *federationv2pb.Control_CallStart:
		return "call_start"
	case *federationv2pb.Control_CallEnd:
		return "call_end"
	case *federationv2pb.Control_SdsRelay:
		return "sds_relay"
	case *federationv2pb.Control_SyncRequest:
		return "sync_request"
	case *federationv2pb.Control_SyncResponse:
		return "sync_response"
	case *federationv2pb.Control_PeerExchange:
		return "peer_exchange"
	case *federationv2pb.Control_UsersDbOffer:
		return "usersdb_offer"
	case *federationv2pb.Control_UsersDbRequest:
		return "usersdb_request"
	case *federationv2pb.Control_PositionSample:
		return "position_sample"
	case *federationv2pb.Control_StationUpdate:
		return "station_update"
	default:
		return "unknown"
	}
}

// SendControl enqueues a typed control message for delivery to the peer.
func (p *Peer) SendControl(ctrl *federationv2pb.Control) error {
	if ctrl == nil {
		return nil
	}
	frame := &federationv2pb.StreamFrame{
		Body: &federationv2pb.StreamFrame_Control{Control: ctrl},
	}
	select {
	case p.send <- frame:
		return nil
	case <-p.done:
		return context.Canceled
	default:
		dropped := p.droppedControl.Add(1)
		p.logger.Printf("federation: send buffer full for peer %s [q=%d/%d sent=%d dropped=%d], dropping control %s",
			p.Name, len(p.send), cap(p.send), p.sentControl.Load(), dropped, controlPayloadKind(ctrl))
		return nil
	}
}


// SendVoiceFrame sends a typed v2 voice frame to the peer. Drops silently
// on a full buffer rather than logging — voice is high-frequency (3-4
// frames per second per active call) and a log per drop would flood.
// The drop is still counted on droppedVoice so the periodic stats log
// surfaces sustained loss.
func (p *Peer) SendVoiceFrame(callUUID string, frameData []byte) error {
	frame := &federationv2pb.StreamFrame{
		Body: &federationv2pb.StreamFrame_VoiceFrame{VoiceFrame: &federationv2pb.VoiceFrame{
			CallUuid:  callUUID,
			FrameData: frameData,
		}},
	}
	select {
	case p.send <- frame:
		return nil
	case <-p.done:
		return context.Canceled
	default:
		p.droppedVoice.Add(1)
		return nil
	}
}

// RegisterISSI adds an ISSI to this peer's remote registry. origin is the
// server where the subscriber is physically attached (Control.origin from
// the SubscriberUpdate). If empty, peer.Name is used — the right fallback
// for the SyncResponse path, where the peer only ships its own locals.
func (p *Peer) RegisterISSI(issi uint32, origin string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if origin == "" {
		origin = p.Name
	}
	p.issis[issi] = origin
	// Eine erneute Register-Message ist ein "atomic replace" — alte
	// GSSI-Affiliations fuer diese ISSI fliegen raus, das nachfolgende
	// AffiliateISSI setzt die aktuelle Liste. Sonst akkumulieren alte
	// Scan-Listen-Eintraege ewig.
	for gssi, members := range p.gssiAffiliations {
		delete(members, issi)
		if len(members) == 0 {
			delete(p.gssiAffiliations, gssi)
		}
	}
}

// DeregisterISSI removes an ISSI from this peer's remote registry.
func (p *Peer) DeregisterISSI(issi uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.issis, issi)
	// Remove from all GSSI affiliations
	for gssi, members := range p.gssiAffiliations {
		delete(members, issi)
		if len(members) == 0 {
			delete(p.gssiAffiliations, gssi)
		}
	}
}

// AffiliateISSI adds GSSI affiliations for an ISSI on this peer.
func (p *Peer) AffiliateISSI(issi uint32, gssis []uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, gssi := range gssis {
		if p.gssiAffiliations[gssi] == nil {
			p.gssiAffiliations[gssi] = make(map[uint32]bool)
		}
		p.gssiAffiliations[gssi][issi] = true
	}
}

// DeaffiliateISSI removes GSSI affiliations for an ISSI on this peer.
func (p *Peer) DeaffiliateISSI(issi uint32, gssis []uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, gssi := range gssis {
		if members, ok := p.gssiAffiliations[gssi]; ok {
			delete(members, issi)
			if len(members) == 0 {
				delete(p.gssiAffiliations, gssi)
			}
		}
	}
}

// HasSubscribersOnGSSI returns true if this peer has any subscribers affiliated to the given GSSI.
func (p *Peer) HasSubscribersOnGSSI(gssi uint32) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.gssiAffiliations[gssi]) > 0
}

// HasISSI returns true if this peer has the given ISSI registered.
func (p *Peer) HasISSI(issi uint32) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.issis[issi]
	return ok
}

// ISSIs returns a copy of all registered ISSIs.
func (p *Peer) ISSIs() []uint32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]uint32, 0, len(p.issis))
	for issi := range p.issis {
		out = append(out, issi)
	}
	return out
}

// Origins returns a copy of the ISSI -> origin map. Origin is the server
// where each ISSI is physically attached, which may differ from peer.Name
// when the peer is just relaying for a deeper hop in the mesh.
func (p *Peer) Origins() map[uint32]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[uint32]string, len(p.issis))
	for issi, origin := range p.issis {
		out[issi] = origin
	}
	return out
}

// GSSIs returns a copy of all GSSIs with affiliations.
func (p *Peer) GSSIs() map[uint32][]uint32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[uint32][]uint32)
	for gssi, members := range p.gssiAffiliations {
		issis := make([]uint32, 0, len(members))
		for issi := range members {
			issis = append(issis, issi)
		}
		out[gssi] = issis
	}
	return out
}

// Cleanup removes all state for this peer.
func (p *Peer) Cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := len(p.issis)
	p.issis = make(map[uint32]string)
	p.gssiAffiliations = make(map[uint32]map[uint32]bool)
	p.logger.Printf("federation: cleaned up peer %s (%d ISSIs removed)", p.Name, count)
}

// Close shuts down the peer connection.
func (p *Peer) Close() {
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	if p.cancel != nil {
		p.cancel()
	}
}

// writeLoop pumps messages from the send channel to the RPC stream.
func (p *Peer) writeLoop() {
	for {
		select {
		case env, ok := <-p.send:
			if !ok {
				return
			}
			if err := p.stream.Send(env); err != nil {
				p.logger.Printf("federation: write to %s failed: %v", p.Name, err)
				return
			}
			if env.GetVoiceFrame() != nil {
				p.sentVoice.Add(1)
			} else {
				p.sentControl.Add(1)
			}

		case <-p.done:
			return
		case <-p.stream.Context().Done():
			return
		}
	}
}
