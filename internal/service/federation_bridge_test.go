package service

import (
	"bytes"
	"log"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/freetetra/server/internal/brew"
)

// newTestBridge wires a federationBridge to a real brew.Server (no listener)
// so BroadcastToSubscriber / BroadcastToGroup hit the real fan-out logic.
// fb.hub is nil — these tests only exercise the receive side, which doesn't
// call back into the hub.
func newTestBridge(t *testing.T) (*federationBridge, *brew.Server, *log.Logger) {
	t.Helper()
	logger := log.New(&bytes.Buffer{}, "", 0)
	server := brew.NewTestServer(logger)
	svc := &Service{
		logger: logger,
		server: server,
		calls:  map[uuid.UUID]*activeCall{},
		callMu: sync.RWMutex{},
	}
	fb := &federationBridge{
		logger: logger,
		svc:    svc,
	}
	return fb, server, logger
}

func TestOnPeerVoiceFrame_SubscriberBranchHitsBothDestAndSource(t *testing.T) {
	fb, server, _ := newTestBridge(t)

	const sourceISSI = uint32(1001)
	const destISSI = uint32(2002)

	// Two locally-attached subscribers: dest and source. In real traffic the
	// source ISSI's owning peer is the one sending us frames, so it usually
	// won't be local — but the plan calls for mirroring service.go's local
	// subscriber-call fan-out, which sends to BOTH legs.
	destClient := brew.RegisterTestClient(server, "dest-radio", map[uint32][]uint32{
		destISSI: nil,
	})
	sourceClient := brew.RegisterTestClient(server, "source-radio", map[uint32][]uint32{
		sourceISSI: nil,
	})
	// Bystander on a different ISSI must not get the frame.
	bystander := brew.RegisterTestClient(server, "bystander", map[uint32][]uint32{
		uint32(9999): nil,
	})

	callID := uuid.New()
	fb.svc.callMu.Lock()
	fb.svc.calls[callID] = &activeCall{
		ID:              callID,
		SourceISSI:      sourceISSI,
		DestinationGSI:  destISSI, // overloaded: holds dest ISSI for subscriber calls
		DestinationType: destinationTypeSubscriber,
	}
	fb.svc.callMu.Unlock()

	fb.OnPeerVoiceFrame("peerA", callID.String(), []byte{0xAA, 0xBB, 0xCC})

	if got := brew.DrainSend(destClient); len(got) != 1 {
		t.Fatalf("dest-radio expected 1 frame, got %d", len(got))
	}
	if got := brew.DrainSend(sourceClient); len(got) != 1 {
		t.Fatalf("source-radio expected 1 frame, got %d", len(got))
	}
	if got := brew.DrainSend(bystander); len(got) != 0 {
		t.Fatalf("bystander expected 0 frames, got %d", len(got))
	}
}

func TestOnPeerVoiceFrame_SubscriberBranchSkipsSourceWhenEqualToDest(t *testing.T) {
	// Defensive: if a bogus call ever has source == dest, don't double-deliver.
	fb, server, _ := newTestBridge(t)

	const issi = uint32(3003)
	client := brew.RegisterTestClient(server, "radio", map[uint32][]uint32{
		issi: nil,
	})

	callID := uuid.New()
	fb.svc.callMu.Lock()
	fb.svc.calls[callID] = &activeCall{
		ID:              callID,
		SourceISSI:      issi,
		DestinationGSI:  issi,
		DestinationType: destinationTypeSubscriber,
	}
	fb.svc.callMu.Unlock()

	fb.OnPeerVoiceFrame("peerA", callID.String(), []byte{0x01})

	if got := brew.DrainSend(client); len(got) != 1 {
		t.Fatalf("expected exactly 1 frame when source==dest, got %d", len(got))
	}
}

func TestOnPeerVoiceFrame_GroupBranchUsesBroadcastToGroup(t *testing.T) {
	fb, server, _ := newTestBridge(t)

	const gssi = uint32(26)
	// Two clients attached to the GSSI, one bystander on a different group.
	tg := brew.RegisterTestClient(server, "tg-radio-1", map[uint32][]uint32{
		uint32(100): {gssi},
	})
	tg2 := brew.RegisterTestClient(server, "tg-radio-2", map[uint32][]uint32{
		uint32(101): {gssi},
	})
	bystander := brew.RegisterTestClient(server, "other-tg-radio", map[uint32][]uint32{
		uint32(200): {uint32(99)},
	})

	callID := uuid.New()
	fb.svc.callMu.Lock()
	fb.svc.calls[callID] = &activeCall{
		ID:              callID,
		SourceISSI:      1001,
		DestinationGSI:  gssi,
		DestinationType: destinationTypeGroup,
	}
	fb.svc.callMu.Unlock()

	fb.OnPeerVoiceFrame("peerA", callID.String(), []byte{0xAA})

	if got := brew.DrainSend(tg); len(got) != 1 {
		t.Fatalf("tg-radio-1 expected 1 frame, got %d", len(got))
	}
	if got := brew.DrainSend(tg2); len(got) != 1 {
		t.Fatalf("tg-radio-2 expected 1 frame, got %d", len(got))
	}
	if got := brew.DrainSend(bystander); len(got) != 0 {
		t.Fatalf("bystander expected 0 frames, got %d", len(got))
	}
}
