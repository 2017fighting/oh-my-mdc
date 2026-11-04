---
target: the panel (web/app/routes/home.tsx)
total_score: 24
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 2
target_identity: "file:/root/clone/oh-my-mdc/web/app/routes/home.tsx"
target_fingerprint: "sha256:035e6fc767a9b98489cbb66e347a9a9beaae76b25206054ae70b56cd4726ac59"
target_path: /root/clone/oh-my-mdc/web/app/routes/home.tsx
timestamp: 2026-11-04T07-15-30Z
slug: web-app-routes-home-tsx
---
# critique · 面板（值班台）

Target: `web/app/routes/home.tsx` + `web/app/components/panel.tsx`（Operate 面）

Method: dual-agent (A: assessA · B: assessB)

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 2 | 「需人工」同屏印两遍且算法不同：统计条用 `counts.needs_attention`（失败**任务** + 驻留副作用，`internal/task/store.go:403`），卡内徽章与说明用 `failed.length + stalled.length + stuck.length`（失败**版本** + 停滞 + 驻留，`panel.tsx:412`）；加载中页头已断言「初始化向导 / 未初始化」而正文是「正在读取设置…」（`home.tsx:113`、`:187-199`）；取数失败后错误条与 spinner 永久并存（`home.tsx:197-217`，非 else-if） |
| 2 | Match System / Real World | 3 | 术语与固定词汇咬得很紧（番号/版本/停滞/驻留/重试不会变好）；但决策句里漏出裸 errno：`permission denied: /media/jellyfin/jav/KV-328` |
| 3 | User Control and Freedom | 3 | Esc、遮罩关闭、焦点回位、`inert`、强制重刮的行内确认、修订冲突恢复都在；全局加载错误条不可关闭（`home.tsx:199-208` 无 `onClose`） |
| 4 | Consistency and Standards | 2 | 同一动作两个词：`重发`（`panel.tsx:590`）vs `立即重发`（`:876`）；同一事实两种长相：`已试 N 次` 一处是 Badge（`:849`）一处是普通元信息（`:556`）；向导步骤按钮 `rounded-md` + `bg-accent`（`wizard.tsx:293-296`）不按 DESIGN 的导航规格；行内动作类名 `h-9 px-2.5 text-[0.8125rem] md:h-7 md:text-xs` 在 `panel.tsx` 里复制了 10 次 |
| 5 | Error Prevention | 3 | 宿主机故障不给重试（`remedy.ts:55-61`），强制重刮只在对数 `finishedCount > 0` 时出现且先写后果；缺口：行内「重试」的爆炸半径不可见——行按版本生成，POST 却是整任务（`panel.tsx:486` `retry(item.movie_id, false)`） |
| 6 | Recognition Rather Than Recall | 2 | 数据模型说明全在 `title` 上（`panel.tsx:178`、`:329`、`:830`），触屏与键盘都拿不到；手机端「面板」禁用时 `MobileNavItem` 连 `title` 都没有（`home.tsx:279-291`），只在 `opacity-40` 里变灰；去设置要自己记住字段在第 8 步里的哪一步 |
| 7 | Flexibility and Efficiency | 1 | `api.tasks` 已支持 `state` 与 `q`（`lib/api.ts:283-291`），`fetchDashboard` 只传 `page`（`home.tsx:311`）：无搜索、无状态筛选、无批量、无快捷键、无总数；分页命中 `hasMore = tasks.length >= pageSize`（`panel.tsx:123`），满页的最后一页会让「下一页」永远可点 |
| 8 | Aesthetic and Minimalist | 3 | 克制、真实：无阴影、无装饰、彩色只作状态；但四张卡同权重（最不急的循环健康与需要人工一样大），且需要人工在 30px 内把自己的总数说了两遍（`:436-450` 徽章 + `:430-433` 句子） |
| 9 | Error Recognition & Recovery | 3 | 回执落在出事那一行并带语气（`notice={notices[item.movie_id]}`，`:478`），判决是人话（回到加工开头 / 重新加种，回到下载中）；缺口：`labelResult` 的 `showHash` 从没被传 `true`（`:744`、`:748`，形参默认 `false`），多版本分流的承诺是死代码 |
| 10 | Help and Documentation | 2 | 向导每一步的说明是真帮助（`wizard.tsx:60-190`）；面板侧没有任何帮助面，只有 hover `title`，术语表与 `docs/` 从不从界面可达 |
| **Total** | | **24/40** | **Acceptable（60%）** |

