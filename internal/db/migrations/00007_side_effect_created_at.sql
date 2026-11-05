-- 00007: 副作用待办的入箱时间（#面板「驻留副作用」的年龄）。
--
-- 面板要为驻留副作用说一句「最早 N 天前」，而 side_effect 原来只有
-- next_attempt_at / done_at 两个时间：退避耗尽的那些行两个都是 NULL，于是
-- 「这件事我躲了多久」在库层面无法回答——上一轮只能在词条里写「组没有年龄」。
--
-- created_at 记的是**入箱**那一刻（同一次删除/刷新/通知）。它不等于最后一次尝试的
-- 时间，但对「这条待办存在多久了」这个问题，入箱时间才是对的锚点，而最后尝试时间
-- 这一版没有落库（attempts 只数次数）。
--
-- 老行没有这一列的值：两句话分开写是为了不让 ADD COLUMN 的 DEFAULT 把老行一起写成
-- 迁移时刻——那是把「不知道」冒充成「刚刚」。NULL 就是 NULL，面板对 NULL 保持沉默。

-- +goose Up
ALTER TABLE side_effect ADD COLUMN created_at timestamptz;

-- 默认值只作用于此后 INSERT 的行（入箱语句不显式写这一列）。
ALTER TABLE side_effect ALTER COLUMN created_at SET DEFAULT now();
