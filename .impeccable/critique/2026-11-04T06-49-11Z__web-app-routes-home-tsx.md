---
target: the panel (web/app/routes/home.tsx)
total_score: 22
max_score: 40
na_heuristics: 
p0_count: 2
p1_count: 3
target_identity: "file:/root/clone/oh-my-mdc/web/app/routes/home.tsx"
target_fingerprint: "sha256:64ed68161470900a5c759663337d2e2583a3fa1e8343dfb91b1016864eddd27f"
target_path: /root/clone/oh-my-mdc/web/app/routes/home.tsx
timestamp: 2026-11-04T06-49-11Z
slug: web-app-routes-home-tsx
---
# critique · 面板（值班台）

Target: `web/app/routes/home.tsx` + `web/app/components/panel.tsx`（Operate 面）

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 2 | 全页没有「上次读取」时间戳；侧栏「循环 运行中」与「循环健康 → 副作用 失败」同时在屏；从「集成设置」返回面板不重新取数（`useEffect` 只依赖 `[apply, page]`） |
| 2 | Match System / Real World | 3 | 术语基本咬得住（下载/刮削/整理、上游错原样透出），但抽屉脚注写「按存的 payload 重新加种」，泄了英文列名，`GLOSSARY.md` 明令避免 |
| 3 | User Control and Freedom | 2 | 「强制重刮」无条件可点、无二次确认，回滚已入库版本的 raw snapshot；点遮罩关闭抽屉会连正在进行的重试回执一起销毁；成功路径下焦点回不到原行 |
| 4 | Consistency and Standards | 2 | 「查看任务」（只读）与「重试」（写操作）同形同尺寸；任务状态用三件套徽章，版本状态只靠 1px 边框色 + `title`；「提前重试」是固定按钮词汇之外的第五个动词 |
| 5 | Error Prevention | 2 | 对 `Telegram 403` / `JavDB 同名清单` / `permission denied` 一概给「重发 / 重试」，这几个按钮注定再失败一次；`useTaskRetry` 只有一个 `busy` 字符串，连点两行会二次 POST 并覆盖上一张回执 |
| 6 | Recognition Rather Than Recall | 2 | `AttentionItem.at` 有字段却从不渲染，行上看不出「放了多久」；回执用 8 位 infohash 指认版本，靠记忆对齐；修 Telegram 要自己想起 chat id 在「集成设置 → Telegram」，行里没有链接 |
| 7 | Flexibility and Efficiency | 2 | `api.tasks({state,q})` 已实现但界面不传：142 条无搜索、无状态筛选、无总数、无批量重试；页号与抽屉都在内存里，F5 即失 |
| 8 | Aesthetic / Minimalist | 2 | 层级倒置：最决定动作的失败原因是最小最灰的 12px；五段 14px 静态「自解释文案」占着黄金位置 |
| 9 | Error Recognition & Recovery | 3 | 诊断很强（phase 徽章 + 上游原话 + 逐版结局 + 时间线），回收很弱（回执在卡片顶部、不可关闭、不指向出事的行） |
| 10 | Help and Documentation | 2 | 唯一的帮助是元文案；没说「第 N 次」按什么间隔、停滞多久才算停滞、强制重刮会动 NFO/图片的哪些东西 |
| **Total** | | **22/40** | **Acceptable（55%）** |

适配上界 40（Operate 面，十条全部适用，无 n/a）。

## Design Specificity Verdict

**LLM 评估**：产品语言与诊断是原创的，外壳是通用后台。四态词汇贯穿表格/抽屉/时间线，上游错原样透出（`媒体库目录不可写：permission denied (/media/movies/MIDE-872)`、`Telegram 403: bot 不在该 chat 里`、`JavDB 「您已创建了同名清单」`），番号与 `movie_id` 双标识、人读的那层在上——这些换到任何后台都活不了。反过来，四张统计卡（`已入库 142` 不是决策）、五段自解释的 `CardDescription`、`第 1 页 · 每页 50 条`、恒等于「已初始化」的页头徽章，是全站最像模板的部分。删掉统计卡与静态说明，这页才从 dashboard 变成值班台。

