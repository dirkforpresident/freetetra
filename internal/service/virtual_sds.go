package service

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	destinationTypeGroup      = "group"
	destinationTypeSubscriber = "subscriber"
	virtualSDSMessageLimit    = 400
	calloutVirtualISSIBase    = 970000
	calloutVirtualISSISpan    = 10000
)

type virtualSDSMessage struct {
	Seq         int64     `json:"seq"`
	Time        time.Time `json:"time"`
	Direction   string    `json:"direction"`
	CallID      string    `json:"call_id,omitempty"`
	Session     string    `json:"session,omitempty"`
	Source      uint32    `json:"source"`
	Destination uint32    `json:"destination"`
	FrameType   uint8     `json:"frame_type"`
	Kind        string    `json:"kind"`
	PayloadHex  string    `json:"payload_hex"`
	Text        string    `json:"text,omitempty"`
	Wrapped     bool      `json:"wrapped"`
}

type dashboardVirtualSDSEndpoint struct {
	ISSI        uint32     `json:"issi"`
	Name        string     `json:"name"`
	Created     time.Time  `json:"created"`
	Updated     time.Time  `json:"updated"`
	LastRX      *time.Time `json:"last_rx,omitempty"`
	LastTX      *time.Time `json:"last_tx,omitempty"`
	RXCount     uint64     `json:"rx_count"`
	TXCount     uint64     `json:"tx_count"`
	Queued      int        `json:"queued"`
	LastMsgSeq  int64      `json:"last_msg_seq"`
	LastMsgKind string     `json:"last_msg_kind,omitempty"`
}

type virtualSDSEndpointState struct {
	ISSI     uint32
	Name     string
	Created  time.Time
	Updated  time.Time
	LastRX   time.Time
	LastTX   time.Time
	RXCount  uint64
	TXCount  uint64
	Messages []virtualSDSMessage
}

func (s *Service) upsertVirtualSDSEndpoint(issi uint32, name string) (dashboardVirtualSDSEndpoint, bool) {
	now := time.Now().UTC()
	s.virtualSDSMu.Lock()
	defer s.virtualSDSMu.Unlock()

	ep, exists := s.virtualSDSEndpoints[issi]
	if !exists {
		ep = &virtualSDSEndpointState{
			ISSI:    issi,
			Created: now,
		}
		s.virtualSDSEndpoints[issi] = ep
	}
	ep.Name = normalizeVirtualSDSName(name, issi)
	ep.Updated = now
	return ep.snapshot(), !exists
}

func (s *Service) ensureGroupCalloutVirtualEndpoint(gssi uint32) (uint32, dashboardVirtualSDSEndpoint, bool) {
	now := time.Now().UTC()
	name := fmt.Sprintf("callout-group-%d", gssi)

	s.virtualSDSMu.Lock()
	defer s.virtualSDSMu.Unlock()
	if s.groupCalloutVirtual == nil {
		s.groupCalloutVirtual = make(map[uint32]uint32)
	}
	if s.virtualSDSEndpoints == nil {
		s.virtualSDSEndpoints = make(map[uint32]*virtualSDSEndpointState)
	}

	if issi, ok := s.groupCalloutVirtual[gssi]; ok && issi != 0 {
		ep := s.virtualSDSEndpoints[issi]
		if ep == nil {
			ep = &virtualSDSEndpointState{
				ISSI:    issi,
				Created: now,
			}
			s.virtualSDSEndpoints[issi] = ep
		}
		ep.Name = name
		ep.Updated = now
		return issi, ep.snapshot(), false
	}

	occupiedByGroup := make(map[uint32]uint32, len(s.groupCalloutVirtual))
	for group, issi := range s.groupCalloutVirtual {
		if group == gssi || issi == 0 {
			continue
		}
		occupiedByGroup[issi] = group
	}

	start := gssi % calloutVirtualISSISpan
	for i := uint32(0); i < calloutVirtualISSISpan; i++ {
		offset := (start + i) % calloutVirtualISSISpan
		candidate := calloutVirtualISSIBase + offset
		if _, taken := occupiedByGroup[candidate]; taken {
			continue
		}

		ep := s.virtualSDSEndpoints[candidate]
		if ep != nil && !strings.HasPrefix(ep.Name, "callout-group-") {
			continue
		}
		created := false
		if ep == nil {
			ep = &virtualSDSEndpointState{
				ISSI:    candidate,
				Created: now,
			}
			s.virtualSDSEndpoints[candidate] = ep
			created = true
		}
		ep.Name = name
		ep.Updated = now
		s.groupCalloutVirtual[gssi] = candidate
		return candidate, ep.snapshot(), created
	}

	// Fallback: stable ISSI outside preferred pool to avoid hard failure.
	candidate := calloutVirtualISSIBase + calloutVirtualISSISpan + (gssi % calloutVirtualISSISpan)
	ep := s.virtualSDSEndpoints[candidate]
	created := false
	if ep == nil {
		ep = &virtualSDSEndpointState{
			ISSI:    candidate,
			Created: now,
		}
		s.virtualSDSEndpoints[candidate] = ep
		created = true
	}
	ep.Name = name
	ep.Updated = now
	s.groupCalloutVirtual[gssi] = candidate
	return candidate, ep.snapshot(), created
}

