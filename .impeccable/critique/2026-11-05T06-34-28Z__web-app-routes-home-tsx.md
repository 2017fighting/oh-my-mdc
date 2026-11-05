---
target: the panel (web/app/routes/home.tsx)
total_score: 34
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 2
p2_count: 3
target_identity: "file:/root/clone/oh-my-mdc/web/app/routes/home.tsx"
target_fingerprint: "sha256:4f48de65b79d422e03bd51019f60f5e3557011ba1fdb6fc195593683796aa5ba"
target_path: /root/clone/oh-my-mdc/web/app/routes/home.tsx
timestamp: 2026-11-05T06-34-28Z
slug: web-app-routes-home-tsx
closed: true
---
Method: dual-agent (A: assessment-a · B: assessment-b)

# Critique — oh-my-mdc 面板（`web/app/routes/home.tsx` + `web/app/components/panel.tsx`）

Mode: **Operate**. Target slug `web-app-routes-home-tsx`. Reviewed against PRODUCT.md and DESIGN.md's own named rules.

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 4 | Freshness self-reported (`上次读取 14:42:05（2 分钟前）`), loop pulse, receipts, one live region. |
| 2 | Match System / Real World | 4 | 番号/版本/重新加种/对账「部·条」all follow GLOSSARY; relative time by default. |
| 3 | User Control and Freedom | 3 | No acknowledge/snooze; no cancel for a running batch; Esc + URL-persisted filters are good. |
| 4 | Consistency and Standards | 3 | `重试` covers three scopes; the 44px/`md:` string is hand-copied in 18 places and missed in empty states; heartbeat renders twice between 768–1023px. |
| 5 | Error Prevention | 3 | 强制重刮 confirms with consequences; batch prints blast radius but not *which* works. |
| 6 | Recognition Rather Than Recall | 3 | `需人工` (task-level) vs the card's number (version/row-level) asks the user to hold two counting schemes. |
| 7 | Flexibility and Efficiency | 4 | `/`, `r`, `j/k`, `?`, URL filters, batch retry. No refresh key. |
| 8 | Aesthetic and Minimalist Design | 4 | Ink-only, no shadows, empty blocks removed; one static data-model paragraph survives (`panel.tsx:475`). |
| 9 | Error Recovery | 4 | Two gateway deaths split apart, errors carry a subject, `Unavailable` is a real state, not a dismissible toast. |
| 10 | Help and Documentation | 2 | Help is a desktop-only `?` sheet plus hover `title`s; no glossary surface; batch scope undocumented. |
| **Total** | | **34/40** | **Good (28–35)** |

Nothing reaches the Excellent band because 3, 6 and 10 fail on the same axis: the screen explains itself fluently to someone who already knows the model.

## Design Specificity Verdict

**Authored for this product — and provably so, structurally rather than cosmetically.**

- **The page title is a count, not a noun** (`home.tsx:175-181`): `8 件事需要人工` / `没有要动手的事`. No generic dashboard opens that way.
- **The attention section is ordered by a question only this pipeline can ask**: 能在这里修 → 停滞 → 要去别处修 → 驻留副作用 (`panel.tsx:760-830`).
- **A design rule is enforced in data**: `remedy.ts` returns `retryable: false` for host failures and `panel.tsx:1019-1022` actually removes the button — including inside the batch path, where such rules normally leak.
- **Named rules are implemented, not documented**: The Nothing-To-Do Rule (`panel.tsx:721`), The One-Voice Rule (single live region at `home.tsx:284`, card line deliberately silent), The Honest-Aftermath Rule (`重新加种中` + a sequence gate at `panel.tsx:625-634`, so a stale re-seed cannot explain a new stall).
- **Copy no other product could ship**: `重试不会变好：这是宿主机上的问题（目录权限、只读挂载或空间）`, `重新加种，等下一轮对账确认`, `重试 3 部 · 5 条版本`, `清单轮询 每 15 分钟 · 2 分钟前`.

