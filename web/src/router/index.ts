import { createRouter, createWebHistory, type RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  { path: "/", name: "landing", component: () => import("../views/LandingPage.vue") },
  { path: "/mitmachen", name: "mitmachen", component: () => import("../views/MitmachenPage.vue") },
  { path: "/live", name: "live", component: () => import("../views/LivePage.vue") },
  { path: "/map", name: "map", component: () => import("../views/MapPage.vue") },
  { path: "/ui", name: "admin", component: () => import("../views/AdminDashboard.vue") },
  // /ui/legacy (the Vuetify SDS console) is intentionally not mounted yet.
  // The Go binary continues to serve it from internal/service/dashboard_ui_vuetify.html
  // until it is ported in a follow-up PR. See the plan for details.
  { path: "/:pathMatch(.*)*", redirect: "/" },
];

export const router = createRouter({
  history: createWebHistory(),
  routes,
});
