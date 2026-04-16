package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/freetetra/server/internal/brew"
	"github.com/freetetra/server/internal/config"
)

type Service struct {
	cfg    config.Config
	logger *log.Logger

	server *brew.Server

	callMu sync.RWMutex
	calls  map[uuid.UUID]*activeCall
	// virtualCircuitCalls tracks locally synthesized Brew circuit endpoints.
	virtualCircuitCalls map[uuid.UUID]*virtualCircuitCall

	activityMu   sync.RWMutex
	activitySeq  int64
	activityRing []dashboardActivity

	calloutMu     sync.RWMutex
	calloutStates map[string]*dashboardCalloutState
	calloutMgr    *calloutManager

	virtualSDSMu        sync.RWMutex
	virtualSDSEndpoints map[uint32]*virtualSDSEndpointState
	groupCalloutVirtual map[uint32]uint32
	virtualSDSSeq       int64

	motdMu    sync.Mutex
	motdStore *motdSeenStore

	sdsRouteHintMu sync.RWMutex
	sdsRouteHints  map[uint32]sdsRouteHint
}

type activeCall struct {
	ID              uuid.UUID
	SourceISSI      uint32
	DestinationGSI  uint32
	DestinationType string
	OriginClientID  string
}

type virtualCircuitCall struct {
	CallID      uuid.UUID
	SourceISSI  uint32
	VirtualISSI uint32
	Connected   bool
}

type sdsRouteHint struct {
	Destination uint32
	Updated     time.Time
}

const sdsRouteHintTTL = 45 * time.Second

func New(cfg config.Config, logger *log.Logger) (*Service, error) {
	s := &Service{
		cfg:                 cfg,
		logger:              logger,
		calls:               make(map[uuid.UUID]*activeCall),
		virtualCircuitCalls: make(map[uuid.UUID]*virtualCircuitCall),
		calloutStates:       make(map[string]*dashboardCalloutState),
		calloutMgr:          newCalloutManager(),
		virtualSDSEndpoints: make(map[uint32]*virtualSDSEndpointState),
		groupCalloutVirtual: make(map[uint32]uint32),
		sdsRouteHints:       make(map[uint32]sdsRouteHint),
	}
	if cfg.MOTD.Enabled {
		store, err := newMOTDSeenStore(cfg.MOTD.DBPath)
		if err != nil {
			return nil, fmt.Errorf("motd store init: %w", err)
		}
		s.motdStore = store
	}
	s.server = brew.NewServer(cfg, logger, s)
	s.registerDashboardHandlers()
	s.initBuiltInVirtualSDSRoutes()

	if cfg.Netstack.BridgeEnabled {
		logger.Printf("netstack bridge is disabled in this build; ignoring NETSTACK_BRIDGE_ENABLED=true")
	}
	if cfg.WebRadio.Enabled {
		logger.Printf("webradio bridge runs as separate client process; ignoring WEBRADIO_ENABLED=true in router")
	}
	if cfg.Zello.Enabled {
		logger.Printf("zello bridge runs as separate client process; ignoring ZELLO_ENABLED=true in router")
	}
	if cfg.SIP.Enabled {
		logger.Printf("sip bridge runs as separate client process; ignoring SIP_ENABLED=true in router")
	}
	return s, nil
}

func (s *Service) initBuiltInVirtualSDSRoutes() {
	if s.cfg.Echo.VirtualISSI != 0 {
		snap, created := s.upsertVirtualSDSEndpoint(s.cfg.Echo.VirtualISSI, "echo-virtual")
		s.logger.Printf(
			"virtual-sds route initialized issi=%d name=%q created=%t",
			snap.ISSI,
			snap.Name,
			created,
		)
	}
}

func (s *Service) Run(ctx context.Context) error {
	return s.server.Start(ctx)
}

func (s *Service) OnConnect(client *brew.Client) {
	s.logger.Printf("session=%s attached", client.ID)
	s.recordActivity("connect", fmt.Sprintf("session=%s remote=%s", client.ID, client.Remote), map[string]any{
		"session": client.ID,
		"remote":  client.Remote,
	})
	s.broadcastAttachmentControl("connect", client)
}

func (s *Service) OnDisconnect(client *brew.Client) {
	s.noteMOTDDisconnect(client.Snapshot())

	toRelease := make([]*activeCall, 0)

	s.callMu.Lock()
	for id, call := range s.calls {
		if call.OriginClientID == client.ID {
			toRelease = append(toRelease, call)
			delete(s.calls, id)
		}
	}
	s.callMu.Unlock()

	for _, call := range toRelease {
		release := brew.BuildCallRelease(call.ID, s.cfg.Netstack.ReleaseCause)
		destinationType := call.DestinationType
		if destinationType == "" {
			destinationType = destinationTypeGroup
		}
		n := s.broadcastByDestinationType(destinationType, call.DestinationGSI, release, client.ID)
		s.logger.Printf(
			"session=%s release call=%s destination=%d destination_type=%s recipients=%d",
			client.ID,
			call.ID.String(),
			call.DestinationGSI,
			destinationType,
			n,
		)
	}
	s.recordActivity("disconnect", fmt.Sprintf("session=%s", client.ID), map[string]any{
		"session": client.ID,
		"remote":  client.Remote,
	})
	s.broadcastAttachmentControl("disconnect", client)
}

