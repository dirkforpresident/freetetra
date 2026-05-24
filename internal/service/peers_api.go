package service

import (
	"encoding/json"
	"net/http"
	"sort"
)

// handlePeersAPI returns connected federation peers (for admin dashboard).
// Peers are sorted by (name, direction) so the admin dashboard's per-poll
// subscriber-source assignment stays stable: federation snapshots come
// from Go-map iteration which is randomized per call, and the dashboard
// uses first-peer-wins to pick the "source" label for each ISSI. Without
// a stable sort, the source column flipped between peers that shared an
// ISSI from one 5-second poll to the next.
func (s *Service) handlePeersAPI(w http.ResponseWriter, r *http.Request) {
	peers := []any{}
	count := 0
	if s.federation != nil {
		snapshots := s.federation.PeerSnapshots()
		sort.SliceStable(snapshots, func(i, j int) bool {
			if snapshots[i].Name != snapshots[j].Name {
				return snapshots[i].Name < snapshots[j].Name
			}
			return snapshots[i].Direction < snapshots[j].Direction
		})
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
