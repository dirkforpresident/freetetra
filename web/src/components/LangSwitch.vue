<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

const { locale } = useI18n();
const active = computed(() => (locale.value === "en" ? "en" : "de"));

function setLang(l: "de" | "en") {
  document.cookie = "ft_lang=" + l + ";path=/;max-age=" + 60 * 60 * 24 * 365 + ";samesite=lax";
  locale.value = l;
  document.documentElement.lang = l;
}
</script>

<template>
  <div class="lang-toggle">
    <a
      href="#"
      class="lang-link"
      :class="{ 'lang-active': active === 'de' }"
      @click.prevent="setLang('de')"
      >DE</a
    >
    ·
    <a
      href="#"
      class="lang-link"
      :class="{ 'lang-active': active === 'en' }"
      @click.prevent="setLang('en')"
      >EN</a
    >
  </div>
</template>

<style scoped>
.lang-toggle {
  position: absolute;
  top: 16px;
  right: 20px;
  font-size: 0.78rem;
  font-family: "JetBrains Mono", monospace;
  color: var(--text-muted);
}
.lang-link {
  color: var(--text-muted);
  text-decoration: none;
  padding: 2px 6px;
  border-radius: 4px;
  cursor: pointer;
}
.lang-link:hover {
  color: var(--accent);
}
.lang-link.lang-active {
  color: var(--accent);
  font-weight: 700;
}
</style>