上界 40（Operate 面，十条全部适用，无 n/a）。

## Design Specificity Verdict

**外壳是品类通用件，行内解剖不是。** 统计卡以上的东西全部可以整块搬到任何 Grafana-lite / Beszel 式后台：208px 竖栏 + 两个导航项（`home.tsx:130-146`）、一个写着「面板」的 `<h1>`、一行四个计数器、四张一模一样的白卡；原子是未改动的 shadcn（`components/ui/card.tsx`、`badge.tsx`、`button.tsx`），品牌标记是一个通用 Lucide `Sparkles`（`home.tsx:126`、`:159`）。全页最大的字是「面板」——一个零信息量的词；而全页唯一属于这个产品的对象「需要人工」的行，正文只有 13px。反过来，抄不走的是那行的解剖：番号 + 病因徽章（`刮削失败` / `整理失败` / `下载停滞` / `副作用驻留`）+ 正文色的原因 + 次级墨的补法 + **按病因推导出来的动作集**（`remedy.ts` 对宿主机问题一颗重试都不给）；同样抄不走的还有任务行里的版本 chip 群（中文 失败 / 破解 失败 同排出现，`panel.tsx:962-990`）与点名三条真实循环的「循环健康」。结论：内容模型、文案与动作规则是自己写的；构图、层级与身份默认。约 60% 的骨架可以不改一个字地用在别的后台，但「需要人工」里没有一行可以。

**确定性扫描**：`impeccable detect --json web/app` → `[]`，exit 0；分目录再扫仍为 `[]`。扫描器活性已证：把 `panel.tsx` 复制到 `/tmp` 并注入一个反模式后报 `gradient-text` + `ai-color-palette`（带 `file:line`），目录递归也报。已知边界：同一次注入里的 `text-[9px]` 没触发 `tiny-text`——`.tsx` 走的是正则规则，元素级规则（对比度、字号、内边距）只能在浏览器里跑，所以「CLI 干净」不等于「页面干净」。

**浏览器浮层**：live-server 起在 8400，静态 serve 预构建的 `web/build/client` 于 8612，chromium 在 1440×950 与 390×844 各扫一遍。注入预检通过（改 title、插 script 均生效），检测器挂上后在**面板 + 抽屉**状态下报 **1 条**：`low-contrast`，实测 **3.7:1（需 4.5:1）**，元素 `span.text-muted-foreground/80`，位置 **`web/app/components/panel.tsx:149`**——就是「上次读取 14:42:05（刚刚）」里的「（刚刚）」。浮层这次真的画出来了（2 个 `.impeccable-overlay` 节点，标签 `LOW CONTRAST TEXT`），所以之前的零发现是内容干净，不是注入失效。裸向导与错误态因后端未运行、数据为 HTTP 层 stub，只覆盖到壳（真实组件 + 编造数据），这一点在下面算作限制，不算覆盖。

## Overall Impression

一轮真实的进步：上一版最狠的两条（重试永远没法成功的失败、面板不知道自己有多旧）都改了，而且改得对——宿主机问题现在不给按钮，新鲜度现在自己走钟。剩下的问题从「设计错了」变成「接线没接完」：康复路径的按钮指向了错误的一步，队列的行按版本切、动作按任务发，页头那个要人 glance 的数字自己跟自己不一致。最大的机会只有一个：让「去 X 设置」真的落到 X，并把队列按番号收成三组，这页就从「说了正确的话」变成「一步到位」。

## What's Working

1. **补救模型（`remedy.ts` + `AttentionRow`）**：动作按病因选，不按子系统。宿主机问题给「查看任务」和一句「重试不会变好」并**拒绝**给重试；设置问题把「去 X 设置」提为描边主操作、把「重发」降为幽灵；上游错点名 JavDB。它消灭了运维界面里最常见的那种假动作（一颗按了也会再失败的按钮），并且用 `extractPath` 从 errno 里揪出真正出事的目录，而不是改写成「出错了」。
2. **新鲜度（`panel.tsx:145-166` + `lib/time.ts` + `useNow`）**：客户端时钟算的「上次读取 hh:mm:ss（N 分钟前）」，每 60 秒自己走一格，没数据就说「还没读到过」而不沿用旧值。过期的值班台和健康的看上去一模一样，这是最便宜的解法，而几乎没人做。
3. **任务行里的版本 chip 群（`VersionChip`，`panel.tsx:962-990`）**：两级聚合不点进去就看得到（中文 失败 / 破解 失败 在同一行），失败把「失败」两个字写进胶囊，状态同时进 `aria-label`——色盲和触屏都不吃亏。

