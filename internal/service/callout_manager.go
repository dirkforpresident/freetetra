package service

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type calloutThreadPhase string

const (
	calloutThreadDraft    calloutThreadPhase = "draft"
	calloutThreadAlerted  calloutThreadPhase = "alerted"
	calloutThreadActive   calloutThreadPhase = "active"
	calloutThreadClearing calloutThreadPhase = "clearing"
	calloutThreadCleared  calloutThreadPhase = "cleared"
)

type calloutResponder struct {
	ISSI          uint32
	LastMessage   uint8
	LastState     string
	LastText      string
	LastSeen      time.Time
	ResponseCount int
}

type calloutThread struct {
	DestinationType string
	Destination     uint32
	Source          uint32
	CalloutNumber   uint8

	Phase        calloutThreadPhase
	LastCallout  calloutMessage
	LastDirection string
	LastSession  string
	Created      time.Time
	Updated      time.Time
	Responses    int

	Responders map[uint32]*calloutResponder
}

type calloutManager struct {
	mu sync.RWMutex

	seq atomic.Uint32
	// Active callout number for a destination/source route.
	activeByRoute map[string]uint8
	// Thread state keyed by route+callout-number.
	threads map[string]*calloutThread
}

func newCalloutManager() *calloutManager {
	return &calloutManager{
		activeByRoute: make(map[string]uint8),
		threads:       make(map[string]*calloutThread),
	}
}

func calloutRouteKey(destinationType string, destination, source uint32) string {
	return fmt.Sprintf("%s:%d:%d", destinationType, destination, source)
}

func calloutThreadKey(destinationType string, destination, source uint32, calloutNumber uint8) string {
	return fmt.Sprintf("%s:%d:%d:%d", destinationType, destination, source, calloutNumber)
}

func (m *calloutManager) nextSequentialNoLock() uint8 {
	return uint8(m.seq.Add(1))
}

func randomUint8() uint8 {
	var b [1]byte
	if _, err := rand.Read(b[:]); err == nil {
		return b[0]
	}
	// Fallback path should be extremely rare; keep deterministic behavior.
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(time.Now().UnixNano()))
	return n[0]
}

// allocateOutboundCalloutNumber picks a callout number for outgoing callout messages.
// It treats follow-up/clear messages as part of an existing route thread when available.
func (m *calloutManager) allocateOutboundCalloutNumber(
	destinationType string,
	destination, source uint32,
	function, messageType uint8,
	refMode string,
) uint8 {
	route := calloutRouteKey(destinationType, destination, source)

	m.mu.Lock()
	defer m.mu.Unlock()

	current, hasCurrent := m.activeByRoute[route]
	mode := refMode
	if mode == "" {
		mode = "organized"
	}

	// Follow-up/clear/report/ack should stay on the active route when possible.
	if hasCurrent && (mode == "reuse" || function == 3 || function == 4 || messageType == 1 || messageType == 2) {
		if function == 4 {
			delete(m.activeByRoute, route)
		}
		return current
	}

	var number uint8
	switch mode {
	case "random":
		number = randomUint8()
	default:
		number = m.nextSequentialNoLock()
	}

	if function != 4 {
		m.activeByRoute[route] = number
	}
	return number
}

func phaseFromCallout(c calloutMessage) calloutThreadPhase {
	if c.EndCallout || c.Function == 4 {
		return calloutThreadCleared
	}
	if c.MessageType == 1 || c.MessageType == 2 {
		return calloutThreadActive
	}
	if c.Function == 1 {
		return calloutThreadAlerted
	}
	if c.Function == 3 {
		return calloutThreadActive
	}
	return calloutThreadDraft
}

func (m *calloutManager) noteTx(destinationType string, destination, source uint32, c calloutMessage, now time.Time) {
	if destinationType == "" || destination == 0 {
		return
	}
	route := calloutRouteKey(destinationType, destination, source)
	threadKey := calloutThreadKey(destinationType, destination, source, c.CalloutNumber)

	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.threads[threadKey]
	if !ok {
		t = &calloutThread{
			DestinationType: destinationType,
			Destination:     destination,
			Source:          source,
			CalloutNumber:   c.CalloutNumber,
			Created:         now,
			Responders:      make(map[uint32]*calloutResponder),
		}
		m.threads[threadKey] = t
	}
	t.LastCallout = c
	t.LastDirection = "tx"
	t.LastSession = ""
	t.Phase = phaseFromCallout(c)
	t.Updated = now
	if source != 0 {
		t.Source = source
	}

	if c.EndCallout || c.Function == 4 {
		delete(m.activeByRoute, route)
	} else {
		m.activeByRoute[route] = c.CalloutNumber
	}
}

