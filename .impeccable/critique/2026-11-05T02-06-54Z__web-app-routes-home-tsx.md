---
target: the panel (web/app/routes/home.tsx)
total_score: 34
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 2
target_identity: "file:/root/clone/oh-my-mdc/web/app/routes/home.tsx"
target_fingerprint: "sha256:dba40a597c8bd93c2a6123fcde0bd28fc9bb36ff4debd359ecc1be84b870bd00"
target_path: /root/clone/oh-my-mdc/web/app/routes/home.tsx
timestamp: 2026-11-05T02-06-54Z
slug: web-app-routes-home-tsx
---
# critique · 面板（值班台）第六轮

Target: `web/app/routes/home.tsx` + `web/app/components/panel.tsx`（Operate 面）

Method: dual-agent (A: assess-a · B: assess-b)

上一轮 28/40 之后是提交 `577ce36`（那个数自己播报、网关死路变真态、左栏粘住、失败按可修性分组、热区抬到 44）+ `c618415`（DESIGN 补三条规则）。这一轮的命题是「验收五修」，再把手电照向上一轮没照到的角落：停滞行的重试之后、网关的另一种死法、手机首屏的心跳、以及键盘在「行不消失」时的落点。

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | 常驻活区实测播报 h1 数字（11→10 同步），上次读取走钟；但停滞行重试后回执说「已重试…回到下载中」而行不动、数不变（QQ-500 实测 h1 3→3）；手机首屏心跳 `slice(0,2)` 漏掉三条循环里唯一会失败的那条（panel.tsx:105-115） |
| 2 | Match System / Real World | 4 | 固定中文词汇 + 上游原话透传（「Telegram 403：Unauthorized（bot token 无效）」）+「重试不会变好」；无黑话 |
| 3 | User Control and Freedom | 3 | Esc/取消/清筛选/URL 回退齐备、抽屉焦点圈实测通过；但重试无撤销路径（可恢复流水线兜底，低风险），且「行不消失」的重试后焦点掉 body |
| 4 | Consistency and Standards | 3 | RowAction/CardAction/状态三件套一致；但 Unavailable 硬编码的「面板只读，不会重复下发」泄漏到设置面的错误态（home.tsx:410，设置面恰恰可写） |
| 5 | Error Prevention | 4 | remedy-first 是产品级防错：宿主机行实测零重试按钮、强制重刮先确认条（自动聚焦、焦点可回）、busyRef 防双击、按钮只动它印的 infohashes |
| 6 | Recognition Rather Than Recall | 3 | 版本身份用标签不用哈希、状态全写字面；但「需人工」口径解释只在 hover title（panel.tsx:1348）、搜索框无可见标签 |
| 7 | Flexibility and Efficiency | 3 | 键盘全可达 + `?state=failed&page=2` 可书签 + 300ms 防抖；无任何快捷键（无 `/` 聚焦搜索）、无跨行批量 |
| 8 | Aesthetic and Minimalist | 4 | 墨色唯一强调、彩色纯信标、空面板零家具；多版本行的徽章三件套堆叠是仅有的噪声 |
| 9 | Error Recovery | 3 | 网关死路已修（Unavailable 真态 + refresh-first 实测）；但 502 HTML 被误诊为「登录然后刷新」（api.ts:317-319 把一切非 JSON 回包当 authelia 登录页） |
| 10 | Help and Documentation | 3 | 帮助即系统：CardTitle title、确认条教后果；title-only 帮助触屏/键盘不可达 |
| **Total** | | **34/40** | **Good（上缘）**：五修全数落地，从 28 → 34；离 Excellent 差的全在「按完之后」与边缘路径 |

上界 40（Operate 面十条全部适用，无 n/a）。综合分 34：assess-a 初判 35，综合方依实测把第 1 项从 4 降到 3——「假完成」瞬间与手机心跳假安心是实打实的状态可见性缺口。

## Design Specificity Verdict

