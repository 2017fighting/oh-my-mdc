---
target: the panel (web/app/routes/home.tsx)
total_score: 29
max_score: 40
na_heuristics: 
p0_count: 1
p1_count: 1
target_identity: "file:/root/clone/oh-my-mdc/web/app/routes/home.tsx"
target_fingerprint: "sha256:251edbd12a17f8876a14f3f422bafac00d34de6c403175ae984d588b706fd3dd"
target_path: /root/clone/oh-my-mdc/web/app/routes/home.tsx
timestamp: 2026-11-04T10-37-38Z
slug: web-app-routes-home-tsx
---
# critique · 面板（值班台）第四轮

Target: `web/app/routes/home.tsx` + `web/app/components/panel.tsx`（Operate 面）

Method: dual-agent (A: assessA2 · B: assessB2)

上一轮 26/40，随后六个提交把 P0/P1 全部处理掉；这一轮检查它们是否真的落地，以及有没有带入新问题。

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | 那个数现在有全页最大的字（`home.tsx:165-175`），但它不在 live region 里，只有构成行是（`panel.tsx:458`）：8→7 是静的；`useNow` 没有 `visibilitychange` 重同步（`panel.tsx:1783-1791`），手机息屏回来仍可能读「3 分钟前」 |
| 2 | Match System / Real World | 3 | 词汇精确，但同一个概念两种写法：`驻留副作用`（`panel.tsx:432/529`）vs 徽章 `副作用驻留`（`panel.tsx:778`）；同一个数三种单位并排：`件` / `部` / `条版本` |
| 3 | User Control and Freedom | 3 | 筛选住进 URL、Esc/遮罩/取消/收起都在；但重试不可撤销、没有批量，重试在飞时遮罩仍能关掉抽屉（`panel.tsx:1487`），在飞的结论会跟着消失 |
| 4 | Consistency and Standards | 3 | 状态三件套与 `RowAction` 已是单一来源；但抽屉的 `重试 N 条版本`（`panel.tsx:1655`）数的版本里有它注定会跳过的（`internal/retry/retry.go:190-200`），而面板那一行刚学会只数真要动的 |
| 5 | Error Prevention | 4 | 宿主机病因既不进按钮也不进计数（`panel.tsx:690/738-745`），连点不二次下发，强制重刮只在有已入库版本时出现且先写后果，永久错先给设置链接 |
| 6 | Recognition Rather Than Recall | 3 | 回执、身份徽章、构成行替人记住了该记的；但数据模型仍只活在 `title=` 里（`panel.tsx:182/338/1203`），且回执在切视图后丢失（`panel.tsx:567-596`） |
| 7 | Flexibility and Efficiency | 2 | 无批量、无键盘换行、无页大小、无排序、无法复制 infohash；每次动作重拉五个端点（`home.tsx:383-401`） |
| 8 | Aesthetic and Minimalist | 2 | 克制依旧，但抽屉里的主操作实测 **1.1:1** 不可读（见 P0），且站在失败里的东西红得一样重：一键能修的和要上宿主机处理的看起来同级 |
| 9 | Error Recovery | 4 | 网关死路变成真错误态并给对的两个动作（`api.ts:300-312`、`home.tsx:265-286`），上游原话透传，筛选空态解释为什么卡与筛不一致；残留：行级重试的失败提示仍落在卡片顶部（`panel.tsx:466`） |
| 10 | Help and Documentation | 2 | 模型说明全在 `title=` 里（`panel.tsx:182/338/1448/1648`）——手机与读屏都拿不到，而它们正是主persona；DESIGN 又禁止正文讲模型，于是界面上无处可查 |
| **Total** | | **29/40** | **Good（72.5%）** |

上界 40（Operate 面十条全部适用，无 n/a）。上一轮 26 → 本轮 29。

## Design Specificity Verdict

**每一行需要决策的地方都是为本产品写的，外壳约 40% 仍是品类家具。**

抄不走的：按病因拒绝给按钮并花文案说「重试不会变好…在宿主上处理 /media 之后再回来」（`remedy.ts:57-66`）、版本身份用 `中文 · M-Team 种子` 而不是 infohash（`panel.tsx:812-815`）、按钮印自己的爆炸半径且服务端照办（`panel.tsx:745` → `internal/retry/retry.go:163-186`）、不可逆动作先写后果再给按钮（`panel.tsx:1616-1635`）、`50+` 的截断诚实（`panel.tsx:422-426`）、以及这轮新的、没有任何别家会把产品名位置让给一句 `8 件事需要人工`。抄得走的：品牌行 + 两个导航项 + h1 + 统计行 + 刷新（`panel.tsx:120-171`）、搜索框加五个状态片（`panel.tsx:1164-1240`）、`番号/状态/版本/更新/→` 加翻页的表格（`panel.tsx:233-320`）、以「去集成设置」收尾的循环健康卡（`panel.tsx:336-376`）——词是这产品的，形状可以被 find-replace 进任何 job-queue 后台。

