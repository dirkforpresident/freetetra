package federation

import (
	"context"
	"log"
	"sync"

	federationv2pb "github.com/freetetra/server/internal/federation/proto/v2"
)

// Peer represents a connected federation peer (another Brew server).
type Peer struct {
	Name      string
	Direction string // "outgoing" or "incoming"

	mu     sync.RWMutex
	stream rpcStream
	cancel context.CancelFunc

	// Remote subscriber state
	issis            map[uint32]bool            // ISSIs registered on this peer
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
		issis:            make(map[uint32]bool),
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
		p.logger.Printf("federation: send buffer full for peer %s, dropping control %s", p.Name, controlPayloadKind(ctrl))
		return nil
	}
}


// SendVoiceFrame sends a typed v2 voice frame to the peer. Drops silently
// on a full buffer rather than logging — voice is high-frequency (3-4
// frames per second per active call) and a log per drop would flood.
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
		return nil
	}
}

// RegisterISSI adds an ISSI to this peer's remote registry.
func (p *Peer) RegisterISSI(issi uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.issis[issi] = true
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
	return p.issis[issi]
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
	p.issis = make(map[uint32]bool)
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

		case <-p.done:
			return
		case <-p.stream.Context().Done():
			return
		}
	}
}
