-- 00014: 补录（#43，GLOSSARY「补录」）——把媒体库里已落盘、不归本项目的旧目录
-- 登记成任务：建任务、把目录里现有视频记为已入库版本，随即强制重刮重写 NFO。
--
-- task_version 两处放行，各服务一件事：
--
--   - source 放行 'backfill'。补录版本的「首次发现」是扫描 media_root 的目录名，
--     不是清单轮询定的源、不是人贴的磁力。复用 manual 或 javdb_magnet 是往来源里
--     记假话；面板与通知的 source 槽位要如实区分这三条路径。
--   - delivery 放行 'none'。交付形态（磁力/私种）描述「这条版本怎么进 qBittorrent」，
--     补录零下载、不过 qBittorrent（GLOSSARY），没有交付形态可言。置 NULL 要放宽
--     NOT NULL 并在所有读方补 COALESCE；枚举加一个词保持列 NOT NULL 的诚实，
--     读方按字面量理解「无交付形态」。payload 保持空串：没有交付材料。
--
-- task.list_id 不动：00012 已放宽为可空，补录任务与手工任务同样落 NULL。

-- +goose Up
ALTER TABLE task_version DROP CONSTRAINT task_version_source_check;
ALTER TABLE task_version ADD CONSTRAINT task_version_source_check
    CHECK (source IN ('javdb_magnet', 'mteam_torrent', 'manual', 'backfill'));

ALTER TABLE task_version DROP CONSTRAINT task_version_delivery_check;
ALTER TABLE task_version ADD CONSTRAINT task_version_delivery_check
    CHECK (delivery IN ('magnet', 'torrent', 'none'));
