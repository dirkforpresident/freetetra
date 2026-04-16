package service

import (
	"testing"

	"github.com/freetetra/server/internal/brew"
)

func TestResolveSubscriberCallRouteNonOriginTargetsOriginClient(t *testing.T) {
	existing := &activeCall{
		SourceISSI:      16777186,
		DestinationGSI:  605,
		DestinationType: destinationTypeSubscriber,
		OriginClientID:  "sip-worker",
	}
	dest, source, targetClientID := resolveSubscriberCallRoute(
		existing,
		&brew.Client{ID: "radio-leg"},
		605,
		16777186,
	)
	if dest != 16777186 {
		t.Fatalf("expected destination 16777186, got %d", dest)
	}
	if source != 16777186 {
		t.Fatalf("expected source 16777186, got %d", source)
	}
	if targetClientID != "sip-worker" {
		t.Fatalf("expected target client sip-worker, got %q", targetClientID)
	}
}

func TestResolveSubscriberCallRouteOriginLegUsesDestinationSubscriber(t *testing.T) {
	existing := &activeCall{
		SourceISSI:      16777186,
		DestinationGSI:  605,
		DestinationType: destinationTypeSubscriber,
		OriginClientID:  "sip-worker",
	}
	dest, source, targetClientID := resolveSubscriberCallRoute(
		existing,
		&brew.Client{ID: "sip-worker"},
		16777186,
		16777186,
	)
	if dest != 605 {
		t.Fatalf("expected destination 605, got %d", dest)
	}
	if source != 16777186 {
		t.Fatalf("expected source 16777186, got %d", source)
	}
	if targetClientID != "" {
		t.Fatalf("expected empty target client, got %q", targetClientID)
	}
}

func TestResolveSubscriberCallRouteWithoutOriginClientKeepsSubscriberRouting(t *testing.T) {
	existing := &activeCall{
		SourceISSI:      16777186,
		DestinationGSI:  605,
		DestinationType: destinationTypeSubscriber,
	}
	dest, source, targetClientID := resolveSubscriberCallRoute(
		existing,
		&brew.Client{ID: "radio-leg"},
		605,
		16777186,
	)
	if dest != 605 {
		t.Fatalf("expected destination 605, got %d", dest)
	}
	if source != 16777186 {
		t.Fatalf("expected source 16777186, got %d", source)
	}
	if targetClientID != "" {
		t.Fatalf("expected empty target client, got %q", targetClientID)
	}
}