**确定性扫描**：`detect --json web/app`（含两个目标文件单独扫、文本模式）四轮 exit 0、`[]`，无 suppression、无 `.impeccable/config.json`。活性对照：`/tmp` 里的 `.tsx` 报 3 条（`side-tab`/`gradient-text`/`ai-color-palette`），`.html+CSS` 报 21 条；而把一个装了 28 种反模式的 `.tsx` 扫一遍只认 4 条。**静态 TSX 路径只做少量类名正则**：对比度、字号、行高、标题跳级、遮挡、溢出全部属于「需要解析页面」的那一类，所以 CLI 干净在这里只等于「没写那几种 AI 味类名」，几乎不值得当证据——真正的判据在浏览器那一轮。

**浏览器证据**：注入路径没有使用（本轮不需要），同一标签页在扫描前后各探一次，`impeccable` 节点 0、脚本 0、样式 0、视口与焦点未变、URL 未变——所以下面的证据是**线上原样**的页面，不带浮层。5 个状态 × 2 个视口共 10 次扫描（行队/筛选空态/抽屉/点过重试的回执/零项加回执），其中 9 次 `[]`，**唯一 1 条真缺陷**：抽屉底部的 `重试 N 条版本` 实测 **1.1:1**（文字 `oklch(0.145 0 0)` 落在 `oklch(0.205 0 0)` 上），确认条的 `确认强制重刮` 同病。根因在 DOM 里查实：`cn()`（tailwind-merge）把自定义 `text-dense` 当成**颜色**类，于是把 variant 的 `text-primary-foreground` 删掉了，按钮继承 `--foreground`。逐像素补测：九个干净状态 129–197 个文本样本，**低于 AA 的 0 个**，最低 5.51:1（回执的「刚刚」），最小字号 12px；抽屉状态的那 1 个样本即上面这条。

## Overall Impression

六步改得是真的：那个数收了、成功会说话了、重试的爆炸半径不再撒谎、网关死路变成了有下一步的错误态、标题让给了「还有几件事」。这一轮扣分的三件事性质不同——一件是我上一轮 polish 时亲手弄坏的（抽屉主按钮不可读：自定义字号 token 撞上 tailwind-merge 的颜色分组），一件是上一轮修复留下的回味（`需要人工` 同时是页面标题、卡片标题和筛选片，而卡片在清空后仍叫这个名字，于是全页最大的两行字互相打脸），一件是这产品一贯的老账（首屏在手机上答不出「机器还醒着吗、下次轮询什么时候」）。最大的机会不是再加东西，而是让 h1 成为唯一说话的那一个：它变化时被播报、清空后卡片改成「刚才做的」、筛选片改叫「全部版本失败」。

## What's Working

1. **按钮印自己的爆炸半径，服务端照办**（`panel.tsx:690/745` → `internal/retry/retry.go:163-186`）：组里只交出还有救的版本，后端按 infohash 选，点名不到就回空结果而不是偷偷重试整个任务。运维界面最贵的坏习惯是「按了也不算数」的按钮，这里把它变成了契约。
2. **病因路由用文案代替控件**（`remedy.ts:57-66/70-76`）：宿主机问题不给按钮、从 errno 里抠出目录、直说「重试不会变好」；永久上游错先给到那一个设置分区的真链接。这是全页最产品化的一处。
3. **成功有了持久的对象**（`panel.tsx:600-650`）：回执挂在卡片上、带版本的判别词、限量自净、可逐条收起。上一轮那个「失败会说话、成功不会」的倒置从结构上修好了——剩下的只是位置。

## Priority Issues

### [P0] 抽屉里的主操作实测 1.1:1，文字基本不可读

- **What**：`DrawerActions` 的 `重试 N 条版本`（`panel.tsx:1655`）与确认条的 `确认强制重刮`（`:1630`）都用 `className="… text-dense …"`；`cn()` 里 tailwind-merge 不认 `text-dense` 是字号，把它归进文字颜色组，于是把 `buttonVariants` 的 `text-primary-foreground` 删掉了。实测文字 `oklch(0.145 0 0)` 落在底色 `oklch(0.205 0 0)` 上 = **1.1:1**（需 4.5:1），1440 与 390 两档、普通态与确认态三处独立复现，DOM 类名列表里确实缺了颜色类。
- **Why it matters**：这是抽屉里唯一的主操作，也是整个产品唯一不可逆动作的确认按钮。上一轮加深色主按钮时可读，是这轮把 13px 收成 `text-dense` token 时弄坏的——一个自造的 token 在工具链里被误分类，代价是一个黑底黑字的主按钮。
- **Fix**：让这个 token 不出现在 `text-*` 命名空间里（Tailwind v4 用 `@utility dense { font-size: …; line-height: …; }`，类名就叫 `dense`），或给 `cn` 补 tailwind-merge 的 `font-size` 扩展；两者都要在抽屉打开态复测 4.5:1。
- **Suggested command**: `/impeccable polish`

