package service

import (
	"sort"
	"time"

	"github.com/freetetra/server/internal/brew"
)

type attachmentControlPayload struct {
	Type      string                  `json:"type"`
	Version   int                     `json:"version"`
	Event     string                  `json:"event"`
	Timestamp string                  `json:"timestamp"`
	Session   string                  `json:"session,omitempty"`
	Remote    string                  `json:"remote,omitempty"`
	Client    *brew.ClientSnapshot    `json:"client,omitempty"`
	Summary   attachmentControlTotals `json:"summary"`
	Channels  []attachmentChannel     `json:"channels"`
}

type attachmentControlTotals struct {
	Clients      int `json:"clients"`
	Subscribers  int `json:"subscribers"`
	ChannelCount int `json:"channel_count"`
}

type attachmentChannel struct {
	GSSI        uint32 `json:"gssi"`
	Sessions    int    `json:"sessions"`
	Subscribers int    `json:"subscribers"`
}

func (s *Service) broadcastAttachmentControl(event string, client *brew.Client) {
	clients := s.server.SnapshotClients()
	payload := attachmentControlPayload{
		Type:      "channel_attachment",
		Version:   1,
		Event:     event,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if client != nil {
		snapshot := client.Snapshot()
		payload.Session = snapshot.ID
		payload.Remote = snapshot.Remote
		payload.Client = &snapshot
	}

	payload.Summary.Clients = len(clients)

	channelSessions := make(map[uint32]map[string]struct{})
	channelSubs := make(map[uint32]map[uint32]struct{})
	for _, c := range clients {
		for _, sub := range c.Subscribers {
			if s.excludeAttachmentSubscriber(sub.Number) {
				continue
			}
			payload.Summary.Subscribers++
			for _, g := range sub.Groups {
				if channelSessions[g] == nil {
					channelSessions[g] = make(map[string]struct{})
				}
				if channelSubs[g] == nil {
					channelSubs[g] = make(map[uint32]struct{})
				}
				channelSessions[g][c.ID] = struct{}{}
				channelSubs[g][sub.Number] = struct{}{}
			}
		}
	}
	payload.Summary.ChannelCount = len(channelSessions)

	payload.Channels = make([]attachmentChannel, 0, len(channelSessions))
	for gssi, sessions := range channelSessions {
		payload.Channels = append(payload.Channels, attachmentChannel{
			GSSI:        gssi,
			Sessions:    len(sessions),
			Subscribers: len(channelSubs[gssi]),
		})
	}
	sort.Slice(payload.Channels, func(i, j int) bool { return payload.Channels[i].GSSI < payload.Channels[j].GSSI })

	wire := brew.BuildAttachmentControl(payload)
	_ = s.server.BroadcastAll(wire, "")
}

func (s *Service) excludeAttachmentSubscriber(issi uint32) bool {
	if issi == 0 {
		return false
	}
	if s.cfg.WebRadio.BrewISSI != 0 && issi == s.cfg.WebRadio.BrewISSI {
		return true
	}
	if s.cfg.Zello.BrewISSI != 0 && issi == s.cfg.Zello.BrewISSI {
		return true
	}
	if s.cfg.Echo.BrewISSI != 0 && issi == s.cfg.Echo.BrewISSI {
		return true
	}
	return false
}
