<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import L from "leaflet";
import "leaflet/dist/leaflet.css";
import { cellToBoundary } from "h3-js";
import { api, type HexCell, type Station } from "../api";
import LangSwitch from "../components/LangSwitch.vue";

const { t } = useI18n();

const LIGHT_TILES = "https://{s}.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}{r}.png";
const DARK_TILES = "https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png";
const DEFAULT_CENTER: [number, number] = [51.5, 10.0];
const DEFAULT_ZOOM = 6;
const HEX_COLOR = "#10b981";
const ONLINE_WINDOW_S = 15 * 60;

const mapEl = ref<HTMLDivElement | null>(null);

const statRepeaters = ref("0/0");
const statDevices = ref<number | string>(0);
const onlineStale = ref(true);
const activeDays = ref<number>(parseInt(localStorage.getItem("ft-map-days") ?? "90", 10));
const theme = ref<"light" | "dark">(
  (localStorage.getItem("freetetra-map-theme") as "light" | "dark") || "light",
);

let map: L.Map | null = null;
let tileLayer: L.TileLayer | null = null;
let hexLayer: L.LayerGroup | null = null;
let stationLayer: L.LayerGroup | null = null;
let firstLoad = true;
let userSetView = false;
let stationsTimer: ReturnType<typeof setInterval> | null = null;
let hexesTimer: ReturnType<typeof setInterval> | null = null;

