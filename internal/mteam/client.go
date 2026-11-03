package mteam

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/metrics"
)

const (
	// originHeader 是站点对 CORS 的校验要求（先例项目都带它）。
	originHeader = "https://kp.m-team.cc/"
	// userAgent 是本服务对 M-Team 的 User-Agent。
	//
	// ⚠️ 它**必须非空、且不能含 `curl`**：实测 Cloudflare 会把这两类请求 302 到
	// www.google.com（规格 §5.2）。Go 的默认 UA 恰好能过，但「恰好能过」不是设计 ——
	// 显式写一个自己的，免得哪天换了 HTTP 栈就莫名其妙被拦。
	userAgent = "oh-my-mdc"
	// searchPageSize 是常规搜索的每页条数（规格 §5.2：pageSize≤200）。
	searchPageSize = 100
	// maxBody 是读响应体的上限。种子可以到几百 KB，给足余量；它存在的意义
	// 是别让一个坏响应把内存吃光。
	maxBody = 16 << 20
	// defaultHTTPTimeout 是单次调用的上限。测试连接另有 10s 的总预算（settings.TestTimeout）。
	defaultHTTPTimeout = 30 * time.Second
	// maxRedirects 是下载链接最多允许的重定向跳数（与 net/http 的默认值一致）。
	maxRedirects = 10
)

// client 是一个带 api key 的 M-Team 客户端。
//
// 只放实测确认过的传输事实（规格 §5.2）：鉴权头、200+code 信封、
// 每个端点的编码（search 是 JSON，genDlToken 是 form）、两种重定向策略。
type client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	metrics metrics.Recorder
}

// search 按番号搜一次（`mode` 固定 adult）。
func (c *client) search(ctx context.Context, keyword string, pageSize int) ([]Hit, error) {
	payload, err := json.Marshal(map[string]any{
		"mode":       "adult",
		"keyword":    keyword,
		"pageNumber": 1,
		"pageSize":   pageSize,
		"visible":    1,
	})
	if err != nil {
		return nil, fmt.Errorf("构造搜索请求: %w", err)
	}

	var data searchData
	if err := c.do(ctx, "/api/torrent/search", "application/json", bytes.NewReader(payload), &data); err != nil {
		return nil, err
	}

	hits := make([]Hit, 0, len(data.Rows))
	for _, row := range data.Rows {
		size, _ := strconv.ParseInt(row.Size, 10, 64)
		seeders, _ := strconv.Atoi(row.Status.Seeders)
		hits = append(hits, Hit{
			ID:        row.ID,
			Name:      row.Name,
			SizeBytes: size,
			Seeders:   seeders,
			Discount:  row.Status.Discount,
			Labels:    row.LabelsNew,
		})
	}
	return hits, nil
}

// download 取回某个种子的字节：先换下载链接，再取它。
//
// 两步合在一起，是因为调用方要的就是「这份种子」—— 中间那个链接是**五分钟就过期**
// 的实现细节，暴露出去只会让调用方有机会存错东西。
func (c *client) download(ctx context.Context, id string) ([]byte, error) {
	// ⚠️ 这一步要 **form**，不是 JSON —— 给 JSON 会得到「參數錯誤」。
	// 站点自己的 wiki 就承认文档里 json/form 的说明不可信（规格 §5.2）。
	form := url.Values{"id": {id}}
	var link string
	if err := c.do(ctx, "/api/torrent/genDlToken",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()), &link); err != nil {
		return nil, err
	}
	if strings.TrimSpace(link) == "" {
		return nil, protocolError("genDlToken", "genDlToken 没有给出下载链接", nil)
	}
	return c.fetch(ctx, link)
}

// fetch 取下载链接指向的字节。
//
// 单独一个方法而不是复用 do，有两处**必须**不同：
//
//  1. 它返回的不是 JSON 信封；
//  2. ⭐ 它**跟随重定向**，而 API 端点不跟。genDlToken 给的链接会 302 到一个 CDN，
//     真正的种子在那一跳之后；而 API 端点恰好相反 —— 那边任何 302 都是
//     Cloudflare 按 UA 拦人。两边共用一个策略的话，必然有一边是错的。
func (c *client) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	raw, err := c.fetchOnce(ctx, rawURL)
	metrics.ObserveUpstream(c.metrics, metrics.UpstreamMTeam, err, IsPermanent)
	return raw, err
}

