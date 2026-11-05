---
target: the panel (web/app/routes/home.tsx)
total_score: 34
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 1
target_identity: "file:/root/clone/oh-my-mdc/web/app/routes/home.tsx"
target_fingerprint: "sha256:f8a30dcfebc70be366dcc5154ac1c4e0bd078b94282534f3b90f54b18708d743"
target_path: /root/clone/oh-my-mdc/web/app/routes/home.tsx
timestamp: 2026-11-05T03-32-42Z
slug: web-app-routes-home-tsx
---
# critique · 面板（值班台）第七轮

Target: `web/app/routes/home.tsx` + `web/app/components/panel.tsx`（Operate 面）

Method: dual-agent (A: assess-a7 · B: assess-b7)

上一轮 34/40 之后是 `6aaf464`（六修：停滞中间态、网关分诊、心跳失败排最前、焦点接回、冷加载一波、六项次要）。本轮命题是「验收六修」，并追一个第六轮就埋下的问题：诚实是前端的渲染期猜测，还是系统的口径？

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | 首次停滞的中间态+回执+h1 不变是全链路诚实；但中间态不过期也不认新停滞——行二次进入停滞名单后「重新加种中 · 已重新加种（刚刚）」挂在旧动作上（A/B 双向实测复现） |
| 2 | Match System / Real World | 4 | 「值班台暂时读不到」「会话没有失效，不需要重新登录」——产品声音，零黑话；502 不再教人白重登 |
| 3 | User Control and Freedom | 3 | 成功路径焦点接回原按钮；失败路径（catch）焦点掉 body——上轮 P2 的同类缺口在错误分支复活 |
| 4 | Consistency and Standards | 3 | 网关分诊、按面安抚、词族扩展一致；但成功回执有名有姓、错误条匿名无番号；回执说「等下一轮对账确认」而抽屉时间线说「版本回下载中」 |
| 5 | Error Prevention | 4 | remedy-first、防双击、确认条原封不动；网关分诊本身是防错 |
| 6 | Recognition Rather Than Recall | 4 | 「需人工」口径解释从 hover title 升级为选中即现，触屏终于摸得到这句话（13px/6.01:1 实测） |
| 7 | Flexibility and Efficiency | 3 | 冷加载 10→5 请求（实测）；依旧零快捷键、无批量 |
| 8 | Aesthetic and Minimalist | 4 | 中间态只是一句 12px 灰字+徽章换词；chips 横滑取代 4+1 折行；回执逐版换行 |
| 9 | Error Recovery | 3 | 单入口两种死法都给了对的第一个动作（六轮 P1 结案）；但重试失败的错误条不点名是哪一行 |
| 10 | Help and Documentation | 3 | 确认条教后果、口径行就地教学；停滞节常量提示对已重新加种的行是过去时错位（小） |
| **Total** | | **34/40** | **Good（上缘）**：与第六轮持平，但失分点从正门退到了侧门 |

上界 40（十条全适用）。34 的持平不是停滞：第六轮的失分在用户必经之路，这轮全部修掉（B：29+5 项机械检查全 PASS）后，失分点退到「重复停滞」「重试失败」这类二阶路径。地板抬高了，天花板还没动。

## Design Specificity Verdict

**六修没有引入一件品类家具——每句新文案都长在这台机器自己的骨架上；但这轮验收暴露了一个结构性事实：诚实是前端的渲染期猜测，不是系统的口径。**

**LLM 评估**（assess-a7）：网关的两种死法解释的是这个部署独有的架构事实（「这个应用自己没有登录页」）；安抚句按面板只读/设置可写分流。「重新加种中」取自流水线自己的动词，并入 下载中/加工中 的进行体词族，amber 三件套复用停滞 token——对四状态固定词汇是正确扩展而非破坏。口径解释行讲的是「一部作品多版本聚合」的状态模型。The Honest-Aftermath Rule 先写规则再实现：回执=已发生的事实+确认条件，行上=瞬时状态+预期归宿，h1=不动的行数——三句话没有一句预支未来。

