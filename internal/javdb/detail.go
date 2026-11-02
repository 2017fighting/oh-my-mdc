package javdb

import (
	"context"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
)

// Detail 读一部作品的详情（`GET /api/v4/movies/{id}`，规格 §7.1 的 JavDB 列来源）。
//
// 它是刮削取数的 JavDB 一侧：演员（含性别与头像）、tags、5 分制分值、
// 片商/发行/系列/导演、封面与预览图。字段级回退与 5→10 分制换算不在这里，
// 那是 scrape 字段图的活（§7.1 的唯一实现）。
//
// 详情匿名可读；token 在场就携带（与清单读同一条规则，P16-1）。
// 回执带着 data 原文，进 raw_snapshot。
func (i *Integration) Detail(ctx context.Context, token, movieID string) (appapi.DetailResult, error) {
	return i.client(token).Detail(ctx, movieID)
}
