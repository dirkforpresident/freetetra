package service

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type dashboardCalloutState struct {
	Key                   string    `json:"key"`
	DestinationType       string    `json:"destination_type"`
	Destination           uint32    `json:"destination"`
	Source                uint32    `json:"source"`
	CalloutNumber         uint8     `json:"callout_number"`
	Function              uint8     `json:"function"`
	FunctionName          string    `json:"function_name"`
	Severity              uint8     `json:"severity"`
	GroupControl          uint8     `json:"group_control"`
	TimestampControl      bool      `json:"timestamp_control"`
	UserReceiptControl    bool      `json:"user_receipt_control"`
	TextIsStatus          bool      `json:"text_is_status"`
	EndCallout            bool      `json:"end_callout"`
	PTTNotAllowed         bool      `json:"ptt_not_allowed"`
	MessageType           uint8     `json:"message_type"`
	MessageRef            uint8     `json:"message_ref"`
	DeliveryReportRequest uint8     `json:"delivery_report_request"`
	ServiceSelection      bool      `json:"service_selection"`
	Storage               bool      `json:"storage"`
	Text                  string    `json:"text"`
	LastDirection         string    `json:"last_direction"`
	LastSession           string    `json:"last_session,omitempty"`
	State                 string    `json:"state"`
	Responses             int       `json:"responses"`
	Updated               time.Time `json:"updated"`
}

func calloutFunctionName(fn uint8) string {
	switch fn {
	case 1:
		return "alert"
	case 2:
		return "test"
	case 3:
		return "info"
	case 4:
		return "clear"
	case 5:
		return "availability"
	default:
		return fmt.Sprintf("fn_%d", fn)
	}
}

func calloutStateLabel(c calloutMessage) string {
	if c.EndCallout || c.Function == 4 {
		return "cleared"
	}
	switch c.MessageType {
	case 0:
		return "pending"
	case 1:
		return "report"
	case 2:
		return "ack"
	default:
		return fmt.Sprintf("msg_type_%d", c.MessageType)
	}
}

func calloutStateKey(destinationType string, destination uint32, calloutNumber uint8) string {
	return fmt.Sprintf("%s:%d:%d", destinationType, destination, calloutNumber)
}

func calloutSubscriberReplyKey(subscriber, target uint32, calloutNumber uint8) string {
	return fmt.Sprintf("%s:%d:%d:%d", destinationTypeSubscriber, subscriber, target, calloutNumber)
}

func isCalloutReplyMessage(c calloutMessage) bool {
	return c.MessageType == 1 || c.MessageType == 2
}

func (s *Service) noteCalloutTx(destinationType string, destination, source uint32, c calloutMessage) string {
	now := time.Now().UTC()
	key := calloutStateKey(destinationType, destination, c.CalloutNumber)
	if s.calloutMgr != nil {
		s.calloutMgr.noteTx(destinationType, destination, source, c, now)
	}

	s.calloutMu.Lock()
	defer s.calloutMu.Unlock()

	st, ok := s.calloutStates[key]
	if !ok {
		st = &dashboardCalloutState{
			Key:             key,
			DestinationType: destinationType,
			Destination:     destination,
			Responses:       0,
		}
		s.calloutStates[key] = st
	}

	st.Source = source
	st.CalloutNumber = c.CalloutNumber
	st.Function = c.Function
	st.FunctionName = calloutFunctionName(c.Function)
	st.Severity = c.Severity
	st.GroupControl = c.GroupControl
	st.TimestampControl = c.TimestampControl
	st.UserReceiptControl = c.UserReceiptControl
	st.TextIsStatus = c.TextIsStatus
	st.EndCallout = c.EndCallout
	st.PTTNotAllowed = c.PTTNotAllowed
	st.MessageType = c.MessageType
	st.MessageRef = c.MessageRef
	st.DeliveryReportRequest = c.DeliveryReportRequest
	st.ServiceSelection = c.ServiceSelection
	st.Storage = c.Storage
	st.Text = c.Text
	st.LastDirection = "tx"
	st.State = calloutStateLabel(c)
	st.Updated = now
	return key
}