// fetchOnce 是 fetch 去掉计量后的那一半。
func (c *client) fetchOnce(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, protocolError("download", "构造取种子的请求失败: "+err.Error(), err)
	}
	c.setHeaders(req, "")
	stripCredentialFor(req, c.apiHost())

	resp, err := c.cdnClient().Do(req)
	if err != nil {
		return nil, transportError("download", ctx.Err(), err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, protocolError("download",
			fmt.Sprintf("取种子回 HTTP %d（下载链接可能已过期：它只有约 5 分钟时效）", resp.StatusCode), nil)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, transportError("download", ctx.Err(), err)
	}

	// 种子是 bencode，以 'd' 开头；错误信封是 JSON，以 '{' 开头。
	// 用这个区分而不是看 Content-Type：实测过期时 Content-Type 仍然是
	// application/x-bittorrent，只有体变了。
	if len(raw) == 0 {
		return nil, protocolError("download", "取种子失败：M-Team 返回了空响应", nil)
	}
	if raw[0] == '{' {
		if err := classifyEnvelope(raw, "download"); err != nil {
			return nil, err
		}
		return nil, protocolError("download", "取种子失败：M-Team 返回了 JSON 而不是种子: "+preview(raw), nil)
	}
	return raw, nil
}

// do 发一次 POST，把成功响应的 data 解进 out。
//
// 这是 API 端点的上游计量出口：每次真正发出的请求记一条（结局按 IsPermanent 分流）。
func (c *client) do(ctx context.Context, path, contentType string, body io.Reader, out any) error {
	err := c.post(ctx, path, contentType, body, out)
	metrics.ObserveUpstream(c.metrics, metrics.UpstreamMTeam, err, IsPermanent)
	return err
}

// post 是 do 去掉计量后的那一半。
func (c *client) post(ctx context.Context, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+path, body)
	if err != nil {
		return protocolError(path, "构造请求失败: "+err.Error(), err)
	}
	c.setHeaders(req, contentType)

	resp, err := c.apiClient().Do(req)
	if err != nil {
		return transportError(path, ctx.Err(), err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := classifyStatus(resp, path); err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return transportError(path, ctx.Err(), err)
	}
	if err := classifyEnvelope(raw, path); err != nil {
		return err
	}

	// 只解 data：信封本身已经校验过了。
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return protocolError(path, "响应不是合法 JSON: "+preview(raw), err)
	}
	if out == nil || len(envelope.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return protocolError(path, "响应的 data 形状不认识: "+err.Error(), err)
	}
	return nil
}

// setHeaders 装上每个请求都要带的头。
//
// ⚠️ 三样都不能少：`x-api-key` 是鉴权，`User-Agent` 是躲 WAF（见 userAgent），
// `Origin` 是站点对 CORS 的校验。
func (c *client) setHeaders(req *http.Request, contentType string) {
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Origin", originHeader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
}

// classifyStatus 把 HTTP 状态码映射成错误。
//
// 重定向单独成一类：M-Team 的 API 从不合法地重定向，而实测的 302 是 Cloudflare
// 按 UA 拦人（Location 指向 www.google.com）。把它当成功会让我们拿着 google 的
// 页面往下走，排查方向也就跟着错掉。
func classifyStatus(resp *http.Response, op string) error {
	status := resp.StatusCode
	if status >= 300 && status < 400 {
		return protocolError(op, fmt.Sprintf(
			"API 端点回了 HTTP %d（Location=%q）—— 这是 Cloudflare 按 User-Agent 拦人，不是鉴权失败",
			status, resp.Header.Get("Location")), nil)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return authError(op, fmt.Sprintf("%s 回 HTTP %d：api key 被拒", op, status), nil)
	}
	if status < 200 || status >= 300 {
		return protocolError(op, fmt.Sprintf("%s 回 HTTP %d", op, status), nil)
	}
	return nil
}

