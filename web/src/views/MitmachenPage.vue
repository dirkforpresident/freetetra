<script setup lang="ts">
import { useI18n } from "vue-i18n";
import LangSwitch from "../components/LangSwitch.vue";

const { t } = useI18n();
</script>

<template>
  <div class="mitmachen-shell">
    <LangSwitch />
    <div class="container">
      <div class="hero">
        <a href="/">{{ t("common.back_to_start") }}</a>
        <h1>{{ t("join.title") }}</h1>
        <div class="tagline">{{ t("join.tagline") }}</div>
      </div>

      <div class="card">
        <h2>{{ t("join.two_ways.title") }}</h2>
        <div class="path">
          <div class="path-icon">📡</div>
          <div>
            <div class="path-name">{{ t("join.path_hotspot") }}</div>
            <div class="path-desc">{{ t("join.path_hotspot.desc") }}</div>
          </div>
        </div>
        <div class="path">
          <div class="path-icon">🌐</div>
          <div>
            <div class="path-name">{{ t("join.path_server") }}</div>
            <div class="path-desc">{{ t("join.path_server.desc") }}</div>
          </div>
        </div>
      </div>

      <div class="card">
        <h2>{{ t("join.path1.title") }}</h2>
        <p>{{ t("join.path1.intro") }}</p>

        <h3>{{ t("join.path1.hardware_h") }}</h3>
        <ul>
          <li v-html="t('join.path1.hw_pi')" />
          <li v-html="t('join.path1.hw_sx')" />
          <li v-html="t('join.path1.hw_ant')" />
          <li v-html="t('join.path1.hw_misc')" />
        </ul>
        <p style="font-size: 0.88rem" v-html="t('join.path1.boards')" />

        <h3>{{ t("join.path1.sw_h") }}</h3>
        <p v-html="t('join.path1.sw_body')" />

        <h3>{{ t("join.path1.acct_h") }}</h3>
        <p v-html="t('join.path1.acct_body')" />

        <h3>{{ t("join.path1.cfg_h") }}</h3>
        <p v-html="t('join.path1.cfg_intro')" />
        <pre>[brew]
host = "freetetra.de"
port = 443
tls = true
username = DEINE_ISSI         # {{ t("join.path1.cfg_comment_issi") }}
password = "blafablafa"       # {{ t("join.path1.cfg_comment_pw") }}</pre>
        <p v-html="t('join.path1.cfg_rest')" />
        <p>{{ t("join.path1.cfg_autoauth") }}</p>
      </div>

      <div class="card">
        <h2>{{ t("join.help.title") }}</h2>
        <p v-html="t('join.help.body1')" />
        <p>{{ t("join.help.body2") }}</p>
      </div>

      <div class="card">
        <h2>{{ t("join.tgs.title") }}</h2>
        <pre>TG 1-9      {{ t("landing.tgs.local.name") }}
TG 10-90    {{ t("landing.tgs.global.name") }}
TG 91+      {{ t("landing.tgs.bm.name") }}
            -&gt; TG 262   = DL
            -&gt; TG 2621  = DL Cluster Nord
            -&gt; TG 1     = World</pre>
        <p v-html="t('join.tgs.more')" />
      </div>

      <div class="card">
        <h2>{{ t("join.path2.title") }}</h2>
        <p v-html="t('join.path2.intro')" />
        <p>{{ t("join.path2.need") }}</p>
        <ul>
          <li>{{ t("join.path2.need_vm") }}</li>
          <li>{{ t("join.path2.need_dom") }}</li>
          <li>{{ t("join.path2.need_sw") }}</li>
        </ul>

        <h3>{{ t("join.path2.fed_h") }}</h3>
        <p v-html="t('join.path2.fed_body')" />
        <pre>FEDERATION_KEY=freetetra-federation-2026
FEDERATION_PEERS=wss://freetetra.de/peer/</pre>
        <p style="font-size: 0.88rem" v-html="t('join.path2.fed_note')" />
      </div>

      <div class="card">
        <h2>{{ t("join.contact.title") }}</h2>
        <p v-html="t('join.contact.body')" />
      </div>

      <div class="footer">
        <a href="/">{{ t("common.home") }}</a> ·
        <a href="/live">{{ t("common.live") }}</a> ·
        <a href="/map">{{ t("common.map") }}</a>
      </div>
    </div>
  </div>
</template>

<style scoped>
.mitmachen-shell {
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
  padding: 56px 0 24px;
}
.hero a {
  color: var(--text-muted);
  text-decoration: none;
  font-size: 0.85rem;
}
.hero a:hover {
  color: var(--accent);
}
.hero h1 {
  font-size: 2.2rem;
  font-weight: 800;
  letter-spacing: -0.02em;
  margin: 16px 0 8px;
}
.hero h1 span {
  color: var(--accent);
}
.hero .tagline {
  font-size: 1rem;
  color: var(--text-dim);
  margin-bottom: 32px;
}

.card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 28px;
  margin-bottom: 20px;
  box-shadow: 0 1px 3px rgba(17, 24, 39, 0.04);
}
.card :deep(h2) {
  font-size: 1.2rem;
  font-weight: 700;
  margin-bottom: 14px;
}
.card :deep(h3) {
  font-size: 0.95rem;
  font-weight: 600;
  margin: 14px 0 6px;
  color: var(--text-dim);
}
.card :deep(p) {
  color: var(--text-dim);
  margin-bottom: 10px;
}
.card :deep(ul) {
  margin: 8px 0 12px 22px;
  color: var(--text-dim);
}
.card :deep(li) {
  margin-bottom: 4px;
}
.card :deep(code) {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.84rem;
  color: var(--accent);
  background: var(--accent-dim);
  padding: 1px 5px;
  border-radius: 3px;
}
.card :deep(a) {
  color: var(--accent);
  text-decoration: none;
}
.card :deep(a:hover) {
  text-decoration: underline;
}

pre {
  background: var(--bg-subtle);
  padding: 14px 16px;
  border-radius: 8px;
  border: 1px solid var(--border);
  font-family: "JetBrains Mono", monospace;
  font-size: 0.82rem;
  overflow-x: auto;
  margin: 8px 0;
  color: var(--text);
  line-height: 1.55;
}

.path {
  display: flex;
  gap: 12px;
  padding: 14px;
  border-radius: 8px;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  margin: 8px 0;
}
.path-icon {
  font-size: 1.4rem;
  line-height: 1.2;
}
.path-name {
  font-weight: 600;
  font-size: 0.95rem;
}
.path-desc {
  font-size: 0.85rem;
  color: var(--text-muted);
  margin-top: 2px;
}

.footer {
  text-align: center;
  padding: 32px 0;
  color: var(--text-muted);
  font-size: 0.8rem;
}
.footer a {
  color: var(--accent);
  text-decoration: none;
  margin: 0 8px;
}

@media (max-width: 640px) {
  .container {
    padding: 0 16px;
  }
  .hero {
    padding: 32px 0 16px;
  }
  .hero h1 {
    font-size: 1.6rem;
  }
  .card {
    padding: 18px;
  }
  pre {
    font-size: 0.75rem;
  }
}
</style>
