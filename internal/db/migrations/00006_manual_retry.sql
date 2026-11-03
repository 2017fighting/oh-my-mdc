-- 00006: 人工重试（#31，规格 §6.1 人工重试行 / §8.2 / P13）。
--
-- 两列，各服务一件事：
--
--   - task_version.force_rescrape 是「下一次取数不许复用」的持久标记。元数据已存在时
--     取数段 0 次网络（GLOSSARY「复用元数据」）；「强制重刮」要打破的正是这条判据，
--     而把它做成版本行上的标记而不是进程内的一次性参数，是因为重试之后真正动手的是
--     对账循环里的取数段——中间隔着进程、隔着轮次，标记必须落在库里。
--   - task_event.detail 是事件的结构化附注。raw_snapshot 一被重取就整体覆盖
--     （规格 §3「元数据三面」），所以强制重刮前的旧快照必须先落到别处：落在
--     manual_retry 事件的 detail 里，新旧两份就能逐源对比（GLOSSARY「原始快照」）。
--     reason 仍只放人话，面板时间线不会被一大段 JSON 撑开。
--
-- 默认值让两列对老行与既有代码都是零影响：force_rescrape=false 等于「照旧复用」，
-- detail='{}' 等于「没有附注」。

-- +goose Up
ALTER TABLE task_version ADD COLUMN force_rescrape boolean NOT NULL DEFAULT false;

ALTER TABLE task_event ADD COLUMN detail jsonb NOT NULL DEFAULT '{}'::jsonb;
