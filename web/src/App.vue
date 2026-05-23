<script setup lang="ts">
import { onMounted } from "vue";
import { RouterView } from "vue-router";
import { useI18n } from "vue-i18n";
import { useSiteConfig } from "./composables/useSiteConfig";

const { ready } = useSiteConfig();
const { locale } = useI18n();

onMounted(async () => {
  try {
    const c = await ready;
    // Server-detected language wins over client guess so behaviour matches
    // the old server-rendered pages (cookie -> Accept-Language -> "de").
    if (c.lang === "de" || c.lang === "en") locale.value = c.lang;
    if (c.server_name) document.title = "FreeTetra — " + c.server_name;
  } catch {
    /* stay on detectLocale()'s fallback */
  }
});
</script>

<template>
  <v-app>
    <RouterView />
  </v-app>
</template>