func (s *Service) noteCalloutRx(session string, env sdsFrameEnvelope, c calloutMessage) string {
	now := time.Now().UTC()
	if s.calloutMgr != nil {
		s.calloutMgr.noteRx(env, c, session, now)
	}

	s.calloutMu.Lock()
	defer s.calloutMu.Unlock()

	resolvedKey := ""
	if env.Destination != 0 {
		groupKey := calloutStateKey(destinationTypeGroup, env.Destination, c.CalloutNumber)
		if _, ok := s.calloutStates[groupKey]; ok {
			resolvedKey = groupKey
		}
	}
	if env.Source != 0 {
		direct := calloutStateKey(destinationTypeSubscriber, env.Source, c.CalloutNumber)
		if _, ok := s.calloutStates[direct]; ok {
			resolvedKey = direct
		}
	}
	if resolvedKey == "" && env.Destination != 0 {
		direct := calloutStateKey(destinationTypeSubscriber, env.Destination, c.CalloutNumber)
		if _, ok := s.calloutStates[direct]; ok {
			resolvedKey = direct
		}
	}
	if resolvedKey == "" {
		var latest time.Time
		for k, st := range s.calloutStates {
			if st.CalloutNumber != c.CalloutNumber {
				continue
			}
			if env.Destination != 0 && st.Source == env.Destination {
				if st.Updated.After(latest) {
					latest = st.Updated
					resolvedKey = k
				}
				continue
			}
			if env.Destination != 0 && st.Destination == env.Destination {
				if st.Updated.After(latest) {
					latest = st.Updated
					resolvedKey = k
				}
				continue
			}
			if env.Source != 0 && st.Destination == env.Source {
				if st.Updated.After(latest) {
					latest = st.Updated
					resolvedKey = k
				}
			}
		}
	}
	if resolvedKey == "" {
		if env.Destination != 0 {
			resolvedKey = calloutStateKey(destinationTypeGroup, env.Destination, c.CalloutNumber)
		} else if env.Source != 0 {
			resolvedKey = calloutStateKey(destinationTypeSubscriber, env.Source, c.CalloutNumber)
		} else {
			resolvedKey = fmt.Sprintf("rx:unknown:%d", c.CalloutNumber)
		}
	}
	isSubscriberReply := isCalloutReplyMessage(c) && env.Source != 0
	key := resolvedKey
	if isSubscriberReply {
		key = calloutSubscriberReplyKey(env.Source, env.Destination, c.CalloutNumber)
	}

	st, ok := s.calloutStates[key]
	if !ok {
		destType := destinationTypeSubscriber
		dest := env.Source
		if strings.HasPrefix(key, destinationTypeGroup+":") {
			destType = destinationTypeGroup
			dest = env.Destination
		} else if env.Source == 0 {
			dest = env.Destination
		}
		st = &dashboardCalloutState{
			Key:             key,
			DestinationType: destType,
			Destination:     dest,
		}
		if isSubscriberReply {
			if parent, ok := s.calloutStates[resolvedKey]; ok && parent != nil && parent.Source != 0 {
				st.Source = parent.Source
			} else if env.Destination != 0 {
				st.Source = env.Destination
			}
		}
		s.calloutStates[key] = st
	}
	if isSubscriberReply && resolvedKey != "" && resolvedKey != key {
		if parent, ok := s.calloutStates[resolvedKey]; ok && parent != nil {
			parent.Responses++
			parent.Updated = now
		}
	}

	if st.Destination == 0 {
		if strings.HasPrefix(key, destinationTypeGroup+":") && env.Destination != 0 {
			st.Destination = env.Destination
		} else if env.Source != 0 {
			st.Destination = env.Source
		} else if env.Destination != 0 {
			st.Destination = env.Destination
		}
	}
	if env.Source != 0 && st.Source == 0 {
		if isSubscriberReply {
			if env.Destination != 0 {
				st.Source = env.Destination
			} else {
				st.Source = env.Source
			}
		} else {
			st.Source = env.Source
		}
	}
	st.CalloutNumber = c.CalloutNumber
	st.Function = c.Function
	st.FunctionName = calloutFunctionName(c.Function)
	st.Severity = c.Severity
	st.GroupControl = c.GroupControl
	st.TimestampControl = c.TimestampControl
	st.UserReceiptControl = c.UserReceiptControl
	st.TextIsStatus = c.TextIsStatus
	st.EndCallout = c.EndCallout
	st.PTTNotAllowed = c.PTTNotAllowed
	st.MessageType = c.MessageType
	st.MessageRef = c.MessageRef
	st.DeliveryReportRequest = c.DeliveryReportRequest
	st.ServiceSelection = c.ServiceSelection
	st.Storage = c.Storage
	if c.Text != "" {
		st.Text = c.Text
	}
	st.LastDirection = "rx"
	st.LastSession = session
	st.State = calloutStateLabel(c)
	st.Responses++
	st.Updated = now
	return key
}

func (s *Service) snapshotCallouts() []dashboardCalloutState {
	s.calloutMu.RLock()
	defer s.calloutMu.RUnlock()
	if len(s.calloutStates) == 0 {
		return nil
	}
	out := make([]dashboardCalloutState, 0, len(s.calloutStates))
	for _, st := range s.calloutStates {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Updated.After(out[j].Updated)
	})
	return out
}
