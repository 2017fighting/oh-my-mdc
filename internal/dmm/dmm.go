// Package dmm 取 DMM（FANZA）的番号元数据：发现腿 + 内容腿（规格 §5.3）。
//
// 它不是 HTML 抓取：站点自己的播放器走 `POST https://api.video.dmm.co.jp/graphql`，
// 五个硬头缺一即被拒（Referer/Content-Type/Accept/Cache-Control/Fanza-Device）。
// 两条腿是：
//
//   - **发现**：`legacySearchPPV(limit: 30, floor: AV, sort: SALES_RANK_SCORE,
//     queryWord: <番号>)` → 候选 product id。排序是销量不是相关度，所以窗口要宽，
//     而且 product id **不是番号**：同一部片有两种零填充拼写（`ssis001` / `ssis00001`），
//     都要试；命中按**身份**（前缀 + 数值）判，绝不按位置 —— 搜 SSIS-001 会搜出
//     SSIS-125 这种邻居，按位置取第一条就是发错片。
//   - **内容**：`ppvContent(id:) + reviewSummary(contentId:)` 一次 POST，
//     日文标题/简介、RFC3339 日期、秒级时长、绝对图片 URL、女优/导演/片商/标签。
//
// 地理约束是**部署前提**而不是参数：DMM 全站只服务日本出口，非日本出口连 TLS 都握不上
// （规格 §13）。因此这里没有代理开关；地区拒绝归 `IsRegionBlocked`，
// 调用方按失败退避，**绝不**把它当成「DMM 没收录」（那会永久降级成 JavDB 元数据）。
//
// 开发机与 CI 都到不了 DMM：测试全程用库里录制的真实响应驱动（testdata/）。
package dmm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/metrics"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

const (
	// DefaultHost 是播放器 GraphQL 的主机。
	DefaultHost = "https://api.video.dmm.co.jp"
	// DefaultReferer 是站点自己的页面地址，五个硬头之一。
	DefaultReferer = "https://video.dmm.co.jp/"
	// searchLeg 是一次发现腿要多少条候选。
	//
	// 它不是可调的页大小：目标未必排在前面（SSIS-001 的 95 条结果里，第一条作品
	// 在销量序的第 19 位），窗口越大越不容易「明明有却说没有」。30 是实测够用的值。
	searchLeg = 30
	// paddedDigits 是 product id 序号可能用的零填充宽度。
	paddedDigits = 5
	// maxBody 是读响应体的上限：挡住一个坏响应把内存吃光。
	maxBody = 8 << 20
	// defaultTimeout 是单次调用的上限。
	defaultTimeout = 30 * time.Second
)

// Options 装配一个 DMM 客户端。
type Options struct {
	// Host 覆盖 API 主机（测试指向 httptest）；空则用 DefaultHost。
	Host string
	// Referer 覆盖 Referer 头；空则用 DefaultReferer。
	Referer string
	// HTTP 允许注入测试用的 transport；空则用带超时的默认客户端。
	HTTP *http.Client
	// Metrics 记每次上游请求的结局（规格 §9 的 dmm 指标）；nil 时什么都不记。
	Metrics metrics.Recorder
}

// Client 是 DMM 播放器 GraphQL 客户端。
type Client struct {
	api     string
	referer string
	http    *http.Client
	metrics metrics.Recorder
}

