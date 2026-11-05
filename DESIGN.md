---
name: oh-my-mdc
description: 把 JavDB 想看清单变成 Jellyfin 条目的单人值班台
colors:
  ink: "oklch(0.205 0 0)"
  ink-foreground: "oklch(0.985 0 0)"
  paper: "oklch(1 0 0)"
  text-ink: "oklch(0.145 0 0)"
  surface-muted: "oklch(0.97 0 0)"
  text-muted: "oklch(0.5 0 0)"
  hairline: "oklch(0.922 0 0)"
  focus-ring: "oklch(0.55 0 0)"
  danger: "oklch(0.577 0.245 27.325)"
  status-info-surface: "oklch(0.977 0.013 236.62)"
  status-info-text: "oklch(0.443 0.11 240.79)"
  status-info-border: "oklch(0.901 0.058 230.902)"
  status-progress-surface: "oklch(0.987 0.022 95.277)"
  status-progress-text: "oklch(0.414 0.112 45.904)"
  status-progress-border: "oklch(0.924 0.12 95.746)"
  status-done-surface: "oklch(0.979 0.021 166.113)"
  status-done-text: "oklch(0.432 0.095 166.913)"
  status-done-border: "oklch(0.905 0.093 164.15)"
  status-danger-surface: "oklch(0.971 0.013 17.38)"
  status-danger-text: "oklch(0.505 0.213 27.518)"
  status-danger-border: "oklch(0.885 0.062 18.334)"
typography:
  title:
    fontFamily: "Geist Variable, PingFang SC, Microsoft YaHei, Noto Sans SC, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.5rem"
    fontWeight: 600
    lineHeight: 1.3333
    letterSpacing: "-0.025em"
  headline:
    fontFamily: "Geist Variable, PingFang SC, Microsoft YaHei, Noto Sans SC, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1rem"
    fontWeight: 500
    lineHeight: 1.375
  body:
    fontFamily: "Geist Variable, PingFang SC, Microsoft YaHei, Noto Sans SC, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.4286
  dense:
    fontFamily: "Geist Variable, PingFang SC, Microsoft YaHei, Noto Sans SC, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Geist Variable, PingFang SC, Microsoft YaHei, Noto Sans SC, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 500
    lineHeight: 1.3333
  data:
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, Liberation Mono, Courier New, monospace"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: 1.625
rounded:
  sm: "6px"
  md: "8px"
  lg: "10px"
  xl: "14px"
  pill: "26px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "24px"
  2xl: "32px"
components:
  button-primary:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.ink-foreground}"
    typography: "{typography.body}"
    rounded: "{rounded.lg}"
    height: "32px"
    padding: "0 10px"
  button-primary-hover:
    backgroundColor: "oklch(0.205 0 0 / 80%)"
  button-outline:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.text-ink}"
    typography: "{typography.body}"
    rounded: "{rounded.lg}"
    height: "32px"
    padding: "0 10px"
  card:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.text-ink}"
    typography: "{typography.body}"
    rounded: "{rounded.xl}"
    padding: "16px"
  badge-status:
    backgroundColor: "{colors.status-done-surface}"
    textColor: "{colors.status-done-text}"
    typography: "{typography.label}"
    rounded: "{rounded.pill}"
    height: "20px"
    padding: "0 8px"
  input:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.text-ink}"
    typography: "{typography.dense}"
    rounded: "{rounded.lg}"
    height: "32px"
    padding: "0 10px"
  nav-item:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.text-ink}"
    typography: "{typography.body}"
    rounded: "{rounded.lg}"
    height: "36px"
    padding: "0 12px"
  table-row:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.text-ink}"
    typography: "{typography.dense}"
    padding: "10px 16px"
---

# Design System: oh-my-mdc

## Overview

**Creative North Star: "值班台"（The Quiet Duty Desk）**

这是一个人的值班台：默认安静，只有「要人动手」的事被推到最前面，其余信息按可扫描的密度排开。所有强调都来自墨色——近黑的按钮、当前视图、表格里的番号；彩色不是品牌的，是信标灯，只在四个版本状态和三种测试结论上亮起。页面底是 30% 的灰，卡片是白的，两者之间只隔一圈 1px 的淡墨，全站唯一的阴影留给右侧抽屉。

这与产品的失败模型一致：绝大多数时间无事发生，一旦出事就必须在几秒内看出是哪部片、卡在哪一步、按哪个按钮。界面不追求表达，追求「不误读」。中文是主语言，西文与数字落在 Geist 上，汉字回落到系统黑体，两者同一字面高度。

