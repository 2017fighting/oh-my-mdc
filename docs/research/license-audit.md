# License and portability audit of the reference repos

- **Date:** 2026-11-01
- **Ticket:** [#6 — Research: License and portability audit of the reference repos](https://github.com/2017fighting/oh-my-mdc/issues/6)
- **Scope:** `/home/zhao/clone/open-mdcng`, `/home/zhao/clone/javdb_rss`, `/home/zhao/clone/mdc-lite` (local clones are the primary sources; GitHub repo metadata cross-checked via `gh api` where reachable).

## 1. License verdicts

### 1.1 javdb_rss — MIT ✅

- **Source:** `javdb_rss/LICENSE` — full MIT text, `Copyright (c) 2026 2017fighting`. GitHub API confirms public repo, license `MIT`, default branch `main`.
- **Obligation when porting:** MIT requires only that *"[t]he above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software"*. Keep the notice (e.g. a `THIRD_PARTY_NOTICES.md` entry naming `javdb_rss`, © 2026 2017fighting, MIT, plus the file paths ported). No derivative-work licensing, no source disclosure.
- **Embedded third-party code — must travel with the port:** `javdb_rss/THIRD_PARTY_NOTICES.md` discloses that `internal/appapi/signature.go` (the `jdsignature` header algorithm, two constants + three-step concatenation) was taken from **FlanChanXwO/javdb-cli** (`internal/javdb/protocol/signature/sign.go`, MIT, © FlanChanXwO), which reverse-engineered it from JavDB.apk 1.9.28; javdb_rss re-validated it against the 1.9.35 server. `signature.go`'s own package comment repeats this attribution. Porting `signature.go` **carries javdb-cli's MIT attribution with it** — port the notice entry verbatim.
- **Authorship:** sole committer `Ryan Valor <272567571@qq.com>` (git log); remote `github.com/2017fighting/javdb_rss`. The LICENSE copyright line ("2017fighting") plus that sole committer identity ties the GitHub account to this human — see §1.4.

### 1.2 open-mdcng — MIT ✅

- **Source:** `open-mdcng/LICENSE` — full MIT text, `Copyright (c) 2026 Ryan Valor`. (GitHub API metadata for this one repo could not be re-verified — two transient `EOF` failures — but the local clone is the primary source and its remote is `github.com/2017fighting/open-mdcng`.)
- **Obligation when porting:** same as above — preserve the © Ryan Valor MIT notice on substantial portions ported. Nothing more.
- **Authorship:** committers are `Ryan Valor <272567571@qq.com>` and `pi <pi@local>` (an AI agent, `Co-Authored-By: pi` trailers). **No third-party human contributor** appears in the history. AI-assisted commits do not create a competing copyright claim that the MIT file doesn't already cover; as always, the safest reading is that the MIT grant is what authorizes the port, and it does.
- **Provenance note:** `open-mdcng/CONTEXT.md` describes the project as an "开源重写 mdc-ng" (open rewrite of the media-scraping tool mdc-ng). oh-my-mdc plans to *replace* the closed-source mdcng deployment; this audit found nothing in open-mdcng's tree that imports or embeds closed-source code — the files examined are original Go + YAML. Do not port anything from the *closed-source* mdcng itself (it is not one of the three repos under audit); nothing here authorizes that.

### 1.3 mdc-lite — ⚠️ NO LICENSE (all rights reserved by default)

- **Source:** `find` over the whole tree (case-insensitive `*license*`, `COPYING*`, `NOTICE*`) returns nothing; GitHub API confirms public repo with license `NONE`. There is **no grant to copy, modify, or redistribute its code** — publishing without a license means default copyright law applies to everyone who is not the rights holder.
- **What that means for porting:** two distinct audiences.
  1. **The oh-my-mdc owner (2017fighting) may port freely** — *if* the authorship evidence in §1.4 holds, the sole copyright holder is porting their own code into their own project; a missing license binds third parties, not the rights holder's own use.
  2. **The public record still reads as a violation.** oh-my-mdc is a public repo. Any auditor (human or tool) diffing ported code against an unlicensed public repo sees unlicensed copying; "the author is me" is unverifiable from the repos alone. The clean fix is one commit: **add an MIT LICENSE to mdc-lite before porting anything from it** (owner's call, matches their other repos).
- **What is unambiguously fine even without a license:** reading `mdc-lite/docs/adr/` (ADR-0004…0020) and `docs/research/` and *reimplementing* the ideas. Ideas, designs, and ADR decisions are not copyrightable subject matter; only expression (code, prose) is. mdc-lite's value to oh-my-mdc is largely at that level — e.g. ADR-0007 (gonja templates for NFO/paths), ADR-0015 (poster crop is v1), ADR-0005 (egress is not the application's business), and its real-Jellyfin acceptance-harness pattern.

### 1.4 Authorship evidence (why §1.3's owner-port carve-out is defensible — and its limit)

- `javdb_rss/LICENSE` says `Copyright (c) 2026 2017fighting`, and javdb_rss's *sole* committer is `Ryan Valor <272567571@qq.com>` — linking that pen name to that account.
- The identical sole human committer signs **all of open-mdcng and mdc-lite**, and all three remotes live under `github.com/2017fighting/`, the account that owns oh-my-mdc and filed this ticket.
- Limit: name/email match is evidence, not proof. The account owner can trivially confirm (and cure §1.3 by licensing mdc-lite). This audit records the chain so the conclusion is checkable rather than asserted.

### 1.5 Adjacent finding: oh-my-mdc itself has no LICENSE

oh-my-mdc is public (GitHub API) with no LICENSE file. Whatever it ports must carry upstream notices, and the repo should pick its own license — MIT is the obvious match to the owner's other repos. Out of this ticket's scope; flagged for the owner.

### 1.6 GPL / copyleft exposure

None found at the repo level: the only license texts in the three trees are the two MIT files above (mdc-lite carries none). Per-transitive-dependency licenses were **not** individually audited; note that a dependency dragged in by ported code carries exactly the same obligation as the same dependency added to `go.mod` directly, so it is a build-hygiene question, not a porting question — run a license scan (e.g. `go-licenses`) when dependency choices settle. (Secondary, pre-researched: `mdc-lite/docs/research/config-libs.md` already catalogues MIT/BSD licenses for gonja, goose, sqlite, fsnotify, go-toml.)

## 2. Allowed-port list

| From | What | Under what terms |
|---|---|---|
| javdb_rss (MIT) | Everything, in principle | Preserve © 2017fighting MIT notice; port the javdb-cli attribution when `signature.go` moves |
| open-mdcng (MIT) | Everything, in principle | Preserve © 2026 Ryan Valor MIT notice |
| mdc-lite (none) | **Nothing yet** | First add an MIT LICENSE to mdc-lite (one commit), then port as MIT; ADRs/docs may be *followed* today |

## 3. No-go list

| What | Why |
|---|---|
| Any mdc-lite **code** copy before it gets a LICENSE | §1.3: no grant exists for third parties; public-repo optics of porting from an unlicensed repo |
| open-mdcng `parser/` | oh-my-mdc v1 takes the 番号 from the qBittorrent tag; filename parsing is explicitly out of scope (map issue #1, *Out of scope*) |
| open-mdcng `scrape/` engine, registry, pool, scheduler **wholesale** | They import open-mdcng's architecture: coupled to `store` (pgx + modernc/sqlite) and the 6,400-LOC `config` package. oh-my-mdc is single-user with its own state model; porting this imports a framework, not a module |
| open-mdcng `fetch/flaresolverr.go` (unless needed) | Optional external FlareSolverr service; only worth porting if DMM anti-bot turns out to require it (ticket #4 decides) |
| Anything from closed-source mdcng the deployment | Not licensed by anything in this audit; open-mdcng is the authorized path |

## 4. Portable-module map

Sizes are non-test LOC (tests in parentheses). "Deps dragged" = non-stdlib plus same-module packages, from `go list -deps`.

### javdb_rss (MIT)

| Module | Size | Deps dragged | What it becomes in oh-my-mdc |
|---|---|---|---|
| `internal/appapi` — JavDB app-API transport: envelope, 8 identity params, `jdsignature`, pagination, per-endpoint files (want/lists/browse/collected/tags/actress_tags/login), liveness probe | 1,968 (2,776) | **Stdlib only** + `internal/catalog` (domain model types; catalog totals 804 non-test LOC) | `internal/javdb/appapi` — the transport layer of the 想看 poller. Near drop-in: no third-party deps, no config coupling. Highest-value port in this audit; production-proven (client docstrings cite 2026-09-28/1.9.35 live validation). Carries javdb-cli MIT attribution (`signature.go`) |
| `internal/mteam` — M-Team source added in v1.2.0 (tag confirmed: `git ls-tree v1.2.0` shows it; HEAD pins the release): client, torrent parse, source, loop, store | 1,718 (1,645) | **Stdlib only** + `internal/catalog` + `internal/pin` | Split the port: `client.go` (382) + `torrent.go` (203) become oh-my-mdc's `internal/mteam` API client verbatim; `source.go`/`loop.go` encode javdb_rss's RSS-loop semantics that oh-my-mdc's own task model replaces; `store.go` (455) is RSS-dedupe state tied to javdb_rss's DB — leave |
| `internal/pin/policy.go` — magnet-choice rule: `Desired` pure function (pick which magnet) + `KeepOrSwitch` (stickiness) | 134 (+269 `source.go`, +361 store) | `internal/catalog` | The precedent for the still-open "magnet-choice policy per 番号" decision (map #1). Port `policy.go` as the seed of oh-my-mdc's chooser; javdb_rss's own ticket split (policy vs. stickiness) is worth copying as design |

### open-mdcng (MIT)

| Module | Size | Deps dragged | What it becomes in oh-my-mdc |
|---|---|---|---|
| `organize/nfo.go` — renders `movie.nfo`: 16-field mapping, deliberate omissions (no `uniqueid`, no `genre`/`country` because Jellyfin splits on `/`) | 361 | stdlib + `plugins` (types) | oh-my-mdc's NFO writer, near-verbatim. The omission rationale in its comments is Jellyfin behaviour knowledge — port the comments too |
| `organize/images.go` — bare-name image layout: `poster.jpg`, `fanart.*`, `extrafanart/fanartN`; `Downloader` seam so transport stays injectable; never converts formats | 122 | stdlib | oh-my-mdc's image writer, drop-in behind whatever fetcher ticket #4 lands on |
| `organize/plan.go` + `execute.go` + `sanitize.go` + `run.go` — hardlink plan/execute (1,957 total for `organize/`) | 1,957 | stdlib (no direct third-party) but coupled to `config`, `parser`, `plugins`, `store`, `internal/clock` | The organize stage of oh-my-mdc. Port plan/execute with `parser` coupling cut (v1: 番号 comes from the tag) and `store` replaced by oh-my-mdc's task state. `refresh.go` = Jellyfin path-scoped rescan — directly reusable for the Jellyfin step |
| `plugins/image.go` — cover→poster crop toward 2:3 with `CropAnchor` (right-crop; PLAN.md's "right portion is the poster" lives here), min-deviation guard, webp via `golang.org/x/image/webp` | 324 (of `plugins/` 1,823) | `x/image/webp`, `x/text` | The poster-crop step of the image pipeline. Small, isolated, high-value |
| `plugins/translate.go` — AI translation via **OpenAI-compatible chat completions**; writes `TranslatedTitle` (never overwrites scraped `Title`, so directory names stay deterministic) | 407 | stdlib + config types | Exactly oh-my-mdc's optional AI-translation feature (map #1 standing decision: OpenAI-compatible endpoint). Port the plugin; keep its never-touch-Title rule |
| `scrape/dmm.go` — native DMM source: GraphQL `legacySearchPPV` (番号 → content-id resolution) then `ppvContent`; two requests = one logical fetch | 673 | `config`, `fetch` | oh-my-mdc's DMM metadata source (the URL/asset map is ticket #4's output). Port surgically — do **not** inherit the whole engine |
| `scrape/field.go` — the 16-field metadata model + merge rules that nfo.go/images.go consume | 434 | stdlib-ish (yaml for source defs) | The shared vocabulary between scrape and organize; port together with nfo.go |
| `fetch/` — shared Fetcher: browser-impersonating TLS (`bogdanfinn/tls-client` + fhttp), header disguise, pacing, challenge detection, health, optional FlareSolverr | 2,317 | `bogdanfinn/fhttp`, `bogdanfinn/tls-client`, `x/time/rate`, `x/net/publicsuffix`, `config`, `internal/clock` | Decide after ticket #4: DMM's anti-bot reality determines whether oh-my-mdc needs this whole chain (heavy: the tls-client tree is the single biggest dep drag in open-mdcng's go.mod) or starts with a thin client. If DMM needs it, this is the proven implementation |
| `scrape/javdb.go`, `avbase.go`, `fc2.go` — other sources | ~654 + … | engine, fetch, store, config | Not needed for v1 (JavDB metadata comes via javdb_rss's app-API port; oh-my-mdc scrapes DMM for images) — listed for completeness |

### mdc-lite (no license — conditional)

| Module | Size | Status |
|---|---|---|
| `internal/source/{dmm,javdb,javbus,fc2}` + `internal/fetch` + `internal/pipeline`/`tasks` — pipeline-architecture reimplementation; 20 ADRs; real-Jellyfin acceptance harness | 19,048 (15,982) | **No-go for code until licensed** (§1.3). Its ADRs and acceptance-harness *pattern* are free to follow. If licensed MIT, the interesting piece is the acceptance harness (`acceptance/`, per README: pinned real Jellyfin in compose, replayed source captures, asserts what the media server reads) — a pattern oh-my-mdc's spec ticket should steal |

## 5. Bottom line

1. **javdb_rss and open-mdcng are MIT — port what's worth porting, keep the notices.** The single must-carry attribution beyond the two MIT lines is javdb-cli's, which rides inside `internal/appapi/signature.go`.
2. **mdc-lite has no license: port nothing from it until it gets one** (one MIT commit by the owner fixes this; following its ADRs is fine today).
3. **Best-value ports, in order:** javdb_rss `internal/appapi` (stdlib-only, production-proven) → javdb_rss `internal/mteam` client + torrent → open-mdcng `organize/nfo.go` + `images.go` + `plugins/image.go` + `translate.go` → open-mdcng `scrape/dmm.go` + `field.go`. Avoid importing open-mdcng's engine/store/config framework; `fetch/` awaits ticket #4's verdict on DMM anti-bot.
4. oh-my-mdc itself should pick a license (MIT suggested) — flagged to the owner, not decided here.