// New 构造客户端。
func New(opts Options) *Client {
	host := strings.TrimRight(strings.TrimSpace(opts.Host), "/")
	if host == "" {
		host = DefaultHost
	}
	referer := strings.TrimSpace(opts.Referer)
	if referer == "" {
		referer = DefaultReferer
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{api: host + "/graphql", referer: referer, http: httpClient, metrics: opts.Metrics}
}

// Actress 是一位女优（内容单的 actresses[] 一项）。
//
// DMM 只给名字与头像：性别拆分归 JavDB（规格 §7.1 演员行），所以这里没有 gender。
type Actress struct {
	Name     string
	ImageURL string
}

// Content 是一条命中的内容单，按字段提取好（不保留 GraphQL 的嵌套形状）。
type Content struct {
	ID              string
	Title           string
	Description     string
	ReleaseDate     string // YYYY-MM-DD：deliveryStartDate 优先，makerReleasedAt 兜底
	DurationSeconds int
	// PackageLargeURL / PackageMediumURL 是官方封套（pl.jpg / ps.jpg）。
	//
	// 哪一张是横版 sleeve 未实测（规格 §13 的几何悬案），两张都留着，
	// 由整理票的探针取「宽>高」那张右裁 2:3。
	PackageLargeURL  string
	PackageMediumURL string
	// SampleURLs 是预览集（jp-N.jpg），落 extrafanart。
	SampleURLs []string
	Actresses  []Actress
	Directors  []string
	Series     string
	Maker      string
	Label      string
	Genres     []string
	// ReviewAverage 是 5 分制的平均分；0 表示没有评价（不写 rating）。
	ReviewAverage float64
}

// Result 是一次「番号 → 内容」的答案。
//
// Found=false 是**正常答案**（无码/FC2/素人，或站点没有这一条）：调用方按字段回退
// JavDB，不记故障。SearchRaw/ContentRaw 是跑过的腿的响应原文，进 raw_snapshot。
type Result struct {
	Found      bool
	Content    Content
	SearchRaw  json.RawMessage
	ContentRaw json.RawMessage
}

// Lookup 取一部作品的 DMM 内容单：先发现（1–2 条搜索腿），再读内容（1 条）。
func (c *Client) Lookup(ctx context.Context, number string) (Result, error) {
	if strings.TrimSpace(number) == "" {
		return Result{}, &Error{Code: settings.CodeBusiness, Op: "lookup", Detail: "读 DMM 需要番号"}
	}

	var (
		result Result
		legs   []json.RawMessage
	)
	for _, term := range searchTerms(number) {
		raw, err := c.post(ctx, searchQuery(term))
		if err != nil {
			return Result{}, err
		}
		// 每跑过一条腿都留一份原文：没命中时两种拼写的答案一起证明
		// 「站点真的没有这一条」，重刮对比也看得到站点回了什么（规格 §3）。
		legs = append(legs, raw)
		result.SearchRaw = searchLegs(legs)

		id, err := matchID(raw, number)
		if err != nil {
			return Result{}, err
		}
		if id == "" {
			continue
		}
		return c.content(ctx, id, raw)
	}
	return result, nil
}

// content 是内容腿：一次 POST 读 ppvContent + reviewSummary。
func (c *Client) content(ctx context.Context, id string, searchRaw json.RawMessage) (Result, error) {
	raw, err := c.post(ctx, contentQuery(id))
	if err != nil {
		return Result{SearchRaw: searchRaw}, err
	}

	var payload struct {
		Data struct {
			PPVContent    *ppvContent    `json:"ppvContent"`
			ReviewSummary *reviewSummary `json:"reviewSummary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Result{SearchRaw: searchRaw, ContentRaw: raw},
			protocolError("content", "内容单不是 JSON: "+err.Error(), err)
	}
	entry := payload.Data.PPVContent
	if entry == nil {
		// id 刚刚解析出来、现在什么都没有：站点说「没这一条」。这是正常缺失。
		return Result{SearchRaw: searchRaw, ContentRaw: raw}, nil
	}

	return Result{
		Found:      true,
		Content:    contentOf(entry, payload.Data.ReviewSummary),
		SearchRaw:  searchRaw,
		ContentRaw: raw,
	}, nil
}

// ppvContent 是内容单的线格式（只取本仓要用的字段，改名/删字段时只改这一处）。
type ppvContent struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	DeliveryStart   string `json:"deliveryStartDate"`
	MakerReleasedAt string `json:"makerReleasedAt"`
	DurationSeconds int    `json:"duration"`

	PackageImage struct {
		LargeURL  string `json:"largeUrl"`
		MediumURL string `json:"mediumUrl"`
	} `json:"packageImage"`
	SampleImages []struct {
		LargeImageURL string `json:"largeImageUrl"`
	} `json:"sampleImages"`
	Actresses []struct {
		Name     string `json:"name"`
		ImageURL string `json:"imageUrl"`
	} `json:"actresses"`
	Directors []struct {
		Name string `json:"name"`
	} `json:"directors"`
	Series struct {
		Name string `json:"name"`
	} `json:"series"`
	Maker struct {
		Name string `json:"name"`
	} `json:"maker"`
	Label struct {
		Name string `json:"name"`
	} `json:"label"`
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres"`
}

type reviewSummary struct {
	Average float64 `json:"average"`
}

// contentOf 把线格式摊成 Content。空字段保持空：字段级回退归 scrape 的字段图，
// 不在这里替它决定「没有就换一个」。
func contentOf(entry *ppvContent, review *reviewSummary) Content {
	content := Content{
		ID:               strings.TrimSpace(entry.ID),
		Title:            strings.TrimSpace(entry.Title),
		Description:      strings.TrimSpace(entry.Description),
		ReleaseDate:      dateOf(entry.DeliveryStart, entry.MakerReleasedAt),
		DurationSeconds:  entry.DurationSeconds,
		PackageLargeURL:  strings.TrimSpace(entry.PackageImage.LargeURL),
		PackageMediumURL: strings.TrimSpace(entry.PackageImage.MediumURL),
		Series:           strings.TrimSpace(entry.Series.Name),
		Maker:            strings.TrimSpace(entry.Maker.Name),
		Label:            strings.TrimSpace(entry.Label.Name),
	}
	for _, sample := range entry.SampleImages {
		if url := strings.TrimSpace(sample.LargeImageURL); url != "" {
			content.SampleURLs = append(content.SampleURLs, url)
		}
	}
	for _, actress := range entry.Actresses {
		if name := strings.TrimSpace(actress.Name); name != "" {
			content.Actresses = append(content.Actresses, Actress{Name: name, ImageURL: strings.TrimSpace(actress.ImageURL)})
		}
	}
	for _, director := range entry.Directors {
		if name := strings.TrimSpace(director.Name); name != "" {
			content.Directors = append(content.Directors, name)
		}
	}
	for _, genre := range entry.Genres {
		if name := strings.TrimSpace(genre.Name); name != "" {
			content.Genres = append(content.Genres, name)
		}
	}
	if review != nil && review.Average > 0 {
		content.ReviewAverage = review.Average
	}
	return content
}

// post 发一次 GraphQL 请求，返回响应原文。
//
// 五个硬头是**必须**的：站点会拒掉不像自家播放器的请求。Origin 从不发（研究文档：
// 是否接受未知，所以不试）。错误先看信封再不看状态码 —— GraphQL 的失败是
// HTTP 200 + `errors` 键。
func (c *Client) post(ctx context.Context, query string) (json.RawMessage, error) {
	raw, err := c.send(ctx, query)
	metrics.ObserveUpstream(c.metrics, metrics.UpstreamDMM, err, IsPermanent)
	return raw, err
}

// send 是 post 去掉计量后的那一半。
func (c *Client) send(ctx context.Context, query string) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, protocolError("graphql", "拼请求体失败: "+err.Error(), err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api, bytes.NewReader(body))
	if err != nil {
		return nil, protocolError("graphql", "构造请求失败: "+err.Error(), err)
	}
	req.Header.Set("Referer", c.referer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Fanza-Device", "BROWSER")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transportError("graphql", ctx.Err(), err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, transportError("graphql", ctx.Err(), fmt.Errorf("读响应: %w", err))
	}

	if err := graphQLError(raw); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, protocolError("graphql", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200)), nil)
	}
	if !json.Valid(raw) {
		return nil, protocolError("graphql", "响应不是 JSON: "+truncate(string(raw), 200), nil)
	}
	return json.RawMessage(raw), nil
}

