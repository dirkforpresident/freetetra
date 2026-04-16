package service

import "testing"

func TestVirtualSDSEndpointUpsertAndDelete(t *testing.T) {
	s := &Service{
		virtualSDSEndpoints: make(map[uint32]*virtualSDSEndpointState),
	}

	snap, created := s.upsertVirtualSDSEndpoint(1201, "")
	if !created {
		t.Fatalf("expected endpoint to be created")
	}
	if snap.Name != "virtual-1201" {
		t.Fatalf("unexpected default endpoint name: %q", snap.Name)
	}
	if !s.hasVirtualSDSEndpoint(1201) {
		t.Fatalf("endpoint should exist after upsert")
	}

	snap, created = s.upsertVirtualSDSEndpoint(1201, "dispatch-a")
	if created {
		t.Fatalf("expected endpoint update, not create")
	}
	if snap.Name != "dispatch-a" {
		t.Fatalf("unexpected updated endpoint name: %q", snap.Name)
	}

	if !s.deleteVirtualSDSEndpoint(1201) {
		t.Fatalf("expected delete to succeed")
	}
	if s.hasVirtualSDSEndpoint(1201) {
		t.Fatalf("endpoint should not exist after delete")
	}
}

func TestVirtualSDSMessagesConsumeFlow(t *testing.T) {
	s := &Service{
		virtualSDSEndpoints: make(map[uint32]*virtualSDSEndpointState),
	}
	s.upsertVirtualSDSEndpoint(2201, "queue")

	if ok := s.maybeStoreVirtualSDSMessage(2201, virtualSDSMessage{
		Direction:   "rx",
		Source:      501,
		Destination: 2201,
		FrameType:   1,
		Kind:        "flash",
		PayloadHex:  "0102",
	}); !ok {
		t.Fatalf("expected first store to succeed")
	}
	if ok := s.maybeStoreVirtualSDSMessage(2201, virtualSDSMessage{
		Direction:   "tx",
		Source:      2201,
		Destination: 501,
		FrameType:   1,
		Kind:        "callout",
		PayloadHex:  "c300",
	}); !ok {
		t.Fatalf("expected second store to succeed")
	}

	list, ok := s.virtualSDSMessages(2201, 0, 10, false)
	if !ok {
		t.Fatalf("expected endpoint queue to exist")
	}
	if len(list) != 2 {
		t.Fatalf("unexpected queue length: %d", len(list))
	}
	if list[0].Seq == 0 || list[1].Seq == 0 || list[1].Seq <= list[0].Seq {
		t.Fatalf("message sequence should be increasing: %d %d", list[0].Seq, list[1].Seq)
	}

	consumed, ok := s.virtualSDSMessages(2201, 0, 1, true)
	if !ok {
		t.Fatalf("expected endpoint queue to exist for consume")
	}
	if len(consumed) != 1 {
		t.Fatalf("unexpected consumed length: %d", len(consumed))
	}

	remaining, ok := s.virtualSDSMessages(2201, 0, 10, false)
	if !ok {
		t.Fatalf("expected endpoint queue to exist after consume")
	}
	if len(remaining) != 1 {
		t.Fatalf("unexpected remaining length: %d", len(remaining))
	}
	if remaining[0].Seq == consumed[0].Seq {
		t.Fatalf("consumed message should not remain in queue")
	}
}

func TestEnsureGroupCalloutVirtualEndpointStable(t *testing.T) {
	s := &Service{
		virtualSDSEndpoints: make(map[uint32]*virtualSDSEndpointState),
		groupCalloutVirtual: make(map[uint32]uint32),
	}

	issiA, snapA, createdA := s.ensureGroupCalloutVirtualEndpoint(10000)
	if issiA == 0 {
		t.Fatalf("issiA must be non-zero")
	}
	if !createdA {
		t.Fatalf("first ensure must create endpoint")
	}
	if snapA.ISSI != issiA {
		t.Fatalf("snapshot issi=%d want=%d", snapA.ISSI, issiA)
	}

	issiA2, _, createdA2 := s.ensureGroupCalloutVirtualEndpoint(10000)
	if issiA2 != issiA {
		t.Fatalf("second ensure changed issi from %d to %d", issiA, issiA2)
	}
	if createdA2 {
		t.Fatalf("second ensure must not report created")
	}

	issiB, _, _ := s.ensureGroupCalloutVirtualEndpoint(10001)
	if issiB == issiA {
		t.Fatalf("different groups must not share virtual issi (%d)", issiA)
	}
}
