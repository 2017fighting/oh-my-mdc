# Research: JavDB API surface for lists, magnets, and login

- **Date**: 2026-11-01
- **Ticket**: [#2](https://github.com/2017fighting/oh-my-mdc/issues/2)
- **Primary source**: static analysis of `/home/zhao/clone/javdb_rss` (the JavDB official-App private JSON API client, in production use since 2026-09) — its code comments, scratch notes, and real-token measurement logs. No authenticated live calls were made for this ticket; every "实测" below is a measurement recorded in javdb_rss, cited by path.
- **Scope**: the four operations oh-my-mdc needs — token init + validity test; reading the 想看 list (incl. list creation / id resolution); removing an item from a list; fetching a 番号's magnet candidates — plus the auth model and 风控 notes. Verdict on PLAN.md's bet that list polling needs no login state.

## TL;DR verdict table

| Operation | Endpoint | Surface | Auth | Evidence status |
|---|---|---|---|---|
| Signature/transport health check | `GET /api/v1/startup` | App API | **Anonymous** (jdsignature only) | ✅ 实测 — `internal/appapi/probe.go` |
| Login (get token) | `POST /api/v1/sessions`, form `username`+`password` | App API | **Anonymous** by definition | ✅ 实测 — `internal/appapi/login.go`, `notes/auth.md` |
| Token validity test | any user-scoped GET, e.g. `GET /api/v1/users/collected_actors` (javdb_rss uses this; `/api/v1/users` also seen) | App API | **Bearer token required** | ✅ 实测 — `cmd/javdb-rss/login.go` |
| Read 想看 list | `GET /api/v2/users/review_movies?status=want_watch&page=N` | App API | **Bearer token required** (anonymous ⇒ `JWTVerificationError`) | ✅ 实测 2026-10-04 — `internal/appapi/want.go` |
| Resolve list ids (清单 you created) | `GET /api/v1/lists/simple?page=N` | App API | **Bearer token required** | ✅ 实测 — `internal/appapi/lists.go`, `.scratch/javdb-rss-ui/notes/lists.md` |
| List detail (name) | `GET /api/v1/lists/{id}` | App API | Anonymous OK for `privacy:"open"` lists; `privacy:"own"` ⇒ `NoPermission` | ✅ 实测 — `internal/appapi/lists.go` |
| **Create a list** | **none found** | — | — | ❌ **Unverified gap** — no create-list endpoint appears anywhere in javdb_rss's endpoint survey (see Gaps) |
| List a list's movies | `GET /api/v1/movies/tags?filter_by=0:l:{listid}` (zone **must** be 0) | App API | Anonymous OK | ✅ 实测 — `notes/lists.md` §3 |
| Remove item from **想看** | `POST /api/v1/movies/{movie_id}/reviews`, form `status=watched` | App API | **Bearer token required** | ✅ 实测 at scale 2026-10-04 (221 writes, 0 failures, 3m14s) — `notes/want-push-deferred.md` |
| Un-mark a review entirely | `DELETE /api/v1/movies/{id}/reviews/{reviewId}` | App API | presumably token | ❌ **Never tested** — shape recorded only (`notes/want-push-deferred.md`) |
| Remove item from a **清单** (MovieList) | **none found** | — | — | ❌ **Unverified gap** |
| Magnet candidates for a 番号 | `GET /api/v1/movies/{movie_id}/magnets` | App API | **Anonymous OK** (token not needed) | ✅ 实测 — `internal/appapi/source.go`, `notes/auth.md` |
| 番号 → movie id resolution | `GET /api/v2/search?q={code}&limit=50` (**fuzzy** — exact-match on `number` yourself) | App API | Anonymous OK | ✅ 实测 — `internal/appapi/source.go` `resolveExact`, `notes/api-recon.md` §10.1 |
| Web (javdb.com HTML) | — | Web | cookie login session; TLS-RST'd from this network without proxy | ⛔ out of scope for javdb_rss; only noted as contrast |

**The bet in PLAN.md (“轮询清单应该不用登录态就可以，只有删除时采用登录态”) is falsified on its first half**: reading the 想看 list is a user-scoped endpoint and returns `JWTVerificationError` without a token (`internal/appapi/want.go` refuses to even send the request). What *is* true: the magnet-fetch step of the loop needs **no** token, and "login state" is not a web-style session — it's a **single, non-expiring Bearer JWT** (details below). So oh-my-mdc's poll loop must hold a token for the list read and reuse the same token for deletion; there is no way to keep the account fully logged-out of the service.

## Transport baseline (applies to every call)

Every App-API request, anonymous or not, must carry (all 实测, `internal/appapi/client.go`):

1. **8 common query params**: `app_channel, app_version, app_version_number, platform, system_version, device_model, device_name, device_uuid` — missing any one ⇒ HTTP 200 + `ParameterInvalid` naming the missing field.
2. **`jdsignature` HTTP header** (not a query param): `"{ts}.{suffix}.{md5(ts+prefix)}"` with two hardcoded constants reverse-engineered from the App (v1.9.28, re-validated against v1.9.35 servers; `internal/appapi/signature.go`, algorithm credit: MIT `FlanChanXwO/javdb-cli`). The signature **does not bind** the token, device, or request content — only the timestamp.
3. **Envelope** `{success, action, message, data}` — parse the envelope even on 4xx: signature *missing* ⇒ HTTP 200 + `ParameterInvalid`; signature *invalid* ⇒ **HTTP 400 + `InvalidSignature`**. Classify by `action`, not status code (`internal/appapi/probe.go`, `notes/api-recon.md` §3).
4. `Authorization: Bearer <token>` header when a token is in play.

Credential-error `action`s (→ "re-acquire token", never retry): `JWTVerificationError, Unauthorized, LoginRequired, TokenInvalid, TokenExpired` (`internal/appapi/client.go` `authActions`).

**Host**: production talks to `https://jdforrepam.com` (directly reachable; `config.example.yaml`, `notes/api-recon.md` §2). The App resolves hosts dynamically via `/api/v1/startup` + a domain blob in the APK — domain rotation is a real (unhandled) upstream risk.

## Per-operation detail

### 1. Token init + validity test

- **Login**: `POST /api/v1/sessions`, `application/x-www-form-urlencoded`, fields `username` + `password` (**`username`, not `email`** — wrong name ⇒ `ParameterInvalid: 參數不能爲空: username`). Response `data.token` (historically also `access_token` — read both). No captcha, no device binding; wrong password ⇒ business error `IncorrentUsernameOrPassword` (upstream's own spelling). All 实测 2026-09-30 — `internal/appapi/login.go`, `notes/auth.md`.
- **⚠️ Single-session account**: a new login **immediately kicks** the previous session (measured: invalidation within ≤271ms of the second login returning; no grace period — `notes/auth.md` 复勘记录 §2). A service that logs in with the account password kicks the user's phone; auto-relogin turns into an infinite kick war (documented rationale in `notes/auth.md`). javdb_rss therefore treats re-login as an explicit user action with a printed warning (`cmd/javdb-rss/login.go`).
- **Token lifetime**: decoded JWT is HS256 with payload `{id, username}` and **no `exp`** — it never expires on its own; it dies only when another login takes over (`notes/auth.md`). One-shot acquisition is therefore the normal operating mode.
- **Alternative acquisition (no kick)**: `scripts/export-phone-token.sh` in javdb_rss reads the live token out of the running App's memory via adb+root, so service and phone share one session (`notes/auth.md` "从 App 里挖 token"). Token storage on-device is KeyStore-encrypted; offline extraction is cryptographically impossible — memory-scrape is the only route.
- **Validity test**: javdb_rss's `login` command verifies the fresh token by calling a **credential-requiring** endpoint (`CollectedActresses`) — "login succeeded" alone proves nothing (`cmd/javdb-rss/login.go`). For oh-my-mdc, `GET /api/v1/users` (seen in the App's endpoint list, `notes/api-recon.md` §6) is the cheaper equivalent; note `GET /api/v1/users/collected_lists` is **broken upstream (HTTP 500)** — do not use it as a probe.
- **Config convention**: env var `JAVDB_TOKEN` takes precedence over a token file (bare JWT or `{"token":...}` JSON, 0600) — `notes/auth.md`, `config.example.yaml`.

### 2. Reading the 想看 list

- `GET /api/v2/users/review_movies?status=want_watch|watched&page=N` — `data.movies[]` is the `movieSlim` shape: `id, number, title, origin_title, cover_url, duration, release_date, has_cnsub, magnets_count, can_play, ...` (`internal/appapi/want.go`, `notes/api-recon.md` §10.1). `magnets_count == 0` ⇒ skip the magnet fetch entirely (safe short-circuit, 实测).
- **Paging**: fixed **10 items/page**, `current_page` returned; **empty page = end**; no total count. Page cap needed to defend against a hostile/broken upstream; javdb_rss caps at 40 pages (400 works) with a loud `Truncated` signal — real account measured at 234 works / 24 pages (2026-10-04), which already exceeded the original cap of 20 (`internal/appapi/want.go` `maxWantPages`).
- **Token required**: anonymous call ⇒ `JWTVerificationError` (实测). `want.go` returns `ErrNoToken` without sending.
- ⚠️ 想看 (`review status=want_watch`) is **not** the same as 收藏番号 (`/api/v1/users/collected_codes`) — the App UI marks 想看 on a movie page; oh-my-mdc wants the former (`internal/appapi/want.go` header comment).
- Resolving **清单** (user-built movie lists, e.g. the planned 想看中文 list): `GET /api/v1/lists/simple` → `{lists:[{id, name, privacy, is_default, movies_count, has_movie}]}`, token required, 5 lists measured, same empty-page paging (`internal/appapi/lists.go`, `notes/lists.md` §3). Use **id, not name**, exactly as PLAN.md already assumes. A list's movies: `GET /api/v1/movies/tags?filter_by=0:l:{id}` — zone must be **0**; wrong zone silently returns *other* movies (实测 4/4 exact count match at zone 0; zone 2 returned a full page of unrelated items).
- **Creating a list: no endpoint found** in javdb_rss's entire knowledge base (its libapp.so strings survey, ~90 endpoints, lists no list-creation route — `notes/api-recon.md` §6). See Gaps.

### 3. Removing an item from the 想看 list

- **The proven route is a *move*, not a delete**: `POST /api/v1/movies/{movie_id}/reviews` with form `status=watched` (or `want_watch`), `score=<int>` (0 ok), `content=<string>` (empty ok). A movie has exactly **one** review per user; posting `status=watched` makes it disappear from `want_watch` and appear in `watched`. Batch-proven 2026-10-04: 234→12 works, 221 writes, zero failures, 3m14s (~0.88s/write, serial) — `notes/api-recon.md` §6, `notes/want-push-deferred.md`, `cmd/markwatched/main.go` (its header comment predates that test run and still says "never tested" — the notes are authoritative).
- **Token required** (it mutates account state).
- **The write takes a movie `id`, not a 番号** — resolve first (see §4 search). `reviewId` values are big integers that turn into scientific notation if decoded as `float64` (prior-art trap recorded in `notes/want-push-deferred.md`).
- **True unmark** `DELETE /api/v1/movies/{id}/reviews/{reviewId}`: **never tested** — contract recorded from prior art only. If oh-my-mdc wants the item gone from *both* 想看 and 看过, this is the only candidate and it is unverified.
- **Ordering invariant** (matches PLAN.md point 7): push to qBittorrent **first**, write back to JavDB **after** — a write-back that succeeds before the download is queued loses the work forever. qBittorrent dedupes by infohash, so re-pushing on write-back failure is harmless (实测, `notes/want-push-deferred.md`).
- **Removing from a 清单** (e.g. a future 想看中文 list): **no endpoint found** — see Gaps.

### 4. Magnet candidates for a 番号

- **Resolve 番号 → movie id first**: `GET /api/v2/search?q={code}` is **fuzzy/prefix** — returns up to 10 near-misses by default (`limit` works, caps at **50**); you must exact-match `number` (case/whitespace-normalized) yourself and **never fall back to the first hit** — mismatches download the wrong movie silently (`internal/appapi/source.go` `resolveExact`, `notes/api-recon.md` §10.1). A 番号 can map to multiple movie ids (compilations).
- **Magnet list**: `GET /api/v1/movies/{movie_id}/magnets`, **anonymous OK** (实测 `notes/auth.md`). Returns **all** magnets in one call, no paging observed. Fields: `name, hash, size, cnsub, hd, files_count, created_at`, sometimes `pikpak_url` (`internal/appapi/source.go` `magnetWire`).
  - `hash` = infohash → `magnet:?xt=urn:btih:{hash}` (javdb_rss builds magnet URIs from it).
  - `cnsub` is a **per-magnet** boolean — the only subtitle signal javdb_rss trusts; do **not** parse `name` for "中文字幕" (实测 counter-example exists: name says 中文字幕, flag false — `notes/api-recon.md` §10.4.1). `has_cnsub` on the movie is a coarser work-level flag.
  - `size` unit is **MiB** (cross-checked against javdb.com's `data-size`, `notes/api-recon.md` §10.4).
  - `created_at` day-granularity; format drifted between `MM/DD/YYYY` and `YYYY-MM-DD` observations — parse both (`notes/api-recon.md` §10.4.1, `internal/feed`).
  - **Order is NOT chronological** — a curation-tier ordering (cnsub/破解/4K tiers); reproducible deterministic ordering needs explicit sort keys (javdb_rss: cnsub-first then newest `created_at`, tie-break infohash — `internal/catalog/select.go`, `CONTEXT.md` 槽位规则). This is directly relevant to oh-my-mdc's magnet-choice policy (map's open item).
- `GET /api/v1/search_magnet?q=` exists but **lacks `cnsub`/`hd`** and gives ISO timestamps — wrong endpoint for this job (`notes/api-recon.md` §10.4).
- Cost: one magnets request per work with `magnets_count > 0`; measured ~0.8–0.9s/request serial, **concurrency 8** is javdb_rss's politeness ceiling (6.75s→1.30s for a 50-work page; 16 only buys 0.44s more) — `internal/appapi/client.go`, `notes/want-push-deferred.md` (full 想看 sweep of 234 works ≈ 24 list pages + 222 magnet calls ≈ 20s wall clock at concurrency 8).

## Auth model summary

- **There is no web-style login session.** Auth = one **Bearer JWT** (HS256, payload `{id, username}`, no `exp`) acquired once via `POST /api/v1/sessions` or scraped from the phone's live session. Every user-scoped read and every write carries it; everything else (search, magnets, movie lists, startup) works anonymously with just `jdsignature`.
- **The account is single-session**: any new login invalidates the old token within ≤271ms. Consequences for oh-my-mdc: (a) don't auto-relogin; (b) prefer sharing the phone's token (`scripts/export-phone-token.sh` route) or dedicating an account; (c) on `AuthError`, surface "re-acquire token", never retry.
- **`device_uuid` is not auth-bound** (same token + 3 different uuids all accepted, 实测 `notes/auth.md`); whether the server uses it for rate-limiting/风控 internally is **unobservable** — keep one stable identity per deployment as a precaution.

## Rate-limit / 风控 notes

- **No documented rate limit and no observed 429** anywhere in javdb_rss's production history. The only throttling evidence is latency (~0.8–0.9s/req) and the self-imposed concurrency cap of 8 ("我们是第三方上游的客人" — `config.example.yaml`, `internal/appapi/client.go`). Treat absence-of-429 as unproven safety, not a contract.
- Write-path scale data point: 221 sequential writes over 3m14s, zero rejections (2026-10-04).
- Failure taxonomy for alarms: `InvalidSignature`/`ParameterInvalid` = signature constants rotted (fix code, retries useless — will happen on App/server rotation); auth actions = token kicked; 5xx/network = retryable. Upstream health must distinguish "never probed" from "probed and failed" (`CONTEXT.md` 上游健康).
- Host `jdforrepam.com` is itself discoverable/rotatable via `/api/v1/startup`; javdb_rss hardcodes it in config.
- `javdb.com` (web) is TLS-reset from this network without a proxy (`notes/api-recon.md` §7) — one more reason the App API is the right surface; oh-my-mdc's prod box reaches DMM via mihomo, not JavDB.

## Explicitly unverified (gaps for downstream tickets)

1. **Creating a 清单** — no create-list endpoint appears in javdb_rss's libapp.so endpoint survey (`notes/api-recon.md` §6); the APK is not in any repo, so this cannot be re-checked statically today. oh-my-mdc's "一键创建多个清单" (PLAN.md point 2) currently has **no known implementation route** on the App API; fallback is manual creation in the App + id capture via `/lists/simple`. A fresh `strings libapp.so` sweep against the pinned APK (URL + sha256 recorded in `notes/api-recon.md` §0) is the way to close this.
2. **Removing a movie from a 清单** — same survey shows nothing (only `collected_lists` read, which 500s). v1 uses 想看, so v1 is unaffected; the 想看中文 list flow (v2) would be blocked on this.
3. **`DELETE /api/v1/movies/{id}/reviews/{reviewId}`** (true unmark, removes from both 想看 and 看过) — shape from prior art, never fired. The proven alternative (POST `status=watched`) moves the item to 看过 instead of deleting it.
4. **Server-side use of `device_uuid` for 风控/limiting** — externally unobservable by construction.
5. **Rate limits / 429 behavior** — never observed; no official docs exist for this private API.
6. **Cross-session magnet ordering stability** — same-session stability 实测; whether `magnets[]` order drifts across days is unknown (`notes/api-recon.md` §10.4.1).
7. Whether `/api/v1/users` alone is sufficient as a cheap token probe (javdb_rss used `collected_actors`); endpoint exists in the survey but the probe-on-`/users` combination specifically wasn't exercised.

## Sources

All under `/home/zhao/clone/javdb_rss`:

- `CONTEXT.md` — vocabulary and upstream model (WantWatch vs MovieList vs Collection, signature constants, upstream health).
- `internal/appapi/client.go` — transport, common params, envelope/error taxonomy, concurrency data.
- `internal/appapi/signature.go` — jdsignature algorithm + constants and provenance.
- `internal/appapi/probe.go` — `/api/v1/startup` health check; signature failure actions.
- `internal/appapi/login.go`, `cmd/javdb-rss/login.go` — login contract, token verification flow, kick warning.
- `internal/appapi/want.go` — 想看 read contract, paging caps, real-account measurements.
- `internal/appapi/lists.go`, `.scratch/javdb-rss-ui/notes/lists.md` (+ `evidence/lists/*.json`) — list discovery/detail/movie-list contracts, zone-0 trap, `collected_lists` 500.
- `internal/appapi/source.go` — `magnetWire` fields, fuzzy-search exact-match rule, `limit` cap 50.
- `cmd/markwatched/main.go` — write path (`POST .../reviews`) implementation.
- `.scratch/javdb-rss/notes/api-recon.md` — endpoint survey from libapp.so + real-request contract appendix (§10).
- `.scratch/javdb-rss/notes/auth.md` — token lifecycle, single-session semantics, device_uuid experiment, export-from-phone route.
- `.scratch/javdb-rss/notes/want-push-deferred.md` — want-read/move-write contracts, batch-write results, push-before-writeback ordering, qbt dedup.
- `config.example.yaml` — host, token env/file precedence, magnet_concurrency rationale.
