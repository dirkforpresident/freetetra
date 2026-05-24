import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import vuetify from "vite-plugin-vuetify";

const GO_BACKEND = process.env.VITE_GO_BACKEND ?? "http://localhost:8080";

const proxyPaths = ["/api", "/lang", "/brew", "/telemetry", "/ws"];

export default defineConfig({
  plugins: [vue(), vuetify({ autoImport: true })],
  server: {
    port: 5173,
    proxy: Object.fromEntries(
      proxyPaths.map((p) => [
        p,
        {
          target: GO_BACKEND,
          changeOrigin: true,
          ws: p === "/ws" || p === "/telemetry",
        },
      ]),
    ),
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
  },
});