function parseUrlHash(): { zoom: number; lat: number; lng: number } | null {
  const m = window.location.hash.match(/^#(\d+(?:\.\d+)?)\/(-?\d+(?:\.\d+)?)\/(-?\d+(?:\.\d+)?)/);
  if (!m) return null;
  return { zoom: parseFloat(m[1]), lat: parseFloat(m[2]), lng: parseFloat(m[3]) };
}
function loadSavedView(): { lat: number; lng: number; zoom: number } | null {
  try {
    return JSON.parse(localStorage.getItem("freetetra-map-view") ?? "null");
  } catch {
    return null;
  }
}

function resolutionForZoom(zoom: number): number {
  if (zoom < 8) return 5;
  if (zoom < 12) return 7;
  return 9;
}

function escapeHtml(s: unknown): string {
  return String(s ?? "").replace(/[&<>"']/g, (c) => {
    const map: Record<string, string> = {
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#39;",
    };
    return map[c] ?? c;
  });
}

function timeAgo(tsSec?: number): string {
  if (!tsSec) return "—";
  const diff = Math.max(0, Math.floor(Date.now() / 1000) - tsSec);
  if (diff < 60) return t("map.ago_s").replace("%d", String(diff));
  if (diff < 3600) return t("map.ago_min").replace("%d", String(Math.floor(diff / 60)));
  if (diff < 86400) return t("map.ago_h").replace("%d", String(Math.floor(diff / 3600)));
  return t("map.ago_d").replace("%d", String(Math.floor(diff / 86400)));
}

function hexPopup(h: HexCell): string {
  const now = Math.floor(Date.now() / 1000);
  const online = h.t && now - h.t < ONLINE_WINDOW_S;
  const dot = online
    ? '<span style="color:#10b981;font-weight:600">&#9679; ' + t("map.online") + "</span>"
    : '<span style="color:#9ca3af">&#9675; ' + t("map.offline") + "</span>";
  const devWord = h.u === 1 ? t("map.device_one") : t("map.device_many");
  const rows: string[] = [];
  rows.push(
    "<div style='font-size:13px;font-weight:600;color:#059669'>" +
      h.n +
      " " +
      t("map.samples") +
      " &middot; " +
      h.u +
      " " +
      devWord +
      "</div>",
  );
  if (h.rp && h.rp.length) {
    rows.push(
      "<div style='font-size:12px;margin-top:4px'>" +
        t("map.from") +
        ": " +
        h.rp.map(escapeHtml).join(", ") +
        "</div>",
    );
  }
  rows.push(
    "<div style='font-size:12px;color:#6b7280;margin-top:4px'>" +
      t("map.last") +
      ": " +
      timeAgo(h.t) +
      "</div>",
  );
  rows.push("<div style='margin-top:6px'>" + dot + "</div>");
  return rows.join("");
}

function stationIcon(stationType: Station["type"], online: boolean): L.DivIcon {
  const color = online ? "#10b981" : "#9ca3af";
  const symbol = stationType === "hotspot" ? "H" : stationType === "repeater" ? "R" : "B";
  const html =
    '<div style="width:28px;height:28px;border-radius:50%;background:' +
    color +
    ';border:3px solid #fff;box-shadow:0 2px 6px rgba(0,0,0,0.35);color:#fff;font-weight:700;font-family:monospace;font-size:13px;display:flex;align-items:center;justify-content:center;">' +
    symbol +
    "</div>";
  return L.divIcon({ className: "station-icon", html, iconSize: [28, 28], iconAnchor: [14, 14] });
}

function stationPopup(st: Station): string {
  const typeName =
    st.type === "hotspot" ? "Hotspot" : st.type === "repeater" ? "Repeater" : "BlueStation";
  const dot = st.online
    ? '<span style="color:#10b981">&#9679; online</span>'
    : '<span style="color:#9ca3af">&#9675; offline</span>';
  const rows: string[] = [];
  rows.push("<b style='font-size:14px'>" + escapeHtml(st.callsign) + "</b> &middot; " + dot);
  rows.push(typeName);
  if (st.dl_freq) rows.push("DL: " + st.dl_freq.toFixed(4) + " MHz");
  if (st.ul_freq) rows.push("UL: " + st.ul_freq.toFixed(4) + " MHz");
  if (st.power_w) rows.push(st.power_w + " W");
  if (st.antenna) rows.push("Antenne: " + escapeHtml(st.antenna));
  if (st.notes) rows.push("<span style='color:#6b7280'>" + escapeHtml(st.notes) + "</span>");
  if (st.website)
    rows.push("<a href='" + escapeHtml(st.website) + "' target='_blank' rel='noopener'>Website</a>");
  return rows.join("<br>");
}

function applyTheme(next: "light" | "dark") {
  if (!map) return;
  theme.value = next;
  document.body.className = next;
  if (tileLayer) map.removeLayer(tileLayer);
  tileLayer = L.tileLayer(next === "dark" ? DARK_TILES : LIGHT_TILES, {
    attribution: "&copy; OpenStreetMap &copy; CartoDB",
    subdomains: "abcd",
    maxZoom: 19,
  }).addTo(map);
  localStorage.setItem("freetetra-map-theme", next);
}

function toggleTheme() {
  applyTheme(theme.value === "dark" ? "light" : "dark");
}

function setActiveDays(days: number) {
  activeDays.value = days;
  localStorage.setItem("ft-map-days", String(days));
  void loadHexes();
}

async function loadHexes() {
  if (!map || !hexLayer) return;
  const zoom = map.getZoom();
  const res = resolutionForZoom(zoom);
  try {
    const d = await api.map(res, activeDays.value);
    const hexes = d.hexes ?? [];
    hexLayer.clearLayers();
    const bounds: [number, number][] = [];
    for (const h of hexes) {
      const boundary = cellToBoundary(h.h, false) as [number, number][];
      const poly = L.polygon(boundary, {
        color: "#047857",
        weight: 1.5,
        opacity: 0.9,
        fillColor: HEX_COLOR,
        fillOpacity: 0.5,
      });
      poly.bindPopup(hexPopup(h));
      poly.addTo(hexLayer);
      bounds.push([h.lat, h.lon]);
    }
    const s = d.stats ?? null;
    if (s) {
      const online = s.repeaters_online ?? 0;
      const total = s.repeaters_total ?? 0;
      statRepeaters.value = online + "/" + total;
      statDevices.value = s.devices_24h ?? 0;
      onlineStale.value = online === 0;
    }
    if (firstLoad && bounds.length > 0 && !userSetView) {
      map.fitBounds(bounds as L.LatLngBoundsExpression, { padding: [40, 40], maxZoom: 11 });
      userSetView = true;
    }
    firstLoad = false;
  } catch (e) {
    console.error(e);
  }
}

async function loadStations() {
  if (!stationLayer) return;
  try {
    const d = await api.stations();
    stationLayer.clearLayers();
    for (const st of d.stations ?? []) {
      const m = L.marker([st.lat, st.lon], { icon: stationIcon(st.type, st.online) });
      m.bindPopup(stationPopup(st));
      m.addTo(stationLayer);
    }
  } catch (e) {
    console.error(e);
  }
}

onMounted(() => {
  if (!mapEl.value) return;
  document.body.className = theme.value;

  map = L.map(mapEl.value, { worldCopyJump: true });

  const urlView = parseUrlHash();
  const savedView = loadSavedView();
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
          if (!userSetView && map) {
            map.flyTo([pos.coords.latitude, pos.coords.longitude], 10);
            userSetView = true;
          }
        },
        () => {},
        { timeout: 4000, enableHighAccuracy: false, maximumAge: 60000 },
      );
    }
  }

  map.on("moveend zoomend", () => {
    if (!map) return;
    const c = map.getCenter();
    const z = map.getZoom();
    localStorage.setItem(
      "freetetra-map-view",
      JSON.stringify({ lat: c.lat, lng: c.lng, zoom: z }),
    );
    const hash = "#" + z + "/" + c.lat.toFixed(4) + "/" + c.lng.toFixed(4);
    if (window.location.hash !== hash) history.replaceState(null, "", hash);
  });

  const locateCtrl = new L.Control({ position: "topleft" });
  locateCtrl.onAdd = function () {
    const div = L.DomUtil.create("div", "leaflet-bar leaflet-control");
    const a = L.DomUtil.create("a", "", div);
    a.href = "#";
    a.title = "Meine Position";
    a.innerHTML = "&#127758;";
    a.style.fontSize = "16px";
    a.style.lineHeight = "26px";
    a.style.textAlign = "center";
    L.DomEvent.on(a, "click", (e) => {
      L.DomEvent.preventDefault(e);
      L.DomEvent.stopPropagation(e);
      if (!navigator.geolocation || !map) return;
      a.innerHTML = "&#8987;";
      navigator.geolocation.getCurrentPosition(
        (pos) => {
          a.innerHTML = "&#127758;";
          map?.flyTo([pos.coords.latitude, pos.coords.longitude], 12);
        },
        () => {
          a.innerHTML = "&#10060;";
          setTimeout(() => (a.innerHTML = "&#127758;"), 1500);
        },
        { timeout: 8000, enableHighAccuracy: true },
      );
    });
    return div;
  };
  locateCtrl.addTo(map);

  tileLayer = L.tileLayer(theme.value === "dark" ? DARK_TILES : LIGHT_TILES, {
    attribution: "&copy; OpenStreetMap &copy; CartoDB",
    subdomains: "abcd",
    maxZoom: 19,
  }).addTo(map);

  hexLayer = L.layerGroup().addTo(map);
  stationLayer = L.layerGroup().addTo(map);

  void loadHexes();
  void loadStations();
  map.on("zoomend", loadHexes);
  stationsTimer = setInterval(loadStations, 60000);
  hexesTimer = setInterval(loadHexes, 30000);
});

