---
target: the panel (web/app/routes/home.tsx)
total_score: 26
max_score: 40
na_heuristics: 
p0_count: 1
p1_count: 3
target_identity: "file:/root/clone/oh-my-mdc/web/app/routes/home.tsx"
target_fingerprint: "sha256:57b6819576d60e53191c3cd0e92d85edd195a4c516cd9d066816841d30179e09"
target_path: /root/clone/oh-my-mdc/web/app/routes/home.tsx
timestamp: 2026-11-04T09-01-12Z
slug: web-app-routes-home-tsx
closed: true
---
# critique · 面板（值班台）

Target: `web/app/routes/home.tsx` + `web/app/components/panel.tsx`（Operate 面）

Method: dual-agent (A: assessA · B: assessB)

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | 新鲜度自报 + 每分钟走的相对时间是全站最好的东西（`panel.tsx:145-150`、`:1538`）；但全页最该被 glance 的那个数只以 12px 徽章活在卡角（`panel.tsx:434-446`），统计行里连它的位置都没有（`panel.tsx:109-113`） |
| 2 | Match System / Real World | 3 | 词汇精确（番号/加工/入库/重发/副作用驻留），但按钮写「重试全部 2 条版本」（`panel.tsx:626`）而后端重试整任务的全部版本（`internal/retry/retry.go:143-157`） |
| 3 | User Control and Freedom | 3 | Esc、点遮罩关闭、行内确认可取消、可关闭提示、筛选住进 URL；但重试与重发都不可撤销，也没有「最近处理过」的痕迹可回看 |
| 4 | Consistency and Standards | 2 | 同一屏三个「需人工」：卡角徽章＝失败版本+停滞+驻留（`panel.tsx:407`）、筛选「需人工」＝全部版本都失败的**任务**（`panel.tsx:969` + `00005_task_domain.sql:131-137`）、后端 `needs_attention`＝失败任务数（`internal/task/store.go:417-424`，前端已无人读） |
| 5 | Error Prevention | 3 | 永久错/宿主机错不给重试（`remedy.ts:55-64`）、连点不二次下发（`panel.tsx:728-731`）、强制重刮只在有已入库版本时出现且先写后果；缺口：组内只要一条有救就给按钮（`panel.tsx:572`），宿主机那条会跟着被再放一遍 |
| 6 | Recognition Rather Than Recall | 2 | 回执按 `movie_id` 记、只渲染在**成功后就会消失的那一行**（`panel.tsx:603-611`），多版本回执给 8 位哈希而屏幕任何地方都没有哈希 |
| 7 | Flexibility and Efficiency | 2 | 无批量重试、无页大小、无排序、无键盘上下移动、无复制 infohash（只活在 `title` 里） |
| 8 | Aesthetic and Minimalist | 3 | 发丝线、单一墨色强调、红 ring 只在有事时出现（`panel.tsx:419`）；但「停滞」那条 hint 每行逐字重复（`remedy.ts:72`） |
| 9 | Error Recovery | 3 | 病因文案是全产品最强的一环，且「去 Telegram 设置」是真链接到那一节（`panel.tsx:679-681`）；缺口：任务重试失败提示落在卡片顶部（`panel.tsx:453-459`），不在按下去的那一行 |
| 10 | Help and Documentation | 2 | 数据模型的唯一解释活在 `title=` 里（`panel.tsx:176`、`:329`、`:860`、`:1120`），手机与键盘都拿不到 |
| **Total** | | **26/40** | **Acceptable（65%）** |

上界 40：Operate 面十条全部适用，无 n/a。

## Design Specificity Verdict

**行内解剖是自己写的，外壳是通用件。**