**面板面继续拉开与「品类家具」的距离：上一轮的缺口是「手一动之后」，这一轮验收确认主链路补上了，剩下的缺口全部在边缘路径与「按完之后我怎么知道它好了」。**

**LLM 评估**（assess-a）：独特性证据链是机制级的——宿主机病因行没有重试按钮、只有「重试不会变好 · 展开原因」收起行（remedy.ts:53-64 正则判 `retryable:false`，实测 GHD-900 buttons=[展开原因, 查看任务]）；「重试 2 条版本」把爆炸半径印在按钮上；番号/movie_id/infohash 三层身份各司其职，回执说「中文：回到加工开头」不说哈希。h1 就是那个数、已入库降级到统计行右端、心跳说「还没读到过」而不撒谎——值班台隐喻在代码里而不在文案里。可换词的残留：设置八节是标准 admin furniture（规格 §8.1 所定）、状态筛选 5 chip 与循环健康的右对齐数字是通用运维面板语汇。

**确定性扫描**（assess-b）：CLI `detect --json web/app` → exit 0、`[]`、0 条。但工具自述文件模式是正则匹配字面类名、元素级规则没跑（「findings are an undercount, not a clean bill of health」）。活性对照：种 11 个违规只抓 2 条（同一行 `gradient` 上；`text-[9px]`、`outline-none`、`shadow-lg`、卡中卡、跳级标题全部静默放过）——这个 0 不构成证据。浏览器浮层注入 3/3 成功（busy 桌面 / busy 移动 / error 桌面），全量元素规则真跑了一遍：唯一告警在移动端——「content overflowing its container」，与独立实测互证（390 宽下 60 字符长串把任务卡内容撑溢出，`div` scrollWidth 376 vs clientWidth 342）——文件扫描与人工评审都没抓到、浮层抓到的真问题。伪阳性记录：chip 发丝边框对比度 1.20–1.25:1 低于非文字 3:1 口径，但状态由文字+位置+色三重承载、非 color-only；danger 徽章行内 `border-transparent` 造成 19.2:1 伪值。

**浏览器实测补充**：对比度全达标（最低 muted/画布 5.85:1，状态字 5.87–8.73，焦点环 4.76/4.88）；字号集合 {12,13,14,16,24}px、无 <12px、根字号放大实测全部跟随缩放；标题零跳级；390 宽 88 个可点元素 <44px 的只有 sr-only 跳转链接；87 个可聚焦 0 个无可访问名、34 站 Tab 全部有可见焦点指示（13 outline + 23 box-shadow ring）；三宽三态零横向滚动；抽屉唯一带模糊阴影；徽章全带状态词 + aria-label、彩色侧边条 0 条。

## Overall Impression

28 → 34：趋势里最大的一次跳升，且「修的都验到了」——五项修复全部 PASS，连上一轮挂着的 rAF 后台标签页 P2 也被 `setTimeout(finish, 120)` 兜底从机制上拆掉（panel.tsx:1009-1021、1062-1079，实测忙位 66-71ms 内释放）。界面的信用主体（按钮印爆炸半径、修不动的行不给按钮、确认条教后果）完好，a11y 面接近满分。

最大的机会依然不是加东西，而是把一个问题问到底：「按完之后，我怎么知道它好了」。停滞行假完成（回执说动了、屏幕说没动）、502 被教去重登（病诊错了）、手机首屏心跳答半句（最会失败的循环恰好不可见）——三个互不相干的文件，是同一个问题的三种表现。

## What's Working

1. **补救优先的防错组织**（`lib/remedy.ts:36-64` + `panel.tsx:461-464, 532-600`）：四分节顺序 = 「我在这里能不能修」，宿主机病因正则识别后 `retryable:false`，实测该行零重试按钮、`/media/...` 原句默认收起、`原因同上` 去重生效。DESIGN 的 Remedy Rule 逐字落地。
2. **一个口径的计数系统**（`lib/attention.ts:19-33` → h1 → 构成行 → 常驻活区）：实测重试后 h1 5→4、活区同步播报、构成行与行数严格一致，全站找不到第二个总数。上一轮「那个数不出声」的 P1 以镜像实现（h1 旁常驻 sr-only `role=status`）修成了等价形态。
3. **时间诚实系统**（`lib/time.ts` + `useNow` + `loopState`）：相对时间 60s 走钟、绝对时间进 title、502 场景实测侧栏三行全部「还没读到过」——没有一个假「运行中」。