func (s *Service) OnMessage(client *brew.Client, msg brew.ParsedMessage) {
	switch m := msg.(type) {
	case *brew.CallControlMessage:
		s.onCallControlFromClient(client, m)
	case *brew.FrameMessage:
		s.onVoiceFrameFromClient(client, m)
	case *brew.SubscriberMessage:
		s.logger.Printf("session=%s subscriber update groups=%v", client.ID, client.AttachedGroups())
		s.recordActivity("subscriber", fmt.Sprintf("session=%s issi=%d type=%d groups=%v", client.ID, m.Number, m.MsgType, m.Groups), map[string]any{
			"session": client.ID,
			"issi":    m.Number,
			"type":    m.MsgType,
			"groups":  m.Groups,
		})
		s.broadcastAttachmentControl("subscriber_update", client)
		s.maybeSendMOTDForSubscriber(client, m)
	}
}

func (s *Service) onCallControlFromClient(client *brew.Client, m *brew.CallControlMessage) {
	wire, err := brew.BuildCallControlFromMessage(m)
	if err != nil {
		s.logger.Printf("session=%s drop call-control state=%d call=%s reason=%v", client.ID, m.CallState, m.Identifier, err)
		return
	}

	dest, source, hasRouting := callRoutingHint(m.Payload)
	if !hasRouting && m.CallState == brew.CallStateShortTransfer {
		if _, ok := m.Payload.(brew.ShortTransferStatusPayload); ok {
			if inferredSource, inferredDest, ok := s.inferSDSRoute(client); ok {
				source = inferredSource
				dest = inferredDest
				hasRouting = true
				s.logger.Printf(
					"session=%s inferred sds preamble route call=%s source=%d dest=%d",
					client.ID,
					m.Identifier.String(),
					source,
					dest,
				)
			} else {
				// Modern Brew SDS short-transfer preamble can be a 2-byte status-only payload.
				// Route metadata is expected to arrive from context/frame handling.
				s.logger.Printf(
					"session=%s sds preamble accepted call=%s pre_coded_status_only=true",
					client.ID,
					m.Identifier.String(),
				)
				return
			}
		}
	}

	s.callMu.RLock()
	existing := s.calls[m.Identifier]
	s.callMu.RUnlock()
	destinationType := destinationTypeGroup
	targetClientID := ""
	if existing != nil {
		dest = existing.DestinationGSI
		if existing.DestinationType != "" {
			destinationType = existing.DestinationType
		}
		if source == 0 {
			source = existing.SourceISSI
		}
		hasRouting = true
	}
	if existing != nil && existing.DestinationType == destinationTypeSubscriber {
		dest, source, targetClientID = resolveSubscriberCallRoute(existing, client, dest, source)
	}
	if m.CallState == brew.CallStateShortTransfer && existing == nil && hasRouting {
		destinationType = s.resolveSDSDestinationType(dest)
	}
	if m.CallState == brew.CallStateSetupRequest || m.CallState == brew.CallStateConnectRequest {
		destinationType = destinationTypeSubscriber
	}
	if !hasRouting || (dest == 0 && targetClientID == "") {
		s.logger.Printf(
			"session=%s drop call-control state=%d call=%s reason=no-route",
			client.ID,
			m.CallState,
			m.Identifier.String(),
		)
		return
	}
	if source != 0 {
		switch m.CallState {
		case brew.CallStateSetupRequest, brew.CallStateConnectRequest, brew.CallStateShortTransfer:
			s.rememberSDSRouteHint(source, dest)
		}
	}

	if shouldTrackCallState(m.CallState) {
		trackSource := source
		trackDest := dest
		trackOrigin := client.ID

		// For subscriber circuit calls, ConnectRequest forwarding may target the opposite leg.
		// Keep the tracked call endpoints anchored to the original SetupRequest pair.
		if m.CallState == brew.CallStateConnectRequest && existing != nil && existing.DestinationType == destinationTypeSubscriber {
			if existing.SourceISSI != 0 {
				trackSource = existing.SourceISSI
			}
			if existing.DestinationGSI != 0 {
				trackDest = existing.DestinationGSI
			}
			if existing.OriginClientID != "" {
				trackOrigin = existing.OriginClientID
			}
		}

		if source == 0 && existing != nil {
			trackSource = existing.SourceISSI
		}
		if m.CallState != brew.CallStateShortTransfer &&
			m.CallState != brew.CallStateSetupRequest &&
			m.CallState != brew.CallStateConnectRequest {
			destinationType = destinationTypeGroup
		}
		s.callMu.Lock()
		s.calls[m.Identifier] = &activeCall{
			ID:              m.Identifier,
			SourceISSI:      trackSource,
			DestinationGSI:  trackDest,
			DestinationType: destinationType,
			OriginClientID:  trackOrigin,
		}
		s.callMu.Unlock()
	}

	if s.maybeHandleVirtualCircuitCallControl(client, m, source, dest) {
		if shouldReleaseCallState(m.CallState) {
			s.callMu.Lock()
			delete(s.calls, m.Identifier)
			delete(s.virtualCircuitCalls, m.Identifier)
			s.callMu.Unlock()
		}
		return
	}

	recipients := 0
	route := "destination"
	if targetClientID != "" {
		if s.server.SendToClient(targetClientID, wire) {
			recipients = 1
			route = "origin-client"
		} else {
			recipients = s.broadcastByDestinationType(destinationType, dest, wire, client.ID)
			route = "origin-client-fallback"
		}
	} else {
		recipients = s.broadcastByDestinationType(destinationType, dest, wire, client.ID)
	}
	s.logger.Printf(
		"session=%s call-control state=%d call=%s source=%d destination=%d destination_type=%s recipients=%d route=%s target_client=%s",
		client.ID,
		m.CallState,
		m.Identifier.String(),
		source,
		dest,
		destinationType,
		recipients,
		route,
		targetClientID,
	)
	if m.CallState == brew.CallStateShortTransfer {
		if p, ok := m.Payload.(brew.ShortDataPayload); ok {
			s.recordActivity("sds-call", fmt.Sprintf("session=%s source=%d destination=%d", client.ID, p.Source, p.Destination), map[string]any{
				"session":     client.ID,
				"source":      p.Source,
				"destination": p.Destination,
			})
		}
	}

	if shouldReleaseCallState(m.CallState) {
		s.callMu.Lock()
		delete(s.calls, m.Identifier)
		delete(s.virtualCircuitCalls, m.Identifier)
		s.callMu.Unlock()
	}
}

