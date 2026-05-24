import { createApp } from "vue";
import { createVuetify } from "vuetify";
import "vuetify/styles";
import "@mdi/font/css/materialdesignicons.css";

import App from "./App.vue";
import { router } from "./router";
import { i18n } from "./i18n";

const vuetify = createVuetify({
  // Default theme is "light" so the public landing / mitmachen pages match
  // the previous look. Admin and live/map pages opt into dark via
  // <v-theme-provider theme="dark"> at the page level.
  theme: { defaultTheme: "light" },
});

createApp(App).use(router).use(i18n).use(vuetify).mount("#app");
