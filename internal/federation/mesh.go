package federation

import (
	"sync"
	"time"

	"github.com/google/uuid"

	federationv2pb "github.com/freetetra/server/internal/federation/proto/v2"
)

// MeshRouter handles message deduplication, TTL, and multi-hop relay.
type MeshRouter struct {
	serverName string

	mu   sync.Mutex
	seen map[string]time.Time // msg_id -> timestamp (for dedup)
}

const (
	// MaxTTL is the maximum number of hops a federation message can travel.
	MaxTTL              = 10
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

// PrepareOutgoing sets mesh fields on a new outgoing control message.
func (mr *MeshRouter) PrepareOutgoing(ctrl *federationv2pb.Control) {
	if ctrl.GetMsgId() == "" {
		ctrl.MsgId = NewMessageID()
	}
	if ctrl.GetTtl() == 0 {
		ctrl.Ttl = int32(MaxTTL)
	}
	if len(ctrl.GetPath()) == 0 {
		ctrl.Path = []string{mr.serverName}
	}
	mr.markSeen(ctrl.GetMsgId())
}

// ShouldProcess checks if an incoming control message should be processed.
func (mr *MeshRouter) ShouldProcess(ctrl *federationv2pb.Control) bool {
	if ctrl.GetOrigin() == mr.serverName {
		return false
	}
	if ctrl.GetTtl() <= 0 {
		return false
	}
	for _, hop := range ctrl.GetPath() {
		if hop == mr.serverName {
			return false
		}
	}
	if ctrl.GetMsgId() != "" && mr.alreadySeen(ctrl.GetMsgId()) {
		return false
	}
	if ctrl.GetMsgId() != "" {
		mr.markSeen(ctrl.GetMsgId())
	}
	return true
}

// PrepareRelay returns a relay copy with decremented TTL and updated path.
func (mr *MeshRouter) PrepareRelay(ctrl *federationv2pb.Control) *federationv2pb.Control {
	newPath := make([]string, len(ctrl.GetPath())+1)
	copy(newPath, ctrl.GetPath())
	newPath[len(ctrl.GetPath())] = mr.serverName
	return &federationv2pb.Control{
		Origin:          ctrl.GetOrigin(),
		ProtocolVersion: ctrl.GetProtocolVersion(),
		MsgId:           ctrl.GetMsgId(),
		Ttl:             ctrl.GetTtl() - 1,
		Path:            newPath,
		Payload:         ctrl.GetPayload(),
	}
}

// ShouldRelay checks if a control message should be forwarded further.
func (mr *MeshRouter) ShouldRelay(ctrl *federationv2pb.Control) bool {
	return ctrl.GetTtl() > 1
}

// IsInPath checks if a server name is in the message path.
func IsInPath(ctrl *federationv2pb.Control, serverName string) bool {
	for _, hop := range ctrl.GetPath() {
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
