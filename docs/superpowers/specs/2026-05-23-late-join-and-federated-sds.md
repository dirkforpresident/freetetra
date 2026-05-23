# Late-join catch-up & federated SDS delivery

Date: 2026-05-23
Status: design — pending approval before implementation
Branch: `feat/federation-protobuf-refactor`

## Goal

Close two visible gaps in the federation experience:

1. **Late-join catch-up** — when a brew client adds a GSSI affiliation while a call is already in flight on that GSSI, send the new subscriber a synthetic `GROUP_TX` so their radio attaches mid-call instead of waiting up to one webradio cycle (~30 s) for the next call_start.
2. **Federated SDS delivery to peer-known ISSIs** — when a local client sends an SDS to a destination ISSI that isn't on this server but *is* on a federation peer, forward via the existing `MsgSDSRelay` path. Today the destination is looked up only in the local subscriber table; if absent, the SDS is dropped silently.

## Non-goals

- Buffering or replaying past voice frames to late joiners (the catch-up is "tune in from now", not "rewind to start").
- SDS broadcast to unknown destinations (federation peer registry doesn't list this ISSI → drop with log).
- Federated delivery reports (status frames). In scope: `FrameTypeSDSTransfer` only. Reports can follow the same plumbing later; flagged in "Out of scope".
- Catch-up for calls in `destinationType=subscriber` (private SDS calls). Catch-up only applies to group calls.

## Background — what exists today

**`s.calls`** ([service.go:401-408](../../../internal/service/service.go#L401-L408)) tracks every active call known to this server, including federation-relayed ones (federation_bridge.OnPeerCallStart writes the same map). It carries `{ID, SourceISSI, DestinationGSI, Priority, Service, DestinationType, OriginClientID}` — everything `brew.BuildGroupTX` needs.

**`brew.Client.ApplySubscriber`** ([client.go:60-80](../../../internal/brew/client.go#L60-L80)) applies subscriber register / affiliate / deaffiliate messages. The service layer receives the same `*SubscriberMessage` in [service.go:284](../../../internal/service/service.go#L284) and emits federation notifications — but doesn't react to the delta otherwise.

**`Hub.FindPeerForISSI`** ([hub.go](../../../internal/federation/hub.go)) returns the `*Peer` that has a given ISSI registered, or nil. `federation_bridge` doesn't expose this to the service layer yet.

**`Hub.BroadcastSDS`** ([hub.go](../../../internal/federation/hub.go)) exists and builds the `Control_SdsRelay` envelope, but **has zero callers** today. `federation_bridge.OnPeerSDSRelay` handles the receive side (peer → local subscriber); the send side (local subscriber → peer) is never wired up.

**`resolveSDSDestinationType`** ([service.go:869-900](../../../internal/service/service.go#L869-L900)) classifies an SDS destination by walking the local client snapshot. It returns `subscriber` as the default when the ISSI is unknown — which falls through to `BroadcastToSubscriber` with zero recipients.

## Feature 1 — late-join catch-up

### Trigger

A `*brew.SubscriberMessage` arriving on the brew handler. Specifically the `SubscriberRegister`, `SubscriberReregister`, and `SubscriberAffiliate` message types — these are the operations that **add** a GSSI to a subscriber.

### Detection

The service layer keeps a per-client memory of attached groups so it can compute the delta when the next message arrives:

```go
// Service struct addition
clientGroupsMu sync.Mutex
clientGroups   map[string]map[uint32]struct{} // clientID -> set of GSSIs
```

On every `*brew.SubscriberMessage`:

1. Snapshot the client's *post-apply* groups via `client.AttachedGroups()`.
2. Compare against the cached set for that client.
3. The difference `new \ old` is the set of GSSIs the client just attached to.
4. Replace the cached set with the new one.
5. On `OnDisconnect`, drop the cached entry for that clientID.

### Action

For each newly attached GSSI `g`:

1. Snapshot `s.calls` for entries where `DestinationGSI == g` and `DestinationType == "group"` (or empty — treated as group by default).
2. For each such call, build `brew.BuildGroupTX(call.ID, call.SourceISSI, call.DestinationGSI, call.Priority, call.Service)`.
3. Send the wire bytes to **just this client** via `server.SendToClient(client.ID, wire)`.
4. Log: `federation: catch-up call=<uuid> gssi=<g> sent to session=<clientID> source=<source>`.

### Edge cases

- **Same client is the call originator.** Skip when `call.OriginClientID == client.ID`. Their radio is already in the call.
- **Call ends between snapshot and send.** Harmless — the radio will receive a stale GROUP_TX, briefly attach, then receive the GROUP_IDLE (which is broadcast to the group, so this client gets it too). The same race exists on any joiner; the radio handles it.
- **Multiple active calls on the same GSSI** (e.g. local + federation-relayed during a brief overlap). Send a catch-up GROUP_TX for each. The radio picks the higher-priority one or queues; standard TETRA behavior.
- **Subscriber re-registering with the same groups.** Empty delta → no catch-up. The cache compare prevents redundant sends.
- **Client never sent a subscriber message before** (first connect). `oldGroups` is empty, so every group in the first message looks "new" — that's correct, we want catch-up for first-attach.

### What this fix does **not** do

It doesn't reorder webradio's call cycle. The 30 s window is unchanged; the catch-up just makes the user hear audio immediately instead of waiting for the next cycle boundary.

## Feature 2 — federated SDS delivery

### Trigger

The existing SDS send path in [service.go::onCallControlFromClient](../../../internal/service/service.go) handles `FrameTypeSDSTransfer` frames. The new logic only fires when local delivery yields zero recipients.

### Detection

After the existing call to `s.broadcastByDestinationType(destinationType, dest, wire, client.ID)` ([service.go:582](../../../internal/service/service.go#L582)):

1. If `recipients > 0` → local delivery succeeded → existing behavior, return.
2. If `recipients == 0` AND `destinationType == subscriber` AND `dest != 0`:
   - Ask the federation layer: does a peer have this ISSI?
   - If yes → federate.
   - If no → log `service: SDS dropped — destination ISSI %d not local and not known to any peer` and return.

### New API in `federation_bridge`

Expose two methods:

```go
// HasRemoteSubscriber reports whether any federation peer has the given ISSI
// in its subscriber registry. Used by the SDS router to decide whether to
// federate or drop.
func (fb *federationBridge) HasRemoteSubscriber(issi uint32) bool

// NotifySDSToPeer broadcasts an SDS payload through the federation mesh.
// Wraps hub.BroadcastSDS so service.go doesn't depend on the hub directly.
func (fb *federationBridge) NotifySDSToPeer(source, dest uint32, payloadHex string)
```

Both delegate to existing `hub.FindPeerForISSI` and `hub.BroadcastSDS` respectively.

### Wire path

```
local client (ISSI A) ── frame ──→ service.go
                                         │
                                         │ broadcastByDestinationType
                                         │ returns 0 recipients
                                         ▼
                                  federation_bridge.HasRemoteSubscriber(dest)
                                         │ yes
                                         ▼
                                  federation_bridge.NotifySDSToPeer(A, dest, hex)
                                         │
                                         ▼
                                  hub.BroadcastSDS
                                         │
                                         ▼
                                  Control{SdsRelay{src, dest, bytes}} via mesh
                                         │
                                         ▼
                            remote hub.handleSdsRelay
                                         │
                                         ▼
                            federation_bridge.OnPeerSDSRelay (already exists)
                                         │
                                         ▼
                            BroadcastToSubscriber(dest) on remote server
                                         │
                                         ▼
                                remote brew client (ISSI dest)
```

The receive side is unchanged — `OnPeerSDSRelay` already reconstructs both the `ShortTransfer` control prefix and the `SDSTransfer` frame.

### Edge cases

- **SDS to an ISSI that's both local and remote.** Local delivery has already returned ≥1 recipient, federation step is skipped. No duplicate.
- **SDS to a group GSSI.** `destinationType == group`, federation step is skipped — group SDS today follows the existing group-call relay paths (`MsgSubscriberUpdate` carries affiliation), not the per-ISSI SDS routing. Out of scope.
- **Peer holding the destination disconnects before delivery.** The hub broadcasts to all peers; whoever currently has the ISSI handles it. If nobody has it any more by the time it arrives, the receive side's `BroadcastToSubscriber` returns 0 — message effectively dropped. Same risk as any TETRA SDS; acceptable.
- **Hex encoding.** `hub.BroadcastSDS` takes hex string for legacy reasons (the proto uses `bytes` but the codec already round-trips through hex). Keep using hex on the public bridge API; convert via `hex.EncodeToString(payload)` at the call site.

## Implementation tasks

Each task is one commit on `feat/federation-protobuf-refactor`.

### Task A1 — Per-client group cache + delta computation

**Files:** `internal/service/service.go`

1. Add `clientGroupsMu sync.Mutex` + `clientGroups map[string]map[uint32]struct{}` to `Service`.
2. Initialize the map in `New(...)`.
3. In `OnDisconnect`, `delete(s.clientGroups, client.ID)` under the lock.
4. Add a helper `(s *Service) diffAndUpdateClientGroups(clientID string, current []uint32) (added []uint32)` that:
   - Builds a set from `current`.
   - Compares with the cached set; returns only the *added* GSSIs.
   - Replaces the cache with the new set.
5. Build + vet + run existing tests.
6. Commit: `service: track per-client group affiliation diffs`

### Task A2 — Catch-up GROUP_TX for new group attaches

**Files:** `internal/service/service.go`

1. In the `*brew.SubscriberMessage` branch ([service.go:284](../../../internal/service/service.go#L284)), after the existing `s.federation.NotifySubscriberUpdate(...)` call, invoke a new helper `s.catchUpClientForNewGroups(client, added)` where `added` comes from `diffAndUpdateClientGroups`.
2. Implementation:
   ```go
   func (s *Service) catchUpClientForNewGroups(client *brew.Client, added []uint32) {
       if len(added) == 0 { return }
       s.callMu.RLock()
       defer s.callMu.RUnlock()
       for _, gssi := range added {
           for _, call := range s.calls {
               if call.DestinationGSI != gssi { continue }
               if call.DestinationType != "" && call.DestinationType != destinationTypeGroup { continue }
               if call.OriginClientID == client.ID { continue }
               wire := brew.BuildGroupTX(call.ID, call.SourceISSI, call.DestinationGSI, call.Priority, call.Service)
               if s.server.SendToClient(client.ID, wire) {
                   s.logger.Printf("federation: catch-up call=%s gssi=%d to session=%s source=%d", call.ID, gssi, client.ID, call.SourceISSI)
               }
           }
       }
   }
   ```
3. Verify `activeCall` struct has `Priority` + `Service` fields. If not (current struct only carries `SourceISSI` + `DestinationGSI` + `DestinationType` + `OriginClientID`), add them — but only set on the call origination path; default to 0 for federation-relayed calls. Inspect first.
4. Build + vet.
5. Commit: `service: send synthetic GROUP_TX to late group joiners`

### Task A3 — Quick integration test

**Files:** new `internal/service/late_join_test.go`

1. Spin up a brew server + service in-process with two fake clients.
2. Client 1 starts a group call on GSSI 13.
3. Client 2 sends a SubscriberMessage affiliating ISSI 100 to GSSI 13.
4. Assert: client 2 received a `GROUP_TX` frame with matching call UUID.
5. Commit: `service: test late-join catch-up`

### Task B1 — Expose peer-subscriber lookup + SDS send via federation_bridge

**Files:** `internal/service/federation_bridge.go`

1. Add `func (fb *federationBridge) HasRemoteSubscriber(issi uint32) bool` — calls `fb.hub.FindPeerForISSI(issi) != nil`.
2. Add `func (fb *federationBridge) NotifySDSToPeer(source, dest uint32, payloadHex string)` — calls `fb.hub.BroadcastSDS(source, dest, payloadHex)`.
3. Build + vet.
4. Commit: `federation_bridge: expose peer-subscriber lookup + SDS send`

### Task B2 — Route SDS to peers when local has no destination

**Files:** `internal/service/service.go`

1. In the SDS frame handler (around [service.go:582](../../../internal/service/service.go#L582)), after `recipients := s.broadcastByDestinationType(...)`:
   ```go
   if recipients == 0 && destinationType == destinationTypeSubscriber && dest != 0 && m.FrameType == brew.FrameTypeSDSTransfer && s.federation != nil {
       if s.federation.HasRemoteSubscriber(dest) {
           s.federation.NotifySDSToPeer(source, dest, hex.EncodeToString(payload))
           s.logger.Printf("federation: SDS routed to peer for dest=%d source=%d", dest, source)
       } else {
           s.logger.Printf("service: SDS dropped — dest %d not local and not known to any peer", dest)
       }
   }
   ```
2. Add `"encoding/hex"` import if not already present.
3. Build + vet.
4. Commit: `service: federate SDS to peer-known subscribers when local delivery yields zero`

### Task B3 — Integration test for SDS federation

**Files:** new `internal/service/federated_sds_test.go`

1. Two services with federation bridges connected by a mock hub.
2. Client on service A sends an SDS to ISSI 200 (which is registered on service B via peer registry).
3. Assert: service B's brew client received the SDS frame.
4. Commit: `service: test federated SDS delivery`

## Verification

- `go test ./...` passes including the two new tests.
- Live federation test: with main + loopback federated, send an SDS from a radio on main to an ISSI registered on the loopback side; expect the loopback echo / webradio operator (or a test client) to receive it.
- Late-join verification: join TG 13 on a radio while the loopback webradio is mid-cycle; audio should start within ~100 ms of the affiliation, not at the next 30 s boundary.

## Risks

- **Race on `s.calls`** during catch-up: we hold `callMu.RLock` while sending. `SendToClient` writes to a buffered channel (non-blocking with select-default), so the lock is held very briefly. Acceptable.
- **Memory: per-client group sets.** O(clients × groups). Cleaned in `OnDisconnect`. Bounded by max concurrent radios; negligible.
- **SDS double-delivery if peer state is stale.** If a peer reports an ISSI it no longer has (peer crashed between dereg and our send), the federate-to-peer call lands at the remote and `BroadcastToSubscriber` finds zero local clients → effectively dropped. No double delivery.
- **catch-up GROUP_TX for a federation-relayed call doesn't have the original `Priority`/`Service`** unless task A2 widens `activeCall`. If those fields are dropped the radio receives priority=0 and service=0 — TETRA radios accept defaults. Acceptable on the first iteration; can be tightened later.

## Out of scope (next iteration)

- SDS *delivery report* (status frame) federation. The wire format already supports `FrameTypeSDSReport`; the `BroadcastSDS` interface today only carries the raw payload. Adding a `frame_type` field to the proto or a separate `Control_SdsReport` message would do it.
- Catch-up for subscriber-to-subscriber (private circuit) calls.
- "Catch-up tail" — sending the last N seconds of buffered voice frames so the late joiner hears the recent past, not just from now. Requires a per-call ring buffer.
