package service

import "testing"

func TestNoteCalloutRxMatchesExistingGroupState(t *testing.T) {
	s := &Service{
		calloutStates: make(map[string]*dashboardCalloutState),
	}

	tx := calloutMessage{
		CalloutNumber: 8,
		Function:      1,
		MessageType:   0,
		Text:          "Alert",
	}
	txKey := s.noteCalloutTx(destinationTypeGroup, 10000, 900000, tx)
	if txKey != "group:10000:8" {
		t.Fatalf("txKey=%q want=group:10000:8", txKey)
	}

	rx := calloutMessage{
		CalloutNumber: 8,
		Function:      3,
		MessageType:   2,
		Text:          "Reject",
	}
	rxKey := s.noteCalloutRx("session-a", sdsFrameEnvelope{Source: 601, Destination: 10000}, rx)
	if rxKey == txKey {
		t.Fatalf("rxKey must be subscriber-scoped, got shared key=%q", rxKey)
	}
	wantRxKey := calloutSubscriberReplyKey(601, 10000, 8)
	if rxKey != wantRxKey {
		t.Fatalf("rxKey=%q want=%q", rxKey, wantRxKey)
	}

	parent := s.calloutStates[txKey]
	if parent == nil {
		t.Fatalf("missing parent state for key=%q", txKey)
	}
	if parent.Responses != 1 {
		t.Fatalf("parent responses=%d want=1", parent.Responses)
	}
	if parent.Text != "Alert" {
		t.Fatalf("parent text=%q want=%q", parent.Text, "Alert")
	}
	if parent.LastDirection != "tx" {
		t.Fatalf("parent last_direction=%q want=tx", parent.LastDirection)
	}

	st := s.calloutStates[rxKey]
	if st == nil {
		t.Fatalf("missing state for key=%q", rxKey)
	}
	if st.DestinationType != destinationTypeSubscriber || st.Destination != 601 {
		t.Fatalf("unexpected destination state: type=%q destination=%d", st.DestinationType, st.Destination)
	}
	if st.Source != 900000 {
		t.Fatalf("source=%d want=900000", st.Source)
	}
	if st.Responses != 1 {
		t.Fatalf("responses=%d want=1", st.Responses)
	}
	if st.Text != "Reject" {
		t.Fatalf("text=%q want=%q", st.Text, "Reject")
	}
	if st.LastDirection != "rx" {
		t.Fatalf("last_direction=%q want=rx", st.LastDirection)
	}
}

func TestNoteCalloutRxCreatesGroupStateFromDestination(t *testing.T) {
	s := &Service{
		calloutStates: make(map[string]*dashboardCalloutState),
	}

	rx := calloutMessage{
		CalloutNumber: 3,
		Function:      3,
		MessageType:   2,
		Text:          "Accept",
	}
	key := s.noteCalloutRx("session-b", sdsFrameEnvelope{Source: 601, Destination: 10000}, rx)
	want := calloutSubscriberReplyKey(601, 10000, 3)
	if key != want {
		t.Fatalf("key=%q want=%q", key, want)
	}

	st := s.calloutStates[key]
	if st == nil {
		t.Fatalf("missing state for key=%q", key)
	}
	if st.DestinationType != destinationTypeSubscriber || st.Destination != 601 {
		t.Fatalf("unexpected destination state: type=%q destination=%d", st.DestinationType, st.Destination)
	}
	if st.Source != 10000 {
		t.Fatalf("source=%d want=10000", st.Source)
	}
	if st.Responses != 1 {
		t.Fatalf("responses=%d want=1", st.Responses)
	}
}

func TestNoteCalloutRxMatchesWhenDestinationIsCalloutSourceISSI(t *testing.T) {
	s := &Service{
		calloutStates: make(map[string]*dashboardCalloutState),
	}

	tx := calloutMessage{
		CalloutNumber: 5,
		Function:      1,
		MessageType:   0,
		Text:          "Alert",
	}
	txKey := s.noteCalloutTx(destinationTypeGroup, 10000, 970000, tx)

	rx := calloutMessage{
		CalloutNumber: 5,
		Function:      3,
		MessageType:   2,
		Text:          "Ack via source",
	}
	rxKey := s.noteCalloutRx("session-c", sdsFrameEnvelope{Source: 601, Destination: 970000}, rx)
	wantRxKey := calloutSubscriberReplyKey(601, 970000, 5)
	if rxKey != wantRxKey {
		t.Fatalf("rxKey=%q want=%q", rxKey, wantRxKey)
	}
	parent := s.calloutStates[txKey]
	if parent == nil {
		t.Fatalf("missing state for key=%q", txKey)
	}
	if parent.Responses != 1 {
		t.Fatalf("parent responses=%d want=1", parent.Responses)
	}

	st := s.calloutStates[rxKey]
	if st == nil {
		t.Fatalf("missing state for key=%q", rxKey)
	}
	if st.Responses != 1 {
		t.Fatalf("responses=%d want=1", st.Responses)
	}
	if st.Text != "Ack via source" {
		t.Fatalf("text=%q want=%q", st.Text, "Ack via source")
	}
}

func TestNoteCalloutRxStoresRepliesPerSubscriber(t *testing.T) {
	s := &Service{
		calloutStates: make(map[string]*dashboardCalloutState),
	}

	tx := calloutMessage{
		CalloutNumber: 6,
		Function:      1,
		MessageType:   0,
		Text:          "Alert",
	}
	txKey := s.noteCalloutTx(destinationTypeGroup, 10000, 900000, tx)

	rx := calloutMessage{
		CalloutNumber: 6,
		Function:      3,
		MessageType:   2,
		Text:          "Ack",
	}
	keyA := s.noteCalloutRx("session-a", sdsFrameEnvelope{Source: 601, Destination: 10000}, rx)
	keyB := s.noteCalloutRx("session-b", sdsFrameEnvelope{Source: 602, Destination: 10000}, rx)
	if keyA == keyB {
		t.Fatalf("reply keys must differ per subscriber: keyA=%q keyB=%q", keyA, keyB)
	}

	parent := s.calloutStates[txKey]
	if parent == nil {
		t.Fatalf("missing parent state for key=%q", txKey)
	}
	if parent.Responses != 2 {
		t.Fatalf("parent responses=%d want=2", parent.Responses)
	}
	if _, ok := s.calloutStates[calloutSubscriberReplyKey(601, 10000, 6)]; !ok {
		t.Fatalf("missing subscriber reply state for ISSI 601")
	}
	if _, ok := s.calloutStates[calloutSubscriberReplyKey(602, 10000, 6)]; !ok {
		t.Fatalf("missing subscriber reply state for ISSI 602")
	}
}
