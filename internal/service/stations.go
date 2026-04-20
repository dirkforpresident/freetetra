package service

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Station is a user-declared FreeTetra station (hotspot / repeater / bluestation).
// Pushed from the Pi-side freetetra-agent via POST /api/stations/push.
type Station struct {
	StationID    string  `json:"station_id"`
	Callsign     string  `json:"callsign"`
	Type         string  `json:"type"` // hotspot | repeater | bluestation
	Lat          float64 `json:"lat"`
	Lon          float64 `json:"lon"`
	DLFreqMHz    float64 `json:"dl_freq"`
	ULFreqMHz    float64 `json:"ul_freq"`
	PowerW       float64 `json:"power_w"`
	Antenna      string  `json:"antenna"`
	Notes        string  `json:"notes"`
	Website      string  `json:"website"`
	LastSeenUnix int64   `json:"last_seen"`
	FirstSeenUnix int64  `json:"first_seen"`
}

// Online returns whether the station pushed within `window`.
func (st Station) Online(window time.Duration) bool {
	return time.Since(time.Unix(st.LastSeenUnix, 0)) < window
}

const stationsPath = "data/stations.json"
const stationOnlineWindow = 15 * time.Minute

type stationStore struct {
	mu       sync.RWMutex
	items    map[string]*Station
	logger   *log.Logger
}

func newStationStore(logger *log.Logger) *stationStore {
	s := &stationStore{items: make(map[string]*Station), logger: logger}
	s.load()
	return s
}

func (s *stationStore) load() {
	data, err := os.ReadFile(stationsPath)
	if err != nil {
		return
	}
	var list []*Station
	if err := json.Unmarshal(data, &list); err != nil {
		s.logger.Printf("stations: load: %v", err)
		return
	}
	for _, st := range list {
		if st.StationID != "" {
			s.items[st.StationID] = st
		}
	}
	s.logger.Printf("stations: loaded %d", len(s.items))
}

func (s *stationStore) save() {
	if err := os.MkdirAll(filepath.Dir(stationsPath), 0755); err != nil {
		s.logger.Printf("stations: mkdir: %v", err)
		return
	}
	list := make([]*Station, 0, len(s.items))
	for _, st := range s.items {
		list = append(list, st)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	tmp := stationsPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		s.logger.Printf("stations: write: %v", err)
		return
	}
	_ = os.Rename(tmp, stationsPath)
}

// Upsert creates or updates a station keyed by StationID. Returns the stored copy.
func (s *stationStore) Upsert(in Station) (*Station, error) {
	if strings.TrimSpace(in.StationID) == "" {
		return nil, fmt.Errorf("station_id required")
	}
	if strings.TrimSpace(in.Callsign) == "" {
		return nil, fmt.Errorf("callsign required")
	}
	if in.Lat < -90 || in.Lat > 90 || in.Lon < -180 || in.Lon > 180 {
		return nil, fmt.Errorf("invalid coordinates")
	}
	t := strings.ToLower(strings.TrimSpace(in.Type))
	if t != "hotspot" && t != "repeater" && t != "bluestation" {
		return nil, fmt.Errorf("type must be hotspot, repeater, or bluestation")
	}
	in.Type = t
	in.Callsign = strings.ToUpper(strings.TrimSpace(in.Callsign))

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	existing, ok := s.items[in.StationID]
	if ok {
		in.FirstSeenUnix = existing.FirstSeenUnix
	} else {
		in.FirstSeenUnix = now
	}
	in.LastSeenUnix = now
	s.items[in.StationID] = &in
	s.save()
	return &in, nil
}

func (s *stationStore) All() []Station {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Station, 0, len(s.items))
	for _, st := range s.items {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Callsign < out[j].Callsign })
	return out
}

// --- HTTP handlers ---

func (s *Service) handleStationPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if s.stationStore == nil {
		http.Error(w, "stations disabled", http.StatusServiceUnavailable)
		return
	}
	var in Station
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	saved, err := s.stationStore.Upsert(in)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "station": saved})
}

func (s *Service) handleStationsList(w http.ResponseWriter, r *http.Request) {
	type out struct {
		Station
		Online bool `json:"online"`
	}
	var list []out
	if s.stationStore != nil {
		for _, st := range s.stationStore.All() {
			list = append(list, out{Station: st, Online: st.Online(stationOnlineWindow)})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(map[string]any{"stations": list, "count": len(list)})
}

func (s *Service) registerStationHandlers() {
	s.server.RegisterHTTPHandler("/api/stations/push", s.handleStationPush)
	s.server.RegisterHTTPHandler("/api/stations", s.handleStationsList)
}
