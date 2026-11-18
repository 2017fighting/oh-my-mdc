package appapi

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// collectedPageSize 是 `/api/v1/users/collected_actors` 的固定页长。
//
// 实测（javdb_rss 2026-09-30，真 token）：**每页固定 10 条**，与任何参数无关——
// 因此「一页 10 条」是可以拿来算上限与余量的常数，而不是我们挑的分页大小。
const collectedPageSize = 10

// maxCollectedPages 是收藏名单的翻页上限。
//
// 「到底了」只能靠空页判断（上游不给总数），所以需要一个上限防一个异常的上游
// （永远返回满页）把它变成自己打自己的循环。20 页 = 200 位：实测那个 144 位的收藏夹
// 是 15 页（14 满页 + 1 页 4 条），余量约 1.4 倍。
//
// 触顶**不静默**：CollectedActors 会再探一页，真被上限切掉时置 Truncated，面板据此说
// 「只列到前 200 位」——一份悄悄少给 56 位的收藏名单，会让「我收藏里有她」与
// 「我根本没收藏过」长得一模一样（与 internal/task/attention.go 的「50+」同一条判据）。
const maxCollectedPages = 20

// CollectedActress 是收藏名单里的一位演员（上游一行）。
type CollectedActress struct {
	ID          string
	Name        string
	VideosCount int
	// Gender 是上游的性别码：**0 = 女优，1 = 男优**（javdb_rss 实测，144 位收藏里 6 位男优）。
	//
	// 原样透出、不翻译成自造枚举：这个码只在收藏名单里给得出（`/api/v1/actors/{id}`
	// 详情没有 gender 字段），而面板要的是「哪几位不是女优」，不是一套我们编的词。
	Gender int
}

// CollectedActors 是一份收藏名单：条目 + 是否被页数上限截断。
type CollectedActors struct {
	Actresses []CollectedActress
	// Truncated 表示上限之外还有数据：这份名单**已知不完整**。
	Truncated bool
}

// collectedEnvelope 是 `/api/v1/users/collected_actors` 的负载形态。
//
//	{"actors":[{"id":"D2EdJ","type":0,"name":"花守夏歩","gender":0,"videos_count":179}]}
//
// 上游只保证 `actors` 这个 key（不给分页元信息），所以「到底了」只能靠空页判断。
// 刻意**不解析** type：它是片库 zone，不是身份（身份永远 id）。
type collectedEnvelope struct {
	Actors []collectedActressRow `json:"actors"`
}

type collectedActressRow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	VideosCount int    `json:"videos_count"`
	Gender      int    `json:"gender"`
}

// CollectedActors 读用户在 App 里收藏的演员名单（#50「从 App 收藏导入」）。
//
// 三条刻意的行为，与 `listPages` 同形但多了一样东西：
//
//  1. **翻页到底、按 id 去重。** 翻页期间收藏会变，同一行可能落在两页上；没有 id 的行
//     丢掉——身份不明的东西不该进一份按身份去重的名单。
//  2. **上限之上多探一页。** `listPages` 到上限就静静停下（清单数量是十几份的量级，
//     碰不到）；这里 10 条一页、碰得到，所以照 `internal/task/attention.go` 的
//     「多取一条」办：探到的那页**不入名单**，只用来分清「恰好 200 位」与「被切了」。
//  3. **需要 token。** 匿名调用实测回 JWTVerificationError；本地不发那一次空请求由
//     javdb.Integration 拦（这一层是传输层，不替调用方判凭据够不够）。
func (c *Client) CollectedActors(ctx context.Context) (CollectedActors, error) {
	seen := make(map[string]bool)
	out := make([]CollectedActress, 0)

	for page := 1; page <= maxCollectedPages; page++ {
		rows, err := c.collectedPage(ctx, page)
		if err != nil {
			return CollectedActors{}, err
		}
		if len(rows) == 0 {
			// 空页 = 到底。这是一份完整名单。
			return CollectedActors{Actresses: out}, nil
		}
		for _, row := range rows {
			id := strings.TrimSpace(row.ID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, CollectedActress{
				ID:          id,
				Name:        strings.TrimSpace(row.Name),
				VideosCount: row.VideosCount,
				Gender:      row.Gender,
			})
		}
	}

	// 读满上限仍未遇空页：再探一页。它只回答「后面还有没有」，自己不进名单。
	probe, err := c.collectedPage(ctx, maxCollectedPages+1)
	if err != nil {
		return CollectedActors{}, err
	}
	return CollectedActors{Actresses: out, Truncated: len(probe) > 0}, nil
}

// collectedPage 拉收藏名单的第 page 页（从 1 开始）。
func (c *Client) collectedPage(ctx context.Context, page int) ([]collectedActressRow, error) {
	var env collectedEnvelope
	if err := c.GetJSON(ctx, "/api/v1/users/collected_actors",
		url.Values{"page": {strconv.Itoa(page)}}, &env); err != nil {
		return nil, fmt.Errorf("取收藏名单第 %d 页: %w", page, err)
	}
	return env.Actors, nil
}
