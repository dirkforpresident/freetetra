# Vue Frontend Split — Plan

> Worktree: `.claude/worktrees/vue-frontend-split` on branch `worktree-vue-frontend-split`.
> Status: planning only. No code changes have been made.

**Goal:** Lift all HTML/CSS/JS currently embedded in Go source files out of the Go tree and into a standalone Vue 3 project. The Go binary becomes JSON-API-only (plus an optional static-file handler when bundled mode is selected). The Vue app can be served two ways and the decision is reversible: either (A) via a reverse-proxy in front of both the Go binary and a Vite/preview server, or (B) by `go:embed`-ing a built `dist/` and serving it from the existing brew server.

**Resolved decisions** (2026-05-23):
- **i18n** — port `i18n_strings.go` content into `web/src/i18n/{de,en}.json`; drop the Go-side `translate()` helper. `/lang/de`+`/lang/en` cookie redirects stay so existing bookmarks work; Vue reads the cookie.
- **UI library** — Vuetify 3. Matches `/ui/legacy` (mechanical port) and gives the new admin dashboard a consistent component vocabulary.
- **Deployment** — **build-flag selectable**. The Go binary supports both: a `web_embed` build tag flips between embedding `web/dist/` via `//go:embed` and serving from a configurable external directory (or proxying — operator's choice). Default build = embedded.
- **Map** — stay on Leaflet; migrate from CDN script tags to npm `leaflet` + `@vue-leaflet/vue-leaflet`.

---

## 1. Current HTML surface (what has to move)

Five HTML payloads live in Go source today. All share the `{{T:key}}` i18n placeholder convention and a tiny set of static substitutions; only the legacy Vuetify file is a "real" SPA. Line counts include surrounding Go boilerplate.

| Route(s) | File | Style | Placeholders | API calls |
|---|---|---|---|---|
| `GET /` | [internal/service/public_page.go](../../../internal/service/public_page.go) (~479 LOC, ~61 HTML tags) | Server-rendered static landing | `{{HOST}}`, `{{SERVER_NAME}}`, `{{OPERATOR}}`, `{{SERVER_INFO_CARD}}`, `{{LANG_SWITCH}}`, `{{LANG_HTML_ATTR}}`, `{{T:…}}` | none |
| `GET /mitmachen` | [internal/service/mitmachen_page.go](../../../internal/service/mitmachen_page.go) (~211 LOC) | Server-rendered static "join us" page | `{{HOST}}`, `{{LANG_*}}`, `{{T:…}}` | none |
| `GET /live` | [internal/service/live_page.go](../../../internal/service/live_page.go) (~272 LOC) | Vanilla JS, polling | `{{LANG_*}}`, `{{T:…}}` | `GET /api/live/last-heard` |
| `GET /map` | [internal/service/positions.go](../../../internal/service/positions.go) (positions.go also owns ~12 API handlers) | Vanilla JS + Leaflet | `{{LANG_*}}`, `{{T:…}}` | `GET /api/map`, `GET /api/stations` |
| `GET /ui`, `GET /ui/` | [internal/service/admin_dashboard.go](../../../internal/service/admin_dashboard.go) (~544 LOC, dashboard is one big string const) | Vanilla JS, polling | `{{SERVER_NAME}}`, `{{LANG_*}}`, `{{T:…}}` | `/api/public/status`, `/api/telemetry/clients`, `/api/peers`, `/api/positions`, `/api/dashboard/snapshot`, `/api/stations`, `/api/radioid/lookup` |
| `GET /ui/legacy` | [internal/service/dashboard_ui_vuetify.html](../../../internal/service/dashboard_ui_vuetify.html) (1155 lines) embedded by [dashboard_ui_assets.go](../../../internal/service/dashboard_ui_assets.go) | Vue 3 + Vuetify via CDN, polling | none | `/api/dashboard/snapshot`, `/api/sds/send` |

**Note on the legacy Vuetify file:** it already uses `Vue.createApp` and Vuetify via CDN script tags ([dashboard_ui_vuetify.html:279-283](../../../internal/service/dashboard_ui_vuetify.html#L279-L283)). It's effectively a Vue SPA without a build step. Migrating it is mostly mechanical — splitting one inline `data()/computed/methods` block into SFCs.

**i18n is server-side today.** [internal/service/i18n.go:86](../../../internal/service/i18n.go#L86) walks the rendered HTML and string-replaces every `{{T:key}}` against [internal/service/i18n_strings.go](../../../internal/service/i18n_strings.go) (de/en). A cookie `ft_lang` plus `Accept-Language` chooses the language. There are ~250+ keys (~21 KB strings file). Either keep server-side i18n (Go exposes `GET /api/i18n/:lang`) or port the strings file into the Vue project (vue-i18n / `<i18n-t>`); the second is cleaner but is the only piece of the migration that touches the i18n_strings.go content.

**Static substitutions (non-i18n)** reduce to: `SERVER_NAME`, `OPERATOR`, `HOST`, `SERVER_INFO_CARD`. All can be served as JSON: `GET /api/site/config` returning `{server_name, operator, host, server_info_html}`. The dynamic `LANG_SWITCH` becomes a Vue component.

---

## 2. API endpoints the Vue app needs

These are the read endpoints the new SPA must hit (everything else, e.g. `/api/sds/send`, `/api/sds/virtual/*`, is admin-only POST already shaped as JSON). All already exist:

- `GET /api/public/status`
- `GET /api/site/config` *(new — replaces `{{HOST}}` / `{{SERVER_NAME}}` / `{{OPERATOR}}` / `{{SERVER_INFO_CARD}}`)*
- `GET /api/i18n/:lang` *(new — only if we keep server-side i18n)*
- `GET /api/live/last-heard`
- `GET /api/map`, `GET /api/positions`, `GET /api/positions/history`
- `GET /api/stations`
- `GET /api/dashboard/snapshot`
- `GET /api/telemetry/clients`
- `GET /api/peers`
- `GET /api/radioid/lookup?issi=…`
- `GET /api/tmo-site/list`, `POST /api/tmo-site/heartbeat`
- `POST /api/sds/send`, `*/api/sds/virtual/*`

No new write endpoints needed.

---

## 3. New repo layout

```
web/                        # standalone Vue 3 project, own package.json
  package.json
  vite.config.ts
  tsconfig.json
  index.html
  src/
    main.ts
    router.ts               # /, /mitmachen, /live, /map, /ui (matches current Go routes)
    api.ts                  # typed fetch helpers
    i18n/                   # locale JSONs (de.json, en.json) — generated from i18n_strings.go
    components/
      LangSwitch.vue
      ServerInfoCard.vue
    views/
      LandingPage.vue       # was public_page.go
      MitmachenPage.vue
      LivePage.vue
      MapPage.vue           # uses leaflet (already CDN-loaded today)
      AdminDashboard.vue    # was admin_dashboard.go
      LegacyVuetifyUI.vue   # was dashboard_ui_vuetify.html (optional — keep as a route or drop)
  dist/                     # build output (gitignored; embedded by Go in bundled mode)
```

`web/` is a sibling of `internal/`, `cmd/`, `codec/`. It is independent enough to live in its own repo later if desired.

---

## 4. Two ways to serve it — pick one (reversible)

### Option A — Reverse proxy in front (recommended for development)

- `nginx`/`caddy` (or `vite` dev server with `proxy:` config) terminates the public port.
- `/api/*`, `/lang/*`, `/telemetry`, `/brew*` → Go binary on `:8080`.
- everything else → Vite dev server during dev, or `web/dist/` as static files in prod.
- Go binary keeps only the JSON API handlers; the five HTML handlers are removed.
- Existing nginx configs at [docs/nginx-single-port.conf](../../nginx-single-port.conf) and [docs/nginx-two-ports.conf](../../nginx-two-ports.conf) already proxy `/brew` and `/telemetry` — we extend the `/api/` pattern and add a `try_files $uri /index.html;` SPA fallback.

**Trade-offs:** more moving parts at deploy time (two processes / one extra container in [docker-compose.yml](../../../docker-compose.yml)), but instant HMR during frontend work and the Go binary no longer needs to ship UI bytes. CSP/headers are managed at the proxy, which is where everyone expects them.

### Option B — `go:embed dist/` (recommended for prod single-binary)

- `web/dist` is built in CI and embedded via `//go:embed all:dist` into a new `internal/service/web_assets.go`.
- Single `mux.Handle("/", http.FileServer(http.FS(distFS)))` replaces all five page handlers; everything under `/api/`, `/lang/*`, `/telemetry` is matched first by the existing `mux.HandleFunc` registrations (Go's `ServeMux` precedence honors longer/more specific patterns).
- SPA fallback: a thin handler that serves `index.html` when the requested path isn't in `dist/`.
- Distribution unchanged — same single binary, same Dockerfile (with a `node:20` build stage inserted before the Go stage).

**Trade-offs:** keeps the "drop one binary on the box" property the project already has. Frontend reload is a full rebuild (`npm run build`) unless devs flip on Option A locally — which is fine, the two are complementary.

### Resolved: build-flag selectable

A small `web` Go package (sibling of `internal/`) exposes a `WebFS() (fs.FS, error)` symbol that the service consumes. Which file backs it is controlled by a build tag:

- **`!web_embed`** (default — what plain `go build` produces) — `WebFS()` returns `os.DirFS($web-root)` where `$web-root` comes from `--web-root /path/to/web/dist` (or `WEB_ROOT=...` env). If neither is set, the handler returns 404 and a hint to enable embedded mode. Lets a fresh-clone developer `go build ./...` without ever touching node.
- **`web_embed`** — `//go:embed all:dist` is compiled in; `WebFS()` returns the embedded FS. This is what the Dockerfile builds after running `npm run build`. Single binary, no `--web-root` needed at runtime.

The handler that serves the SPA (with `try_files`-style fallback to `index.html`) lives in [internal/service/web_assets.go](../../../internal/service/web_assets.go) and is build-tag-agnostic — it just consumes `WebFS()`.

Dev workflow is independent of the prod choice: `npm run dev` in `web/` runs Vite with `server.proxy` forwarding `/api`, `/lang`, `/brew`, `/telemetry`, `/ws` to `localhost:8080`. Devs never embed.

---

## 5. Migration phases

Each phase is a self-contained commit/PR that leaves the system shippable. Phases 1–3 are pure additions; phase 4 is the first deletion.

### Phase 0 — scaffold `web/` (no behaviour change)

- `npm create vite@latest web -- --template vue-ts`
- Add Vuetify 3 (since the legacy UI already uses it), `vue-router`, `vue-i18n`.
- Wire `vite.config.ts` `server.proxy` so dev server forwards `/api`, `/lang`, `/brew`, `/telemetry`, `/ws` to `localhost:8080`.
- Add an `npm run build` step to the Dockerfile in a `node:20` builder stage that copies the `web/dist` output into the Go build context.
- Add `web/dist/` to `.gitignore`.

### Phase 1 — `GET /api/site/config` + extract i18n strings

- New Go handler `/api/site/config` returns `{server_name, operator, host, server_info_html, lang}` from `s.cfg`.
- Decide i18n strategy (recommendation: port the de/en maps from `i18n_strings.go` into `web/src/i18n/de.json` and `web/src/i18n/en.json` once, then drop the Go-side `translate()` call from the page handlers in phase 4). A one-shot script in `scripts/` can dump the maps to JSON.
- The `/lang/de` and `/lang/en` redirect handlers stay so existing bookmarks keep working; the cookie is now read by Vue instead.

### Phase 2 — port the simplest pages

Pure-static pages first (no JS in the original), so reviewers can diff visually:
- `LandingPage.vue` (was `public_page.go`)
- `MitmachenPage.vue` (was `mitmachen_page.go`)

Mount them at the same URLs (`/`, `/mitmachen`) via `vue-router`. Both Go handlers still exist — Option A's proxy decides; in Option B the new static handler is registered last so it loses on exact matches. Verify with the [verify](../../../scripts/) skill or by hand.

### Phase 3 — port the live/map/admin pages

- `LivePage.vue` polls `/api/live/last-heard` (~50 lines of vanilla JS today, becomes one composable).
- `MapPage.vue` uses Leaflet via npm instead of CDN.
- `AdminDashboard.vue` is the largest port: ~5 fetches every few seconds, RadioID lookup-on-demand, plus the activity feed sub-poll. Keep the existing data shape — no Go changes.
- `LegacyVuetifyUI.vue` — the existing CDN-Vue file translates 1:1 into one SFC (it already has `data/computed/methods`). Decide whether to keep `/ui/legacy` as a route or just drop it; we already replaced it on `/ui`.

### Phase 4 — delete the Go HTML

Once phases 1–3 are live and the new pages have been smoke-tested against a federated node:
- Delete the five `render*Page` functions and the `*HTML` consts.
- Delete `dashboard_ui_vuetify.html` and `dashboard_ui_assets.go`.
- Delete the `{{T:…}}` `translate()` helper from `i18n.go` (keep `detectLang` + the `/lang/*` redirects — Vue still wants to know the cookie).
- Trim `i18n_strings.go` to only what server-side errors/log strings still need (probably empty — strings file likely deletes entirely).

Net Go LOC removed: ~2400 across `public_page.go`, `live_page.go`, `mitmachen_page.go`, `positions.go` (HTML portion), `admin_dashboard.go`, `dashboard_ui.go` (legacy handler), and the `i18n_strings.go` content.

### Phase 5 — wire both deployment modes

- Add `node:20` builder stage to `Dockerfile` that runs `npm ci && npm run build` in `web/`, copies `web/dist/` into the Go build context.
- Default Go build uses `-tags web_embed`; the resulting binary embeds `dist/`.
- Add a `--web-root` flag (and `WEB_ROOT` env) to `cmd/tetra-brew/main.go`; honored only in non-embed builds.
- Update `docs/nginx-single-port.conf` with an example SPA-fallback block for operators who want to skip the binary's static serving (still uses non-embed build).
- Smoke-test against `tests/federation-freecat/` and `tests/federation-loopback/` to confirm no API regression.

---

## 6. Resolved: `/ui/legacy` deferred to a follow-up PR

- The new `AdminDashboard.vue` is read-only monitoring. It does NOT include the SDS console / callout-thread / virtual-endpoint write surface that lives in `dashboard_ui_vuetify.html`.
- Porting the legacy file (~1155 LOC of inline CDN-Vue, including the multi-step callout flow logic) is decoupled from this branch.
- For this branch: the `/ui/legacy` Vue route is **dropped**. The Go binary continues to serve [internal/service/dashboard_ui_vuetify.html](../../../internal/service/dashboard_ui_vuetify.html) at `/ui/legacy` until the port lands in a separate PR.
- Phase 4 of this plan keeps `dashboard_ui_vuetify.html`, [dashboard_ui_assets.go](../../../internal/service/dashboard_ui_assets.go), and `handleDashboardUI` in place — only the four read-only HTML pages get deleted. The follow-up PR can then delete them alongside the SDS console port.

---

## 7. Risks / non-goals

- **Not refactoring the API.** Endpoints keep their current paths and JSON shapes; this is a pure UI lift.
- **Not changing federation, codecs, or any non-UI Go code.** This plan touches `internal/service/*_page.go`, `dashboard_ui*.go`, `i18n*.go`, plus the new `web/` tree and Dockerfile.
- **Auth.** None of the current HTML pages are authenticated; the admin dashboard is reachable by anyone who can hit the port. The migration does not change that — if we want auth, it's a separate plan layered after.
- **SEO.** The landing page (`/`) is currently server-rendered and crawlable. A Vue SPA at `/` is not, by default. If discoverability matters, add a static `index.html` with the landing copy baked in at build time (Vite SSG plugin or a tiny prerender step) — cheap to add later.