**确定性扫描**（assess-b7）：CLI detect → exit 0、`[]`、0 条（regex 模式欠报声明 verbatim 存档）。活性对照升级：regex 11 抓 2；浏览器渲染扫描同一份 11 违规文件抓 10 条发现/9 种规则——页内全量检测因此有真读数。浮层注入 3/3 成功（busy 桌面/移动、gwbad），页内检测全部 No anti-patterns found——这次「无发现」有活性对照背书。

**实测补充**（assess-b7）：对比度无一处低于门槛（最低 muted/卡 6.01:1，重新加种中徽章 8.73，新口径行与注记行 12–13px/6.01）；无 <12px；标题零跳级；390 宽 24 个可交互元素 0 个 <44×44；25 Tab 停靠 0 无可见指示；三宽零横向溢出；chips 横滑单行且容器不产生额外 Tab 停靠点；抽屉唯一模糊阴影；60 字 movie_id 三处均不撑破容器；确认条开着 F5 中断干净。

## Overall Impression

六修全部落地且质量在线，第六轮两座 P1 正式结案：「重试落地」峰值时刻第一次全链路诚实，深夜错误页说了值班者最想听的话。但验收捅破了最后一层纸：中间态的真值由前端渲染期猜（reseeding 字典），而后端的停滞判据本来就读事件序——猜对一次容易，猜对所有往返做不到。峰值场景现在完美，峰值的返场（再停滞）在撒谎。下一个大机会是让 /api/attention 直接回答「这条停滞发生在最近一次重新加种之前还是之后」，让过期标记整类消失。

## What's Working

1. **The Honest-Aftermath Rule 逐字落地，三处各司其职**（panel.tsx:818 徽章换词、:854 行内补句、:1293-1295 回执措辞）：首次停滞的按下→回执→行上→h1→焦点五步实测全部诚实——七轮里最值钱的修复。
2. **网关分诊是一次架构级的产品化**（api.ts:222-234 + home.tsx:426-434 + :177）：六个实测场景文案全部准确；flap 的「屏幕上还是上一次的数据」是同类好句。
3. **失败优先的心跳没有破坏共享词汇**：桌面侧栏与手机统计行共用 loopLines 同源排序；sidefx 恢复后顺序回正（8816 实测）。

## 六修验证（commit 6aaf464，B 全部机械化 PASS）

| # | 修复 | 判定 | 关键证据 |
|---|---|---|---|
| ① | 停滞中间态 | ✅ PASS（首停滞） | 徽章→重新加种中、注记行、回执「重新加种，等下一轮对账确认」且回执无「回到下载中」；warn 路径无标记；行离开后中间态退场。⚠️ 重复停滞 FAIL → P1 |
| ② | 网关分诊 | ✅ PASS | 六场景（面板/设置 × auth/bad/appfail）文案准确；首钮刷新页面；错误态 h1=值班台暂时读不到 |
| ③ | 心跳失败排最前 | ✅ PASS | 8811 失败时副作用打头（桌面+390）；8816 恢复后回正 |
| ④ | 焦点接回 | ✅ PASS（成功路径） | 停滞行 settle 后 activeElement=data-retry 原钮；行消失路径去下一颗可按重试。❌ 失败路径漏接 → P2 |
| ⑤ | 冷加载一波 | ✅ PASS | /api 请求恰 5 个 |
| ⑥ | 次要×6 | ✅ PASS | 口径行可见、chips 单行横滑、60 字符三处不溢出、回执两段换行、时间线无「— →」 |

## Priority Issues

