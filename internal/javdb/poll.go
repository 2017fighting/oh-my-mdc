package javdb

import (
	"context"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
)

// AllListItems 全量扫一张清单（空页即止，按 movie_id 去重）。
//
// poll 用它在同一轮里拿到清单的全部项（规格 §5.1/§6.1）；token 在场则携带，
// 匿名也可读（P16-1）。写侧（删清单项）不在这里，走旁路副作用。
func (i *Integration) AllListItems(ctx context.Context, token, listID string) ([]appapi.ListItem, error) {
	return i.client(token).AllListItems(ctx, listID)
}

// Magnets 读一部作品的磁力候选（规格 §5.1）。空数组是合法答案。
func (i *Integration) Magnets(ctx context.Context, token, movieID string) ([]appapi.Magnet, error) {
	if strings.TrimSpace(movieID) == "" {
		return nil, &appapi.ValidationError{Message: "读磁力候选需要 movie_id"}
	}
	return i.client(token).Magnets(ctx, movieID)
}
