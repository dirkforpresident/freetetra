<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { api, type LastHeardEntry } from "../api";
import LangSwitch from "../components/LangSwitch.vue";

const { t, locale } = useI18n();

const entries = ref<LastHeardEntry[]>([]);
const lastUpdate = ref<string>("…");
const offline = ref(false);

// Tick once a second so fmtAgo / live duration stay live without re-polling.
const now = ref(Date.now());
let nowTimer: ReturnType<typeof setInterval> | null = null;
let pollTimer: ReturnType<typeof setInterval> | null = null;

async function poll() {
  try {
    const d = await api.liveLastHeard();
    entries.value = d.entries ?? [];
    offline.value = false;
    lastUpdate.value = new Date().toLocaleTimeString(locale.value === "en" ? "en-US" : "de-DE");
  } catch {
    offline.value = true;
    lastUpdate.value = "offline";
  }
}

onMounted(() => {
  poll();
  pollTimer = setInterval(poll, 2000);
  nowTimer = setInterval(() => (now.value = Date.now()), 1000);
});
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer);
  if (nowTimer) clearInterval(nowTimer);
});

const active = computed(() => entries.value.filter((e) => !e.ended_at));
const past = computed(() => entries.value.filter((e) => !!e.ended_at).slice(0, 30));

function fmtDuration(ms: number): string {
  if (!ms || ms <= 0) return "";
  if (ms < 1000) return ms + "ms";
  const s = Math.floor(ms / 1000);
  if (s < 60) return s + "s";
  return Math.floor(s / 60) + "m " + (s % 60) + "s";
}

function fmtAgo(iso: string): string {
  const then = new Date(iso).getTime();
  const s = Math.floor((now.value - then) / 1000);
  if (s < 5) return t("live.just_now");
  if (s < 60) return t("live.ago_s").replace("%d", String(s));
  const m = Math.floor(s / 60);
  if (m < 60) return t("live.ago_min").replace("%d", String(m));
  const h = Math.floor(m / 60);
  if (h < 24) return t("live.ago_h").replace("%d", String(h));
  return t("live.ago_d").replace("%d", String(Math.floor(h / 24)));
}

function networkClass(gssi: number): "dmr" | "tetra" | "local" {
  if (gssi >= 91) return "dmr";
  if (gssi >= 10) return "tetra";
  return "local";
}
function networkLabel(gssi: number): string {
  if (gssi >= 91) return t("live.tg_dmr");
  if (gssi >= 10) return t("live.tg_tetra");
  return t("live.tg_local");
}
function liveDuration(e: LastHeardEntry): string {
  return fmtDuration(now.value - new Date(e.started_at).getTime());
}
</script>

<template>
  <div class="live-shell">
    <LangSwitch />
    <div class="container">
      <div class="header">
        <h1>Free<span>Tetra</span> Live</h1>
        <div class="meta">
          <span>{{ lastUpdate }}</span>
        </div>
      </div>

      <div class="card">
        <h2>
          {{ t("live.active_calls") }}
          <span class="active-count">{{ active.length }}</span>
        </h2>
        <div v-if="active.length === 0" class="empty">{{ t("live.silent") }}</div>
        <div
          v-for="e in active"
          :key="e.call_id"
          class="row live"
          :class="networkClass(e.dest_gssi)"
        >
          <span class="cs"><span class="pulse-dot" />{{ e.callsign || "–" }}</span>
          <span class="issi">{{ e.source_issi }}</span>
          <span class="tg">TG {{ e.dest_gssi }}</span>
          <span class="dur">{{ liveDuration(e) }}</span>
          <span class="badge">{{ networkLabel(e.dest_gssi) }}</span>
          <span class="when">{{ fmtAgo(e.started_at) }}</span>
        </div>
      </div>

      <div class="card">
        <h2>{{ t("live.last_heard") }}</h2>
        <div v-if="past.length === 0" class="empty">{{ t("live.no_calls") }}</div>
        <div v-for="e in past" :key="e.call_id" class="row" :class="networkClass(e.dest_gssi)">
          <span class="cs">{{ e.callsign || "–" }}</span>
          <span class="issi">{{ e.source_issi }}</span>
          <span class="tg">TG {{ e.dest_gssi }}</span>
          <span class="dur">{{ fmtDuration(e.duration_ms) }}</span>
          <span class="badge">{{ networkLabel(e.dest_gssi) }}</span>
          <span class="when">{{ fmtAgo(e.started_at) }}</span>
        </div>
      </div>

      <div class="foot">
        <a href="/">{{ t("common.home") }}</a> ·
        <a href="/map">{{ t("common.map") }}</a> ·
        <a href="/ui">{{ t("common.dashboard") }}</a>
      </div>
    </div>
  </div>
