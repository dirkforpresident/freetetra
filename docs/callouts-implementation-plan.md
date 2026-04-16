# Callouts Implementation Plan

## Goals

1. Move callout lifecycle logic into a dedicated service-layer state machine.
2. Remove callout-number control from the Web UI.
3. Make responder state keyed by the actual sender ISSI.
4. Define and adopt a callout PDU where `call_out_number` is an **8-bit counter**.

---

## Current Structure (As-Is)

### Data and transport path

- Inbound SDS/callout frames are handled in `Service.onVoiceFrameFromClient()`:
  - [`internal/service/service.go`](../internal/service/service.go)
- Payload wrapping/unwrapping and callout encode/decode:
  - [`internal/service/sds_codec.go`](../internal/service/sds_codec.go)
- Callout state aggregation and snapshot model:
  - [`internal/service/callout_state.go`](../internal/service/callout_state.go)
- Managed response relay (text SDS -> callout response):
  - [`internal/service/callout_managed.go`](../internal/service/callout_managed.go)
- UI/API surface for callout/SDS:
  - [`internal/service/dashboard_ui.go`](../internal/service/dashboard_ui.go)

### Current technical behavior

1. Callout state keys are string-based (`destination_type:destination:callout_number`).
2. Subscriber replies are stored with a special subscriber key format but still matched heuristically.
3. Callout number is modeled as 4-bit (`0..15`) in the current PDU parser/builder.
4. UI still exposes and manipulates callout-number flow behavior.

### Current PDU (legacy, implemented now)

`sdsProtocolCallout = 0xC3`, current layout in `buildCalloutPayload`/`parseCalloutPayload`:

1. Octet 0: Protocol ID (`0xC3`)
2. Octet 1: message type / delivery report / storage
3. Octet 2: SDS message ref (8-bit)
4. Octet 3: extension flag + text coding scheme
5. Octet 4: function (4 bits) + callout number (4 bits)
6. Octet 5: group control / severity / timestamp / user receipt
7. Octet 6: text/status + end + ptt
8. Octet 7..N: text

---

## Target Service-Layer State Machine

### New core component

Create `CalloutManager` (service layer) with explicit thread identity:

- `thread_id` (UUID, internal primary key)
- `destination_type`, `destination`
- `source_issi`
- `wire.call_out_number` (8-bit counter)
- `state`
- `created_at`, `updated_at`, `last_activity_at`

### Thread states

1. `draft`
2. `alerted`
3. `active`
4. `clearing`
5. `cleared`
6. `expired`

### State transitions

1. `draft -> alerted` on `StartAlert`
2. `alerted -> active` on `ReceiveResponse` or `SendFollowup`
3. `active -> active` on additional response/followup
4. `active|alerted -> clearing` on `SendClear`
5. `clearing -> cleared` on clear send completion/ack policy
6. any non-final -> `expired` on timeout policy

### Responder tracking model (per thread)

`responders[issi] = {`

- `last_message_type`
- `last_state`
- `last_text`
- `last_seen`
- `response_count`

`}`

This becomes the single source for "callout response states reflect sender ISSI".

---

## PDU Protocol Definition (Target)

## Callout PDU v2 (`0xC3`) with 8-bit call-out number

`call_out_number` is an 8-bit monotonic counter per source scope (wrap at `255 -> 0`).

### Binary layout (v2 canonical)

1. Octet 0: `protocol_id = 0xC3`
2. Octet 1: `msg_type[7:4] | delivery_report[3:2] | storage[0]`
3. Octet 2: `sds_msg_ref` (8-bit)
4. Octet 3: `extension_header[7]=1 | text_coding_scheme[6:0]`
5. Half-octet (4-bit): `function`
6. Octet 5: `call_out_number_8` (**8-bit**)
7. Half-octet (4-bit): `severity`
8. Octet 6: `group_control[7:6] | timestamp[5] | user_receipt[4] | severity[3:0]`
9. Octet 7: `text_is_status[7] | end_callout[6] | ptt_not_allowed[5] | reserved[4:0]`
10. Octet 8..N: text payload

### Compatibility decode rules

1. If octet 3 `extension_header == 1` and payload length >= 8, parse as v2.
2. Else parse as legacy v1 (4-bit callout number in octet 4 low nibble).
3. Expose a normalized `CalloutNumber uint8` in internal model for both formats.

### Compatibility encode rules

1. New outbound callouts use v2 by default.
2. Optional config flag can force legacy v1 for interoperability tests.

---

## API and UI Changes

### New callout APIs (preferred path)

1. `POST /api/callouts/start`
2. `POST /api/callouts/{thread_id}/message`
3. `POST /api/callouts/{thread_id}/clear`
4. `GET /api/callouts`
5. `GET /api/callouts/{thread_id}`

### UI behavior

1. Remove editable callout-number control from UI flow.
2. UI chooses thread action only; backend assigns/keeps `call_out_number`.
3. Show assigned callout reference as read-only.
4. Response table sourced from `CalloutManager.responders` keyed by sender ISSI.

---

## Implementation Phases

### Phase 1: Add manager and models

1. Add `internal/service/callout_manager.go`.
2. Add thread/responder structs and transition methods.
3. Add unit tests for transitions and responder attribution.

### Phase 2: Wire manager into runtime

1. Route callout TX/RX events in `service.go` through manager.
2. Keep existing snapshot fields, but source from manager internals.
3. Add migration shim so old state map can be removed later.

### Phase 3: PDU v2 (8-bit callout number)

1. Update `buildCalloutPayload` to emit v2 by default.
2. Update `parseCalloutPayload` to parse both v1 and v2.
3. Add codec tests for v1/v2 compatibility and counter wrapping.

### Phase 4: API + UI cleanup

1. Add callout thread APIs.
2. Update UI flow to thread actions only (no callout-number input).
3. Keep `/api/sds/send` for raw/manual SDS debugging only.

### Phase 5: Remove legacy state paths

1. Delete direct `calloutStates` mutation paths after parity checks.
2. Keep a temporary compatibility adapter if required for external tools.

---

## Test Matrix (Minimum)

1. Start alert allocates 8-bit call-out number server-side.
2. Followup/clear reuse the same thread number.
3. Two different responder ISSIs produce two distinct responder states.
4. Cross-group/virtual routing still maps responder ISSI correctly.
5. v1 decode works for old payloads.
6. v2 encode/decode preserves all flags and 8-bit counter.
7. Counter wraps cleanly at 255.

---

## Open Design Decisions

1. Counter scope: global, per source ISSI, or per source+destination.
2. Clear completion semantics: immediate clear vs ack-confirmed clear.
3. Timeout defaults for `active` and `clearing`.
4. Whether managed relay should always emit callout `msg_type=2` or preserve intent by state.
