# Federation Protobuf-Only Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the JSON-shaped `Message` intermediate type and the `v2_codec` translation layer from `internal/federation/`, so federation code operates directly on the protobuf types it already serializes to the wire. Also remove the UDP voice plane and the unused v1 `Envelope` proto.

**Architecture:** Single feature branch `feat/federation-protobuf-only`. Each task is one atomic commit that compiles and passes `go vet ./...`. Mesh routing, hub dispatch, and peer I/O all shift from `*federation.Message` to `*federationv2pb.Control`. Wire format (`StreamFrame { oneof Control | VoiceFrame }`) is unchanged — pre- and post-refactor builds can federate together.

**Tech Stack:** Go 1.22+, `google.golang.org/grpc`, `google.golang.org/protobuf`. Existing test harnesses: `tests/federation-loopback/run.sh` (3-node gossip discovery), `tests/federation-freecat/run.sh` (loopback peer + TG25 echo). No Go unit tests for federation exist; integration tests are the regression net.

**Spec:** [docs/superpowers/specs/2026-05-23-federation-protobuf-only-design.md](../specs/2026-05-23-federation-protobuf-only-design.md)

---

## Task 0: Baseline verification

Confirm the tree builds and the existing federation tests pass on `feat/federation-protobuf-only` *before* any code change. If something is already broken, fix or document it before touching federation code.

**Files:** none (read-only)

- [ ] **Step 1: Verify branch + clean working state for federation code**

```bash
git status -s internal/federation/
git rev-parse --abbrev-ref HEAD
```

Expected: branch is `feat/federation-protobuf-only`. `internal/federation/hub.go` may show `M` (pre-existing reconnect/backoff edit from the prior session — keep). No other federation file should be staged. Other top-level dirty files in the working tree are unrelated and stay alone.

- [ ] **Step 2: Build the world**

```bash
go build ./...
```

Expected: exit 0, no output. If it fails, stop and report — the baseline is broken.

- [ ] **Step 3: Vet the world**

```bash
go vet ./...
```

Expected: exit 0. Stop if any vet error appears.

- [ ] **Step 4: Run unit tests**

```bash
go test ./...
```

Expected: all packages pass. Federation has no unit tests, so this is mostly Brew + service tests. Stop on failure.

- [ ] **Step 5: Note baseline. No commit.**

If any of steps 2–4 failed, write a short note in the PR description and either revert or rebase on a known-good commit. Do not proceed.

---

## Task 1: Move constants out of `protocol.go`

`ProtocolVersion` and `MaxTTL` need to survive the eventual deletion of `protocol.go`. Move them now so subsequent tasks can delete the file cleanly. Pure relocation — zero behavioral change.

**Files:**
- Modify: `internal/federation/hub.go`
- Modify: `internal/federation/mesh.go`
- Modify: `internal/federation/protocol.go`

- [ ] **Step 1: Add `ProtocolVersion` to `hub.go`**

Open `internal/federation/hub.go`. After the existing `federationSubpathHeader` const block at the top, add:

```go
const (
	// ProtocolVersion advertised in the Hello handshake.
	ProtocolVersion = 2
)
```

- [ ] **Step 2: Add `MaxTTL` to `mesh.go`**

Open `internal/federation/mesh.go`. Locate the existing `const ( deduplicationWindow ... )` block near the top and extend it:

```go
const (
	// MaxTTL is the maximum number of hops a federation message can travel.
	MaxTTL              = 10
	deduplicationWindow = 30 * time.Second
	cleanupInterval    = 10 * time.Second
)
```

- [ ] **Step 3: Remove constants from `protocol.go`**

Open `internal/federation/protocol.go`. Delete the two lines:

```go
// ProtocolVersion is the federation protocol version.
const ProtocolVersion = 2

// MaxTTL is the maximum number of hops a message can travel.
const MaxTTL = 10
```

Leave everything else in `protocol.go` (the `Message` struct, `Msg*` constants, `Capability*` constants, etc.) untouched in this task.

- [ ] **Step 4: Build + vet**

```bash
go build ./... && go vet ./...
```

Expected: exit 0. There are no duplicate-const errors because the same identifier appears in only one file at a time.

- [ ] **Step 5: Commit**

```bash
git add internal/federation/hub.go internal/federation/mesh.go internal/federation/protocol.go
git commit -m "$(cat <<'EOF'
federation: move ProtocolVersion + MaxTTL out of protocol.go

Preparation for removing protocol.go entirely. Pure relocation; no
behavior change.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Add `Peer.SendControl` alongside `SendJSON`

Introduce the protobuf-native send path. `SendJSON` stays around for now so other call sites compile.

**Files:**
- Modify: `internal/federation/peer.go`

- [ ] **Step 1: Add `SendControl` method**

Open `internal/federation/peer.go`. Locate `func (p *Peer) SendJSON(msg *Message) error` (around line 75). Add the new method directly above it:

```go
// SendControl enqueues a typed control message for delivery to the peer.
// This is the protobuf-native send path; SendJSON is the legacy adapter
// kept around during the transition off the Message struct.
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
		p.logger.Printf("federation: send buffer full for peer %s, dropping control message", p.Name)
		return nil
	}
}
```

- [ ] **Step 2: Build + vet**

```bash
go build ./... && go vet ./...
```

Expected: exit 0. `SendControl` is unused — that's fine; it'll have callers in Task 3.

- [ ] **Step 3: Commit**

```bash
git add internal/federation/peer.go
git commit -m "$(cat <<'EOF'
federation: add Peer.SendControl alongside SendJSON