</template>

<style scoped>
.live-shell {
  --bg: #f9fafb;
  --bg-card: #ffffff;
  --bg-subtle: #f3f4f6;
  --border: #e5e7eb;
  --accent: #059669;
  --accent-dim: rgba(5, 150, 105, 0.08);
  --blue: #2563eb;
  --red: #dc2626;
  --text: #111827;
  --text-dim: #4b5563;
  --text-muted: #6b7280;
  background: var(--bg);
  color: var(--text);
  font-family: "Inter", system-ui, sans-serif;
  line-height: 1.5;
  min-height: 100vh;
  position: relative;
}

.container {
  max-width: 900px;
  margin: 0 auto;
  padding: 24px;
}

.header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  padding-bottom: 16px;
  margin-bottom: 24px;
  border-bottom: 1px solid var(--border);
}
.header h1 {
  font-size: 1.6rem;
  font-weight: 800;
  letter-spacing: -0.01em;
}
.header h1 span {
  color: var(--accent);
}
.header .meta {
  font-size: 0.82rem;
  color: var(--text-muted);
  font-family: "JetBrains Mono", monospace;
}

.card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 20px;
  margin-bottom: 16px;
}
.card h2 {
  font-size: 1rem;
  font-weight: 700;
  margin-bottom: 12px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-dim);
}
.active-count {
  float: right;
  font-family: "JetBrains Mono", monospace;
  color: var(--accent);
}

.row {
  display: flex;
  gap: 14px;
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--bg-subtle);
  margin-bottom: 6px;
  font-size: 0.9rem;
  align-items: center;
  border: 1px solid transparent;
  transition:
    box-shadow 0.3s ease,
    border-color 0.3s ease;
}
.row .cs {
  font-family: "JetBrains Mono", monospace;
  font-weight: 600;
  color: var(--text);
  min-width: 84px;
}
.row .issi {
  font-family: "JetBrains Mono", monospace;
  color: var(--text-muted);
  font-size: 0.8rem;
  min-width: 80px;
}
.row .tg {
  font-family: "JetBrains Mono", monospace;
  font-weight: 600;
  min-width: 60px;
}
.row .dur {
  color: var(--text-muted);
  font-size: 0.82rem;
  min-width: 70px;
}
.row .when {
  color: var(--text-muted);
  font-size: 0.82rem;
  margin-left: auto;
}
.row .badge {
  font-size: 0.7rem;
  padding: 2px 8px;
  border-radius: 4px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-weight: 600;
}

/* Past calls: subtle border */
.row.local {
  border-color: rgba(107, 114, 128, 0.25);
}
.row.local .tg {
  color: var(--text-muted);
}
.row.local .badge {
  background: rgba(107, 114, 128, 0.12);
  color: var(--text-muted);
}

.row.tetra {
  border-color: rgba(5, 150, 105, 0.25);
}
.row.tetra .tg {
  color: var(--accent);
}
.row.tetra .badge {
  background: rgba(5, 150, 105, 0.12);
  color: var(--accent);
}

.row.dmr {
  border-color: rgba(217, 119, 6, 0.3);
}
.row.dmr .tg {
  color: #d97706;
}
.row.dmr .badge {
  background: rgba(217, 119, 6, 0.12);
  color: #d97706;
}

