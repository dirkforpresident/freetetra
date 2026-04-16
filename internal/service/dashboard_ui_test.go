package service

import "testing"

func TestParseCalloutThreadKey(t *testing.T) {
	destType, dest, ref, ok := parseCalloutThreadKey("group:10002:14")
	if !ok {
		t.Fatalf("expected key to parse")
	}
	if destType != destinationTypeGroup || dest != 10002 || ref != 14 {
		t.Fatalf("unexpected parse result: type=%q dest=%d ref=%d", destType, dest, ref)
	}
}

func TestCalloutThreadNumberFromRequest(t *testing.T) {
	req := uiSDSSendRequest{
		DestinationType: destinationTypeGroup,
		Destination:     10002,
		CalloutKey:      "group:10002:14",
	}

	ref, forced, err := calloutThreadNumberFromRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !forced {
		t.Fatalf("expected forced callout reference")
	}
	if ref != 14 {
		t.Fatalf("ref=%d want=14", ref)
	}
}

func TestCalloutThreadNumberFromRequestRejectsDestinationMismatch(t *testing.T) {
	req := uiSDSSendRequest{
		DestinationType: destinationTypeGroup,
		Destination:     10002,
		CalloutKey:      "group:10003:14",
	}

	_, _, err := calloutThreadNumberFromRequest(req)
	if err == nil {
		t.Fatalf("expected mismatch error")
	}
}
