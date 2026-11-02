# ADR-0001: Postgres only，砍掉 SQLite

- 状态：已接受（#8，推翻 PLAN.md 与地图初稿的「开发用 SQLite」）
- 背景：单用户自托管；open-mdcng 为双方言付出 dialect.go + 两套后端测试，收益为零。
- 决策：只支持 Postgres（生产 CNPG，开发仓内 compose + CI 服务容器）。pgx/v5 + goose embed 启动 Up 只前进，Up 前 advisory lock。不做跨库搬迁。
- 后果：无方言接缝；开发需起一个 compose PG；open-mdcng 的 `/* identity */` 双方言迁移机制不复用。规格见 `docs/spec.md` §2/§4。
