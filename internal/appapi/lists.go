package appapi

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// maxListPages 是清单列表的翻页上限。
//
// 上游是「返回空即到底」，因此需要一个上限来防一个异常的上游让我们无限翻下去。
// 实测该账号共 5 份清单，第 2 页就是空的；20 页是很宽的余量。
const maxListPages = 20

// List 是一份 JavDB 清单的摘要（/api/v1/lists/simple 的一行）。
//
// 判定清单永远靠 ID，Name 只是快照（规格 §5.1）：默认清单在两种读法下名字不同，
// 所以名字不能当身份用。
type List struct {
	ID          string
	Name        string
	Privacy     string
	IsDefault   bool
	MoviesCount int
}

// ListItem 是清单里的一项：movie_id + 番号（规格 §5.1 读清单项）。
type ListItem struct {
	MovieID string
	Number  string
}

// listEnvelope 是 `/api/v1/lists/simple` 的负载形态（#16 实测）。
//
//	{"lists":[{"id":"k4EVE4","name":"遥控跳弹","privacy":"open",
//	           "is_default":false,"movies_count":1,"has_movie":false}]}
//
// 刻意**不解析** has_movie：它是「当前用户有没有把这部片收进清单」，与本服务无关。
type listEnvelope struct {
	Lists []listSimple `json:"lists"`
}

type listSimple struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Privacy     string `json:"privacy"`
	IsDefault   bool   `json:"is_default"`
	MoviesCount int    `json:"movies_count"`
}

// createListEnvelope 是 `POST /api/v1/lists` 的负载形态。
//
// 实测：成功时 data 是**清单对象**（含 id），失败时 data 为 null。
//
//	{"success":1,"action":null,"message":"清單創建成功",
//	 "data":{"id":"e8r1OM","name":"ohmymdc-probe","privacy":"open", …}}
type createListEnvelope struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// movieListEnvelope 是 `/api/v1/movies/tags` 的负载形态（清单项读）。
type movieListEnvelope struct {
	Movies []movieSlim `json:"movies"`
}

// movieSlim 是列表类端点返回的作品精简形态（只取清单项判定要用的两个字段）。
type movieSlim struct {
	ID     string `json:"id"`
	Number string `json:"number"`
}

// Lists 读用户自己建的清单列表（/api/v1/lists/simple）。
//
// 探测用：向导把结果按 id 匹配「想看清单」，名字仅展示（规格 §5.1/§8.1）。
// 需要 token：匿名调用实测返回 JWTVerificationError。
func (c *Client) Lists(ctx context.Context) ([]List, error) {
	rows, err := c.listPages(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]List, 0, len(rows))
	for _, l := range rows {
		out = append(out, List{
			ID:          strings.TrimSpace(l.ID),
			Name:        strings.TrimSpace(l.Name),
			Privacy:     strings.TrimSpace(l.Privacy),
			IsDefault:   l.IsDefault,
			MoviesCount: l.MoviesCount,
		})
	}
	return out, nil
}

// listPages 按页读到空为止，按 id 去重。
//
// 「到底了」只能靠「这一页是空的」判断（上游不给总数）；上限见 maxListPages。
func (c *Client) listPages(ctx context.Context) ([]listSimple, error) {
	var out []listSimple
	seen := make(map[string]bool)
	for page := 1; page <= maxListPages; page++ {
		var env listEnvelope
		if err := c.GetJSON(ctx, "/api/v1/lists/simple",
			url.Values{"page": {fmt.Sprint(page)}}, &env); err != nil {
			return nil, fmt.Errorf("取清单列表第 %d 页: %w", page, err)
		}
		if len(env.Lists) == 0 {
			return out, nil
		}
		for _, l := range env.Lists {
			id := strings.TrimSpace(l.ID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, l)
		}
	}
	return out, nil
}

