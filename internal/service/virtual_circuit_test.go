package service

import (
	"io"
	"log"
	"testing"

	"github.com/google/uuid"

	"github.com/freetetra/server/internal/brew"
	"github.com/freetetra/server/internal/config"
)

func newTestService(t *testing.T, cfg config.Config) *Service {
	t.Helper()
	svc, err := New(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("service init failed: %v", err)
	}
	return svc
}

func TestSetupRequestTracksSubscriberDestinationType(t *testing.T) {
	svc := newTestService(t, config.Config{})
	client := &brew.Client{ID: "c1"}
	callID := uuid.New()

	svc.onCallControlFromClient(client, &brew.CallControlMessage{
		CallState:  brew.CallStateSetupRequest,
		Identifier: callID,
		Payload: brew.CircularCallPayload{
			Source:      605,
			Destination: 602,
			Duplex:      1,
		},
	})

	svc.callMu.RLock()
	call := svc.calls[callID]
	svc.callMu.RUnlock()
	if call == nil {
		t.Fatalf("expected tracked call")
	}
	if call.DestinationType != destinationTypeSubscriber {
		t.Fatalf("expected destination_type=%q got=%q", destinationTypeSubscriber, call.DestinationType)
	}
}

func TestVirtualCircuitSetupAndConnect(t *testing.T) {
	svc := newTestService(t, config.Config{
		Echo: config.EchoConfig{VirtualISSI: 499001},
	})
	client := &brew.Client{ID: "c1"}
	callID := uuid.New()

	handled := svc.maybeHandleVirtualCircuitCallControl(client, &brew.CallControlMessage{
		CallState:  brew.CallStateSetupRequest,
		Identifier: callID,
		Payload: brew.CircularCallPayload{
			Source:      605,
			Destination: 499001,
			Duplex:      1,
		},
	}, 605, 499001)
	if !handled {
		t.Fatalf("setup request should be handled as virtual circuit")
	}

	handled = svc.maybeHandleVirtualCircuitCallControl(client, &brew.CallControlMessage{
		CallState:  brew.CallStateConnectRequest,
		Identifier: callID,
		Payload: brew.CircularCallPayload{
			Source:      605,
			Destination: 499001,
			Duplex:      1,
		},
	}, 605, 499001)
	if !handled {
		t.Fatalf("connect request should be handled as virtual circuit")
	}

	svc.callMu.RLock()
	vc := svc.virtualCircuitCalls[callID]
	svc.callMu.RUnlock()
	if vc == nil {
		t.Fatalf("expected virtual circuit state to exist")
	}
	if !vc.Connected {
		t.Fatalf("expected virtual circuit to be connected")
	}
}
