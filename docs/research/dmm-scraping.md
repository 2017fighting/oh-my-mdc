# Research: DMM scraping surface for images and metadata

- Date: 2026-11-01
- Ticket: [#4](https://github.com/2017fighting/oh-my-mdc/issues/4)
- Question: what does scraping DMM (FANZA) by 番号 actually look like — discovery (番号 → page/JSON), the high-res image set (fanart URL shape, right-crop geometry for poster, preview set for extrafanart), title/description fields (ja), anti-bot/geo constraints.
- Method: static reading of the two reference repos (`/home/zhao/clone/open-mdcng`, `/home/zhao/clone/mdc-lite`) plus their own prior research docs and live-captured fixtures, then a live reachability probe from this dev box. **No DMM host was reachable from this box** (see §7), so every live observation below is either (a) captured by the reference repos earlier on a JP-reachable machine, or (b) reproduced-as-blocked here today.

Evidence classes used below:

- **[code]** — provable from the reference repos' source.
- **[capture]** — real bytes captured live by the reference repos (captured fixtures / recorded live runs).
- **[blocked]** — this box today: connection attempted, TLS handshake cut.
- **[unverified]** — no primary source could settle it.

---

## 1. Discovery: 番号 → content id (undocumented GraphQL, two legs)

DMM scraping does **not** hit the HTML site. Both repos talk to the site's own player API **[code]**:

- Endpoint: `POST https://api.video.dmm.co.jp/graphql`, derived from the configured domain as `https://api.video.<domain>/graphql` with Referer `https://video.<domain>/` (`open-mdcng/scrape/dmm.go` `dmmEndpoints`, domain default `dmm.co.jp` in `open-mdcng/config/defaults.go:157`; identical in `mdc-lite/internal/source/dmm/dmm.go` `endpoints`/`defaultDomains`).
- Request body is `{"query": "<text>"}`. Values are **interpolated into the query text as JSON-quoted strings, not passed as GraphQL variables** — deliberate, because the endpoint is undocumented and a guessed variable type fails server-side with a schema error (`open-mdcng/scrape/dmm.go` header comment; `mdc-lite/internal/source/dmm/dmm.go` above `searchQuery`).

### Leg 1 — search: `legacySearchPPV` **[code + capture]**

```graphql
query SearchPPV {
  legacySearchPPV(limit: 30, floor: AV, sort: SALES_RANK_SCORE, queryWord: "ssis001") {
    result { contents { id } pageInfo { totalCount } }
  }
}
```

- Response: `data.legacySearchPPV.result.contents[].id` — candidate product ids only. The former `makerContentId` field was removed from the schema (the endpoint answered `422` on the drift) — `open-mdcng/docs/source-notes.md` dmm §, `open-mdcng/scrape/dmm.go` search comment.
- `sort: SALES_RANK_SCORE` — sales rank, **not relevance**; the window of 30 is deliberate because the exact entry is not always near the front (SSIS-001's first product sits 19 ranks in; its search answers 95 results — `open-mdcng/scrape/dmm.go` `dmmSearchLeg` comment). **[capture]** mdc-lite's real capture `mdc-lite/internal/source/dmm/testdata/captures/dmm-SSIS-001-search-ssis001.json` (live 200, captured 2026-10-29 per its `fixtures.json` manifest) shows exactly this: 30 ids, `totalCount: 95`, and `ssis00001` **not** among them.

### The id ≠ the 番号: two zero-padding spellings **[code + capture]**

The product id is label prefix + sequence, and the sequence is spelled two ways: S1's first title is `ssis00001`, every later one is `ssis001NN`. Search is a substring match, so `ssis001` finds the 94 later entries but not the first. Both spellings are tried, reduced first, then the 5-wide zero-padded form (`SSIS-001 → ["ssis001", "ssis00001"]`; a number already 5-wide costs one leg) — `open-mdcng/scrape/dmm.go` `dmmSearchTerms`/`dmmPaddedDigits=5`; same in mdc-lite `searchTerms`. **[capture]** the padded leg's fixture `dmm-SSIS-001-search-ssis00001.json` exists precisely because leg 1 missed.

### Exact-match rule: identity, never position **[code]**

A search for `SSIS-001` returns `SSIS-0011` as a neighbour. Every candidate is compared by **identity** — lowercased family prefix + numeric value with leading zeros stripped — so `ssis00001` ≡ `SSIS-001` but `ssis001` ≢ `SSIS-0011` (`dmmSameWork` in both repos). Zero candidates → the content leg is not spent. FC2 numbers are excluded up front by the source's applicability regex (everything except the FC2 family and empty prefix).

### Leg 2 — content: `ppvContent` + `reviewSummary` in ONE POST **[code + capture]**

```graphql
query ContentPageData {
  ppvContent(id: "ssis00001") {
    id floor title description deliveryStartDate makerReleasedAt duration
    packageImage { largeUrl mediumUrl }
    sampleImages { largeImageUrl }
    sample2DMovie { highestMovieUrl }   # mdc-lite omits this + VR line (no trailer field)
    sampleVRMovie { highestMovieUrl }
    actresses { name imageUrl } directors { name } series { name }
    maker { name } label { name } genres { name }
  }
  reviewSummary(contentId: "ssis00001") { average total }
}
```

- `data.ppvContent == null` after a resolved id is a normal miss (`Found: false`), not an error.
- **[capture]** the real capture `dmm-SSIS-001-content.json` (live 200, 2026-10-29) is the ground truth for every URL shape in §3.
- Request cost per 番号: **1–2 search legs + 1 content leg** (three in the worst case, two typically).

## 2. Request headers, pacing, anti-bot **[code]**

Five headers travel with every GraphQL POST (both repos, identical):

```
Referer: https://video.dmm.co.jp/
Content-Type: application/json
Accept: application/json
Cache-Control: no-cache
Fanza-Device: BROWSER
```

- The endpoint "rejects a request that does not look like it came from the site's own player" — these are hard-required, not optional (`open-mdcng/scrape/dmm.go` `post` comment). `Content-Type` specifically was the fix of a real past bug (`open-mdcng/docs/source-notes.md`).
- **`Origin` is never sent** (deliberate; whether the endpoint would accept one is unknown). **User-Agent** is not set by the source; it comes from the fetch layer's browser-disguise (Chrome profile, e.g. `chrome_133`) with pinned header ordering (`open-mdcng/fetch/disguise.go`).
- No cookie, no age-gate, no captcha was ever needed for the GraphQL API in either repo — unlike the HTML site's age gate or javbus's `302 → /doc/driver-verify`.
- Error envelope: an `"errors"` key inside an HTTP 200 body is a failure, messages surfaced verbatim; a region refusal (`not-available-in-your-region` / `not available in your region`) is classified as **blocked, not missing** — "a blocked exit must not become evidence of absence" (both repos).
- Pacing (fetch layer, both repos): per-registrable-domain token bucket, default **1 req/s burst 1**; cooldown **300 s** on 403/429/503 (404 never penalised); **2 retries** (3 attempts), no backoff sleep; **30 s** per-request timeout; FlareSolverr fallback exists but only for GET (irrelevant for these POSTs). Per-source overrides exist; mdc-lite exposes `scrape.with.sources.dmm.proxy` for the JP exit.

## 3. Per-asset URL map **[code + capture]**

All image URLs arrive absolute in the `ppvContent` JSON — no HTML parsing, no URL construction needed. Host spellings seen across real captures/recorded runs (the same `<cid>` directory shape appears on two CDN hosts; which one serves a given response is the API's choice, not ours):

| Asset | Field role | URL shape | Primary evidence |
|---|---|---|---|
| GraphQL API | — | `https://api.video.dmm.co.jp/graphql` | capture manifest `fixtures.json`; code |
| Package image **large** | fanart per PLAN.md's bet / poster per both repos' code (see §4) | `https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/<cid>/<cid>pl.jpg` (live capture) · `https://pics.dmm.co.jp/digital/video/<cid>/<cid>pl.jpg` (constructed fixture `open-mdcng/scrape/testdata/dmm_content.json`) | `dmm-SSIS-001-content.json` `packageImage.largeUrl` |
| Package image **medium** | portrait candidate (avbase uses `ps.jpg` as its poster) | same dir, `<cid>ps.jpg` | `packageImage.mediumUrl`; avbase live run below |
| Sample / preview set | extrafanart (and fanart in both repos' code) | same dir, `<cid>jp-1.jpg … <cid>jp-10.jpg` (SSIS-001 real capture has exactly 10; count varies per title) | `sampleImages[].largeImageUrl` |
| Actress portraits | actor thumbs | `https://awsimgsrc.dmm.co.jp/pics_dig/mono/actjpgs/<slug>.jpg` (capture) · `https://pics.dmm.co.jp/mono/actjpgs/<slug>.jpg` (fixture + live run) | `actresses[].imageUrl` |
| Trailer | trailer (open-mdcng maps it; mdc-lite doesn't query it) | `https://cc3001.dmm.co.jp/litevideo/freepv/s/ssi/<cid>/<cid>mhb.mp4` (fixture) · `cc3001.dmm.co.jp/pv/…/<cid>_mhb_w.mp4` (recorded live run) | `sample2DMovie.highestMovieUrl`, VR fallback `sampleVRMovie` |
| Metadata (ja) | everything below | the `ppvContent` document itself | capture |

Note the path split `s/ssi/` mirrors the id's first chars — the fixture's shape, worth treating as opaque rather than constructing it ourselves; take URLs from the JSON.

**Image fetch requirements** **[code]**: dmm's image CDNs want **no Referer, no cookie** (unlike javbus images, which 403 without a Referer) — mdc-lite's `FetchImage` sends nothing beyond the transport defaults, and its comment records the CDN "wants no Referer and no cookie — but does need the same Japanese exit the API does". Both repos put zero headers on dmm image downloads.

## 4. Metadata fields (all ja) **[code + capture]**

From the real SSIS-001 capture (values verbatim in `dmm-SSIS-001-content.json`):

| Field | GraphQL source | Notes |
|---|---|---|
| title (ja) | `title` | full Japanese retail title |
| description (ja) | `description` | long-form promo text → `plot`/`outline`; PLAN.md's AI-translation input |
| releasedate | `deliveryStartDate` → fallback `makerReleasedAt` | RFC3339 UTC in capture (`2021-02-18T01:00:00Z`); both repos normalise to `YYYY-MM-DD`, delivery wins |
| runtime | `duration` | **seconds** in the API (8826) → minutes in NFO (147) |
| rating | `reviewSummary.average` | **5-point scale** → ×2 for the 10-point `<rating>` (ADR-0016 in both repos); 0 left unset. Capture: 4.22 → 8.44 |
| tags | `genres[].name` | ja genre names (capture: 8 tags) |
| actors | `actresses[].name` + `imageUrl` | **female-only list, no gender field** — gender split needs javdb (PLAN.md already assigns actors to javdb) |
| director | `directors[].name` | capture: `朝原` |
| studio | `maker.name` | capture: エスワン ナンバーワンスタイル |
| publisher | `label.name` | capture: S1 NO.1 STYLE |
| series | `series.name` | nullable (capture: `null`) |
| floor | `floor` | `"AV"` in capture; VR has its own floor/movie fields |

NFO convention (mdc-lite `internal/pipeline/nfo.go`): `<title>` carries the 番号 prefix, `<originaltitle>` is the raw ja title, `<plot>` the ja description; translated copies are separate derived fields — matching PLAN.md's "use the original ja, AI-translate".

## 5. Poster geometry — PLAN.md's bet vs the code **[code, geometry itself unverified]**

PLAN.md: *"图片爬来应该是一个横图，只能作为fanart，需要右侧裁剪得到poster"* (the scraped image is a wide banner usable only as fanart; crop its right portion for the poster). Verification against the code:

- **The right-crop exists and its geometry is exact** — `mdc-lite/internal/pipeline/images.go` `cropPoster` (ADR-0015): keep **full height**, cut a **2:3 portrait slice from the right edge**, `w = (h*2+1)/3`, re-encode JPEG q90, Go stdlib only. The comment states the domain rationale: "the right edge is where a landscape sleeve's front cover sits (a 16:9 fanart keeps about 37.5% of its width)". It runs **only when no source supplied a poster**; provenance is recorded as `<source> (自动裁切)` and a crop failure leaves the task `partial`, not `failed`.
- **But for DMM hits neither repo actually crops** — both map `poster = packageImage.largeUrl` (the `pl.jpg`) **directly**, and fanart = first `sampleImages` entry (open-mdcng `dmmMetadata`; mdc-lite `fieldsOf`: `f.Poster = PackageImage.LargeURL`, `fanart = samples[0]`, fallback fanart = package image). The crop is their fallback for poster-less sources (javbus/fc2), where the fanart is javbus's own wide composite sleeve.
- **The two repos' own writings disagree about `pl.jpg`'s geometry**: open-mdcng's comments assert the package image "is portrait" (twice, `scrape/dmm.go`); mdc-lite's GLOSSARY states the opposite rule for the domain — "日版番号媒体抓来的原版封面是横版完整封套（含正反面折痕）；媒体库要的竖版 poster.jpg 是从它右侧裁出来的产物，不是另抓一张" (the JP sleeve is a wide front+back composite; the portrait poster is a crop of it, never a separate fetch). mdc-lite's own avbase research table (`docs/research/field-model.md`) records from real page data: **poster = `…ps.jpg`, 横版 = `…pl.jpg`** — i.e. the small `ps` is the portrait and the large `pl` is the landscape, which supports PLAN.md's bet and implies both repos' `poster = pl.jpg` mapping puts a wide image in the poster slot. open-mdcng's one live DMM-adjacent run (avbase, 2026-09-25) likewise used `ssis00001ps.jpg` as the poster.
- **No pixel measurement exists anywhere in either repo** — open-mdcng's dmm fixtures are explicitly *constructed* (its fixtures manifest says "a real capture would be better evidence… a known gap"), and every dmm image CDN is TLS-blocked from non-JP egress. So `pl.jpg` vs `ps.jpg` geometry is **[unverified]** — see §7.

**Recommended reading for the spec**: keep both URLs; treat poster as PLAN.md does — wide sleeve (`pl.jpg`) → fanart + right 2:3 crop → poster — with `ps.jpg` as the cheap portrait alternative; settle the geometry empirically from the JP egress in prod (one `curl` + `file` on a `pl.jpg`), since the crop code (ADR-0015 shape) is already proven and works whether the sleeve is 3:2 or 16:9.

## 6. Geo constraints — Japan egress is a hard prerequisite for metadata AND images

- `dmm serves Japan only`: the source declares `RequiresProxy() = true`; a region refusal is reported as blocked naming the proxy setting, never as "no such title" **[code]**.
- **[blocked]** Measured on this dev box today (2026-11-01): all six hosts — `www.dmm.co.jp`, `video.dmm.co.jp`, `api.video.dmm.co.jp`, `pics.dmm.co.jp`, `awsimgsrc.dmm.co.jp`, `cc3001.dmm.co.jp` — resolve, TCP-connect, then die with `TLS connect error: unexpected EOF` mid-handshake (`curl` exit 35). This is an IP/geo-level TLS cut, not DNS, not a 403 page.
- mdc-lite's prior measurement (`docs/research/source-landscape.md`) found the identical failure twice and made the consequence explicit: the Japan exit is needed **not just for fields but for images** — dmm's image CDNs also host javbus's sample images, so without JP egress even javbus-driven extrafanart dies there.
- When the API *is* reachable but the exit is wrong, it answers GraphQL `errors` with `not-available-in-your-region` (both spellings seen) **[code]**.
- Live history **[capture]**: on 2026-09-25 one capture machine's exit reached the GraphQL endpoint — `SSIS-001` answered 200 with all 14 fields (`open-mdcng/docs/source-notes.md` live-run table); mdc-lite's fixtures were captured 2026-10-29 the same way. So the protocol above is live-proven, just not from *this* box.
- Prod topology (PLAN.md): the router's mihomo auto-routes dmm to a Japan egress; v1 ignores proxy management entirely. The dev box has no such egress — this is the known fog item already on the map (#1 "Not yet specified": DMM reachability from a dev environment).

## 7. What could NOT be verified (gaps for the downstream scraping ticket)

1. **Any live call from this box.** All six dmm hosts TLS-blocked (§6). Everything "live" above is the reference repos' captures, cited as such. First prod/dev-JP-egress action: replay one search+content pair and confirm the schema hasn't drifted (it did once already — `makerContentId`, answered 422).
2. **`pl.jpg` / `ps.jpg` pixel geometry** (§5) — the one contradiction between the reference repos' code comments and the domain docs; no measurement exists. Decides whether poster comes from a right-crop of `pl.jpg` (PLAN.md's bet, mdc-lite ADR-0015's machinery) or directly from `ps.jpg`/`packageImage` (both repos' current mapping). One `curl …pl.jpg | file -` from the JP egress settles it.
3. **Whether the image CDNs ever demand a Referer/UA** — repos send none for dmm images and nothing in the record shows a refusal, but there is equally no recorded *successful* dmm image download outside the acceptance fixture server. (javbus images DO 403 without Referer — different site, different rule.)
4. **`floor: AV` coverage** — whether every 番号 family oh-my-mdc will see lives on the AV floor (VR titles have their own fields: `sampleVRMovie`; PLAN.md's VR cd1/cd2 handling may meet a different floor/id shape). Unmeasured.
5. **Real rate limits / bot thresholds on the GraphQL API** — repos pace at 1 req/s and nothing in the record shows a 429; but no adversarial probing was done (nor should be from this box).
6. **Trailer URL stability** — two shapes recorded (`litevideo/freepv/…mhb.mp4` in the constructed fixture, `pv/…_mhb_w.mp4` in the avbase-era live run); mdc-lite drops the trailer entirely.

## 8. Fetch strategy summary (per 番号)

1. Search leg(s): `POST api.video.dmm.co.jp/graphql`, `legacySearchPPV(limit:30, floor:AV, sort:SALES_RANK_SCORE, queryWord:<reduced>)`, five headers, UA from browser-disguise, ~1 req/s; if no exact-identity hit, retry once with the 5-wide padded spelling.
2. Content leg: one `ppvContent` + `reviewSummary` POST by id.
3. Images: download absolute URLs straight from the JSON (`pl.jpg`, `ps.jpg`, `jp-N.jpg`, `actjpgs/*.jpg`) with no special headers, through the same JP egress; sniff format from magic bytes (repos don't trust Content-Type).
4. Poster: right 2:3 crop of the wide sleeve (mdc-lite `cropPoster`) pending the §7.2 geometry check; `ps.jpg` as the portrait fallback.
5. NFO: `<title>` = 番号 + title, `<originaltitle>` = ja title, `<plot>` = ja description, runtime = duration/60, rating = average×2; actors/gender from javdb per PLAN.md.
6. Treat GraphQL `errors` as failure, region-refusal as blocked-not-missing, 422 as schema drift, `ppvContent: null` as a normal miss.

## Source index

- `/home/zhao/clone/open-mdcng/scrape/dmm.go` — the native DMM source (protocol, headers, id logic, field mapping, region handling).
- `/home/zhao/clone/open-mdcng/scrape/testdata/dmm_content.json` + `fixtures.yaml` — constructed fixture (URL shapes; marked not-a-capture).
- `/home/zhao/clone/open-mdcng/docs/source-notes.md` — dmm section + live-run table (2026-09-25), header/422/region notes.
- `/home/zhao/clone/open-mdcng/.scratch/open-mdcng/issues/42-nextjs-json-data-unreadable.md` — recorded live avbase run with dmm CDN poster/trailer URLs.
- `/home/zhao/clone/mdc-lite/internal/source/dmm/dmm.go` — second implementation of the same contract (mirrors, per-source proxy knob, image fetch without Referer).
- `/home/zhao/clone/mdc-lite/internal/source/dmm/testdata/captures/` — **real live captures** (2026-10-29, status 200): both search legs + full content JSON for SSIS-001.
- `/home/zhao/clone/mdc-lite/internal/pipeline/images.go` + `docs/adr/0015-poster-crop-is-v1.md` + `GLOSSARY.md` — the poster right-crop (2:3) and the sleeve-is-landscape domain claim.
- `/home/zhao/clone/mdc-lite/docs/research/dmm-contract.md` — mdc-lite's own line-cited audit of the open-mdcng DMM contract (source of several fetch-layer details above).
- `/home/zhao/clone/mdc-lite/docs/research/source-landscape.md` — the non-JP-egress measurement (TLS EOF on all dmm hosts, twice).
- `/home/zhao/clone/mdc-lite/docs/research/field-model.md` — the `ps.jpg`=portrait / `pl.jpg`=landscape observation from real avbase data.
- This box, 2026-11-01: `curl` probes of six dmm hosts → TLS handshake EOF (exit 35) on all.
