// Package appapi 是 JavDB 官方 App 私有 JSON API 的传输层。
//
// 整体移植自 javdb_rss（MIT License）的 internal/appapi（规格 §2 移植清单、§5.1）。
// 这一层刻意**只放实测确认过的传输事实**，不放任何业务规则：
//
//   - jdsignature 是 **HTTP 请求头**（不是查询参数）；
//   - 每次请求必须带 8 个公共查询参数，缺一即 ParameterInvalid；
//   - 响应信封为 {success, action, message, data}。
//
// 错误分类照 #16 的实测分层（规格 §5.1）：
//
//   - 业务校验错 = success:0 + action:null（zh-TW message）——不是凭据/传输问题；
//   - action 非空才是传输/凭据类错（InvalidSignature、JWTVerificationError…）。
//
// 因此判据是先看 success，再看 action，**不看 HTTP 状态码**。
package appapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/metrics"
)

// Identity 是每次请求都要带上的设备与版本信息。
//
// 这 8 个字段全部经实测确认：少任何一个，服务端返回
// `ParameterInvalid: Parameter cannot be empty: <第一个缺的字段>`。
type Identity struct {
	AppChannel       string
	AppVersion       string
	AppVersionNumber string
	Platform         string
	SystemVersion    string
	DeviceModel      string
	DeviceName       string
	DeviceUUID       string
}

// DefaultIdentity 返回一组能通过服务端校验的默认值。
//
// 这些值本身不是秘密，服务端也不校验它们与 token 的同源性 —— 但 device_uuid
// 是签名之外唯一可能被用来做设备指纹的字段，因此固定成一个常量，
// 好让一台实例长期保持同一个身份。
func DefaultIdentity() Identity {
	return Identity{
		AppChannel:       "official",
		AppVersion:       "1.9.35",
		AppVersionNumber: "10935",
		Platform:         "android",
		SystemVersion:    "13",
		DeviceModel:      "Pixel 6",
		DeviceName:       "Pixel",
		DeviceUUID:       "11111111-2222-3333-4444-555555555555",
	}
}

// values 把 Identity 摊成查询参数。
func (id Identity) values() url.Values {
	return url.Values{
		"app_channel":        {id.AppChannel},
		"app_version":        {id.AppVersion},
		"app_version_number": {id.AppVersionNumber},
		"platform":           {id.Platform},
		"system_version":     {id.SystemVersion},
		"device_model":       {id.DeviceModel},
		"device_name":        {id.DeviceName},
		"device_uuid":        {id.DeviceUUID},
	}
}

// Signer 产出 jdsignature 头的值。
//
// 这是一个刻意的接缝：签名常量派生自 App 内的 access key，App 升级或服务端轮换
// 都可能让整套签名作废（症状是 InvalidSignature）。抽成接口意味着将来换算法只需
// 换一个实现，而不是在传输层里到处找常量。
type Signer interface {
	// Sign 返回给定 Unix 秒对应的 jdsignature 值。ts <= 0 时用当前时间。
	Sign(ts int64) string
}

// APIError 是服务端在信封里报的业务错误（success:0）。
//
// Action 为空表示「业务校验错」（如名字过短、同名清单）——不是传输或凭据问题。
// Action 非空表示传输/凭据类错，由调用方按 action 再细分。
type APIError struct {
	Action  string
	Message string
	// Status 是 HTTP 状态码。同一个 action 在不同状态下含义不同，
	// 因此保留它供诊断；0 表示响应本来就是 200（信封失败路径）。
	Status int
}

func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("javdb api")
	if e.Status != 0 {
		fmt.Fprintf(&b, " (HTTP %d)", e.Status)
	}
	if e.Action != "" {
		fmt.Fprintf(&b, ": %s", e.Action)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	return b.String()
}

// IsBusiness 报告这是不是一次「业务校验错」：success:0 且 action 为空。
//
// 业务错不重试也不当作凭据问题——它是请求内容不合服务端规则（规格 §5.1）。
func (e *APIError) IsBusiness() bool { return e.Action == "" }

// ValidationError 表示请求在本地就被判为不合规（如清单名不在 4–30 字符），
// 根本没发出去。它归 business：要用户改输入，重试无用。
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// AuthError 表示请求被接受但**凭据有问题** —— 签名对了，token 缺失/过期/被顶下线。
//
// 单独成一型是因为它需要完全不同的处置：普通 APIError 是「这次没拿到数据」，
// AuthError 是「去重新导出 token」。规格 §5.1/§9：JWTVerificationError 归 auth，
// 并挂钩上游 permanent 指标。
type AuthError struct {
	Action  string
	Message string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("javdb api: 需要重新提供 token (%s): %s", e.Action, e.Message)
}

