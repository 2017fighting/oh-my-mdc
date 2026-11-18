package javdb

import (
	"context"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
)

// Actress 读一位女优的基本信息（`/api/v1/actors/{id}`）：订阅建行时的名字快照来自这里。
//
// 它是面板「贴 id / 女优页链接 → 建订阅」那一步的唯一上游读取。匿名可读（§13 的
// 2026-11 收窄名单里它还在），token 在场则携带。
func (i *Integration) Actress(ctx context.Context, token, id string) (appapi.ActressInfo, error) {
	return i.client(token).Actress(ctx, id)
}

// ActressWorks 读女优页的一页作品（`/api/v1/movies/tags`，掩码 `0:a:{id}`）。
//
// 女优订阅循环（#47）只读一页一次：翻页与「见过下界即停」是那个循环自己的判断，
// 水位线是它的账。筛子不在这里（ADR-0017：取数取全集，判据下沉到详情层）。
func (i *Integration) ActressWorks(ctx context.Context, token, id string, page int) ([]appapi.ActressWork, error) {
	if strings.TrimSpace(id) == "" {
		return nil, &appapi.ValidationError{Message: "读女优页需要女优 id"}
	}
	return i.client(token).ActressWorks(ctx, id, page)
}