**LLM 评估**：抄不走的都在行里——动作由病因推导（`remedy.ts:37-65` 对 EACCES 直接拒绝给按钮并写出「重试不会变好：…在宿主上处理 /media 之后再回来」）、版本级只说「失败」不说「需人工」（`panel.tsx:1120-1128`）、按钮上印爆炸半径（`panel.tsx:626`）、不可逆动作用一行内联确认说清拉回几条版本、旧快照留在哪（`panel.tsx:1373-1376`）、空态教下一步（`panel.tsx:1043-1047`）。抄得走的是行以上的一切：统计行把「已入库 142」用 `font-medium` 加在最显眼处而「需人工」根本没有位置（`panel.tsx:109-113`）、五行分段筛选（`panel.tsx:964-971`）、`<h1>面板</h1>`（`home.tsx:249`）——全页最大的 24px 字给了一个零信息量的词，而真正要人动手的那行正文只有 13px。DESIGN.md 比页面本身更有个性：文档写了「注意力排序固定：需要人工 → 任务 → 副作用 → 循环健康」，页面照做了顺序，却没照做**权重**。约一半的骨架可以整块换成任何 job-queue 后台；「需要人工」区里没有一行可以。

**确定性扫描**：`impeccable detect --json web/app`（含 `home.tsx`、`panel.tsx` 单文件、`--no-config --no-design-system`）四轮全部 exit 0、`[]`。规则目录活着（62 条）。活性对照：把反模式写进 `/tmp` 的 `.tsx` 报 3 条（`side-tab` / `gradient-text` / `ai-color-palette`），但同一份内容的单条探针证明静态 TSX 引擎只是**类名 token 匹配**——`text-[9px]`、`text-blue-500/60`、紫色外发光、卡中卡、`rounded-2xl border-l-8` 全部漏；同样的反模式写成 HTML+CSS 则报 13 条，且静态与浏览器引擎结果**完全一致**（同一份 `bad.html`：静态 8 条 6 规则，浏览器 6 条同 6 规则）。所以「CLI 干净」对 `.tsx` 只意味着没写那几个被写死的类名，真正的判据在浏览器那一轮。

**浏览器证据**：本轮注入成功、且在页面里真的跑了。用 Playwright 版 Chrome（156）+ 裸 CDP，把 `web/build/client`（16:04 构建，新于两个源文件）配上覆盖全部端点的 fixture 起在 8791，先在页面里断言 `#app-shell`、`h1=面板`、`section[aria-label="失败的版本"]`、5 行表格、7 个版本 chip 都在，再扫：1440×950、390×844、以及**抽屉打开**的两种宽度，四次全部 `[]`、exit 0。活性对照注入同一页面后立刻报 6 条（`low-contrast` 实测 3.5:1、`tiny-text` 9px、`gradient-text`、`side-tab`、`dark-glow`、`ai-color-palette`），浮层节点 9 个、标签如 `LOW CONTRAST TEXT, TINY BODY TEXT`，移除对照后回到 `[]`——所以这次的零是内容干净，不是注入失效。注入走的是浏览器（`live-server stop --keep-inject`），但页面在无头 Chrome 里，**你的浏览器里不会出现浮层**，本轮没有 `[Human]` 可见物。逐像素补测：1440 与 390 各 165 / 158 个文本样本，低于 AA 的 **0** 个，最低 **5.87:1**（状态色字落在同族状态底上），最小字号 12px —— 上一轮 `panel.tsx:149` 那条 3.7:1 的「（刚刚）」已经修好。

## Overall Impression

骨架越来越对，账越算越不对。上一轮报的两件事（重试治不好的病、面板不知道自己多旧）都真修了，而且修得比要求更好；这一轮剩下的核心问题不是审美，是**同一件事在屏幕上被算成三个数**：卡角那个红徽章、下面那个「需人工」筛选、以及后端那个已经没人读的 `needs_attention`。作者回来是为了「还有几件事要我动手」，而现在他可以清空筛选、红徽章不动，或者反过来——一部片中文版入库、破解版失败，卡里挂着一条失败，筛「需人工」却是 0 行。第二层问题是**成功的动作不留痕**：按下重试，行消失，回执也随行一起消失，屏幕上唯一的变化是一个他没在看的数。最值得改的一件事只有一件：把这个数收成一个口径并说清构成（失败 2 · 停滞 1 · 副作用 1），再给每次动完手留一条会自己退场、但退场前能被看见的回执。