// TransportError 表示请求根本没走到上游：连接被拒、DNS、TLS、超时。
//
// 它与 ProtocolError 的区别就是处置：网络故障等一会儿重试，协议不一致要查代码。
type TransportError struct {
	Path string
	Err  error
}

func (e *TransportError) Error() string { return fmt.Sprintf("请求 %s: %v", e.Path, e.Err) }
func (e *TransportError) Unwrap() error { return e.Err }

// ProtocolError 表示上游回了响应，但**不是我们认得的标准信封**：
// 裸 HTTP 5xx（如 Cloudflare HTML）、信封解析失败、非 2xx 且无信封。
//
// 这类错意味着「服务端行为与预期不一致」，要查而不是盲重试。
type ProtocolError struct {
	Path    string
	Status  int
	Message string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("请求 %s 响应不符合信封（HTTP %d）: %s", e.Path, e.Status, e.Message)
}

// authActions 是实测与上游一致认定的「凭据有问题」错误名。
var authActions = map[string]bool{
	"JWTVerificationError": true,
	"Unauthorized":         true,
	"LoginRequired":        true,
	"TokenInvalid":         true,
	"TokenExpired":         true,
}

// Client 是一个带签名的 App API 客户端。
type Client struct {
	// Host 形如 https://jdforrepam.com，结尾斜杠会被去掉。
	Host string
	// Token 是用户手工从 App 导出的 JWT。空字符串表示匿名请求。
	Token string
	// Identity 是设备与版本信息。
	Identity Identity
	// Signer 产出 jdsignature。
	Signer Signer
	// Lang 是 accept-language，影响服务端返回的文案语言与部分字段。
	Lang string
	// HTTP 允许注入测试用的 transport。为零值时使用带超时的默认客户端。
	HTTP *http.Client
	// Metrics 记每次请求的结局（规格 §9 的 javdb 上游）；nil 时什么都不记。
	Metrics metrics.Recorder

	// sleep 是重试之间的等待，零值时用 time.Sleep。测试用它把退避压成 0。
	sleep func(time.Duration)
}

// defaultTransport 是所有 Client 共用的一份 Transport。
//
// 为什么必须共用：早先 httpClient() 每次调用都 new 一个 http.Client，等于每个请求
// 配一份空连接池 —— 于是每条 javdb 请求都得重新走一次 TLS 握手。实测（2026-11，
// 见 docs/javdb-app-traffic.md）这台机器到上游各镜像域名的握手有 3–35% 的阵发失败率
// （连接被断，一个响应字节都没有），共用连接池把这类失败从「每请求一次」摊成「每连接一次」，
// 顺便拿到 net/http 对闲置连接被复用时的自动重试。
var defaultTransport = &http.Transport{
	Proxy:               http.ProxyFromEnvironment,
	DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	ForceAttemptHTTP2:   true,
	MaxIdleConns:        8,
	MaxIdleConnsPerHost: 8,
	IdleConnTimeout:     90 * time.Second,
	TLSHandshakeTimeout: 10 * time.Second,
}

// defaultHTTPClient 与 defaultTransport 一起构成零值 Client 的默认出口。
var defaultHTTPClient = &http.Client{Timeout: 20 * time.Second, Transport: defaultTransport}

// 握手期断链期间（上文的 3–35% 窗口）重打两次能把一轮的失败概率压到 ~1%。
// 上限刻意小：javdb 有并发自帽（规格 §5.1），重试不能变成放大流量。
const (
	maxTransportRetries = 2
	retryBase           = 400 * time.Millisecond
)

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTPClient
}

// retryable 报告这次失败值不值得重打：只有「幂等、无请求体的 GET」上的传输错
// （连接被断、DNS、TLS 握手）才重打。
//
// 其余一律不重打：POST /api/v1/lists 建清单不幂等；业务错与协议错是「要改东西」，
// 立刻返回原文比藏着掖着更有用；ctx 已经取消时重打只会白等。
func retryable(req *http.Request, err error) bool {
	if req.Method != http.MethodGet || req.Body != nil {
		return false
	}
	if req.Context().Err() != nil {
		return false
	}
	var te *TransportError
	return errors.As(err, &te)
}

