-- 00015: 女优订阅（#47，规格 §4.5/§6.4，ADR-0014/0015，迁移 00015）。
--
-- 第二个待办来源。它只负责「产出一批待办作品」，之后与清单轮询走同一段 ingest
-- （定源 → 先落库 → 加种），所以本迁移只落订阅自己的形状：
--
--   - subscription：身份是 actor.javdb_actor_id（不是名字——名字只是快照，改名不该断
--     订阅），带水位线、回看窗口、回灌下界与启停。行**永不删除**：面板只有停用/恢复，
--     task.subscription_id 的外键与历史因此永远可解。kind 是上位词留下的扩展位
--     （GLOSSARY「订阅」/ADR-0015：将来的番号订阅、全站标签订阅加在这里，不另起表）。
--   - task.subscription_id：与 list_id 并存的「首次发现来源」另一半。订阅建的任务
--     list_id 为 NULL（没有清单项可删，故 list_removal 天然不触发），订阅身份记在这里。
--     写入后不覆盖：来源是「首次发现」，不是「最近一次发现」。
--   - app_config 的两个订阅旋钮：subscription_interval 是本票的独立节拍（默认 15m，
--     与清单轮询同拍）；subscription_max_adds 是每轮限量的列（默认 10），本票只建列
--     不读它——限量归 #49。
--
-- subscription_skip（跳过/收下，ADR-0017 的三条判据）不在这里，它是 #48 的迁移 00016。

-- +goose Up

-- 订阅：一位女优一条（UNIQUE (kind, actress_actor_id)：同一位女优不会订两遍）。
--
-- actress_actor_id 的外键立得起来的前提是建订阅时 upsert 一行 actor：name 取上游、
-- gender/avatar_url 先 NULL 等刮削补（规格 §4.5「actor 词义扩宽」）。
--
-- watermark_date 可空：NULL 只应出现在手工改库的场景，读方按「建订阅当天」兜底。
CREATE TABLE subscription (
    id               bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind             text        NOT NULL DEFAULT 'actress' CHECK (kind IN ('actress')),
    actress_actor_id text        NOT NULL REFERENCES actor (javdb_actor_id),
    watermark_date   date,
    lookback_days    integer     NOT NULL DEFAULT 30 CHECK (lookback_days > 0),
    backfill_from    date,
    enabled          boolean     NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind, actress_actor_id)
);

-- 「首次发现来源」的另一半（规格 §4.1）：与 list_id 并存、同为首次发现语义，
-- 写入后不覆盖；两列可以同时有值（同一部作品既在清单又在某位女优的新作里）。
ALTER TABLE task ADD COLUMN subscription_id bigint REFERENCES subscription (id);

-- 「带来 N 部」与订阅循环的逐条遍历都按这一列查。
CREATE INDEX task_subscription_id_idx ON task (subscription_id);

-- 订阅循环的独立节拍（规格 §4.3/§6.2）：app_config 是单行设置，值由向导最后一步改。
ALTER TABLE app_config ADD COLUMN subscription_interval interval NOT NULL DEFAULT '15 minutes';

-- 每轮限量（规格 §6.4「节流与去重」）：本票只建列，读它的是 #49。
ALTER TABLE app_config ADD COLUMN subscription_max_adds integer NOT NULL DEFAULT 10
    CHECK (subscription_max_adds > 0);
