// Package javdb 把 appapi 传输层接到设置域与向导上（规格 §5.1、§8.1、§8.4）。
//
// 它拥有三件事：
//
//   - 「测试读取」：用户域读（证明 token 可用）+ 登记清单读（证明清单真的读得到）；
//   - 向导探测：列出用户自建的清单，供按 id 选中登记；
//   - 向导代建：建一张「想看清单」，把 data.id 交回登记。
//
// token 的处置是单会话的：本包**绝不**自动重新登录（那会踢掉用户的手机），
// token 失效（JWTVerificationError）归 auth，喂上游 permanent 指标 + 需人工。
package javdb

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/metrics"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// DefaultHost 是 App 私有 API 的主机（实测沿用 javdb_rss 的生产值）。
const DefaultHost = "https://jdforrepam.com"

// DefaultWantWatchListName 是向导代建「想看清单」的默认名。
//
// 服务端要求清单名 4–30 字符，「想看」二字不合规（规格 §5.1 的清单名陷阱），
// 因此默认名取「想看清单」。用户可改。
const DefaultWantWatchListName = "想看清单"

// DefaultWantWatchListDescription 是代建清单的默认描述。
const DefaultWantWatchListDescription = "oh-my-mdc 自动创建"

// Options 装配一个 JavDB 集成门面所需的依赖。
type Options struct {
	// Host 覆盖默认主机（测试用）；空则用 DefaultHost。
	Host string
	// HTTP 允许注入测试用的 transport；空则用带超时的默认客户端。
	HTTP *http.Client
	// Metrics 记每次上游请求的结局（规格 §9 的 javdb 指标）；nil 时什么都不记。
	Metrics metrics.Recorder
}

// Integration 是 JavDB 集成门面：一个 settings.Tester + 向导辅助操作。
type Integration struct {
	host    string
	http    *http.Client
	metrics metrics.Recorder
}

// New 构造 JavDB 集成门面。
func New(opts Options) *Integration {
	host := opts.Host
	if host == "" {
		host = DefaultHost
	}
	return &Integration{host: host, http: opts.HTTP, metrics: opts.Metrics}
}

// client 用一份设置原文装配 appapi 客户端。
//
// token 空＝匿名请求（清单项读匿名可读；但用户域读会失败，正好用来判 token）。
func (i *Integration) client(token string) *appapi.Client {
	return &appapi.Client{
		Host:     i.host,
		Token:    strings.TrimSpace(token),
		Identity: appapi.DefaultIdentity(),
		Signer:   appapi.NewSigner(),
		HTTP:     i.http,
		Metrics:  i.metrics,
	}
}

// Test 实现 settings.Tester：「测试读取」＝用户域读 + 登记清单读（规格 §8.4）。
//
// 两关的判据：
//
//  1. 用户域读（Lists）证明 token 可用 —— 匿名调用实测返回 JWTVerificationError，
//     因此坏 token 会在这里归成 auth；
//  2. 若已登记「想看」清单，再读一次它的项，证明这张清单真的读得到。
//
// 未登记清单时只验 token（JavDB 步早于清单步，不该因为还没登记就红）。
func (i *Integration) Test(ctx context.Context, _ settings.Section, input settings.TestInput) settings.TestResult {
	client := i.client(input.Text("token"))

	lists, err := client.Lists(ctx)
	if err != nil {
		return classify(err)
	}

	entry, ok := input.ListFor("want_watch")
	if !ok {
		return settings.TestResult{
			OK:     true,
			Detail: fmt.Sprintf("token 可用，读到 %d 张清单；清单还没登记", len(lists)),
		}
	}

	items, err := client.ListItems(ctx, entry.ListID)
	if err != nil {
		return classify(err)
	}

	name := entry.Name
	if name == "" {
		name = entry.ListID
	}
	return settings.TestResult{
		OK:     true,
		Detail: fmt.Sprintf("token 可用；清单「%s」读到 %d 项", name, len(items)),
	}
}

// Lists 探测用户自建的清单（向导「读取已有清单」）。
func (i *Integration) Lists(ctx context.Context, token string) ([]appapi.List, error) {
	if strings.TrimSpace(token) == "" {
		return nil, &appapi.ValidationError{Message: "还没保存 JavDB token"}
	}
	return i.client(token).Lists(ctx)
}

// CreateList 代建一张清单并返回新 id（向导「一键创建」）。
//
// 名字与描述的空值在这里兜一次（默认「想看清单」/「oh-my-mdc 自动创建」）：
// 这是单点，HTTP 层与向导都不再各自重复一份默认值。
func (i *Integration) CreateList(ctx context.Context, token, name, description string) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", &appapi.ValidationError{Message: "还没保存 JavDB token"}
	}
	if strings.TrimSpace(name) == "" {
		name = DefaultWantWatchListName
	}
	if strings.TrimSpace(description) == "" {
		description = DefaultWantWatchListDescription
	}
	return i.client(token).CreateList(ctx, name, description)
}

// RemoveFromList 把一部作品移出清单（规格 §5.1 的写侧；list_removal 的唯一执行路径）。
//
// 只有它需要 token：读清单匿名可读（P16-1），写清单不行。token 为空时本地就报
// business，不空打一次上游换回一个 auth。
//
// 幂等：实测重复 remove 回 success:1（#16），所以「删成功但没落库」的下一次尝试
// 会直接成功，不需要额外的去重记录。
func (i *Integration) RemoveFromList(ctx context.Context, token, listID, movieID string) error {
	if strings.TrimSpace(token) == "" {
		return &appapi.ValidationError{Message: "还没保存 JavDB token"}
	}
	return i.client(token).RemoveFromList(ctx, listID, movieID)
}

// classify 把一次上游错误翻成 settings 的稳定机器码（规格 §8.4 的测试错误码）。
//
// 分类本身由 appapi.Classify 单点决定（与 /api/javdb/* 的 HTTP 错误共用一套）；
// 这里只把它翻成 settings.TestCode。
func classify(err error) settings.TestResult {
	code := appapi.Classify(err)
	return settings.TestResult{Code: testCode(code), Detail: err.Error()}
}

func testCode(code string) settings.TestCode {
	switch code {
	case appapi.CodeAuth:
		return settings.CodeAuth
	case appapi.CodeBusiness:
		return settings.CodeBusiness
	case appapi.CodeProtocol:
		return settings.CodeProtocol
	default:
		return settings.CodeNetwork
	}
}

// 编译期确认门面满足测试接缝。
var _ settings.Tester = (*Integration)(nil)
