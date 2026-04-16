package service

import (
	"io"
	"log"
	"testing"

	"github.com/freetetra/server/internal/config"
)

func TestBrewModulePlane_UpdateAttachmentState(t *testing.T) {
	p := NewBrewModulePlane(config.Config{}, log.New(io.Discard, "", 0), 0, nil)

	p.updateAttachmentState(`{"type":"channel_attachment","version":1,"channels":[{"gssi":10001,"subscribers":3},{"gssi":10002,"subscribers":0}]}`)

	if got := p.GroupSubscriberCount(10001); got != 3 {
		t.Fatalf("GroupSubscriberCount(10001)=%d want=3", got)
	}
	if got := p.GroupSubscriberCount(10002); got != 0 {
		t.Fatalf("GroupSubscriberCount(10002)=%d want=0", got)
	}

	// Invalid payloads must not reset previously valid state.
	p.updateAttachmentState(`{bad-json`)
	if got := p.GroupSubscriberCount(10001); got != 3 {
		t.Fatalf("state changed after invalid json: got=%d want=3", got)
	}
}
