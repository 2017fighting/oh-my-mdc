# ADR-0009: 「已初始化」看配置就位，不看测试通过

- 状态：已接受（#19）
- 背景：规格 §4.4 说主线三件套（JavDB 可读且清单登记就位、qBittorrent 可连、Jellyfin 可写）齐了才算 initialized；#19 的验收要求「主线三件套被删 → initialized 变 false 回向导」。两种读法：把 `last_test_ok` 当门禁，或把「行存在」当门禁。
- 决策：`initialized` = `javdb` 行存在 **且** `list_registry` 有 `purpose='want_watch'` **且** `qbittorrent` 行存在 **且** `jellyfin` 行存在。`last_test_*` 只是诊断账本（向导 chip 与排障），不进判据。
- 理由：
  1. 测试是用户主动触发的采样；上游一次抖动不该把已跑起来的系统踢回向导、锁掉面板。
  2. #19 交付的框架里六节测试还是占位（`not_implemented`）；若以测试为准，三件套永远无法「已初始化」，门禁与向导都无从验证。
  3. 凭据失效有它自己的信号：§9 的 `mdc_upstream_requests_total{result="permanent"}` 与「需要人工」区，不需要门禁替它报警。
- 后果：第一次配齐三件套就开门，真实可达性由各集成自己的首次调用与告警暴露。集成票落地后若要收紧，把 `last_test_ok` 加进 `isInitialized` 即可（一处改动）。