**确定性扫描**：`impeccable detect --json web/app` → `[]`，exit 0；15 个文件逐文件扫也是 `[]`；关掉 config / inline-ignore / design-system 仍为 `[]`。同一二进制对故意做坏的临时文件报 24 条（html）与 2 条（tsx，带 `file:line`），证明扫描真的在跑——干净是真的干净，不是没跑。注意覆盖边界：`.tsx` 只走正则规则，`tiny-text` / `low-contrast` / `cramped-padding` 这类元素规则须在浏览器里跑。

**浏览器浮层**：live-server 起在 8400，`addScriptTag` 注入 `detect.js` 成功（10 个 hook 就位），1440×950 与 390×844 全页各 **0 findings**，抽屉打开重扫也 **0**。零发现时 `detect.js` 不绘制任何浮层，`[Human]` 页里看不到标记是预期行为，不是注入失败。注入活性已在同页验证：塞入一段坏 DOM 后 `impeccableScan()` 当场报 `GRADIENT TEXT` + `AI COLOR PALETTE` 浮层，`impeccableDetectAsync()` 报 `1.2:1 (need 4.5:1)` 与 `letter-spacing: 0.20em`。结论：本页无格式化反模式，问题全在层级、语义与状态表达上——正是审查该抓的那层。

## Overall Impression

这是一个人写的、知道自己要什么的值班台，但它把「解释自己」和「报告状态」的预算弄反了：五段 14px 文案在教人读数据模型，真正决定动作的失败原因却是全页最小最灰的一行字。最强的部分是「需要人工」这一张卡：红环、动态句、上游原话、行内动手。最大的机会是承认「清队列」是这页唯一的活——让每一行按补救方式给动作，而不是按子系统给「重试」。

## What's Working

1. **队列就是界面，而且允许沉默。** 一张卡、一圈红环、一个计数、一句会从「5 件要动手的事…」切换成「没有要动手的事…」的说明；空态整页安静且正确，没有弹窗、没有 toast 风暴。
2. **诊断胜过装饰。** `整理失败` / `刮削失败` / `下载停滞` / `副作用驻留` 带 phase；逐版结局说清做了哪件事（`回到加工开头`、`重新加种，回到下载中`）；UI 从不把失败磨平成「出错了」。
3. **墨色 + 发丝线在真实密度下站得住。** 全页唯一的彩色是四组状态 chip；`142` 用墨色 24px `tabular-nums`；分隔全靠 1px；唯一的阴影留给抽屉。12px 下限全站守住，字号全用 rem，`prefers-reduced-motion` 减弱而非消灭转圈。

## Priority Issues

### [P0] 「需要人工」的行只能重试，而多数行的问题重试解决不了
- **What**：`SSIS-150 · 发 Telegram`（`Telegram 403: bot 不在该 chat 里`）与 `ABP-978 · 删清单项`（`JavDB 「您已创建了同名清单」`）给的是「重发」；`IPX-556` 那行自己写着「qbt 里还在下就不会重下」却给了「重试」；`MIDE-872` 是 `/media` 权限问题，重试必然复现。
- **Why**：这是打开面板的唯一理由（PRODUCT：回到面板时是在清「需要人工」队列）。现在它告诉你病因、扣着药，唯一的按钮保证再病一次。
- **Fix**：按补救方式分行，而不是按子系统：可重试（流水线）→ 重试；需改集成（Telegram 403 / token）→ 文字链「去 Telegram 设置」调 `onOpenWizard`；需去上游（同名清单、M-Team 404）→ 外链 + 把重试降为幽灵；需宿主机（permission denied）→ 不给按钮，只给一行「要在宿主机改 /media 权限」。不能重试的行不许出现重试。
- **Suggested command**：`/impeccable clarify`

### [P0] 面板不知道自己有多旧，全局状态灯还自相矛盾
- **What**：`loopLabel()` 只要任一循环有 `last_run_at` 就报「运行中」，于是侧栏「循环 运行中」与两屏下的「副作用 … 失败」同时在屏；循环健康把「成功」「失败」渲染成同一种 `text-muted-foreground`，没有 token、没有图标；统计卡与队列没有 as-of 时间戳；`home.tsx` 的 `useEffect` 只依赖 `[apply, page]`，所以「面板 → 集成设置 → 面板」不保存就不会重取，改完 Telegram 回来看到的还是改之前的队列。
- **Why**：值班台的新鲜度未知比直接报错更糟——你分不清「还没好」和「这是旧快照」，而两个全局指示灯正好在最需要答案的时候互相拆台。
- **Fix**：把新鲜度放到眼睛起点：面板头一行 `上次读取 14:42:05 · [刷新]`（把「刷新」从任务卡挪出来）；`loopLabel` 以失败为准（`运行中 · 1 条循环失败`）；循环健康改用三件套 token，并补上间隔与下次时间（`清单轮询 · 每 60s · 下次 14:43:05 · 成功`）；`useEffect` 认 `view` 或在返回路径里显式 `refresh()`。
- **Suggested command**：`/impeccable harden`