## What's Working

1. **病因优先的动作模型（`remedy.ts` + `VersionGroup`/`StuckRow`）**：动作按「什么能治好这一行」选，不按子系统；宿主机问题宁可**不给按钮**并写清「重试不会变好」，还从 errno 里抠出出事目录。「按了也再失败」的假按钮是运维界面最贵的坏习惯，这里拒绝生产它。
2. **新鲜度一等公民（`panel.tsx:145-166` + `useNow` + `lib/time.ts`）**：客户端时钟的「上次读取 hh:mm:ss（N 分钟前）」，每分钟自己走一格，没数据就说「还没读到过」；过期的值班台与健康的看起来一模一样，这是最便宜的一处诚实。
3. **不可逆动作的后果前置（`panel.tsx:1326-1390`）**：强制重刮只在真有已入库版本时出现，确认条自己说出「把 1 条已入库版本拉回加工开头」、机制（重新取数并重写落盘）、证据去哪了（旧快照留在任务事件里），确认与取消同框，不开对话框。这套口吻应该被全页抄。

## Priority Issues

### [P0] 同一个「需人工」在一屏里有三个互不相容的算法

- **What**：卡角徽章 = 失败**版本** + 停滞 + 驻留副作用（`panel.tsx:407-444`）；「需人工」筛选 = `task_state.state='failed'` 的**任务**，而该视图只在「没有任何版本处于 processing/downloading/finished」时才判 failed（`panel.tsx:969`、`internal/db/migrations/00005_task_domain.sql:131-137`）；后端 `counts.needs_attention` 还等于失败任务数（`internal/task/store.go:417-424`），前端已无人读。三种真实后果：①徽章 4、筛选「筛选后 1 条」；②一部片中文版已入库 + 破解版失败 → 卡里挂着失败行、任务状态却是「已入库」，筛「需人工」返回 **0 行**，这条失败永远进不了筛选视图；③`Attention()` 每半默认取 50 条（`internal/task/attention.go:52-56`），60 条队列报 50，界面不声明截断。
- **Why it matters**：作者回来的唯一问题是「还有几件要我动手、我清完了没有」。现在他可以清完筛出来的每一条，红徽章一动不动；也可以清空筛选看到 0 行、以为干净。一个自相矛盾的计数比没有计数更坏，因为它是这页的全部目的。
- **Fix**：定一个口径——「有失败/停滞版本的**番号数** + 驻留副作用数」——徽章由 `failedGroups.length + stalledGroups.length + stuck.length` 推导，筛选改成同一口径（或把筛选改名为「任务级失败」，旁边并列行级数）。删掉 `overview.counts.needs_attention` 让第二个真相无法再长回来。徽章里给构成：`失败 2 · 停滞 1 · 副作用 1`；任一半命中 limit 时显示 `50+`。
- **Suggested command**: `/impeccable clarify`

### [P1] 重试成功的回执随那一行一起消失，成功是无声的

- **What**：`useTaskRetry` 写完 `notices[movieID]` 就 `await onRefresh()`（`panel.tsx:735-750`），而回执唯一的渲染点在 `VersionGroup` 里（`panel.tsx:603-611`）——正是重试成功后从树上消失的那个组件。于是被拒时有话、成功时无话。`panel.tsx:428` 的 `aria-live` 只在 `total` 跨零时改文案，3→2 不播报。
- **Why it matters**：这是作者每一次清队列都会走的最短回路。按下去 → 行没了 → 没有「已下发：回到加工开头」→ 他唯一能确认的线索是自己没在看的那个数。校准是反的：失败会说话，成功不会。
- **Fix**：把回执提到卡片级的一个列表（番号 + 结局 + 相对时间，留最近 5 条），渲染在分组之上、与行是否还在无关；提供「知道了」逐条消失，或在同一任务重新进入队列时自动清掉。数字变化与回执插入都进同一个 live region。
- **Suggested command**: `/impeccable harden`

