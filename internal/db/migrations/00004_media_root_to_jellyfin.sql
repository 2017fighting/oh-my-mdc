-- 00004: media_root 从 app_config 搬到 jellyfin（规格 §4.3 / §5.5，#22 落定）。
--
-- 媒体库根目录是「Jellyfin 可写」那一件事的一半：三个容器把同一 host 目录挂到同一
-- 路径，刷新与硬链接都靠它。放在 app_config 里会让向导的 Jellyfin 步没法一次 PATCH
-- 存完（地址 + key + 目录），也让刷新去另一张表找同一个约定。搬过来之后
-- **行存在＝这一节配齐**照旧，app_config 只剩间隔/重试/暂停。
--
-- 迁移只前进（ADR-0001）：先加列、把已有值搬过去，再删旧列。int/text 那次的
-- 显式转换不适用这里——两列同为 text，直接搬。

-- +goose Up
ALTER TABLE jellyfin ADD COLUMN media_root text NOT NULL DEFAULT '';

-- 已经配好 Jellyfin 的部署：把旧值带过来，不逼人重填。
UPDATE jellyfin
SET media_root = (SELECT media_root FROM app_config WHERE id = 1)
WHERE id = 1 AND media_root = '';

ALTER TABLE app_config DROP COLUMN media_root;
