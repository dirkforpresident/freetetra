<script setup lang="ts">
import { onMounted } from "vue";
import { useI18n } from "vue-i18n";
import { useSiteConfig } from "../composables/useSiteConfig";
import LangSwitch from "../components/LangSwitch.vue";

const { t } = useI18n();
const { config, ready } = useSiteConfig();

onMounted(() => {
  // siteConfig boot is already triggered in App.vue; here we just need the
  // promise to be in flight (or already resolved) before the template uses
  // config.value. Awaiting is optional — the template handles null.
  void ready;
});
</script>

<template>
  <div class="landing-shell">
    <LangSwitch />
    <div class="container">
      <div class="hero">
        <h1>Free<span>Tetra</span></h1>
        <div class="tagline">{{ t("landing.tagline") }}</div>
      </div>

      <div class="card">
        <h2>{{ t("landing.what_is.title") }}</h2>
        <p>{{ t("landing.what_is.body1") }}</p>
        <p v-html="t('landing.what_is.body2').replace('{{HOST}}', config?.host ?? '')" />
        <div class="federation-info" v-html="t('landing.what_is.based')" />
      </div>

      <div class="card">
        <h2>{{ t("landing.whats_up.title") }}</h2>
        <p>{{ t("landing.whats_up.intro") }}</p>
        <div class="services" style="margin-top: 12px">
          <a href="/live" class="svc svc-link">
            <div class="svc-tg">/live</div>
            <div>
              <div class="svc-name">{{ t("landing.whats_up.live.name") }}</div>
              <div class="svc-desc">{{ t("landing.whats_up.live.desc") }}</div>
            </div>
          </a>
          <a href="/map" class="svc svc-link">
            <div class="svc-tg">/map</div>
            <div>
              <div class="svc-name">{{ t("landing.whats_up.map.name") }}</div>
              <div class="svc-desc">{{ t("landing.whats_up.map.desc") }}</div>
            </div>
          </a>
          <a href="/ui" class="svc svc-link">
            <div class="svc-tg">/ui</div>
            <div>
              <div class="svc-name">{{ t("landing.whats_up.ui.name") }}</div>
              <div class="svc-desc">{{ t("landing.whats_up.ui.desc") }}</div>
            </div>
          </a>
        </div>
      </div>

      <div class="card">
        <h2>{{ t("landing.tgs.title") }}</h2>
        <div class="services">
          <div class="svc">
            <div class="svc-tg">TG 1-9</div>
            <div>
              <div class="svc-name">{{ t("landing.tgs.local.name") }}</div>
              <div class="svc-desc">{{ t("landing.tgs.local.desc") }}</div>
            </div>
          </div>
          <div class="svc">
            <div class="svc-tg">TG 10-90</div>
            <div>
              <div class="svc-name">{{ t("landing.tgs.global.name") }}</div>
              <div class="svc-desc">{{ t("landing.tgs.global.desc") }}</div>
            </div>
          </div>
          <div class="svc">
            <div class="svc-tg">TG 91+</div>
            <div>
              <div class="svc-name">{{ t("landing.tgs.bm.name") }}</div>
              <div class="svc-desc">{{ t("landing.tgs.bm.desc") }}</div>
            </div>
          </div>
        </div>
        <p
          class="tgs-services"
          v-html="t('landing.tgs.services')"
        />
      </div>

      <div class="card">
        <h2>{{ t("landing.connect.title") }}</h2>
        <p>{{ t("landing.connect.intro") }}</p>
        <pre class="connect-snippet">[brew]
