-- 00016: 订阅跳过表（#48，规格 §4.5/§6.4，GLOSSARY「跳过 / 收下」，ADR-0017）。
--
-- 筛子整体在详情层：一部作品拉一次 `/api/v4/movies/{id}`，三条判据各自落一行——
-- 女优数 > 1 → not_single、类別「精選綜合」→ compilation、类別「4/16 小時以上作品」
-- → long_form。判据可以错，所以被排掉的必须留下痕迹：面板看得见、能一键「收下」。
--
-- 行的一生：
--
--   - 回看窗口（默认 30 天）让同一部作品每一轮都再现，所以落行必须幂等：部分唯一索引
--     UNIQUE (subscription_id, movie_id) WHERE accepted_at IS NULL 就是那条「每部作品
--     同一时刻最多一条没被收下的记录」。
--   - 「收下」只置 accepted_at，不删行：已收下的行既是「用户裁决过」的记录，也是同一部
--     作品不再被重新排一遍的依据（否则它下一轮又出现在面板上，等于没处可收）。
--   - 30 天清理只清**没被收下**的那些（循环每轮跑一次）：跳过是「最近被排掉的」，一条
--     早就没人看的记录留着只是噪音；而收下的那条要留着当依据。
--
-- 标签 id 一概不存：判据按名字去上游词表查 id，词表在进程内缓存、重启重取（ADR-0017）。

-- +goose Up

CREATE TABLE subscription_skip (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subscription_id bigint      NOT NULL REFERENCES subscription (id),
    -- movie_id 不是外键：跳过发生在建任务之前，被排掉的作品本来就没有 task 行。
    movie_id        text        NOT NULL,
    number          text        NOT NULL,
    -- title 取详情（跳过的作品不会走刮削，面板上就靠这一列认人）。
    title           text        NOT NULL DEFAULT '',
    release_date    date,
    -- 三条判据，与 §9 的 mdc_subscription_skipped_total{reason} 同一套词。
    reason          text        NOT NULL CHECK (reason IN ('not_single', 'compilation', 'long_form')),
    skipped_at      timestamptz NOT NULL DEFAULT now(),
    accepted_at     timestamptz
);

-- 回看窗口每轮都会重见同一部作品：同一时刻只允许一条「没被收下」的记录。
-- 部分索引而不是普通唯一约束——已收下的行可以有多条（同一部作品被收下、清理掉、又被
-- 判据排掉、再收下一次），它们不是「待收下」的状态。
CREATE UNIQUE INDEX subscription_skip_pending_idx
    ON subscription_skip (subscription_id, movie_id)
    WHERE accepted_at IS NULL;

-- 30 天清理（订阅循环每轮跑一次）只扫没被收下的行，索引照这条扫描写。
CREATE INDEX subscription_skip_cleanup_idx
    ON subscription_skip (skipped_at)
    WHERE accepted_at IS NULL;
