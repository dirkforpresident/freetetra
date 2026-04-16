package service

import (
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/freetetra/server/internal/brew"
)

func (s *Service) maybeRelayManagedCalloutResponse(
	client *brew.Client,
	callID uuid.UUID,
	sourceHint uint32,
	destinationHint uint32,
	payload []byte,
) bool {
	raw, _ := unwrapSDSPayload(payload)
	if len(raw) < 4 {
		return false
	}
	if raw[0] == sdsProtocolCallout {
		return false
	}
	if !isManagedCalloutResponseTextProtocol(raw[0]) {
		return false
	}

	text := strings.TrimSpace(decodeSDSText(raw))
	if text == "" {
		return false
	}

	source, attachedGroups, ok := resolveSourceAndGroups(client.Snapshot(), sourceHint)
	if !ok || source == 0 || len(attachedGroups) == 0 {
		return false
	}
	targetGroups := chooseManagedCalloutTargetGroups(attachedGroups, destinationHint)
	if len(targetGroups) == 0 {
		return false
	}

	relayed := false
	for _, gssi := range targetGroups {
		ctx, ok := s.findManagedCalloutContext(source, gssi)
		if !ok {
			continue
		}
		calloutPayload := buildCalloutPayload([]byte(text), managedCalloutResponseOptions(ctx))
		parsed, ok := parseCalloutPayload(calloutPayload)
		if !ok {
			continue
		}

		control := brew.BuildShortData(callID, brew.ShortDataPayload{
			Source:      source,
			Destination: gssi,
			Number:      "CALL OUT",
		})
		frame := brew.BuildSDSFrame(callID, uint16(len(calloutPayload)*8), buildSDSFramePayload(source, gssi, calloutPayload))
		release := brew.BuildCallRelease(callID, 0)

		nControl := s.server.BroadcastToGroup(gssi, control, client.ID)
		nFrame := s.server.BroadcastToGroup(gssi, frame, client.ID)
		nRelease := s.server.BroadcastToGroup(gssi, release, client.ID)
		_ = s.noteCalloutRx(client.ID, sdsFrameEnvelope{
			Source:      source,
			Destination: gssi,
			Payload:     calloutPayload,
			Wrapped:     true,
		}, parsed)

		s.recordActivity(
			"callout-managed-relay",
			"managed callout response relayed",
			map[string]any{
				"session":            client.ID,
				"call_id":            callID.String(),
				"source":             source,
				"group":              gssi,
				"text":               text,
				"callout_number":     parsed.CalloutNumber,
				"recipients_control": nControl,
				"recipients_frame":   nFrame,
				"recipients_release": nRelease,
			},
		)
		s.logger.Printf(
			"session=%s managed callout relay call=%s source=%d group=%d callout_number=%d recipients_control=%d recipients_frame=%d recipients_release=%d",
			client.ID,
			callID.String(),
			source,
			gssi,
			parsed.CalloutNumber,
			nControl,
			nFrame,
			nRelease,
		)
		if nControl+nFrame+nRelease > 0 {
			relayed = true
		}
	}
	return relayed
}

func isManagedCalloutResponseTextProtocol(protocolID byte) bool {
	switch protocolID {
	case sdsProtocolTextMessagingTL, sdsProtocolImmediateTextTL, sdsProtocolHomeModeDisplay:
		return true
	default:
		return false
	}
}

func resolveSourceAndGroups(snapshot brew.ClientSnapshot, sourceHint uint32) (uint32, []uint32, bool) {
	if len(snapshot.Subscribers) == 0 {
		return 0, nil, false
	}

	if sourceHint != 0 {
		for _, sub := range snapshot.Subscribers {
			if sub.Number != sourceHint {
				continue
			}
			groups := uniqueSortedGroups(sub.Groups)
			if len(groups) == 0 {
				groups = uniqueSortedGroups(snapshot.Groups)
			}
			return sourceHint, groups, len(groups) > 0
		}
	}

	source := snapshot.Subscribers[0].Number
	groups := uniqueSortedGroups(snapshot.Subscribers[0].Groups)
	if len(groups) == 0 {
		groups = uniqueSortedGroups(snapshot.Groups)
	}
	return source, groups, len(groups) > 0
}

func uniqueSortedGroups(in []uint32) []uint32 {
	if len(in) == 0 {
		return nil
	}
	m := make(map[uint32]struct{}, len(in))
	for _, g := range in {
		if g == 0 {
			continue
		}
		m[g] = struct{}{}
	}
	if len(m) == 0 {
		return nil
	}
	out := make([]uint32, 0, len(m))
	for g := range m {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func chooseManagedCalloutTargetGroups(attachedGroups []uint32, destinationHint uint32) []uint32 {
	groups := uniqueSortedGroups(attachedGroups)
	if len(groups) == 0 {
		return nil
	}
	if destinationHint != 0 {
		for _, g := range groups {
			if g == destinationHint {
				return []uint32{g}
			}
		}
	}
	return groups
}

func managedCalloutResponseOptions(state dashboardCalloutState) calloutEncodeOptions {
	opts := defaultCalloutEncodeOptions()
	opts.MessageType = 2 // Response/ack style message
	opts.DeliveryReportRequest = 0
	opts.StorageAllowed = false
	opts.ExtensionHeader = false
	if state.TextIsStatus {
		opts.TextCodingScheme = uint8(ISO8859_1)
	}
	if state.Function > 0 {
		opts.Function = state.Function
	} else {
		opts.Function = 3
	}
	opts.CalloutNumber = state.CalloutNumber
	opts.Severity = state.Severity & 0x0f
	opts.GroupControl = state.GroupControl & 0x03
	opts.TimestampControl = false
	opts.UserReceiptControl = false
	opts.TextIsStatus = false
	opts.EndCallout = false
	opts.PTTNotAllowed = state.PTTNotAllowed
	return opts
}

func (s *Service) findManagedCalloutContext(source, group uint32) (dashboardCalloutState, bool) {
	if s.calloutMgr != nil {
		if ctx, ok := s.calloutMgr.latestGroupContext(source, group); ok {
			return ctx, true
		}
	}

	s.calloutMu.RLock()
	defer s.calloutMu.RUnlock()

	var (
		found bool
		best  dashboardCalloutState
	)
	for _, st := range s.calloutStates {
		if st == nil {
			continue
		}
		if st.DestinationType != destinationTypeGroup {
			continue
		}
		if st.Destination != group {
			continue
		}
		if st.State == "cleared" {
			continue
		}
		if source != 0 && st.Source != 0 && st.Source != source {
			continue
		}
		if !found || st.Updated.After(best.Updated) {
			best = *st
			found = true
		}
	}
	return best, found
}
