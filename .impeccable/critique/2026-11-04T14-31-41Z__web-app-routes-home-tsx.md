---
target: the panel (web/app/routes/home.tsx)
total_score: 28
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 2
target_identity: "file:/Users/raincore/clone/oh-my-mdc/web/app/routes/home.tsx"
target_fingerprint: "sha256:251edbd12a17f8876a14f3f422bafac00d34de6c403175ae984d588b706fd3dd"
target_path: /Users/raincore/clone/oh-my-mdc/web/app/routes/home.tsx
timestamp: 2026-11-04T14-31-41Z
slug: web-app-routes-home-tsx
closed: true
---
# critique · 面板（值班台）第五轮

Target: `web/app/routes/home.tsx` + `web/app/components/panel.tsx`（Operate 面）

Method: dual-agent (A: assessA · B: assessB)

上一轮 29/40，之后只有一个提交（`4309570`：修抽屉主按钮对比度、卡片不再复述「需要人工」）。这一轮先验收那个 P0 是否真落地，再把账翻到底：四种状态 × 两个视口 × 15 个组合，加一次可复现的注入与一批实测数字。

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | `上次读取 22:24:02（刚刚）`、`清单轮询 每 15 分钟 · 2 分钟前`、忙位旋转都对；但 h1 从 `6 件事` 变 `5 件事` 不在任何 live region（`home.tsx:169`），实测被播报的是卡片里那句 13px 构成；网关失败时侧栏说 `循环 未启动`（`home.tsx:203-209`），把「读不到」说成「没在跑」 |
| 2 | Match System / Real World | 3 | `先在 qBittorrent 里确认种子还在下；确认丢了种，重试才会重新加种。` 和 `JavDB 400：您已創建了同名清單` 是产品级的人话；但版本被叫 `无标签`（`panel.tsx:1727`，把「没有判别词」说成缺陷），抽屉里 `失败于刮削：刮削取数失败：…` 把阶段说了两遍 |
| 3 | User Control and Freedom | 2 | Esc/遮罩/取消/逐条收起/筛选住 URL 都在；但网关失败时那个 `×`（`home.tsx:266` → `312`）把人锁进永久的「正在读取面板数据…」，实测点掉后 3 秒无任何重取 |
| 4 | Consistency and Standards | 3 | 状态三件套、`RowAction`、胶囊只给状态都守住了；同一个版本仍有三个名字（表格 `JavDB 已入库` / 行内 `JavDB 磁力` / 抽屉 `无标签`） |
| 5 | Error Prevention | 4 | 宿主机病因既不进按钮也不进计数、强制重刮只在有已入库版本时出现且先写后果、确认条开着时连遮罩点击都被禁掉（`panel.tsx:1516`）——这轮实测确认全部在线 |
| 6 | Recognition Rather Than Recall | 3 | 原因/补法/动作同处一屏、按钮印作用域、回执复述刚才动了哪几条；但两处决定性解释只在 `title`（`panel.tsx:1269` 需人工的判据、`:1694` 抽屉重试的语义），触屏压根没有 hover |
| 7 | Flexibility and Efficiency | 2 | 无批量、行不可点（`tr onClick` 无 tabindex，`panel.tsx:265`）、`更新` 列写死 `aria-sort="descending"`、无法复制 infohash、面板不自动刷新 |
| 8 | Aesthetic and Minimalist | 3 | 首屏安静且排序正确，红环克制；但滚过第一屏后左栏 208px 纯白（实测 `aside` 高 2038px、`static`），首屏最重要那块的可视线只有 13px/400，比它下面三张 16px/500 的卡标题都轻 |
| 9 | Error Recovery | 3 | 宿主机错给真路径、同名清单给「去 JavDB App 里删掉或改掉」、Telegram 403 给设置链接 + 幽灵重发；唯独网关这层唯一有效的 `刷新页面` 排在 `重试` 之后 |
| 10 | Help and Documentation | 2 | 模型说明全在 `title` 里，`需要人工` 的 h2 是 `sr-only`；侧栏那段静态散文在 1.5 屏之下、手机上不渲染 |
| **Total** | | **28/40** | **Good（70%）** |