New protobuf-native send entry point. SendJSON remains during transition.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Switch mesh routing + outbound to `*Control`

Mesh signatures flip to `*federationv2pb.Control`. Every `Broadcast*` method, `buildHello`, `sendPeerExchange`, `sendUsersDBOffer`, `sendFullSync`, `relayToPeers`, `broadcastToAllPeers` builds and emits `*Control` directly. After this commit, outbound never touches `Message`. Inbound still uses `Message` via the existing `handleControlMessage` → `handleJSONMessage` path (Task 4 handles that).

**Files:**
- Modify: `internal/federation/mesh.go`
- Modify: `internal/federation/hub.go`

- [ ] **Step 1: Rewrite mesh.go to take `*Control`**

Open `internal/federation/mesh.go`. Replace the function bodies that currently read `msg.MsgID`, `msg.TTL`, `msg.Path`, `msg.Origin` so they read the proto getters instead. Final file should look like:

```go
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
	relay := *ctrl
	relay.Ttl = ctrl.GetTtl() - 1
	relay.Path = make([]string, len(ctrl.GetPath())+1)
	copy(relay.Path, ctrl.GetPath())
	relay.Path[len(ctrl.GetPath())] = mr.serverName
	return &relay
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
```

- [ ] **Step 2: Rewrite `buildHello` to return `*Control`**

In `internal/federation/hub.go`, replace the existing `buildHello` body with:

```go
// buildHello constructs the Hello control message advertised to peers.
func (h *Hub) buildHello(peerName string) *federationv2pb.Control {
	return &federationv2pb.Control{
		Origin:          h.serverName,
		ProtocolVersion: ProtocolVersion,
		Payload: &federationv2pb.Control_Hello{
			Hello: &federationv2pb.Hello{},
		},
	}
}
```