### [P1] 网关会话过期时，面板停在一个永不结束的 spinner 上

- **What**：网关回一个 200 的登录 HTML 页 → `response.json()` 失败 → `body = null` → `response.ok` 仍为真 → `request` 把 `null` 当 T 返回（`lib/api.ts:272-291`）→ `apply(loaded)` 在 `.then` 的**兑现**回调里抛 TypeError（`home.tsx:85-95`），而 `.then(onFulfilled, onRejected)` 不接自己回调抛的错 → 未处理拒绝，`snapshot` 与 `loadError` 都仍是 null → 永久「正在读取设置…」（`home.tsx:259-263`）。401 稍好但也不对：「读不到设置：请求失败（HTTP 401）」既说错名词，也没说是**网关**要凭据。
- **Why it matters**：作者从网关进来，会话寿命比他清队列的时间短。此刻他看到的是一个转圈和零信息，重新登录的入口在整个应用里不存在。
- **Fix**：校验信封（`if (!body || typeof body !== "object") throw new HttpError(status, "bad_envelope", "网关返回的不是 JSON")`），把 `apply` 包进 try，401/403/HTML 单独识别成「网关会话过期，重新登录」。顺手把面板视图的文案从「读不到设置」改成「读不到面板数据」。
- **Suggested command**: `/impeccable harden`

### [P1] 组内「重试」的爆炸半径比按钮写的更大，而且会带上宿主机版本

- **What**：按钮文案数的是这一组里的行数（`panel.tsx:626`），后端 `Retry(movieID)` 却遍历该任务**全部**版本（`internal/retry/retry.go:143-157`），回执里因此会冒出屏幕上看不到的哈希。`retryable = group.items.some(...)`（`panel.tsx:572`）意味着一条 permission denied 的版本还在同组时，按钮照样出现，按下就把它一并再放一遍——与 `remedy.ts:56-64` 刚立的规矩「重试不会变好就别给按钮」直接冲突。
- **Why it matters**：这是不可逆（会往 qBittorrent 里加种）的动作，却既不数清也不排除已知无效的那部分；而隔壁强制重刮为了同样的风险专门做了确认条。校准又反了一次。
- **Fix**：文案说真话（「重试这条任务的 N 条版本」）；组内混有宿主机病因时，要么隐藏重试并指名「这条版本挡住了它」，要么让后端接受 infohash 列表，让按钮含义与行为一致。
- **Suggested command**: `/impeccable clarify`

### [P2] 「按番号收组」只在相邻时成立

- **What**：`groupByNumber` 只与上一条比（`panel.tsx:526-534`），而失败项是 `ORDER BY v.updated_at DESC` 出来的（`internal/task/attention.go:68-84`）。两部片交替失败时，同一番号的两个版本会排成两组、两颗一模一样的「重试」——`ff3c63b` 那句「队列按番号收组」在真实数据上会悄悄失效。
- **Why it matters**：它正是 P0 那个数的另一半——按番号收组本来就是为了让「有几件事」等于「有几行」。
- **Fix**：用 `Map` 按 `movie_id` 归并，或把查询改成 `ORDER BY movie_id, updated_at DESC`；加一个「两任务的版本交错」的用例。
- **Suggested command**: `/impeccable harden`

## Persona Red Flags

**Alex（力量型用户）**：清不了队列批次——N 行要 N 次点击，每次点击还等五个端点一起刷新（`home.tsx:391-399`）。表格行带 `onClick` 和 `cursor-pointer`（`panel.tsx:247-249`）但不可聚焦、无键处理，Tab→Enter 只在番号格那颗小按钮上有效。回执说 8 位哈希（`panel.tsx:788`）而行上写「中文 · JavDB」，屏幕任何地方都没有哈希（`title` 里才有），他无法把回执对回版本。没有任何地方能复制 infohash，他会去 qBittorrent 手敲。页大小、排序都不可改。

