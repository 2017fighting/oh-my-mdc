-- 00012: 手工登记（#39，GLOSSARY「手工登记」）——不经清单轮询，面板贴磁力 + JavDB 链接直接建任务。
--
-- 两处放行，各服务一件事：
--
--   - task.list_id 改可空。这列的语义是「首次发现来自哪张清单」；手工任务的首次发现
--     是人在面板上贴的一串链接，不是任何清单。空串冒充 id 过不了外键，写死想看清单的
--     id 则是往「首次发现来源」里塞假话——将来按清单回查任务会把手工任务误算进去。
--     删清单副作用读的是入箱时的 payload，不读这列（poll 只给自己加的版本置待删，
--     手工版本永远不会有这份待办），可空没有涟漪。
--   - task_version.source 放行 'manual'。指标、事件时间线与通知的 source 槽位都要如实
--     区分「人贴的」与「清单轮询定的源」；复用 javdb_magnet 是往来源里记假话。
--     delivery 不动：磁力交付形态本来就是 'magnet'，交付材料（payload）就是贴来的磁力串，
--     人工重试的重新加种分支因此天然可用。

-- +goose Up
ALTER TABLE task ALTER COLUMN list_id DROP NOT NULL;

ALTER TABLE task_version DROP CONSTRAINT task_version_source_check;
ALTER TABLE task_version ADD CONSTRAINT task_version_source_check
    CHECK (source IN ('javdb_magnet', 'mteam_torrent', 'manual'));
