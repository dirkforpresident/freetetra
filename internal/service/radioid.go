package service

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RadioIDAuth provides automatic user authentication via the RadioID API.
// Any ISSI registered at radioid.net (= licensed amateur radio operator)
// is automatically allowed to connect. No manual account creation needed.
type RadioIDAuth struct {
	logger    *log.Logger
	cache     map[uint32]*radioIDEntry
	blocklist map[uint32]bool
	mu        sync.RWMutex
}

type radioIDEntry struct {
	ISSI           uint32    `json:"issi"`
	Callsign       string    `json:"callsign"`
	Name           string    `json:"name"`
	City           string    `json:"city"`
	Country        string    `json:"country"`
	Valid          bool      `json:"valid"`
	CachedAt       time.Time `json:"cached_at"`
}

const radioIDCacheTTL = 24 * time.Hour

type radioIDResponse struct {
	Count   int `json:"count"`
	Results []struct {
		ID               int    `json:"id"`
		Callsign         string `json:"callsign"`
		Name             string `json:"name"`
		Fname            string `json:"fname"`
		City             string `json:"city"`
		Country          string `json:"country"`
		HasValidCallsign string `json:"has_valid_callsign"`
	} `json:"results"`
}

func newRadioIDAuth(logger *log.Logger) *RadioIDAuth {
	return &RadioIDAuth{
		logger:    logger,
		cache:     make(map[uint32]*radioIDEntry),
		blocklist: make(map[uint32]bool),
	}
}

// Verify checks if an ISSI belongs to a licensed amateur radio operator.
// Returns (callsign, allowed).
func (r *RadioIDAuth) Verify(issi uint32) (string, bool) {
	// Check blocklist first
	r.mu.RLock()
	if r.blocklist[issi] {
		r.mu.RUnlock()
		r.logger.Printf("RadioID: ISSI %d is BLOCKED", issi)
		return "", false
	}

	// Check cache
	if entry, ok := r.cache[issi]; ok && time.Since(entry.CachedAt) < radioIDCacheTTL {
		r.mu.RUnlock()
		return entry.Callsign, entry.Valid
	}
	r.mu.RUnlock()

	// Query RadioID API. TETRA ISSIs can have extension digits (e.g. 262356300
	// = DMR ID 2623563 + extension 00). Try full ISSI first, then strip digits
	// from the end until we find a match.
	entry := r.queryRadioID(issi)
	if !entry.Valid {
		for truncated := issi / 10; truncated >= 1000000; truncated /= 10 {
			e := r.queryRadioID(truncated)
			if e.Valid {
				e.ISSI = issi // Keep original ISSI for cache
				entry = e
				break
			}
		}
	}

	r.mu.Lock()
	r.cache[issi] = entry
	r.mu.Unlock()

	if entry.Valid {
		r.logger.Printf("RadioID: ISSI %d -> %s (%s, %s) -> ALLOWED",
			issi, entry.Callsign, entry.Name, entry.City)
	} else {
		r.logger.Printf("RadioID: ISSI %d -> NOT FOUND -> DENIED", issi)
	}

	return entry.Callsign, entry.Valid
}

func (r *RadioIDAuth) queryRadioID(issi uint32) *radioIDEntry {
	url := fmt.Sprintf("https://radioid.net/api/dmr/user/?id=%d", issi)
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		r.logger.Printf("RadioID: API error for ISSI %d: %v", issi, err)
		// On API error, allow (fail-open for availability)
		return &radioIDEntry{ISSI: issi, Valid: true, Callsign: fmt.Sprintf("ISSI%d", issi), CachedAt: time.Now()}
	}
	defer resp.Body.Close()

	var result radioIDResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		r.logger.Printf("RadioID: decode error for ISSI %d: %v", issi, err)
		return &radioIDEntry{ISSI: issi, Valid: true, Callsign: fmt.Sprintf("ISSI%d", issi), CachedAt: time.Now()}
	}

	if result.Count == 0 || len(result.Results) == 0 {
		return &radioIDEntry{ISSI: issi, Valid: false, CachedAt: time.Now()}
	}

	res := result.Results[0]
	name := strings.TrimSpace(res.Fname)
	if name == "" {
		name = strings.TrimSpace(res.Name)
	}

	return &radioIDEntry{
		ISSI:     issi,
		Callsign: strings.ToUpper(strings.TrimSpace(res.Callsign)),
		Name:     name,
		City:     strings.TrimSpace(res.City),
		Country:  strings.TrimSpace(res.Country),
		Valid:    res.HasValidCallsign == "1" && res.Callsign != "",
		CachedAt: time.Now(),
	}
}

