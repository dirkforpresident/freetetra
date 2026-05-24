package service

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
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

	// Default time-decay: 90 Tage. Param "days=N" oder "days=0" fuer alles.
	days := 90
	if d := r.URL.Query().Get("days"); d != "" {
		fmt.Sscanf(d, "%d", &days)
	}
	var sinceTs int64 = 0
	if days > 0 {
		sinceTs = time.Now().Unix() - int64(days)*86400
	}

	hexes, err := s.coverageDB.AggregateHexes(res, nil, nil, nil, nil, sinceTs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	totalSamples, uniqueIssis := s.coverageDB.Stats()
	devices24h := s.coverageDB.Devices24h()

	// TMO-Site-Stats aus dem stationStore (online = letzter Push < 15 min)
	var sitesOnline, sitesTotal int
	if s.stationStore != nil {
		for _, st := range s.stationStore.All() {
			sitesTotal++
			if st.Online(stationOnlineWindow) {
				sitesOnline++
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]any{
		"hexes":      hexes,
		"resolution": res,
		"stats": map[string]int{
			"total_samples":    totalSamples,
			"unique_issis":     uniqueIssis,
			"devices_24h":      devices24h,
			"tmo_sites_online": sitesOnline,
			"tmo_sites_total":  sitesTotal,
		},
	})
}

func (s *Service) handleMapPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(s.renderMapPage(detectLang(r))))
}

func (s *Service) renderMapPage(lang Lang) string {
	out := translate(coverageMapHTML, lang)
	return strings.NewReplacer(
		"{{LANG_HTML_ATTR}}", string(lang),
		"{{LANG_SWITCH}}", langSwitchHTML(lang),
	).Replace(out)
}

