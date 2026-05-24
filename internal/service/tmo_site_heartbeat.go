package service

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// TMOSiteHeartbeat tracks which TMO-sites are connected and how many
// subscribers each one has. The data comes from periodic POST requests
// from BlueStation dashboards.
//
// This solves the problem that BlueStation doesn't forward subscriber
// registrations over Brew if they're only on local TGs (1-4).
type TMOSiteHeartbeat struct {
	mu       sync.RWMutex
	tmoSites map[string]*TMOSiteStatus
}

// TMOSiteStatus tracks a single TMO-site's reported state.
type TMOSiteStatus struct {
	Name        string    `json:"name"`         // BlueStation cell name (e.g. "DO0RAM")
	Callsign    string    `json:"callsign"`     // Operator callsign
	Subscribers []uint32  `json:"subscribers"`  // List of registered ISSIs
	LastSeen    time.Time `json:"last_seen"`    // Last heartbeat received
	IP          string    `json:"ip,omitempty"` // Source IP (for admin info)
}

const tmoSiteTimeout = 90 * time.Second

func newTMOSiteHeartbeat() *TMOSiteHeartbeat {
	return &TMOSiteHeartbeat{
		tmoSites: make(map[string]*TMOSiteStatus),
	}
}

// Update records a heartbeat from a TMO-site.
func (rh *TMOSiteHeartbeat) Update(name, callsign, ip string, subscribers []uint32) {
	rh.mu.Lock()
	defer rh.mu.Unlock()
	rh.tmoSites[name] = &TMOSiteStatus{
		Name:        name,
		Callsign:    callsign,
		Subscribers: subscribers,
		LastSeen:    time.Now(),
		IP:          ip,
	}
}

// ActiveTMOSites returns TMO-sites that sent a heartbeat in the last 90s.
func (rh *TMOSiteHeartbeat) ActiveTMOSites() []TMOSiteStatus {
	rh.mu.RLock()
	defer rh.mu.RUnlock()
	cutoff := time.Now().Add(-tmoSiteTimeout)
	out := make([]TMOSiteStatus, 0, len(rh.tmoSites))
	for _, r := range rh.tmoSites {
		if r.LastSeen.After(cutoff) {
			out = append(out, *r)
		}
	}
	return out
}

// TotalSubscribers returns the total count of unique ISSIs across all active TMO-sites.
func (rh *TMOSiteHeartbeat) TotalSubscribers() int {
	rh.mu.RLock()
	defer rh.mu.RUnlock()
	cutoff := time.Now().Add(-tmoSiteTimeout)
	unique := make(map[uint32]bool)
	for _, r := range rh.tmoSites {
		if r.LastSeen.After(cutoff) {
			for _, issi := range r.Subscribers {
				unique[issi] = true
			}
		}
	}
	return len(unique)
}

// ActiveCount returns the number of active TMO-sites.
func (rh *TMOSiteHeartbeat) ActiveCount() int {
	rh.mu.RLock()
	defer rh.mu.RUnlock()
	cutoff := time.Now().Add(-tmoSiteTimeout)
	count := 0
	for _, r := range rh.tmoSites {
		if r.LastSeen.After(cutoff) {
			count++
		}
	}
	return count
}

func (s *Service) registerTMOSiteHandlers() {
	s.server.RegisterHTTPHandler("/api/tmo-site/heartbeat", s.handleTMOSiteHeartbeat)
	s.server.RegisterHTTPHandler("/api/tmo-site/list", s.handleTMOSiteList)
}

func (s *Service) handleTMOSiteHeartbeat(w http.ResponseWriter, r *http.Request) {
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

	if s.tmoSites == nil {
		s.tmoSites = newTMOSiteHeartbeat()
	}
	s.tmoSites.Update(req.Name, req.Callsign, ip, req.Subscribers)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Service) handleTMOSiteList(w http.ResponseWriter, r *http.Request) {
	if s.tmoSites == nil {
		s.tmoSites = newTMOSiteHeartbeat()
	}
	tmoSites := s.tmoSites.ActiveTMOSites()

	// Strip IPs unless localhost
	isLocal := isLocalRequest(r)
	publicTMOSites := make([]map[string]any, 0, len(tmoSites))
	for _, t := range tmoSites {
		entry := map[string]any{
			"name":             t.Name,
			"callsign":         t.Callsign,
			"subscriber_count": len(t.Subscribers),
			"last_seen":        t.LastSeen,
		}
		if isLocal {
			entry["ip"] = t.IP
			entry["subscribers"] = t.Subscribers
		}
		publicTMOSites = append(publicTMOSites, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]any{
		"tmo_sites": publicTMOSites,
		"count":     len(publicTMOSites),
	})
}