## Priority Issues

### [P1] 「去 Telegram / Jellyfin / JavDB 设置」和「去集成设置」全都落到向导第 1 步
- **What**：`panel.tsx:575-581` 渲染「去 {settingsLabel(remedy.settings)} 设置」，`:367` 渲染「去集成设置」，两者都调 `onOpenWizard`，签名是 `() => void`（`:66`）；`home.tsx:232` 只做 `setRequestedView("wizard")`；`Wizard` 挂载时 `React.useState(0)`（`wizard.tsx:194`），而步骤顺序是 javdb(1)…telegram(7)。`lg` 以下步骤条被 `hidden`（`wizard.tsx:281`），手机上连跳转都没有。
- **Why**：这是产品唯一的康复回路（「驻留的通知不会自动禁用集成，人要看着这一行去改哪一格」）。按钮报出一个目的地，送达另一个，再收六次「下一步」的过路费；在手机端——那条 PRODUCT 说他会用来清队列的设备——代价是穿过六个他不能碰的表单。
- **Fix**：`Wizard` 收 `initialSection?: SectionName`，把 `remedy.settings` 映到步号，由 `onOpenWizard(section)` 传下去；步骤条在每一档宽度都可见（可折叠不算）；再带上来源 `movie_id`，返回面板时滚到并闪一下那一行。
- **Suggested command**：`/impeccable shape`

### [P1] 队列的行是版本粒度的，动作是任务粒度的
- **What**：`failed.map` 的 key 是 `failed-${item.infohash}`（`panel.tsx:465`），但按钮 POST `retry(item.movie_id, false)`（`:486`），回执也按任务存（`notice={notices[item.movie_id]}`，`:478`）。KV-328 的两个版本都失败时：两行、两颗做同一件事的「重试」、同一张回执印两遍。
- **Why**：产品自己的论点是「版本各自独立走路」；界面把它压成一个含糊单位——修的是「这次失败」还是「这部作品」，按之前没人知道；两行长成一个样又把待办量放大一倍。抽屉里同样错位：`VersionCard` 用 12px 等宽把 32 位 infohash 放在最前面（`:1280-1295`），真正的判别词（中文 / 破解 / JavDB 磁力 / M-Team 种子）反而是最暗的一行，而「重试」会分三条不同分支，解释却写在任务级页脚的一段散文里。
- **Fix**：按番号分组，版本行缩进其下；一个任务级动作并把爆炸半径写进标签（「重试全部 2 条版本」）；回执在多版本时用已经写好却没人调的 `shortHash()` 分流（`labelResult(result, true)`）；失败 / 停滞 / 驻留收成三个带计数的分组标题。抽屉里让判别词打头、hash 降成 8 位前缀（值进 `title`），把分支写到每颗按钮旁边（「重试 = 回加工开头」），对 qBittorrent 还在下的版本禁用——那是「永不重下」规则。
- **Suggested command**：`/impeccable distill`

### [P2] 页头那个「需人工」数，和 30px 下的卡片数，算法不一样
- **What**：统计条读 `counts.needs_attention`（`panel.tsx:108-110`）；服务端把它算成失败**任务** + 驻留副作用（`internal/task/store.go:403`，`internal/server/overview.go:79`），停滞完全不计。卡片的 `total = failed.length + stalled.length + stuck.length`（`panel.tsx:412`）是失败**版本** + 停滞 + 驻留。同屏渲染就是 3 对 4。分歧是常态：任何一部片有普通/中字/破解两版失败，或任何一个种子停滞，就会撞上。
- **Why**：他 glance 的那个数在回答「队列在变长吗」，而它自称两个值。
- **Fix**：一个真相源。要么统计条直接用卡片那三个数组算，要么两者改名（统计条「需人工任务」、卡片「件待办」）。更省的做法：统计条删掉「需人工」这一项——卡片已经用更大的字说了同一件事，并把首屏第一块让给它。
- **Suggested command**：`/impeccable polish`