## Round-5 修复验收（commit 577ce36）

| 修复 | 判定 | 证据 |
|---|---|---|
| ① h1 计数入活区 | ✅ PASS（镜像实现） | home.tsx:182/266/368；实测 11→10 播报同步、首载静默 |
| ② 网关死路移除 | ✅ PASS | Unavailable 真态卡、无 ×、3.2s 无死锁；gateway 变体按钮序 = 「刷新页面」在前；无 snapshot 时循环行「还没读到过」 |
| ③ 侧栏 sticky | ✅ PASS | `sticky top-6 h-[calc(100dvh-3rem)]`，实测 top 24px / 高 852px，滚 1.5 屏后循环块 rect.top 797 在视口内 |
| ④ 按可修性分组 + 折叠 | ✅ PASS | 实测 DOM 顺序四分节；宿主行 `aria-expanded=false→true`、完整补法展开前不可见；折叠钮 390 宽 44px |
| ⑤ 44px 热区 | ✅ PASS | RowAction `h-11 md:h-7`；390 宽 88 可点中 <44 仅 sr-only 跳转链 |
| 遗留 P2：rAF 挂死 | ✅ 已修 | 忙位在 `afterPaint` 前释放 + `setTimeout(120)` 兜底；实测无 stuck_spinning、焦点落下一颗 data-retry |

## Priority Issues

### [P1] 停滞行重试后的「假完成」瞬间
- **What**：对停滞行按重试，回执报「已重试 QQ-500 · JavDB 磁力：重新加种，回到下载中」，但该行保留原位、h1 不变（实测 3→3）、行上无任何中间态。
- **Why it matters**：值班台的全部信用押在「屏幕说的 = 事实」。回执与屏幕并置矛盾，深夜用户会按「已重试」放心去睡，而 qBittorrent 里可能已没有那种子。
- **Fix**：重试停滞行成功后给行内瞬时中间态（阶段徽章暂变「重新加种中」，或行内追加 12px「已重新加种，等下一轮对账确认」）；回执补后果半句。改 `VersionGroup`（panel.tsx:724-820）+ `summarizeRetry`。
- **Suggested command**: `/impeccable harden`

### [P1] 网关 502 被误诊为「去登录」
- **What**：`isGatewayAuthError`（api.ts:317-319）把「200 + HTML」（真登录页）与「非 200 + HTML」（502/网关故障）同判为 auth；Unavailable 文案固定教「登录然后刷新」（home.tsx:409）。实测 502 场景下已登录用户被指去登录。
- **Why it matters**：上游挂了按会话过期治，用户白重登一次然后更不信任报错；这是唯一入口的失败态。
- **Fix**：在 `request()` 分支：200+HTML → `gateway_auth`（「登录后刷新」）；非 200+HTML → `gateway_bad`（「网关或上游故障，稍等再刷新」）。顺手把硬编码的「面板只读」安抚句按面传入（设置面不该说这句话）。
- **Suggested command**: `/impeccable harden`

### [P2] 手机首屏心跳漏掉最会失败的循环
- **What**：窄屏统计行 `loopLines(...).slice(0, 2)`（panel.tsx:105-115）固定显示 清单轮询+对账；副作用循环是三条里唯一会带失败进面板的，390 宽首屏永远看不到。
- **Why it matters**：「要不要再等」的答案答了一半；副作用失败时手机首屏给出假安心，真相在两屏之下。
- **Fix**：不按固定顺序 slice——failing 的循环行排前，再补前两条；或实测宽度放得下就直接显示 3 条。
- **Suggested command**: `/impeccable layout`