/* Active calls */
.row.live.local {
  background: rgba(107, 114, 128, 0.08);
  border-color: var(--text-muted);
  box-shadow:
    0 0 24px rgba(107, 114, 128, 0.5),
    0 0 6px rgba(107, 114, 128, 0.3);
  animation: glow-local 1.6s infinite;
}
.row.live.tetra {
  background: var(--accent-dim);
  border-color: var(--accent);
  box-shadow:
    0 0 24px rgba(5, 150, 105, 0.6),
    0 0 6px rgba(5, 150, 105, 0.4);
  animation: glow-tetra 1.6s infinite;
}
.row.live.dmr {
  background: rgba(217, 119, 6, 0.08);
  border-color: #d97706;
  box-shadow:
    0 0 24px rgba(217, 119, 6, 0.6),
    0 0 6px rgba(217, 119, 6, 0.4);
  animation: glow-dmr 1.6s infinite;
}

@keyframes glow-tetra {
  0%,
  100% {
    box-shadow:
      0 0 24px rgba(5, 150, 105, 0.6),
      0 0 6px rgba(5, 150, 105, 0.4);
  }
  50% {
    box-shadow:
      0 0 36px rgba(5, 150, 105, 0.9),
      0 0 12px rgba(5, 150, 105, 0.6);
  }
}
@keyframes glow-dmr {
  0%,
  100% {
    box-shadow:
      0 0 24px rgba(217, 119, 6, 0.6),
      0 0 6px rgba(217, 119, 6, 0.4);
  }
  50% {
    box-shadow:
      0 0 36px rgba(217, 119, 6, 0.9),
      0 0 12px rgba(217, 119, 6, 0.6);
  }
}
@keyframes glow-local {
  0%,
  100% {
    box-shadow:
      0 0 16px rgba(107, 114, 128, 0.4),
      0 0 4px rgba(107, 114, 128, 0.3);
  }
  50% {
    box-shadow:
      0 0 24px rgba(107, 114, 128, 0.6),
      0 0 8px rgba(107, 114, 128, 0.4);
  }
}

.pulse-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  animation: pulse 1s infinite;
  margin-right: 6px;
}
.row.live.tetra .pulse-dot {
  background: var(--accent);
}
.row.live.dmr .pulse-dot {
  background: #d97706;
}
.row.live.local .pulse-dot {
  background: var(--text-muted);
}
@keyframes pulse {
  0%,
  100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.5;
    transform: scale(1.3);
  }
}

.empty {
  color: var(--text-muted);
  padding: 14px;
  text-align: center;
  font-size: 0.88rem;
  font-style: italic;
}
.foot {
  text-align: center;
  padding: 24px 0;
  color: var(--text-muted);
  font-size: 0.78rem;
}
.foot a {
  color: var(--accent);
  text-decoration: none;
  margin: 0 4px;
}
.foot a:hover {
  text-decoration: underline;
}

@media (max-width: 640px) {
  .container {
    padding: 14px;
  }
  .header {
    margin-bottom: 16px;
    padding-bottom: 12px;
  }
  .header h1 {
    font-size: 1.3rem;
  }
  .header .meta {
    font-size: 0.72rem;
  }
  .card {
    padding: 14px;
    margin-bottom: 12px;
  }
  .card h2 {
    font-size: 0.85rem;
  }
  .row {
    flex-wrap: wrap;
    gap: 4px 10px;
    font-size: 0.85rem;
    padding: 10px 12px;
  }
  .row .cs {
    min-width: 0;
    flex: 1 1 auto;
  }
  .row .issi {
    min-width: 0;
    font-size: 0.75rem;
  }
  .row .tg {
    min-width: 0;
  }
  .row .dur {
    min-width: 0;
    font-size: 0.78rem;
  }
  .row .badge {
    font-size: 0.65rem;
    padding: 1px 6px;
  }
  .row .when {
    flex-basis: 100%;
    margin-left: 0;
    font-size: 0.72rem;
    text-align: right;
    margin-top: 2px;
  }
}
@media (max-width: 380px) {
  .row .issi {
    display: none;
  }
}
</style>
