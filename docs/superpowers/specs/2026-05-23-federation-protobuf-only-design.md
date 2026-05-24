# Federation: protobuf-only internal types

Date: 2026-05-23
Status: design — pending implementation plan
Branch: `feat/federation-protobuf-only`

## Goal

Eliminate the JSON-shaped intermediate types inside `internal/federation/` so the federation code operates directly on the generated protobuf types it already sends and receives over the wire. The on-the-wire format (`StreamFrame { oneof Control | VoiceFrame }` over gRPC bidi) is unchanged. Brew (radio client) WebSocket and binary protocols in `internal/brew/`, plus the HTTP API and web UI, are explicitly untouched.

## Motivation

Today the federation hub performs a proto → Go struct → JSON bytes → Go struct round-trip on every inbound control message ([hub.go:399-402](../../../internal/federation/hub.go#L399-L402)). Outbound it builds a Go struct (`Message`) and translates it to protobuf in [v2_codec.go](../../../internal/federation/v2_codec.go) before send. The translation layer is ~220 lines of mechanical field-copy and silently drops some fields (e.g. `Message.Capabilities` is never written to the proto Hello). The wire was migrated to typed protobuf in the v2 transport; the in-process types are the only remaining JSON-era artifact.

Removing them produces:

- A single source of truth for federation message structure (the `.proto` file).
- ~500 lines removed.
- One Hello-field bug fixed by elimination (`Capabilities` was dead code anyway — see "Dead code removed" below).
- A clearer story for the next phase (whatever extensions to the protocol come next).

## Scope

In scope:

- `internal/federation/` Go code refactor.
- Deletion of `internal/federation/udp_voice.go` and all UDP-voice integration in the hub and config — UDP plane is being removed per the scoping decision in the brainstorming session.
- Deletion of unreferenced v1 envelope proto (`internal/federation/proto/federation.proto` + generated `*.pb.go`).
- Removal of dead capability-negotiation code (`Peer.SupportsCapability` and the `Capability*` constants have zero behavioral effect).
- Tiny corresponding cleanup in [internal/service/federation_bridge.go](../../../internal/service/federation_bridge.go) and [internal/config/config.go](../../../internal/config/config.go) for UDP fields.

Explicitly out of scope:

- Brew client/server protocol (`internal/brew/`), HTTP handlers, web UI assets.
- Any change to the wire format or gRPC service definition (`federation_v2_draft.proto` stays as-is).
- The federation auth model (`x-brew-peer` / `x-brew-key` / `x-brew-subpath` metadata).
- The reconnect/backoff logic added in the prior session.

## Files removed

| Path | Reason |
|---|---|
| `internal/federation/protocol.go` | Defines `Message` (the JSON-tagged intermediate) plus `Msg*` and `Capability*` string constants. Replaced by direct use of the proto types. `ProtocolVersion` and `MaxTTL` move to `hub.go` / `mesh.go` respectively. |
| `internal/federation/v2_codec.go` | 218 lines of `Message ↔ Control` translation — unreachable once consumers operate on `*federationv2pb.Control` directly. |
| `internal/federation/udp_voice.go` | UDP voice plane removed per scoping decision. |
| `internal/federation/proto/federation.proto` | v1 `Envelope { Kind, payload bytes }` proto. `grep federationpb` finds zero importers. |
| `internal/federation/proto/federation.pb.go` | Generated from the v1 proto above. |
| `internal/federation/proto/federation_grpc.pb.go` | Generated from the v1 proto above. |

## Files modified

### `internal/federation/hub.go`

The largest source of change. Specific edits:

1. **Direct proto dispatch in `handleControlMessage`.** Replace the proto → JSON → struct round-trip ([hub.go:399-402](../../../internal/federation/hub.go#L399-L402)) with a `switch payload := ctrl.GetPayload().(type)` on the proto `oneof`. Delete `handleJSONMessage`.
2. **Per-payload handler functions.** Extract each `case` arm into a small named method (`handleHello`, `handleSubscriberUpdate`, `handleCallStart`, etc.) to keep `handleControlMessage` under the 100-line / complexity-8 limit set by CLAUDE.md.
3. **Direct proto construction in `Broadcast*`.** Every `Broadcast*` method currently builds `&Message{Type: MsgX, ...}` and calls `h.broadcastToAllPeers(msg)`. Rewrite to construct `&federationv2pb.Control{Origin: ..., Payload: &federationv2pb.Control_X{X: &federationv2pb.X{...}}}` directly.
4. **Internal helpers switch type.** `sendUsersDBOffer`, `handleUsersDBOffer`, `sendPeerExchange`, `sendFullSync`, `relayToPeers`, `broadcastToAllPeers`, `buildHello` all take `*federationv2pb.Control` (or build one and pass it on) instead of `*Message`.
5. **Mesh fields populate the Control wrapper.** `msg_id`, `ttl`, `path`, `origin` are top-level fields on `Control` — they're set on the wrapper, not nested in the payload message.
6. **Delete UDP integration.** Remove `Hub.udpVoice` field, `udpInTokens`, `udpInTokenMu`, `EnableUDPVoice` method, the UDP-handshake branch in `handleHello` ([hub.go:481-497](../../../internal/federation/hub.go#L481-L497)), the UDP cleanup in `unregisterPeer` and `renamePeer`. `BroadcastVoiceFrame` collapses to a single `SendVoiceFrame` loop (TCP gRPC path only).
7. **Delete legacy binary fast-path.** Remove `handleBinaryMessage` ([hub.go:645-652](../../../internal/federation/hub.go#L645-L652)) and the `Peer.SendBinary` call sites. `readLoop` already dispatches `frame.GetVoiceFrame()`; that's the only voice receive path post-refactor.
8. **Move constants.** `ProtocolVersion` → near `Hub` declaration in `hub.go`. `MaxTTL` → `mesh.go`.

### `internal/federation/peer.go`

1. **Rename `SendJSON(msg *Message)` → `SendControl(ctrl *federationv2pb.Control)`.** Body drops the codec call and wraps directly in `&federationv2pb.StreamFrame{Body: &federationv2pb.StreamFrame_Control{Control: ctrl}}`.
2. **Delete `SendBinary`.** Legacy 36-byte UUID prefix path with no remaining callers.
3. **Delete capability tracking.** Remove `capabilities []string` field, `SetCapabilities`, `Capabilities`, `SupportsCapability`. `SupportsCapability` has zero call sites; `SetCapabilities` stored data that never crossed the wire (proto Hello has no capabilities field).

### `internal/federation/mesh.go`

1. **Signature changes.** `ShouldProcess`, `ShouldRelay`, `PrepareRelay`, `PrepareOutgoing`, `IsInPath` take `*federationv2pb.Control` instead of `*Message`. The fields they read (`MsgId`, `Ttl`, `Path`, `Origin`) are identical on `Control`.
2. **`MaxTTL` constant** moves here from `protocol.go`.

### `internal/service/federation_bridge.go`

1. **Delete the dead UDP-disabled warning** ([federation_bridge.go:47-49](../../../internal/service/federation_bridge.go#L47-L49)). The block logs "UDP voice disabled by configuration" whenever the user *configured* UDP — backwards messaging, and irrelevant after UDP removal.
2. No signature changes to `hub.Broadcast*` calls — those methods keep taking `(issi, action, gssis)` primitives; the proto-building moves inside the hub.

### `internal/config/config.go`

1. **Remove `FederationConfig.UDPPort` and `UDPAdvAddr`** fields.
2. **Remove `FEDERATION_UDP_PORT` and `FEDERATION_UDP_ADV_ADDR`** env reads from `LoadFromEnv`.

### `.env.example`

1. Drop any documented `FEDERATION_UDP_*` lines.

## Dead code removed (rationale)

- `Peer.SupportsCapability` — `grep -r SupportsCapability` returns zero call sites outside the function definition itself.
- `Peer.SetCapabilities` + `peer.capabilities` — written from `handleHello` with values from `Message.Capabilities`, which is *never populated* on the receive side because `controlToMessage` doesn't copy it (the proto Hello has no `capabilities` field). The slice is always empty in practice.
- `Capability*` string constants in `protocol.go` — only used in `buildHello` to fill the field that never reaches the wire.
- v1 `Envelope` proto and its generated Go — last touched in the v1 → v2 migration; no current importer.
- `Peer.SendBinary` — superseded by `SendVoiceFrame`; the only caller was `handleBinaryMessage`, which itself is unreachable from the v2 read loop (which dispatches on the typed `oneof`, not raw frames).

## Wire compatibility

Unchanged. The protobuf bytes serialized to the wire for `StreamFrame { Control | VoiceFrame }` are identical before and after this refactor. A pre-refactor build can connect to a post-refactor build and exchange the full message set without renegotiation, because:

- The `.proto` files defining the wire (`federation_v2_draft.proto`) are not modified.
- The generated `*.pb.go` is regenerated only as part of removing the *unused v1* proto.
- The fields that disappear (`Message.Capabilities`, the UDP-handshake fields in Hello) were either never on the wire or were on the wire but are now ignored on the receive side — a forward-compatible behavior for protobuf.

The UDP voice plane is a separate concern: pre-refactor nodes that had UDP enabled and were talking to other UDP-enabled nodes will, post-refactor, fall back to the TCP gRPC voice path. That fallback is already the default in this codebase (see [federation_bridge.go:47-49](../../../internal/service/federation_bridge.go#L47-L49) which currently disables UDP regardless of config).

## Build sequence

One PR, ordered so every commit compiles and tests pass:

1. **Move constants only.** `ProtocolVersion` → `hub.go`. `MaxTTL` → `mesh.go`. No behavioral change; `protocol.go` keeps `Message` + the JSON tags + `Msg*`/`Capability*` strings.
2. **Add `Peer.SendControl` alongside `SendJSON`.** New entry point that takes `*federationv2pb.Control` and skips the codec. Both methods coexist temporarily.
3. **Migrate outbound to direct proto.** Rewrite every `Hub.Broadcast*` method, `buildHello`, `sendPeerExchange`, `sendUsersDBOffer`, `sendFullSync`, `relayToPeers`, `broadcastToAllPeers` to construct `*federationv2pb.Control` directly and call `SendControl`. After this commit, `messageToControl` and `SendJSON` are unreferenced.
4. **Migrate inbound to direct proto.** Rewrite `handleControlMessage` to switch on `ctrl.GetPayload()` proto types via per-payload methods (`handleHello`, `handleSubscriberUpdate`, …) sized to fit the 100-line / complexity-8 limit. Delete `handleJSONMessage`. After this commit, `controlToMessage` is unreferenced.
5. **Delete the codec layer.** Remove `v2_codec.go`. Remove `Peer.SendJSON`. Remove `handleBinaryMessage` and `Peer.SendBinary` (already unreachable from the v2 read loop).
6. **Switch `mesh.go` signatures.** `ShouldProcess`, `ShouldRelay`, `PrepareRelay`, `PrepareOutgoing`, `IsInPath` take `*federationv2pb.Control` instead of `*Message`. All call sites in `hub.go` are already passing `*Control` after steps 3–4. Trivial mechanical edit; field reads change from `msg.MsgID` to `ctrl.GetMsgId()` etc.
7. **Delete UDP plane.** Remove `udp_voice.go`, the `Hub.udpVoice*` fields, `EnableUDPVoice`, the UDP-handshake branch in the hello handler, the WS-vs-UDP split in `BroadcastVoiceFrame`, the dead bridge warning, the `FederationConfig.UDP*` fields, the `FEDERATION_UDP_*` env reads, and any `.env.example` lines.
8. **Delete the JSON-era leftovers.** Remove `protocol.go` (`Message`, `Msg*`, `Capability*`, `SyncSubscriber`, `GossipPeer` Go types). Remove the v1 `Envelope` proto + generated `*.pb.go`. Remove the capability fields and methods (`capabilities`, `SetCapabilities`, `Capabilities`, `SupportsCapability`) on `Peer`.

Verification runs are documented in the next section and execute after step 8.

## Verification

- **Build clean.** `go build ./...` and `go vet ./...` pass at every commit in the build sequence.
- **Existing federation tests.** `./tests/federation-loopback/run.sh` (3-node gossip discovery ft-a → ft-b → ft-c) and `./tests/federation-freecat/run.sh` (loopback peer + TG25 echo) must pass without modification. These exercise: handshake, subscriber sync, peer-exchange gossip, mesh relay, call routing, voice frame propagation through the TCP voice path.
- **Smoke test against a pre-refactor peer.** Start one node on `master` (pre-refactor) and one on `feat/federation-protobuf-only` (post-refactor); confirm they federate cleanly. This is the wire-compatibility check.
- **No regressions in `go test ./...`.** Existing unit tests stay green; this refactor does not introduce new tests because behavior is unchanged.

## Risks

- **`SyncSubscriber.Callsign` ambiguity.** The proto carries it and the Go `SyncSubscriber` did too. Today neither sender nor receiver populates `Callsign`. Post-refactor, the field is still present on the proto type and still unused — same behavior, no regression, no fix promised here.
- **Hidden caller of `Peer.SendBinary` or `SupportsCapability`.** Mitigation: `grep -rn` across the entire tree before deletion; CI compile failure if any other package depends on these.
- **External tooling consuming the v1 `Envelope` proto.** The `proto/federation.proto` file was published in the repo and may be used by third-party clients. We are deleting it. If an external client still reads it, that breaks. Mitigation: a `README` note in `internal/federation/proto/` pointing to v2 as the supported version.

## Out of scope, but flagged

- The `BREW_MODE=dmrbridge`-related routing in the hub (`isFederatedGSSI`, TG ranges) is unaffected.
- The reconnect/backoff change from the prior session ([hub.go:231-326](../../../internal/federation/hub.go#L231-L326)) is unrelated and stays as-is on this branch.
- If the codec round-trip turns out to be load-bearing somewhere we didn't find (e.g. logging via JSON-marshal of `Message` for human-readable peer trace), it will surface as a compile failure in step 6 and we'll handle it then.