### [P1] 字号层级倒置，页面在解释自己而不是在报告
- **What**：`AttentionRow` 的失败原因是 `text-xs` + `text-muted-foreground`（12px / 0.5），而上面五段静态说明是 14px；行内按钮是 h-7/12px；页头副标题、任务卡说明、「同目标只排一条…」都在讲数据模型；页头 `已初始化` 徽章在面板视图里恒为真。
- **Why**：手机上，两行副标题 + 需要人工说明 + 任务说明吃掉约 5 行，第一个状态才出现；而「原因」——决定你按哪个按钮的那句话——是产品里最不清楚的字。DESIGN.md 自己把 12px 留给徽章与时间戳。
- **Fix**：原因升到 13px `Dense` 的 `text-foreground`，phase 交给已有徽章；删掉四段静态说明（移进卡片标题的 `title`）与页头副标题，只留需要人工那句动态句；`md` 以下行内动作 h-9 + 13px；面板视图去掉 `已初始化` 徽章。
- **Suggested command**：`/impeccable typeset`

### [P1] 版本状态只在 hover 里，破坏性动作没有护栏
- **What**：`VersionChip` 把状态塞在 `title` 属性里，下载中与加工中的胶囊长得一样，失败只差一圈红边；`MIDE-872` 在表里显示需人工，两个 chip 都不说哪个版本坏了。`DrawerActions` 无条件渲染「重试 + 强制重刮」、同等权重、无确认，连两个版本都在下载中的任务也给。
- **Why**：表格要回答的两个问题就是「哪个版本」「按哪个」，一个只在 hover 里（触屏与读屏都拿不到），另一个是能把已入库版本的 raw snapshot 拉回加工开头的不可逆动作。
- **Fix**：版本 chip 放进三件套 token + 胶囊里加短状态词（或 6px 前端点），并给 `aria-label`；`强制重刮` 只在存在 `finished` 版本时可用，且先出一行内确认（说清会把哪几条版本拉回加工开头、旧快照进事件）；`重试` 保持墨色实心，`查看任务` 降为幽灵或让番号本身成为入口。
- **Suggested command**：`/impeccable polish`

### [P1] 手机与 200% 缩放会丢状态、缩热区
- **What**：`lg` 以下的卡片行没有「更新」时间（`md:table-cell` 那格在列表行里没有对应物）；行按钮的 `aria-label={`打开 ${number} 的详情`}` 把状态徽章与版本 chip 的文本一起吞掉，读屏只听到「打开 SSIS-834 的详情」；行内动作与分页是 h-7/12px，低于自家 `MobileNavItem` 的 44px 让步；抽屉打开时 `inert` 只包住 `<Panel>`，`home.tsx` 的侧栏与移动顶栏在 20% 遮罩后面仍可点可 Tab。
- **Why**：手机是两个清队列的场景之一，现在是清队列最差的地方：没有可用来分诊的时间戳、状态不被朗读、28px 目标、「模态」能点出去。
- **Fix**：卡片行补 `更新` 一行；`aria-label` 带上状态与版本数，或干脆去掉 `aria-label` 改视觉隐藏 span；`md` 以下动作升到 h-9/13px；`inert` 提到包含 `<aside>`/`<header>` 的外层，或让抽屉走 portal 并把整个外壳 inert。
- **Suggested command**：`/impeccable adapt`

## Persona Red Flags

**Alex（Power User）**：一张卡里 8 个同形 h-7 按钮，「查看任务」与「重试」像素级相同，肌肉记忆必错。连点两行「重试」会二次 POST 并覆盖回执（`busy` 是单值）。无 `r` 快捷键、无 roving tabindex、无批量重试——三次失败=三次穿越 2 个导航项 + 8 个按钮。分页只有上一页/下一页（142 条、无总数、无搜索、无状态筛选，尽管 API 支持），刚好 50 条时「下一页」可点、落到空页再被 `apply()` 弹回第 1 页。F5 丢页号与抽屉。