**Key Characteristics:**
- 注意力排序固定：需要人工 → 任务 → 副作用 → 循环健康。
- 近黑墨色是唯一强调色；彩色 100% 用于状态语义。
- 状态不靠颜色单独承载：失败要写「失败」两个字，色只是补一层信号。
- 时间默认说「多久以前」；绝对时间进 `title`。
- 深度靠发丝线，不靠阴影；抽屉是唯一的例外。
- 12px 是字号下限；表格与表单元信息落在 12–13px。
- 圆角三档分工：控件 10px、卡片 14px、徽章胶囊。
- 全中文文案，四个状态词与五个按钮词是固定词汇。

## Colors

一套近无彩的纸墨体系，加四组低彩度的状态对；没有第二个品牌色。

### Primary
- **墨色 Ink**（`oklch(0.205 0 0)`，`--primary`）：主按钮、当前导航、抽屉主操作。每屏最多一次。
- **纸白 Paper**（`oklch(1 0 0)`，`--background` / `--card`）：卡片、输入框、抽屉。
- **文字墨 Text Ink**（`oklch(0.145 0 0)`，`--foreground`）：标题与正文。

### Neutral
- **暖灰底 Muted Canvas**（`oklch(0.97 0 0)`，`--muted`）：页面底以 30% 叠在白上（≈`#f5f5f5`），表格头与内嵌块用它。
- **次级墨 Muted Ink**（`oklch(0.5 0 0)`，`--muted-foreground`）：说明、时间戳、表头、页脚。选 0.50 而不是 0.556：灰底上要有 4.5:1。
- **发丝灰 Hairline**（`oklch(0.922 0 0)`，`--border` / `--input`）：分隔线、输入框边。
- **焦点灰 Focus Ring**（`oklch(0.55 0 0)`，`--ring`）：2px 焦点环，白底 4.7:1。

### Secondary
- **信号蓝 Info**（`oklch(0.977 0.013 236.62)` / `oklch(0.443 0.11 240.79)` / `oklch(0.901 0.058 230.902)`）：下载中。
- **信号琥珀 Progress**（`oklch(0.987 0.022 95.277)` / `oklch(0.414 0.112 45.904)` / `oklch(0.924 0.12 95.746)`）：加工中、待加种、待强制重刮、警告提示。
- **信号翠 Done**（`oklch(0.979 0.021 166.113)` / `oklch(0.432 0.095 166.913)` / `oklch(0.905 0.093 164.15)`）：已入库、测试通过、操作成功。
- **信号红 Danger**（`oklch(0.971 0.013 17.38)` / `oklch(0.505 0.213 27.518)` / `oklch(0.885 0.062 18.334)`）：需人工、副作用驻留、测试失败、错误。

### Named Rules
**The Ink-Only Rule.** 近黑墨色是唯一强调色。想加一个彩色品牌色，先改这套系统，不要在某个页面上先斩后奏。

**The Signal-Only Color Rule.** 彩色只允许出现在状态上：四个版本状态、三种测试结论、告警。彩色不用于装饰、标题、图标或分区。

**The Three-Part State Rule.** 每个状态是三件套：`-surface` 底、`-text` 字、`-border` 边。禁止只换文字颜色，或者拿 Tailwind 调色板直取。

## Typography

**Display Font:** Geist Variable（回落到 PingFang SC / Microsoft YaHei / Noto Sans SC / system-ui）
**Body Font:** 同上，同一套字面
**Label/Mono Font:** `ui-monospace, SFMono-Regular, Menlo, Consolas`

**Character:** 一个无衬线家族贯穿全站，西文与数字用 Geist，汉字交给系统黑体；等宽只用于「标识」——番号、infohash、movie_id、token、命令串。

### Hierarchy
- **Title**（600，24px，1.333，-0.025em）：页面标题。全站每页只有一个。
- **Headline**（500，16px，1.375）：卡片标题；抽屉标题放大到 18px/600。
- **Body**（400，14px，1.4286）：导航、说明、按钮、正文。帮助文案限制在 ~72ch。
- **Dense**（400，13px，1.5）：表格单元格、表单标签、行内动作标题、「需要人工」里的失败原因（用 `text-foreground`，不是次级墨）。
- **Label**（500，12px，1.333）：徽章、测试 chip、时间戳、页脚。
- **Data**（400，12px，1.625，等宽）：番号、infohash、token 掩码、命令。