host = "{{ config?.host ?? "" }}"
port = 443
tls = true
username = DEINE_DIGITALFUNK_ID
password = "blafablafa"</pre>
        <p class="connect-note" v-html="t('landing.connect.note')" />
        <p class="connect-doc">
          <a href="/mitmachen">{{ t("landing.connect.full_doc") }}</a>
        </p>
      </div>

      <div v-if="config?.server_info_html" v-html="config.server_info_html" />

      <div class="footer">
        <p>FreeTetra — {{ t("common.operated_by") }} {{ config?.operator ?? "" }}</p>
        <p style="margin-top: 4px">
          {{ t("common.powered_by") }}
          <a href="https://github.com/MidnightBlueLabs/tetra-bluestation">BlueStation</a>
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.landing-shell {
  --bg: #f9fafb;
  --bg-card: #ffffff;
  --bg-subtle: #f3f4f6;
  --border: #e5e7eb;
  --border-strong: #d1d5db;
  --accent: #059669;
  --accent-bright: #10b981;
  --accent-dim: rgba(5, 150, 105, 0.08);
  --blue: #2563eb;
  --purple: #7c3aed;
  --yellow: #d97706;
  --red: #dc2626;
  --text: #111827;
  --text-dim: #4b5563;
  --text-muted: #6b7280;
  background: var(--bg);
  color: var(--text);
  font-family: "Inter", system-ui, sans-serif;
  line-height: 1.6;
  min-height: 100vh;
  position: relative;
}

.container {
  max-width: 900px;
  margin: 0 auto;
  padding: 0 24px;
}

.hero {
  text-align: center;
  padding: 80px 0 40px;
}
.hero h1 {
  font-size: 2.8rem;
  font-weight: 800;
  letter-spacing: -0.02em;
  margin-bottom: 8px;
}
.hero h1 span {
  color: var(--accent);
}
.hero .tagline {
  font-size: 1.15rem;
  color: var(--text-dim);
  margin-bottom: 40px;
}

.card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 32px;
  margin-bottom: 24px;
  box-shadow:
    0 1px 3px rgba(17, 24, 39, 0.04),
    0 1px 2px rgba(17, 24, 39, 0.03);
}
.card :deep(h2) {
  font-size: 1.3rem;
  font-weight: 700;
  margin-bottom: 16px;
  color: var(--text);
}
.card :deep(p) {
  color: var(--text-dim);
  margin-bottom: 12px;
}

.services {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 12px;
}
.svc {
  display: flex;
  align-items: center;
  gap: 10px;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 12px 18px;
  flex: 1;
  min-width: 200px;
}
.svc-link {
  text-decoration: none;
  color: inherit;
  cursor: pointer;
}
.svc-tg {
  font-family: "JetBrains Mono", monospace;
  font-weight: 600;
  color: var(--blue);
  font-size: 0.9rem;
}
.svc-name {
  font-weight: 600;
  font-size: 0.9rem;
}
.svc-desc {
  font-size: 0.75rem;
  color: var(--text-muted);
}

.tgs-services {
  margin-top: 16px;
  font-size: 0.85rem;
  color: var(--text-muted);
}

.connect-snippet {
  background: var(--bg);
  padding: 16px;
  border-radius: 8px;
  border: 1px solid var(--border);
  font-family: "JetBrains Mono", monospace;
  font-size: 0.82rem;
  color: var(--accent);
  overflow-x: auto;
  margin-top: 8px;
}
.connect-note {
  margin-top: 12px;
  font-size: 0.82rem;
}
.connect-doc {
  margin-top: 14px;
}
.connect-doc a {
  color: var(--accent);
  font-weight: 600;
  text-decoration: none;
}

.federation-info {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.85rem;
  color: var(--text-muted);
  margin-top: 16px;
  padding: 12px;
  background: var(--bg-subtle);
  border-radius: 8px;
  border: 1px solid var(--border);
}
.federation-info :deep(code) {
  font-family: "JetBrains Mono", monospace;
  color: var(--accent);
  font-size: 0.8rem;
}

.footer {
  text-align: center;
  padding: 40px 0;
  color: var(--text-muted);
  font-size: 0.8rem;
}
.footer a {
  color: var(--accent);
  text-decoration: none;
}
.footer a:hover {
  text-decoration: underline;
}

@media (max-width: 640px) {
  .container {
    padding: 0 16px;
  }
  .hero {
    padding: 48px 0 24px;
  }
  .hero h1 {
    font-size: 2rem;
  }
  .hero .tagline {
    font-size: 0.95rem;
    margin-bottom: 28px;
  }
  .card {
    padding: 20px;
    margin-bottom: 16px;
  }
  .card :deep(h2) {
    font-size: 1.1rem;
  }
  .card :deep(p) {
    font-size: 0.9rem;
  }
  .svc {
    min-width: 100%;
    padding: 10px 14px;
  }
  .footer {
    padding: 28px 0;
    font-size: 0.75rem;
  }
}
</style>