**Sam（键盘 / 读屏 / 低视力）**：重试成功后那一行被移除，焦点掉回 `<body>`，没有任何回位处理——而抽屉在同样的问题上做得一丝不苟（`panel.tsx:96-101`），说明它被想到过。`VersionChip` 把 `aria-label` 放在 `<span>` 上（`panel.tsx:1104-1118`），普通 span 不暴露 aria-label，读屏只读到可见文字，完整 infohash 永远拿不到。数字变化不播报（`panel.tsx:428` 只在跨零时改文案，`:444` 的数在 live region 之外）。决定「按不按」的那句 hint 是 12px 次级墨（`panel.tsx:604-607`），且停滞行逐字重复。

**23:00 用手机从网关进来的作者**：没有自动刷新也没有下拉刷新，动完手唯一拿到真相的办法是滚回页顶按「刷新」（`panel.tsx:153-163`）。`useNow` 是没有 `visibilitychange` 的裸 `setInterval`（`panel.tsx:1542`），手机息屏回来后仍可能写着「3 分钟前」。首屏是「正在读取设置…」——在他最不确定东西有没有在跑的时刻，屏幕用了错的名词。会话过期把他留在无声 spinner 里，应用里没有「重新登录」四个字。行级失败提示落在卡片顶部（`panel.tsx:453-459`），他在第 8 行按重试、屏幕不动、错误在屏幕上方看不见的地方。

## Minor Observations

- `panel.tsx:107-108` 的注释仍宣称「需人工只出现一次」，实际又作为筛选 chip 出现一次，且数不同。
- `Promise.all` 拉五个端点（`home.tsx:391-399`）：`/api/side-effects` 一个 503 会把整个面板打成空白，包括已知的任务表。
- `home.tsx:100-110` 的 `refresh` 吞掉自己的错误，于是「已下发」可能落在一次失败的重读上面，屏幕上仍是旧行。
- 已有 `已暂停` 徽章（`home.tsx:219`）却没有恢复入口，要去 `循环健康 → 去集成设置` 再找到第 8 步的那个开关。
- 抽屉遮罩的 `onClick` 在强制重刮确认条打开时、以及重试在飞时都能一键关掉抽屉（`panel.tsx:1330-1331`），确认态无声消失。
- 副作用队列为空时仍渲染一张带标题和「队列是空的。」的卡（`panel.tsx:856-870`），是整个页面最后一块约 80px 的空白结构。
- `text-[0.8125rem]` 手写约 25 次，而 DESIGN 里 `dense` 是排版 token；token 只活在文档里。
- 「停滞」的 hint 是常量字符串（`remedy.ts:72`），逐行重复同一句时它是纯噪声；只有「先去 qBittorrent 确认」这句在每组说一次就够。
- 表格无 `aria-sort`、无排序入口、也没有说明顺序，作者无法得知「更新」列是降序。
- 移动端 `RowAction` 是 36px，而 `Toggle` 用伪元素撑到 60×44（`form.tsx:70-74`），同一产品两套热区标准。

## Questions to Consider

1. 「需人工」该数「要动手的事」还是「出问题的作品」？卡片已经知道构成（失败 2 · 停滞 1 · 副作用 1），为什么摘要是一个没有单位的整数？
2. 23:00 时，「已入库 142」配得上第一行的位置吗？它是这页唯一一个永远不需要动作的数。
3. 面板允许对自己做什么——30 秒静默轮询，还是什么都不做？现在什么都不做，于是每一个「到底进去了没有」都要经过一次点击。
4. 重试成功后，屏幕该留下什么十秒：消失的行、一条回执、还是一个自己跳动的数？选一个，别三个都没有。
5. 网关会话在 23:00 死掉时，这个应用唯一的职责是说出口，还是重定向？现在两个都没做。
