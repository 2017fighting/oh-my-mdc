---
target: critique 向导
total_score: 33
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 2
target_identity: "file:/Users/raincore/clone/oh-my-mdc/web/app/routes/wizard.tsx"
target_fingerprint: "sha256:01db18e9a7cf6ce4721a59271723d53f52e25eefc079ad46333f689945a7081a"
target_path: /Users/raincore/clone/oh-my-mdc/web/app/routes/wizard.tsx
timestamp: 2026-11-05T13-44-36Z
slug: web-app-routes-wizard-tsx
---
# Critique · 八步初始化向导（web/app/routes/wizard.tsx）

Method: dual-agent (A: wizard-review-a · B: wizard-review-b)

Run Notes：A 路经 bsk 真实 Chromium 核验（后端不在本机，用页面级 fetch mock 驱动完整八步旅程，15 张截图，含 570px 窄屏）；回执文案与掩码形状可能与真实后端有出入。B 路 CLI 扫描 + 浏览器注入均零发现。所有临时服务已停止，无项目文件被改动。

## Design Health Score

| # | 启发式 | 分数 | 关键问题 |
|---|-----------|-------|-----------|
| 1 | 系统状态可见 | 4 | 门禁计数只在保存时刷新，后端侧真相变化不会自己走 |
| 2 | 贴近真实世界 | 4 | 无实质扣分；上游原话原样上屏 |
| 3 | 用户可控与自由 | 3 | 草稿无法单字段撤销；误输入只能刷新清空 |
| 4 | 一致性与标准 | 3 | 测试按钮五个名字 vs 固定词「测试连接」；GateStrip「去 X」可指自身 |
| 5 | 错误预防 | 4 | Test-Commits-First；revision 冲突自愈；autorun 两段式 |
| 6 | 识别而非回忆 | 3 | qbt↔Jellyfin「同一 host 目录」跨步约束只在单侧提示 |
| 7 | 灵活与效率 | 3 | 每节可深链、默认值预填；无键盘加速器（一次性流程，可辩护） |
| 8 | 美学与极简 | 3 | 首屏五颗琥珀「!」；短步分隔线下 ~40px 空档 |
| 9 | 错误识别与恢复 | 3 | 机器码→人话；浏览器原始英文错误（"Failed to fetch"）不翻译 |
| 10 | 帮助与文档 | 3 | WIZARD_CONFUSABLES 选得准；第 3–7 步帮助够不着 |
| **总分** | | **33/40** | **Good（28–35）**，全部十条启发式均计分，无 n/a |

## 设计专属性判词

**LLM 评估：长在这个产品上，不是通用后台模板换皮。**「先落库后动手」翻译成了交互（Commit-On-Next / Test-Commits-First 字面实现）；「认 id 不认名字」长在表单里（探测/一键代建/推荐三路径）；门禁语义是骨架（requirements() 单一来源、Gate-Once、Finish-Receipt）。错失的半步：完成回执是全旅程唯一「机器接班」时刻，只有 12px 绿字挂在长卡片最底部。

**确定性扫描：零发现。** CLI `[]`（exit 0）；浏览器 5 视图 6 次注入扫描全部 No anti-patterns found。检测器未抓到 LLM 漏掉的东西；LLM 抓到的语义层问题（同名链接、焦点、触控目标）不在检测器扫描范围。无误报。

**视觉叠加层：** 注入成功，[Human] 标签页跑了 5 个视图，零反模式无可高亮；标签页已关闭。视图 2–5 为 mock 数据驱动渲染；无后端真实渲染（Unavailable 兜底页）亦检测过，干净，空态是正式错误卡而非白屏。

## 亮点

1. Test-Commits-First 教科书级落地：先保存再测、结论跟 revision 走、改一格即收旧结论。
2. Finish-Receipt + Gate-Once 组合：「完成初始化」永远可按、回执说清跑什么、没齐列链接；「按不着的灰按钮」反模式被拆掉。
3. 脏可见三层结构：字段「未保存」→ 动作行承诺 → 脏关页拦截；lib/draft 按「节+revision」跨步不丢。

## 优先问题

1. **[P1] 门禁卡五个「去补」链接同名**（CheckLine aria 可访问名全是「去补」，读屏链接导航不可用）。Fix：`aria-label={\`去补 ${item.short}\`}`，零成本。→ /impeccable audit
2. **[P1] JavDB Token 获取路径零指导**（第一步唯一字段不教「怎么导出」，全向导/术语/GLOSSARY 均无）。Fix：hint 半句导出路径 + WIZARD_CONFUSABLES 第五条。→ /impeccable clarify
3. **[P2] 步进导航后焦点不落位**（navigate() 换节焦点掉回 body，读屏每步迷路）。Fix：节切换 effect 聚焦 main#main 或新卡 h2。→ /impeccable audit
4. **[P2] Telegram 未启用时 4 个通知开关照常全亮**（为没启用的集成先做偏好决策；7 块成最重一屏）。Fix：关闭时收成一行或禁用+半透明+hint。→ /impeccable distill
5. **[P2] 探测行/推荐按钮/页脚「术语」触控目标 <44px**（DESIGN 自己规定 md 以下 h-11；清单选择恰恰发生在手机上）。Fix：min-h-11、套 ACTION、inline-flex min-h-11。→ /impeccable adapt

## 人物旅程红 flags

- **Jordan**：token 导出无指导是唯一硬卡点；qbt 步第一字段要求懂 svc DNS 而解释块在 6 字段后（placeholder 默认值救命）；「媒体库目录不在盘」没有指向能看挂载的方法；清单 id 概念被三路径完全包掉（超额完成）。
- **Sam**：五个同名「去补」；换步焦点不落位；「完成初始化」说明只在 title。其余过关：aria-current="step"、sr-only 步骤状态、role=switch、aria-describedby/invalid、模态焦点管理+inert 计数、全程真控件无键盘陷阱、对比度实测达标。
- **Casey**：探测行/推荐 ≈28px；第 3–7 步「术语」入口在页脚视口外、手机无第二入口；autorun 命令 pre 横滑与纵滑打架无溢出提示；打断恢复过关（草稿、拦截、44px 主操作、16px 输入）。

## 次要观察

1. [P3] GateStrip「去 X」可指向当前步自身（点击无反应）：同节时不渲染链接。
2. 测试按钮五变体名 vs 固定词：在 DESIGN 记「测试按钮按被测语义变尾字」。
3. wizard.tsx 头注释「已改」vs UI「未保存」——注释漂移。
4. 分隔线到首字段 ~40px 空档，短步显空。
5. 「还差 0 项」不可达文案：加防御句「屏幕看不出差哪项，刷新一次」。
6. 门禁计数只在保存时刷新；4/5→5/5 无活区播报。
7. StepChain 居中算法第 8 步实测正确，别动。
8. 完成步「暂停循环」开关可加「保持关闭」半句。

## 值得想的问题

1. 八步固定顺序 vs 门禁驱动顺序：真正的模型是「五个门禁缺口」，线性只是投影；要不要按「还差的第一项」重排（可选三步沉底）？
2. 清单步借 JavDB 的绿色结论（chip 钥匙是 javdb 节 revision）：将来「探测到但登记没生效」会不会被绿胶囊掩盖？「已登记 #k4EVE4」是清单步天然的结论位。
3. 「未保存」字段粒度 vs 提交表粒度：单人自托管下无害；两标签页同节冲突成为常态时要不要升格为字段清单？