func (s *Service) onVoiceFrameFromClient(client *brew.Client, m *brew.FrameMessage) {
	s.callMu.RLock()
	call := s.calls[m.Identifier]
	virtualCall := s.virtualCircuitCalls[m.Identifier]
	s.callMu.RUnlock()
	if m.FrameType != brew.FrameTypeSDSTransfer && m.FrameType != brew.FrameTypeSDSReport {
		if virtualCall != nil && m.FrameType == brew.FrameTypeTrafficChannel {
			wire := brew.BuildFrame(m.FrameType, m.Identifier, m.LengthBits, m.Data)
			recipients := s.server.BroadcastToSubscriber(virtualCall.SourceISSI, wire, "")
			s.logger.Printf(
				"session=%s virtual-circuit-echo call=%s source=%d recipients=%d bytes=%d",
				client.ID,
				m.Identifier.String(),
				virtualCall.SourceISSI,
				recipients,
				len(m.Data),
			)
			return
		}
		if call == nil {
			return
		}
		wire := brew.BuildFrame(m.FrameType, m.Identifier, m.LengthBits, m.Data)
		if call.DestinationType == destinationTypeSubscriber && call.SourceISSI != 0 && call.DestinationGSI != 0 {
			recipients := s.server.BroadcastToSubscriber(call.DestinationGSI, wire, client.ID)
			if call.SourceISSI != call.DestinationGSI {
				recipients += s.server.BroadcastToSubscriber(call.SourceISSI, wire, client.ID)
			}
			if recipients == 0 {
				s.logger.Printf(
					"session=%s drop frame call=%s destination_type=%s source=%d destination=%d reason=no-recipients",
					client.ID,
					m.Identifier.String(),
					call.DestinationType,
					call.SourceISSI,
					call.DestinationGSI,
				)
			}
			return
		}
		_ = s.broadcastByDestinationType(call.DestinationType, call.DestinationGSI, wire, client.ID)
		return
	}

	env := parseSDSFrameEnvelope(m.Data)
	payload := env.Payload
	source := env.Source
	dest := env.Destination

	// Managed callout mode:
	// if a subscriber sends plain text SDS while a callout is active for its group,
	// relay that text as protocol-195 callout response into attached group(s).
	if m.FrameType == brew.FrameTypeSDSTransfer {
		if s.maybeRelayManagedCalloutResponse(client, m.Identifier, source, dest, payload) {
			return
		}
	}

	destinationType := destinationTypeSubscriber
	if call != nil {
		if source == 0 {
			source = call.SourceISSI
		}
		if dest == 0 {
			dest = call.DestinationGSI
		}
		if call.DestinationType != "" {
			destinationType = call.DestinationType
		} else {
			destinationType = s.resolveSDSDestinationType(dest)
		}
	} else {
		if source == 0 || dest == 0 {
			inferredSource, inferredDest, ok := s.inferSDSRoute(client)
			if !ok {
				s.logger.Printf(
					"session=%s drop sds frame call=%s reason=no-route",
					client.ID,
					m.Identifier.String(),
				)
				return
			}
			if source == 0 {
				source = inferredSource
			}
			if dest == 0 {
				dest = inferredDest
			}
		}
		destinationType = s.resolveSDSDestinationType(dest)
	}

	if source != 0 && dest != 0 {
		s.rememberSDSRouteHint(source, dest)
	}

	wire := brew.BuildFrame(m.FrameType, m.Identifier, m.LengthBits, buildSDSFramePayload(source, dest, payload))
	recipients := s.broadcastByDestinationType(destinationType, dest, wire, client.ID)

	calloutKey := ""
	calloutInfo := map[string]any(nil)
	if m.FrameType == brew.FrameTypeSDSTransfer {
		if callout, ok := parseCalloutPayload(payload); ok {
			calloutKey = s.noteCalloutRx(client.ID, sdsFrameEnvelope{
				Source:      source,
				Destination: dest,
				Payload:     payload,
				Wrapped:     true,
			}, callout)
			calloutInfo = map[string]any{
				"callout_key":               calloutKey,
				"callout_message_type":      callout.MessageType,
				"callout_delivery_report":   callout.DeliveryReportRequest,
				"callout_ref":               callout.MessageRef,
				"callout_function":          callout.Function,
				"callout_number":            callout.CalloutNumber,
				"callout_severity":          callout.Severity,
				"callout_group_control":     callout.GroupControl,
				"callout_timestamp_control": callout.TimestampControl,
				"callout_user_receipt":      callout.UserReceiptControl,
				"callout_end":               callout.EndCallout,
				"callout_ptt_not_allowed":   callout.PTTNotAllowed,
				"callout_text":              callout.Text,
			}
		}
	}

	if m.FrameType == brew.FrameTypeSDSTransfer &&
		s.hasVirtualSDSEndpoint(dest) &&
		sdsTLDeliveryReportRequested(payload) {
		reportRef := byte(0)
		if len(payload) >= 3 {
			reportRef = payload[2]
		}
		s.emitSDSDeliveryReportAck(m.Identifier, dest, source, reportRef)
	}

	s.maybeStoreVirtualSDSMessage(dest, virtualSDSMessage{
		Direction:   "rx",
		CallID:      m.Identifier.String(),
		Session:     client.ID,
		Source:      source,
		Destination: dest,
		FrameType:   m.FrameType,
		Kind:        detectSDSKind(payload),
		PayloadHex:  hex.EncodeToString(payload),
		Text:        decodeSDSText(payload),
		Wrapped:     true,
	})

	s.logger.Printf(
		"session=%s sds-frame call=%s source=%d destination=%d destination_type=%s recipients=%d",
		client.ID,
		m.Identifier.String(),
		source,
		dest,
		destinationType,
		recipients,
	)

	s.recordActivity("sds-rx", fmt.Sprintf("session=%s destination=%d destination_type=%s type=%d bytes=%d", client.ID, dest, destinationType, m.FrameType, len(payload)), map[string]any{
		"session":          client.ID,
		"destination":      dest,
		"destination_type": destinationType,
		"frame_type":       m.FrameType,
		"sds_kind":         detectSDSKind(payload),
		"payload_hex":      hex.EncodeToString(payload),
		"sds_source":       source,
		"sds_dest":         dest,
		"wrapped":          true,
		"callout_key":      calloutKey,
		"callout":          calloutInfo,
	})
}

