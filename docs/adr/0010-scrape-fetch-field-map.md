# ADR-0010: 刮削取数——字段图、图片 URL 与「阻断 ≠ 缺失」

- 状态：已接受（#27；细化规格 §5.3/§7.1，验收 P9-1/P9-2/P15-1）；#30 修订：复用判据不数译文
- 背景：规格 §7.1 定了「哪一列归哪个源、怎么回退」，但几处需要实现期落定：
  1. `task` 只有标题/简介/日期/时长/片商/系列/导演/评分/标签这些强类型列，**没有图片列**（§4.1），而整理票（#29）需要图片 URL；
  2. `title_ja` 的回退写的是「JavDB 标题」，而 JavDB 详情同时给 `title`（常是繁中译名）与 `origin_title`（日文原题）；
  3. 规格写 `publisher` 归「JavDB label」，而 JavDB 详情里对应的字段叫 `publisher_name`（片商/发行两列在 JavDB 是 maker/publisher）；
  4. 规格只写 `rating` 归 JavDB，但 JavDB 的分值是 5 分制，而 NFO 的 `<rating>` 是 10 分制（参考仓 open-mdcng/mdc-lite 同样是 ×2）；
  5. DMM 的 pl/ps 几何未实测（§5.3/§13 的悬案），而取数段并不下载图片。
- 决策：
  - **图片的值落在 provenance**：`poster` / `fanart` 记同一组横版 sleeve 候选（DMM 的 `pl`/`ps`；DMM 缺则 JavDB `cover_url`），`extrafanart` 记预览集（DMM `jp-N`；缺则 JavDB `preview_images`）。`raw_value` 就是 URL 列表，整理票从 provenance 读；不新增图片列。
  - **几何探针归整理票（#29）**：取数只留候选 URL，不下载、不量宽高；首次 JP 出口刮削时在真实 JPEG 上做一次 `curl + file`，把「pl/ps 谁横」记进整理段。
  - **`title_ja` 回退优先 `origin_title`**：这一列是「日文原题」（NFO 的 `<originaltitle>`），拿 JavDB 的繁中译名填它会让后续 AI 翻译（#30）译中文到中文。`title` 只作第二顺位。
  - **`publisher` = JavDB `publisher_name`**：JavDB 与 DMM 的 maker/label 命名不同，字段图按 JavDB 的 publisher 面取；它缺失就留空，**不**反向拿 DMM 的 label 补（唯一回退方向 DMM→JavDB）。
  - **`rating` 落 10 分制**：JavDB 的 `score`（如 `"4.43"`）×2；provenance 的 raw_value 保留上游原字符串。0/缺省不写，而不是写 0 分。
  - **DMM 内容单按完整契约解析**：除字段图要用的标题/简介/图片外，日期、秒级时长、女优/导演/片商/标签/5 分制评价都解析成类型。字段图只取 DMM 归属的那几列，其余的用途是「契约漂移可见」与后续票（如 DMM tags 若要启用），并把原文一并留在 raw_snapshot。
  - **DMM 的失败不是「缺失」**：只有「搜不到 / `ppvContent: null`」才按 DMM 缺失走字段级回退；地区阻断、GraphQL `errors`、schema 错、连不上都是**故障**，排退避重试并留 `task_event` 告警。否则一次出口配错会把全库元数据静默降级成 JavDB。
  - **复用判据 = provenance 非空**：刮过（哪怕上游什么都没给）就算有元数据；新版本 0 次网络，只留在 processing 等整理（GLOSSARY「复用元数据」）。判据检查放在抢租约之前，命中时连租约都不拿，整理票随时可以接上。
- 后果：
  - 图片的「值」与「来源」合一，重刮时 raw_snapshot 仍能对比；整理票只读 provenance/raw_snapshot，不猜上游。
  - 取数失败**每次**留一条 `task_event`（尝试史要看得见），退避用 attempts/next_attempt_at；**「三次落 failed + Telegram ×1」的裁决归聚合的整理票（#29）**——同一条版本上还有整理段的失败要一起算，本段不做终局判定。
  - 单版本即时通道（webhook）只做「下载完成 → processing」（#25）；加工推进挂在 reconcile 的「推进 processing」上（§6.2），所以一条版本从完成到取数最多等一个对账周期。
  - DMM 无日本出口的开发环境里取数会持续退避失败，这是部署前提（§13），不是本段的缺陷；`mdc_upstream_requests_total{upstream="dmm"}` 归指标票（#32）。
  - 规格见 §5.3/§7.1。
- 修订（#30）：`title_zh`/`outline_zh` 是 AI 翻译的产物，**不进复用判据**——判据写作 `(provenance - 'title_zh' - 'outline_zh') <> '{}'`。理由：一条只有译文的任务仍然需要取数，否则「先翻译、后取数」或「翻译补译」会把整段刮削跳过。同一处落定：**翻译不是取数**——复用判据命中仍是 0 次 JavDB/DMM 调用，但 AI 翻译启用而译文缺失时会补一次翻译请求（规格 §5.7），失败同样只降级 + 一条告警，不计 attempts。
