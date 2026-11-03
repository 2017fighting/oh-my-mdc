package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/metrics"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// client 是一个已带凭据的 Jellyfin API 客户端。
//
// 只放实测确认过的传输事实：每请求带 X-Emby-Token（规格 §5.5 点名的鉴权头）、
// 地址可以带子路径（反向代理的 /jellyfin 前缀）而端点仍拼在它下面。
type client struct {
	base    *url.URL
	http    *http.Client
	token   string
	metrics metrics.Recorder
}

func newClient(cfg Config, httpClient *http.Client, recorder metrics.Recorder) (*client, error) {
	raw := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	base, err := url.Parse(raw)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, ValidationError{Message: "Jellyfin 地址需要 http(s) 地址: " + raw}
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &client{base: base, http: httpClient, token: strings.TrimSpace(cfg.APIKey), metrics: recorder}, nil
}

// systemInfo 读 GET /System/Info：规格 §5.5 把「带 token 读得到」定为「可写」判定。
//
// 这个端点只要被接受的凭据就能读（不额外验管理员），向导期望的输入是管理员 API key；
// 权限不足的凭据会在第一次真实刷新时以 auth 错暴露，走 §9 的 permanent 指标。
func (c *client) systemInfo(ctx context.Context) (SystemInfo, error) {
	status, body, err := c.get(ctx, "/System/Info")
	if err != nil {
		return SystemInfo{}, err
	}
	if err := c.accept("System/Info", status, body); err != nil {
		return SystemInfo{}, err
	}

	var info SystemInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return SystemInfo{}, protocolError("System/Info", "System/Info 回执不是 JSON: "+truncate(string(body), 120), err)
	}
	return info, nil
}

// mediaUpdate 是 POST /Library/Media/Updated 的一个目标路径（Jellyfin 的 MediaUpdateInfoPathDto）。
type mediaUpdate struct {
	Path       string `json:"Path"`
	UpdateType string `json:"UpdateType"`
}

// updatedMedia 报一条路径的新增（目录级 rescan，规格 §5.5）。
//
// 回执只有 2xx 与「已受理」两件事，不解析 body：Jellyfin 这个端点对未知路径也回 204
// （实测陷阱），所以 2xx 不是「目录真的被扫了」的证据。
func (c *client) updatedMedia(ctx context.Context, path string) error {
	payload, err := json.Marshal(map[string][]mediaUpdate{
		"Updates": {{Path: path, UpdateType: "Created"}},
	})
	if err != nil {
		return protocolError("Library/Media/Updated", "拼刷新请求体失败: "+err.Error(), err)
	}
	status, body, err := c.post(ctx, "/Library/Media/Updated", payload)
	if err != nil {
		return err
	}
	return c.accept("Library/Media/Updated", status, body)
}

// refreshLibrary 触发一次全库扫描（目录级被拒时的降级，P11-2）。
func (c *client) refreshLibrary(ctx context.Context) error {
	status, body, err := c.post(ctx, "/Library/Refresh", nil)
	if err != nil {
		return err
	}
	return c.accept("Library/Refresh", status, body)
}

// get 发一次读请求。
func (c *client) get(ctx context.Context, path string) (int, []byte, error) {
	return c.do(ctx, http.MethodGet, path, nil)
}

// post 发一次写请求；body 为 nil 时没有请求体（/Library/Refresh 就是这样）。
func (c *client) post(ctx context.Context, path string, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	return c.do(ctx, http.MethodPost, path, reader)
}

// do 是一次 Jellyfin 调用的公共部分：拼地址、带 token、读完响应体。
//
// 它只负责「请求有没有发出去、回没回来」，非 2xx 的含义留给调用方。
// 上游计量也在这里：每个请求一条。
func (c *client) do(ctx context.Context, method, path string, body io.Reader) (int, []byte, error) {
	status, raw, err := c.send(ctx, method, path, body)
	c.observe(status, err)
	return status, raw, err
}

// observe 记一次请求的结局（规格 §9 的 jellyfin 上游）。
//
// 401/403 是凭据类永久错（token 被拒或权限不够）；429/5xx 与传输错是瞬时；
// 其余 4xx 在 Jellyfin 是「这次请求它不收」（protocol），按 §9 的口径不算凭据失效，
// 归瞬时重试。
func (c *client) observe(status int, err error) {
	if c == nil || c.metrics == nil {
		return
	}
	result := metrics.ResultOK
	switch {
	case err != nil:
		if settings.IsPermanentCode(Classify(err)) {
			result = metrics.ResultPermanent
		} else {
			result = metrics.ResultTransient
		}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		result = metrics.ResultPermanent
	case status < 200 || status >= 300:
		result = metrics.ResultTransient
	}
	c.metrics.RecordUpstream(metrics.UpstreamJellyfin, result)
}

// send 是 do 去掉计量后的那一半。
func (c *client) send(ctx context.Context, method, path string, body io.Reader) (int, []byte, error) {
	u := c.base.JoinPath(path)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return 0, nil, protocolError(path, "构造请求失败: "+err.Error(), err)
	}
	// 规格 §5.5 点名 X-Emby-Token：管理员 API key 走这个头就够（不用再登录取用户 token）。
	req.Header.Set("X-Emby-Token", c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(req)
	if err != nil {
		return 0, nil, transportError(path, ctx.Err(), err)
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, transportError(path, ctx.Err(), err)
	}
	return response.StatusCode, raw, nil
}

// accept 把非 2xx 变成一个带分类的错误；2xx 返回 nil（判据只有这一处）。
func (c *client) accept(op string, status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	return c.statusError(op, status, body)
}

// statusError 把非 2xx 归成 auth（401/403）或 protocol（其余），并留住状态码：
// 刷新要靠它区分「被拒」与「上游一时不对」（见 isRejection）。
func (c *client) statusError(op string, status int, body []byte) error {
	detail := op + " 回 HTTP " + strconv.Itoa(status) + ": " + truncate(strings.TrimSpace(string(body)), 160)
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return authError(op, detail+"（API key 被拒或权限不够：Jellyfin 要管理员 key）", nil)
	}
	return &Error{Code: settings.CodeProtocol, Op: op, Status: status, Detail: detail}
}