func (s *Service) emitSDSDeliveryReportAck(callID uuid.UUID, sourceVirtualISSI, destinationISSI uint32, ref byte) {
	if sourceVirtualISSI == 0 || destinationISSI == 0 {
		return
	}
	// SDS report status byte: 0x00 = delivered/success.
	reportPayload := []byte{0x00}
	reportFrame := brew.BuildSDSReportFrame(callID, 8, buildSDSFramePayload(sourceVirtualISSI, destinationISSI, reportPayload))
	nFrame := s.server.BroadcastToSubscriber(destinationISSI, reportFrame, "")

	s.logger.Printf(
		"sds-report ack source=%d destination=%d call=%s ref=%d recipients_frame=%d",
		sourceVirtualISSI,
		destinationISSI,
		callID.String(),
		ref,
		nFrame,
	)

	s.recordActivity("sds-report-tx", fmt.Sprintf("source=%d destination=%d ref=%d recipients=%d", sourceVirtualISSI, destinationISSI, ref, nFrame), map[string]any{
		"call_id":          callID.String(),
		"source":           sourceVirtualISSI,
		"destination":      destinationISSI,
		"ref":              ref,
		"payload_hex":      hex.EncodeToString(reportPayload),
		"recipients_frame": nFrame,
	})

	s.maybeStoreVirtualSDSMessage(sourceVirtualISSI, virtualSDSMessage{
		Direction:   "tx",
		CallID:      callID.String(),
		Source:      sourceVirtualISSI,
		Destination: destinationISSI,
		FrameType:   brew.FrameTypeSDSReport,
		Kind:        "report",
		PayloadHex:  hex.EncodeToString(reportPayload),
		Wrapped:     false,
	})
}

