import { createApp } from "vue";
import { createVuetify } from "vuetify";
import "vuetify/styles";
import "@mdi/font/css/materialdesignicons.css";

import App from "./App.vue";
import { router } from "./router";
import { i18n } from "./i18n";

const vuetify = createVuetify({
  theme: { defaultTheme: "dark" },
});

createApp(App).use(router).use(i18n).use(vuetify).mount("#app");