上界 40（Operate 面十条全部适用，无 n/a）。上一轮 29 → 本轮 28：修好的东西真的修好了，但审计翻出了上一轮没跑到的两条（网关死路、后台标签页里的焦点悬停），净下来基本持平。

## Design Specificity Verdict

**每一行需要决策的地方都是为「一部作品多个版本」写的；外壳仍是可整体换词的品类家具，而且这一轮新增的证据是：外壳在滚过一屏之后会塌掉。**

**LLM 评估**（assessA）：`重试 2 条版本` 这个短语在通用队列里无处安放；版本身份胶囊 `中文 · JavDB 磁力` / `破解 · M-Team 种子`、回执挂在卡片而不是行里、宿主机病因**拒绝给按钮**并花一整句话解释「重试不会变好…在宿主上处理 `/media/movies/ABP-123/ABP-123.mp4` 之后再回来」——这些是抄不走的结构，不是文案。h1 就是那个数、卡片唯一可见的首行是它的构成，也是被论证过的选择。可换词的一半：卡片分区轨、搜索框 + 5 颗状态 chip、`第 1 / 3 页`、右侧 512px 抽屉、发丝分隔、shadcn 的 `CardHeader/CardTitle`。最刺眼的新账：滚过第一屏之后左栏变成纯白，屏幕上不再有任何「这台机器在值班」的痕迹——那一刻它退回成普通后台。

**确定性扫描**（assessB）：`impeccable detect --json web/app` → exit 0、`[]`、0 条 rule。工具自己声明「22 files scanned with regex matching only … findings are an undercount, not a clean bill of health」。活性对照证明它只看字面类名：一个种了 16 个反模式的 `.tsx` 只认 1 条（`from-purple-500` 的 `ai-color-palette`），`text-[9px]`、`outline-none`、`shadow-2xl` 全部被静默放过。**这条 0 不构成任何证据。**

**浏览器浮层**：注入成功（`detect.js` 3.1MB，HTTP 200），页内只剩一行 `[impeccable] No anti-patterns found.`，DOM 里 `[id^=impeccable],[class*=impeccable]` 节点数 0、`body.children` 5 → 5，前后截图逐像素一致，直接调 `window.impeccableScanAsync()` 也返回 `[]`。也就是**没有画任何东西**，不作为「页面干净」的证据；下面的数字才是证据。

**实测数字（15 个 状态×视口 组合）**：对比度 0 处不达标，全页最低 **5.51:1**（muted 墨落在 `#f5f5f5` 上），状态三件套 5.87 / 7.05 / 7.23 / 8.73，焦点环 4.88 白底、4.48 灰底（需 ≥3）。**上一轮那个 1.1:1 的抽屉主按钮确认修好**：`dense` 从 `text-*` 命名空间挪出去了，`重试 N 条版本` 与 `确认强制重刮` 现在实测 17.18:1。字号集合只有 12/13/14/16/24，无 <12px；两档宽度均无横向溢出；标题层级 0 跳级；36 个可聚焦元素 0 个缺可访问名；35/36 有可见焦点指示（缺的那个是 disabled 的 `上一页`，进不了 Tab 序列）；抽屉的 inert / 焦点循环 / Esc 归还焦点全部实测通过；`status-*-text` 从不单独承载状态；全站唯一带模糊的阴影仍是抽屉。

## Overall Impression

这个界面最贵的那部分仍然值钱，而且这轮被验证了：**没有假按钮**。按钮印自己的爆炸半径、服务端照办；修不动的行连重试都不给；不可逆动作先写后果、写完还把遮罩点掉这件事禁掉。对比度问题修得干净（最低 5.51:1，全站没有一处低于 AA）。扣分全部落在「手一动之后」：屏幕不发声（h1 静默）、键盘不落点（焦点落 body，且那条交接链挂在 rAF 上，后台标签页里整段悬停）、外壳会塌（滚过 208px 的左栏变成纯白）、以及唯一的那条入口失败态会自我关死（`×` → 永久转圈）。

