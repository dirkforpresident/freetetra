package service

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

// Position represents a decoded LIP position report.
type Position struct {
	ISSI      uint32    `json:"issi"`
	Lat       float64   `json:"lat"`
	Lon       float64   `json:"lon"`
	Timestamp time.Time `json:"timestamp"`
}

// PositionStore tracks positions for coverage mapping.
// In-memory: latest position per ISSI + recent history
// On disk: ALL positions in a JSONL file (one line per position)
type PositionStore struct {
	mu        sync.RWMutex
	positions map[uint32]*Position
	history   []Position
	logger    *log.Logger

	logFile string // path to JSONL append-only log
}

const maxPositionHistory = 500
const positionLogFile = "data/positions.jsonl"

func newPositionStore(logger *log.Logger) *PositionStore {
	ps := &PositionStore{
		positions: make(map[uint32]*Position),
		history:   make([]Position, 0, maxPositionHistory),
		logger:    logger,
		logFile:   positionLogFile,
	}
	// Ensure data dir exists
	_ = os.MkdirAll("data", 0755)
	return ps
}

// AppendToLog writes a position to the JSONL log file.
func (ps *PositionStore) appendToLog(p *Position) {
	if ps.logFile == "" {
		return
	}
	f, err := os.OpenFile(ps.logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	line, _ := json.Marshal(p)
	f.Write(line)
	f.Write([]byte("\n"))
}

// LoadAllFromLog reads all historical positions from the JSONL log.
func (ps *PositionStore) LoadAllFromLog() []Position {
	if ps.logFile == "" {
		return nil
	}
	f, err := os.Open(ps.logFile)
	if err != nil {
		return nil
	}
	defer f.Close()

	out := make([]Position, 0, 1000)
	dec := json.NewDecoder(f)
	for dec.More() {
		var p Position
		if err := dec.Decode(&p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// Update stores a new position for an ISSI.
func (ps *PositionStore) Update(issi uint32, lat, lon float64) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	now := time.Now()
	pos := &Position{
		ISSI:      issi,
		Lat:       lat,
		Lon:       lon,
		Timestamp: now,
	}
	ps.positions[issi] = pos
	ps.history = append(ps.history, *pos)
	if len(ps.history) > maxPositionHistory {
		ps.history = ps.history[len(ps.history)-maxPositionHistory:]
	}
	ps.appendToLog(pos)
	ps.logger.Printf("POSITION: ISSI=%d lat=%.6f lon=%.6f", issi, lat, lon)
}

// Latest returns the most recent position for each ISSI.
func (ps *PositionStore) Latest() []Position {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	out := make([]Position, 0, len(ps.positions))
	for _, p := range ps.positions {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.After(out[j].Timestamp)
	})
	return out
}

// History returns the last N position updates across all ISSIs.
func (ps *PositionStore) History() []Position {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	out := make([]Position, len(ps.history))
	copy(out, ps.history)
	// Reverse so newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// DecodeLIPCoords decodes a TETRA LIP (Location Information Protocol) position
// from SDS Type4 raw bytes. Returns (lat, lon, ok).
//
// LIP short report format (protocol_id = 0x0A):
//   Byte 0: 0x0A (LIP short location report PDU type)
//   Then: longitude (25 bits, signed) + latitude (24 bits, signed)
//   at various bit offsets depending on the LIP variant.
func DecodeLIPCoords(rawBytes []byte) (float64, float64, bool) {
	if len(rawBytes) < 8 {
		return 0, 0, false
	}
	if rawBytes[0] != 0x0A {
		return 0, 0, false
	}

	// Build big integer from bytes
	var bits uint64
	for _, b := range rawBytes {
		bits = (bits << 8) | uint64(b)
	}
	totalBits := len(rawBytes) * 8

	// Try different bit offsets for the coordinate fields
	for _, bitOff := range []int{12, 16, 8, 20, 24} {
		shiftLon := totalBits - bitOff - 25
		shiftLat := totalBits - bitOff - 25 - 24
		if shiftLat < 0 {
			continue
		}

		lonBits := int32((bits >> uint(shiftLon)) & 0x1FFFFFF)
		latBits := int32((bits >> uint(shiftLat)) & 0xFFFFFF)

		// Sign extension
		if lonBits&0x1000000 != 0 {
			lonBits -= 0x2000000
		}
		if latBits&0x800000 != 0 {
			latBits -= 0x1000000
		}

		lon := float64(lonBits) * 360.0 / math.Pow(2, 25)
		lat := float64(latBits) * 180.0 / math.Pow(2, 24)

		// Sanity check: Europe-ish coordinates
		if lat >= 35 && lat <= 72 && lon >= -25 && lon <= 45 {
			return lat, lon, true
		}
	}
	return 0, 0, false
}

// TryParseLIPFromSDS checks if an SDS payload contains a LIP position report
// and returns the decoded coordinates if so.
// The payload is the raw SDS data after the Brew frame header.
func TryParseLIPFromSDS(sdsData []byte) (float64, float64, bool) {
	if len(sdsData) < 2 {
		return 0, 0, false
	}

	protocolID := sdsData[0]

	// LIP reports can come as:
	// - Protocol ID 0x0A directly (simple LIP)
	// - Inside SDS-TL with protocol_id indicating location
	if protocolID == 0x0A {
		return DecodeLIPCoords(sdsData)
	}

	// SDS-TL wrapped: protocol_id 0x83 or 0x84 with LIP payload inside
	// Or Type4 SDS where the user data starts with 0x0A
	if len(sdsData) > 3 {
		// Skip protocol_id byte, check if rest starts with LIP marker
		if sdsData[1] == 0x0A {
			return DecodeLIPCoords(sdsData[1:])
		}
		// Some implementations wrap with additional headers
		for i := 0; i < len(sdsData)-8 && i < 4; i++ {
			if sdsData[i] == 0x0A {
				lat, lon, ok := DecodeLIPCoords(sdsData[i:])
				if ok {
					return lat, lon, true
				}
			}
		}
	}

	return 0, 0, false
}

// registerPositionHandlers adds HTTP handlers for position endpoints.
func (s *Service) registerPositionHandlers() {
	s.server.RegisterHTTPHandler("/api/positions", s.handlePositions)
	s.server.RegisterHTTPHandler("/api/positions/history", s.handlePositionHistory)
	s.server.RegisterHTTPHandler("/api/positions/push", s.handlePositionPush)
	s.server.RegisterHTTPHandler("/api/map", s.handleMapData)
	s.server.RegisterHTTPHandler("/map", s.handleMapPage)
}

// handleMapData returns ALL historical positions (from JSONL log) for the map.
func (s *Service) handleMapData(w http.ResponseWriter, r *http.Request) {
	all := s.positionStore.LoadAllFromLog()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=10")
	json.NewEncoder(w).Encode(map[string]any{
		"positions": all,
		"count":     len(all),
	})
}

func (s *Service) handleMapPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(coverageMapHTML))
}

const coverageMapHTML = `<!DOCTYPE html>
<html lang="de">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>FreeTetra Coverage Map</title>
<link rel="stylesheet" href="https://unpkg.com/leaflet@1.9.4/dist/leaflet.css">
<script src="https://unpkg.com/leaflet@1.9.4/dist/leaflet.js"></script>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: 'Inter', system-ui, sans-serif; background: #0a0d12; color: #e5e7eb; }
.header {
    padding: 14px 20px;
    background: #111827;
    border-bottom: 1px solid #1f2937;
    display: flex;
    justify-content: space-between;
    align-items: center;
}
.header h1 { font-size: 1.1rem; font-weight: 700; }
.header h1 span { color: #6ee7b7; }
.header .info { font-size: 0.85rem; color: #9ca3af; }
.header .info b { color: #6ee7b7; font-family: 'JetBrains Mono', monospace; }
#map { width: 100vw; height: calc(100vh - 50px); }
.leaflet-popup-content {
    color: #0a0d12;
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.82rem;
}
.leaflet-popup-content b { color: #047857; }
</style>
</head>
<body>

<div class="header">
    <h1>Free<span>Tetra</span> Coverage Map</h1>
    <div class="info">
        <b id="point-count">0</b> Positionen ·
        <b id="issi-count">0</b> Geraete ·
        <a href="/" style="color:#60a5fa;text-decoration:none">&larr; zur Startseite</a>
    </div>
</div>

<div id="map"></div>

<script>
const map = L.map("map", { worldCopyJump: true }).setView([51.5, 10.0], 6);

// Dark tiles
L.tileLayer("https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png", {
    attribution: '&copy; OpenStreetMap &copy; CartoDB',
    subdomains: "abcd",
    maxZoom: 19
}).addTo(map);

// Color per ISSI (deterministic)
function colorFor(issi) {
    const hue = (issi * 137) % 360;
    return "hsl(" + hue + ", 70%, 55%)";
}

let layerGroup = L.layerGroup().addTo(map);

async function loadPositions() {
    try {
        const r = await fetch("/api/map");
        const d = await r.json();
        const positions = d.positions || [];

        layerGroup.clearLayers();
        const issis = new Set();
        const bounds = [];

        for (const p of positions) {
            issis.add(p.issi);
            const color = colorFor(p.issi);
            const marker = L.circleMarker([p.lat, p.lon], {
                radius: 5,
                color: color,
                fillColor: color,
                fillOpacity: 0.7,
                weight: 1,
            });
            marker.bindPopup(
                "<b>ISSI " + p.issi + "</b><br>" +
                p.lat.toFixed(5) + ", " + p.lon.toFixed(5) + "<br>" +
                new Date(p.timestamp).toLocaleString("de-DE")
            );
            marker.addTo(layerGroup);
            bounds.push([p.lat, p.lon]);
        }

        document.getElementById("point-count").textContent = positions.length;
        document.getElementById("issi-count").textContent = issis.size;

        if (bounds.length > 0 && layerGroup.getLayers().length > 0) {
            // Only fit bounds on first load
            if (!window._fitDone) {
                map.fitBounds(bounds, { padding: [40, 40], maxZoom: 13 });
                window._fitDone = true;
            }
        }
    } catch (e) {
        console.error(e);
    }
}

loadPositions();
setInterval(loadPositions, 30000);
</script>

</body>
</html>`

// handlePositionPush accepts position reports from FreeTetra agents.
// Used by the Pi-side agent to forward LIP positions to the central server,
// which then forwards them to APRS-IS.
//
// POST /api/positions/push
// {"positions": [{"issi": 2623563, "lat": 53.42, "lon": 9.95}, ...]}
func (s *Service) handlePositionPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Positions []struct {
			ISSI uint32  `json:"issi"`
			Lat  float64 `json:"lat"`
			Lon  float64 `json:"lon"`
		} `json:"positions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	for _, p := range req.Positions {
		if p.ISSI == 0 {
			continue
		}
		s.positionStore.Update(p.ISSI, p.Lat, p.Lon)
		if s.aprsBridge != nil {
			go s.aprsBridge.SendPosition(p.ISSI, p.Lat, p.Lon)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "received": len(req.Positions)})
}

func (s *Service) handlePositions(w http.ResponseWriter, r *http.Request) {
	positions := s.positionStore.Latest()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]any{
		"positions": positions,
		"count":     len(positions),
		"server":    "FreeTetra",
	})
}

func (s *Service) handlePositionHistory(w http.ResponseWriter, r *http.Request) {
	history := s.positionStore.History()
	limit := 100
	if len(history) > limit {
		history = history[:limit]
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]any{
		"positions": history,
		"count":     len(history),
	})
}

// processSDSForPosition checks incoming SDS data for LIP position reports.
func (s *Service) processSDSForPosition(sourceISSI uint32, sdsData []byte) {
	if s.positionStore == nil {
		return
	}
	lat, lon, ok := TryParseLIPFromSDS(sdsData)
	if !ok {
		return
	}
	s.positionStore.Update(sourceISSI, lat, lon)
	s.recordActivity("position",
		fmt.Sprintf("ISSI=%d lat=%.4f lon=%.4f", sourceISSI, lat, lon),
		map[string]any{"issi": sourceISSI, "lat": lat, "lon": lon},
	)

	// Forward to APRS-IS
	if s.aprsBridge != nil {
		go s.aprsBridge.SendPosition(sourceISSI, lat, lon)
	}
}
