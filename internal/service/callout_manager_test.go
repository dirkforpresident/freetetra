package service

import (
	"testing"
	"time"
)

func TestCalloutManagerAllocateOutboundCalloutNumber(t *testing.T) {
	m := newCalloutManager()

	n1 := m.allocateOutboundCalloutNumber(destinationTypeGroup, 10002, 970002, 1, 0, "organized")
	n2 := m.allocateOutboundCalloutNumber(destinationTypeGroup, 10002, 970002, 1, 0, "organized")
	if n2 == n1 {
		t.Fatalf("organized allocation must advance: n1=%d n2=%d", n1, n2)
	}

	n3 := m.allocateOutboundCalloutNumber(destinationTypeGroup, 10002, 970002, 3, 0, "reuse")
	if n3 != n2 {
		t.Fatalf("reuse mode must keep active number: got=%d want=%d", n3, n2)
	}

	n4 := m.allocateOutboundCalloutNumber(destinationTypeGroup, 10002, 970002, 4, 0, "reuse")
	if n4 != n2 {
		t.Fatalf("clear should keep route number before close: got=%d want=%d", n4, n2)
	}
}

func TestCalloutManagerLatestGroupContextIncludesResponderState(t *testing.T) {
	m := newCalloutManager()
	now := time.Now().UTC()

	tx := calloutMessage{
		MessageType:   0,
		Function:      1,
		CalloutNumber: 77,
		Text:          "alert",
	}
	m.noteTx(destinationTypeGroup, 10002, 970002, tx, now)

	rx := calloutMessage{
		MessageType:   2,
		Function:      3,
		CalloutNumber: 77,
		Text:          "ack",
	}
	m.noteRx(sdsFrameEnvelope{Source: 601, Destination: 10002}, rx, "session-a", now.Add(time.Second))

	ctx, ok := m.latestGroupContext(970002, 10002)
	if !ok {
		t.Fatalf("expected latestGroupContext to find thread")
	}
	if ctx.CalloutNumber != 77 {
		t.Fatalf("callout_number=%d want=77", ctx.CalloutNumber)
	}
	if ctx.Responses != 1 {
		t.Fatalf("responses=%d want=1", ctx.Responses)
	}
}

