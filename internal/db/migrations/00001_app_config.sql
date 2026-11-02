-- 00001: app_config —— 用户设置里那唯一一行应用级配置（规格 §4.3，ADR-0002）。
-- 设置域的其余表（六张集成表、list_registry、任务域…）由后续票据以新迁移追加；
-- 本文件只落骨架需要的那一行，用来证明「迁移 → 读设置」这条启动链路通。

-- +goose Up
CREATE TABLE app_config (
    id                 smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    poll_interval      interval    NOT NULL DEFAULT '15 minutes',
    reconcile_interval interval    NOT NULL DEFAULT '5 minutes',
    sidefx_interval    interval    NOT NULL DEFAULT '1 minute',
    max_attempts       integer     NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
    media_root         text        NOT NULL DEFAULT '',
    paused             boolean     NOT NULL DEFAULT false,
    updated_at         timestamptz NOT NULL DEFAULT now()
);

INSERT INTO app_config (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