// Block adds an ISSI to the blocklist.
func (ra *RadioIDAuth) Block(issi uint32) {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	ra.blocklist[issi] = true
	ra.logger.Printf("RadioID: ISSI %d BLOCKED", issi)
}

// Unblock removes an ISSI from the blocklist.
func (ra *RadioIDAuth) Unblock(issi uint32) {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	delete(ra.blocklist, issi)
	ra.logger.Printf("RadioID: ISSI %d UNBLOCKED", issi)
}

// CachedUsers returns all cached RadioID entries.
func (ra *RadioIDAuth) CachedUsers() []radioIDEntry {
	ra.mu.RLock()
	defer ra.mu.RUnlock()
	out := make([]radioIDEntry, 0, len(ra.cache))
	for _, e := range ra.cache {
		if e.Valid {
			out = append(out, *e)
		}
	}
	return out
}

// BlockedISSIs returns all blocked ISSIs.
func (ra *RadioIDAuth) BlockedISSIs() []uint32 {
	ra.mu.RLock()
	defer ra.mu.RUnlock()
	out := make([]uint32, 0, len(ra.blocklist))
	for issi := range ra.blocklist {
		out = append(out, issi)
	}
	return out
}

// registerRadioIDHandlers adds admin API endpoints for RadioID management.
func (s *Service) registerRadioIDHandlers() {
	s.server.RegisterHTTPHandler("/api/radioid/users", s.handleRadioIDUsers)
	s.server.RegisterHTTPHandler("/api/radioid/block", s.handleRadioIDBlock)
	s.server.RegisterHTTPHandler("/api/radioid/lookup", s.handleRadioIDLookup)
}

// isLocalRequest checks if a request originates from localhost.
// Checks X-Real-IP (set by nginx) first, then falls back to RemoteAddr.
func isLocalRequest(r *http.Request) bool {
	// If behind reverse proxy, check the real client IP
	realIP := r.Header.Get("X-Real-IP")
	if realIP != "" {
		return realIP == "127.0.0.1" || realIP == "::1"
	}
	host := r.RemoteAddr
	return strings.HasPrefix(host, "127.0.0.1:") ||
		strings.HasPrefix(host, "[::1]:") ||
		strings.HasPrefix(host, "localhost:")
}

func (s *Service) handleRadioIDUsers(w http.ResponseWriter, r *http.Request) {
	if s.radioIDAuth == nil {
		http.Error(w, "RadioID auth not enabled", http.StatusNotFound)
		return
	}
	users := s.radioIDAuth.CachedUsers()
	blocked := s.radioIDAuth.BlockedISSIs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"users":   users,
		"blocked": blocked,
		"count":   len(users),
	})
}

// handleRadioIDBlock is localhost-only (admin action).
func (s *Service) handleRadioIDBlock(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		http.Error(w, "forbidden — admin API only accessible from localhost (use SSH)", http.StatusForbidden)
		return
	}
	if s.radioIDAuth == nil {
		http.Error(w, "RadioID auth not enabled", http.StatusNotFound)
		return
	}
	issiStr := r.URL.Query().Get("issi")
	action := r.URL.Query().Get("action") // "block" or "unblock"
	var issi uint32
	fmt.Sscanf(issiStr, "%d", &issi)
	if issi == 0 {
		http.Error(w, "missing issi parameter", http.StatusBadRequest)
		return
	}
	if action == "unblock" {
		s.radioIDAuth.Unblock(issi)
	} else {
		s.radioIDAuth.Block(issi)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "issi": issi, "action": action})
}

func (s *Service) handleRadioIDLookup(w http.ResponseWriter, r *http.Request) {
	if s.radioIDAuth == nil {
		http.Error(w, "RadioID auth not enabled", http.StatusNotFound)
		return
	}
	issiStr := r.URL.Query().Get("issi")
	var issi uint32
	fmt.Sscanf(issiStr, "%d", &issi)
	if issi == 0 {
		http.Error(w, "missing issi parameter", http.StatusBadRequest)
		return
	}
	callsign, allowed := s.radioIDAuth.Verify(issi)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"issi":     issi,
		"callsign": callsign,
		"allowed":  allowed,
	})
}
