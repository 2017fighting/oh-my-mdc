// Package mteam 是第二个数据来源：M-Team（私有 PT 站）的搜索与取种（规格 §5.2）。
//
// 它拥有四件事：
//
//   - 「测试检索」：pageSize=1 的单行搜索，证明 key 可用（规格 §8.4 的 M-Team 测试内容）；
//   - 搜索：POST /api/torrent/search，**JSON** 体，`mode:adult` + 番号；
//   - 取种：genDlToken（**form**）→ 跟 302 到 CDN 取 .torrent 字节 → 本地算 infohash
//     （M-Team 任何端点都不给 infohash，只有下载才拿得到）；
//   - 选种：seeders 降序 → 折扣（FREE 优先）→ 体积降序（本票落定，可调）。
//
// # 它与 JavDB 最不一样的一点
//
// **它不给磁链。** 搜索与详情都不返回 infohash，而它是私种 —— announce 里那份
// 按次签发的凭据只存在于 .torrent 文件里。因此「选哪条」必须发生在下载**之前**
// （用搜索元数据），而版本行的主键（infohash）只能在下完之后从字节里算出来。
//
// # 两条传输事实写死在客户端里
//
// 失败是 **HTTP 200 + 信封 code**（成功 code=="0"）；API 端点**不跟**重定向
// （那里的 302 是 Cloudflare 按 UA 拦人），而下载链接**必须跟**（那一跳是 CDN）。
// 两边共用一个重定向策略的话，必然有一边是错的。
package mteam

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// DefaultHost 是 M-Team 的 API 入口（站点域名把第一个 label 换成 api 就是它）。
const DefaultHost = "https://api.m-team.cc"

// Options 装配一个 M-Team 集成门面所需的依赖。
type Options struct {
	// Host 覆盖默认入口（测试用）；空则用 DefaultHost。
	Host string
	// HTTP 允许注入测试用的 transport；空则用带超时的默认客户端。
	// 注入的客户端也会被覆写重定向策略（见 apiClient/cdnClient）。
	HTTP *http.Client
}

// Integration 是 M-Team 集成门面：一个 settings.Tester + 取种。
type Integration struct {
	host string
	http *http.Client
}

// New 构造 M-Team 集成门面。
func New(opts Options) *Integration {
	host := strings.TrimSpace(opts.Host)
	if host == "" {
		host = DefaultHost
	}
	return &Integration{host: host, http: opts.HTTP}
}

// Torrent 是一次取种的产物：选中的那一条 + 它的字节 + 本地算出的 infohash。
type Torrent struct {
	// ID 是种子在 M-Team 上的 id（只用来下载与排错，不是身份）。
	ID string
	// Name 是种子名（通常以番号开头）。
	Name string
	// Infohash 是从 .torrent 的 info 字典原始字节算出的 v1 infohash（版本行主键）。
	Infohash string
	// Label 是 best-effort 展示标签：labelsNew 里有「中字」就是「中文」。
	Label string
	// SizeBytes 是搜索给出的体积（展示与选种用；qbt 不拿它做判断）。
	SizeBytes int64
	// Bytes 是 .torrent 的原始字节（私种，multipart 加种用）。
	Bytes []byte
}

// LabelChinese 是 label 词表里的「中文」（规格 §4.1：v1 只有「中文/破解」）。
const LabelChinese = "中文"

// Test 实现 settings.Tester：「测试检索」＝ pageSize=1 的单行搜索（规格 §8.4）。
//
// 一行就够：它同时证明 key 有效、信封可读、搜索可用，而一次搜索的配额成本最小。
func (i *Integration) Test(ctx context.Context, _ settings.Section, input settings.TestInput) settings.TestResult {
	apiKey := strings.TrimSpace(input.Text("api_key"))
	if apiKey == "" {
		return settings.TestResult{Code: settings.CodeAuth, Detail: "还没保存 M-Team 的 x-api-key"}
	}

	hits, err := i.client(apiKey).search(ctx, testKeyword, testPageSize)
	if err != nil {
		return settings.TestResult{Code: Classify(err), Detail: err.Error()}
	}

	detail := fmt.Sprintf("key 可用；单行搜索返回 %d 条", len(hits))
	if len(hits) > 0 {
		detail += fmt.Sprintf("（首条：%s）", truncate(hits[0].Name, 60))
	}
	return settings.TestResult{OK: true, Detail: detail}
}

// 测试检索用一条**必然存在**的番号：搜索请求必须带 keyword，
// 拿一个具体的番号比拿空串更能证明「搜得到东西」。
const (
	testKeyword  = "SSIS-001"
	testPageSize = 1
)

// Resolve 取一条种子的全流程：搜索 → 选一条 → genDlToken → 下载 → 本地算 infohash。
//
// 任何一步失败都原样返回（调用方据此降级 JavDB 磁力）：本方法不重试、不猜配额。
// 一次命中要下载一次种子，那是 M-Team 唯一的瓶颈配额（100 个/小时，官方建议值），
// 因此调用方必须只在「JavDB 有磁力且尚无版本」时才调它（规格 §5.2 限速意识）。
func (i *Integration) Resolve(ctx context.Context, apiKey, number string) (Torrent, error) {
	number = strings.TrimSpace(number)
	if number == "" {
		return Torrent{}, ValidationError{Message: "取种需要番号：搜索的 keyword 就是它"}
	}

	c := i.client(strings.TrimSpace(apiKey))
	if c.apiKey == "" {
		return Torrent{}, authError("search", "M-Team 节还没保存 x-api-key", nil)
	}

	hits, err := c.search(ctx, number, searchPageSize)
	if err != nil {
		return Torrent{}, err
	}
	chosen, ok := SelectHit(hits)
	if !ok {
		return Torrent{}, NotFoundError{Number: number}
	}

	raw, err := c.download(ctx, chosen.ID)
	if err != nil {
		return Torrent{}, err
	}
	infohash, err := Infohash(raw)
	if err != nil {
		return Torrent{}, protocolError("download", fmt.Sprintf("取回的种子不能用（id=%s）: %v", chosen.ID, err), err)
	}

	return Torrent{
		ID:        chosen.ID,
		Name:      chosen.Name,
		Infohash:  infohash,
		Label:     chosen.Label(),
		SizeBytes: chosen.SizeBytes,
		Bytes:     raw,
	}, nil
}

// client 用一份 api key 装配 HTTP 客户端。
func (i *Integration) client(apiKey string) *client {
	return &client{baseURL: i.host, apiKey: apiKey, http: i.http}
}

// 编译期确认门面满足测试接缝。
var _ settings.Tester = (*Integration)(nil)
