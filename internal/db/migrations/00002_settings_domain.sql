-- 00002: 设置域 —— 六张集成表、list_registry（规格 §4.3，ADR-0002）。
--
-- 统一契约：
--   * 集成表单行（id = 1），**行存在＝已配置**；DELETE 掉行就是解除配置。
--   * 可选集成（mteam / ai_translation / telegram）另带 enabled，配了也能关着。
--   * 每张集成表都带 last_test_at / last_test_ok / last_test_error —— 测试连接的账本，
--     不属于用户设置，因此**不进** revision 指纹（否则测试一次就把别的标签页判成冲突）。
--   * 时间戳只由程序写：app_config 有 updated_at，集成表只有 last_test_*。
--   * 敏感列一律 text NOT NULL DEFAULT ''（缺省即未设置），清单见 internal/settings/sensitive.go。

-- +goose Up

-- 清单登记：按 id 认清单，名字只是快照（规格 §4.3 / §5.1）。
CREATE TABLE list_registry (
    list_id    integer     PRIMARY KEY,
    purpose    text        NOT NULL CHECK (purpose IN ('want_watch', 'want_watch_zh')),
    name       text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

-- 一个用途只许占一张清单（「想看」与「想看中文」各自一张）。
CREATE UNIQUE INDEX list_registry_purpose_key ON list_registry (purpose);

CREATE TABLE javdb (
    id             smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    token          text        NOT NULL DEFAULT '',
    last_test_at   timestamptz,
    last_test_ok   boolean,
    last_test_error text
);

CREATE TABLE qbittorrent (
    id               smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    url              text        NOT NULL DEFAULT '',
    username         text        NOT NULL DEFAULT '',
    password         text        NOT NULL DEFAULT '',
    category         text        NOT NULL DEFAULT 'mdc',
    download_root    text        NOT NULL DEFAULT '',
    webhook_base_url text        NOT NULL DEFAULT '',
    last_test_at     timestamptz,
    last_test_ok     boolean,
    last_test_error  text
);

CREATE TABLE jellyfin (
    id              smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    url             text        NOT NULL DEFAULT '',
    api_key         text        NOT NULL DEFAULT '',
    last_test_at    timestamptz,
    last_test_ok    boolean,
    last_test_error text
);

CREATE TABLE mteam (
    id              smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    api_key         text        NOT NULL DEFAULT '',
    enabled         boolean     NOT NULL DEFAULT false,
    last_test_at    timestamptz,
    last_test_ok    boolean,
    last_test_error text
);

CREATE TABLE ai_translation (
    id              smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    base_url        text        NOT NULL DEFAULT '',
    api_key         text        NOT NULL DEFAULT '',
    model           text        NOT NULL DEFAULT '',
    enabled         boolean     NOT NULL DEFAULT false,
    last_test_at    timestamptz,
    last_test_ok    boolean,
    last_test_error text
);

CREATE TABLE telegram (
    id                        smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    bot_token                 text        NOT NULL DEFAULT '',
    chat_id                   text        NOT NULL DEFAULT '',
    enabled                   boolean     NOT NULL DEFAULT false,
    notify_download_started   boolean     NOT NULL DEFAULT true,
    notify_filed              boolean     NOT NULL DEFAULT true,
    notify_processing_failed  boolean     NOT NULL DEFAULT true,
    notify_download_stalled   boolean     NOT NULL DEFAULT true,
    last_test_at              timestamptz,
    last_test_ok              boolean,
    last_test_error           text
);