const coverageMapHTML = `<!DOCTYPE html>
<html lang="{{LANG_HTML_ATTR}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{T:map.title}}</title>
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
.header .stat-group { display: inline-flex; align-items: center; gap: 6px; }
.live-dot {
    display: inline-block; width: 8px; height: 8px; border-radius: 50%;
    background: #10b981; animation: pulse 2s infinite;
}
.live-dot.stale { background: #9ca3af; animation: none; }
@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.35; } }
.header a { text-decoration: none; }
#map { width: 100vw; height: calc(100vh - 56px); }

.theme-btn {
    padding: 4px 10px;
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.78rem;
    font-family: inherit;
}

.filter-group { display: inline-flex; gap: 4px; align-items: center; margin-left: 8px; }
.filter-btn {
    background: transparent; border: 1px solid currentColor;
    color: inherit; padding: 3px 8px; border-radius: 4px;
    font-size: 0.72rem; cursor: pointer;
    font-family: 'JetBrains Mono', monospace;
    transition: all 0.15s;
}
.filter-btn:hover { background: rgba(5,150,105,0.1); }
.filter-btn.active { background: #059669; color: white; border-color: #059669; }

.lang-toggle-inline { display: inline-flex; gap: 4px; align-items: center; margin-left: 8px; font-family: 'JetBrains Mono', monospace; font-size: 0.72rem; }
.lang-link { color: inherit; text-decoration: none; opacity: 0.6; padding: 2px 4px; }
.lang-link:hover { opacity: 1; }
.lang-link.lang-active { opacity: 1; font-weight: 700; color: #059669; }

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

/* Mobile */
@media (max-width: 640px) {
    .header { padding: 8px 12px; gap: 6px; }
    .header h1 { font-size: 0.95rem; flex-basis: 100%; }
    .header .info { font-size: 0.72rem; gap: 10px; flex-wrap: wrap; }
    .header .info .hide-sm { display: none; }
    .theme-btn { padding: 3px 8px; font-size: 0.72rem; }
    #map { height: calc(100vh - 78px); }
    .legend { font-size: 0.7rem; padding: 8px 10px; }
    .leaflet-control-zoom a { width: 32px !important; height: 32px !important; line-height: 32px !important; font-size: 18px !important; }
    .leaflet-bar a { width: 32px !important; height: 32px !important; line-height: 32px !important; }
}
@media (max-width: 380px) {
    .header h1 { font-size: 0.9rem; }
    .header .info .hide-xs { display: none; }
}
</style>
</head>
<body class="light">

<div class="header">
    <h1>Free<span>Tetra</span> Map</h1>
    <div class="info">
        <span class="stat-group"><span class="live-dot" id="online-dot"></span><b id="stat-tmo-sites">0/0</b> {{T:map.tmo_site}}</span>
        <span><b id="stat-devices">0</b> {{T:map.devices}}</span>
        <span class="filter-group" title="{{T:map.filter_title}}">
            <button class="filter-btn" data-days="7">7d</button>
            <button class="filter-btn" data-days="30">30d</button>
            <button class="filter-btn active" data-days="90">90d</button>
            <button class="filter-btn" data-days="0">{{T:map.filter_all}}</button>
        </span>
        <button class="theme-btn" onclick="toggleTheme()" id="theme-toggle">🌙 Dark</button>
        <span class="lang-toggle-inline">{{LANG_SWITCH}}</span>
        <a href="/">{{T:common.back_to_start}}</a>
    </div>
</div>

<div id="map"></div>

<script>
// CartoDB Voyager (light, default) and Dark Matter (dark)
const LIGHT_TILES = "https://{s}.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}{r}.png";
const DARK_TILES = "https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png";

const DEFAULT_CENTER = [51.5, 10.0];
const DEFAULT_ZOOM = 6;

// Parse URL hash like "#10/53.42/9.95"
function parseUrlHash() {
    const m = window.location.hash.match(/^#(\d+(?:\.\d+)?)\/(-?\d+(?:\.\d+)?)\/(-?\d+(?:\.\d+)?)/);
    if (!m) return null;
    return { zoom: parseFloat(m[1]), lat: parseFloat(m[2]), lng: parseFloat(m[3]) };
}

function loadSavedView() {
    try { return JSON.parse(localStorage.getItem("freetetra-map-view")); } catch(e) { return null; }
}

const map = L.map("map", { worldCopyJump: true });

// Initial view priority: URL hash > saved view > geolocation > default
const urlView = parseUrlHash();
const savedView = loadSavedView();
let userSetView = false;

if (urlView) {
    map.setView([urlView.lat, urlView.lng], urlView.zoom);
    userSetView = true;
} else if (savedView) {
    map.setView([savedView.lat, savedView.lng], savedView.zoom);
    userSetView = true;
} else {
    map.setView(DEFAULT_CENTER, DEFAULT_ZOOM);
    if (navigator.geolocation) {
        navigator.geolocation.getCurrentPosition(
            (pos) => {
                if (!userSetView) {
                    map.flyTo([pos.coords.latitude, pos.coords.longitude], 10);
                    userSetView = true;
                }
            },
            () => {},
            { timeout: 4000, enableHighAccuracy: false, maximumAge: 60000 }
        );
    }
}

// Save view on pan/zoom + sync URL hash
map.on("moveend zoomend", () => {
    const c = map.getCenter();
    const z = map.getZoom();
    localStorage.setItem("freetetra-map-view", JSON.stringify({ lat: c.lat, lng: c.lng, zoom: z }));
    const hash = "#" + z + "/" + c.lat.toFixed(4) + "/" + c.lng.toFixed(4);
    if (window.location.hash !== hash) history.replaceState(null, "", hash);
});

// "Meine Position" button
const locateCtrl = L.control({ position: "topleft" });
locateCtrl.onAdd = function() {
    const div = L.DomUtil.create("div", "leaflet-bar leaflet-control");
    const a = L.DomUtil.create("a", "", div);
    a.href = "#";
    a.title = "Meine Position";
    a.innerHTML = "&#127758;";
    a.style.fontSize = "16px";
    a.style.lineHeight = "26px";
    a.style.textAlign = "center";
    L.DomEvent.on(a, "click", function(e) {
        L.DomEvent.preventDefault(e);
        L.DomEvent.stopPropagation(e);
        if (!navigator.geolocation) return;
        a.innerHTML = "&#8987;";
        navigator.geolocation.getCurrentPosition(
            (pos) => { a.innerHTML = "&#127758;"; map.flyTo([pos.coords.latitude, pos.coords.longitude], 12); },
            () => { a.innerHTML = "&#10060;"; setTimeout(() => a.innerHTML = "&#127758;", 1500); },
            { timeout: 8000, enableHighAccuracy: true }
        );
    });
    return div;
};
locateCtrl.addTo(map);

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


const HEX_COLOR = "#10b981"; // FreeTetra accent green
const ONLINE_WINDOW_S = 15 * 60; // sample newer than 15 min ⇒ "online"

const I18N = {
    online:     '{{T:map.online}}',
    offline:    '{{T:map.offline}}',
    samples:    '{{T:map.samples}}',
    device_one: '{{T:map.device_one}}',
    device_many:'{{T:map.device_many}}',
    from:       '{{T:map.from}}',
    last:       '{{T:map.last}}',
    ago_s:      '{{T:map.ago_s}}',
    ago_min:    '{{T:map.ago_min}}',
    ago_h:      '{{T:map.ago_h}}',
    ago_d:      '{{T:map.ago_d}}',
};
function timeAgo(tsSec) {
    if (!tsSec) return "—";
    const diff = Math.max(0, Math.floor(Date.now() / 1000) - tsSec);
    if (diff < 60) return I18N.ago_s.replace('%d', diff);
    if (diff < 3600) return I18N.ago_min.replace('%d', Math.floor(diff / 60));
    if (diff < 86400) return I18N.ago_h.replace('%d', Math.floor(diff / 3600));
    return I18N.ago_d.replace('%d', Math.floor(diff / 86400));
}

function escapeHexHtml(s) { return String(s ?? "").replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }

function hexPopup(h) {
    const now = Math.floor(Date.now() / 1000);
    const online = h.t && (now - h.t) < ONLINE_WINDOW_S;
    const dot = online
        ? '<span style="color:#10b981;font-weight:600">&#9679; ' + I18N.online + '</span>'
        : '<span style="color:#9ca3af">&#9675; ' + I18N.offline + '</span>';
    const devWord = h.u === 1 ? I18N.device_one : I18N.device_many;
    const rows = [];
    rows.push("<div style='font-size:13px;font-weight:600;color:#059669'>" + h.n + " " + I18N.samples + " &middot; " + h.u + " " + devWord + "</div>");
    if (h.rp && h.rp.length) {
        rows.push("<div style='font-size:12px;margin-top:4px'>" + I18N.from + ": " + h.rp.map(escapeHexHtml).join(", ") + "</div>");
    }
    rows.push("<div style='font-size:12px;color:#6b7280;margin-top:4px'>" + I18N.last + ": " + timeAgo(h.t) + "</div>");
    rows.push("<div style='margin-top:6px'>" + dot + "</div>");
    return rows.join("");
}

function resolutionForZoom(zoom) {
    if (zoom < 8) return 5;   // ~8.5 km hexes
    if (zoom < 12) return 7;  // ~1.2 km hexes
    return 9;                  // ~174 m hexes
}

let hexLayer = L.layerGroup().addTo(map);
let firstLoad = true;

// Zeitraum-Filter (Default 90 Tage). Wird in localStorage gespeichert.
let activeDays = parseInt(localStorage.getItem("ft-map-days") ?? "90", 10);
function applyFilterButtons() {
    document.querySelectorAll(".filter-btn").forEach(b => {
        b.classList.toggle("active", parseInt(b.dataset.days, 10) === activeDays);
    });
}
document.querySelectorAll(".filter-btn").forEach(b => {
    b.addEventListener("click", () => {
        activeDays = parseInt(b.dataset.days, 10);
        localStorage.setItem("ft-map-days", String(activeDays));
        applyFilterButtons();
        loadHexes();
    });
});
applyFilterButtons();

async function loadHexes() {
    const zoom = map.getZoom();
    const res = resolutionForZoom(zoom);

    try {
        const r = await fetch("/api/map?res=" + res + "&days=" + activeDays);
        const d = await r.json();
        const hexes = d.hexes || [];

        hexLayer.clearLayers();

        const maxCount = hexes.reduce((m, h) => Math.max(m, h.n), 1);
        const bounds = [];

        for (const h of hexes) {
            const boundary = h3.cellToBoundary(h.h, false);
            const polygon = L.polygon(boundary, {
                color: "#047857",
                weight: 1.5,
                opacity: 0.9,
                fillColor: HEX_COLOR,
                fillOpacity: 0.5,
            });
            polygon.bindPopup(hexPopup(h));
            polygon.addTo(hexLayer);
            bounds.push([h.lat, h.lon]);
        }

        const s = d.stats || {};
        const online = s.tmo_sites_online ?? 0;
        const total = s.tmo_sites_total ?? 0;
        document.getElementById("stat-tmo-sites").textContent = online + "/" + total;
        document.getElementById("stat-devices").textContent = s.devices_24h ?? 0;
        document.getElementById("online-dot").classList.toggle("stale", online === 0);

        if (firstLoad && bounds.length > 0 && !userSetView) {
            map.fitBounds(bounds, { padding: [40, 40], maxZoom: 11 });
            userSetView = true;
        }
        firstLoad = false;
    } catch (e) {
        console.error(e);
    }
}

// --- FreeTetra stations (TMO-Site/Hotspot/BlueStation) ---
const stationLayer = L.layerGroup().addTo(map);

function stationIcon(type, online) {
    const color = online ? "#10b981" : "#9ca3af";
    const symbol = type === "hotspot" ? "H" : type === "tmo_site" ? "T" : "B";
    const html = '<div style="width:28px;height:28px;border-radius:50%;background:' + color +
        ';border:3px solid #fff;box-shadow:0 2px 6px rgba(0,0,0,0.35);color:#fff;font-weight:700;font-family:monospace;font-size:13px;display:flex;align-items:center;justify-content:center;">' + symbol + '</div>';
    return L.divIcon({ className: "station-icon", html, iconSize: [28, 28], iconAnchor: [14, 14] });
}

function escapeHtml(s) { return String(s ?? "").replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }

function stationPopup(st) {
    const typeName = st.type === "hotspot" ? "Hotspot" : st.type === "tmo_site" ? "TMO-Site" : "BlueStation";
    const dot = st.online ? '<span style="color:#10b981">&#9679; online</span>' : '<span style="color:#9ca3af">&#9675; offline</span>';
    const rows = [];
    rows.push("<b style='font-size:14px'>" + escapeHtml(st.callsign) + "</b> &middot; " + dot);
    rows.push(typeName);
    if (st.dl_freq) rows.push("DL: " + st.dl_freq.toFixed(4) + " MHz");
    if (st.ul_freq) rows.push("UL: " + st.ul_freq.toFixed(4) + " MHz");
    if (st.power_w) rows.push(st.power_w + " W");
    if (st.antenna) rows.push("Antenne: " + escapeHtml(st.antenna));
    if (st.notes) rows.push("<span style='color:#6b7280'>" + escapeHtml(st.notes) + "</span>");
    if (st.website) rows.push("<a href='" + escapeHtml(st.website) + "' target='_blank' rel='noopener'>Website</a>");
    return rows.join("<br>");
}

async function loadStations() {
    try {
        const r = await fetch("/api/stations");
        const d = await r.json();
        stationLayer.clearLayers();
        for (const st of (d.stations || [])) {
            const m = L.marker([st.lat, st.lon], { icon: stationIcon(st.type, st.online) });
            m.bindPopup(stationPopup(st));
            m.addTo(stationLayer);
        }
    } catch (e) { console.error(e); }
}

loadHexes();
loadStations();
map.on("zoomend", loadHexes);
setInterval(loadStations, 60000);
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
		TMOSite   string `json:"tmo_site"`
		Positions []struct {
			ISSI    uint32  `json:"issi"`
			Lat     float64 `json:"lat"`
			Lon     float64 `json:"lon"`
			TMOSite string  `json:"tmo_site"`
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
		site := p.TMOSite
		if site == "" {
			site = req.TMOSite
		}
		s.positionStore.Update(p.ISSI, p.Lat, p.Lon)
		if s.coverageDB != nil {
			_ = s.coverageDB.Insert(p.ISSI, p.Lat, p.Lon, nil, nil, site)
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
	// TMO-Site-Tag = lokaler Server-Name (FEDERATION_NAME). Bei Federation-
	// incoming Samples wird der Peer-Name als Tag verwendet.
	tmoSite := s.cfg.Federation.Name
	s.positionStore.Update(sourceISSI, lat, lon)
	if s.coverageDB != nil {
		_ = s.coverageDB.Insert(sourceISSI, lat, lon, nil, nil, tmoSite)
	}
	s.recordActivity("position",
		fmt.Sprintf("ISSI=%d lat=%.4f lon=%.4f", sourceISSI, lat, lon),
		map[string]any{"issi": sourceISSI, "lat": lat, "lon": lon},
	)

	// Federation-Sync: an alle Peers schicken
	if s.federation != nil {
		s.federation.NotifyPositionSample(sourceISSI, lat, lon, tmoSite)
	}

	// Forward to APRS-IS
	if s.aprsBridge != nil {
		go s.aprsBridge.SendPosition(sourceISSI, lat, lon)
	}
}
