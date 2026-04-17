package federation

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// MeshRouter handles message deduplication, TTL, and multi-hop relay.
type MeshRouter struct {
	serverName string

	mu   sync.Mutex
	seen map[string]time.Time // msg_id -> timestamp (for dedup)
}

const (
	deduplicationWindow = 30 * time.Second
	cleanupInterval     = 10 * time.Second
)

func newMeshRouter(serverName string) *MeshRouter {
	mr := &MeshRouter{
		serverName: serverName,
		seen:       make(map[string]time.Time),
	}
	go mr.cleanupLoop()
	return mr
}

// NewMessageID generates a unique message ID.
func NewMessageID() string {
	return uuid.New().String()[:8]
}

// PrepareOutgoing sets mesh fields on a new outgoing message.
func (mr *MeshRouter) PrepareOutgoing(msg *Message) {
	if msg.MsgID == "" {
		msg.MsgID = NewMessageID()
	}
	if msg.TTL == 0 {
		msg.TTL = MaxTTL
	}
	if msg.Path == nil {
		msg.Path = []string{mr.serverName}
	}
	// Mark as seen so we don't process our own message if it comes back
	mr.markSeen(msg.MsgID)
}

// ShouldProcess checks if an incoming message should be processed.
// Returns false if:
// - We originated it (loop)
// - We already processed it (dedup)
// - TTL is exhausted
// - We are in the path (loop)
func (mr *MeshRouter) ShouldProcess(msg *Message) bool {
	// Originated by us
	if msg.Origin == mr.serverName {
		return false
	}

	// TTL exhausted
	if msg.TTL <= 0 {
		return false
	}

	// Already in path (loop detection)
	for _, hop := range msg.Path {
		if hop == mr.serverName {
			return false
		}
	}

	// Already seen (deduplication)
	if msg.MsgID != "" && mr.alreadySeen(msg.MsgID) {
		return false
	}

	// Mark as seen
	if msg.MsgID != "" {
		mr.markSeen(msg.MsgID)
	}

	return true
}

// PrepareRelay prepares a message for relaying to the next hop.
// Returns a copy with decremented TTL and updated path.
func (mr *MeshRouter) PrepareRelay(msg *Message) *Message {
	relay := *msg
	relay.TTL = msg.TTL - 1
	relay.Path = make([]string, len(msg.Path)+1)
	copy(relay.Path, msg.Path)
	relay.Path[len(msg.Path)] = mr.serverName
	return &relay
}

// ShouldRelay checks if a message should be forwarded to other peers.
func (mr *MeshRouter) ShouldRelay(msg *Message) bool {
	return msg.TTL > 1
}

// IsInPath checks if a server name is in the message path.
func IsInPath(msg *Message, serverName string) bool {
	for _, hop := range msg.Path {
		if hop == serverName {
			return true
		}
	}
	return false
}

func (mr *MeshRouter) markSeen(msgID string) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	mr.seen[msgID] = time.Now()
}

func (mr *MeshRouter) alreadySeen(msgID string) bool {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	_, exists := mr.seen[msgID]
	return exists
}

func (mr *MeshRouter) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		mr.mu.Lock()
		cutoff := time.Now().Add(-deduplicationWindow)
		for id, ts := range mr.seen {
			if ts.Before(cutoff) {
				delete(mr.seen, id)
			}
		}
		mr.mu.Unlock()
	}
}
