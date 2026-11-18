package appapi

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ActressWork 是女优页列表的一行：作品身份 + 番号 + 发行日。
//
// 发行日是这一条链路存在的理由：订阅的取数窗口（水位线 ± 回看窗口）全靠它，
// 列表端点的默认序不是发行日，所以这里必须写明 sort_by=release&order_by=desc（§6.4）。
type ActressWork struct {
	MovieID string
	Number  string
	// ReleaseDate 是作品的发行日；上游没给或给不出日期时是零值
	// （零值不进取数窗口，也不推进水位线——不猜一个今天）。
	ReleaseDate time.Time
}

// ActressInfo 是 `/api/v1/actors/{id}` 里我们用到的那一点。
//
// 契约来源是 javdb_rss 的实测（2026-10）：`data.actor` 里只有 id/name/videos_count，
// 顶层另有 filter_tags 与 tags（那两个是 #48 的筛子要的，本票不读）。
type ActressInfo struct {
	ID          string
	Name        string
	VideosCount int
}

// actressEnvelope 是 `/api/v1/actors/{id}` 的负载形态。
type actressEnvelope struct {
	Actor struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		VideosCount int    `json:"videos_count"`
	} `json:"actor"`
}

// actressWorkEnvelope 是女优页列表的负载形态；movieSlim 已带 release_date。
type actressWorkEnvelope struct {
	Movies []movieSlim `json:"movies"`
}

// Actress 读一位女优在上游的基本信息（`/api/v1/actors/{id}`）。
//
// 订阅建行时的名字快照就来自这一次读取：上游没有按名字搜女优的端点（§13 实测），
// 所以「贴 id → 建订阅」必须顺手把名字取回来，否则面板上只有一串 id。
//
// 匿名可读（§13 2026-11 的收窄名单里它还在），token 在场则携带。
func (c *Client) Actress(ctx context.Context, id string) (ActressInfo, error) {
	actressID := strings.TrimSpace(id)
	if actressID == "" {
		return ActressInfo{}, &ValidationError{Message: "女优 id 为空"}
	}

	var env actressEnvelope
	if err := c.GetJSON(ctx, "/api/v1/actors/"+url.PathEscape(actressID), nil, &env); err != nil {
		return ActressInfo{}, fmt.Errorf("取女优 %s: %w", actressID, err)
	}

	// 用 URL 里那个 id，而不是上游回的 actor.id：两者必须永远同一个标识，
	// 而上游若改了 id 那就是另一件事（订阅身份是请求里的那个）。
	return ActressInfo{
		ID:          actressID,
		Name:        strings.TrimSpace(env.Actor.Name),
		VideosCount: env.Actor.VideosCount,
	}, nil
}

// ActressWorks 读女优页的第 page 页作品（`GET /api/v1/movies/tags`）。
//
// 掩码是 `0:a:{id}`（zone 0、实体字母 a、女优 id），**不带第 4 段**：ADR-0017 的裁决是
// 取全集——`s`（單體影片）在上游静默地排掉共演与部分合集，被排掉的连跳过表都进不去，
// 「收下」那个出口对它们不可用。筛子因此整体下沉到详情层（#48），取数只取全集。
//
// 排序写明 release/desc：翻页的「见过下界即停」与水位线的「最晚发行日」都建立在
// 「新的在前」上。空页即止由调用方（订阅循环）判断——这里只读一页。
func (c *Client) ActressWorks(ctx context.Context, id string, page int) ([]ActressWork, error) {
	actressID := strings.TrimSpace(id)
	if actressID == "" {
		return nil, &ValidationError{Message: "女优 id 为空"}
	}
	if page < 1 {
		page = 1
	}

	var env actressWorkEnvelope
	if err := c.GetJSON(ctx, "/api/v1/movies/tags",
		url.Values{
			"filter_by": {"0:a:" + actressID},
			"sort_by":   {"release"},
			"order_by":  {"desc"},
			"page":      {fmt.Sprint(page)},
		}, &env); err != nil {
		return nil, fmt.Errorf("取女优 %s 第 %d 页的作品: %w", actressID, page, err)
	}

	out := make([]ActressWork, 0, len(env.Movies))
	for _, movie := range env.Movies {
		out = append(out, ActressWork{
			MovieID:     strings.TrimSpace(movie.ID),
			Number:      strings.TrimSpace(movie.Number),
			ReleaseDate: parseDate(movie.ReleaseDate),
		})
	}
	return out, nil
}

// parseDate 读上游的 `YYYY-MM-DD`；给不出日期就留零值。
//
// 不猜：空串、坏值都只意味着「这件作品的发行日我们不知道」，把它当成今天会把
// 水位线推进到一件也许很旧的作品上，而把窗口里的新作品静默甩掉。
func parseDate(raw string) time.Time {
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return parsed
}