### [P1] `需要人工` 一张嘴变成三张嘴，清空后卡片标题还和 h1 打架

- **What**：`需要人工` 同时是 h1 那句话（`home.tsx:169`）、卡片标题（`panel.tsx:455`）和筛选片 `需人工`（`panel.tsx:1169`），同一个视口里说三遍。更糟的是清空之后：h1 说 `没有要动手的事`，卡片标题仍是 `需要人工`、说明是 `已经清完了。`（`panel.tsx:447-460`）——全页最大的两行字互相否认。卡片内部同一个词还用两种单位并排：构成行 `失败 1 部`（`:431`）与分节标题 `失败 2 条版本`（`:479/651-660`）。
- **Why it matters**：上一轮的 P0 是「一件事三个数」，这轮变成了「一个概念三个名字、一个数两种单位」。作者读完 h1 再读卡片，必须自己算「部」和「条版本」是不是同一个东西——而这正是构成行当初被加进来要消灭的算数。
- **Fix**：h1 独占这句话；卡片在 `total === 0` 时不带标题（或改叫 `刚才做的`）；删掉分节计数、只留构成行，或把两者写成同一个单位；筛选片改叫 `全部版本失败`，让「需人工」只属于一个概念。
- **Suggested command**: `/impeccable clarify`

### [P2] 手机上首屏答不出「机器还醒着吗、下次轮询什么时候」

- **What**：循环状态只有两处——桌面侧栏（`home.tsx:179-211`，`hidden md:flex`）与页面最后一张卡（`panel.tsx:336-376`，在副作用队列与最多 50 行任务表之后）。手机顶部那块只有品牌行与两个导航项（`home.tsx:214-240`）。
- **Why it matters**：23:00 清完队列之后，作者的下一个问题就是「我刚加的那部，下一轮什么时候去拿」。页面说「没有要动手的事」然后沉默，答案要滚过一张分页表格才找得到；安静的收尾只有在心跳可见时才让人安心。
- **Fix**：把一行循环状态放进统计行（`清单轮询 每 15 分钟 · 3 分钟前 成功`，数据在 `intervals` 与 `overview.loops` 里已经算好了），或在手机头部块里给同一行。
- **Suggested command**: `/impeccable layout`

### [P2] 回执替眼睛记住了，却抢了键盘的位置

- **What**：每次行消失型成功都把焦点强制移到回执列表（`panel.tsx:895-899` → `:612-617`），而回执在卡片顶部、最多带三个「收起」按钮（`:640-648`），排在下一条 `重试` 之前。
- **Why it matters**：用键盘清八行，现在每次成功要付最多四个多余 Tab 和一次视口跳到卡片头；而回执本身是 `role="status"`，已经会被播报——移动焦点对读屏没有额外收益，对看得见的键盘用户是纯损失，也和抽屉那套「把焦点还给触发行」的讲究相反（`panel.tsx:96-104`）。
- **Fix**：焦点留在这个列表里——移到下一条的 `重试`，实在一条不剩才落到回执；播报交给 `role="status"`。
- **Suggested command**: `/impeccable harden`

### [P2] 决定「按不按」的那句话，是行里最小最淡的字

- **What**：补法（hint）是 `text-xs text-muted-foreground`（`panel.tsx:727-729`），排在 13px 正文色的原因下面（`:723`）。宿主机失败与永久上游错时，hint 才是那句该照做的指令，原因是 errno 转述；停滞行里 hint 是唯一区分「按了会重新加种」与「按了也没用」的信息（`remedy.ts:70-76`）。
- **Why it matters**：DESIGN 自己写着「决定按哪个按钮的那句话用 13px 正文色」。这一轮把补法变成决定句之后，它仍穿着脚注的衣服——手机上先读到 `permission denied`、再看到红徽章、最后才看到那句 12px 的「不要按」。
- **Fix**：`!retryable` 或「这句话改变决定」时把 hint 提到 `dense + 正文色`，同时把纯 errno 的原因降为次级墨；按「人该照哪句动」分配权重，而不是按字段来源。
- **Suggested command**: `/impeccable typeset`

## Persona Red Flags