func (s *Service) inferSDSRoute(client *brew.Client) (source uint32, destination uint32, ok bool) {
	clientSnapshot := client.Snapshot()
	if len(clientSnapshot.Subscribers) == 0 {
		return 0, 0, false
	}
	// Use the first known subscriber on this session as source.
	// This keeps SDS routing functional when a client is attached with multiple ISSIs.
	source = clientSnapshot.Subscribers[0].Number

	if dest, ok := s.sdsRouteHint(source); ok {
		return source, dest, true
	}

	all := s.server.SnapshotClients()
	subscriberCandidates := make(map[uint32]struct{})
	for _, c := range all {
		for _, sub := range c.Subscribers {
			if sub.Number == source {
				continue
			}
			if s.excludeAttachmentSubscriber(sub.Number) {
				continue
			}
			subscriberCandidates[sub.Number] = struct{}{}
		}
	}

	virtualCandidates := make(map[uint32]struct{})
	for _, issi := range s.virtualSDSNumbers() {
		if issi == source {
			continue
		}
		virtualCandidates[issi] = struct{}{}
	}

	groupCandidates := make(map[uint32]struct{})
	for _, sub := range clientSnapshot.Subscribers {
		if sub.Number != source {
			continue
		}
		for _, g := range sub.Groups {
			if g == 0 {
				continue
			}
			groupCandidates[g] = struct{}{}
		}
	}
	if len(groupCandidates) == 0 {
		// Fallback to all groups in this session if source->groups mapping is unavailable.
		for _, sub := range clientSnapshot.Subscribers {
			for _, g := range sub.Groups {
				if g == 0 {
					continue
				}
				groupCandidates[g] = struct{}{}
			}
		}
	}

	if dest, ok := pickPreferredSDSDestination(subscriberCandidates, groupCandidates, virtualCandidates); ok {
		return source, dest, true
	}

	combined := make(map[uint32]struct{}, len(virtualCandidates)+len(subscriberCandidates)+len(groupCandidates))
	for dest := range virtualCandidates {
		combined[dest] = struct{}{}
	}
	for dest := range subscriberCandidates {
		combined[dest] = struct{}{}
	}
	for dest := range groupCandidates {
		combined[dest] = struct{}{}
	}

	if len(combined) > 1 {
		choices := keysSorted(combined)
		s.logger.Printf(
			"session=%s infer sds route ambiguous source=%d subscriber_candidates=%v group_candidates=%v virtual_candidates=%v merged=%v",
			client.ID,
			source,
			keysSorted(subscriberCandidates),
			keysSorted(groupCandidates),
			keysSorted(virtualCandidates),
			choices,
		)
	}

	return 0, 0, false
}

func (s *Service) rememberSDSRouteHint(source, destination uint32) {
	if source == 0 || destination == 0 {
		return
	}
	s.sdsRouteHintMu.Lock()
	s.sdsRouteHints[source] = sdsRouteHint{
		Destination: destination,
		Updated:     time.Now().UTC(),
	}
	s.sdsRouteHintMu.Unlock()
}

func (s *Service) sdsRouteHint(source uint32) (uint32, bool) {
	if source == 0 {
		return 0, false
	}
	now := time.Now().UTC()
	s.sdsRouteHintMu.RLock()
	hint, ok := s.sdsRouteHints[source]
	s.sdsRouteHintMu.RUnlock()
	if !ok {
		return 0, false
	}
	if now.Sub(hint.Updated) > sdsRouteHintTTL {
		s.sdsRouteHintMu.Lock()
		// delete stale entry only if unchanged
		current, still := s.sdsRouteHints[source]
		if still && current.Updated.Equal(hint.Updated) && current.Destination == hint.Destination {
			delete(s.sdsRouteHints, source)
		}
		s.sdsRouteHintMu.Unlock()
		return 0, false
	}
	return hint.Destination, true
}