### [P2] 142 条里找不出一部片，而 API 早就支持搜索与筛选
- **What**：`api.tasks({ state?, q?, page? })` 齐备（`lib/api.ts:283-291`），`fetchDashboard` 只传 `page`（`home.tsx:311`）。任务卡头的 `CardAction` 是空的（`panel.tsx:175-182`），分页只有上一页/下一页，页大小硬编码 50，没有总数；下一页靠长度启发式（`panel.tsx:123`）可点，落到空页时弹的是新装引导文案「还没有任务。在 JavDB 的想看清单里加一部有磁力的片」（`:918-926`）——在一个跑满 142 条的库里。
- **Why**：真实回访只有两种：「SSIS-002 去哪了」和「只看需人工」。今天唯一的工具是翻页，而翻页会用一个「全新安装」的空态骗他一次。
- **Fix**：搜索框 + 状态筛选 chip 放进任务卡的 `CardAction`（DESIGN 的 One-Action 位），debounce 进 `q`，改条件把页号归 1；显示「共 N 条」并在短页上禁用下一页；给 `EmptyTasks` 分出「筛不到」和「库里真没有」两套文案。
- **Suggested command**：`/impeccable layout`

### [P2] 检测器实测：新鲜度那行的「（刚刚）」只有 3.7:1；解释性文案全在 hover 里
- **What**：`panel.tsx:149` 把 `text-muted-foreground/80` 套在已经 12px 的 `text-muted-foreground` 上，在纸白上合成 `#828282`，实测 3.7:1（需 4.5:1）——跑的是浏览器里的真实渲染，不是猜测。同一批问题还包含：数据模型说明只存在于非交互 div 的 `title`（`panel.tsx:178`、`:329`、`:830`），键盘与读屏都取不到；手机端「面板」禁用时只靠 `opacity-40`，`MobileNavItem` 压根没有 `title` prop；版本 chip 只用边框色区分失败/入库，`下载中` 与 `加工中` 视觉上完全一致；绝对时间只在 `title` 里。
- **Why**：PRODUCT 的地板是「键盘可达、语义正确、对比度可用」——这条正好踩线；而新鲜度是 DESIGN 自己立的规则，它的解释文字是页面上最读不清的字。
- **Fix**：删掉那层 `/80`（或换 `text-foreground/70` 并复核到 ≥4.5:1）；把 `title` 里的说明搬进视觉隐藏的 `span` 或用 `<abbr>`/`aria-describedby`；`MobileNavItem` 补 `title` 与屏幕可读的一句话；版本 chip 补短状态词而非只换边。
- **Suggested command**：`/impeccable audit`

## Persona Red Flags

**Alex（Power User，兼本产品唯一的用户）**：142 条里没有搜索也没有状态筛选，尽管 `api.tasks` 两条都支持；没有任何批量动作——4 条驻留通知要按 4 次「重发」，5 个失败任务要按 5 次「重试」，没有「把 remedy 判定安全的一次性重试」。没有键盘加速器：没有 `/` 搜索、没有 j/k、没有 `r` 刷新，抽屉的 Esc / 焦点陷阱是唯一的键盘福利。「下一页」由 `tasks.length >= pageSize` 启用，一满页就永远能点，落到空页看到的是新装引导文案——一次「打包装好的错误结果」，他会开始不信这个分页器。页大小写死 50，也没有总数，他连列表有多长都说不出。

**Sam（键盘 / 读屏 / 200% 缩放）**：全部解释性文案都在非交互 div 的 `title` 里，读屏拿不到、键盘也唤不出；手机端禁用的「面板」是 `opacity-40` 的白底灰字，既低于 4.5:1 又零解释，桌面对应物的唯一解释挂在一个 `disabled` 按钮的 `title` 上。版本 chip 只有失败/入库才改边框色，低视力用户在 200% 缩放下看到三颗一样的长相。时间是 `title`-only（`panel.tsx:210`、`:277`），那个唯一精确的坐标触摸屏和键盘都够不着。`<tr onClick>`（`:244-247`）配 `cursor-pointer` 许诺了整行可点，键盘用户却没有这条路——DESIGN 禁止在行上挂 `role="button"` 是对的，但整行点击仍是鼠标专属福利。