Note: UDP fields drop out (Task 6 removes the UDP plane wholesale; we're pre-empting here because retaining `h.udpVoice` reads here would require keeping that field around longer). The `Hello` proto still has `udp_addr` / `udp_token` fields — they're emitted as empty strings now, which protobuf encodes as zero bytes. Wire-compatible.

`peerName` is now an unused parameter; keep the signature for symmetry with the rest of the codebase (`buildHello(peerName)` reads correctly at every call site), but add a leading `_` if Go's lint complains:

```go
func (h *Hub) buildHello(_ string) *federationv2pb.Control {
```

- [ ] **Step 3: Rewrite every `Hub.Broadcast*` method to build `*Control`**

The pattern: where the current code is

```go
func (h *Hub) BroadcastSubscriber(issi uint32, action string, gssis []uint32) {
	msg := &Message{
		Type:   MsgSubscriberUpdate,
		Origin: h.serverName,
		ISSI:   issi,
		Action: action,
		GSSIs:  gssis,
	}
	h.mesh.PrepareOutgoing(msg)
	h.broadcastToAllPeers(msg)
}
```

the rewritten version is:

```go
func (h *Hub) BroadcastSubscriber(issi uint32, action string, gssis []uint32) {
	subAction := federationv2pb.SubscriberUpdate_ACTION_UNSPECIFIED
	switch action {
	case "register":
		subAction = federationv2pb.SubscriberUpdate_ACTION_REGISTER
	case "deregister":
		subAction = federationv2pb.SubscriberUpdate_ACTION_DEREGISTER
	}
	ctrl := &federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_SubscriberUpdate{
			SubscriberUpdate: &federationv2pb.SubscriberUpdate{
				Issi:   issi,
				Action: subAction,
				Gssis:  append([]uint32(nil), gssis...),
			},
		},
	}
	h.mesh.PrepareOutgoing(ctrl)
	h.broadcastToAllPeers(ctrl)
}
```

Apply the same pattern to every public `Broadcast*` method:

| Method | Payload type | Action mapping |
|---|---|---|
| `BroadcastSubscriber` | `SubscriberUpdate` | `"register"`→`ACTION_REGISTER`, `"deregister"`→`ACTION_DEREGISTER` |
| `BroadcastAffiliate` | `AffiliateUpdate` | `"affiliate"`→`ACTION_AFFILIATE`, `"deaffiliate"`→`ACTION_DEAFFILIATE` |
| `BroadcastCallStart` | `CallStart` | n/a — copy uuid/source/dest/priority/service fields |
| `BroadcastCallEnd` | `CallEnd` | n/a — copy uuid/cause |
| `BroadcastSDS` | `SdsRelay` | hex-decode `sdsDataHex` into `SdsData []byte` |
| `BroadcastPositionSample` | `PositionSample` | copy issi/lat/lon/repeater |
| `BroadcastStation` | `StationUpdate` | `structpb.NewStruct(stationMap)` |

The SDS one in particular:

```go
func (h *Hub) BroadcastSDS(sourceISSI, destISSI uint32, sdsDataHex string) {
	raw, err := hex.DecodeString(sdsDataHex)
	if err != nil {
		h.logger.Printf("federation: invalid local SDS hex: %v", err)
		return
	}
	ctrl := &federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_SdsRelay{
			SdsRelay: &federationv2pb.SdsRelay{
				SourceIssi: sourceISSI,
				DestIssi:   destISSI,
				SdsData:    raw,
			},
		},
	}
	h.mesh.PrepareOutgoing(ctrl)
	h.broadcastToAllPeers(ctrl)
}
```

And the station one:

```go
func (h *Hub) BroadcastStation(station map[string]any) {
	st, err := structpb.NewStruct(station)
	if err != nil {
		h.logger.Printf("federation: cannot encode station map: %v", err)
		return
	}
	ctrl := &federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_StationUpdate{
			StationUpdate: &federationv2pb.StationUpdate{Station: st},
		},
	}
	h.mesh.PrepareOutgoing(ctrl)
	h.broadcastToAllPeers(ctrl)
}
```

Add the import for `structpb` to `hub.go`:

```go
"google.golang.org/protobuf/types/known/structpb"
```

And keep `"encoding/hex"` since `BroadcastSDS` still uses it.

- [ ] **Step 4: Special case — `BroadcastCallStart` tracks `activeCalls`**

The existing implementation also bookkeeps `activeCalls[callUUID][peerName] = true` for each outgoing peer. Preserve that bookkeeping in the rewritten version. The full replacement:

```go
func (h *Hub) BroadcastCallStart(callUUID string, sourceISSI, destGSSI uint32, priority uint8, service uint16) {
	if !isFederatedGSSI(destGSSI) {
		return
	}
	ctrl := &federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_CallStart{
			CallStart: &federationv2pb.CallStart{
				Uuid:       callUUID,
				SourceIssi: sourceISSI,
				DestGssi:   destGSSI,
				Priority:   uint32(priority),
				Service:    uint32(service),
			},
		},
	}
	h.mesh.PrepareOutgoing(ctrl)

	h.callMu.Lock()
	if h.activeCalls[callUUID] == nil {
		h.activeCalls[callUUID] = make(map[string]bool)
	}
	h.callMu.Unlock()

	h.mu.RLock()
	for _, peer := range h.peers {
		_ = peer.SendControl(ctrl)
		h.callMu.Lock()
		h.activeCalls[callUUID][peer.Name] = true
		h.callMu.Unlock()
	}
	h.mu.RUnlock()
}
```

- [ ] **Step 5: Switch `broadcastToAllPeers` and `relayToPeers` to `*Control`**

```go
func (h *Hub) broadcastToAllPeers(ctrl *federationv2pb.Control) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, peer := range h.peers {
		_ = peer.SendControl(ctrl)
	}
}

func (h *Hub) relayToPeers(ctrl *federationv2pb.Control, excludeName string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, peer := range h.peers {
		if peer.Name != excludeName {
			_ = peer.SendControl(ctrl)
		}
	}
}
```

- [ ] **Step 6: Switch `sendPeerExchange`, `sendUsersDBOffer`, `sendFullSync` to send `*Control`**

```go
func (h *Hub) sendPeerExchange(peer *Peer) {
	h.knownMu.RLock()
	gp := make([]*federationv2pb.GossipPeer, 0, len(h.knownPeers)+1)
	if h.selfURL != "" {
		gp = append(gp, &federationv2pb.GossipPeer{Name: h.serverName, Url: h.selfURL})
	}
	for name, u := range h.knownPeers {
		if name != peer.Name {
			gp = append(gp, &federationv2pb.GossipPeer{Name: name, Url: u})
		}
	}
	h.knownMu.RUnlock()

	if len(gp) == 0 {
		return
	}
	_ = peer.SendControl(&federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_PeerExchange{
			PeerExchange: &federationv2pb.PeerExchange{Peers: gp},
		},
	})
	h.logger.Printf("federation: sent %d known peer(s) to %s", len(gp), peer.Name)
}

func (h *Hub) sendUsersDBOffer(peer *Peer) {
	if h.handler == nil || h.selfURL == "" {
		return
	}
	ts, count := h.handler.GetUsersDBInfo()
	if ts == "" || count == 0 {
		return
	}
	baseURL := h.selfURL
	baseURL = strings.Replace(baseURL, "wss://", "https://", 1)
	baseURL = strings.Replace(baseURL, "ws://", "http://", 1)
	baseURL = strings.TrimSuffix(baseURL, "/peer/")
	baseURL = strings.TrimSuffix(baseURL, "/peer")
	dbURL := baseURL + "/api/users.txt"

	_ = peer.SendControl(&federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_UsersDbOffer{
			UsersDbOffer: &federationv2pb.UsersDbOffer{
				Timestamp: ts,
				Url:       dbURL,
				Count:     uint32(count),
			},
		},
	})
}

func (h *Hub) sendFullSync(peer *Peer) {
	if h.handler == nil {
		return
	}
	localSubs := h.handler.GetLocalSubscribers()
	subs := make(map[string]*federationv2pb.SyncSubscriber, len(localSubs))
	for issi, gssis := range localSubs {
		subs[fmt.Sprintf("%d", issi)] = &federationv2pb.SyncSubscriber{
			Gssis: append([]uint32(nil), gssis...),
		}
	}
	_ = peer.SendControl(&federationv2pb.Control{
		Origin: h.serverName,
		Payload: &federationv2pb.Control_SyncResponse{
			SyncResponse: &federationv2pb.SyncResponse{Subscribers: subs},
		},
	})
	h.logger.Printf("federation: sent sync to %s (%d subscribers)", peer.Name, len(subs))
}
```

- [ ] **Step 7: Switch the `peer.SendJSON(h.buildHello(...))` call sites to `peer.SendControl`**

Two sites: in `maintainOutgoingPeer` and in `Connect`. The change is now a direct rename since `buildHello` returns `*Control`:

```go
_ = peer.SendControl(h.buildHello(pc.Name))
```

and:

```go
_ = peer.SendControl(h.buildHello(peerName))
```

- [ ] **Step 8: Adjust relay sites inside the existing `handleJSONMessage`**

`handleJSONMessage` still operates on `*Message` (Task 4 rewrites it). For now, the relay sites that previously called `peer.SendJSON(relay)` must keep working because `Message`-based relay hasn't been migrated yet. Leave the `case MsgSubscriberUpdate:` … `case MsgStationUpdate:` arms in `handleJSONMessage` alone for this commit. They still build `*Message` and pass to `peer.SendJSON` / `h.relayToPeers(relay, ...)`.

But `h.relayToPeers` now takes `*Control`, not `*Message`. To keep the legacy inbound path compiling for this commit, add a temporary bridge in `hub.go`:

```go
// relayMessageToPeers is a transitional helper used by the legacy
// Message-based inbound path. Removed in Task 4.
func (h *Hub) relayMessageToPeers(msg *Message, excludeName string) {
	ctrl := messageToControl(msg)
	if ctrl == nil {
		return
	}
	h.relayToPeers(ctrl, excludeName)
}
```

Replace every `h.relayToPeers(relay, peer.Name)` call inside `handleJSONMessage`'s switch arms with `h.relayMessageToPeers(relay, peer.Name)`.

For the `MsgCallStart` and `MsgCallEnd` arms that loop `for _, p := range h.peers { p.SendJSON(relay) ... }`, leave them as-is. They go away entirely in Task 4.

For `h.mesh.PrepareRelay(&msg)` (returns `*Message` today, returns `*Control` after this commit): `handleJSONMessage` currently passes a `*Message`. After mesh.go's signature change, this won't compile. Wrap the call in the same arms:

```go
relay := controlToMessage(h.mesh.PrepareRelay(messageToControl(&msg)))
```

This is ugly but only lives until Task 4 deletes `handleJSONMessage` entirely.

Same fix for `h.mesh.ShouldRelay(&msg)` and `h.mesh.ShouldProcess(...)` inside `handleJSONMessage`: wrap the `*Message` in `messageToControl(&msg)`.

- [ ] **Step 9: Build + vet**

```bash
go build ./... && go vet ./...
```

Expected: exit 0. If you missed a call site, the compiler will tell you exactly where.

- [ ] **Step 10: Run unit tests**

```bash
go test ./...
```

Expected: pass.

- [ ] **Step 11: Commit**

```bash
git add internal/federation/mesh.go internal/federation/hub.go
git commit -m "$(cat <<'EOF'
federation: migrate mesh + outbound to typed protobuf

MeshRouter operates on *federationv2pb.Control. Every Broadcast* and
internal send helper builds Control directly and calls SendControl.
Inbound path still goes through Message via a transitional adapter
(relayMessageToPeers), removed in the next commit.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Migrate inbound to direct proto dispatch

Rewrite `handleControlMessage` so it switches on the proto `oneof` directly. Extract each case into a small named method. Delete `handleJSONMessage`, `relayMessageToPeers`, and the transition adapters from Task 3.

**Files:**
- Modify: `internal/federation/hub.go`

- [ ] **Step 1: Replace `handleControlMessage` body**

Locate the existing `handleControlMessage` (around line 393). Replace with:

```go
func (h *Hub) handleControlMessage(peer *Peer, ctrl *federationv2pb.Control) {
	if ctrl == nil {
		return
	}

	// Mesh routing gate — preserves the exact set of message types treated
	// as control-plane (origin-only check) by the legacy handleJSONMessage.
	// NOTE: UsersDbOffer / UsersDbRequest fall into the default (data-plane)
	// branch here, matching pre-refactor behavior. The sender does not call
	// PrepareOutgoing on those messages, so TTL stays 0 and ShouldProcess
	// drops them. That is a pre-existing bug in users-DB federation, not a
	// regression introduced here; out of scope for this refactor.
	switch ctrl.GetPayload().(type) {
	case *federationv2pb.Control_Hello,
		*federationv2pb.Control_SyncRequest,
		*federationv2pb.Control_SyncResponse,
		*federationv2pb.Control_PeerExchange:
		if ctrl.GetOrigin() == h.serverName {
			return
		}
	default:
		if !h.mesh.ShouldProcess(ctrl) {
			return
		}
	}

	switch p := ctrl.GetPayload().(type) {
	case *federationv2pb.Control_Hello:
		h.handleHello(peer, ctrl, p.Hello)
	case *federationv2pb.Control_SyncRequest:
		h.sendFullSync(peer)
	case *federationv2pb.Control_SyncResponse:
		h.handleSyncResponse(peer, p.SyncResponse)
	case *federationv2pb.Control_UsersDbOffer:
		h.handleUsersDBOffer(peer, p.UsersDbOffer)
	case *federationv2pb.Control_UsersDbRequest:
		h.sendUsersDBOffer(peer)
	case *federationv2pb.Control_PeerExchange:
		h.handlePeerExchange(peer, p.PeerExchange)
	case *federationv2pb.Control_SubscriberUpdate:
		h.handleSubscriberUpdate(peer, ctrl, p.SubscriberUpdate)
	case *federationv2pb.Control_AffiliateUpdate:
		h.handleAffiliateUpdate(peer, ctrl, p.AffiliateUpdate)
	case *federationv2pb.Control_CallStart:
		h.handleCallStart(peer, ctrl, p.CallStart)
	case *federationv2pb.Control_CallEnd:
		h.handleCallEnd(peer, ctrl, p.CallEnd)
	case *federationv2pb.Control_SdsRelay:
		h.handleSdsRelay(peer, ctrl, p.SdsRelay)
	case *federationv2pb.Control_PositionSample:
		h.handlePositionSample(peer, ctrl, p.PositionSample)
	case *federationv2pb.Control_StationUpdate:
		h.handleStationUpdate(peer, ctrl, p.StationUpdate)
	}
}
```

- [ ] **Step 2: Add the per-payload handler methods**

Append (or insert near `handleControlMessage`):

```go
func (h *Hub) handleHello(peer *Peer, ctrl *federationv2pb.Control, _ *federationv2pb.Hello) {
	h.logger.Printf("federation: hello from %s (version %d)", ctrl.GetOrigin(), ctrl.GetProtocolVersion())
	if origin := ctrl.GetOrigin(); origin != "" && origin != peer.Name {
		old := peer.Name
		h.renamePeer(peer, origin)
		h.logger.Printf("federation: renamed peer %s -> %s", old, origin)
	}
	h.sendPeerExchange(peer)
	h.sendUsersDBOffer(peer)
}

func (h *Hub) handleSyncResponse(peer *Peer, sr *federationv2pb.SyncResponse) {
	for issiStr, info := range sr.GetSubscribers() {
		var issi uint32
		fmt.Sscanf(issiStr, "%d", &issi)
		peer.RegisterISSI(issi)
		peer.AffiliateISSI(issi, info.GetGssis())
	}
	h.logger.Printf("federation: synced %d subscribers from %s", len(sr.GetSubscribers()), peer.Name)
}

func (h *Hub) handleUsersDBOffer(peer *Peer, off *federationv2pb.UsersDbOffer) {
	if h.handler == nil || off.GetUrl() == "" {
		return
	}
	ourTS, _ := h.handler.GetUsersDBInfo()
	if ourTS == "" || off.GetTimestamp() > ourTS {
		h.logger.Printf("federation: downloading users DB from %s (%d users, ts=%s)",
			peer.Name, off.GetCount(), off.GetTimestamp())
		if err := h.handler.DownloadUsersDBFrom(off.GetUrl()); err != nil {
			h.logger.Printf("federation: users DB download failed: %v", err)
		} else {
			h.logger.Printf("federation: users DB updated from %s", peer.Name)
		}
	}
}

func (h *Hub) handlePeerExchange(peer *Peer, px *federationv2pb.PeerExchange) {
	newPeers := 0
	for _, gp := range px.GetPeers() {
		if gp.GetName() == h.serverName || gp.GetUrl() == "" {
			continue
		}
		if h.tryAddDiscoveredPeer(gp.GetName(), gp.GetUrl()) {
			newPeers++
		}
	}
	if newPeers > 0 {
		h.logger.Printf("federation: discovered %d new peer(s) via %s", newPeers, peer.Name)
	}
}

func (h *Hub) handleSubscriberUpdate(peer *Peer, ctrl *federationv2pb.Control, up *federationv2pb.SubscriberUpdate) {
	switch up.GetAction() {
	case federationv2pb.SubscriberUpdate_ACTION_REGISTER:
		peer.RegisterISSI(up.GetIssi())
		peer.AffiliateISSI(up.GetIssi(), up.GetGssis())
		h.logger.Printf("federation: %s registered ISSI %d (GSSIs=%v) [ttl=%d path=%v]",
			peer.Name, up.GetIssi(), up.GetGssis(), ctrl.GetTtl(), ctrl.GetPath())
	case federationv2pb.SubscriberUpdate_ACTION_DEREGISTER:
		peer.DeregisterISSI(up.GetIssi())
		h.logger.Printf("federation: %s deregistered ISSI %d [ttl=%d]", peer.Name, up.GetIssi(), ctrl.GetTtl())
	}
	if h.mesh.ShouldRelay(ctrl) {
		h.relayToPeers(h.mesh.PrepareRelay(ctrl), peer.Name)
	}
}

func (h *Hub) handleAffiliateUpdate(peer *Peer, ctrl *federationv2pb.Control, up *federationv2pb.AffiliateUpdate) {
	switch up.GetAction() {
	case federationv2pb.AffiliateUpdate_ACTION_AFFILIATE:
		peer.AffiliateISSI(up.GetIssi(), up.GetGssis())
		h.logger.Printf("federation: %s affiliated ISSI %d -> GSSIs %v [ttl=%d]",
			peer.Name, up.GetIssi(), up.GetGssis(), ctrl.GetTtl())
	case federationv2pb.AffiliateUpdate_ACTION_DEAFFILIATE:
		peer.DeaffiliateISSI(up.GetIssi(), up.GetGssis())
		h.logger.Printf("federation: %s deaffiliated ISSI %d from GSSIs %v [ttl=%d]",
			peer.Name, up.GetIssi(), up.GetGssis(), ctrl.GetTtl())
	}
	if h.mesh.ShouldRelay(ctrl) {
		h.relayToPeers(h.mesh.PrepareRelay(ctrl), peer.Name)
	}
}

func (h *Hub) handleCallStart(peer *Peer, ctrl *federationv2pb.Control, cs *federationv2pb.CallStart) {
	if h.handler != nil {
		h.handler.OnPeerCallStart(peer.Name, cs.GetUuid(), cs.GetSourceIssi(),
			cs.GetDestGssi(), uint8(cs.GetPriority()), uint16(cs.GetService()))
	}
	h.callMu.Lock()
	if h.activeCalls[cs.GetUuid()] == nil {
		h.activeCalls[cs.GetUuid()] = make(map[string]bool)
	}
	h.callMu.Unlock()

	if !h.mesh.ShouldRelay(ctrl) {
		return
	}
	relay := h.mesh.PrepareRelay(ctrl)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, p := range h.peers {
		if p.Name == peer.Name || IsInPath(ctrl, p.Name) {
			continue
		}
		_ = p.SendControl(relay)
		h.callMu.Lock()
		h.activeCalls[cs.GetUuid()][p.Name] = true
		h.callMu.Unlock()
	}
}

func (h *Hub) handleCallEnd(peer *Peer, ctrl *federationv2pb.Control, ce *federationv2pb.CallEnd) {
	if h.handler != nil {
		h.handler.OnPeerCallEnd(peer.Name, ce.GetUuid(), uint8(ce.GetCause()))
	}
	h.callMu.Lock()
	delete(h.activeCalls, ce.GetUuid())
	h.callMu.Unlock()

	if !h.mesh.ShouldRelay(ctrl) {
		return
	}
	relay := h.mesh.PrepareRelay(ctrl)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, p := range h.peers {
		if p.Name == peer.Name || IsInPath(ctrl, p.Name) {
			continue
		}
		_ = p.SendControl(relay)
	}
}

func (h *Hub) handleSdsRelay(peer *Peer, ctrl *federationv2pb.Control, sr *federationv2pb.SdsRelay) {
	if h.handler != nil {
		h.handler.OnPeerSDSRelay(peer.Name, sr.GetSourceIssi(), sr.GetDestIssi(), hex.EncodeToString(sr.GetSdsData()))
	}
	if h.mesh.ShouldRelay(ctrl) {
		h.relayToPeers(h.mesh.PrepareRelay(ctrl), peer.Name)
	}
}

func (h *Hub) handlePositionSample(peer *Peer, ctrl *federationv2pb.Control, ps *federationv2pb.PositionSample) {
	if h.handler != nil {
		h.handler.OnPeerPositionSample(peer.Name, ps.GetIssi(), ps.GetLat(), ps.GetLon(), ps.GetRepeater())
	}
	if h.mesh.ShouldRelay(ctrl) {
		h.relayToPeers(h.mesh.PrepareRelay(ctrl), peer.Name)
	}
}

func (h *Hub) handleStationUpdate(peer *Peer, ctrl *federationv2pb.Control, su *federationv2pb.StationUpdate) {
	if h.handler != nil && su.GetStation() != nil {
		h.handler.OnPeerStationUpdate(peer.Name, su.GetStation().AsMap())
	}
	if h.mesh.ShouldRelay(ctrl) {
		h.relayToPeers(h.mesh.PrepareRelay(ctrl), peer.Name)
	}
}
```

- [ ] **Step 3: Delete `handleJSONMessage` and the transition adapter**

Remove the entire `handleJSONMessage` function. Remove `relayMessageToPeers` (the temporary bridge added in Task 3 step 8). The `readLoop`'s call site (`if ctrl := frame.GetControl(); ctrl != nil { h.handleControlMessage(peer, ctrl) }`) already calls `handleControlMessage`, so no caller change is needed.

- [ ] **Step 4: Build + vet**

```bash
go build ./... && go vet ./...
```

Expected: exit 0. After this commit, `controlToMessage` and `messageToControl` have no callers — the compiler will warn if you missed a site (`declared and not used` doesn't apply to package-level funcs, so dead-code detection happens at next-task delete time).

- [ ] **Step 5: Run tests**

```bash
go test ./...
```

- [ ] **Step 6: Commit**

```bash
git add internal/federation/hub.go
git commit -m "$(cat <<'EOF'
federation: dispatch inbound directly on protobuf oneof

handleControlMessage now switches on ctrl.GetPayload() and dispatches
to per-payload handlers. Removes the proto -> Go struct -> JSON ->
Go struct roundtrip and the transitional Message-based relay adapter.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Delete the codec + legacy binary paths

`messageToControl` / `controlToMessage` and `Peer.SendJSON` are now unreferenced. So are `handleBinaryMessage` and `Peer.SendBinary`. Delete.

**Files:**
- Delete: `internal/federation/v2_codec.go`
- Modify: `internal/federation/peer.go`
- Modify: `internal/federation/hub.go`

- [ ] **Step 1: Verify no callers**

```bash
grep -rn "messageToControl\|controlToMessage\|SendJSON\|handleBinaryMessage\|SendBinary" --include='*.go' internal/
```

Expected output: only the definitions themselves (in `v2_codec.go`, `peer.go`, `hub.go`). No call sites outside those files.

If any external caller appears (e.g. in `internal/service/`), stop and investigate — the task list missed something.

- [ ] **Step 2: Delete v2_codec.go**

```bash
git rm internal/federation/v2_codec.go
```

- [ ] **Step 3: Delete `Peer.SendJSON` and `Peer.SendBinary`**

In `internal/federation/peer.go`, delete the entire methods:

```go
// SendJSON sends a JSON federation message to the peer.
func (p *Peer) SendJSON(msg *Message) error { ... }

// SendBinary sends raw binary data to the peer (for voice frames).
func (p *Peer) SendBinary(data []byte) error { ... }
```

- [ ] **Step 4: Delete `handleBinaryMessage`**

In `internal/federation/hub.go`, delete:

```go
// handleBinaryMessage processes binary federation data (voice frames).
// Format: callUUID (36 bytes ASCII) + frame payload
func (h *Hub) handleBinaryMessage(peer *Peer, data []byte) {
	if len(data) < 36 {
		return
	}
	h.handleVoiceFrame(peer, string(data[:36]), data[36:])
}
```

- [ ] **Step 5: Build + vet**

```bash
go build ./... && go vet ./...
```

Expected: exit 0. If the v2_codec deletion broke something, the compiler points to the unresolved import / function.

- [ ] **Step 6: Test**

```bash
go test ./...
```

- [ ] **Step 7: Commit**

```bash
git add -u internal/federation/
git commit -m "$(cat <<'EOF'
federation: drop v2_codec, SendJSON, and the legacy binary fast-path

v2_codec.go (Message <-> Control translation), Peer.SendJSON,
Peer.SendBinary, and Hub.handleBinaryMessage are all unreferenced
after the inbound/outbound migration.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Delete the UDP voice plane

`udp_voice.go`, `Hub.udpVoice` integration, `EnableUDPVoice`, `FederationConfig.UDP*`, and the matching env vars all go.

**Files:**
- Delete: `internal/federation/udp_voice.go`
- Modify: `internal/federation/hub.go`
- Modify: `internal/service/federation_bridge.go`
- Modify: `internal/config/config.go`
- Modify: `.env.example`

- [ ] **Step 1: Delete udp_voice.go**

```bash
git rm internal/federation/udp_voice.go
```

- [ ] **Step 2: Strip UDP plumbing from `Hub`**

In `internal/federation/hub.go`, remove from the `Hub` struct:

```go
udpVoice *UDPVoice

udpInTokenMu sync.RWMutex
udpInTokens  map[string]string
```

Remove from `NewHub`'s returned struct literal:

```go
udpInTokens: make(map[string]string),
```

Delete the `EnableUDPVoice` method entirely.

Delete the `NewToken` helper if it lives in `udp_voice.go` and is only used there. (It should — verify with grep.)

Delete the `unregisterPeer` UDP cleanup:

```go
if h.udpVoice != nil {
	h.udpVoice.UnregisterPeer(peer.Name)
}
h.udpInTokenMu.Lock()
delete(h.udpInTokens, peer.Name)
h.udpInTokenMu.Unlock()
```

Delete the `renamePeer` UDP-token cache rename block:

```go
h.udpInTokenMu.Lock()
if tok, ok := h.udpInTokens[oldName]; ok {
	delete(h.udpInTokens, oldName)
	if _, exists := h.udpInTokens[newName]; !exists {
		h.udpInTokens[newName] = tok
	}
}
h.udpInTokenMu.Unlock()
```

Simplify `BroadcastVoiceFrame`:

```go
func (h *Hub) BroadcastVoiceFrame(callUUID string, frameData []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, peer := range h.peers {
		_ = peer.SendVoiceFrame(callUUID, frameData)
	}
}
```

- [ ] **Step 3: Strip UDP plumbing from the bridge**

In `internal/service/federation_bridge.go`, delete:

```go
if cfg.Federation.UDPPort > 0 || cfg.Federation.UDPAdvAddr != "" {
	logger.Printf("federation: UDP voice disabled by configuration, using TCP WS for voice")
}
```

- [ ] **Step 4: Drop UDP fields from config**

In `internal/config/config.go`, remove from `FederationConfig`:

```go
// UDP-Voice-Plane ...
UDPPort    int
UDPAdvAddr string
```

Remove from `LoadFromEnv`:

```go
UDPPort:    envInt("FEDERATION_UDP_PORT", 0),
UDPAdvAddr: env("FEDERATION_UDP_ADV_ADDR", ""),
```

- [ ] **Step 5: Drop UDP lines from `.env.example`**

Open `.env.example`. Delete any `FEDERATION_UDP_PORT=` or `FEDERATION_UDP_ADV_ADDR=` lines (commented or not).

- [ ] **Step 6: Build + vet**

```bash
go build ./... && go vet ./...
```

Expected: exit 0. If you missed an `h.udpVoice` reference or a `UDPPort` reader, the compiler will say so.

- [ ] **Step 7: Test**

```bash
go test ./...
```

- [ ] **Step 8: Commit**

```bash
git add -A internal/federation/ internal/service/federation_bridge.go internal/config/config.go .env.example
git commit -m "$(cat <<'EOF'
federation: remove UDP voice plane

Drops udp_voice.go, the Hub.udpVoice / udpInTokens fields, the
EnableUDPVoice method, the UDP-handshake branch in the Hello path,
FederationConfig.UDP* fields, and the corresponding env vars. Voice
now flows exclusively over the gRPC bidi stream's VoiceFrame.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Delete `protocol.go`, capability remnants, and v1 proto

The Go-side `Message` struct and friends are unreferenced. So are the capability fields on `Peer` and the v1 `Envelope` proto.

**Files:**
- Delete: `internal/federation/protocol.go`
- Delete: `internal/federation/proto/federation.proto`
- Delete: `internal/federation/proto/federation.pb.go`
- Delete: `internal/federation/proto/federation_grpc.pb.go`
- Modify: `internal/federation/peer.go`

- [ ] **Step 1: Verify nothing imports protocol.go's types**

```bash
grep -rn "federation\.Message\b\|federation\.MsgHello\|federation\.MsgSubscriber\|federation\.Capability\|federation\.GossipPeer\|federation\.SyncSubscriber" --include='*.go'
grep -rn "\bMessage\b\|\bMsgHello\|\bCapabilityGRPC\|\bCapabilityTyped" --include='*.go' internal/federation/
```

Expected: zero hits outside `protocol.go` and `peer.go`. (Inside `peer.go`, the only hit will be the `capabilities` field and methods, which we delete in step 3.)

If grep returns anything else, stop and fix the missing migration.

- [ ] **Step 2: Delete protocol.go**

```bash
git rm internal/federation/protocol.go
```

- [ ] **Step 3: Delete capability code from peer.go**

In `internal/federation/peer.go`, remove from the `Peer` struct:

```go
capabilities []string
```

Remove the matching initialization from `newPeer`:

```go
capabilities: make([]string, 0),
```

Delete the methods:

```go
func (p *Peer) SetCapabilities(caps []string) { ... }
func (p *Peer) Capabilities() []string { ... }
func (p *Peer) SupportsCapability(cap string) bool { ... }
```

- [ ] **Step 4: Delete the v1 proto + generated files**

```bash
git rm internal/federation/proto/federation.proto
git rm internal/federation/proto/federation.pb.go
git rm internal/federation/proto/federation_grpc.pb.go
```

- [ ] **Step 5: Build + vet**

```bash
go build ./... && go vet ./...
```

Expected: exit 0.

- [ ] **Step 6: Run all tests**

```bash
go test ./...
```

- [ ] **Step 7: Sanity grep**

```bash
wc -l internal/federation/*.go
ls internal/federation/proto/
```

Expected file list in `internal/federation/`: `hub.go`, `mesh.go`, `peer.go`. (No `protocol.go`, `v2_codec.go`, `udp_voice.go`.)

Expected `proto/`: only the `v2/` subdir.

- [ ] **Step 8: Commit**

```bash
git add -A internal/federation/
git commit -m "$(cat <<'EOF'
federation: drop protocol.go, Peer capability fields, and v1 proto

The Message struct, the Msg*/Capability* string constants, the v1
Envelope proto plus its generated code, and the unused
Peer.{Capabilities,SetCapabilities,SupportsCapability} methods are
all gone. internal/federation/ now compiles against the v2 proto only.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Integration verification

Run the existing federation harnesses end-to-end. Confirm wire compatibility with a pre-refactor build if you can.

**Files:** none (read-only)

- [ ] **Step 1: 3-node loopback test**

```bash
./tests/federation-loopback/run.sh
```

Expected: prints `PASS: ft-a discovered ft-c and loopback module is connected` and exits 0. This exercises Hello handshake, peer-exchange gossip, mesh relay across 3 hops, and the loopback echo module's authentication.

If it fails, capture logs:

```bash
KEEP_RUNNING=1 ./tests/federation-loopback/run.sh
docker compose -p ft-loopback-test logs --tail 200 ft-a ft-b ft-c
docker compose -p ft-loopback-test down -v
```

- [ ] **Step 2: TG25 loopback federation test**

```bash
./tests/federation-freecat/run.sh
```

Expected: prints `PASS: loopback node connected to free.tetra.cat and TG25 echo is running` and exits 0. This requires `free.tetra.cat:8102` to be reachable; if it isn't, skip and document.

- [ ] **Step 3: (Optional) Cross-version smoke test**

To prove wire compatibility:

```bash
# Terminal A — pre-refactor build
git worktree add ../freetetra-pre master
cd ../freetetra-pre && go build -o /tmp/freetetra-pre ./cmd/...

# Terminal B — post-refactor build
cd /home/kschumnn/Desktop/freetetra && go build -o /tmp/freetetra-post ./cmd/...

# Start one of each pointing at the other; confirm "federation: connected to ..."
# appears in both logs and /api/peers lists the counterpart.
```

If both directions handshake and exchange a `MsgSubscriberUpdate`/`SubscriberUpdate` without error, wire compatibility is verified.

- [ ] **Step 4: Final tree-state check**

```bash
git log --oneline master..HEAD
wc -l internal/federation/*.go
```

Expected: 7 commits beyond master, totalling around -500 LOC across federation (positive deltas in hub.go for direct proto construction, larger negative deltas from removed files).

- [ ] **Step 5: No commit needed here**

Task 8 is verification only.

---

## Summary

| Task | Files touched | LOC delta (approx) |
|---|---|---|
| 0 | — | 0 |
| 1 | 3 | 0 (move) |
| 2 | 1 | +20 |
| 3 | 2 | +120 / -80 |
| 4 | 1 | +200 / -180 |
| 5 | 3 | -260 |
| 6 | 5 | -270 |
| 7 | 5 | -160 |
| 8 | — | 0 |

End state: `internal/federation/` is three files (`hub.go`, `mesh.go`, `peer.go`) plus the v2 proto. Federation operates exclusively on `*federationv2pb.Control` and `*federationv2pb.VoiceFrame`. Wire format unchanged.
