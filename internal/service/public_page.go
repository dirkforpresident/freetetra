package service

import (
	"encoding/json"
	"net/http"
	"time"
)

// registerPublicHandlers adds HTTP handlers for the public landing page.
// The landing HTML itself is served by the Vue SPA via registerSPAFallback
// at "/"; this function now only registers the JSON API.
func (s *Service) registerPublicHandlers() {
	s.server.RegisterHTTPHandler("/api/public/status", s.handlePublicStatus)
}

func (s *Service) handlePublicStatus(w http.ResponseWriter, r *http.Request) {
	clients := s.server.SnapshotClients()

	// Repeater count + subscriber count come from BlueStation telemetry (most accurate).
	// Falls back to heartbeat API for custom clients.
	repeaterCount := 0
	subscriberCount := 0
	if s.telemetry != nil && s.telemetry.ActiveCount() > 0 {
		repeaterCount = s.telemetry.ActiveCount()
		subscriberCount = s.telemetry.TotalSubscribers()
	} else if s.repeaters != nil {
		repeaterCount = s.repeaters.ActiveCount()
		subscriberCount = s.repeaters.TotalSubscribers()
	}
	_ = clients

	positions := s.positionStore.Latest()

	serverName := "FreeTetra"
	if s.cfg.Federation.Name != "" {
		serverName = "FreeTetra " + s.cfg.Federation.Name
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=5")
	json.NewEncoder(w).Encode(map[string]any{
		"server":      serverName,
		"version":     "1.0",
		"uptime":      time.Since(startTime).String(),
		"repeaters":   repeaterCount,
		"subscribers": subscriberCount,
		"positions":   len(positions),
	})
}

var startTime = time.Now()