// classifyEnvelope 检查响应信封，并在失败时分类（规格 §5.2 / P5-4）。
//
// ⚠️ **失败也是 HTTP 200。** 实测 key 无效时是 `200 + {"code":"1","message":"key無效"}`，
// 完全没带 key 时是 `200 + {"code":"401",…}`。只看状态码的实现会把它当成功，
// 然后在解析 data 时莫名其妙地炸 —— 这就是这里必须看 code 的理由。
//
// 分类靠 message 的原话 —— 那是唯一可用的信号，因此**认不出来的一律归 protocol**：
// 猜错分类比不分类更坏（告警会说「去换 key」而真相是别的）。
func classifyEnvelope(raw []byte, op string) error {
	var envelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return protocolError(op, "响应不是合法 JSON: "+preview(raw), err)
	}
	if envelope.Code == "0" {
		return nil
	}

	switch {
	case envelope.Code == "401", strings.Contains(envelope.Message, "key無效"):
		return authError(op, "M-Team 拒绝了 api key（"+envelope.Message+"）：去站点【控制台 → 實驗室 → 存取令牌】换一个", nil)
	case strings.Contains(envelope.Message, "超出有效期"), strings.Contains(envelope.Message, "不可用"):
		return protocolError(op, "M-Team 下载链接已过期（"+envelope.Message+"）", nil)
	default:
		return protocolError(op, fmt.Sprintf("M-Team 返回失败（code=%s）：%s", envelope.Code, envelope.Message), nil)
	}
}

// base 返回去掉末尾斜杠的入口地址。
func (c *client) base() string {
	return strings.TrimRight(c.baseURL, "/")
}

// apiHost 是 API 入口的 host：只有发给它的请求才带存取令牌。
func (c *client) apiHost() string {
	parsed, err := url.Parse(c.base())
	if err != nil {
		return ""
	}
	return parsed.Host
}

// stripCredentialFor 在请求不是发给 API 入口时去掉存取令牌与 CORS 头。
//
// Go 只自动剥 Authorization / Cookie 这类标准头；x-api-key 是自定义头，
// 不显式去掉就会被原样送到 CDN 域名上 —— 一条下载链接不该拿到存取令牌。
func stripCredentialFor(req *http.Request, apiHost string) {
	if apiHost == "" || req.URL.Host == apiHost {
		return
	}
	req.Header.Del("x-api-key")
	req.Header.Del("Origin")
}

// apiClient 返回**API 端点**要用的客户端：绝不跟随重定向（理由见 classifyStatus）。
//
// 注入的客户端（测试）也要遵守这一条，因此在这里覆写：策略是本包的契约，
// 不是调用方的自由选择。
func (c *client) apiClient() *http.Client {
	cp := c.baseClient()
	cp.CheckRedirect = noRedirect
	return cp
}

// cdnClient 返回**下载链接**要用的客户端：跟随重定向（那一跳是 CDN，不是拦截）。
//
// 跨主机的那一跳会去掉 x-api-key 与 Origin：Go 只自动剥 Authorization / Cookie
// 这类标准头，自定义头会原样带到 CDN 域名上 —— 一条下载链接不该拿到存取令牌。
func (c *client) cdnClient() *http.Client {
	cp := c.baseClient()
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("下载链接重定向超过 %d 跳", maxRedirects)
		}
		stripCredentialFor(req, c.apiHost())
		return nil
	}
	return cp
}

// baseClient 是两者共用的基底（复制注入的客户端，不原地改它）。
func (c *client) baseClient() *http.Client {
	if c.http == nil {
		return &http.Client{Timeout: defaultHTTPTimeout}
	}
	cp := *c.http
	return &cp
}

// preview 截一小段响应体用于报错。
//
// 场景很具体：被 WAF 拦下来时响应是 HTML，而「不是合法 JSON」这句话单独看不出
// 发生了什么 —— 带上开头几十字节，人一眼就知道拿到的是网页。
func preview(raw []byte) string {
	return truncate(strings.TrimSpace(string(raw)), 120)
}

// searchData 是搜索响应的 data 面（分页信封里再套一层 data）。
type searchData struct {
	Rows []searchRow `json:"data"`
}

// searchRow 是搜索结果的一行线上形状。
//
// ⚠️ size 与 seeders 线上是**字符串**（"130736"），createdDate 是
// "2021-02-22 15:19:51"。infoHash 恒为 null —— 所以本包从不读它。
type searchRow struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Size      string   `json:"size"`
	LabelsNew []string `json:"labelsNew"`
	Status    struct {
		Seeders  string `json:"seeders"`
		Discount string `json:"discount"`
	} `json:"status"`
}
