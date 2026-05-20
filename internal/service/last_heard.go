package service

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// LastHeardEntry is a single call event for the public /live view.
type LastHeardEntry struct {
	CallID     string    `json:"call_id"`
	SourceISSI uint32    `json:"source_issi"`
	Callsign   string    `json:"callsign,omitempty"`
	Name       string    `json:"name,omitempty"`
	DestGSSI   uint32    `json:"dest_gssi"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	DurationMs int64     `json:"duration_ms"`
	Origin     string    `json:"origin,omitempty"` // "subscriber", "injected", "peer"
}

// LastHeardBuffer keeps the last N call events in memory.
type LastHeardBuffer struct {
	mu      sync.RWMutex
	entries []LastHeardEntry
	maxLen  int
	byID    map[string]int // call_id -> index in entries
}

func newLastHeardBuffer(maxLen int) *LastHeardBuffer {
	return &LastHeardBuffer{
		entries: make([]LastHeardEntry, 0, maxLen),
		maxLen:  maxLen,
		byID:    make(map[string]int),
	}
}

// isServiceISSI returns true for ISSIs reserved fuer Service-Bots
// (Webradio, Echo, SDS-Inject, etc.) — die sollen nicht im Last-Heard
// erscheinen, sonst spammen sie den Feed zu. Echte Funkamateure haben
// laenderspezifische ISSIs (DL z.B. 262XXXX), nie >= 900000.
func isServiceISSI(issi uint32) bool {
	return issi >= 900000 && issi <= 999999
}

// ignoredLastHeardGSSIs sind Talkgroups die nicht im Last-Heard auftauchen
// sollen — typischerweise Status/LIP-Pseudo-Calls von Funkgeraeten die
// Position-Updates via Circuit Mode senden (statt SDS).
var ignoredLastHeardGSSIs = map[uint32]struct{}{
	262999: {}, // DO1XX/andere: TETRA-Funkgeraet LIP-Status-Updates
}

func isIgnoredGSSI(gssi uint32) bool {
	_, ok := ignoredLastHeardGSSIs[gssi]
	return ok
}

// Start records a new call. If a call with the same ID exists, it's left untouched.
func (b *LastHeardBuffer) Start(callID uuid.UUID, sourceISSI, destGSSI uint32, origin string) {
	if isServiceISSI(sourceISSI) || isIgnoredGSSI(destGSSI) {
		return
	}
	id := callID.String()
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.byID[id]; ok {
		return
	}
	entry := LastHeardEntry{
		CallID:     id,
		SourceISSI: sourceISSI,
		DestGSSI:   destGSSI,
		StartedAt:  time.Now().UTC(),
		Origin:     origin,
	}
	b.entries = append(b.entries, entry)
	if len(b.entries) > b.maxLen {
		// Drop oldest, rebuild byID index
		drop := len(b.entries) - b.maxLen
		b.entries = b.entries[drop:]
		b.byID = make(map[string]int, len(b.entries))
		for i, e := range b.entries {
			b.byID[e.CallID] = i
		}
	}
	b.byID[id] = len(b.entries) - 1
}

// End finalizes a call entry with end-time + duration.
func (b *LastHeardBuffer) End(callID uuid.UUID) {
	id := callID.String()
	b.mu.Lock()
	defer b.mu.Unlock()
	idx, ok := b.byID[id]
	if !ok {
		return
	}
	now := time.Now().UTC()
	b.entries[idx].EndedAt = &now
	b.entries[idx].DurationMs = now.Sub(b.entries[idx].StartedAt).Milliseconds()
}

// Snapshot returns a copy of the buffer, newest first.
func (b *LastHeardBuffer) Snapshot() []LastHeardEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]LastHeardEntry, len(b.entries))
	// reverse-copy so newest is at index 0
	for i, e := range b.entries {
		out[len(b.entries)-1-i] = e
	}
	return out
}

// EnrichCallsigns fills in Callsign from the RadioID lookup for each entry.
func (b *LastHeardBuffer) EnrichCallsigns(lookup func(issi uint32) string) []LastHeardEntry {
	entries := b.Snapshot()
	for i := range entries {
		if cs := lookup(entries[i].SourceISSI); cs != "" {
			entries[i].Callsign = cs
		}
	}
	return entries
}

func (s *Service) handleLastHeardAPI(w http.ResponseWriter, r *http.Request) {
	if s.lastHeard == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"entries":[],"count":0}`))
		return
	}
	entries := s.lastHeard.EnrichCallsigns(s.lookupCallsign)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=2")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"entries": entries,
		"count":   len(entries),
		"now":     time.Now().UTC(),
	})
}

// lookupCallsign returns the callsign for a given ISSI via RadioID, or "" if unknown.
func (s *Service) lookupCallsign(issi uint32) string {
	if s.radioIDAuth == nil {
		return ""
	}
	cs, ok := s.radioIDAuth.Verify(issi)
	if !ok {
		return ""
	}
	return cs
}
