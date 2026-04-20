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

// handleMapData returns hexagon aggregations or recent points based on zoom level.
//
// Query params:
//
//	res=5|7|9     H3 resolution (5=region, 7=city, 9=street)
//	bbox=lat1,lon1,lat2,lon2   bounding box filter
//	mode=hexes|points
func (s *Service) handleMapData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=10")

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "hexes"
	}

	if s.coverageDB == nil {
		// Fallback to JSONL
		all := s.positionStore.LoadAllFromLog()
		json.NewEncoder(w).Encode(map[string]any{"positions": all, "count": len(all)})
		return
	}

	if mode == "points" {
		samples, _ := s.coverageDB.RecentSamples(2000)
		json.NewEncoder(w).Encode(map[string]any{"positions": samples, "count": len(samples)})
		return
	}

	res := 7
	if rs := r.URL.Query().Get("res"); rs != "" {
		fmt.Sscanf(rs, "%d", &res)
	}

	hexes, err := s.coverageDB.AggregateHexes(res, nil, nil, nil, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	totalSamples, uniqueIssis := s.coverageDB.Stats()
	json.NewEncoder(w).Encode(map[string]any{
		"hexes":      hexes,
		"resolution": res,
		"stats": map[string]int{
			"total_samples": totalSamples,
			"unique_issis":  uniqueIssis,
		},
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
<script src="https://unpkg.com/h3-js@4.1.0/dist/h3-js.umd.js"></script>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: 'Inter', system-ui, sans-serif; transition: background 0.2s, color 0.2s; }

/* Light theme (default) */
body.light { background: #f9fafb; color: #1f2937; }
body.light .header { background: #ffffff; border-bottom: 1px solid #e5e7eb; }
body.light .header h1 span { color: #059669; }
body.light .header .info { color: #6b7280; }
body.light .header .info b { color: #059669; }
body.light .header a { color: #2563eb; }
body.light .legend { background: rgba(255, 255, 255, 0.95); border: 1px solid #e5e7eb; color: #1f2937; }
body.light .legend .scale { color: #6b7280; }
body.light .leaflet-popup-content-wrapper, body.light .leaflet-popup-tip { background: #ffffff; color: #1f2937; border: 1px solid #e5e7eb; }
body.light .leaflet-popup-content { color: #1f2937; }
body.light .leaflet-popup-content b { color: #059669; }
body.light .theme-btn { background: #f3f4f6; color: #1f2937; border: 1px solid #d1d5db; }

/* Dark theme */
body.dark { background: #0a0d12; color: #e5e7eb; }
body.dark .header { background: #111827; border-bottom: 1px solid #1f2937; }
body.dark .header h1 span { color: #6ee7b7; }
body.dark .header .info { color: #9ca3af; }
body.dark .header .info b { color: #6ee7b7; }
body.dark .header a { color: #60a5fa; }
body.dark .legend { background: rgba(17, 24, 39, 0.95); border: 1px solid #1f2937; color: #e5e7eb; }
body.dark .legend .scale { color: #9ca3af; }
body.dark .leaflet-popup-content-wrapper, body.dark .leaflet-popup-tip { background: #111827; color: #e5e7eb; border: 1px solid #1f2937; }
body.dark .leaflet-popup-content { color: #e5e7eb; }
body.dark .leaflet-popup-content b { color: #6ee7b7; }
body.dark .theme-btn { background: #1f2937; color: #e5e7eb; border: 1px solid #374151; }

.header {
    padding: 12px 20px;
    display: flex;
    justify-content: space-between;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
}
.header h1 { font-size: 1.05rem; font-weight: 700; }
.header .info { font-size: 0.82rem; display: flex; gap: 14px; align-items: center; }
.header .info b { font-family: 'JetBrains Mono', monospace; }
.header a { text-decoration: none; }
#map { width: 100vw; height: calc(100vh - 56px); }

.theme-btn {
    padding: 4px 10px;
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.78rem;
    font-family: inherit;
}

.legend {
    padding: 10px 14px;
    border-radius: 8px;
    font-size: 0.78rem;
    line-height: 1.6;
}
.legend .grad {
    height: 8px;
    border-radius: 4px;
    margin: 6px 0;
    background: linear-gradient(to right, #1e3a8a, #6ee7b7, #fbbf24, #ef4444);
}
.legend .scale { display: flex; justify-content: space-between; font-size: 0.7rem; }

.leaflet-popup-content {
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.78rem;
}
</style>
</head>
<body class="light">

<div class="header">
    <h1>Free<span>Tetra</span> Coverage Map</h1>
    <div class="info">
        <span><b id="stat-samples">0</b> Samples</span>
        <span><b id="stat-issis">0</b> Geräte</span>
        <span><b id="stat-hexes">0</b> Hexagone</span>
        <span>Res: <b id="stat-res">7</b></span>
        <button class="theme-btn" onclick="toggleTheme()" id="theme-toggle">🌙 Dark</button>
        <a href="/">&larr; Start</a>
    </div>
</div>

<div id="map"></div>

<script>
// CartoDB Voyager — much more readable than light_all (clear roads, labels, terrain)
const LIGHT_TILES = "https://{s}.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}{r}.png";
const DARK_TILES = "https://{s}.basemaps.cartocdn.com/rastertiles/dark_matter/{z}/{x}/{y}{r}.png";

const map = L.map("map", { worldCopyJump: true }).setView([51.5, 10.0], 6);

let tileLayer = L.tileLayer(LIGHT_TILES, {
    attribution: '&copy; OpenStreetMap &copy; CartoDB',
    subdomains: "abcd",
    maxZoom: 19
}).addTo(map);

function applyTheme(theme) {
    document.body.className = theme;
    map.removeLayer(tileLayer);
    tileLayer = L.tileLayer(theme === "dark" ? DARK_TILES : LIGHT_TILES, {
        attribution: '&copy; OpenStreetMap &copy; CartoDB',
        subdomains: "abcd",
        maxZoom: 19
    }).addTo(map);
    document.getElementById("theme-toggle").textContent = theme === "dark" ? "☀ Light" : "🌙 Dark";
    localStorage.setItem("freetetra-map-theme", theme);
}

function toggleTheme() {
    const current = document.body.className;
    applyTheme(current === "dark" ? "light" : "dark");
}

const savedTheme = localStorage.getItem("freetetra-map-theme") || "light";
if (savedTheme === "dark") applyTheme("dark");

// Legend
const legend = L.control({ position: "bottomright" });
legend.onAdd = function() {
    const div = L.DomUtil.create("div", "legend");
    div.innerHTML =
        "<div><b>Coverage Density</b></div>" +
        "<div class=\"grad\"></div>" +
        "<div class=\"scale\"><span>1</span><span>10</span><span>100</span><span>1000+</span></div>" +
        "<div style=\"margin-top:6px;color:#9ca3af\">Auto-Resolution nach Zoom</div>";
    return div;
};
legend.addTo(map);

// Color scale: blue (low) → green → yellow → red (high)
function densityColor(count, max) {
    const t = Math.log10(count + 1) / Math.log10(max + 1);
    if (t < 0.33) {
        const k = t / 0.33;
        return interpolateRGB([30, 58, 138], [110, 231, 183], k);
    } else if (t < 0.66) {
        const k = (t - 0.33) / 0.33;
        return interpolateRGB([110, 231, 183], [251, 191, 36], k);
    } else {
        const k = Math.min(1, (t - 0.66) / 0.34);
        return interpolateRGB([251, 191, 36], [239, 68, 68], k);
    }
}

function interpolateRGB(a, b, t) {
    const r = Math.round(a[0] + (b[0] - a[0]) * t);
    const g = Math.round(a[1] + (b[1] - a[1]) * t);
    const bl = Math.round(a[2] + (b[2] - a[2]) * t);
    return "rgb(" + r + "," + g + "," + bl + ")";
}

function resolutionForZoom(zoom) {
    if (zoom < 8) return 5;   // ~8.5 km hexes
    if (zoom < 12) return 7;  // ~1.2 km hexes
    return 9;                  // ~174 m hexes
}

let hexLayer = L.layerGroup().addTo(map);
let firstLoad = true;

async function loadHexes() {
    const zoom = map.getZoom();
    const res = resolutionForZoom(zoom);

    try {
        const r = await fetch("/api/map?res=" + res);
        const d = await r.json();
        const hexes = d.hexes || [];

        hexLayer.clearLayers();

        const maxCount = hexes.reduce((m, h) => Math.max(m, h.n), 1);
        const bounds = [];

        for (const h of hexes) {
            // Get hexagon polygon from H3
            const boundary = h3.cellToBoundary(h.h, false);
            const color = densityColor(h.n, maxCount);
            const polygon = L.polygon(boundary, {
                color: "#1f2937",
                weight: 1.5,
                opacity: 0.9,
                fillColor: color,
                fillOpacity: 0.75,
            });
            polygon.bindPopup(
                "<b>" + h.n + " Sample(s)</b><br>" +
                h.u + " Gerät(e)<br>" +
                "Resolution: " + h.r + "<br>" +
                h.lat.toFixed(5) + ", " + h.lon.toFixed(5) +
                (h.rssi != null ? "<br>Avg RSSI: " + h.rssi + " dBm" : "")
            );
            polygon.addTo(hexLayer);
            bounds.push([h.lat, h.lon]);
        }

        document.getElementById("stat-samples").textContent = (d.stats?.total_samples ?? 0).toLocaleString("de-DE");
        document.getElementById("stat-issis").textContent = d.stats?.unique_issis ?? 0;
        document.getElementById("stat-hexes").textContent = hexes.length;
        document.getElementById("stat-res").textContent = res;

        if (firstLoad && bounds.length > 0) {
            map.fitBounds(bounds, { padding: [40, 40], maxZoom: 11 });
            firstLoad = false;
        }
    } catch (e) {
        console.error(e);
    }
}

loadHexes();
map.on("zoomend", loadHexes);
setInterval(loadHexes, 30000);
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
		if s.coverageDB != nil {
			_ = s.coverageDB.Insert(p.ISSI, p.Lat, p.Lon, nil, nil)
		}
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
	if s.coverageDB != nil {
		_ = s.coverageDB.Insert(sourceISSI, lat, lon, nil, nil)
	}
	s.recordActivity("position",
		fmt.Sprintf("ISSI=%d lat=%.4f lon=%.4f", sourceISSI, lat, lon),
		map[string]any{"issi": sourceISSI, "lat": lat, "lon": lon},
	)

	// Forward to APRS-IS
	if s.aprsBridge != nil {
		go s.aprsBridge.SendPosition(sourceISSI, lat, lon)
	}
}