最大的机会不是再加东西：这轮起有三条彼此独立的「沉默」——数变了不出声、行消失不落点、后台时不收尾。它们可以一条修法解决：**把「动作已生效」这一件事交给一个常驻的 live region 与一条与 rAF 无关的焦点交接**，而不是分散在构成行、回执块和 rAF 回执里。

## What's Working

1. **按钮印作用域，服务端照办**（`panel.tsx:695-790` → `internal/retry/retry.go:163-186`）：组里只交出还有救的版本，`retryTargets` 已剔掉宿主机病因，按钮上写的数字就是真正会动的条数；按下之后回执用同一套名字复述（`中文：回到加工开头；破解：回到加工开头`）。实测按下前后 h1 6 → 5、整组行消失、回执落在原行位置。运维界面最贵的坏习惯是「按了也不算数」，这里把它做成了契约。
2. **病因决定动作，而不是子系统决定动作**（`remedy.ts:57-90`）：ABP-123 只有 `查看任务`，没有重试；SSIS-001 的驻留副作用是 `去 Telegram 设置` + 幽灵 `重发`；STARS-256 的停滞是 `重试` 配一句「先在 qBittorrent 里确认种子还在下」。这是全页最产品化的结构，也决定了这一轮里「最不可动的那行最该被折叠」这条账。
3. **陈旧度是一等公民**（`panel.tsx:160-175`、`useNow()`、`lib/time.ts`）：`上次读取 22:24:02（刚刚）`、`循环健康` 三行各自独立走钟、读不到时写 `还没读到过` 而不沿用旧值。这让「没变」与「没读到」在屏幕上不是同一件事——而这正是 23:00 回家第一眼要分辨的东西。

## Priority Issues

### [P1] 网关失败是一条会被自己关死的死路

- **What**：`home.tsx:266` 的 `onClose={() => setLoadError(null)}` 清掉错误后，`home.tsx:312` 走 `loadError ? null : <Loader2>正在读取面板数据…` 分支，而取数 effect 只依赖 `queryKey`，不会再发请求 → 永久转圈（assessA 实测：点 `×` 后 3 秒，`main.textContent` 仍是 `正在读取面板数据…`）。同一屏里 `重试`（24×20）排在唯一有效的 `刷新页面`（48×20）之前（`home.tsx:288-303`），提示自身的关闭靶是 **14×14**。侧栏同时显示 `循环 未启动`。手机 390 上这一屏是 `面板` + 一条红条 + 约 700px 纯白。
- **Why it matters**：这是唯一的进入路径的失败态，也是 23:00 从网关进来最可能先看到的一屏。它不但没有下一步，还给了两个错误的下一步：先试着关掉它（锁死），或按那个重试（同一个失效会话再失败一次）。
- **Fix**：`loadError && !snapshot` 时在正文渲染一块持久的「读不到面板数据」空态，而不是 `null`；错误提示不给 `onClose`（或关掉即 `void refresh()`）；`刷新页面` 升为第一颗按钮；`loopState()` 在 `snapshot === null` 时返回 `还没读到过`；提示里的行内动作与 `×` 抬到 44×44。
- **Suggested command**: `/impeccable harden`

### [P1] 手一动，屏幕不发声、键盘不落点

