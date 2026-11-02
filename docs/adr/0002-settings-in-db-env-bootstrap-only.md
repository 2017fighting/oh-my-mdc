# ADR-0002: 用户设置在 DB，env 只做 bootstrap

- 状态：已接受（#8；#12 修正 env 清单，砍掉 MDC_DATA_DIR）
- 背景：PLAN.md 希望设置优先读环境变量；但「设置没有第二条覆盖路径」才能让向导成为唯一事实源。
- 决策：六张集成表（行存在＝已配置）+ 单行 app_config + list_registry；env 只有 DSN/监听（全部带默认值）。向导按表 PATCH，敏感列只吐掩码，DB 明文。程序自己写的时间戳不进用户设置表。
- 后果：零 env 覆盖路径，配置审计单点；home-ops 侧 SOPS 只剩 CNPG role 密码一枚。规格见 §4.3/§8.4。