### Named Rules
**The 12px Floor Rule.** 正文与元信息不低于 12px；12px 只给徽章与最次的时间戳，表格与表单不降到 12 以下。字号一律用 rem，用户调大默认字号时必须跟着放大。

**The Mono-for-Identity Rule.** 等宽字体只承载可逐字符核对的标识与命令，不用于「显得技术」的普通文案。

## Layout

外壳宽 1152px（`max-w-6xl`），四周 24px；桌面左侧 208px 竖导航，`md`（768px）以下换成顶部品牌行 + 两格 44px 视图切换。左栏内层 sticky（`top-6`，高度 `100dvh-3rem`）：滚过一屏之后导航与循环心跳还在，不能变成一片白。主列纵向节奏 24px，卡片内 16px（小卡 12px），表单项之间 12px，标签与控件之间 6px。

面板的区块顺序是产品约束，不随屏幕改：**页面标题**（就是那个数：`8 件事需要人工` / `没有要动手的事`，24px/600）→ 统计条（`下载中 3`、`加工中 2` 两组，13px，间距与统计行同族）→ 需要人工 → 任务 → 副作用队列 → 循环健康。统计条与「已入库 141 + 上次读取 hh:mm:ss（N 分钟前）+ 刷新」同一行、左右两端。首屏第一块必须是需要人工，不是计数器：全站最大的字留在这句话上，「面板」这个词不再是标题（位置由导航说明）。任务区在 `lg`（1024px）以上是稠密表格（番号 / 状态 / 版本 / 更新 / 抽屉箭头，`table-fixed` + 列宽 11rem / 6.5rem / 自适应 / 8rem / 2rem），以下改成卡片行（番号 + ID + 版本 chip + 更新 + 状态 + 箭头）；卡片行必须带「更新」，否则手机会丢掉唯一的新鲜度。分页条只在真有任务时出现。

**The Ordered-By-Cost Rule.** 统计行只放「还在动」的数（下载中 / 加工中）；已入库是永远不会要人动手的那个数，降级到右端与新鲜度同处，不加粗、不占第一行。窄屏（`lg` 以下）侧栏是藏起来的，同一个统计行右侧接上循环心跳（`清单轮询 每 15 分钟 · 2 分钟前`、`对账 …`）——它是「我要不要再等等」的答案，不能等滑到文档底部才出现。心跳只取两条时，失败的那条循环排最前（`failing` 优先排序）：三条里唯一会带失败进面板的副作用循环，不能被固定的 slice 顺序恒定掉。

**The Nothing-To-Do Rule.** 「需要人工」卡无事可做、且没有刚做完的回执时整块不渲染：标题已经说了「没有要动手的事」，再留一张空卡只是给首屏加一段家具。

**The Freshness-In-First-View Rule.** 面板必须自己说出它有多旧：「上次读取」用客户端时钟算，相对时间每 60 秒自己走一格；找不到数据就说「还没读到过」，不沿用上一次的值。

**The Fixed-Row Rule.** 「需要人工」与副作用队列的行是「正文列 + 动作列」：`sm` 以下竖排（文字占满整行，动作落到下一行），`sm` 起横排并右对齐——不能让 `shrink-0` 的按钮把失败原因挤成 150px 宽的窄条。

向导宽 1024px（`max-w-5xl`）；`lg` 起是 230px 步骤条 + 表单，`lg` 以下步骤条隐藏，位置由「第 N / 8 步」徽章与上一步/下一步承担。抽屉固定右侧，最大 512px，全高，窄屏占满。

**The Remedy-First Rule.** 「需要人工」里四个分节的顺序就是「我在这里能不能修」：能在这里修（可重试的失败）→ 停滞（先确认 qBittorrent）→ 要去别处修（宿主机/上游问题）→ 驻留副作用。修不动那组的补法默认收成一行 `重试不会变好 · 展开原因`——那句带 `/media/...` 路径的全文只在展开后出现。

**The One-Voice Rule.** 屏幕上的那个数（`N 件事需要人工`）由它自己的常驻活区播报，构成行与回执不再抢第二个声音；回执的活区常驻在面板上（空内容也在树上），因为带内容挂载的活区不会念。

**The One-Action Rule.** 每张卡片只留一个主操作位（右上 `CardAction`）。卡片头是两列网格，动作放 `CardAction`，不要用 `flex-row` 覆盖它。

## Elevation & Depth