Generic residue is narrow and nameable: the 任务 table, the 循环健康 card, and the load/empty chrome would survive transplant with different nouns.

**Deterministic scan**: `impeccable detect --json` over `web/app/routes/home.tsx`, `components/panel.tsx`, `components/form.tsx`, `components/ui`, `app.css` → **8 files, exit 0, `[]`, zero findings**. Three corroborating runs (directory form; `--no-config --no-inline-ignores`; a liveness probe on a scratch `.tsx` that correctly produced 2 findings) confirm this is a real zero and not suppression or a silent crash. **It is nonetheless an undercount**: `detect` in text mode states that only the regex layer ran on `.tsx`, so `tiny-text`, `low-contrast`, `text-overflow`, `clipped-overflow-container`, `skipped-heading`, `line-length`, `nested-cards`, `monotonous-spacing`, `text-occlusion` and the `design-system-*` family never saw the panel. The regex/palette/copy layer is genuinely clean (status tokens pre-tuned, `Geist Variable` deliberate, no nested cards, `—` overwhelmingly in comments). Treat the scan as "no mechanical pattern violations in the text layer," not a clean bill of health.

**Visual overlays**: none — no browser automation tool is exposed in this session, so no server was started, no injection attempted, and no overlay is claimed. Worth knowing for the next attempt: the panel cannot be exercised from `react-router dev` at all — `web/vite.config.ts` has no `/api` proxy, so all five dashboard calls 404 and `home.tsx:437-443` renders the `读不到面板数据` card. A real URL scan needs `make dev` (Go binary serving embedded `internal/web/dist` + Postgres).

## Overall Impression

This is one of the most disciplined Operate surfaces I have reviewed: the state model is real, the honest-aftermath behaviour is better than most commercial products manage, and the accountability of "who said this number" is designed rather than accidental. The problem is not craft, it is **coverage**: the panel is excellent at *acting on a failure* and thin at the two other jobs it claims — *watching* (nothing refreshes itself, no refresh key) and *letting something stop being urgent* (no acknowledge, no aging, oldest-last ordering). The single biggest opportunity: make the watch job as honest and self-updating as the retry job.

## What's Working

1. **One narrator for the number.** `home.tsx:284` owns the live region and stays mounted with empty content; the card's visible line refuses to become a second voice and degrades to `已经清完了。` when only receipts remain. The One-Voice Rule is implemented down to the "mounted live region" trap that most teams get wrong.
2. **The remedy is data, and the missing button is the design.** Host failures have no 重试 anywhere, including inside the aggregate batch, because `remedyForFailure` decides and the UI merely renders. Judgment lives in one file with tests (`remedy.test.ts`).
3. **Freshness is first-class.** `loadedAt` is separate state from the data, relative time self-ticks every 60s, and `loopState` refuses to call an unread system `未启动` (`还没读到过`). A duty desk that cannot lie about staleness is the whole premise, and this one mostly doesn't.

## Priority Issues

### [P1] Watching is the least-supported job on a monitoring surface

**What**: Nothing on the panel refreshes itself. `fetchDashboard` runs on mount and on query change only (`home.tsx:110-127`), and no shortcut covers refresh (only `? / r j k Esc`). A stall being watched renders a frozen screen whose only truth is `上次读取`.
**Why it matters**: half the declared product job is "watch the pipeline"; the operator who comes back to check whether the re-add worked must click 刷新 or reload, and doing so loses nothing except their trust that the screen is live.
**Fix**: bind a refresh key (e.g. `Shift+R`) to the same `onRefresh`; when `document.visibilityState === "visible"` on the panel, poll a read-only subset (overview + attention) on the poll interval; keep `上次读取` honest by updating `loadedAt` on every successful read. Do not silently poll the task table — the table must stay still while being read.
**Suggested command**: `/impeccable harden`

### [P1] The headline number can be permanently stuck

