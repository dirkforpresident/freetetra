import { createRouter, createWebHistory, type RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  { path: "/", name: "landing", component: () => import("../views/LandingPage.vue") },
  { path: "/mitmachen", name: "mitmachen", component: () => import("../views/MitmachenPage.vue") },
  { path: "/live", name: "live", component: () => import("../views/LivePage.vue") },
  { path: "/map", name: "map", component: () => import("../views/MapPage.vue") },
  { path: "/ui", name: "admin", component: () => import("../views/AdminDashboard.vue") },
  { path: "/ui/legacy", name: "legacy", component: () => import("../views/LegacyVuetifyUI.vue") },
  { path: "/:pathMatch(.*)*", redirect: "/" },
];

export const router = createRouter({
  history: createWebHistory(),
  routes,
});