平面系统。卡片与页面底之间只有一圈 1px 的 `ring-foreground/10`；表格行、队列行、版本行之间用发丝线分隔，不叠盒子。全站唯一的阴影是右侧抽屉的 `shadow-2xl`，配合 20% 墨色遮罩；抽屉打开时背景 `inert`，页面不滚。

### Shadow Vocabulary
- **抽屉浮层**（`0 25px 50px -12px rgb(0 0 0 / 0.25)`）：仅在 `role="dialog"` 的抽屉上使用。

### Named Rules
**The Hairline Depth Rule.** 分层靠 1px 线与背景色差，不靠阴影。除抽屉外任何元素出现阴影，都算新加了一层不存在的结构。

## Shapes

圆角按角色分三档：控件（按钮、输入、行内动作）10px；卡片 14px；徽章与 chip 2.6×基础半径的胶囊（26px）。表格本身无圆角，靠卡片裁切。分隔线恒为 1px，不用左侧/右侧彩色边条制造强调。图标全部来自 Lucide，统一 1.5px 描边、12–20px，不用 emoji 或字符代替。

**The Pill-Means-Status Rule.** 胶囊形只用于状态与计数徽章；普通标签用 8px 方块圆角，避免「什么都是胶囊」。

## Components

### Buttons
- **Shape:** 10px 圆角，高度 32px（默认）或 28px（小），左右 10px。行内动作用小号；`md` 以下一律抬到 44px，热区优先。
- **Primary:** 墨底纸字；每屏一个主操作（抽屉底部「重试」）。
- **Hover / Focus:** hover 80% 墨；焦点 2px `--ring` 环，2px 偏移；按下位移 1px（减弱动效时取消）。
- **Secondary / Ghost / Link:** 描边按钮用于「保存本步」「测试读取」「刷新」「重发」等第二动作；幽灵按钮用于上一步/跳过；文字链只用于「去集成设置」。禁用态透明度 50%。

### Chips
- **Style:** 高度 20px，12px/500，胶囊；底色 + 同族深字 + 1px 同族边。
- **State:** 四个版本状态用四组状态 token；版本 chip 最长 112px，超出省略；**失败的那条把「失败」写进胶囊**，入库用绿色描边；状态同时进 `aria-label`，不只进 `title`。
- **Rule:** 颜色从不单独承载状态——红绿对色弱等于没有，hover 的 `title` 对触屏等于没有。

### Cards / Containers
- **Corner Style:** 14px。
- **Background:** 纸白；页面底是 30% 灰。
- **Shadow Strategy:** 无；一圈 10% 墨的 ring。需要人工区在有事时换成 1px 红色 ring。
- **Border:** 无实线边。
- **Internal Padding:** 16px（小卡 12px）；表格内容区左右归零，由单元格自己留 16px。

### Inputs / Fields
- **Style:** 32px 高，10px 圆角，1px 输入框边，纸白底；13px 字（移动端 16px，避免 iOS 聚焦放大）。
- **Focus:** 边框转 `--ring` + 2px 环。
- **Error / Disabled:** 错误由区块级提示条承担（红色三件套，`role="alert"`）；禁用态透明度 60%。
- **Toggle:** 视觉 36×20，热区靠伪元素撑到 60×44；开是墨底，关是灰底。

### Navigation
- **Style:** 桌面 208px 竖排，项高 36px，10px 圆角；当前项白底 + 1px 淡墨 ring + 500 字重，未选中为次级墨。
- **Mobile:** 顶部两格分段控件，高 44px，当前项灰底，等价于桌面两项。
- **Semantics:** 按钮而非链接（视图状态在内存里），`aria-current="page"`；向导步骤条用 `aria-current="step"`。

### Signature Component
- **任务抽屉（Task Drawer）：** 右侧 512px 全高，portal 到 `body`，`role="dialog"` + `aria-modal`；打开时整个 `#app-shell` 置 `inert`（侧栏与顶栏一起），焦点进抽屉并在内部循环，Esc 关闭，关掉先撤 `inert` 再把焦点还给触发行（行已消失则退到 `#main`）；版本列表用发丝线分隔，事件时间线用小圆点 + 12px 相对时间。
- **需要人工的行：** 一行四段：身份（番号/待办名）+ 状态徽章 + 多久以前 / 失败原因（13px 正文色，全页最该被读到的一句）/ 补法（12px 次级墨，只在能说清「为什么重试没用」时出现）/ 按补救方式给的动作。
- **操作回执（`ReceiptList`）：** 卡片顶部一条浅灰带，一行一条：结局语气词（已下发 / 没成，终态绿、未成琥珀）+ 番号 + 刚刚发生什么 + 相对时间 + 收起按钮。它挂在卡片上而不是行里——重试成功后那一行会消失，回执是那一刻唯一的证据。留 3 条、活 10 分钟；行消失后焦点落在这里。

