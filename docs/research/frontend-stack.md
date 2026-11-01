# Research: Frontend stack and design references

- **Date:** 2026-11-01
- **Ticket:** [#5 — Research: Frontend stack and design references](https://github.com/2017fighting/oh-my-mdc/issues/5)
- **Question:** Which frontend stack best fits "Go binary with embedded UI, shadcn + tailwind, framework where possible, Chinese-first, 好看"?

## Constraints (from ticket + issue #1 map)

1. Pure static output embedded in the Go binary (`//go:embed`) behind one HTTP server — **no Node/runtime at deploy**, no separate frontend hosting.
2. Pages: setup wizard (token/清单/qBittorrent/Jellyfin/M-Team/AI 翻译), task dashboard (番号 task table, states), manual retry. All data is fetched at runtime from the Go JSON API — no SEO, no public content.
3. All copy zh-CN; single-user, self-hosted.
4. shadcn/ui + Tailwind; "framework where possible" — prefer file-based routing / data-loading conventions over hand-rolled.

## Verdict table

| Candidate | Static output (official docs) | Server runtime needed? | shadcn fit | Build complexity | Fit |
|---|---|---|---|---|---|
| **Vite + React Router (framework mode, `ssr:false`)** | `react-router build` → `build/client/`, one `index.html` + hashed assets | No | Official `react-router` template | Low: `npm run build` → embed `dist` | ✅ **Recommended** |
| Vite + TanStack Router (library, no Start) | Plain Vite → `dist/` | No | Official `vite` template (components are plain React) | Low | ✅ Credible alternative (proven by refs below) |
| TanStack Start (SPA mode) | `spa.enabled` → `/_shell.html` + static assets in `dist/client` | No (if no server fns/routes used) | Official `start` template | Medium: SPA-mode config; hosting story is server-first | ⚠️ Works, but fights the framework's center of gravity |
| Next.js static export (`output:'export'`) | `next build` → `out/`, HTML per route | No | Official `next` template (its home turf) | High: long unsupported-features list; every page becomes `'use client'` anyway | ⚠️ Embed-friendly on paper, worst DX fit here |
| Astro | Static output first-class | No | Official `astro` template | Low-Medium | ❌ Islands model targets content sites; an interactive control panel is one big island |

## Candidate analysis (primary sources)

### 1. Vite + React Router framework mode — recommended

- SPA mode is one line: set `ssr: false` in `react-router.config.ts`; "the server build will no longer be generated" and `react-router build` pre-renders the root route at build time into a single `index.html`; deploy `build/client` to any static host. — [React Router SPA guide](https://github.com/remix-run/react-router/blob/main/docs/how-to/spa.md)
- Full static prerender is also available (`prerender: true` or a path array in `react-router.config.ts`) if we ever want per-route HTML shells. — [React Router pre-rendering guide](https://github.com/remix-run/react-router/blob/main/docs/how-to/pre-rendering.md)
- Caveats to know: the root route is still rendered at build time, so routes must be SSR-safe (no `window` during initial render); the host must direct all URLs to `index.html` — exactly what the Go handler already does. — SPA guide (above)
- shadcn: `pnpm dlx shadcn@latest init -t react-router` is an official supported template. — [shadcn installation docs](https://ui.shadcn.com/docs/installation)
- Precedent in this very ecosystem: `open-mdcng` already runs Vite + React 19 + react-router-dom 7 + Tailwind 4 + shadcn CLI, building straight into `internal/web/dist` with `//go:embed all:dist` and a Go SPA handler (deep-link fallback + hashed-asset caching) — the entire pipeline is a solved problem in code we can read (`/home/zhao/clone/open-mdcng/internal/web/embed.go`, `web/vite.config.ts`). `mdc-lite` is the same shape (Vite + React 19, `//go:embed all:dist` in `internal/web/web.go`).

### 2. TanStack Start (SPA mode) — viable, youngest

- SPA mode: `tanstackStart({ spa: { enabled: true } })` in `vite.config.ts`; build prerenders the root to `/_shell.html`; "A CDN that can serve static assets is all you need"; you add host rewrites so 404s fall back to the shell. — [TanStack Start SPA mode guide](https://github.com/TanStack/router/blob/main/docs/start/framework/react/guide/spa-mode.md)
- Static prerendering of arbitrary routes exists too (`prerender: { enabled: true, crawlLinks: true, … }`). — [static-prerendering guide](https://github.com/TanStack/router/blob/main/docs/start/framework/react/guide/static-prerendering.md)
- But the [hosting guide](https://github.com/TanStack/router/blob/main/docs/start/framework/react/guide/hosting.md) is server-runtime-first (Cloudflare/Netlify/Railway/Nitro/Vercel/Node/Bun; "serve `dist/client` as static assets and forward all other requests to the server entry's fetch handler"). We'd ignore the framework's headline features (server functions/routes) — Go owns the API.
- Maturity: `@tanstack/react-start` is v1.x (npm 1.169.4, 2026-11) with weekly rolling releases and recently restructured docs — expect churn. shadcn supports it officially (`init -t start`).

### 3. Next.js static export — embed-friendly on paper, worst fit here

- `output: 'export'` in `next.config.js`; `next build` emits `out/` with HTML/CSS/JS "for your application… deployable and hosted on any web server that can serve HTML/CSS/JS static assets". Server Components run **at build** — useless for runtime Go data. — [Next.js static exports guide](https://nextjs.org/docs/app/guides/static-exports)
- Unsupported list (same page): Route Handlers relying on Request data, Rewrites/Redirects/Headers/Proxy, ISR, Image Optimization with the default loader, Draft Mode, Server Actions, Intercepting Routes. A dashboard that only talks to `/api` must opt out of nearly everything Next is for; every page is `'use client'` + SWR/react-query.
- Verdict: it embeds, but we'd be using Next as "React with extra steps" while carrying its config surface and build toolchain.

### 4. Astro / others

- Astro has official shadcn support ([shadcn docs](https://ui.shadcn.com/docs/installation)) and truly static output, but its islands architecture is for content-heavy sites; a fully interactive control panel gains nothing. SvelteKit static is solid but shadcn is React — excluded.

## Recommendation

**Vite + React Router (framework mode, `ssr:false`) + Tailwind CSS v4 + shadcn/ui, `vite build` → `internal/web/dist` → `//go:embed all:dist`.**

- "Framework where possible": satisfied — file routes (`routes.ts`), typed routes, `clientLoader`/`clientAction` for the Go API, SPA mode one flag away, and it's the mature Remix lineage (react-router latest v8.5.1, v7.18.5 maintained — [releases](https://github.com/remix-run/react-router/releases)).
- Embed-friendliness: single `build/client` static dir, no runtime — port `open-mdcng`'s `embed.go` + SPA handler verbatim (deep-link fallback, hashed assets, `Check()` guard).
- shadcn + Tailwind: first-class, official template, proven in open-mdcng.
- **Runner-up:** plain **Vite + TanStack Router + TanStack Query** (no Start) — exactly what `satnaing/shadcn-admin` and `autobrr` run; slightly better type-safe routing/search-params DX, no framework server to opt out of. Choose it over React Router only if file-based typed routing + search-param state is prized; both embed identically (plain Vite `dist/`).
- zh-CN: no candidate constrains the language — copy lives in our components, `<html lang="zh-CN">`, font stack (PingFang SC / Microsoft YaHei / Noto Sans SC) via Tailwind theme. If bilingual ever matters, autobrr's react-i18next setup is the pattern. 好看 comes from the shadcn design system + the references below, not the framework.

## Reference repos worth cribbing

| Repo | License | Stars | Stack | What to take |
|---|---|---|---|---|
| [satnaing/shadcn-admin](https://github.com/satnaing/shadcn-admin) | MIT | ~16.3k | Vite + React 19 + **TanStack Router** + Tailwind 4 + shadcn + TanStack Query/Table | The aesthetic target: dashboard shell, sidebar/nav, tasks board + table page, settings sections, theming. MIT so components can be adapted directly with attribution. |
| [sadmann7/tablecn](https://github.com/sadmann7/tablecn) (ex shadcn-table) | MIT | ~6.4k | Next.js 16 + shadcn + TanStack Table/Query | The task-dashboard grid: sorting/filter/pagination toolbar, row actions, empty/loading states. Reference the UX, not the Next plumbing. |
| [autobrr/autobrr](https://github.com/autobrr/autobrr) | GPL-2.0 | ~3.2k | Go + Vite/React 19/TanStack Router, `//go:embed all:dist` (`web/build.go`), react-i18next | Domain sibling (torrent automation). Embed pipeline (`web/build.go`), release/event feed UI, filter/settings forms, i18n. GPL — pattern only, no code copy. |
| [navidrome/navidrome](https://github.com/navidrome/navidrome) | GPL-3.0 | ~24.5k | Go + embedded React UI (`ui/embed.go` `//go:embed all:build`) | Self-hosted media polish: media grid layouts, dark mode, settings page structure. GPL — pattern only. |
| [jxxghp/MoviePilot](https://github.com/jxxghp/MoviePilot) + [MoviePilot-Frontend](https://github.com/jxxghp/MoviePilot-Frontend) | GPL-3.0 | ~12k | Python/FastAPI + Vue 3 + Vuetify + Tailwind | The closest 好看 Chinese-first media-dashboard exemplar: info density, zh copy conventions, home/dashboard layout. Pattern only (different stack, GPL). |
| [immich-app/immich](https://github.com/immich-app/immich) | AGPL-3.0 | — | SvelteKit; first-run wizard at `web/src/routes/auth/onboarding/+page.svelte` | Setup-wizard pattern: stepwise onboarding (admin init → config → done) with server-validated steps. Pattern only. |
| [jeessy2/ddns-go](https://github.com/jeessy2/ddns-go) | MIT | ~17.5k | Go single binary, embedded web UI (`//go:embed static`, templates) | The "Chinese-first, configure-everything-in-browser, Go single binary" genre proof — minimal first-run config UX. |

License caveat: MIT refs (shadcn-admin, tablecn) are adaptable with notice; GPL/AGPL refs are look-but-don't-copy — see the parallel license-audit research (#6).

## Setup-wizard & task-dashboard patterns to adopt

- Wizard: multi-step form over a `GET /api/setup/status` gate — first load of an unconfigured server lands on `/setup` (Immich pattern); steps map 1:1 to PLAN.md items (token → 清单 → qBittorrent → Jellyfin → M-Team 可选 → AI 翻译 可选), each step test-then-save (JavDB token validity test is already a wizard requirement in PLAN.md).
- Task dashboard: shadcn `DataTable` (TanStack Table) à la tablecn, rows = 番号 tasks with state chips (下载中/待刮削/已入库/失败), row action = 手动重试 (POST + optimistic refresh via TanStack Query), plus a detail drawer for magnet candidates/history.
- Never-merged throwaway branch: this research lives at `research/frontend-stack` → `docs/research/frontend-stack.md`.

## Sources

- React Router: [SPA guide](https://github.com/remix-run/react-router/blob/main/docs/how-to/spa.md), [pre-rendering guide](https://github.com/remix-run/react-router/blob/main/docs/how-to/pre-rendering.md), [releases](https://github.com/remix-run/react-router/releases) (v8.5.1 latest)
- TanStack Start: [SPA mode](https://github.com/TanStack/router/blob/main/docs/start/framework/react/guide/spa-mode.md), [static prerendering](https://github.com/TanStack/router/blob/main/docs/start/framework/react/guide/static-prerendering.md), [hosting](https://github.com/TanStack/router/blob/main/docs/start/framework/react/guide/hosting.md), npm `@tanstack/react-start@1.169.4`
- Next.js: [static exports guide](https://nextjs.org/docs/app/guides/static-exports) (fetched 2026-11-01, docs v16.4.3), npm `next@16.4.3`
- shadcn/ui: [installation](https://ui.shadcn.com/docs/installation) — "Available for Next.js, Vite, Laravel, React Router, Astro, and TanStack Start… Supported templates: `next`, `vite`, `start`, `react-router`, and `astro`"
- Vite: [build docs](https://vite.dev/guide/build) (`base`, `outDir`), npm `vite@8.3.5`
- Local precedent: `/home/zhao/clone/open-mdcng` (`internal/web/embed.go`, `web/vite.config.ts`, `web/package.json`), `/home/zhao/clone/mdc-lite` (`internal/web/web.go`, `web/package.json`)
- Reference repos verified via GitHub API 2026-11-01 (stars/licenses/push dates) and `package.json`/source fetched from each default branch
