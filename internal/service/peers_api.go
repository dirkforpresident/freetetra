package service

import (
	"encoding/json"
	"net/http"
)

// handlePeersAPI returns connected federation peers (for admin dashboard).
func (s *Service) handlePeersAPI(w http.ResponseWriter, r *http.Request) {
	peers := []any{}
	count := 0
	if s.federation != nil {
		snapshots := s.federation.PeerSnapshots()
		for _, p := range snapshots {
			peers = append(peers, p)
		}
		count = len(snapshots)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"peers": peers,
		"count": count,
	})
}