**The Remedy Rule.** 动作按「什么能治好这一行」决定，不按子系统：流水线问题给「重试」；设置问题先给「去 Telegram / Jellyfin 设置」再把重发降为幽灵；上游业务错说明要去 JavDB 处理；宿主机权限或空间问题**不给重试按钮**，只给「查看任务」和一句「重试不会变好」。判据在 `web/app/lib/remedy.ts`，认不出来一律回落到重试。

**The Honest-Aftermath Rule.** 按下去之后，屏幕说的必须与回执说的同一件事：行不消失的重试（停滞）成功后，行上给瞬时中间态——阶段徽章换「重新加种中」（progress 三件套）+ 一句「已重新加种（多久前），等下一轮对账确认后回到下载中」；回执只陈述已发生的动作与它的确认条件（「重新加种，等下一轮对账确认」），不预支屏幕下一秒兑现不了的结果。回执里多版本的逐版结论从第二段起换行，一段一句话，不当日志串。网关错误分两种死法：200 + 登录页教「登录然后刷新」；非 200（502/503/504）教「稍等再刷新页面，不需要重新登录」——绝不教人白重登。安抚句按面给：面板只读是真的，设置面可写，不能共用同一句。

**The Consequence-First Rule.** 不可逆动作先写后果再给按钮：「强制重刮」只在存在 `finished` 版本时出现，点击先出一行内确认条（琥珀 surface，说清会拉回几条版本、旧快照留在事件里），确认与取消都在条子里，不开对话框。
- **需要人工区：** 卡片内三组行（失败 / 停滞 / 驻留副作用），行间发丝线，动作按钮在行右，不弹对话框。

## Do's and Don'ts

### Do:
- **Do** 用四个状态 token（`--status-*`）表达下载中/加工中/已入库/需人工，三件套一起用。
- **Do** 把卡片动作放进 `CardAction`，保持标题—描述—动作的两列网格。
- **Do** 12px 起排版，全部用 rem；表格用 13px，表头与时间戳 12px。
- **Do** 失败原因用 13px 正文色的 `Dense`，不要用 12px 次级墨——它是决定按哪个按钮的那句话。
- **Do** 时间默认写「3 分钟前」「昨天」，绝对时间放 `title`；「上次读取」与相对时间一起走钟。
- **Do** 行内动作 `md` 以下 h-11 / 13px（44px 拇指热区），`md` 起才缩到 h-7 / 12px；相邻动作间距 ≥8px。
- **Do** 用发丝线分隔列表行；卡片里不再套卡片。
- **Do** 用 2px `--ring` 焦点环，覆盖按钮、行内动作、导航、步骤条与行内可点文字。
- **Do** 数字用 `tabular-nums`（番号、时间、计数、页码）。
- **Do** 中文优先字体栈，西文 Geist，汉字系统黑体。
- **Do** 空状态给下一步（「在 JavDB 的想看清单里加一部有磁力的片」）。

### Don't:
- **Don't** 加彩色品牌强调；彩色只作状态信号。
- **Don't** 复活深色主题：`.dark` 与 `dark:` 变体已删除。要加，先补切换入口并逐状态校对对比度。
- **Don't** 给卡片、行、按钮加阴影；阴影只属于抽屉。
- **Don't** 用 1px 以上的左/右彩色边条做强调。
- **Don't** 用 `flex-row` 覆盖 `CardHeader` 的网格，或把动作塞进标题 div。
- **Don't** 用 emoji、字符或 Unifont 图形代替 Lucide 图标。
- **Don't** 把彩色换成 Tailwind 调色板直取；状态色只有 token。
- **Don't** 用 9–11px 正文；也不要写死 px 字号。
- **Don't** 用颜色单独表达状态（红绿、只换边框色、只放 `title`）。
- **Don't** 对注定失败的失败给重试按钮，也不要为凑齐动作行而摆假按钮。
- **Don't** 用静态说明段讲数据模型：说明进卡片标题的 `title`，正文位置留给状态。
- **Don't** 在表格行上挂 `role="button"`；行保持 `row`，可点区域用单元格内的真按钮。