### [P2] 「行不消失」的重试后键盘焦点掉 body
- **What**：焦点交接只处理行消失分支（panel.tsx:1036-1040, 1181-1185）；停滞行重试后按钮 disabled 再启用，无人接回——实测 `activeElement === BODY`。
- **Why it matters**：纯键盘用户连续处理队列时每行都从页头重新 Tab；读屏用户彻底丢失位置。
- **Fix**：`release()` 后补分支：`origin.isConnected && document.activeElement === document.body → origin.focus()`。
- **Suggested command**: `/impeccable harden`

### [P2] 每次首载拉取两遍仪表盘
- **What**：home.tsx:138-143 的「从设置回来重读」effect 用加工后的 `view` 做比较：首渲染时把 null-snapshot 算成 `"settings"` 存入 ref，snapshot 到达后翻成 `"panel"` 触发多余 `refresh()`。Playwright 网络轨迹实测：每次冷加载完整两波 5 请求。
- **Why it matters**：违背了自己注释「首次挂载不重复拉」的意图；弱网值班场景首屏翻倍变慢。
- **Fix**：比较改用 `route.view`（纯地址派生），或 `previous.current === null` 时跳过。
- **Suggested command**: `/impeccable optimize`

## Persona Red Flags

**Alex（急躁高手）**：零快捷键（无 `/` 聚焦搜索、无单键重试、抽屉无 j/k）；五条失败 = 五次逐行点击，想要「重试全部可修的」+ 逐条回执的一键批量。会认账的：深链可书签、防抖搜索、`/settings/telegram` 直达节。红旗结论：核心循环对鼠标流畅，对键盘无加速器。

**Sam（读屏/纯键盘）**：实测通过清单很长（skip link、抽屉 inert+焦点圈+Esc 归还、确认条自动聚焦、回执活区常驻、44px 全套、状态全写字面）。红旗三条：① 行不消失的重试后焦点掉 body（P2-4）；② 「展开原因」后路径全文静默插入，无播报、焦点不动；③ 搜索框只有 aria-label 无可见标签，「需人工」口径解释只在 title。

**作者本人 · 23:00 值班运维者**：已被照顾好——手机全 44px、心跳进首屏、失败原因 13px 正文色、去设置一条真链接。红旗两条：① 停滞行假完成让他放心去睡；② 手机心跳漏副作用循环，最会失败的那条恰好不可见。深夜白底刺眼是 DESIGN 已裁决的取舍（Don't 复活深色主题），非遗漏。

## Minor Observations

1. 浮层互证的移动端长串溢出：60 字符不可断串在任务卡容器内被裁剪（无页面滚动、不破版），movie_id 展示策略值得核对（如 `break-all` 或中段省略）。
2. chip 发丝边框 1.2:1 低于非文字 3:1 口径——状态三重冗余兜底，记为口径账非缺陷。
3. 错误态 h1 回退成「面板」（home.tsx:170-176），规则在错误态暂时失守；可换「值班台暂时读不到」。
4. 状态筛选 5 chip 在 390 折成 4+1；5 个可见选项超认知负荷清单的 ≤4 项（唯一失败项）。
5. 抽屉事件时间线空 `from_state` 渲染成「— → 下载中」，左侧「—」略突兀。
6. 回执多版本拼接串三版以上会变日志，可考虑两版以上换行。
7. 循环健康行无「正在跑/耗时」、失败行无下游指引（唯一下游「去集成设置」语义上属 app 节）。

## Questions to Consider

- 如果每一行都从「按哪个按钮」改成「按完之后，我怎么知道它好了」——假完成、502 误诊、心跳答半句，会不会被同一个问题一起治好？
- 五个状态筛选 chip 能不能收敛成「只看还在动的」开关 + 一个搜索框？「需人工」的口径解释就不用藏进 title 了。
- 值班台的勇气问题：副作用循环失败时，手机首屏的心跳敢不敢把第一格位置让给它？
- 「已经清完了。」是全站唯一的庆祝时刻——要不要顺手给一天收尾：「下次轮询 22:45 · 3 分钟后」？