func (s *Service) deleteVirtualSDSEndpoint(issi uint32) bool {
	s.virtualSDSMu.Lock()
	defer s.virtualSDSMu.Unlock()
	if _, ok := s.virtualSDSEndpoints[issi]; !ok {
		return false
	}
	delete(s.virtualSDSEndpoints, issi)
	return true
}

func (s *Service) hasVirtualSDSEndpoint(issi uint32) bool {
	s.virtualSDSMu.RLock()
	defer s.virtualSDSMu.RUnlock()
	_, ok := s.virtualSDSEndpoints[issi]
	return ok
}

func (s *Service) virtualSDSNumbers() []uint32 {
	s.virtualSDSMu.RLock()
	defer s.virtualSDSMu.RUnlock()

	out := make([]uint32, 0, len(s.virtualSDSEndpoints))
	for issi := range s.virtualSDSEndpoints {
		out = append(out, issi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (s *Service) snapshotVirtualSDSEndpoints() []dashboardVirtualSDSEndpoint {
	s.virtualSDSMu.RLock()
	defer s.virtualSDSMu.RUnlock()

	out := make([]dashboardVirtualSDSEndpoint, 0, len(s.virtualSDSEndpoints))
	for _, ep := range s.virtualSDSEndpoints {
		out = append(out, ep.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ISSI < out[j].ISSI })
	return out
}

func (s *Service) maybeStoreVirtualSDSMessage(issi uint32, msg virtualSDSMessage) bool {
	if issi == 0 {
		return false
	}

	s.virtualSDSMu.Lock()
	defer s.virtualSDSMu.Unlock()

	ep := s.virtualSDSEndpoints[issi]
	if ep == nil {
		return false
	}

	now := time.Now().UTC()
	s.virtualSDSSeq++
	msg.Seq = s.virtualSDSSeq
	if msg.Time.IsZero() {
		msg.Time = now
	}
	if strings.TrimSpace(msg.Direction) == "" {
		msg.Direction = "rx"
	}

	ep.Messages = append(ep.Messages, msg)
	if len(ep.Messages) > virtualSDSMessageLimit {
		ep.Messages = append([]virtualSDSMessage(nil), ep.Messages[len(ep.Messages)-virtualSDSMessageLimit:]...)
	}
	ep.Updated = now
	if msg.Direction == "tx" {
		ep.LastTX = now
		ep.TXCount++
	} else {
		ep.LastRX = now
		ep.RXCount++
	}
	return true
}

func (s *Service) virtualSDSMessages(issi uint32, sinceSeq int64, limit int, consume bool) ([]virtualSDSMessage, bool) {
	if limit <= 0 {
		limit = virtualSDSMessageLimit
	}
	if limit > virtualSDSMessageLimit {
		limit = virtualSDSMessageLimit
	}

	if !consume {
		s.virtualSDSMu.RLock()
		defer s.virtualSDSMu.RUnlock()
		ep := s.virtualSDSEndpoints[issi]
		if ep == nil {
			return nil, false
		}
		return filterVirtualMessages(ep.Messages, sinceSeq, limit), true
	}

	s.virtualSDSMu.Lock()
	defer s.virtualSDSMu.Unlock()
	ep := s.virtualSDSEndpoints[issi]
	if ep == nil {
		return nil, false
	}

	out := make([]virtualSDSMessage, 0, limit)
	remaining := make([]virtualSDSMessage, 0, len(ep.Messages))
	for _, msg := range ep.Messages {
		if msg.Seq <= sinceSeq || len(out) >= limit {
			remaining = append(remaining, msg)
			continue
		}
		out = append(out, msg)
	}
	ep.Messages = remaining
	ep.Updated = time.Now().UTC()
	return out, true
}

func filterVirtualMessages(messages []virtualSDSMessage, sinceSeq int64, limit int) []virtualSDSMessage {
	out := make([]virtualSDSMessage, 0, limit)
	for _, msg := range messages {
		if msg.Seq <= sinceSeq {
			continue
		}
		out = append(out, msg)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func normalizeVirtualSDSName(name string, issi uint32) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Sprintf("virtual-%d", issi)
	}
	return name
}

func (ep *virtualSDSEndpointState) snapshot() dashboardVirtualSDSEndpoint {
	out := dashboardVirtualSDSEndpoint{
		ISSI:    ep.ISSI,
		Name:    ep.Name,
		Created: ep.Created,
		Updated: ep.Updated,
		RXCount: ep.RXCount,
		TXCount: ep.TXCount,
		Queued:  len(ep.Messages),
	}
	if !ep.LastRX.IsZero() {
		v := ep.LastRX
		out.LastRX = &v
	}
	if !ep.LastTX.IsZero() {
		v := ep.LastTX
		out.LastTX = &v
	}
	if n := len(ep.Messages); n > 0 {
		out.LastMsgSeq = ep.Messages[n-1].Seq
		out.LastMsgKind = ep.Messages[n-1].Kind
	}
	return out
}
