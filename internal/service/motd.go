package service

import (
	"github.com/freetetra/server/internal/brew"
)

// motdSeenStore tracks which subscribers have already seen the MOTD.
type motdSeenStore struct {
	path string
	seen map[uint32]bool
}

func newMOTDSeenStore(path string) (*motdSeenStore, error) {
	return &motdSeenStore{path: path, seen: make(map[uint32]bool)}, nil
}

func (s *Service) noteMOTDDisconnect(snap brew.ClientSnapshot) {
	// no-op if MOTD not configured
}

func (s *Service) maybeSendMOTDForSubscriber(client *brew.Client, m *brew.SubscriberMessage) {
	// no-op if MOTD not configured
}