**What**: `attentionRowCount` counts rows nobody can act on (`attention.ts:36-43`), rows are ordered `updated_at DESC` (`attention.ts:10-14`), and there is no acknowledge, snooze, or "I know" anywhere. One three-week-old Telegram 403 keeps `4 件事需要人工` on screen forever, and the *oldest* unresolved failure sorts last.
**Why it matters**: the one number that means "act now" decays into wallpaper; the failure model that the design was built around — "nothing happens, then one thing needs a human" — inverts into "always four things, ignore the headline".
**Fix**: carry age into the visible 构成行 (`失败 3 部 · 驻留副作用 1 条（最早 12 天前）`); group 驻留副作用 by cause so three retries of one bad token read as one thing; sort the unstuck-by-nothing group oldest-first, not newest-first.
**Suggested command**: `/impeccable clarify`

### [P2] Batch retry prints blast radius but not a roster

**What**: `重试 3 部 · 5 条版本` (`panel.tsx:747-753`) fires three sequential requests (`runBatch`, `panel.tsx:666-706`) with no list of which 番号, no `2/3` progress, no cancel, and the label doesn't name a single movie.
**Why it matters**: the cheapest bulk action is the least inspectable, on the one surface that elsewhere goes to great lengths to say exactly what it will do (The Consequence-First Rule). A stalled re-add is not free.
**Fix**: put the 番号 list in the button's `aria-description` and `title`; swap the label to `正在重试 2/3` while `batchBusy`; allow the batch to be halted between steps (each step is already idempotent-safe).
**Suggested command**: `/impeccable harden`

### [P2] Two things called 需人工, and the caveat is hover-only

**What**: the title says `8 件事需要人工` (row-level: failed versions, stalled downloads, stuck side effects); the filter chip says `需人工` and counts tasks whose *every* version failed. The difference lives in a `title` on the chip (`panel.tsx:1700`) — which DESIGN.md itself rules out on touch.
**Why it matters**: the first thing a returning operator does is click `需人工`, get `0`, and conclude the card lied. It also produces the panel's only decision point with more than four options: five status chips (`全部 / 下载中 / 加工中 / 已入库 / 需人工`).
**Fix**: rename the chip `需人工（任务级）` and give the explainer a permanent home under the filter row; consider merging `已入库` out of the chip row, since it is the one chip that can never lead to an action (it already lives at the right end of the stats row).
**Suggested command**: `/impeccable clarify`

### [P2] The accessibility layer states more than it delivers

**What**: `aria-sort="descending"` is asserted on 更新 with no sort control (`panel.tsx:372`); `ShortcutsHelp` is `role="dialog" aria-modal="true"` with no Tab containment and no background scroll lock, unlike the drawer (`panel.tsx:2340-2385`); the mobile task row is one `<button>` whose `aria-label` deadens every nested `VersionChip` label (`panel.tsx:325`), so mobile loses per-version state; modal teardown keys off a global `document.querySelector('aside[role="dialog"]')`, so closing the drawer under an open help sheet can strip the shell's `inert`; `j/k` in the drawer captures `previousFocus` *inside* the drawer, so Esc falls back to `#main` instead of the row.
**Why it matters**: PRODUCT.md only promises keyboard reachable, semantic, contrast-usable — this surface mostly keeps that promise, but `aria-sort` tells a screen reader about an affordance that does not exist, and a mobile operator silently gets less state than the desktop table shows.
**Fix**: drop `aria-sort` or add the real sort control; give the help sheet the drawer's modal treatment (containment + overflow lock); on mobile, expose version state as text in the row rather than only inside dead `aria-label`s; make teardown reference-counted instead of query-based.
**Suggested command**: `/impeccable audit`

## Persona Red Flags

**Alex (power user)** — `r` with no focused group does not retry; it *focuses* the first retry button (`panel.tsx:176-183`), while the help sheet says `重试：焦点所在行` (`panel.tsx:2331`) — the one surprising behaviour is undocumented. No refresh key. Attention rows can't be filtered by remedy kind, so "show me the ones I can actually fix" means scrolling.