**作者-操作员（PRODUCT 里那个人：走廊里单手拿手机，或 23:00 在笔记本前）**：他改不完 Telegram——「去 Telegram 设置」送到第 1 步，`lg` 以下步骤条不显示，六个「下一步」穿过 JavDB / qBittorrent / Jellyfin 的表单，每一步旁边还各有一颗「保存本步」。他答不出「我不在时发生了什么」：两天未打开与两分钟前打开的观感完全一样，只有绝对计数和「上次读取（刚刚）」，`loadedAt`（`panel.tsx:145`）是应用对他上一次访问的唯一记忆，而它每次加载都重置。他要 glance 的那个数自己打架。他被要求去宿主机处理 `/media/jellyfin/jav/KV-328`，而那个路径是嵌在一段中文正文里的纯文本，没有复制按钮——他正单手拿着手机，需要把路径粘到另一台机器的 SSH 里。

## Minor Observations

- `重发`（`panel.tsx:590`）与 `立即重发`（`:876`）是同一个 POST 的两个名字。
- `已试 N 次` 在副作用队列里是 Badge（`:849`），在需要人工行里是普通元信息（`:556`）。
- 循环健康里有一行叫「副作用」（`:341`），紧挨着一张叫「副作用队列」的卡——同一个名词，两种范围。
- `labelResult` 的 `showHash` 形参从没被传 `true`（`:744`、`:748`），注释承诺的多版本分流永远不会执行。
- `formatWhen` 超过 7 天返回 19 字符绝对时间（`lib/time.ts:48-53`），在固定 8rem 的「更新」列里会撞进箭头列——1440×900 的渲染上 ABP-999 可见。
- 加载失败时错误条（`home.tsx:199-208`）没有 `onClose`，而 spinner 是独立的 `!snapshot` 分支，两者会同时永久占据屏幕，只有一颗「重试」。
- 首次加载期间 `view` 已经是 wizard（`home.tsx:100`，`initialized` 默认 false），于是页头先断言「初始化向导 / 未初始化」，正文才说「正在读取设置…」——升级页面会在第一秒自我打脸。
- `text-[0.8125rem]` 在 `panel.tsx` 出现 23 次、行内动作类名 10 次；DESIGN 的「`md` 以下 h-9 / 13px」只以散文存在。一个 `RowAction` 组件（或一个 Button size）才能让规则可执行。
- 向导步骤按钮用 `rounded-md` + `bg-accent`（`wizard.tsx:293-296`），与 DESIGN 的导航规格（10px、白底、1px ring）不一致——面板侧栏守了，向导没守。
- `Sparkles` 是品牌标记（`home.tsx:126`、`:159`），一个通用 Lucide 字形，别处一律意味着「AI / 新功能」，而这个产品唯一的身份资产是 `favicon.ico`。
- 抽屉遮罩是 `bg-foreground/20`（`panel.tsx:1081`），20% 下去面「需要人工」的红字仍在抢眼（抽屉渲染图里可见）。这是 DESIGN 规定的值——值得重议，而不是重写。
- 全站没有 URL 状态：`routes.ts` 只有一条 index 路由，当前视图、向导步号、打开的任务都既不可书签也不可刷新恢复。
- 检测器的一处漏报（事实，非意见）：注入的 `text-[9px]` 没触发 `tiny-text`，所以 CLI 干净只能说明「没有格式化反模式」。
- 资源体量：JS 451 KiB 原始 / 145 KiB gzip（entry 219 KiB 是最大头），CSS 38 KiB / 8 KiB gzip，5 个 woff2 只实际请求 1 个。单人自托管下不成问题，列在这里只是为了让「干净」可核。

## Questions to Consider

1. 如果值班台的理想状态是安静，为什么全清状态还完整渲染四张卡？空队列时把整页收成一行「没有要动手的事 · 上次读取 3 分钟前」，其余卡片有事才出现，会失去什么？
2. 如果集成设置初始化之后其实只是在改一格字段，为什么它还是八步向导？八段可寻址的分区（`/settings/telegram`）会让每一颗「去 X 设置」落在它点名的字段上。
3. 版本本来各走各的路，为什么唯一的手动动作是任务级的？一个版本优先的队列——失败/重试/重新加种就长在出事那个版本旁边——会把真相提前多少？
4. 如果统计条那颗「需人工」换成「自你上次之后 +2」，你会失去什么？绝对计数就在下面一张卡里。
5. 对机器你选了「认 id 不认名字」。对眼睛呢——按番号分组、版本列在下面是更快，还是一张第二列是 12px UUID 的表更快？
6. 如果那行裸 errno（`permission denied: /media/…`）是页面上最诚实的一句话，为什么它是唯一不是中文的一句？「全中文」和「原样透出」谁赢——还是说它其实是个不该出现的第三态：既不是上游业务话术，也不是给人看的中文？