func pickPreferredSDSDestination(subscriberCandidates, groupCandidates, virtualCandidates map[uint32]struct{}) (uint32, bool) {
	// Explicit virtual endpoints are operator-configured SDS sinks/sources.
	// If exactly one exists, it is preferred over inferred subscriber/group targets.
	if len(virtualCandidates) == 1 {
		for dest := range virtualCandidates {
			return dest, true
		}
	}
	if len(virtualCandidates) > 1 {
		return 0, false
	}

	nonGroupCandidates := make(map[uint32]struct{}, len(subscriberCandidates)+len(virtualCandidates))
	for s := range subscriberCandidates {
		nonGroupCandidates[s] = struct{}{}
	}

	// Prefer explicit subscriber/virtual destinations over group fallback.
	if len(nonGroupCandidates) == 1 {
		for dest := range nonGroupCandidates {
			return dest, true
		}
	}
	if len(nonGroupCandidates) > 1 {
		return 0, false
	}

	if len(groupCandidates) == 1 {
		for dest := range groupCandidates {
			return dest, true
		}
	}
	return 0, false
}

func keysSorted(m map[uint32]struct{}) []uint32 {
	out := make([]uint32, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (s *Service) resolveSDSDestinationType(destination uint32) string {
	if destination == 0 {
		return destinationTypeSubscriber
	}
	if s.hasVirtualSDSEndpoint(destination) {
		return destinationTypeSubscriber
	}

	clients := s.server.SnapshotClients()
	hasSubscriber := false
	hasGroup := false
	for _, c := range clients {
		for _, sub := range c.Subscribers {
			if sub.Number == destination {
				hasSubscriber = true
			}
			for _, g := range sub.Groups {
				if g == destination {
					hasGroup = true
					break
				}
			}
		}
	}
	if hasSubscriber {
		return destinationTypeSubscriber
	}
	if hasGroup {
		return destinationTypeGroup
	}
	return destinationTypeSubscriber
}

func (s *Service) broadcastByDestinationType(destinationType string, destination uint32, payload []byte, excludeClientID string) int {
	if destinationType == destinationTypeGroup {
		return s.server.BroadcastToGroup(destination, payload, excludeClientID)
	}
	return s.server.BroadcastToSubscriber(destination, payload, excludeClientID)
}

func (s *Service) maybeHandleVirtualCircuitCallControl(client *brew.Client, m *brew.CallControlMessage, source, dest uint32) bool {
	virtualISSI := s.cfg.Echo.VirtualISSI
	if virtualISSI == 0 {
		return false
	}

	s.callMu.Lock()
	vc := s.virtualCircuitCalls[m.Identifier]
	if vc == nil && dest == virtualISSI && (m.CallState == brew.CallStateSetupRequest || m.CallState == brew.CallStateConnectRequest) {
		if source == 0 {
			source = sourceHintFromClient(client)
		}
		vc = &virtualCircuitCall{
			CallID:      m.Identifier,
			SourceISSI:  source,
			VirtualISSI: virtualISSI,
		}
		s.virtualCircuitCalls[m.Identifier] = vc
	}
	if vc != nil && vc.SourceISSI == 0 && source != 0 {
		vc.SourceISSI = source
	}
	s.callMu.Unlock()

	if vc == nil {
		return false
	}
	if vc.SourceISSI == 0 {
		s.logger.Printf(
			"session=%s virtual-circuit drop state=%d call=%s reason=no-source",
			client.ID,
			m.CallState,
			m.Identifier.String(),
		)
		return true
	}

	switch m.CallState {
	case brew.CallStateSetupRequest:
		nAccept := s.server.BroadcastToSubscriber(vc.SourceISSI, brew.BuildSetupAccept(m.Identifier), "")
		nAlert := s.server.BroadcastToSubscriber(vc.SourceISSI, brew.BuildCallAlert(m.Identifier), "")
		connectPayload := brew.CircularCallPayload{
			Source:      vc.VirtualISSI,
			Destination: vc.SourceISSI,
			Grant:       1,
		}
		if p, ok := m.Payload.(brew.CircularCallPayload); ok {
			connectPayload.Number = p.Number
			connectPayload.Priority = p.Priority
			connectPayload.Service = p.Service
			connectPayload.Mode = p.Mode
			connectPayload.Duplex = p.Duplex
			connectPayload.Method = p.Method
			connectPayload.Communication = p.Communication
			connectPayload.Timeout = p.Timeout
			connectPayload.Ownership = p.Ownership
			connectPayload.Queued = p.Queued
		}
		nConnect := s.server.BroadcastToSubscriber(vc.SourceISSI, brew.BuildConnectRequest(m.Identifier, connectPayload), "")
		s.callMu.Lock()
		if current := s.virtualCircuitCalls[m.Identifier]; current != nil {
			current.Connected = true
		}
		s.callMu.Unlock()
		s.logger.Printf(
			"session=%s virtual-circuit setup call=%s source=%d virtual=%d setup_accept=%d call_alert=%d connect_request=%d",
			client.ID,
			m.Identifier.String(),
			vc.SourceISSI,
			vc.VirtualISSI,
			nAccept,
			nAlert,
			nConnect,
		)
		return true
	case brew.CallStateConnectRequest:
		grant := uint8(1)
		permission := uint8(1)
		if p, ok := m.Payload.(brew.CircularCallPayload); ok {
			if p.Grant != 0 {
				grant = p.Grant
			}
			if p.Permission != 0 {
				permission = p.Permission
			}
			if p.Duplex == 0 && p.Permission == 0 {
				permission = 0
			}
		}
		nConfirm := s.server.BroadcastToSubscriber(
			vc.SourceISSI,
			brew.BuildConnectConfirm(m.Identifier, brew.CircularGrantPayload{
				Grant:      grant,
				Permission: permission,
			}),
			"",
		)
		s.callMu.Lock()
		if current := s.virtualCircuitCalls[m.Identifier]; current != nil {
			current.Connected = true
		}
		s.callMu.Unlock()
		s.logger.Printf(
			"session=%s virtual-circuit connect call=%s source=%d virtual=%d grant=%d permission=%d recipients=%d",
			client.ID,
			m.Identifier.String(),
			vc.SourceISSI,
			vc.VirtualISSI,
			grant,
			permission,
			nConfirm,
		)
		return true
	case brew.CallStateCallRelease, brew.CallStateSetupReject:
		s.callMu.Lock()
		delete(s.virtualCircuitCalls, m.Identifier)
		s.callMu.Unlock()
		s.logger.Printf(
			"session=%s virtual-circuit release call=%s source=%d virtual=%d",
			client.ID,
			m.Identifier.String(),
			vc.SourceISSI,
			vc.VirtualISSI,
		)
		return true
	default:
		// Consume all other control states of virtual calls.
		return true
	}
}

func resolveSubscriberCallRoute(existing *activeCall, client *brew.Client, dest, source uint32) (uint32, uint32, string) {
	if existing == nil {
		return dest, source, ""
	}
	sender := sourceHintFromClient(client)
	if source == 0 && sender != 0 {
		source = sender
	}
	if client != nil && existing.OriginClientID != "" && client.ID != existing.OriginClientID {
		// A response from the radio/MS leg should return to the SIP origin client.
		if existing.SourceISSI != 0 {
			dest = existing.SourceISSI
		}
		return dest, source, existing.OriginClientID
	}
	if sender == existing.DestinationGSI && existing.SourceISSI != 0 {
		dest = existing.SourceISSI
	} else if sender == existing.SourceISSI && existing.DestinationGSI != 0 {
		dest = existing.DestinationGSI
	} else if sender == 0 && client != nil && existing.OriginClientID != "" {
		// Some clients emit ConnectRequest with the original setup payload route.
		// Fall back to the tracked origin leg when subscriber hints are unavailable.
		if client.ID == existing.OriginClientID && existing.DestinationGSI != 0 {
			dest = existing.DestinationGSI
		} else if client.ID != existing.OriginClientID && existing.SourceISSI != 0 {
			dest = existing.SourceISSI
		}
	}
	return dest, source, ""
}

func sourceHintFromClient(client *brew.Client) uint32 {
	if client == nil {
		return 0
	}
	snapshot := client.Snapshot()
	if len(snapshot.Subscribers) == 0 {
		return 0
	}
	return snapshot.Subscribers[0].Number
}

func callRoutingHint(payload any) (destination uint32, source uint32, ok bool) {
	switch p := payload.(type) {
	case brew.GroupTransmissionPayload:
		return p.Destination, p.Source, true
	case brew.CircularCallPayload:
		return p.Destination, p.Source, true
	case brew.ShortDataPayload:
		return p.Destination, p.Source, true
	case brew.PacketRequestPayload:
		return p.Number, p.Number, true
	default:
		return 0, 0, false
	}
}

func shouldTrackCallState(state uint8) bool {
	switch state {
	case brew.CallStateGroupTX,
		brew.CallStateSetupRequest,
		brew.CallStateConnectRequest,
		brew.CallStateShortTransfer,
		brew.CallStatePDPRequest:
		return true
	default:
		return false
	}
}

func shouldReleaseCallState(state uint8) bool {
	switch state {
	case brew.CallStateCallRelease,
		brew.CallStateSetupReject,
		brew.CallStatePDPRelease,
		brew.CallStatePDPReject:
		return true
	default:
		return false
	}
}

func payloadCause(v any) uint8 {
	switch p := v.(type) {
	case brew.CausePayload:
		return p.Cause
	default:
		return 0
	}
}

func (s *Service) netstackStartCall(callID uuid.UUID, sourceISSI, destinationGSI uint32) bool {
	return s.StartInjectedCall("netstack", callID, sourceISSI, destinationGSI)
}

func (s *Service) netstackIdleCall(callID uuid.UUID, cause uint8) {
	s.IdleInjectedCall("netstack", callID, cause)
}

func (s *Service) netstackReleaseCall(callID uuid.UUID, cause uint8) {
	s.ReleaseInjectedCall("netstack", callID, cause)
}

func (s *Service) netstackVoiceFrame(callID uuid.UUID, data []byte) {
	s.InjectedVoiceFrame("netstack", callID, data)
}

func (s *Service) StartInjectedCall(origin string, callID uuid.UUID, sourceISSI, destinationGSI uint32) bool {
	return s.StartInjectedGroupTX(origin, callID, sourceISSI, destinationGSI, 0, 0, 0)
}

func (s *Service) StartInjectedGroupTX(
	origin string,
	callID uuid.UUID,
	sourceISSI, destinationGSI uint32,
	priority uint8,
	access uint8,
	service uint16,
) bool {
	s.callMu.Lock()
	s.calls[callID] = &activeCall{
		ID:              callID,
		SourceISSI:      sourceISSI,
		DestinationGSI:  destinationGSI,
		DestinationType: destinationTypeGroup,
		OriginClientID:  origin,
	}
	s.callMu.Unlock()

	wire := brew.BuildGroupTXWithAccess(callID, sourceISSI, destinationGSI, priority, access, service)
	recipients := s.server.BroadcastToGroup(destinationGSI, wire, "")
	if recipients == 0 {
		s.callMu.Lock()
		delete(s.calls, callID)
		s.callMu.Unlock()
		s.logger.Printf(
			"%s group-tx ignored call=%s source=%d tg=%d recipients=0",
			origin,
			callID.String(),
			sourceISSI,
			destinationGSI,
		)
		return false
	}
	s.logger.Printf(
		"%s group-tx call=%s source=%d tg=%d recipients=%d",
		origin,
		callID.String(),
		sourceISSI,
		destinationGSI,
		recipients,
	)
	return true
}

func (s *Service) IdleInjectedCall(origin string, callID uuid.UUID, cause uint8) {
	s.callMu.RLock()
	call := s.calls[callID]
	s.callMu.RUnlock()
	if call == nil {
		return
	}
	wire := brew.BuildGroupIdle(callID, cause)
	recipients := s.server.BroadcastToGroup(call.DestinationGSI, wire, "")
	s.logger.Printf(
		"%s group-idle call=%s tg=%d recipients=%d",
		origin,
		callID.String(),
		call.DestinationGSI,
		recipients,
	)
}

func (s *Service) ReleaseInjectedCall(origin string, callID uuid.UUID, cause uint8) {
	var call *activeCall
	s.callMu.Lock()
	call = s.calls[callID]
	delete(s.calls, callID)
	s.callMu.Unlock()
	if call == nil {
		return
	}
	wire := brew.BuildCallRelease(callID, cause)
	recipients := s.server.BroadcastToGroup(call.DestinationGSI, wire, "")
	s.logger.Printf(
		"%s call-release call=%s tg=%d recipients=%d",
		origin,
		callID.String(),
		call.DestinationGSI,
		recipients,
	)
}

func (s *Service) InjectedVoiceFrame(_ string, callID uuid.UUID, data []byte) {
	s.callMu.RLock()
	call := s.calls[callID]
	s.callMu.RUnlock()
	if call == nil {
		return
	}

	ste, err := normalizeTrafficSTE(data)
	if err != nil {
		s.logger.Printf("drop injected traffic frame call=%s reason=%v", callID.String(), err)
		return
	}

	wire := brew.BuildVoiceFrame(callID, uint16(len(ste)*8), ste)
	_ = s.server.BroadcastToGroup(call.DestinationGSI, wire, "")
}

func (s *Service) InjectedPacketFrame(_ string, callID uuid.UUID, data []byte) {
	s.callMu.RLock()
	call := s.calls[callID]
	s.callMu.RUnlock()
	if call == nil {
		return
	}
	wire := brew.BuildPacketDataFrame(callID, uint16(len(data)*8), data)
	_ = s.server.BroadcastToGroup(call.DestinationGSI, wire, "")
}

func (s *Service) GroupSubscriberCount(gssi uint32) int {
	return s.server.CountAttachedToGroup(gssi)
}

func (s *Service) recordActivity(kind, message string, data map[string]any) {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()

	s.activitySeq++
	entry := dashboardActivity{
		Seq:     s.activitySeq,
		Kind:    kind,
		Message: message,
		Time:    time.Now().UTC(),
		Data:    data,
	}
	s.activityRing = append(s.activityRing, entry)
	if len(s.activityRing) > 600 {
		s.activityRing = append([]dashboardActivity(nil), s.activityRing[len(s.activityRing)-600:]...)
	}
}

func (s *Service) activitySince(seq int64) []dashboardActivity {
	s.activityMu.RLock()
	defer s.activityMu.RUnlock()
	if len(s.activityRing) == 0 {
		return nil
	}
	out := make([]dashboardActivity, 0, len(s.activityRing))
	for _, e := range s.activityRing {
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	return out
}

func (s *Service) currentActivitySeq() int64 {
	s.activityMu.RLock()
	defer s.activityMu.RUnlock()
	return s.activitySeq
}
