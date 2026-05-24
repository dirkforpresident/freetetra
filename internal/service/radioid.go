package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// RadioIDAuth provides automatic user authentication via the RadioID API.
// Any ISSI registered at radioid.net (= licensed amateur radio operator)
// is automatically allowed to connect. No manual account creation needed.
//
// In offline mode (HamNet, no internet), checks a local users.txt file
// instead of the API. Format: one entry per line, "ISSI CALLSIGN".
type RadioIDAuth struct {
	logger    *log.Logger
	cache     map[uint32]*radioIDEntry
	blocklist map[uint32]bool
	offline   bool              // No internet — use local users.txt
	localFile string            // Path to users.txt
	localDB   map[uint32]string // ISSI -> Callsign (loaded from users.txt)
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

// LocalDBInfo returns the timestamp and count of entries in users.txt.
// Timestamp is the file's modification time in RFC3339 format.
func (r *RadioIDAuth) LocalDBInfo() (string, int) {
	info, err := os.Stat(r.localFile)
	if err != nil {
		return "", 0
	}
	r.mu.RLock()
	count := len(r.localDB)
	r.mu.RUnlock()
	return info.ModTime().UTC().Format(time.RFC3339), count
}

// LocalDBPath returns the path to users.txt (for HTTP serving).
func (r *RadioIDAuth) LocalDBPath() string {
	return r.localFile
}

// DownloadFromURL downloads users.txt from a peer's HTTP endpoint.
func (r *RadioIDAuth) DownloadFromURL(url string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	tmpFile := r.localFile + ".tmp"
	f, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmpFile)
		return fmt.Errorf("write: %w", err)
	}
	f.Close()

	if err := os.Rename(tmpFile, r.localFile); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	r.mu.Lock()
	r.localDB = make(map[uint32]string)
	r.mu.Unlock()
	r.loadLocalDB()
	return nil
}

// NewRadioIDAuth returns a configured RadioIDAuth. Exported wrapper around
// newRadioIDAuth for use by sibling binaries (e.g. dmrbridge gate).
func NewRadioIDAuth(logger *log.Logger, offline bool, localFile string) *RadioIDAuth {
	return newRadioIDAuth(logger, offline, localFile)
}

func newRadioIDAuth(logger *log.Logger, offline bool, localFile string) *RadioIDAuth {
	if localFile == "" {
		localFile = "users.txt"
	}
	r := &RadioIDAuth{
		logger:    logger,
		cache:     make(map[uint32]*radioIDEntry),
		blocklist: make(map[uint32]bool),
		offline:   offline,
		localFile: localFile,
		localDB:   make(map[uint32]string),
	}
	r.loadLocalDB()
	return r
}

// SyncLocalDB downloads the full RadioID user database and saves it locally.
// This lets the server authenticate users even without internet.
// Call this once (e.g. at startup or via cron) while online.
func (r *RadioIDAuth) SyncLocalDB() error {
	r.logger.Printf("RadioID: downloading full user database from radioid.net...")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get("https://radioid.net/static/users.json")
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	var dump struct {
		Users []struct {
			RadioID          int    `json:"radio_id"`
			Callsign         string `json:"callsign"`
			HasValidCallsign string `json:"has_valid_callsign"`
		} `json:"users"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dump); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	// Write to users.txt (sorted, simple format)
	f, err := os.Create(r.localFile)
	if err != nil {
		return fmt.Errorf("create %s: %w", r.localFile, err)
	}
	defer f.Close()

	fmt.Fprintln(f, "# FreeTetra local users database")
	fmt.Fprintln(f, "# Auto-generated from radioid.net — do not edit manually")
	fmt.Fprintln(f, "# Format: <ISSI> <CALLSIGN>")
	fmt.Fprintf(f, "# Generated: %s\n", time.Now().Format(time.RFC3339))
	count := 0
	for _, u := range dump.Users {
		if u.HasValidCallsign != "1" || u.Callsign == "" || u.RadioID == 0 {
			continue
		}
		fmt.Fprintf(f, "%d %s\n", u.RadioID, strings.ToUpper(u.Callsign))
		count++
	}

	r.logger.Printf("RadioID: saved %d users to %s", count, r.localFile)

	// Reload into memory
	r.mu.Lock()
	r.localDB = make(map[uint32]string)
	r.mu.Unlock()
	r.loadLocalDB()
	return nil
}

// loadLocalDB reads the users.txt file (fallback for offline mode).
// Format: one entry per line, "<ISSI> <CALLSIGN>" (whitespace separated).
// Lines starting with # are comments.
func (r *RadioIDAuth) loadLocalDB() {
	f, err := os.Open(r.localFile)
	if err != nil {
		if r.offline {
			r.logger.Printf("RadioID: offline mode but %s not found — no users will be accepted", r.localFile)
		}
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var issi uint32
		fmt.Sscanf(fields[0], "%d", &issi)
		if issi == 0 {
			continue
		}
		r.localDB[issi] = strings.ToUpper(fields[1])
		count++
	}
	r.logger.Printf("RadioID: loaded %d local users from %s", count, r.localFile)
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

	// Offline mode: check local users.txt only
	if r.offline {
		// Try full ISSI first, then strip trailing digits
		for check := issi; check >= 1000000; check /= 10 {
			if call, ok := r.localDB[check]; ok {
				r.mu.RUnlock()
				return call, true
			}
			if check == issi && len(r.localDB) > 0 {
				// Only try truncation if we have entries at all
				continue
			} else {
				break
			}
		}
		r.mu.RUnlock()
		r.logger.Printf("RadioID: offline mode — ISSI %d not in local users.txt", issi)
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
		// API unreachable — try local users.txt as fallback
		if call, ok := r.localDB[issi]; ok {
			return &radioIDEntry{ISSI: issi, Valid: true, Callsign: call, CachedAt: time.Now()}
		}
		// Fail-open if no local DB (availability > strict security)
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
	s.server.RegisterHTTPHandler("/api/users.txt", s.handleUsersDBFile)
}

// handleUsersDBFile serves the local users.txt file for peers to download.
func (s *Service) handleUsersDBFile(w http.ResponseWriter, r *http.Request) {
	if s.radioIDAuth == nil {
		http.Error(w, "radioid not enabled", http.StatusNotFound)
		return
	}
	path := s.radioIDAuth.LocalDBPath()
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "users.txt not available", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, _ := os.Stat(path)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if info != nil {
		w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	}
	io.Copy(w, f)
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