- **What**：四件同源的事实——① 变化的那个数不在 live region（`home.tsx:169`；实测 DOM 变异目标是 `H1`，祖先链无 `aria-live`/`role=status`，被播报的只有构成行 `失败 2 部 · …`）；② 回执的 `div[role=status][aria-label=操作回执]` 是**带着内容一起挂载**的（`panel.tsx:644`），活区在被播报之前不存在；③ 强制重刮的确认条（`panel.tsx:1616-1650`，479×97）既无 `role` 也无 `aria-live`，出现时也没有任何元素获得焦点；④ 重试成功后焦点落到 `body`——instrumented 运行里 `HTMLElement.prototype.focus` 被调用 **0 次**，`[data-retry]:not([disabled])` 一次都没被查询，而下一行可按的重试当时就在树上。
- **Why it matters**：这是整个产品的峰值时刻。看得见的人靠 h1 少一个数字确认，读屏的人听到的是更小的那句话，键盘用户按完之后不知道自己站在哪；而「按了之后什么都没有发生」正是值班台最不能给的反馈。
- **Fix**：给 h1 本体一个常驻的 `role="status"`（或把构成行与那个数合成一句 live 文本），并让回执的活区在无回执时就挂在树上（空内容常驻，只更新文本）；确认条加 `role="group"` + `aria-live="polite"`，出现时把焦点移到 `确认强制重刮`；焦点交接不依赖 `afterPaint()` 的 rAF（见下一条）。
- **Suggested command**: `/impeccable harden`

### [P2] 焦点交接与忙位复位挂在 rAF 上，后台标签页里整段悬停

- **What**：`panel.tsx:937-941` 的交接（以及 `finally` 里的忙位复位）排在 `await afterPaint()` 之后，而 `afterPaint` 用两次 `requestAnimationFrame`（`panel.tsx:960-971`）。标签页不可见时 rAF 不触发：assessA 与 assessB 两个独立会话都实测到 `document.visibilityState === "hidden"` 下 1.5 秒不回调，按下重试后 `activeElement` 落在 `body`、`[data-retry]` 仍有两个可选项；忙位旋转在这段时间里不会收。
- **Why it matters**：手机上按完就锁屏、或按完切去 JavAdb 是常态动作。回到页面时接手的是「按钮还在转、焦点在 body」，而且下一条重试有可能在用户回来的一瞬间被抢焦点。
- **Fix**：用 `setTimeout(0)` / `queueMicrotask` + 一次 layout flush（或 `document.visibilityState !== "visible"` 时直接走同步分支）替代裸 rAF；`finally` 的忙位复位提到 `afterPaint` 之前，只有焦点交接留给「提交之后」。
- **Suggested command**: `/impeccable harden`

### [P2] 滚过一屏之后，桌面外壳只剩右移的一列

- **What**：`home.tsx:179` 的 `<aside className="hidden w-52 shrink-0 flex-col gap-6 md:flex">` 是 `position: static`；实测 `aside` 高 2038px、页高 2086px、`循环 运行中` 那块在 y=1986，而视口只有 766——第一屏之后导航、循环状态与那段说明全部离屏，左侧 208px 变成纯白（1440 宽第二、三屏截图）。手机上同一信息更远：`循环健康` 在 3363px 文档的底部，而手机侧栏被隐藏，没有替代品。
- **Why it matters**：值班台的存在理由是「随时知道这台机器什么状态」。看表格的那 1500px 正是它最该在的时候，而它恰好不在；副作用队列与循环健康要滚过 10 行任务才摸得到。
- **Fix**：外层 `aside` 保留占位、内层 `sticky top-6 h-[calc(100dvh-3rem)]`；把 `循环 运行中` 两行提到导航正下方（它比那段静态散文重要得多）；删掉 `未初始化时整站只剩向导…`（未初始化时正文已经是向导，这句话永远不解决当下的问题）；手机上把循环三行并入统计行已有的 `清单轮询` 那一段，而不是留在文档末尾。
- **Suggested command**: `/impeccable layout`

### [P2] 首屏最重要的一块，在排版上比它下面三张卡都轻；「修不动的」与「能修的」同权重