// ListItems 读某张清单的第一页（/api/v1/movies/tags?filter_by=0:l:{list_id}）。
//
// zone 写死 0：清单形态里没有 zone，而写错 zone 不会报错、只会静默给别的作品
// （实测 0:l:{id} 与上游 movies_count 逐位相同，换成 2:l:{id} 全部变成 50 条）。
//
// 匿名可读；token 在场则携带。每项带 id（=movie_id）与 number（番号）。
// 「测试读取」与向导只需要第一页；poll 的全量扫用 AllListItems。
func (c *Client) ListItems(ctx context.Context, listID string) ([]ListItem, error) {
	return c.ListItemsPage(ctx, listID, 1)
}

// maxListItemPages 是清单项翻页的上限。
//
// 上游「返回空即到底」，因此需要一个上限防一个异常的上游让我们无限翻下去。
// 单用户清单量级远小于 200 页；这是宽余量，不是业务约束。
const maxListItemPages = 200

// ListItemsPage 读某张清单的第 page 页（从 1 开始）。
func (c *Client) ListItemsPage(ctx context.Context, listID string, page int) ([]ListItem, error) {
	listID = strings.TrimSpace(listID)
	if listID == "" {
		return nil, &ValidationError{Message: "清单 id 为空"}
	}
	if page < 1 {
		page = 1
	}

	var env movieListEnvelope
	if err := c.GetJSON(ctx, "/api/v1/movies/tags",
		url.Values{
			"filter_by": {"0:l:" + listID},
			"page":      {fmt.Sprint(page)},
		}, &env); err != nil {
		return nil, fmt.Errorf("取清单 %s 第 %d 页的项: %w", listID, page, err)
	}

	out := make([]ListItem, 0, len(env.Movies))
	for _, m := range env.Movies {
		out = append(out, ListItem{
			MovieID: strings.TrimSpace(m.ID),
			Number:  strings.TrimSpace(m.Number),
		})
	}
	return out, nil
}

// AllListItems 全量扫一张清单，按 movie_id 去重。
//
// 规格 §5.1/§6.1：上游不给总页数，「空页即止」。翻页期间清单可能被增删，
// 因此按 movie_id 去重，同 id 以第一次出现的番号为准。空 movie_id 的行走不过去
// （没有主键就没法建任务）。
func (c *Client) AllListItems(ctx context.Context, listID string) ([]ListItem, error) {
	seen := make(map[string]bool)
	var out []ListItem

	for page := 1; page <= maxListItemPages; page++ {
		items, err := c.ListItemsPage(ctx, listID, page)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return out, nil
		}
		for _, item := range items {
			if item.MovieID == "" || seen[item.MovieID] {
				continue
			}
			seen[item.MovieID] = true
			out = append(out, item)
		}
	}
	return out, nil
}

// 服务端的清单名约束（#16 实测）：4–30 字符。本地先拦一次，免得白打一次上游，
// 也让「默认名会不会不合规」变成一条可以钉死的测试。
const (
	MinListNameLen = 4
	MaxListNameLen = 30
)

// CreateList 建一张清单（POST /api/v1/lists，form-encoded），返回新清单 id。
//
// 服务端规则（实测 #16）：name 必填 4–30 字符、同名拒；description 可选。
// 失败时 success:0 + action:null + zh-TW message —— 由调用方原样透出
// （同名拒的错误要能让用户看见，规格 P2-2）。
//
// ⚠️ 必须是 form 编码，JSON body 会被拒（ParameterInvalid）。
func (c *Client) CreateList(ctx context.Context, name, description string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < MinListNameLen || n > MaxListNameLen {
		return "", &ValidationError{Message: fmt.Sprintf("清单名需 %d–%d 个字符，当前 %d 个", MinListNameLen, MaxListNameLen, n)}
	}

	form := url.Values{"name": {name}}
	if description != "" {
		form.Set("description", description)
	}

	var env createListEnvelope
	if err := c.PostFormJSON(ctx, "/api/v1/lists", form, &env); err != nil {
		return "", err
	}
	id := strings.TrimSpace(env.ID)
	if id == "" {
		return "", &ProtocolError{Path: "/api/v1/lists", Message: "建清单成功但响应里没有 data.id"}
	}
	return id, nil
}
