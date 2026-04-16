package service

import (
	"sync"
	"testing"
	"time"

	"github.com/freetetra/server/internal/brew"
)

func TestResolveSourceAndGroups(t *testing.T) {
	snapshot := brew.ClientSnapshot{
		Subscribers: []brew.SubscriberSnapshot{
			{Number: 501, Groups: []uint32{10000, 15001}},
			{Number: 502, Groups: []uint32{10000}},
		},
		Groups: []uint32{10000, 15001},
	}

	source, groups, ok := resolveSourceAndGroups(snapshot, 501)
	if !ok {
		t.Fatalf("expected ok")
	}
	if source != 501 {
		t.Fatalf("source=%d want=501", source)
	}
	if len(groups) != 2 || groups[0] != 10000 || groups[1] != 15001 {
		t.Fatalf("groups=%v", groups)
	}
}

func TestChooseManagedCalloutTargetGroups(t *testing.T) {
	groups := []uint32{15001, 10000, 15001}

	selected := chooseManagedCalloutTargetGroups(groups, 10000)
	if len(selected) != 1 || selected[0] != 10000 {
		t.Fatalf("selected=%v", selected)
	}

	selected = chooseManagedCalloutTargetGroups(groups, 0)
	if len(selected) != 2 || selected[0] != 10000 || selected[1] != 15001 {
		t.Fatalf("selected=%v", selected)
	}
}

func TestManagedCalloutResponseOptions(t *testing.T) {
	state := dashboardCalloutState{
		CalloutNumber: 9,
		Function:      1,
		Severity:      3,
		GroupControl:  2,
	}
	payload := buildCalloutPayload([]byte("ok"), managedCalloutResponseOptions(state))
	parsed, ok := parseCalloutPayload(payload)
	if !ok {
		t.Fatalf("expected parse success")
	}
	if parsed.MessageType != 2 {
		t.Fatalf("message_type=%d want=2", parsed.MessageType)
	}
	if parsed.CalloutNumber != 9 {
		t.Fatalf("callout_number=%d want=9", parsed.CalloutNumber)
	}
	if parsed.Function != 1 {
		t.Fatalf("function=%d want=1", parsed.Function)
	}
	if parsed.Text != "ok" {
		t.Fatalf("text=%q want=ok", parsed.Text)
	}
}

func TestFindManagedCalloutContextPrefersLatestGroupState(t *testing.T) {
	now := time.Now().UTC()
	svc := &Service{
		calloutMu: sync.RWMutex{},
		calloutStates: map[string]*dashboardCalloutState{
			"group:10000:1": {
				DestinationType: destinationTypeGroup,
				Destination:     10000,
				CalloutNumber:   1,
				State:           "pending",
				Updated:         now.Add(-time.Minute),
			},
			"group:10000:2": {
				DestinationType: destinationTypeGroup,
				Destination:     10000,
				CalloutNumber:   2,
				State:           "pending",
				Updated:         now,
			},
		},
	}

	ctx, ok := svc.findManagedCalloutContext(0, 10000)
	if !ok {
		t.Fatalf("expected context")
	}
	if ctx.CalloutNumber != 2 {
		t.Fatalf("callout_number=%d want=2", ctx.CalloutNumber)
	}
}
