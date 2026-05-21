package federation

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Peer represents a connected federation peer (another Brew server).
type Peer struct {
	Name      string
	Direction string // "outgoing" or "incoming"

	mu   sync.RWMutex
	conn *websocket.Conn

	// Remote subscriber state
	issis            map[uint32]bool            // ISSIs registered on this peer
	gssiAffiliations map[uint32]map[uint32]bool // GSSI -> set of ISSIs

	send   chan []byte
	done   chan struct{}
	logger *log.Logger
}

func newPeer(name, direction string, conn *websocket.Conn, logger *log.Logger) *Peer {
	return &Peer{
		Name:             name,
		Direction:        direction,
		conn:             conn,
		issis:            make(map[uint32]bool),
		gssiAffiliations: make(map[uint32]map[uint32]bool),
		send:             make(chan []byte, 256),
		done:             make(chan struct{}),
		logger:           logger,
	}
}

// SendJSON sends a JSON federation message to the peer.
func (p *Peer) SendJSON(msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	select {
	case p.send <- data:
		return nil
	case <-p.done:
		return websocket.ErrCloseSent
	default:
		p.logger.Printf("federation: send buffer full for peer %s, dropping message type=%s", p.Name, msg.Type)
		return nil
	}
}

// SendBinary sends raw binary data to the peer (for voice frames).
func (p *Peer) SendBinary(data []byte) error {
	select {
	case p.send <- data:
		return nil
	case <-p.done:
		return websocket.ErrCloseSent
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
	if p.conn != nil {
		p.conn.Close()
	}
}

// writeLoop pumps messages from the send channel to the WebSocket.
func (p *Peer) writeLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case data, ok := <-p.send:
			if !ok {
				return
			}
			// Determine message type: JSON (text) or binary
			msgType := websocket.TextMessage
			if len(data) > 0 && data[0] != '{' && data[0] != '[' {
				msgType = websocket.BinaryMessage
			}
			p.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := p.conn.WriteMessage(msgType, data); err != nil {
				p.logger.Printf("federation: write to %s failed: %v", p.Name, err)
				return
			}
			p.conn.SetWriteDeadline(time.Time{})

		case <-ticker.C:
			p.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := p.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
			p.conn.SetWriteDeadline(time.Time{})

		case <-p.done:
			return
		}
	}
}