**Sam（Keyboard / 读屏 / 200% 缩放）**：焦点序从跳转链接开始要连过 10 个停靠点才到表格。重试成功的回执是 `role="status"`，但内容是「已下发：aabb1100 回到加工开头」——没有番号、没有行关联，随后整行消失也没有 `aria-live` 播报，读屏用户无法确认自己是否成功。抽屉的 `role="dialog" aria-modal` / 焦点陷阱 / Esc / 锁滚动都在，但虚拟光标仍能走到遮罩后面的导航。200% 缩放下宽度跌破 `md`，表格变卡片行，唯一的新鲜度数据（更新）消失。`VersionChip` 的状态在不可聚焦的 `title` 里，读屏永远拿不到。

**作者-运维者（单人、笔记本或手机清「需人工」）**：能一眼看清 5 件事和上游原话，但答不出「STARS-512 挂了多久」——`AttentionItem.at` 从不渲染，只有停滞行把时长写进了 `reason`。按「重试」后重跑、再失败、行回到队列，除了顶部一张写哈希的红条没有新信息；面板从不说「这类失败要在宿主机上改权限」——而他本人正是那个能改的人。抽屉里想追「为什么又失败」，看到的是 `状态变化 / 取数完成，开始整理 / 下载中 → 下载中`（空转）与 `人工重试 / 人工重试：回到加工开头`（标签套标签），恰好在最需要信任它的时候磨损时间线。收尾是灰色的「失败」、矛盾的侧栏、一个文字链接，没有收束节拍。

## Minor Observations

- 所有时间都是秒级绝对时间（`2026/11/4 14:29:16`）；分诊要的是「3 小时前」+ hover 出绝对值。
- `第 {page} 页 · 每页 50 条` 永不显示总数：`共 142 条 · 第 1/3 页` 一行算术的事。
- 需人工统计卡的 hint 写「失败待重试与驻留副作用」，漏了停滞，但计数里含停滞。
- 副作用行写 `第 2 次`（`attempts + 1`）没有单位与间隔；动作用了固定词汇之外的「提前重试」。
- `Notice` 有 `onClose` 却从不传，回执不可关闭，`space-y-2` 里还会叠加三条，手机上把行挤出屏幕。
- 空态「下一轮轮询就会出现在这里」没有下一轮时间；旁边的「刷新」只重读 API 不触发轮询，新手按两次仍是空。
- 空态仍渲染整条分页栏（`第 1 页 · 每页 50 条` + 两个禁用按钮）——全新安装上的死家具。
- 循环健康只覆盖三个循环，第四条通道（qBittorrent 完成 webhook）没有健康表示，webhook 静默死掉要等对账补。
- `Sparkles` 作品牌标记没有产品含义，也没有品牌承诺。
- 表格列宽无人管理：1440 下「版本」吞掉约 600px 富余，状态与番号贴左。

## Questions to Consider

1. 如果「需人工」是这页的全部意义，为什么前 120px 属于四个计数？把 `142 已入库 · 3 下载中 · 1 加工中` 压成一行 13px 会让页面更差，还是只是更不像 dashboard？
2. 失败原因是宿主机上的 `permission denied` 时，「重试」是什么意思？一个按钮解决不了的失败类型，面板该给按钮，还是该把「这一类要人去宿主机」当成它最值钱的那句话打出来？
3. 「强制重刮」会把已入库版本的 raw snapshot 覆盖回加工开头，它离「重试」8px、同款描边、没有确认。如果设计者预期自己会误触一次，这里会长什么样？
4. 面板已经旧了 20 分钟而屏幕上没有一句话说这件事，这比直接 500 更好还是更糟？侧栏那个「运行中」是给谁看的，既然两屏下的循环健康不同意？
5. 为什么这页要用五段 14px 讲自己的数据模型——是抽屉解释不了自己，还是作者从来不必冷读这页？
6. 既然重试的结局是逐版的，回执为什么是卡片顶部一个 8 位哈希，而不是改动的那个版本下面一行字？人拿着 `aabb1100` 能做什么？
