# Research: M-Team API for search and torrent download

- **Date:** 2026-11-01
- **Ticket:** [#3](https://github.com/2017fighting/oh-my-mdc/issues/3)
- **Question:** How does M-Team's API serve search-by-番号 and `.torrent` download? Auth (`x-api-key`), search semantics (keyword, categories incl. adult), torrent detail fields (size, seeders, free/leech status), the `.torrent` download flow (file, not magnet — what does adding it to qBittorrent require?), rate limits, adult-mode toggles.

## Sources and evidence levels

Every claim below carries its source. Three tiers, kept distinct:

| Tier | Source | How reached |
|---|---|---|
| **官方** | M-Team public wiki `wiki.m-team.cc/zh-tw/api` (+ `/en/api`) — server-rendered, no login | `curl` 2026-11-01, HTTP 200 |
| **官方** | Official OpenAPI 3.1 spec `test2.m-team.cc/api/v3/api-docs` (414 paths) — the spec JSON is public even though the swagger-ui page is not | `curl` 2026-11-01, HTTP 200, 225 KB |
| **实测** | Live no-key probe of `api.m-team.cc` by me, 2026-11-01 (no API key used anywhere in this research) | `curl` |
| **生产代码** | `javdb_rss` v1.2.0 — `internal/mteam/*` (+ its probe notes `.scratch/javdb-rss-mteam/notes/mteam-api.md`, dated 2026-10-13, made with a real key; that's *their* first-hand evidence, cited as such) | local clone at `/home/zhao/clone/javdb_rss` |
| **先例** | PT-depiler `src/packages/site/definitions/mteam.ts` (master), MoviePilot `app/modules/indexer/spider/mtorrent.py` (v3) | raw.githubusercontent.com 2026-11-01 |

## 1. Auth — `x-api-key`

- Third-party tools must use an **API Access Token** obtained at 【控制台 → 實驗室 → 存取令牌】, passed in the HTTP header `x-api-key`. Using cookies instead can get the account suspended. *(官方 wiki `/zh-tw/api`)*
- Token shape observed in production: 36-char UUID-form string. *(javdb_rss notes §1, first-hand 2026-10-13)*
- **Failures come back as HTTP 200** with a JSON envelope `{"code":…,"message":…,"data":…}`; success is `code == "0"` (wire format is a string, though the OpenAPI spec declares `code` as integer — javdb_rss parses the string). Observed failure shapes:
  - Invalid key → `200 + {"code":"1","message":"key無效"}` *(javdb_rss notes §1)*
  - No key → `200 + {"code":"401",…}` — my own probe today: `POST https://api.m-team.cc/api/torrent/search` without key → HTTP **200** `{"code":401,"data":null,"message":"Access Denied"}` *(实测 2026-11-01; also javdb_rss client_test.go)*. Message wording varies ("Access Denied" vs "Full authentication is required…"); classify on `code`, not wording.
- Two request-header traps, both first-hand *(javdb_rss notes §1, client.go)*:
  - **User-Agent must be non-empty and must not contain `curl`** — Cloudflare 302s such requests to `www.google.com`. Looks like auth failure, is a WAF block.
  - javdb_rss also sends `Origin: https://kp.m-team.cc/` and `Accept: application/json, text/plain, */*` (site's CORS check precedent).
- Allowed endpoint prefixes for third parties: `/admin/**`, `/login`, `/apikey/**` forbidden; under `/member/**` only a short allowlist (`/member/profile`, …); under `/msg/**` only statistic endpoints. *(官方 wiki `/zh-tw/api`)*

## 2. Search — `POST /api/torrent/search`

- **Request body is JSON** (form or query string gets 參數錯誤 for this endpoint). Body javdb_rss sends in production *(client.go)*:

  ```json
  {"mode": "adult", "keyword": "SSIS-001", "pageNumber": 1, "pageSize": 100, "visible": 1}
  ```

- Officially documented request schema `TorrentSearch` *(官方 OpenAPI spec)*: `mode` (string), `keyword` (string, ≤100 chars), `pageNumber` (1–1000), `pageSize` (**1–200**), `categories` (int[]), `visible` (int), `discount` (string filter), `labelsNew` (array filter), `sortField`/`sortDirection`, `imdb`/`douban`/`bangumi`/`dmmCode` (full-URL/ID searches), plus filter arrays (`sources`, `mediums`, `teams`, `videoCodecs`, …) and date ranges.
- **Adult-mode**: `mode: "adult"` is the AV area; `mode: "normal"` the general area. `mode=adult` + 番号 keyword is verified working in production: `keyword=SSIS-001` → 3 hits, all category `410` *(javdb_rss notes §6, first-hand)*. Other documented `mode` values (`movie`/`music`/`tvshow`/`waterfall`/`rss`/`rankings`/`all`) are **second-hand** (MCP tool schema, cited in javdb_rss notes §6) — not verified. ⚠️ There is **no API-side "adult toggle"** beyond `mode`/`categories` found in any source; PT-depiler merely defaults its adult *area* to disabled client-side *(mteam.ts `area_adult … enabled: false`)*. Account-level adult-content gating (if any) could not be verified.
- **Categories (adult area ids)** *(PT-depiler `siteCategory` table)*: `410` AV(有碼)/HD Censored, `424` SD Censored, `437` DVDiSo, `431` Blu-Ray, `429/430/426/432` AV(無碼) HD/SD/DVDiSo/Blu-Ray, `436` AV(網站)/0Day, `440` AV(Gay)/HD, `425/433` IV 写真 video/photo, `411/412/413` H-游戏/H-动画/H-漫画. Normal area starts at `401` 電影/SD … (21 ids). Search returns `category` per row.
- **Response row** (envelope `data.data[]`; wire shapes first-hand in javdb_rss client_test fixture, cross-checked in MoviePilot/PT-depiler):
  - `id` (torrent id, string) — needed for detail/download
  - `name` (torrent title, usually starts with the 番号, e.g. `SONE-086 2025 2160p DMM WEB-DL …-MTeam`)
  - `smallDescr` (short description, MoviePilot)
  - `size` — **string** of bytes (`"6834984408"`)
  - `labelsNew`: string[] (`"中字"`, `"4k"`, …); legacy `labels` is a packed id string ("4" = 中字)
  - `createdDate`: `"2021-02-22 15:19:51"` (space-separated, seconds precision)
  - `status.seeders` / `status.leechers` / `status.timesCompleted` — **strings**
  - `status.discount` / `status.discountEndTime` — **free/leech status** (see §3)
  - `infoHash`: **always `null`** (verified across 5 keywords/20+ rows in search *and* in `detail`'s 50-key payload) *(javdb_rss notes §4, first-hand)*
  - Pagination envelope: `data.{data, pageNumber, pageSize, total, totalPages}`.
- **`mode` note:** MoviePilot's client omits `mode` entirely and filters by `categories` — both parameter styles are in production use. javdb_rss uses `mode` only.
- IMDb search needs the **full URL** (`https://www.imdb.com/title/tt…`), not the bare id *(官方 wiki `/zh-tw/imdbtosearch`; MoviePilot `__get_params`)*.

## 3. Torrent detail fields & free/leech status

- `POST /api/torrent/detail` — body is **form/multipart** `id=<tid>` (JSON or query string → 參數錯誤) *(javdb_rss notes §1, first-hand)*. The official spec declares these as query parameters — **the spec is wrong on the wire**; the wiki itself warns the docs' json/form annotations may not match reality *(官方 wiki `/zh-tw/api`)*.
- Detail payload has ~50 keys incl. `infoHash: null`, `hasChineseSubtitle`, `labelsNew`, `smallDescr`; **no `magnet`, no `torrent` sub-object, no `announce`** *(javdb_rss notes §4, first-hand)*.
- ⚠️ **`hasChineseSubtitle` is unreliable** — measured `false` while `labelsNew` contains `中字`. Only `labelsNew` is trustworthy for the 中字 signal *(javdb_rss notes §7, first-hand; test `TestSearchCNSubOnlyFromLabelsNew`)*.
- **Free/leech status = `status.discount`** on the search/detail row, values *(MoviePilot `mtorrent.py` discount dicts; PT-depiler `mapDiscountToFactors`, aligned with Jackett's `MTeamTp.cs`)*:
  - `NORMAL` (default), `FREE` (download ×0), `PERCENT_50` (×0.5), `PERCENT_70` (×0.3, i.e. 70% off), `_2X` / `_2X_FREE` / `_2X_PERCENT_50` (upload ×2 with the stated download factor)
  - `status.discountEndTime` — when the promo ends; `status.promotionRule{discount,endTime}` — site-wide promo override; `status.mallSingleFree{status:"ONGOING",endDate}` — 成人区限时 free (PT-depiler comment: "成人区限时free")
  - `discount` is also a documented **request filter** on search *(官方 OpenAPI spec)* — PT-depiler exposes filter options 普通/30%/50%/免費 (`NORMAL`/`PERCENT_70`/`PERCENT_50`/`FREE`).
  - **Caveat:** javdb_rss never consumed `discount` (its own decoded fields are id/name/size/labelsNew/createdDate/seeders only), so the exact discount wire values are cross-verified from two precedent codebases, not from javdb_rss's probe dump. Treat value names as high-confidence-but-unreproduced here.

## 4. `.torrent` download flow (file, not magnet)

Two-step, verified end-to-end in production *(javdb_rss client.go `Download`, notes §5, first-hand 2026-10-13)*:

1. `POST /api/torrent/genDlToken` with **form-encoded** body `id=<tid>` → `data` is a download URL of shape `…/api/rss/dlv2?…`. The official spec confirms `dlv2`'s signed-parameter shape `RssDownloadV2Form {sign, t, tid, uid, useHttps}` (sign/t/tid/uid required) *(官方 OpenAPI spec)*.
2. `GET` that URL **with `x-api-key`** → it **302s to a CDN** (`https://fr1.halomt.com?playload=…`) → follow the redirect → the `.torrent` bytes (bencode, starts with `d`, `Content-Type: application/x-bittorrent`, ~130 KB observed).

Hard constraints discovered by live testing:

- **The download URL dies in ~5 minutes** (then `200 + {"code":"1","message":"連結不可用！ 超出有效期"}` — and note the body stays `application/x-bittorrent`-typed on expiry; distinguish by first byte `d` vs `{`). ⚒ This kills "hand qBittorrent the genDlToken URL" — qbt fetches later than that *(javdb_rss notes §5, ADR-0004)*.
- **Redirect policy must be split**: API endpoints must NOT follow redirects (any 3xx there = Cloudflare UA block); the download URL MUST follow (the 302 is the legit CDN hop) *(javdb_rss client.go, notes §5)*.
- **Private tracker, per-issue credentials**: the file's `announce` is `https://tra1.m-team.cc/announce?credential=<base64 of sign&t&tid&uid>`; `info.private = 1`; `announce-list` empty; **there is no passkey** *(javdb_rss notes §5, first-hand; credential param shape matches the official spec's `RssDownloadV2Form`)*. Each download re-issues a fresh `sign`/`t` (same `info` → same infohash). Credentials are account-bound: **rotating the M-Team key kills every previously issued .torrent**.
- Credential lifetime: user-observation in javdb_rss (not a measurement) says announce credentials from months ago still report fine — tracker does not appear to expire them by time.
- **No infohash from the API, ever** → to get one you must download the file and compute the v1 infohash yourself: SHA-1 over the **raw bencode `info` dict bytes** (not re-encoded — key order is not guaranteed on the wire) *(javdb_rss torrent.go + torrent_test.go)*. M-Team is a private tracker and modifies `info.source`, so its infohash differs from any public-tracker copy of the same content *(javdb_rss ADR-0004, CONTEXT.md)*. Magnet-with-`tr=` synthesis was evaluated and rejected: a magnet without metadata can't be guaranteed to resolve *(ADR-0004)*.

### What adding it to qBittorrent requires

- Nothing exotic: it's a standard v1 `.torrent`. Serve it over HTTP with **`Content-Type: application/x-bittorrent`** (qBittorrent only accepts that value for enclosure type — javdb_rss CONTEXT.md) and let qbt fetch the URL, or feed the bytes to its `torrents/add` API. Because `private=1`, qbt disables DHT/PEX for it and announces only to the embedded credential URL — **the embedded credential is the only auth**, which is why a served `.torrent` is an account credential leak if exposed *(javdb_rss README "⚠️ 这个端点会交出你的凭据")*.
- Acceptance-tested 2026-10-14 with a real key against real M-Team and real **qBittorrent v5.2.4**: the hash qbt computes equals the locally-computed v1 infohash bit-for-bit *(javdb_rss README feature table)*.
- Feed-side timing constraint observed: qBittorrent aborts a feed request after **30 s without byte transfer** — so search+download must NOT happen inside the feed render path; javdb_rss does verdicts in a background loop *(javdb_rss README "它怎么工作", source.go)*.

## 5. Rate limits — official "recommended" values *(官方 wiki `/zh-tw/api`, table "API 速率限制")*

| Behavior | Recommended rate |
|---|---|
| `/torrent/search` | 1000 / rolling 24 h |
| `/torrent/detail` | 100 / hour |
| Torrent download actions | 100 / hour |
| Torrent download quota | 1000 / day |

- These are **suggestions, not enforced thresholds** (wiki: "以下僅公布部分建議速率上限"). Actual enforcement unknown — javdb_rss explicitly chose **not** to stress-test the boundary *(notes §10)*. 5 back-to-back `genDlToken` calls showed no throttling (0.59–0.66 s each).
- **Quota is unreadable**: `/api/member/profile` carries no quota/usage fields *(javdb_rss notes §3, first-hand)* — you cannot query "how much is left".
- Practical numbers *(javdb_rss notes §3, first-hand)*: search 0.67–0.97 s, genDlToken 0.59–0.66 s, download 2.26–2.40 s → ≈3.8 s per hit; a 234-item backfill is **quota-bound (~1.2 h at 100/h), not latency-bound**. javdb_rss gates downloads with a sliding 1-hour window at 100 by default (`max_downloads_per_hour`).
- wiki security notes (same page): key ≈ account credential; never commit it publicly; rotate; leak ⇒ revoke. The wiki also says the API-key feature exists so third-party tools stop using cookies.

## 6. Capability map (for the prefer-mteam download path)

| Need | M-Team capability | Caveat |
|---|---|---|
| Search by 番号 | ✅ `mode:"adult"`, `keyword:<番号>`; precise in production | Adult area only; normal-area fallback deliberately not built in javdb_rss |
| Chinese-sub filter | ✅ `labelsNew` contains `中字` | `hasChineseSubtitle` field lies |
| Size / seeders | ✅ `size` (string), `status.seeders` (string) | Parse strings yourself |
| Free/leech | ✅ `status.discount` + `discountEndTime` (+ `promotionRule`, `mallSingleFree`) | Value names from precedent clients, not from an official enum |
| Categories | ✅ `categories[]` filter; `category` per row; adult ids 410–440 | — |
| Get `.torrent` | ✅ `genDlToken` (form) → URL → GET +follow 302 | URL expires ~5 min; two redirect policies; UA rules |
| Magnet | ❌ none (infoHash always null; private tracker) | infohash must be computed from the file |
| Identity for dedupe | ✅ v1 infohash computed locally | Account-bound credentials; key rotation invalidates issued files |
| Rate headroom | ✅ documented recommended limits | Quota unreadable; backfill quota-bound at 100 dl/h |

## 7. What could NOT be verified (stated, not guessed)

1. **Enforced (vs recommended) rate limits** — no public source; probing the boundary risks the account. Untested by design.
2. **Discount enum on the wire** — `FREE`/`PERCENT_50`/… come from MoviePilot/PT-depiler code; no official enum and javdb_rss's probe dump didn't capture it.
3. **`mode` values beyond `adult`** (`normal`/`movie`/… list is second-hand from an MCP tool schema).
4. **Account-level adult gating** — no evidence any toggle exists besides `mode`/`categories`; unverified either way.
5. **Swagger UI content** (`test2.m-team.cc/api/swagger-ui.html`) — requires test2 login (only the machine-readable `/api/v3/api-docs` is public, which is what this file cites).
6. **`code` field type** — official spec says integer; wire (javdb_rss probes + mine) shows string. Parsers should accept string.
7. **Whether `genDlToken` counts against the download quota** — deliberately unresolved in javdb_rss (gate covers both steps).

## 8. Source index

- M-Team wiki (official, public): `https://wiki.m-team.cc/zh-tw/api` and `/en/api`; `https://wiki.m-team.cc/zh-tw/imdbtosearch` — fetched 2026-11-01.
- Official OpenAPI spec (public): `https://test2.m-team.cc/api/v3/api-docs` — fetched 2026-11-01.
- Live probe (no key): `POST https://api.m-team.cc/api/torrent/search` — 2026-11-01, this research.
- javdb_rss @ v1.2.0, `/home/zhao/clone/javdb_rss`: `internal/mteam/client.go`, `client_test.go`, `torrent.go`, `loop.go`, `source.go`; `docs/adr/0004-mteam-guid-is-infohash.md`; `.scratch/javdb-rss-mteam/notes/mteam-api.md` (probe notes 2026-10-13); `README.md` §M-Team; `config.example.yaml`; `CONTEXT.md`.
- PT-depiler: `https://github.com/pt-plugins/PT-depiler` `src/packages/site/definitions/mteam.ts` (master) — fetched 2026-11-01.
- MoviePilot: `https://github.com/jxxghp/MoviePilot` `app/modules/indexer/spider/mtorrent.py` (v3) — fetched 2026-11-01.