### [P1] 重新加种的中间态不过期，也不认「新的停滞」
- What：reseeding 只按 movieID 记一次成功时间，行离开停滞名单即隐没、再次进入即原样复亮——「重新加种中」+「已重新加种（刚刚）」指向一次早已确认过的旧动作（A sceneB3、B Riley 8819 双向实测）。行从未离开时同一句「等下一轮对账确认」挂到天荒地老。
- Why：六轮 P1 的镜像缺陷——屏幕在该警惕的时刻说宽心话。停滞过的种子最容易再停滞；后端明明知道真相（retry.go:393 停滞判据读事件序）。
- Fix：最小修在前端——reseededAtOf 加时序门（reseededAt < 当前停滞项 item.at 才亮）；根治在后端——/api/attention stalled 项带回「告警是否晚于最近一次 readd」。
- Suggested command: /impeccable harden

### [P2] 重试失败的分支没有焦点接回
- What：焦点交接只在 try 分支；catch 只 setError（panel.tsx:1091-1094），HTTP 失败实测焦点掉 body。副作用重发同构（:1243）。
- Why：纯键盘用户最容易遇到失败的队列恰恰最需要它。
- Fix：焦点接回挪进 finally（origin.isConnected && activeElement === body → origin.focus()），两处。
- Suggested command: /impeccable harden

### [P2] 失败的错误条匿名，成功的回执有名有姓
- What：taskRetry.error 的红色 Notice 只渲染上游原话（panel.tsx:531-535）；多行同屏时靠记忆对号。
- Why：回执系统的信用来自「每句话落在具体对象上」。
- Fix：错误信息带番号（调用方握有 plan.title），或错误条复用回执行形态。
- Suggested command: /impeccable clarify

### [P3] 同一次重新加种，回执与时间线两种说法
- What：前端说「等下一轮对账确认」，抽屉时间线渲染后端原文「版本回下载中」（retry.go:341）。
- Fix：EventRow 对 manual_retry && detail.action === "readded" 用前端 retryActionLabel 重写显示句（reason 进 title）。
- Suggested command: /impeccable clarify

### [P3] 390 下尾端 chip 焦点被容器裁切
- What：Tab 到「需人工」chip 时容器不横滚，chip 右缘越出 24px 被裁、焦点环同被裁（chips-focus-last.png）。
- Fix：容器 scroll-padding 或焦点时 scrollIntoView({inline:"nearest"})；右缘留白软化第一眼。
- Suggested command: /impeccable adapt

## Persona Red Flags

**Alex**：最快踩中 P1——「重试→刷新确认→再盯结果」动线的第三步正是旧标记的谎；失败重试焦点掉 body 打断键盘流；仍无 / 、单键重试、批量。
**Sam**：成功动线接近满分。剩余：失败重试焦点掉 body（P2）；错误条匿名；中间态徽章换词无播报（回执已说清，可接受）。
**23:00 值班运维者**：错误页救掉一次深夜白跑；心跳失败进首屏。但 P1 正是写给他的：那句「刚刚」可能属于一小时前的另一次事故。

## Minor Observations

1. flap 错误条「稍等一下再重试」与唯一按钮「刷新页面」时序相抵——立刻点必然再吃 502。
2. 停滞节常量提示对已重新加种的行是过去时错位；reseededAt 在场时可互换。
3. 中间态徽章无进行中视觉线索（与停滞同 amber）——12px Loader2 是候选（取舍题）。
4. 桌面表格截断的 movie_id 无 title。
5. 回执逐版换行靠 split("；")，reason 含「；」时会误拆；按 detail 结构化拆分更稳。
6. 筛「需人工」且为空时口径说两遍（筛选条下 + EmptyTasks），措辞不同义同。
7. 390 下「需人工」chip 右缘被裁约半字，首眼像渲染破损。

## Questions to Consider

- 中间态的真值该由谁说？前端渲染期猜，还是 /api/attention 直接回答，让过期标记整类消失？
- 「等下一轮对账确认」敢不敢把「下一轮大概什么时候」说出口（对账每 5 分钟侧栏写着）？
- 错误条值不值得拿到回执的同一套版式（成功有名有姓、失败无名无姓的不对称）？
- 心跳失败优先救了首屏，但 slice(0,2) 把「对账」挤出去——失败时容量该不该放宽到三条？
