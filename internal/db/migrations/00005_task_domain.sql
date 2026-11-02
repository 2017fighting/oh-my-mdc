-- 00005: 任务域 —— task / task_version / version_file / task_event + task_state 视图，
-- 以及副作用出箱表（规格 §4.1/§4.2/§4.4，ADR-0003/0005/0006）。
--
-- 两级聚合：任务以 movie_id 为主键，承载刮削元数据与聚合状态；版本以 infohash 为主键、
-- 全局唯一，一条下载候选一行。**任务级状态不落列**（ADR-0006）：task_state 视图按
-- 固定优先级实时聚合，规格只有这一处实现。
--
-- 本票只落磁力路径，但表按规格全列建好：M-Team（#24）、webhook/对账（#25）、
-- 副作用引擎（#26）、刮削（#27）都在后面按列接线，不再改结构。
--
-- 文本列不参与 settings 的敏感列反射（那是设置域的表）；任务域按设计不存凭据。

-- +goose Up

-- 任务：一部作品一条，番号只是展示/目录属性，不是主键（GLOSSARY「任务」/「番号」）。
-- 刮削列（§7.1）在这里一次建齐：值在强类型列、逐字段来源在 provenance、
-- 逐源原始返回在 raw_snapshot（规格 §3「元数据三面」）。
CREATE TABLE task (
    movie_id        text        PRIMARY KEY,
    number          text        NOT NULL,
    list_id         text        NOT NULL REFERENCES list_registry (list_id),
    title_ja        text        NOT NULL DEFAULT '',
    title_zh        text        NOT NULL DEFAULT '',
    outline         text        NOT NULL DEFAULT '',
    outline_zh      text        NOT NULL DEFAULT '',
    release_date    date,
    runtime_minutes integer,
    studio          text        NOT NULL DEFAULT '',
    publisher       text        NOT NULL DEFAULT '',
    series          text        NOT NULL DEFAULT '',
    director        text        NOT NULL DEFAULT '',
    trailer_url     text        NOT NULL DEFAULT '',
    rating          numeric,
    tags            text[]      NOT NULL DEFAULT '{}',
    provenance      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    raw_snapshot    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- 版本：一条下载候选，主键 infohash（全局唯一）。普通/中字/破解各是一条版本；
-- VR 的 cd1/cd2 是 version_file，不是版本（GLOSSARY「版本」/「分片」）。
--
-- add_pending 是 ADR-0004 的落库标记：先落库（true）再加种，成功/「已存在」才清。
-- list_removal_pending 是「清单项待删」：恒在加下载之后由旁路执行（规格 §6.1）。
-- claimed_at/claimed_by 是 §4.4 的带超时租约；attempts 是加工重试计数。
CREATE TABLE task_version (
    infohash             text        PRIMARY KEY,
    movie_id             text        NOT NULL REFERENCES task (movie_id) ON DELETE CASCADE,
    source               text        NOT NULL CHECK (source IN ('javdb_magnet', 'mteam_torrent')),
    delivery             text        NOT NULL CHECK (delivery IN ('magnet', 'torrent')),
    label                text        NOT NULL DEFAULT '',
    status               text        NOT NULL DEFAULT 'downloading'
                                     CHECK (status IN ('downloading', 'processing', 'finished', 'failed')),
    error_phase          text        CHECK (error_phase IN ('downloading', 'scrape', 'organize')),
    error_reason         text        NOT NULL DEFAULT '',
    claimed_at           timestamptz,
    claimed_by           text        NOT NULL DEFAULT '',
    attempts             integer     NOT NULL DEFAULT 0,
    next_attempt_at      timestamptz,
    add_pending          boolean     NOT NULL DEFAULT true,
    list_removal_pending boolean     NOT NULL DEFAULT false,
    payload              text        NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- 分片：同一条版本（同一个种子）内的多个视频文件。入库幂等与「已落盘」判定的永久记录。
CREATE TABLE version_file (
    id         bigint  GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    infohash   text    NOT NULL REFERENCES task_version (infohash) ON DELETE CASCADE,
    rel_path   text    NOT NULL,
    size_bytes bigint  NOT NULL DEFAULT 0,
    part_index integer NOT NULL DEFAULT 0,
    is_primary boolean NOT NULL DEFAULT false,
    UNIQUE (infohash, rel_path)
);

-- 任务事件：append-only。面板时间线、告警「只发一次」的判据、失败追问都靠它。
CREATE TABLE task_event (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    movie_id   text        NOT NULL REFERENCES task (movie_id) ON DELETE CASCADE,
    infohash   text,
    kind       text        NOT NULL CHECK (kind IN ('state_change', 'side_effect', 'alert', 'manual_retry')),
    from_state text,
    to_state   text,
    phase      text,
    reason     text        NOT NULL DEFAULT '',
    at         timestamptz NOT NULL DEFAULT now()
);

-- 副作用出箱：删清单项 / 刷 Jellyfin / 发 Telegram（ADR-0005）。
-- 部分唯一索引让「同一目标只许一条待办」与「完成后可再次触发」同时成立。
-- 驻留（需人工）＝ done_at IS NULL AND next_attempt_at IS NULL（规格 §4.2），
-- 所以 next_attempt_at 必须可空；新待办靠 DEFAULT now() 立即可跑。
CREATE TABLE side_effect (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind            text        NOT NULL CHECK (kind IN ('list_removal', 'jellyfin_refresh', 'notify')),
    target_key      text        NOT NULL,
    payload         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    next_attempt_at timestamptz DEFAULT now(),
    attempts        integer     NOT NULL DEFAULT 0,
    last_error      text        NOT NULL DEFAULT '',
    done_at         timestamptz
);

CREATE UNIQUE INDEX side_effect_pending_key ON side_effect (kind, target_key) WHERE done_at IS NULL;

-- 演员：刮削时随 JavDB 数据全量落行（#27 接线）；v1 不抓演员详情页。
CREATE TABLE actor (
    javdb_actor_id text        PRIMARY KEY,
    name           text        NOT NULL DEFAULT '',
    gender         text,
    avatar_url     text,
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE task_actor (
    movie_id       text NOT NULL REFERENCES task (movie_id) ON DELETE CASCADE,
    javdb_actor_id text NOT NULL REFERENCES actor (javdb_actor_id) ON DELETE CASCADE,
    role           text NOT NULL DEFAULT 'actor',
    PRIMARY KEY (movie_id, javdb_actor_id, role)
);

-- 任务状态：按 §3 的固定优先级实时聚合（ADR-0006）。没有版本的 task 状态为 NULL
-- （poll 只在有磁力时建任务，正常不会出现；NULL 比假装成某个状态更诚实）。
CREATE VIEW task_state AS
SELECT
    t.movie_id,
    CASE
        WHEN bool_or(v.status = 'processing') THEN 'processing'
        WHEN bool_or(v.status = 'downloading') THEN 'downloading'
        WHEN bool_or(v.status = 'finished') THEN 'finished'
        WHEN count(v.infohash) > 0 THEN 'failed'
        ELSE NULL
    END AS state
FROM task t
LEFT JOIN task_version v ON v.movie_id = t.movie_id
GROUP BY t.movie_id;

-- 索引（规格 §4.4 点名四条；另外两条服务本票的面板查询与 FK）。
CREATE INDEX task_version_status_next_attempt_idx ON task_version (status, next_attempt_at);
CREATE INDEX task_version_claimed_at_idx ON task_version (claimed_at);
CREATE INDEX side_effect_kind_next_attempt_idx ON side_effect (kind, next_attempt_at);
CREATE INDEX task_event_movie_id_id_idx ON task_event (movie_id, id DESC);

CREATE INDEX task_version_movie_id_idx ON task_version (movie_id);
CREATE INDEX task_version_pending_add_idx ON task_version (created_at) WHERE add_pending;
