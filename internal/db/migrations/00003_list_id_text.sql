-- 00003: list_registry.list_id 由 integer 改为 text（规格 §4.3，实测修正）。
--
-- 00002 照规格写成了 integer，但 #16 的 live probe 实测发现 JavDB 的清单 id 是
-- 6 字符 nanoid 风格的**字符串**（如 "e8r1OM"、"k4EVE4"），建清单端点
-- `POST /api/v1/lists` 回的 `data.id` 也是字符串。整数列装不下它，
-- 「一键建清单 → 拿 data.id 登记」这条验收（P2-2）就走不通。
--
-- 迁移只前进（ADR-0001）：这里用 ALTER 而不是改 00002——已经跑过 00002 的库
-- 需要一条能升级的路径。int → text 必须显式 USING；已有行的整数值变成等价的
-- 十进制文本，不会丢。
--
-- 判定清单永远靠 id（规格 §5.1）：类型变了，语义不变。task.list_id（#23）后续
-- 建表时按 text 外键对齐这里。

-- +goose Up
ALTER TABLE list_registry
    ALTER COLUMN list_id TYPE text USING list_id::text;
