package service

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/freetetra/server/internal/brew"
)

type dashboardActivity struct {
	Seq     int64          `json:"seq"`
	Time    time.Time      `json:"time"`
	Kind    string         `json:"kind"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

type dashboardSubscriber struct {
	Session string   `json:"session"`
	Remote  string   `json:"remote"`
	ISSI    uint32   `json:"issi"`
	Groups  []uint32 `json:"groups"`
}

type dashboardGroup struct {
	GSSI        uint32 `json:"gssi"`
	Subscribers int    `json:"subscribers"`
	Sessions    int    `json:"sessions"`
}

type dashboardSnapshotResponse struct {
	ServerTime       string                        `json:"server_time"`
	Clients          []brew.ClientSnapshot         `json:"clients"`
	Subscribers      []dashboardSubscriber         `json:"subscribers"`
	Groups           []dashboardGroup              `json:"groups"`
	Callouts         []dashboardCalloutState       `json:"callouts"`
	VirtualEndpoints []dashboardVirtualSDSEndpoint `json:"virtual_sds_endpoints"`
	Activity         []dashboardActivity           `json:"activity"`
	LastSeq          int64                         `json:"last_seq"`
}

type uiSDSSendRequest struct {
	DestinationType string `json:"destination_type"`
	Destination     uint32 `json:"destination"`
	SDSType         string `json:"sds_type"`
	Text            string `json:"text"`
	PayloadHex      string `json:"payload_hex"`
	SourceISSI      uint32 `json:"source_issi"`
	CalloutRefMode  string `json:"callout_ref_mode"`
	// Optional internal flow hint: thread key in the format "<type>:<destination>:<ref>".
	CalloutKey string `json:"callout_key"`

	// Deprecated: callout_number is ignored; callout numbering is service-managed.
	CalloutNumber          uint8 `json:"callout_number"`
	CalloutFunction        uint8 `json:"callout_function"`
	CalloutSeverity        uint8 `json:"callout_severity"`
	CalloutGroupControl    uint8 `json:"callout_group_control"`
	CalloutMessageType     uint8 `json:"callout_message_type"`
	CalloutReportRequested bool  `json:"callout_report_requested"`
	// Deprecated: use callout_report_requested.
	CalloutDeliveryReport     uint8 `json:"callout_delivery_report"`
	CalloutTextCodingScheme   uint8 `json:"callout_text_coding_scheme"`
	CalloutStorageAllowed     bool  `json:"callout_storage_allowed"`
	CalloutExtensionHeader    bool  `json:"callout_extension_header"`
	CalloutTimestampControl   bool  `json:"callout_timestamp_control"`
	CalloutUserReceiptControl bool  `json:"callout_user_receipt_control"`
	CalloutTextIsStatus       bool  `json:"callout_text_is_status"`
	CalloutEnd                bool  `json:"callout_end"`
	CalloutPTTNotAllowed      bool  `json:"callout_ptt_not_allowed"`
}

type uiVirtualSDSEndpointRequest struct {
	ISSI uint32 `json:"issi"`
	Name string `json:"name"`
}

func (s *Service) registerDashboardHandlers() {
	// New FreeTetra admin dashboard (replaces cheetah's Vuetify UI)
	s.server.RegisterHTTPHandler("/ui", s.handleAdminDashboard)
	s.server.RegisterHTTPHandler("/ui/", s.handleAdminDashboard)
	// Keep old Vuetify dashboard at /ui/legacy for reference
	s.server.RegisterHTTPHandler("/ui/legacy", s.handleDashboardUI)
	// API endpoints
	s.server.RegisterHTTPHandler("/api/dashboard/snapshot", s.handleDashboardSnapshot)
	s.server.RegisterHTTPHandler("/api/sds/send", s.handleSDSSend)
	s.server.RegisterHTTPHandler("/api/sds/virtual/endpoints", s.handleVirtualSDSEndpoints)
	s.server.RegisterHTTPHandler("/api/sds/virtual/endpoints/", s.handleVirtualSDSEndpointPath)
	s.server.RegisterHTTPHandler("/api/sds/virtual/send", s.handleVirtualSDSSend)
	// Peer info for admin dashboard
	s.server.RegisterHTTPHandler("/api/peers", s.handlePeersAPI)
}

func (s *Service) handleDashboardUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(dashboardVueHTML))
}

func (s *Service) handleDashboardSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sinceSeq := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("since_seq")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			sinceSeq = v
		}
	}

	clients := s.server.SnapshotClients()
	subscribers := make([]dashboardSubscriber, 0)
	groupSessions := make(map[uint32]map[string]struct{})
	groupSubs := make(map[uint32]map[uint32]struct{})

	for _, c := range clients {
		for _, sub := range c.Subscribers {
			ds := dashboardSubscriber{
				Session: c.ID,
				Remote:  c.Remote,
				ISSI:    sub.Number,
				Groups:  append([]uint32(nil), sub.Groups...),
			}
			subscribers = append(subscribers, ds)
			for _, g := range sub.Groups {
				if groupSessions[g] == nil {
					groupSessions[g] = make(map[string]struct{})
				}
				if groupSubs[g] == nil {
					groupSubs[g] = make(map[uint32]struct{})
				}
				groupSessions[g][c.ID] = struct{}{}
				groupSubs[g][sub.Number] = struct{}{}
			}
		}
	}

	sort.Slice(subscribers, func(i, j int) bool {
		if subscribers[i].ISSI != subscribers[j].ISSI {
			return subscribers[i].ISSI < subscribers[j].ISSI
		}
		return subscribers[i].Session < subscribers[j].Session
	})

	groups := make([]dashboardGroup, 0, len(groupSessions))
	for gssi, sess := range groupSessions {
		groups = append(groups, dashboardGroup{
			GSSI:        gssi,
			Subscribers: len(groupSubs[gssi]),
			Sessions:    len(sess),
		})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].GSSI < groups[j].GSSI })

	lastSeq := s.currentActivitySeq()
	activity := s.activitySince(sinceSeq)
	callouts := s.snapshotCallouts()
	virtualEndpoints := s.snapshotVirtualSDSEndpoints()
	resp := dashboardSnapshotResponse{
		ServerTime:       time.Now().UTC().Format(time.RFC3339),
		Clients:          clients,
		Subscribers:      subscribers,
		Groups:           groups,
		Callouts:         callouts,
		VirtualEndpoints: virtualEndpoints,
		Activity:         activity,
		LastSeq:          lastSeq,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Service) handleSDSSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req uiSDSSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	req.DestinationType = strings.ToLower(strings.TrimSpace(req.DestinationType))
	req.SDSType = strings.ToLower(strings.TrimSpace(req.SDSType))
	if req.Destination == 0 {
		http.Error(w, "destination must be > 0", http.StatusBadRequest)
		return
	}
	switch req.DestinationType {
	case "group", "subscriber":
	default:
		http.Error(w, "destination_type must be 'group' or 'subscriber'", http.StatusBadRequest)
		return
	}

	source := req.SourceISSI
	virtualSource := false
	virtualSnap := dashboardVirtualSDSEndpoint{}
	virtualCreated := false
	if source == 0 && req.SDSType == "callout" && req.DestinationType == destinationTypeGroup {
		source, virtualSnap, virtualCreated = s.ensureGroupCalloutVirtualEndpoint(req.Destination)
		virtualSource = true
	}
	if source == 0 {
		source = 900000
	}

	calloutOpts, err := calloutOptionsFromRequest(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.SDSType == "callout" && s.calloutMgr != nil {
		forcedNumber, forced, err := calloutThreadNumberFromRequest(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if forced {
			calloutOpts.CalloutNumber = forcedNumber
		} else {
			calloutOpts.CalloutNumber = s.calloutMgr.allocateOutboundCalloutNumber(
				req.DestinationType,
				req.Destination,
				source,
				calloutOpts.Function,
				calloutOpts.MessageType,
				strings.ToLower(strings.TrimSpace(req.CalloutRefMode)),
			)
		}
	}
	payload, err := buildSDSPayloadWithOptions(req.SDSType, req.Text, req.PayloadHex, calloutOpts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	callID := uuid.New()
	control := brew.BuildShortData(callID, brew.ShortDataPayload{
		Source:      source,
		Destination: req.Destination,
		Number:      strings.ToUpper(req.SDSType),
	})
	framePayload := buildSDSFramePayload(source, req.Destination, payload)
	frame := brew.BuildSDSFrame(callID, uint16(len(payload)*8), framePayload)
	release := brew.BuildCallRelease(callID, 0)

	calloutKey := ""
	calloutNumberOut := -1
	if req.SDSType == "callout" {
		if callout, ok := parseCalloutPayload(payload); ok {
			calloutKey = s.noteCalloutTx(req.DestinationType, req.Destination, source, callout)
			calloutNumberOut = int(callout.CalloutNumber)
		}
	}

	sendFn := s.server.BroadcastToGroup
	if req.DestinationType == "subscriber" {
		sendFn = func(dest uint32, payload []byte, exclude string) int {
			return s.server.BroadcastToSubscriber(dest, payload, exclude)
		}
	}

	nControl := sendFn(req.Destination, control, "")
	nFrame := sendFn(req.Destination, frame, "")
	nRelease := sendFn(req.Destination, release, "")

	s.maybeStoreVirtualSDSMessage(source, virtualSDSMessage{
		Direction:   "tx",
		CallID:      callID.String(),
		Source:      source,
		Destination: req.Destination,
		FrameType:   brew.FrameTypeSDSTransfer,
		Kind:        detectSDSKind(payload),
		PayloadHex:  hex.EncodeToString(payload),
		Text:        decodeSDSText(payload),
		Wrapped:     false,
	})

	s.recordActivity("sds-tx", fmt.Sprintf("ui send type=%s destination_type=%s destination=%d recipients=%d", req.SDSType, req.DestinationType, req.Destination, nFrame), map[string]any{
		"call_id":            callID.String(),
		"type":               req.SDSType,
		"destination_type":   req.DestinationType,
		"destination":        req.Destination,
		"source":             source,
		"payload_hex":        hex.EncodeToString(payload),
		"callout_key":        calloutKey,
		"recipients_control": nControl,
		"recipients_frame":   nFrame,
		"recipients_release": nRelease,
		"source_virtual":     virtualSource,
	})
	if virtualSource {
		s.recordActivity("sds-virtual-endpoint", fmt.Sprintf("group callout source mapped group=%d -> issi=%d created=%t", req.Destination, virtualSnap.ISSI, virtualCreated), map[string]any{
			"group":   req.Destination,
			"issi":    virtualSnap.ISSI,
			"name":    virtualSnap.Name,
			"created": virtualCreated,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":                 true,
		"call_id":            callID.String(),
		"callout_key":        calloutKey,
		"callout_number":     calloutNumberOut,
		"source":             source,
		"source_virtual":     virtualSource,
		"virtual_endpoint":   virtualSnap,
		"recipients_control": nControl,
		"recipients_frame":   nFrame,
		"recipients_release": nRelease,
	})
}

func (s *Service) handleVirtualSDSEndpoints(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"endpoints": s.snapshotVirtualSDSEndpoints(),
		})
		return
	case http.MethodPost:
		var req uiVirtualSDSEndpointRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if req.ISSI == 0 {
			http.Error(w, "issi must be > 0", http.StatusBadRequest)
			return
		}
		snap, created := s.upsertVirtualSDSEndpoint(req.ISSI, req.Name)
		s.recordActivity("sds-virtual-endpoint", fmt.Sprintf("upsert issi=%d name=%q created=%t", req.ISSI, snap.Name, created), map[string]any{
			"issi":    req.ISSI,
			"name":    snap.Name,
			"created": created,
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"created":  created,
			"endpoint": snap,
		})
		return
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

func (s *Service) handleVirtualSDSEndpointPath(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/sds/virtual/endpoints/"), "/")
	if trimmed == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(trimmed, "/")
	issi64, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil || issi64 == 0 {
		http.Error(w, "invalid endpoint issi", http.StatusBadRequest)
		return
	}
	issi := uint32(issi64)

	if len(parts) == 1 && r.Method == http.MethodDelete {
		if ok := s.deleteVirtualSDSEndpoint(issi); !ok {
			http.NotFound(w, r)
			return
		}
		s.recordActivity("sds-virtual-endpoint", fmt.Sprintf("delete issi=%d", issi), map[string]any{
			"issi": issi,
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"issi": issi,
		})
		return
	}

	if len(parts) == 2 && parts[1] == "messages" && r.Method == http.MethodGet {
		sinceSeq := int64(0)
		if raw := strings.TrimSpace(r.URL.Query().Get("since_seq")); raw != "" {
			if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
				sinceSeq = v
			}
		}
		limit := 100
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v > 0 {
				limit = v
			}
		}
		consume := false
		if raw := strings.TrimSpace(r.URL.Query().Get("consume")); raw != "" {
			if raw == "1" || strings.EqualFold(raw, "true") || strings.EqualFold(raw, "yes") {
				consume = true
			}
		}

		messages, ok := s.virtualSDSMessages(issi, sinceSeq, limit, consume)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"issi":     issi,
			"messages": messages,
		})
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Service) handleVirtualSDSSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req uiSDSSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	req.DestinationType = strings.ToLower(strings.TrimSpace(req.DestinationType))
	req.SDSType = strings.ToLower(strings.TrimSpace(req.SDSType))
	if req.SourceISSI == 0 {
		http.Error(w, "source_issi must be > 0", http.StatusBadRequest)
		return
	}
	if req.Destination == 0 {
		http.Error(w, "destination must be > 0", http.StatusBadRequest)
		return
	}
	if !s.hasVirtualSDSEndpoint(req.SourceISSI) {
		http.Error(w, "virtual endpoint source_issi is not registered", http.StatusNotFound)
		return
	}
	switch req.DestinationType {
	case "group", "subscriber":
	default:
		http.Error(w, "destination_type must be 'group' or 'subscriber'", http.StatusBadRequest)
		return
	}

	calloutOpts, err := calloutOptionsFromRequest(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.SDSType == "callout" && s.calloutMgr != nil {
		forcedNumber, forced, err := calloutThreadNumberFromRequest(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if forced {
			calloutOpts.CalloutNumber = forcedNumber
		} else {
			calloutOpts.CalloutNumber = s.calloutMgr.allocateOutboundCalloutNumber(
				req.DestinationType,
				req.Destination,
				req.SourceISSI,
				calloutOpts.Function,
				calloutOpts.MessageType,
				strings.ToLower(strings.TrimSpace(req.CalloutRefMode)),
			)
		}
	}
	payload, err := buildSDSPayloadWithOptions(req.SDSType, req.Text, req.PayloadHex, calloutOpts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	calloutNumberOut := -1
	if req.SDSType == "callout" {
		if callout, ok := parseCalloutPayload(payload); ok {
			calloutNumberOut = int(callout.CalloutNumber)
		}
	}

	callID := uuid.New()
	control := brew.BuildShortData(callID, brew.ShortDataPayload{
		Source:      req.SourceISSI,
		Destination: req.Destination,
		Number:      strings.ToUpper(req.SDSType),
	})
	framePayload := buildSDSFramePayload(req.SourceISSI, req.Destination, payload)
	frame := brew.BuildSDSFrame(callID, uint16(len(payload)*8), framePayload)
	release := brew.BuildCallRelease(callID, 0)

	sendFn := s.server.BroadcastToGroup
	if req.DestinationType == "subscriber" {
		sendFn = func(dest uint32, payload []byte, exclude string) int {
			return s.server.BroadcastToSubscriber(dest, payload, exclude)
		}
	}

	nControl := sendFn(req.Destination, control, "")
	nFrame := sendFn(req.Destination, frame, "")
	nRelease := sendFn(req.Destination, release, "")

	s.maybeStoreVirtualSDSMessage(req.SourceISSI, virtualSDSMessage{
		Direction:   "tx",
		CallID:      callID.String(),
		Source:      req.SourceISSI,
		Destination: req.Destination,
		FrameType:   brew.FrameTypeSDSTransfer,
		Kind:        detectSDSKind(payload),
		PayloadHex:  hex.EncodeToString(payload),
		Text:        decodeSDSText(payload),
		Wrapped:     false,
	})

	s.recordActivity("sds-virtual-tx", fmt.Sprintf("source=%d destination_type=%s destination=%d type=%s recipients=%d", req.SourceISSI, req.DestinationType, req.Destination, req.SDSType, nFrame), map[string]any{
		"source":             req.SourceISSI,
		"destination_type":   req.DestinationType,
		"destination":        req.Destination,
		"type":               req.SDSType,
		"call_id":            callID.String(),
		"payload_hex":        hex.EncodeToString(payload),
		"recipients_control": nControl,
		"recipients_frame":   nFrame,
		"recipients_release": nRelease,
	})

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":                 true,
		"call_id":            callID.String(),
		"callout_number":     calloutNumberOut,
		"recipients_control": nControl,
		"recipients_frame":   nFrame,
		"recipients_release": nRelease,
	})
}

func calloutOptionsFromRequest(req uiSDSSendRequest) (calloutEncodeOptions, error) {
	opts := defaultCalloutEncodeOptions()
	if req.SDSType != "callout" {
		return opts, nil
	}

	if req.CalloutFunction > 0 {
		opts.Function = req.CalloutFunction
	}
	if opts.Function > 15 {
		return opts, fmt.Errorf("callout_function must be between 0 and 15")
	}
	if req.CalloutSeverity > 0 {
		opts.Severity = req.CalloutSeverity
	}
	if opts.Severity > 15 {
		return opts, fmt.Errorf("callout_severity must be between 0 and 15")
	}
	if req.CalloutGroupControl > 3 {
		return opts, fmt.Errorf("callout_group_control must be between 0 and 3")
	}
	opts.GroupControl = req.CalloutGroupControl

	if req.CalloutMessageType > 2 {
		return opts, fmt.Errorf("callout_message_type must be 0 (transfer), 1 (report), or 2 (ack)")
	}
	opts.MessageType = req.CalloutMessageType

	if req.CalloutReportRequested || req.CalloutDeliveryReport > 0 {
		opts.DeliveryReportRequest = 1
	} else {
		opts.DeliveryReportRequest = 0
	}

	if req.CalloutTextCodingScheme > uint8(VISCII) {
		return opts, fmt.Errorf("callout_text_coding_scheme must be between 0 and %d", VISCII)
	}
	opts.TextCodingScheme = req.CalloutTextCodingScheme
	opts.StorageAllowed = req.CalloutStorageAllowed
	// Default to legacy encoding for compatibility; optional v2 via checkbox.
	opts.ExtensionHeader = req.CalloutExtensionHeader
	opts.TimestampControl = req.CalloutTimestampControl
	opts.UserReceiptControl = req.CalloutUserReceiptControl
	opts.TextIsStatus = req.CalloutTextIsStatus
	opts.EndCallout = req.CalloutEnd
	opts.PTTNotAllowed = req.CalloutPTTNotAllowed
	return opts, nil
}

func parseCalloutThreadKey(raw string) (destinationType string, destination uint32, calloutNumber uint8, ok bool) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 3 {
		return "", 0, 0, false
	}

	destinationType = strings.ToLower(strings.TrimSpace(parts[0]))
	if destinationType != destinationTypeGroup && destinationType != destinationTypeSubscriber {
		return "", 0, 0, false
	}
	dest64, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil || dest64 == 0 {
		return "", 0, 0, false
	}
	ref64, err := strconv.ParseUint(strings.TrimSpace(parts[2]), 10, 8)
	if err != nil {
		return "", 0, 0, false
	}
	return destinationType, uint32(dest64), uint8(ref64), true
}

func calloutThreadNumberFromRequest(req uiSDSSendRequest) (uint8, bool, error) {
	raw := strings.TrimSpace(req.CalloutKey)
	if raw == "" {
		return 0, false, nil
	}
	destType, dest, ref, ok := parseCalloutThreadKey(raw)
	if !ok {
		return 0, false, fmt.Errorf("callout_key must be '<destination_type>:<destination>:<reference>'")
	}
	if destType != req.DestinationType || dest != req.Destination {
		return 0, false, fmt.Errorf("callout_key destination does not match destination_type/destination")
	}
	return ref, true, nil
}

const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <title>TETRA Brew Live Console</title>
  <style>
    :root {
      --bg-a: #0b1f2a;
      --bg-b: #132b36;
      --panel: #102533d8;
      --line: #3a6076;
      --text: #f3f6f8;
      --muted: #9eb4c2;
      --accent: #f58f3d;
      --accent-2: #5ed1ff;
      --ok: #4ad188;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      font-family: "Avenir Next", "Segoe UI", "Helvetica Neue", sans-serif;
      color: var(--text);
      background:
        radial-gradient(1200px 500px at 15% -20%, #20495f 5%, transparent 65%),
        radial-gradient(900px 500px at 95% 110%, #1f4744 5%, transparent 70%),
        linear-gradient(135deg, var(--bg-a), var(--bg-b));
      padding: 18px;
    }
    .grid {
      display: grid;
      gap: 14px;
      grid-template-columns: 1.2fr 0.8fr;
    }
    .card {
      border: 1px solid var(--line);
      background: var(--panel);
      border-radius: 14px;
      padding: 14px;
      box-shadow: 0 14px 32px #06111688;
      backdrop-filter: blur(6px);
    }
    h1 { margin: 0 0 4px; font-size: 26px; letter-spacing: 0.3px; }
    h2 { margin: 0 0 10px; font-size: 16px; color: var(--accent-2); text-transform: uppercase; letter-spacing: 1px; }
    .muted { color: var(--muted); font-size: 13px; }
    table { width: 100%; border-collapse: collapse; font-size: 13px; }
    th, td { border-bottom: 1px solid #2d4a5c; padding: 7px 6px; text-align: left; vertical-align: top; }
    th { color: #b7cfdf; font-size: 12px; text-transform: uppercase; letter-spacing: 0.8px; }
    .log {
      height: 360px;
      overflow: auto;
      border: 1px solid #284355;
      border-radius: 10px;
      padding: 8px;
      background: #081823b5;
      font-family: "IBM Plex Mono", "Consolas", monospace;
      font-size: 12px;
    }
    .log-line { margin: 0 0 6px; white-space: pre-wrap; word-break: break-word; }
    .k { color: var(--accent); }
    .ok { color: var(--ok); }
    form { display: grid; gap: 10px; }
    .row { display: grid; gap: 8px; grid-template-columns: repeat(2, minmax(0, 1fr)); }
    label { display: grid; gap: 5px; font-size: 12px; color: #c7dae4; text-transform: uppercase; letter-spacing: .6px; }
    input, select, textarea, button {
      border-radius: 10px;
      border: 1px solid #466478;
      padding: 10px;
      color: var(--text);
      background: #0a1d29;
      font: inherit;
    }
    textarea { min-height: 84px; resize: vertical; }
    button {
      background: linear-gradient(120deg, var(--accent), #f6ad70);
      color: #1e1a14;
      font-weight: 700;
      letter-spacing: .4px;
      cursor: pointer;
      border: 0;
    }
    .subtle-box {
      border: 1px solid #2b4a60;
      border-radius: 10px;
      padding: 10px;
      background: #0a1a24a3;
    }
    .subtle-title {
      margin: 0 0 8px;
      color: #8ecff0;
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: .9px;
    }
	    .status { font-size: 13px; color: var(--muted); }
	    .btn-row { display: flex; gap: 8px; flex-wrap: wrap; }
	    .btn-secondary {
	      background: linear-gradient(120deg, #3d88b0, #63b0d7);
	      color: #09141a;
	    }
	    .btn-danger {
	      background: linear-gradient(120deg, #d35a52, #ea8a5a);
	      color: #1b120f;
	    }
	    .flow-step {
	      border: 1px solid #2f5870;
	      border-radius: 10px;
	      padding: 8px;
	      background: #0a202dc2;
	    }
	    .flow-step-title {
	      color: #d2ebfa;
	      font-size: 12px;
	      text-transform: uppercase;
	      letter-spacing: .9px;
	      margin-bottom: 6px;
	    }
	    .flow-mini {
	      font-size: 12px;
	      color: #9eb4c2;
	    }
	    .flow-replies {
	      max-height: 180px;
	      overflow: auto;
	      border: 1px solid #284355;
	      border-radius: 10px;
	      background: #081823b5;
	      padding: 8px;
	    }
	    .flow-replies table { font-size: 12px; }
	    @media (max-width: 980px) { .grid { grid-template-columns: 1fr; } }
	  </style>
</head>
<body>
  <div class="card" style="margin-bottom:14px">
    <h1>TETRA Brew Live Console</h1>
    <div class="muted">Callout-focused console: live subscriber/group state, callout lifecycle tracking, and SDS tools.</div>
    <div id="sync" class="status" style="margin-top:6px">Waiting for first sync...</div>
  </div>

  <div class="grid">
    <div class="card">
      <h2>Subscribers</h2>
      <table id="subscribersTable">
        <thead><tr><th>ISSI</th><th>Groups</th><th>Session</th><th>Remote</th></tr></thead>
        <tbody></tbody>
      </table>
      <h2 style="margin-top:14px">Groups</h2>
      <table id="groupsTable">
        <thead><tr><th>GSSI</th><th>Subscribers</th><th>Sessions</th></tr></thead>
        <tbody></tbody>
      </table>
	      <h2 style="margin-top:14px">Callout States</h2>
	      <table id="calloutsTable">
	        <thead><tr><th>Target</th><th>Src/Reply</th><th>No</th><th>Fn</th><th>Ref</th><th>State</th><th>Text</th><th>Last</th></tr></thead>
	        <tbody></tbody>
	      </table>
	      <h2 style="margin-top:14px">Virtual SDS Endpoints</h2>
	      <table id="virtualEndpointsTable">
	        <thead><tr><th>ISSI</th><th>Name</th><th>Queued</th><th>RX/TX</th><th>Last Kind</th></tr></thead>
	        <tbody></tbody>
	      </table>
	      <h2 style="margin-top:14px">Activity Log</h2>
	      <div id="activity" class="log"></div>
	    </div>

	    <div class="card">
	      <h2>Callout Flow</h2>
	      <div class="subtle-box" style="margin-bottom:12px">
	        <div class="subtle-title">Guided Group Flow</div>
	        <div class="flow-step">
	          <div class="flow-step-title">Thread</div>
	          <label>Active Thread
	            <select id="activeCallout">
	              <option value="">(new callout thread)</option>
	            </select>
	          </label>
	          <div class="row">
	            <label>Reference Strategy
	              <select id="flowRefMode">
	                <option value="organized">Organized (recommended)</option>
	                <option value="random">Random</option>
	                <option value="reuse">Reuse selected</option>
	              </select>
	            </label>
	            <label>Current Reference
	              <input id="flowReferenceView" type="text" value="n/a" readonly />
	            </label>
	          </div>
	          <div id="flowThread" class="flow-mini">No active thread selected. Step 1 will create a callout thread.</div>
	        </div>
	        <div class="flow-step" style="margin-top:8px">
	          <div class="flow-step-title">Responses</div>
	          <div id="flowResponsesSummary" class="flow-mini">Responses will appear here after alerting.</div>
	          <div class="flow-replies" style="margin-top:6px">
	            <table id="flowRepliesTable">
	              <thead><tr><th>Responder</th><th>State</th><th>Text</th><th>Last</th></tr></thead>
	              <tbody></tbody>
	            </table>
	          </div>
	        </div>
	        <div class="btn-row" style="margin-top:10px">
	          <button id="flowStartBtn" type="button" class="btn-secondary">1. Alert Group</button>
	          <button id="flowMessageBtn" type="button" class="btn-secondary">2. Send Group Message</button>
	          <button id="flowClearBtn" type="button" class="btn-danger">3. Exit Callout</button>
	          <button id="flowLoadBtn" type="button">Load Thread -> Form</button>
	        </div>
	        <div id="flowStatus" class="status" style="margin-top:8px">Set destination/text then send Step 1.</div>
	      </div>
	      <h2>Callout / SDS</h2>
	      <form id="sdsForm">
        <div class="row">
          <label>Destination Type
            <select id="destinationType">
              <option value="subscriber">Subscriber</option>
              <option value="group">Group</option>
            </select>
          </label>
          <label>Destination
            <input id="destination" type="number" min="1" step="1" required />
          </label>
        </div>
        <label>Destination Picker
          <select id="destinationPreset">
            <option value="">(manual entry)</option>
          </select>
        </label>
        <div class="row">
          <label>SDS Type
            <select id="sdsType">
              <option value="callout">CallOut</option>
              <option value="flash">FlashSDS</option>
              <option value="home_indicator">HomeIndicator</option>
              <option value="wap_push">WAP-Push</option>
            </select>
          </label>
          <label>Source ISSI (optional)
            <input id="sourceISSI" type="number" min="0" step="1" placeholder="900000"/>
          </label>
        </div>
        <div id="calloutPanel" class="subtle-box">
          <div class="subtle-title">Callout Options</div>
          <div class="row">
            <label>Callout Reference
              <input type="text" value="Service-assigned (8-bit)" readonly />
            </label>
            <label>Function
              <select id="calloutFunction">
                <option value="1">1 Alert</option>
                <option value="2">2 Test</option>
                <option value="3">3 Info</option>
                <option value="4">4 Clear</option>
                <option value="5">5 Availability</option>
              </select>
            </label>
          </div>
          <div class="row">
            <label>Severity (0-15)
              <input id="calloutSeverity" type="number" min="0" max="15" step="1" value="1" />
            </label>
            <label>Group Control (0-3)
              <input id="calloutGroupControl" type="number" min="0" max="3" step="1" value="0" />
            </label>
          </div>
          <div class="row">
            <label>Message Type
              <select id="calloutMessageType">
                <option value="0">0 Transfer</option>
                <option value="1">1 Report</option>
                <option value="2">2 Ack</option>
              </select>
            </label>
            <label><input id="calloutReportRequested" type="checkbox" /> Report requested</label>
          </div>
          <div class="row">
            <label>Text Coding Scheme
              <select id="calloutTextCodingScheme">
                <option value="0">Packed7Bit (0)</option>
                <option value="1" selected>ISO8859_1 (1)</option>
                <option value="2">ISO8859_2 (2)</option>
                <option value="3">ISO8859_3 (3)</option>
                <option value="4">ISO8859_4 (4)</option>
                <option value="5">ISO8859_5 (5)</option>
                <option value="6">ISO8859_6 (6)</option>
                <option value="7">ISO8859_7 (7)</option>
                <option value="8">ISO8859_8 (8)</option>
                <option value="9">ISO8859_9 (9)</option>
                <option value="10">ISO8859_10 (10)</option>
                <option value="11">ISO8859_13 (11)</option>
                <option value="12">ISO8859_14 (12)</option>
                <option value="13">ISO8859_15 (13)</option>
                <option value="14">CodePage437 (14)</option>
                <option value="15">CodePage737 (15)</option>
                <option value="16">CodePage850 (16)</option>
                <option value="17">CodePage852 (17)</option>
                <option value="18">CodePage855 (18)</option>
                <option value="19">CodePage857 (19)</option>
                <option value="20">CodePage860 (20)</option>
                <option value="21">CodePage861 (21)</option>
                <option value="22">CodePage863 (22)</option>
                <option value="23">CodePage865 (23)</option>
                <option value="24">CodePage866 (24)</option>
                <option value="25">CodePage869 (25)</option>
                <option value="26">UTF16BE (26)</option>
                <option value="27">VISCII (27)</option>
              </select>
            </label>
            <label>SDS Reference
              <input type="text" value="auto-increment" readonly />
            </label>
          </div>
          <div class="row">
            <label><input id="calloutStorageAllowed" type="checkbox" /> Storage allowed</label>
            <label><input id="calloutExtensionHeader" type="checkbox" /> Extension header used</label>
          </div>
          <div class="row">
            <label><input id="calloutTimestampControl" type="checkbox" /> Timestamp control</label>
            <label><input id="calloutUserReceiptControl" type="checkbox" /> User receipt control</label>
          </div>
          <div class="row">
            <label>CallOut Text Control
              <select id="calloutTextControl">
                <option value="0" selected>0 Free Text</option>
                <option value="1">1 Status</option>
              </select>
            </label>
            <label><input id="calloutPTTNotAllowed" type="checkbox" /> PTT not allowed</label>
          </div>
          <div class="row">
            <label><input id="calloutEnd" type="checkbox" /> End callout</label>
            <label></label>
          </div>
        </div>
        <label>Text Payload
          <textarea id="textPayload" placeholder="Human-readable payload"></textarea>
        </label>
        <label>Raw Payload Hex (optional; overrides text/type)
          <textarea id="hexPayload" placeholder="82deadbeef..."></textarea>
        </label>
        <button type="submit">Send SDS</button>
        <div id="sendStatus" class="status"></div>
      </form>
    </div>
  </div>

  <script>
	    let lastSeq = 0;
	    const activityEl = document.getElementById("activity");
	    const syncEl = document.getElementById("sync");
	    const sendStatusEl = document.getElementById("sendStatus");
	    const sdsTypeEl = document.getElementById("sdsType");
	    const calloutPanelEl = document.getElementById("calloutPanel");
	    const activeCalloutEl = document.getElementById("activeCallout");
	    const flowStatusEl = document.getElementById("flowStatus");
	    const flowThreadEl = document.getElementById("flowThread");
	    const flowRefModeEl = document.getElementById("flowRefMode");
	    const flowReferenceViewEl = document.getElementById("flowReferenceView");
	    const flowResponsesSummaryEl = document.getElementById("flowResponsesSummary");
	    const destinationTypeEl = document.getElementById("destinationType");
	    const destinationEl = document.getElementById("destination");
	    const destinationPresetEl = document.getElementById("destinationPreset");
	    let latestCallouts = [];
	    let latestGroups = [];
	    let latestSubscribers = [];

    function updateTableRows(tableId, rows) {
      const tbody = document.querySelector("#" + tableId + " tbody");
      tbody.innerHTML = rows.map(function(r) {
        return "<tr>" + r.map(function(c) { return "<td>" + c + "</td>"; }).join("") + "</tr>";
      }).join("");
    }

    function renderActivity(activity) {
      for (const a of activity) {
        const div = document.createElement("div");
        div.className = "log-line";
        const ts = new Date(a.time).toLocaleTimeString();
        const seq = document.createElement("span");
        seq.className = "k";
        seq.textContent = "[" + a.seq + "]";
        const kind = document.createElement("span");
        kind.className = "ok";
        kind.textContent = a.kind;
        div.appendChild(seq);
        div.appendChild(document.createTextNode(" " + ts + " "));
        div.appendChild(kind);
        div.appendChild(document.createTextNode(" " + (a.message || "")));
        activityEl.appendChild(div);
      }
      while (activityEl.childElementCount > 600) {
        activityEl.removeChild(activityEl.firstChild);
      }
      activityEl.scrollTop = activityEl.scrollHeight;
    }

    async function refresh() {
      const res = await fetch("/api/dashboard/snapshot?since_seq=" + lastSeq, {cache: "no-store"});
      if (!res.ok) throw new Error("snapshot failed " + res.status);
      const data = await res.json();
      lastSeq = data.last_seq || lastSeq;
      syncEl.textContent = "Last sync: " + new Date(data.server_time).toLocaleTimeString() + " | Clients: " + data.clients.length + " | Subscribers: " + data.subscribers.length;

      updateTableRows("subscribersTable", data.subscribers.map(s => [
        String(s.issi),
        (s.groups || []).join(", "),
        s.session,
        s.remote
      ]));
	      updateTableRows("groupsTable", data.groups.map(g => [
	        String(g.gssi),
	        String(g.subscribers),
	        String(g.sessions)
	      ]));
	      updateTableRows("calloutsTable", (data.callouts || []).map(c => [
	        formatCalloutTarget(c),
	        String(c.source || 0),
	        String(c.callout_number),
	        c.function_name + " (" + String(c.function) + ")",
	        String(c.message_ref),
	        c.state + " / " + c.last_direction + " / r=" + String(c.responses),
	        c.text || "",
	        new Date(c.updated).toLocaleTimeString()
	      ]));
	      updateTableRows("virtualEndpointsTable", (data.virtual_sds_endpoints || []).map(v => [
	        String(v.issi),
	        v.name || "",
	        String(v.queued || 0),
	        String(v.rx_count || 0) + " / " + String(v.tx_count || 0),
	        v.last_msg_kind || ""
	      ]));
	      latestCallouts = Array.isArray(data.callouts) ? data.callouts : [];
	      latestGroups = Array.isArray(data.groups) ? data.groups : [];
	      latestSubscribers = Array.isArray(data.subscribers) ? data.subscribers : [];
	      refreshDestinationPreset();
	      refreshActiveCalloutSelect();
	      if (Array.isArray(data.activity) && data.activity.length > 0) {
	        renderActivity(data.activity);
	      }
	    }

	    function uniqueSortedNumbers(values) {
	      const set = new Set((values || []).map(v => Number(v || 0)).filter(v => v > 0));
	      return Array.from(set).sort((a, b) => a - b);
	    }

	    function refreshDestinationPreset() {
	      const type = String(destinationTypeEl.value || "subscriber");
	      const current = Number(destinationEl.value || 0);
	      const options = ['<option value="">(manual entry)</option>'];
	      if (type === "group") {
	        const groups = uniqueSortedNumbers((latestGroups || []).map(g => g.gssi));
	        for (const g of groups) {
	          options.push('<option value="' + String(g) + '">group:' + String(g) + '</option>');
	        }
	      } else {
	        const subscribers = uniqueSortedNumbers((latestSubscribers || []).map(s => s.issi));
	        for (const issi of subscribers) {
	          options.push('<option value="' + String(issi) + '">subscriber:' + String(issi) + '</option>');
	        }
	      }
	      destinationPresetEl.innerHTML = options.join("");
	      if (current > 0) {
	        const currentStr = String(current);
	        const found = Array.from(destinationPresetEl.options || []).some(o => o.value === currentStr);
	        destinationPresetEl.value = found ? currentStr : "";
	      } else {
	        destinationPresetEl.value = "";
	      }
	    }

	    function parseCalloutKey(key) {
	      const raw = String(key || "");
	      const parts = raw.split(":");
	      if (parts.length < 3) {
	        return null;
	      }
	      if (parts.length === 3) {
	        return { kind: parts[0], destination: Number(parts[1] || 0), calloutNumber: Number(parts[2] || 0), len: parts.length };
	      }
	      return {
	        kind: parts[0],
	        destination: Number(parts[1] || 0),
	        target: Number(parts[2] || 0),
	        calloutNumber: Number(parts[3] || 0),
	        len: parts.length
	      };
	    }

	    function isSubscriberReplyCallout(c) {
	      if (!c || c.destination_type !== "subscriber") return false;
	      const parsed = parseCalloutKey(c.key);
	      return !!(parsed && parsed.kind === "subscriber" && parsed.len >= 4);
	    }

	    function isCalloutCleared(c) {
	      if (!c) return true;
	      if (c.end_callout) return true;
	      const state = String(c.state || "").toLowerCase();
	      return state === "cleared" || state.startsWith("cleared ");
	    }

	    function flowAnchorCallouts() {
	      return latestCallouts.filter(c => c && c.destination_type && !isCalloutCleared(c) && !isSubscriberReplyCallout(c));
	    }

	    function formatCalloutTarget(c) {
	      if (isSubscriberReplyCallout(c)) {
	        const parsed = parseCalloutKey(c.key);
	        const target = parsed && parsed.target ? parsed.target : 0;
	        return "reply issi:" + String(c.destination || 0) + " -> " + String(target || "?");
	      }
	      return String(c.destination_type || "unknown") + ":" + String(c.destination || 0);
	    }

	    function refreshActiveCalloutSelect() {
	      const previous = activeCalloutEl.value;
	      const active = flowAnchorCallouts();
	      const options = ['<option value="">(new callout thread)</option>'].concat(active.map(c => {
	        const key = String(c.key || "");
	        const label = c.destination_type + ":" + String(c.destination) +
	          " #"+ String(c.callout_number) +
	          " " + String(c.function_name || "") +
	          " r=" + String(c.responses || 0) +
	          " src=" + String(c.source || 0);
	        return '<option value="' + key + '">' + label + '</option>';
	      }));
	      activeCalloutEl.innerHTML = options.join("");
	      if (previous && active.some(c => c.key === previous)) {
	        activeCalloutEl.value = previous;
	      }
	      renderFlowThread();
	    }

	    function selectedCallout() {
	      const key = activeCalloutEl.value || "";
	      if (!key) return null;
	      return flowAnchorCallouts().find(c => c.key === key) || null;
	    }

	    function flowRepliesFor(callout) {
	      if (!callout) return [];
	      const number = Number(callout.callout_number || 0);
	      const destination = Number(callout.destination || 0);
	      const source = Number(callout.source || 0);
	      const threadUpdatedAt = new Date(callout.updated || 0).getTime();
	      return latestCallouts.filter(c => {
	        if (!c) return false;
	        if (c.key === callout.key) return false;
	        if (Number(c.callout_number || 0) !== number) return false;

	        if (!isSubscriberReplyCallout(c)) return false;
	        const parsed = parseCalloutKey(c.key);
	        if (!parsed) return false;
	        if (parsed.target !== 0 && (parsed.target === destination || (source !== 0 && parsed.target === source))) return true;
	        if (source !== 0 && Number(c.source || 0) === source) return true;
	        return false;
	      }).concat(
	        latestCallouts.filter(c => {
	          if (!c) return false;
	          if (c.key === callout.key) return false;
	          if (isSubscriberReplyCallout(c)) return false;
	          if (String(c.destination_type || "") !== "group") return false;
	          if (String(c.last_direction || "") !== "rx") return false;
	          if (Number(c.callout_number || 0) !== number) return false;
	          const updatedAt = new Date(c.updated || 0).getTime();
	          if (threadUpdatedAt > 0 && updatedAt > 0 && updatedAt+3600000 < threadUpdatedAt) return false;
	          if (source !== 0 && Number(c.source || 0) === source) return false;
	          if (destination !== 0 && Number(c.destination || 0) === destination) return true;
	          // Accept cross-group exchange rows that share the reference and arrived recently.
	          return true;
	        })
	      ).filter((c, i, arr) => arr.findIndex(v => v && v.key === c.key) === i).sort((a, b) => new Date(b.updated).getTime() - new Date(a.updated).getTime());
	    }

	    function responderForReplyRow(r) {
	      if (!r) return 0;
	      if (isSubscriberReplyCallout(r)) {
	        return Number(r.destination || 0);
	      }
	      return Number(r.source || 0);
	    }

	    function replyStateLabel(r) {
	      if (!r) return "";
	      const target = String(r.destination_type || "") + ":" + String(r.destination || 0);
	      return String(r.state || "") + " via " + target;
	    }

	    function renderFlowThread() {
	      const c = selectedCallout();
	      if (!c) {
	        flowThreadEl.textContent = "No active thread selected. Step 1 will create a callout thread.";
	        flowResponsesSummaryEl.textContent = "Responses will appear here after alerting.";
	        flowReferenceViewEl.value = "server-assigned";
	        updateTableRows("flowRepliesTable", []);
	        return false;
	      }
	      flowReferenceViewEl.value = "#" + String(c.callout_number || 0);
	      flowThreadEl.textContent =
	        "Thread " + c.destination_type + ":" + c.destination +
	        " ref #" + String(c.callout_number || 0) +
	        " source " + String(c.source || 0);

	      const replies = flowRepliesFor(c);
	      const responders = new Set(replies.map(r => responderForReplyRow(r)).filter(v => v > 0));
	      flowResponsesSummaryEl.textContent =
	        "Responses/exchanges: " + String(replies.length) +
	        " from " + String(responders.size) + " subscriber(s).";
	      updateTableRows("flowRepliesTable", replies.map(r => [
	        String(responderForReplyRow(r) || 0),
	        replyStateLabel(r),
	        String(r.text || ""),
	        new Date(r.updated).toLocaleTimeString()
	      ]));
	    }

	    function setCalloutMode() {
	      sdsTypeEl.value = "callout";
	      refreshCalloutPanel();
	    }

	    function applyCalloutToForm(c, keepText) {
	      if (!c) return;
	      destinationTypeEl.value = c.destination_type || "group";
	      destinationEl.value = Number(c.destination || 0);
	      refreshDestinationPreset();
	      document.getElementById("sourceISSI").value = Number(c.source || 0);
	      document.getElementById("calloutFunction").value = Number(c.function || 1);
	      document.getElementById("calloutSeverity").value = Number(c.severity || 1);
	      document.getElementById("calloutGroupControl").value = Number(c.group_control || 0);
	      document.getElementById("calloutReportRequested").checked = Number(c.delivery_report_request || 0) > 0;
	      document.getElementById("calloutTextCodingScheme").value = Number(c.callout_text_coding_scheme || 1);
	      document.getElementById("calloutTextControl").value = c.text_is_status ? "1" : "0";
	      document.getElementById("calloutPTTNotAllowed").checked = !!c.ptt_not_allowed;
	      if (!keepText && c.text) {
	        document.getElementById("textPayload").value = c.text;
	      }
	      setCalloutMode();
	    }

	    function buildRequestFromForm(extra) {
	      const out = {
	        destination_type: destinationTypeEl.value,
	        destination: Number(destinationEl.value || 0),
	        sds_type: document.getElementById("sdsType").value,
	        text: document.getElementById("textPayload").value || "",
	        payload_hex: document.getElementById("hexPayload").value || "",
	        source_issi: Number(document.getElementById("sourceISSI").value || 0),
	        callout_ref_mode: String(flowRefModeEl.value || "organized"),
	        callout_function: Number(document.getElementById("calloutFunction").value || 1),
	        callout_severity: Number(document.getElementById("calloutSeverity").value || 1),
	        callout_group_control: Number(document.getElementById("calloutGroupControl").value || 0),
	        callout_message_type: Number(document.getElementById("calloutMessageType").value || 0),
	        callout_report_requested: document.getElementById("calloutReportRequested").checked,
	        callout_text_coding_scheme: Number(document.getElementById("calloutTextCodingScheme").value || 1),
	        callout_storage_allowed: document.getElementById("calloutStorageAllowed").checked,
	        callout_extension_header: document.getElementById("calloutExtensionHeader").checked,
	        callout_timestamp_control: document.getElementById("calloutTimestampControl").checked,
	        callout_user_receipt_control: document.getElementById("calloutUserReceiptControl").checked,
	        callout_text_is_status: Number(document.getElementById("calloutTextControl").value || 0) === 1,
	        callout_end: document.getElementById("calloutEnd").checked,
	        callout_ptt_not_allowed: document.getElementById("calloutPTTNotAllowed").checked
	      };
	      if (extra && extra.callout_key) {
	        out.callout_key = String(extra.callout_key);
	      }
	      return out;
	    }

	    async function sendRequest(body) {
	      const res = await fetch("/api/sds/send", {
	        method: "POST",
	        headers: {"Content-Type": "application/json"},
	        body: JSON.stringify(body)
	      });
	      const text = await res.text();
	      if (!res.ok) {
	        throw new Error(text || ("request failed " + res.status));
	      }
	      const out = JSON.parse(text);
	      let info = "SDS sent call=" + out.call_id + " recipients(frame)=" + out.recipients_frame;
	      if (out.source_virtual && out.virtual_endpoint && out.virtual_endpoint.issi) {
	        info += " | reply ISSI=" + out.virtual_endpoint.issi;
	      }
	      sendStatusEl.textContent = info;
	      await refresh();
	      return out;
	    }

	    async function sendSDS(evt) {
	      evt.preventDefault();
	      sendStatusEl.textContent = "Sending...";
	      try {
	        await sendRequest(buildRequestFromForm());
	      } catch (err) {
	        sendStatusEl.textContent = "Error: " + err.message;
	      }
	    }

	    document.getElementById("sdsForm").addEventListener("submit", (evt) => {
	      sendSDS(evt).catch(err => sendStatusEl.textContent = "Error: " + err.message);
	    });
	    destinationTypeEl.addEventListener("change", () => {
	      refreshDestinationPreset();
	      renderFlowThread();
	    });
	    destinationPresetEl.addEventListener("change", () => {
	      const selected = Number(destinationPresetEl.value || 0);
	      if (selected > 0) {
	        destinationEl.value = selected;
	      }
	      renderFlowThread();
	    });
	    activeCalloutEl.addEventListener("change", renderFlowThread);
	    flowRefModeEl.addEventListener("change", renderFlowThread);
	    destinationEl.addEventListener("input", () => {
	      refreshDestinationPreset();
	      renderFlowThread();
	    });
	    document.getElementById("flowLoadBtn").addEventListener("click", () => {
	      const c = selectedCallout();
	      if (!c) {
	        flowStatusEl.textContent = "Select an active callout thread first.";
	        return;
	      }
	      applyCalloutToForm(c, true);
	      flowStatusEl.textContent = "Loaded thread ref #" + c.callout_number + " for " + c.destination_type + ":" + c.destination;
	    });
	    document.getElementById("flowStartBtn").addEventListener("click", async () => {
	      setCalloutMode();
	      const c = selectedCallout();
	      if (c) {
	        applyCalloutToForm(c, true);
	      }
	      document.getElementById("hexPayload").value = "";
	      document.getElementById("calloutMessageType").value = "0";
	      document.getElementById("calloutTextControl").value = "0";
	      document.getElementById("calloutEnd").checked = false;
	      document.getElementById("calloutFunction").value = "1";
	      flowStatusEl.textContent = "Step 1: sending alert (reference assigned by service)...";
	      try {
	        const out = await sendRequest(buildRequestFromForm(c ? { callout_key: c.key } : null));
	        const assignedRef = out && out.callout_number;
	        if (assignedRef !== undefined) {
	          flowStatusEl.textContent = "Step 1 complete: alert sent with reference #" + String(assignedRef) + ".";
	        } else {
	          flowStatusEl.textContent = "Step 1 complete: alert sent.";
	        }
	        renderFlowThread();
	      } catch (err) {
	        flowStatusEl.textContent = "Error: " + err.message;
	      }
	    });
	    document.getElementById("flowMessageBtn").addEventListener("click", async () => {
	      const c = selectedCallout();
	      if (!c) {
	        flowStatusEl.textContent = "Select an active callout thread first.";
	        return;
	      }
	      applyCalloutToForm(c, true);
	      document.getElementById("hexPayload").value = "";
	      document.getElementById("calloutFunction").value = "3";
	      document.getElementById("calloutMessageType").value = "0";
	      document.getElementById("calloutTextControl").value = "0";
	      document.getElementById("calloutEnd").checked = false;
	      const textEl = document.getElementById("textPayload");
	      if (!String(textEl.value || "").trim()) {
	        textEl.value = String(c.text || "INFO");
	      }
	      flowStatusEl.textContent = "Step 2: sending group message on reference #" + String(c.callout_number) + "...";
	      try {
	        await sendRequest(buildRequestFromForm({ callout_key: c.key }));
	        flowStatusEl.textContent = "Step 2 complete: group message sent on same reference.";
	        renderFlowThread();
	      } catch (err) {
	        flowStatusEl.textContent = "Error: " + err.message;
	      }
	    });
	    document.getElementById("flowClearBtn").addEventListener("click", async () => {
	      const c = selectedCallout();
	      if (!c) {
	        flowStatusEl.textContent = "Select an active callout to clear.";
	        return;
	      }
	      applyCalloutToForm(c, true);
	      document.getElementById("hexPayload").value = "";
	      document.getElementById("calloutFunction").value = "4";
	      document.getElementById("calloutMessageType").value = "0";
	      document.getElementById("calloutTextControl").value = "0";
	      document.getElementById("calloutEnd").checked = true;
	      flowStatusEl.textContent = "Step 3: sending clear on reference #" + String(c.callout_number) + "...";
	      try {
	        await sendRequest(buildRequestFromForm({ callout_key: c.key }));
	        activeCalloutEl.value = "";
	        flowStatusEl.textContent = "Step 3 complete: callout exited.";
	        renderFlowThread();
	      } catch (err) {
	        flowStatusEl.textContent = "Error: " + err.message;
	      }
	    });
	    function refreshCalloutPanel() {
	      calloutPanelEl.style.display = sdsTypeEl.value === "callout" ? "grid" : "none";
	    }
    sdsTypeEl.addEventListener("change", refreshCalloutPanel);
    refreshCalloutPanel();

    refresh().catch(err => syncEl.textContent = "Initial sync failed: " + err.message);
    setInterval(() => {
      refresh().catch(err => syncEl.textContent = "Sync error: " + err.message);
    }, 1200);
  </script>
</body>
</html>`