func (m *calloutManager) noteRx(env sdsFrameEnvelope, c calloutMessage, session string, now time.Time) {
	if c.CalloutNumber == 0 && env.Source == 0 && env.Destination == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Try to match an existing thread by callout number and common route hints.
	var target *calloutThread
	for _, t := range m.threads {
		if t.CalloutNumber != c.CalloutNumber {
			continue
		}
		if env.Destination != 0 && (t.Destination == env.Destination || t.Source == env.Destination) {
			if target == nil || t.Updated.After(target.Updated) {
				target = t
			}
			continue
		}
		if env.Source != 0 && t.Destination == env.Source {
			if target == nil || t.Updated.After(target.Updated) {
				target = t
			}
		}
	}

	// Create fallback thread when no route match exists yet.
	if target == nil {
		destinationType := destinationTypeGroup
		destination := env.Destination
		source := env.Source
		if destination == 0 {
			destinationType = destinationTypeSubscriber
			destination = env.Source
			source = env.Destination
		}
		threadKey := calloutThreadKey(destinationType, destination, source, c.CalloutNumber)
		target = &calloutThread{
			DestinationType: destinationType,
			Destination:     destination,
			Source:          source,
			CalloutNumber:   c.CalloutNumber,
			Created:         now,
			Responders:      make(map[uint32]*calloutResponder),
		}
		m.threads[threadKey] = target
	}

	target.LastCallout = c
	target.LastDirection = "rx"
	target.LastSession = session
	target.Phase = phaseFromCallout(c)
	target.Updated = now
	target.Responses++
	if env.Destination != 0 && target.Destination == 0 {
		target.Destination = env.Destination
	}
	if target.Source == 0 && env.Destination != 0 {
		target.Source = env.Destination
	}

	responderISSI := env.Source
	if responderISSI != 0 {
		r := target.Responders[responderISSI]
		if r == nil {
			r = &calloutResponder{ISSI: responderISSI}
			target.Responders[responderISSI] = r
		}
		r.LastMessage = c.MessageType
		r.LastState = calloutStateLabel(c)
		r.LastText = c.Text
		r.LastSeen = now
		r.ResponseCount++
	}
}

func (m *calloutManager) latestGroupContext(source, group uint32) (dashboardCalloutState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var (
		found bool
		best  dashboardCalloutState
	)
	for _, t := range m.threads {
		if t.DestinationType != destinationTypeGroup {
			continue
		}
		if t.Destination != group {
			continue
		}
		if t.Phase == calloutThreadCleared {
			continue
		}
		if source != 0 && t.Source != 0 && t.Source != source {
			continue
		}
		state := dashboardCalloutState{
			Key:                   calloutStateKey(t.DestinationType, t.Destination, t.CalloutNumber),
			DestinationType:       t.DestinationType,
			Destination:           t.Destination,
			Source:                t.Source,
			CalloutNumber:         t.CalloutNumber,
			Function:              t.LastCallout.Function,
			FunctionName:          calloutFunctionName(t.LastCallout.Function),
			Severity:              t.LastCallout.Severity,
			GroupControl:          t.LastCallout.GroupControl,
			TimestampControl:      t.LastCallout.TimestampControl,
			UserReceiptControl:    t.LastCallout.UserReceiptControl,
			TextIsStatus:          t.LastCallout.TextIsStatus,
			EndCallout:            t.LastCallout.EndCallout,
			PTTNotAllowed:         t.LastCallout.PTTNotAllowed,
			MessageType:           t.LastCallout.MessageType,
			MessageRef:            t.LastCallout.MessageRef,
			DeliveryReportRequest: t.LastCallout.DeliveryReportRequest,
			ServiceSelection:      t.LastCallout.ServiceSelection,
			Storage:               t.LastCallout.Storage,
			Text:                  t.LastCallout.Text,
			LastDirection:         t.LastDirection,
			LastSession:           t.LastSession,
			State:                 calloutStateLabel(t.LastCallout),
			Responses:             t.Responses,
			Updated:               t.Updated,
		}
		if !found || state.Updated.After(best.Updated) {
			best = state
			found = true
		}
	}
	return best, found
}