// graphQLError 检查 HTTP 200 响应体里的 `errors` 键。
//
// 这是 DMM 表达失败的常态（HTTP 200 + errors），所以它必须先于任何数据解析：
// 把 errors 当成「没命中」会让 schema 漂移永久静默成 JavDB 元数据。
func graphQLError(raw []byte) error {
	var payload struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Errors) == 0 {
		return nil
	}
	message := payload.Errors[0].Message
	if isRegionMessage(message) {
		return &Error{
			Code:   settings.CodeProtocol,
			Op:     "graphql",
			Detail: "DMM 拒绝了这次请求：地区阻断（" + message + "）—— DMM 只服务日本出口，检查部署的出口路由",
			Region: true,
		}
	}
	return protocolError("graphql", "DMM 回了 GraphQL 错误："+message, nil)
}

// isRegionMessage 是研究文档记录过的两种地区拒绝拼写。
func isRegionMessage(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "not-available-in-your-region") ||
		strings.Contains(lower, "not available in your region")
}

// searchQuery 拼发现腿的查询。
//
// 值**内插进查询文本**而不是走 GraphQL 变量：端点没有文档，变量类型是猜的，
// 猜错就是服务端 schema 错误；内插是野外验证过的形状。值经 JSON 转义，
// 「不能含引号」只是第二道防线。
func searchQuery(term string) string {
	return fmt.Sprintf(`query SearchPPV {
  legacySearchPPV(limit: %d, floor: AV, sort: SALES_RANK_SCORE, queryWord: %s) {
    result {
      contents { id }
      pageInfo { totalCount }
    }
  }
}`, searchLeg, jsonString(term))
}