**Sam (accessibility-dependent)** — `aria-sort` announces a sort that doesn't exist; `text-muted-foreground` at `oklch(0.5)` was tuned against a 30% canvas, but table heads paint `bg-muted/40` over white (`panel.tsx:367`), a lighter ground than the one that justified 4.5:1; the help modal neither contains Tab nor locks scroll.

**Casey (distracted mobile)** — mobile task rows swallow every version chip's label into one button label; `清除筛选` in both empty states is `size="sm"` = 28px (`panel.tsx:1765`, `1791`), against the 44px rule the surface obeys via a hand-copied `h-11 px-3 dense md:h-7 md:px-2.5 md:text-xs` string in 18 places; the heartbeat renders twice between 768 and 1023px (`home.tsx:194` `md:flex` vs `panel.tsx:259` `lg:hidden`, where DESIGN.md says the sidebar is hidden below `lg`); the freshness cluster (`panel.tsx:268-289`) has no `flex-wrap`/`min-w-0`/`truncate`, and falls through to a wider absolute timestamp after 7 days.

**The author/operator** — the oldest unresolved failure sorts last, exactly backwards for triage; he cannot tell the panel "I know, stop shouting"; `正在跑 43 秒` is printed at second resolution while `useNow` ticks every 60s, a precision claim the clock cannot hold.

## Minor Observations

- `home.tsx:170-181`: while loading, the title degrades to `面板` — the one word DESIGN.md says is no longer a title.
- `panel.tsx:475`: `副作用失败退避重试，耗尽后移到「需要人工」等人工重发。` breaks the Don't on static data-model prose in body position, on a card that already carries a `title`.
- `panel.tsx:1100`, `1572`: adjacent row actions at `gap-1.5` (6px), under the documented ≥8px.
- `panel.tsx:1080`: the host-failure 补法 renders `dense text-foreground` where DESIGN.md specifies 12px 次级墨 — defensible ("it is the instruction"), but an undocumented deviation.
- `panel.tsx:383-385`: clickable `<tr onClick>` with `cursor-pointer`, no keyboard equivalent (mitigated by the 番号-cell button, so 2.1.1 survives through a nested control).
- `panel.tsx:1157-1160`: `aria-expanded` without `aria-controls`; the revealed region has no `id`.
- `panel.tsx:2123-2131`: `role="group" aria-live="polite"` on a live region mounted with its content — decorative, since focus is moved instead.
- Global first-match selectors drive behaviour (`input[type="search"]`, `[data-retry]:not([disabled])` at 6 sites): fine today, silently retargeted by any second match.
- `<main>` on narrow screens contains the only nav landmark (`home.tsx:254-278`), so `跳到主内容` lands before the nav rather than past it.
- Hygiene: mis-indented block at `panel.tsx:216-218`; a duplicated `useNow` docblock glued above `ShortcutsHelp` (`panel.tsx:2322-2327`) while the real `useNow` (`:2391`) is undocumented.

## Questions to Consider

1. If the only number on the page can only be decreased over SSH, is it still a fact — or is it furniture that ages in place?
2. Why is the newest failure the most prominent one, when the failure model says the interesting signal is how long something has been stuck?
3. The panel is honest that it cannot promise the next second (`重新加种，等下一轮对账确认`), yet it never refreshes itself. What would 半自动 look like if the watch job got the same honesty budget as the retry job?
4. Could `能在这里修 / 停滞 / 要去别处修 / 驻留副作用` collapse into two questions — "press something" vs "go somewhere" — with the second group becoming one line: `3 件要你去别处（最早 12 天前）`?
5. Should batch retry be allowed to exist without naming its three movies, when a single row's retry is where the design put all its honesty?
6. If mobile adaptation is explicitly not a requirement, why does the panel carry 18 hand-copied 44px/`md:` rules that have already drifted in two places — keep them, or delete them and the extra breakpoint with them?
