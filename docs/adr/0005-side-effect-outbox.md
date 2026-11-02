# ADR-0005: 旁路副作用统一进 side_effect 出箱表

- 状态：已接受（#8 + 事后修订：部分唯一索引；#13 细化重试分流）
- 背景：删清单项、刷 Jellyfin、发 Telegram 不是任务状态，失败不该阻塞入库，且需幂等与合并。
- 决策：单表 side_effect（kind, target_key, payload, attempts, next_attempt_at, done_at），部分唯一索引 UNIQUE(kind,target_key) WHERE done_at IS NULL（已完成不封死将来的重复触发）；jellyfin 刷新天然 coalesce；notify 以 event:infohash 幂等。瞬时错退避重试、永久错立即驻留（需人工），绝不自动禁用集成。
- 后果：入库与副作用解耦；驻留语义统一（done_at IS NULL AND next_attempt_at IS NULL）驱动需人工区与指标。规格见 §4.2/§5.6。