**Alex（力量型用户，成批清队列）**：没有批量，八行八次往返，每次都要等五个端点刷新（`home.tsx:383-401`）且没有乐观移除，按下去先卡一下行才走。每次成功后焦点落到回执（`panel.tsx:895-899`），每条回执又往 Tab 路径上加一个「收起」（`:640-648`）。抽屉的按钮过度承诺：`重试 N 条版本` 数的版本里，已入库的那些注定回答「只有『强制重刮』才会让它重跑」（`internal/retry/retry.go:190-200`），而面板那一行刚学会只数真要动的。infohash 无处可复制，只在 `title` 与 8 位前缀里（`panel.tsx:1674-1680`），他只能去 qBittorrent 手敲。

**Sam（键盘 / 读屏 / 低视力）**：`VersionChip` 把 `aria-label` 放在普通 `<span>` 上（`panel.tsx:1349` over `ui/badge.tsx:30-41`），读屏拿不到完整来源名。桌面表格行可点但不可聚焦（`panel.tsx:260-263`：`tr` + `onClick` + `cursor-pointer`，无 tabindex、无键盘处理），唯一入口是首格那颗 24px 高的番号按钮。变化的那个数不播报：h1 在 live region 之外（`home.tsx:242`），被播报的是卡片里的构成行——于是重试成功后读屏听到的是构成，不是新总数。行级重试的失败提示渲染在卡片顶部（`panel.tsx:464-470`），不在按下去的那一行，而回执抢焦点的逻辑在出错时又不触发。

**23:00 拿手机从网关进来的作者**：第一帧是 `<h1>面板</h1>` 加 spinner，那句话要等五个并行请求都回来才换上，标题当着他的面换字。首屏没有循环心跳（`home.tsx:214-240`），心跳在队列卡与一张分页表之后的最后一张卡。所有模型说明都是 `title` 提示（`panel.tsx:182/338/1203/1648`），iOS 长按不管用。`刷新` 只在统计行右端（`panel.tsx:163-170`），滚过五十行之后要滚回去按；没有下拉刷新也没有自动刷新。`useNow` 是裸 `setInterval`，手机睡醒后相对时间可能停在旧值，而这页的诚实招牌就是「上次读取 14:42:05（2 分钟前）」。

## Minor Observations

- `panel.tsx:107-111` 的注释仍宣称那个数「唯一的住处是下面那张卡的徽章与构成说明」——徽章已经没了、数搬到 h1 了，注释停在上一版。
- `已入库` 仍是 `font-medium text-foreground`（`panel.tsx:148`），与 DESIGN 新写的 The Ordered-By-Cost Rule（「不加粗」）不一致：降级只发生在位置上，141 与两个活着的计数同权重。
- 停滞的 hint 是常量串（`remedy.ts:70-76`），去重只在组内比较（`panel.tsx:704-706`），三部停住的片会把同一句 qBittorrent 话印三遍；它应该只出现在分节标题上。
- 回执的成功词 `已下发`（`panel.tsx:628`）夸大了纯本地状态重置：`reset_processing` 不向外下发任何东西，只是把一行拨回去。
- 回执是组件状态（`panel.tsx:567-596`）：点进卡片自己推荐的 `/settings/telegram` 再回来，回执就没了。
- `<th className="w-8 px-2 py-2" aria-label="打开抽屉" />`（`panel.tsx:249`）给读屏一个空列头，内容只是装饰性箭头的名字。
- 抽屉遮罩在重试在飞时仍能关掉抽屉（`panel.tsx:1487`，只挡了 `confirming`），在飞的结论随之消失。
- `fetchDashboard` 是单个 `Promise.all`（`home.tsx:383-401`）：首载时 `/api/side-effects` 一个失败就把整个面板变成「读不到面板数据」，连本来正常的任务表也一起没了。
- 副作用队列里 `{( <div className="divide-y">…)}` 是一层包着唯一子节点的死 JSX 表达式（`panel.tsx:1068-1071`）。
- DESIGN 写统计行用中点分隔（`下载中 3 · 加工中 2`），代码是 `gap-x-4` 的独立 span、没有分隔符（`panel.tsx:124-131`）。
- 构成行说 `驻留副作用`、分节标题也说 `驻留副作用`、行上徽章却说 `副作用驻留`（`panel.tsx:773-779`）——挑一个词序。
- 抽屉的 `重试 N 条版本` 与面板的精确计数是两套标准；同一个标签在两个面上含义不同。

## Questions to Consider

1. 如果 h1 是这页的答案，它变化时该不该由它自己说话？现在被播报的是构成行（更小的事），那个数反而沉默。
2. 最后一行清掉之后，那张还活着的卡为什么还叫 `需要人工`，而不是变成「刚才做了什么」的记录？回执是今晚这个队列被处理过的唯一证据，标题却在和 h1 吵架。
3. `已入库 141` 值得待在首屏第一行吗——23:00 的作者永远不可能对它动手？
4. 一件作品同时有失败版本和停滞下载时，它该算「1 件事」还是「2 件事」？
