package service

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// RepeaterHeartbeat tracks which repeaters are connected and how many
// subscribers each one has. The data comes from periodic POST requests
// from BlueStation dashboards.
//
// This solves the problem that BlueStation doesn't forward subscriber
// registrations over Brew if they're only on local TGs (1-4).
type RepeaterHeartbeat struct {
	mu        sync.RWMutex
	repeaters map[string]*RepeaterStatus
}

// RepeaterStatus tracks a single repeater's reported state.
type RepeaterStatus struct {
	Name        string    `json:"name"`         // BlueStation cell name (e.g. "DO0RAM")
	Callsign    string    `json:"callsign"`     // Operator callsign
	Subscribers []uint32  `json:"subscribers"`  // List of registered ISSIs
	LastSeen    time.Time `json:"last_seen"`    // Last heartbeat received
	IP          string    `json:"ip,omitempty"` // Source IP (for admin info)
}

const repeaterTimeout = 90 * time.Second

func newRepeaterHeartbeat() *RepeaterHeartbeat {
	return &RepeaterHeartbeat{
		repeaters: make(map[string]*RepeaterStatus),
	}
}

// Update records a heartbeat from a repeater.
func (rh *RepeaterHeartbeat) Update(name, callsign, ip string, subscribers []uint32) {
	rh.mu.Lock()
	defer rh.mu.Unlock()
	rh.repeaters[name] = &RepeaterStatus{
		Name:        name,
		Callsign:    callsign,
		Subscribers: subscribers,
		LastSeen:    time.Now(),
		IP:          ip,
	}
}

// ActiveRepeaters returns repeaters that sent a heartbeat in the last 90s.
func (rh *RepeaterHeartbeat) ActiveRepeaters() []RepeaterStatus {
	rh.mu.RLock()
	defer rh.mu.RUnlock()
	cutoff := time.Now().Add(-repeaterTimeout)
	out := make([]RepeaterStatus, 0, len(rh.repeaters))
	for _, r := range rh.repeaters {
		if r.LastSeen.After(cutoff) {
			out = append(out, *r)
		}
	}
	return out
}

// TotalSubscribers returns the total count of unique ISSIs across all active repeaters.
func (rh *RepeaterHeartbeat) TotalSubscribers() int {
	rh.mu.RLock()
	defer rh.mu.RUnlock()
	cutoff := time.Now().Add(-repeaterTimeout)
	unique := make(map[uint32]bool)
	for _, r := range rh.repeaters {
		if r.LastSeen.After(cutoff) {
			for _, issi := range r.Subscribers {
				unique[issi] = true
			}
		}
	}
	return len(unique)
}

// ActiveCount returns the number of active repeaters.
func (rh *RepeaterHeartbeat) ActiveCount() int {
	rh.mu.RLock()
	defer rh.mu.RUnlock()
	cutoff := time.Now().Add(-repeaterTimeout)
	count := 0
	for _, r := range rh.repeaters {
		if r.LastSeen.After(cutoff) {
			count++
		}
	}
	return count
}

func (s *Service) registerRepeaterHandlers() {
	s.server.RegisterHTTPHandler("/api/repeater/heartbeat", s.handleRepeaterHeartbeat)
	s.server.RegisterHTTPHandler("/api/repeater/list", s.handleRepeaterList)
}

func (s *Service) handleRepeaterHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name        string   `json:"name"`
		Callsign    string   `json:"callsign"`
		Subscribers []uint32 `json:"subscribers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}

	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		ip = r.RemoteAddr
	}

	if s.repeaters == nil {
		s.repeaters = newRepeaterHeartbeat()
	}
	s.repeaters.Update(req.Name, req.Callsign, ip, req.Subscribers)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Service) handleRepeaterList(w http.ResponseWriter, r *http.Request) {
	if s.repeaters == nil {
		s.repeaters = newRepeaterHeartbeat()
	}
	repeaters := s.repeaters.ActiveRepeaters()

	// Strip IPs unless localhost
	isLocal := isLocalRequest(r)
	publicRepeaters := make([]map[string]any, 0, len(repeaters))
	for _, rep := range repeaters {
		entry := map[string]any{
			"name":             rep.Name,
			"callsign":         rep.Callsign,
			"subscriber_count": len(rep.Subscribers),
			"last_seen":        rep.LastSeen,
		}
		if isLocal {
			entry["ip"] = rep.IP
			entry["subscribers"] = rep.Subscribers
		}
		publicRepeaters = append(publicRepeaters, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]any{
		"repeaters": publicRepeaters,
		"count":     len(publicRepeaters),
	})
}
