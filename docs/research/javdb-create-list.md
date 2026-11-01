# Research: Close the JavDB create-清单 gap (APK strings sweep)

- **Date**: 2026-11-01
- **Ticket**: [#15](https://github.com/2017fighting/oh-my-mdc/issues/15)
- **Target binary**: `jdb_official_v1.9.35.apk`, sha256 `3616335e465afe5f57376080ddd211ae61aad77e44ecf8aec9cb819deeea4a5e`, 34,140,434 bytes — the exact APK pinned in javdb_rss's recon notes (`.scratch/javdb-rss/notes/api-recon.md` §0). Re-downloaded from the pinned official source for this ticket and verified **sha256 and size match bit-for-bit**. Not committed to any repo.
- **Prior findings**: [docs/research/javdb-api.md](https://github.com/2017fighting/oh-my-mdc/blob/research/javdb-api/docs/research/javdb-api.md) (branch `research/javdb-api`, ticket #2).
- **Headline**: the prior "no create-list endpoint found" verdict was **wrong**. The App's own binary contains a bare `/api/v1/lists` route plus a native "New list" dialog, and the sweep also surfaced a remove-movie-from-list route (`/api/v1/lists/{id}/movie_actions`) that javdb_rss never recorded. The wizard's one-click 清单 creation now has a concrete candidate endpoint — dynamically **unverified** (static evidence only, see Verdict).

## Method

1. Downloaded the pinned APK from the URL recorded in javdb_rss `notes/api-recon.md` §0; verified sha256 `3616335e…4a5e` and 34,140,434 bytes — identical to the pinned fingerprint. APK not committed anywhere.
2. `unzip` → swept `lib/arm64-v8a/libapp.so` (Dart AOT snapshot, unobfuscated identifiers) with `strings`, extracting every `/api/v…` URL-shaped literal (99 unique after stripping pool-adjacency artifacts). Cross-checked `lib/armeabi-v7a/libapp.so` — identical lists-route set. `classes.dex` was not needed (the app is Flutter; the API layer lives in Dart — per `notes/api-recon.md` §1 and confirmed by `package:astarte/net/http_api.dart` symbols).
3. Unicode-aware sweep (ASCII + UTF-16LE runs via a small Python extractor) for Chinese UI strings — the Dart snapshot stores l10n values as UTF-8, so plain `strings` already surfaced them.
4. Around each candidate route, mined the unobfuscated Dart identifiers and l10n keys (function names, `get:`-prefixed l10n getters, form/query field names) to recover semantics that `strings` alone can't give (HTTP verb, payload fields).
5. Diffed the fresh sweep against every endpoint recorded in javdb_rss's notes (`api-recon.md`, `lists.md`, `auth.md`, `want-push-deferred.md`) and the prior findings doc.
6. Cross-checked against MIT prior art [FlanChanXwO/javdb-cli](https://github.com/FlanChanXwO/javdb-cli) (its source on GitHub) where it implements the same routes.
7. **No authenticated or anonymous live calls were made.** Everything below is static evidence from the APK + javdb_rss's recorded measurements + prior-art source. Dynamic behavior marked ❓ unverified throughout.

## Endpoint deltas vs prior findings

The full lists-route surface in the APK (both ABIs):

| Route | In prior findings/notes? | New evidence this sweep |
|---|---|---|
| `GET /api/v1/lists/simple` | ✅ measured (javdb_rss `lists.go`, `notes/lists.md`) | unchanged |
| `GET /api/v1/lists/{id}` | ✅ measured | unchanged |
| `GET /api/v1/lists/related` | ✅ in §6 survey only | prior art implements it: `GET /api/v1/lists/related?movie_id=&page=&limit=` (javdb-cli `endpoint/lists/lists.go`) |
| **`/api/v1/lists` (bare)** | ❌ **never recorded anywhere** | prior art implements `GET /api/v1/lists?page=&limit=&sort_by=` (sort_by **required by the server**, default `created`; page default 1, limit default 20 — javdb-cli `MyLists`). Plus the App's create-list UI (below) points at a **POST** on this same route — ❓ verb binding inferred, not proven |
| **`/api/v1/lists/{id}/movie_actions`** | ❌ **never recorded anywhere** | add/remove a movie to/from a list (see Verdict §2) — ❓ shape inferred |
| **`/api/v1/lists/{id}/collect_actions`** | ❌ never recorded | follow/unfollow someone else's list (same `*_actions` family as `/actors/{id}/collect_actions`); App l10n `mineCollectionLists` / `mineListCollectedDeleteAlert` |
| `GET /api/v1/users/collected_lists` | ✅ recorded (broken upstream, HTTP 500) | unchanged |
| `/api/v1/movies/{id}/reviews/{reviewId}` | ✅ recorded from prior art, "never fired" | **now confirmed present in the APK itself** (`/api/v1/movies/%s/reviews/%s`, plus `/like` and `/report` siblings); prior art *ships* a working `unmark` command on it — evidence upgraded, see Verdict §3 |

Non-lists routes newly noticed but irrelevant to this ticket (recorded for completeness): `/api/v1/actors/batch_uncollection`, `/api/v1/codes/{id}/collect_actions`, `/api/v1/movies/{id}/play` + `/resume_play`, `/api/v1/reviews/hotly`, `/api/v2/users/{id}/reviews`, `/api/v2/search_image`, `/api/v1/users/change_username|forgot_password|reset_password|promotion_logs|transaction_logs|unpaid_tickets|feedback`, the whole `/api/v1/wallets/*` and `/api/v{2,3,4}/plans*` surfaces, `/api/v1/about|helps|articles|ads|debug_logging|magnet_apps`. Full sweep reproducible with `strings lib/arm64-v8a/libapp.so | grep -oE '/api/v[0-9]/[^ "]*' | sort -u` (99 hits).

## Verdict

### 1. Create-list endpoint: **exists — `POST /api/v1/lists` (verb inferred), dynamically unverified**

Static evidence chain, each link from the binary:

- Route string `/api/v1/lists` (bare) exists in `libapp.so` — the only candidate route that can create; the other five lists routes are all `%s`-parameterized or read-only.
- The App has a **native new-list flow** (not a WebView): l10n keys `get:movieListsNewList` ("New list" button — value string `New list` present in the snapshot), `get:movieListWidgetHintName` with value **`List name (4-30 characters)`** (create/edit dialog, name field + client-side length validation), `get:movieListWidgetHintDescription` (description field), `get:movieListWidgetEditTitle` (`Edit list`), `get:movieNewListsSuccess` (success toast). Dialog symbol `ListDialog`.
- Companion code identifiers: `updateMyList`, `deleteMyList`, `getMyLists`, `_getMyList`, `MyListsPage`, `MyListsDetailPage` (+Presenter/Provider), `MovieListsPagePresenter`, `movie_lists_entity.dart` / `movie_lists_info_entity.dart`.
- Standalone HTTP-verb strings `POST`, `PATCH`, `DELETE` exist in the snapshot (bare `DELETE` at 0x8b3ba is an HTTP verb — the other five `DELETE` hits are SQLite `DELETE FROM`), consistent with a generic verb-taking request wrapper in `package:astarte/net/dio_utils.dart`.

**Inferred request shape** (Rails-style resource conventions + dialog fields; **everything in this paragraph is ❓ unverified**): `POST /api/v1/lists`, form-encoded `name=<4–30 chars>` and likely `description=<string>` (field-name strings `name`, `description`, `privacy` all exist in the snapshot; `privacy` is also a measured field of list objects). Response: standard `{success, action, message, data}` envelope; `data` presumably carries the created list's `id` — **unverified**. Update/delete: `updateMyList` → `PATCH`/`PUT /api/v1/lists/{id}`, `deleteMyList` → `DELETE /api/v1/lists/{id}` (same convention; ❓ unverified).

What could **not** be verified statically: the verb→route binding (Dio passes verbs as method calls, not string literals adjacent to routes), the exact body field set, and the response payload. One live `POST` with a throwaway name would settle all three — cheap to do inside the implementation ticket, deliberately not done here (mutates account state; this ticket is static-only).

**Wizard implication**: PLAN.md's one-click 想看(中文) creation now has a concrete implementation candidate instead of "no known route". Since it is unverified, the wizard ticket should implement create behind a single live probe (create → read back via `GET /api/v1/lists/simple` → optionally `updateMyList`-rename or `deleteMyList` if the probe list is wrong), with detection + manual instructions as the fallback path. Detection itself stays exactly as prior findings described (`GET /api/v1/lists/simple`, match by id not name).

### 2. Remove-movie-from-清单: **route exists — `/api/v1/lists/{id}/movie_actions`, shape unverified**

- Route string `/api/v1/lists/%s/movie_actions` in both ABIs — never recorded in javdb_rss.
- Code identifiers `setMovieAction`, `listMovieAction`, `movieListAction`; l10n `get:movieListsActionRemove` ("Remove" action) with confirm dialog `get:mineListDetailDeleteAlert`; field-name strings `movie_id` and `action` exist in the snapshot.
- Inferred (❓): `POST /api/v1/lists/{list_id}/movie_actions` with form `movie_id=<id>` + `action=<add|remove>` — values of `action` unknown; the `collect_actions` sibling family presumably shares the pattern. v1 (想看 flow) doesn't need this; it only needed recording. The v2 想看中文 flow's blocker is now a documented candidate rather than a blank.
- javdb_rss never exercised it; javdb-cli doesn't implement it. No live evidence exists anywhere.

### 3. `DELETE /api/v1/movies/{id}/reviews/{reviewId}` — **shape now triple-sourced, still never fired by our side**

- APK route string `/api/v1/movies/%s/reviews/%s` (plus `/like`, `/report` siblings) — new primary-source confirmation of the path shape the prior findings carried from prior art.
- App code identifier `deleteComment` (the App's comment/review deletion UI) backs it.
- Prior art [FlanChanXwO/javdb-cli](https://github.com/FlanChanXwO/javdb-cli) (`internal/javdb/appapi/endpoint/user/user.go`) **ships a working `unmark` command**: `DeleteJSON("/api/v1/movies/"+movieID+"/reviews/"+rid, nil, nil)` — **DELETE verb, no body, no query params**. `rid` is the review's `id` from the detail payload, formatted as an exact decimal integer (their test suite documents the float64→scientific-notation trap: `2.44850177e+08` silently hits a nonexistent review). README lists `mark`/`unmark` as deliberate state-changing commands, i.e. exercised functionality.
- Remaining ❓: response payload on success (presumably the standard envelope) — neither javdb_rss nor this ticket has fired it.

### 4. Bonus: `GET /api/v1/lists` (bare) is a better list-polling candidate than `/lists/simple`

Prior art's `MyLists` shows `/api/v1/lists` accepts `page`/`limit`/`sort_by` (sort_by required server-side, default `created`; limit default 20) — a paginated full-list read that returns richer entities than `/lists/simple` (javdb_rss measured `simple` returns `{id, name, privacy, is_default, movies_count, has_movie}`). The App's own sort menu keys (`mineListSortByNumber/Release/Score/Hit/WatchedCount/WantWatchCount/MagnetUpdate`) enumerate the UI's sort vocabulary. oh-my-mdc can stick with `/lists/simple` for id detection; nothing here forces a change.

## Explicitly unverified

1. **Verb bindings** for create/update/delete list and movie_actions — inferred from conventions + UI semantics + verb strings; `strings` cannot bind verb to route.
2. **Create-list request body** — `name` (4–30 chars, from the App's own dialog hint) is high-confidence; `description`/`privacy` membership is plausible (field strings + dialog) but unproven.
3. **Create-list response payload** — unknown; the wizard needs the new list's `id`, likely in `data`.
4. **movie_actions request shape and `action` values** — route + field-name strings only.
5. **DELETE reviews success response** — never fired by javdb_rss or this ticket (prior art ships it, so confidence is high).
6. All of this is from **v1.9.35**; older/newer app versions may differ (the pinned-APK fingerprint in javdb_rss is the anchor).

## Sources

- APK `jdb_official_v1.9.35.apk` (sha256-verified against the pin): `strings` over `lib/arm64-v8a/libapp.so` and `lib/armeabi-v7a/libapp.so` — route strings, Dart identifiers, l10n keys and values cited inline above.
- `/home/zhao/clone/javdb_rss/.scratch/javdb-rss/notes/api-recon.md` §0 (APK fingerprint + URL), §1 (Flutter/Dart form), §6 (prior endpoint survey).
- `/home/zhao/clone/javdb_rss/.scratch/javdb-rss-ui/notes/lists.md` + `internal/appapi/lists.go` (measured lists contracts: `/lists/simple`, `/lists/{id}`, zone-0 trap).
- Prior findings: branch `research/javdb-api`, `docs/research/javdb-api.md` (ticket #2).
- Prior art: [FlanChanXwO/javdb-cli](https://github.com/FlanChanXwO/javdb-cli) — `internal/javdb/appapi/endpoint/lists/lists.go` (`GET /api/v1/lists` with required `sort_by`), `endpoint/user/user.go` + tests (DELETE unmark shape, review-id formatting), README (`mark`/`unmark` as shipped commands).