- **What**：告警卡唯一可见的首行是 `<p className="dense">`（`panel.tsx:471-482`，实测 13px/400/1.5），`任务`/`副作用队列`/`循环健康` 的标题是 `CardTitle`（16px/500），真正的 `需要人工` 标题是 `sr-only`。于是全页等级是 h1 24/600 → 卡标题 16/500 → 最该被读的那行 13/400。同时 `VersionGroup` 按 `groupByNumber` 顺序渲染：宿主机病因的 ABP-123 与可重试的 IPX-777 字号、原因字号、动作列完全同级，区别只有「没有按钮」+ 一段 13px 的提示；390 上 ABP-123 一行占 543→790 CSS px，接近首屏 40%。另外首屏之后表格永远在折下：`1440×900` 时第一条任务行顶部在 **1109px**（折下 209px），`390×844` 时第一张任务卡在 **1659px**（折下 815px），告警卡自身桌面 802px / 手机 1190px。
- **Why it matters**：扫读时视线先落在 16px 的 `任务` 上，而那张卡里 10 行只有 3 行与当下有关；值班台上「我在这里能做什么」是第一问，现在必须逐行读完才知道哪几行值得动手，而最不可动的那行往往排在最前。
- **Fix**：构成行升到 14–15px/500（或直接用 headline 规格的 `h2`），`AlertTriangle` 与它同基线；失败节内再切一刀 `能在这里修` / `要去别处修`（或把不可修行的提示收成一行 `重试不会变好 · 展开原因`），可修的行排前；单组内 `reason` 相同（SSIS-001 的两条）只印一次，让分组高度随「要动手的事」而不是「条目数」增长。
- **Suggested command**: `/impeccable typeset`

### [P3] 同一个版本三套名字，最该读的那句话在口吃

- **What**：`VersionChip` 用 `version.label || sourceShortLabel()` → `JavDB 已入库`（`panel.tsx:1388-1405`）；需要人工里 `versionIdentity()` → `JavDB 磁力`（`:745-760`）；抽屉里 `version.label || "无标签"` + 另起一行来源（`:1727`）。抽屉失败句是 `失败于刮削：刮削取数失败：DMM 接口 503…`（`:1758`）；事件时间线是 `人工重试` + `人工重试：回到加工开头` 两遍；回执在手机上会把 `破解` 拆成 `；破` / `解：…`。
- **Why it matters**：`无标签` 把正常状态说成缺陷；口吃让那句 13px 的结论变成需要解析的文字，在 23:00 的手机上尤其贵。
- **Fix**：抽一个 `versionIdentity(version)` 供 chip / 行 / 抽屉共用（`无标签` 改 `无判别词` 或直接给来源）；抽屉那句在 `reason` 已以阶段词开头时不再加前缀；`EventRow` 在 `reason` 已含 kind 标签时不重复渲染；回执按「名字 + 结果」分段，给 `破解：回到…` 一个 `whitespace-nowrap` 或 `:has()` 边界，别让一个词断在行尾。
- **Suggested command**: `/impeccable clarify`

## Persona Red Flags

**Alex（清队列的高级用户）**：3 条可修条目 = 3 次独立往返，位置分散（`重试 2 条版本`、`重试`、`重发`），没有 `全部重试`。表格行鼠标可点但不是可达元素（`panel.tsx:265` 的 `tr onClick` 无 tabindex），键盘必须 Tab 到番号那颗 `min-h-6` 按钮，23 行 = 23 次 Tab 才能到底部。`更新` 列写死 `aria-sort="descending"`，没有「只看可重试」「只看某阶段」，infohash 无处可复制（只在 `title` 与 8 位前缀里）。想强制重刮必须开抽屉：表格里没有入口。

**Sam（键盘 / 读屏 / 低视力）**：h1 那个数静默，被播报的是更小的构成行；动作会播报**两次**（构成行 `aria-live` + 回执 `role=status`），内容高度重叠；确认条不播报也不接焦点；重试成功后焦点落 `body`（`focus()` 0 次调用）。关键解释只在 `title`：`需人工` 的判据、抽屉重试的语义、`VersionChip` 的完整状态——而 `aria-label` 挂在一个没有 role 的 `<span>` 上（`panel.tsx:1398` over `ui/badge.tsx:39`），屏幕阅读器多半直接忽略。桌面行内动作实测 66×28，低于 44。