// contentQuery 拼内容腿的查询：ppvContent + reviewSummary 一次 POST。
func contentQuery(id string) string {
	return fmt.Sprintf(`query ContentPageData {
  ppvContent(id: %s) {
    id
    floor
    title
    description
    deliveryStartDate
    makerReleasedAt
    duration
    packageImage { largeUrl mediumUrl }
    sampleImages { largeImageUrl }
    actresses { name imageUrl }
    directors { name }
    series { name }
    maker { name }
    label { name }
    genres { name }
  }
  reviewSummary(contentId: %s) { average total }
}`, jsonString(id), jsonString(id))
}

// jsonString 把值渲染成 JSON 字符串字面量（含引号）。
func jsonString(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return string(raw)
}

// searchLegs 把跑过的搜索腿原文按顺序组成一个 JSON 数组（每条仍原样内嵌）。
//
// 只留最后一条会让证据缺一半：第一种拼写没命中的样子、第二种命中的样子，
// 都是「为什么不走回退」的答案。
func searchLegs(legs []json.RawMessage) json.RawMessage {
	if len(legs) == 0 {
		return nil
	}
	raw, err := json.Marshal(legs)
	if err != nil {
		return legs[len(legs)-1]
	}
	return raw
}

// matchID 在搜索结果里找身份相同的那一条；找不到返回空串（不是错误）。
func matchID(raw json.RawMessage, number string) (string, error) {
	var payload struct {
		Data struct {
			LegacySearchPPV struct {
				Result struct {
					Contents []struct {
						ID string `json:"id"`
					} `json:"contents"`
				} `json:"result"`
			} `json:"legacySearchPPV"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", protocolError("search", "搜索结果不是 JSON: "+err.Error(), err)
	}
	for _, candidate := range payload.Data.LegacySearchPPV.Result.Contents {
		if candidate.ID != "" && sameWork(candidate.ID, number) {
			return candidate.ID, nil
		}
	}
	return "", nil
}

// searchTerms 是值得问站点的拼写，最具体的在前。
//
// product id = 厂牌前缀 + 序号，序号有两种拼法：S1 的第一部是 `ssis00001`，
// 之后都是 `ssis001NN`。搜索是子串匹配，所以 `ssis001` 找得到后来那 94 部、
// 找不到第一部，`ssis00001` 反过来。先问省零拼写，常见情况只花一次请求。
func searchTerms(number string) []string {
	reduced := strings.ToLower(candidateKey(number))
	prefix, value, ok := identity(number)
	if !ok {
		return []string{reduced}
	}
	if len(reduced)-len(prefix) >= paddedDigits {
		return []string{reduced}
	}
	count, err := strconv.Atoi(value)
	if err != nil {
		return []string{reduced}
	}
	padded := prefix + fmt.Sprintf("%0*d", paddedDigits, count)
	if padded == reduced {
		return []string{reduced}
	}
	return []string{reduced, padded}
}

// identity 把番号或 product id 归约成真正标识它的两个部分：厂牌前缀 + 序号的数值。
//
// 序号是**值**不是拼法：`SSIS-001` 与 id `ssis00001` 是同一条，归约后都是 `ssis1`。
// 直接比拼法正是「明明同一条却匹配不上」的来源。
func identity(s string) (prefix, value string, ok bool) {
	s = strings.ToLower(candidateKey(s))
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == len(s) || i == 0 {
		return "", "", false
	}
	digits := strings.TrimLeft(s[i:], "0")
	if digits == "" {
		digits = "0"
	}
	return s[:i], digits, true
}

// sameWork 报告一个候选 id 与番号是否同一部作品：前缀相同且数值相等。
func sameWork(id, number string) bool {
	idPrefix, idValue, ok := identity(id)
	if !ok {
		return false
	}
	numberPrefix, numberValue, ok := identity(number)
	if !ok {
		return false
	}
	return idPrefix == numberPrefix && idValue == numberValue
}

// candidateKey 是番号归一化：大写、去掉分隔符。
//
// 只去 `-`/`_`/空白，不做更激进的规整：看起来一样的番号之间的差异
// 恰恰是身份的一部分，抹掉它们等于把精确匹配退化成模糊匹配。
func candidateKey(number string) string {
	s := strings.ToUpper(strings.TrimSpace(number))
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '-', '_', ' ', '\t':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// dateOf 把站点的 RFC3339 时间归成 YYYY-MM-DD，deliveryStartDate 优先。
func dateOf(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if at, err := time.Parse(time.RFC3339, value); err == nil {
			return at.UTC().Format("2006-01-02")
		}
		if len(value) >= 10 {
			if _, err := time.Parse("2006-01-02", value[:10]); err == nil {
				return value[:10]
			}
		}
	}
	return ""
}

// Error 是一次 DMM 调用的失败，带规格 §8.4 的稳定机器码。
//
// Region 单独标出来：地区阻断是**部署出口**的问题，不是「站点没收录」，
// 调用方必须退避重试，不许把它降级成 JavDB 元数据。
type Error struct {
	Code   settings.TestCode
	Op     string
	Detail string
	Region bool
	Err    error
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	if e.Err != nil {
		return fmt.Sprintf("dmm %s: %v", e.Op, e.Err)
	}
	return "dmm " + e.Op
}

func (e *Error) Unwrap() error { return e.Err }

// IsRegionBlocked 报告这次失败是不是地区阻断（研究文档：blocked ≠ missing）。
func IsRegionBlocked(err error) bool {
	var dmmErr *Error
	return errors.As(err, &dmmErr) && dmmErr.Region
}

// IsPermanent 报告一次调用失败是否**永久**（规格 §9 的 permanent 钩子）。
//
// DMM 是匿名播放器 API，没有凭据可失效，所以它的失败（地区阻断、schema 漂移、
// 网络抖）在 settings.IsPermanentCode 的口径下全是瞬时——permanent 恒为 false。
// 这里仍然走同一个分类函数，好让「哪类错算永久」只有一个定义；将来 DMM 若引入
// 凭据类失败，这里不用改。
func IsPermanent(err error) bool {
	var dmmErr *Error
	if errors.As(err, &dmmErr) {
		return settings.IsPermanentCode(dmmErr.Code)
	}
	return false
}

func protocolError(op, detail string, err error) error {
	return &Error{Code: settings.CodeProtocol, Op: op, Detail: detail, Err: err}
}

func transportError(op string, ctxErr error, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctxErr, context.DeadlineExceeded) {
		return &Error{Code: settings.CodeTimeout, Op: op, Detail: op + " 超时: " + err.Error(), Err: err}
	}
	return &Error{Code: settings.CodeNetwork, Op: op, Detail: "连不上 DMM（" + op + "）: " + err.Error(), Err: err}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
