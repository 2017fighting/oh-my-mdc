package appapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// MovieAction 是清单项动作 `name` 的取值（规格 §5.1 增/删清单项）。
//
// ⚠️ 服务端陷阱（#16 实测）：name 缺失或取值未知时**默认执行 remove**。所以这个类型
// 只有两个合法值，且只由本包常量产出；任何来自用户/配置/数据库的字符串都必须过
// valid()，本地不认就报错，绝不带进请求。
type MovieAction string

const (
	// MovieActionAdd 把一部作品加进清单（重复 add 实测 success:1，幂等）。
	MovieActionAdd MovieAction = "add"
	// MovieActionRemove 把一部作品从清单里删掉（重复 remove 实测 success:1，幂等）。
	MovieActionRemove MovieAction = "remove"
)

// ErrUnknownMovieAction 说明调用方给了一个不在白名单里的动作值。
//
// 它是**本地**错误：请求一个字节都没发出去。给定值一旦漏到线上，服务端会把它当
// remove 执行——对一个只想加清单的调用，那是一次静默的数据丢失。
var ErrUnknownMovieAction = errors.New("javdb api: 清单项动作只能是 add 或 remove")

// valid 报告这个动作值是否在白名单里。
func (a MovieAction) valid() bool {
	return a == MovieActionAdd || a == MovieActionRemove
}

// SetListMembership 发一次 movie_actions：把 movie_id 加进/移出清单。
//
// 传输事实（规格 §5.1 实测）：
//   - **form-encoded**（`name` + `movie_id`），动作不在 `action` 字段（那个字段被忽略）；
//   - 成功回执 data 恒 null，因此不解析负载；
//   - 重复 add、重复 remove、remove 不存在的 movie 全部 success:1 —— 天然幂等，
//     所以「上一次删成功了但没来得及落库」不会变成永久错误。
func (c *Client) SetListMembership(ctx context.Context, listID, movieID string, action MovieAction) error {
	if !action.valid() {
		return fmt.Errorf("%w: %q", ErrUnknownMovieAction, string(action))
	}

	listID = strings.TrimSpace(listID)
	if listID == "" {
		return &ValidationError{Message: "清单项动作缺清单 id"}
	}
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return &ValidationError{Message: "清单项动作缺 movie_id"}
	}

	form := url.Values{
		"name":     {string(action)},
		"movie_id": {movieID},
	}
	if err := c.PostFormJSON(ctx,
		"/api/v1/lists/"+url.PathEscape(listID)+"/movie_actions", form, nil); err != nil {
		return fmt.Errorf("清单 %s %s movie %s: %w", listID, action, movieID, err)
	}
	return nil
}

// RemoveFromList 把一部作品移出清单（旁路副作用 list_removal 的唯一写路径）。
func (c *Client) RemoveFromList(ctx context.Context, listID, movieID string) error {
	return c.SetListMembership(ctx, listID, movieID, MovieActionRemove)
}