**23:00 拿手机从网关进来的作者**：第一屏顺序是品牌（约 92px）→ 44px 两格分段导航 → 24px 的 `6 件事需要人工` → 两行统计（`清单轮询 每 15 分钟 · 2 分钟前` 夹在第四句，主语不明显）→ 红卡。能动手的东西确实在一屏内（第一个可点动作约 543px，第一个 `重试` 约 684px），但 `重试不会变好：…/media/movies/ABP-123/ABP-123.mp4…` 占 4 行、无任何可点出口。24/36 个可点元素小于 44×44：回执的 ✕ **14×14**、错误条里的 `重试` **24×20**、状态片 36px 高且彼此只隔 **4px**、行内动作间隔 **6px**。上下滑 3363px 才见循环健康；撞上会话过期则是一条红条 + 700px 纯白 + 一个会把自己关死的 ✕。

## Minor Observations

- SSIS-001 的两条版本原因逐字相同（`刮削取数失败：DMM 接口 503，30 秒内重试 3 次`），去重只对 `hint` 生效（`panel.tsx:734`），`reason` 没有——手机上这一组多占两行。
- 抽屉里的 8 位哈希只差第 7 位（截自同一 movie_id 的 mock 数据），它本想回答「哪条是哪条」；真正起作用的是 `中文`/`破解`。真机上哈希仍是随机 8 位，这条只说明「哈希前缀不是身份」。
- `副作用队列` 的动作在手机上换行后左对齐，与桌面右对齐同页并存，读起来像两套规则。
- 表格里 `已入库` 徽章与该行 chip 的 `JavDB 已入库` 把同一个词说两遍（任务级 vs 版本级），没有图例时容易被当成重复。
- `共 23 条`（12px）与 `第 1 / 3 页` 在手机上分居两屏；`pageCount > 1` 才渲染分页这条规则是对的。
- 网关态 h1 是 `面板`，与 DESIGN「『面板』这个词不再是标题」冲突；可接受，但那一屏的标题就此没有信息量。
- 手机上被禁用的 `面板` 导航项（`opacity-60`）没有可见解释（`title` 在触屏上无效），看起来只是「选了但灰了」。
- `?state=failed` 的空态与 `没有匹配的任务。` 两段文案本轮**未能渲染验证**：mock 的 `/api/tasks` 忽略 `state`/`q`（fixture 的错），只做了源码判断。
- 产品侧没有「h1 那个数（attention 行数）」与「表格徽章（task_state 视图）」之间的一致性校验：attention 被截断或两接口短暂不同步时，会渲染出 `没有要动手的事` + 满屏 `需人工`。
- 已入库 `141` 仍是 `font-medium text-foreground`，与 The Ordered-By-Cost Rule（「不加粗」）不一致：降级只发生在位置上。

## Questions to Consider

1. 如果 h1 就是产品的全部标题，为什么它每次变化时唯一被播报的是它下面那句构成？把 live 语义挪到那个数上，会不会比现在更接近「值班台先报数」的本意？
2. 「能在这里修 / 要去别处修」是不是这一屏真正的第一刀？现在第一刀切在子系统（失败 / 停滞 / 副作用），而作者心里的第一问是「我现在按哪个」。
3. 左栏在看表格的那 1500px 里要不要继续存在？如果它变成一条固定的 40px 状态条（`循环 运行中 · 清单轮询 2 分钟前 · 已入库 141`），内容列会多出 224px——还是这张表更喜欢现在的呼吸感？
4. 回执说的是机器做了什么（`回到加工开头`）。值班台有没有可能改说人该做什么（`两分钟后看一次，不用你现在动手`）——还是那已经是越过产品边界的猜测？
