-- 00013: 待补齐标注（#42，GLOSSARY「待补齐」）。
--
-- task.missing_faces 是四面判据（无任何图 / 无演员 / 无简介 / 翻译启用却无中文标题）
-- 的机器名数组，空数组 = 四面齐全。它**不是任务状态**（ADR-0006：任务状态只由
-- task_state 视图聚合，不落列）——面板聚合成区、/metrics 出计数，修复动作是
-- 强制重刮，四面补齐自动摘标（整理段重新入库时覆写，补译路径单面摘除）。
--
-- 取值在 text[] 而非四个 boolean：判据清单是产品词汇（面板 chip 按成员渲染），
-- 将来加第五面只加词不改结构（与 §7.2 布局词表同一取舍）。词表：images /
-- actors / outline / title_zh（internal/task/faces.go 是唯一清单）。
--
-- 部分索引只服务面板聚合区与 /metrics 的计数扫描：标注非空的任务是少数。

-- +goose Up
ALTER TABLE task ADD COLUMN missing_faces text[] NOT NULL DEFAULT '{}';

CREATE INDEX task_missing_faces_idx ON task (updated_at DESC) WHERE missing_faces <> '{}';