onUnmounted(() => {
  if (stationsTimer) clearInterval(stationsTimer);
  if (hexesTimer) clearInterval(hexesTimer);
  if (map) {
    map.remove();
    map = null;
  }
  document.body.className = "";
});
</script>

<template>
  <div :class="['map-shell', theme]">
    <div class="header">
      <h1>Free<span>Tetra</span> Map</h1>
      <div class="info">
        <span class="stat-group"
          ><span class="live-dot" :class="{ stale: onlineStale }" />
          <b>{{ statRepeaters }}</b> {{ t("map.repeater") }}</span
        >
        <span><b>{{ statDevices }}</b> {{ t("map.devices") }}</span>
        <span class="filter-group" :title="t('map.filter_title')">
          <button
            v-for="d in [7, 30, 90]"
            :key="d"
            class="filter-btn"
            :class="{ active: activeDays === d }"
            @click="setActiveDays(d)"
          >
            {{ d }}d
          </button>
          <button class="filter-btn" :class="{ active: activeDays === 0 }" @click="setActiveDays(0)">
            {{ t("map.filter_all") }}
          </button>
        </span>
        <button class="theme-btn" @click="toggleTheme">
          {{ theme === "dark" ? "☀ Light" : "🌙 Dark" }}
        </button>
        <span class="lang-toggle-inline"><LangSwitch /></span>
        <a href="/">{{ t("common.back_to_start") }}</a>
      </div>
    </div>
    <div ref="mapEl" id="map" />
  </div>
