-- 00008: 清单验读的账本列 —— 门禁第六判据的机读真相（2026-11）。
--
-- 门禁从五判据升到六：登记的「想看」清单要被一次真实测试读到过才算齐。
-- 「读到过」需要记账：测试读了哪张清单的 id，记在 javdb.last_test_list_id。
-- 判据读的是账本状态（last_test.ok ∧ 记录 id == 当前登记 id），不是活体探测——
-- 上游抖动不会推翻已验记录，换 id 或没验过才拦。
--
-- 列随 last_test_* 走统一契约：六张集成表都加，但只有 javdb 的测试会写它
-- （别的集成没有「读清单」这回事，恒为空串）。测试失败不清旧值：失败证明不了
-- 清单不可读，只证明这次没读成——已验记录不该被一次网络抖动抹掉。

-- +goose Up
ALTER TABLE javdb          ADD COLUMN last_test_list_id text NOT NULL DEFAULT '';
ALTER TABLE qbittorrent    ADD COLUMN last_test_list_id text NOT NULL DEFAULT '';
ALTER TABLE jellyfin       ADD COLUMN last_test_list_id text NOT NULL DEFAULT '';
ALTER TABLE mteam          ADD COLUMN last_test_list_id text NOT NULL DEFAULT '';
ALTER TABLE ai_translation ADD COLUMN last_test_list_id text NOT NULL DEFAULT '';
ALTER TABLE telegram       ADD COLUMN last_test_list_id text NOT NULL DEFAULT '';