// envelope 是服务端的响应信封。
//
// success 在实测中出现过 0/1 两种数值形态，上游还见过布尔形态，
// 因此用 any 接住再自行判断。
type envelope struct {
	Success any             `json:"success"`
	Action  string          `json:"action"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// successTruthy 判断信封里的 success 是否表示成功。
//
// 实测成功返回 {"success":1,...}、失败返回 {"success":0,...}。
// 同时接受布尔与字符串形态，因为这个字段的类型没有被固定下来。
func successTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	default:
		return true
	}
}

// GetJSON 发一次签名的 GET，并把信封里的 data 解进 dest。
//
// dest 为 nil 时只校验成功与否，不解析负载。
func (c *Client) GetJSON(ctx context.Context, path string, params url.Values, dest any) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, params, nil)
	if err != nil {
		return err
	}
	raw, err := c.do(req, path)
	if err != nil {
		return err
	}
	return decodeInto(path, raw, dest)
}

// PostFormJSON 发一次签名的 POST（application/x-www-form-urlencoded）。
//
// 建清单走的就是这条：`POST /api/v1/lists` 收的是表单，不是 JSON（JSON 被拒）。
func (c *Client) PostFormJSON(ctx context.Context, path string, form url.Values, dest any) error {
	req, err := c.newRequest(ctx, http.MethodPost, path, nil, form)
	if err != nil {
		return err
	}
	raw, err := c.do(req, path)
	if err != nil {
		return err
	}
	return decodeInto(path, raw, dest)
}

// newRequest 组装一次签名请求：公共参数进 query，jdsignature 进头。
//
// GET 与 POST 的区别只是 body 与 method，其余（签名、公共参数、UA）完全一致 ——
// 因此只在这里写一遍。
func (c *Client) newRequest(ctx context.Context, method, path string, params, form url.Values) (*http.Request, error) {
	q := c.Identity.values()
	for k, vs := range params {
		for _, v := range vs {
			q.Set(k, v)
		}
	}

	u := strings.TrimRight(c.Host, "/") + path
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}

	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("构造请求: %w", err)
	}
	if form != nil {
		req.Header.Set("content-type", "application/x-www-form-urlencoded")
	}

	// jdsignature 是请求头 —— 这一条是实测确认的，早期把它当查询参数会得到 ParameterInvalid。
	req.Header.Set("jdsignature", c.Signer.Sign(time.Now().Unix()))
	req.Header.Set("user-agent", "Dart/3.4 (dart:io)")
	req.Header.Set("connection", "keep-alive")
	if c.Lang != "" {
		req.Header.Set("accept-language", c.Lang)
	}
	if c.Token != "" {
		req.Header.Set("authorization", "Bearer "+c.Token)
	}
	return req, nil
}

// do 发请求并把响应体解析成信封，返回其中的 data。
//
// 上游计量挂在这一次出口上：一条逻辑请求记一条（重试算同一条），结局按 IsPermanent
// 分流（auth = 凭据失效、protocol = 签名/信封不兼容都算永久；其余等一会儿就好）。
func (c *Client) do(req *http.Request, path string) (json.RawMessage, error) {
	raw, err := c.sendWithRetry(req, path)
	metrics.ObserveUpstream(c.Metrics, metrics.UpstreamJavDB, err, IsPermanent)
	return raw, err
}

// sendWithRetry 是 do 的重试外壳：传输错按 retryBase 翻倍退避后重打，最多两次。
//
// 重试只发生在「一个响应字节都没收到」的情况下（见 retryable），所以不会出现
// 「上游其实已经处理了、我们又打一遍」的问题。返回的永远是最后一次的结局。
func (c *Client) sendWithRetry(req *http.Request, path string) (json.RawMessage, error) {
	raw, err := c.send(req, path)
	for attempt := 1; retryable(req, err) && attempt <= maxTransportRetries; attempt++ {
		c.wait(attempt)
		raw, err = c.send(req, path)
	}
	return raw, err
}

// wait 睡一个重试间隔：retryBase、2·retryBase、…
func (c *Client) wait(attempt int) {
	d := retryBase
	for i := 1; i < attempt; i++ {
		d *= 2
	}
	if c.sleep != nil {
		c.sleep(d)
		return
	}
	time.Sleep(d)
}

// send 是 do 去掉计量后的那一半。
func (c *Client) send(req *http.Request, path string) (json.RawMessage, error) {
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, &TransportError{Path: path, Err: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, &TransportError{Path: path, Err: fmt.Errorf("读取响应: %w", err)}
	}

	// 先尽力解析信封，**不管 HTTP 状态码**。
	//
	// 这一点是实测逼出来的：签名值无效时服务端返回 HTTP 400，但响应体仍然是标准信封。
	//
	//   签名缺失  → HTTP 200 + {"success":0,"action":"ParameterInvalid"}
	//   签名无效  → HTTP 400 + {"success":0,"action":"InvalidSignature"}
	//
	// 如果按状态码提前返回一个字符串错误，action 就丢了 —— 而 action 正是
	// 区分「签名坏了，要改代码」与「网络抖了，等会儿重试」的唯一依据。
	var env envelope
	// 判据是「success 字段存在」而不是「action 非空」——
	// 成功响应里 action 就是 null，用后者会把每条成功响应都误判成解析失败。
	envelopeOK := json.Unmarshal(body, &env) == nil && env.Success != nil

	if envelopeOK && !successTruthy(env.Success) {
		if authActions[env.Action] {
			return nil, &AuthError{Action: env.Action, Message: env.Message}
		}
		return nil, &APIError{Action: env.Action, Message: env.Message, Status: resp.StatusCode}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &ProtocolError{Path: path, Status: resp.StatusCode, Message: truncate(string(body), 200)}
	}
	if !envelopeOK {
		return nil, &ProtocolError{Path: path, Status: resp.StatusCode, Message: "信封解析失败: " + truncate(string(body), 200)}
	}

	if len(env.Data) == 0 || string(env.Data) == "null" {
		return nil, nil
	}
	return env.Data, nil
}

func decodeInto(path string, raw json.RawMessage, dest any) error {
	if dest == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("解析 %s 负载: %w", path, err)
	}
	return nil
}

// IsAuthError 报告 err 链上是否有凭据类错误。
func IsAuthError(err error) bool {
	var ae *AuthError
	return errors.As(err, &ae)
}

// IsBusinessError 报告 err 是否是业务校验错（success:0 + action:null）。
func IsBusinessError(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.IsBusiness()
}

// 稳定的错误分类码（与 settings 的测试错误码同名字面）。
//
// 单一真相：javdb 的测试 chip 与 /api/javdb/* 的 HTTP 错误都用这一套，
// 免得同一个分类在两处各写一遍 switch、将来新增一种错只改到其中一处。
const (
	CodeAuth     = "auth"
	CodeBusiness = "business"
	CodeProtocol = "protocol"
	CodeNetwork  = "network"
)

// Classify 把一次上游错误归成稳定分类码。
//
// 分层照 #16：
//
//	AuthError        → auth（凭据无效/被踢）
//	业务错(action 空) → business（success:0，如清单同名拒/名字过短）
//	ProtocolError    → protocol（非预期响应/信封）
//	其它 APIError     → protocol（含 InvalidSignature：改代码，不是换 token）
//	其余             → network（连接/超时等，等一会儿重试）
func Classify(err error) string {
	var authErr *AuthError
	if errors.As(err, &authErr) {
		return CodeAuth
	}
	var validation *ValidationError
	if errors.As(err, &validation) {
		return CodeBusiness
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.IsBusiness() {
			return CodeBusiness
		}
		return CodeProtocol
	}
	var protoErr *ProtocolError
	if errors.As(err, &protoErr) {
		return CodeProtocol
	}
	return CodeNetwork
}

// IsPermanent 报告一次上游错误是不是**永久**的（重试无用，要人介入/改代码）。
//
// 这是规格 §9 的上游指标钩子：`mdc_upstream_requests_total{result="permanent"}`
// 用它判定，而 `UpstreamCredentialsInvalid` 只数 permanent —— 所以 5xx/429 必须
// 留在 transient 里：一次 Cloudflare 502 不该伪装成「去换 token」。
//
// permanent = 凭据失效（auth，如 JWTVerificationError）或协议/签名不兼容
// （protocol，如 InvalidSignature）；network 与 5xx/429 是 transient。
func IsPermanent(err error) bool {
	switch Classify(err) {
	case CodeAuth:
		return true
	case CodeProtocol:
		var protoErr *ProtocolError
		if errors.As(err, &protoErr) &&
			(protoErr.Status >= 500 || protoErr.Status == http.StatusTooManyRequests) {
			return false
		}
		return true
	default:
		return false
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