</template>

<style scoped>
.map-shell {
  font-family: "Inter", system-ui, sans-serif;
  min-height: 100vh;
  position: relative;
  transition:
    background 0.2s,
    color 0.2s;
}
.map-shell.light {
  background: #f9fafb;
  color: #1f2937;
}
.map-shell.dark {
  background: #0a0d12;
  color: #e5e7eb;
}

.header {
  padding: 12px 20px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}
.map-shell.light .header {
  background: #ffffff;
  border-bottom: 1px solid #e5e7eb;
}
.map-shell.dark .header {
  background: #111827;
  border-bottom: 1px solid #1f2937;
}
.header h1 {
  font-size: 1.05rem;
  font-weight: 700;
}
.map-shell.light .header h1 span {
  color: #059669;
}
.map-shell.dark .header h1 span {
  color: #6ee7b7;
}
.header .info {
  font-size: 0.82rem;
  display: flex;
  gap: 14px;
  align-items: center;
}
.header .info b {
  font-family: "JetBrains Mono", monospace;
}
.map-shell.light .header .info {
  color: #6b7280;
}
.map-shell.light .header .info b {
  color: #059669;
}
.map-shell.dark .header .info {
  color: #9ca3af;
}
.map-shell.dark .header .info b {
  color: #6ee7b7;
}
.header a {
  text-decoration: none;
}
.map-shell.light .header a {
  color: #2563eb;
}
.map-shell.dark .header a {
  color: #60a5fa;
}

.stat-group {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.live-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #10b981;
  animation: pulse 2s infinite;
}
.live-dot.stale {
  background: #9ca3af;
  animation: none;
}
@keyframes pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.35;
  }
}

#map {
  width: 100vw;
  height: calc(100vh - 56px);
}

.theme-btn {
  padding: 4px 10px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 0.78rem;
  font-family: inherit;
}
.map-shell.light .theme-btn {
  background: #f3f4f6;
  color: #1f2937;
  border: 1px solid #d1d5db;
}
.map-shell.dark .theme-btn {
  background: #1f2937;
  color: #e5e7eb;
  border: 1px solid #374151;
}

.filter-group {
  display: inline-flex;
  gap: 4px;
  align-items: center;
  margin-left: 8px;
}
.filter-btn {
  background: transparent;
  border: 1px solid currentColor;
  color: inherit;
  padding: 3px 8px;
  border-radius: 4px;
  font-size: 0.72rem;
  cursor: pointer;
  font-family: "JetBrains Mono", monospace;
  transition: all 0.15s;
}
.filter-btn:hover {
  background: rgba(5, 150, 105, 0.1);
}
.filter-btn.active {
  background: #059669;
  color: white;
  border-color: #059669;
}

.lang-toggle-inline {
  display: inline-flex;
  gap: 4px;
  align-items: center;
  margin-left: 8px;
  font-family: "JetBrains Mono", monospace;
  font-size: 0.72rem;
  position: relative;
}
/* The shared LangSwitch component positions itself absolute top-right by
   default; inside the header inline strip we want it flowing inline. */
.lang-toggle-inline :deep(.lang-toggle) {
  position: static;
}

@media (max-width: 640px) {
  .header {
    padding: 8px 12px;
    gap: 6px;
  }
  .header h1 {
    font-size: 0.95rem;
    flex-basis: 100%;
  }
  .header .info {
    font-size: 0.72rem;
    gap: 10px;
    flex-wrap: wrap;
  }
  .theme-btn {
    padding: 3px 8px;
    font-size: 0.72rem;
  }
  #map {
    height: calc(100vh - 78px);
  }
}
</style>
