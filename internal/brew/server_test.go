package brew

import "testing"

func TestSendToClient(t *testing.T) {
	client := &Client{
		ID:   "sip-worker",
		send: make(chan []byte, 1),
	}
	srv := &Server{
		clients: map[string]*Client{
			client.ID: client,
		},
	}
	payload := []byte{0x01, 0x02, 0x03}
	if ok := srv.SendToClient(client.ID, payload); !ok {
		t.Fatalf("expected SendToClient to succeed")
	}
	select {
	case got := <-client.send:
		if len(got) != len(payload) {
			t.Fatalf("unexpected payload length %d", len(got))
		}
	default:
		t.Fatalf("expected payload to be queued")
	}
}

func TestSendToClientMissingClient(t *testing.T) {
	srv := &Server{clients: map[string]*Client{}}
	if ok := srv.SendToClient("missing", []byte{0x01}); ok {
		t.Fatalf("expected SendToClient to fail for missing client")
	}
}
