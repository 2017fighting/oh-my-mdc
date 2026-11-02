package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultHTTPTimeout 是单次调用的上限。测试连接另有 10s 的总预算（settings.TestTimeout）。
const defaultHTTPTimeout = 30 * time.Second

// maxBodyBytes 挡住一个异常响应把内存读爆（Bot API 的回执都很小）。
const maxBodyBytes = 1 << 20

// client 是一个已带 token 的 Telegram Bot API 客户端。
//
// token 是路径的一段（/bot<token>/getMe），所以任何错误文案都要经过 scrub——
// 否则「连不上」的原文会带着 token 进 last_test_error 与时间线。
type client struct {
	base  *url.URL
	http  httpClient
	token string
}

// newClient 解析入口地址并装上默认 HTTP 客户端。
//
// 注入的 *http.Client 在判空之后才装进客户端：nil 指针装进接口会变成非 nil 接口，
// 之后每一次 Do 都会 panic。
func newClient(host string, cfg Config, injected *http.Client) (*client, error) {
	// host 已由 Integration 归一化（New 里 TrimSpace + 去尾部斜杠），这里只管解析。
	base, err := url.Parse(host)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, ValidationError{Message: "Telegram 入口地址需要 http(s) 地址: " + host}
	}
	var hc httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	if injected != nil {
		hc = injected
	}
	return &client{base: base, http: hc, token: cfg.BotToken}, nil
}

// botInfo 是 getMe 里向导要用的两样（chip 文案用）。
type botInfo struct {
	ID       int64  `json:"id"`
	IsBot    bool   `json:"is_bot"`
	Username string `json:"username"`
}

// envelope 是 Bot API 的统一回执信封：ok=false 时带 error_code/description。
type envelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
}

// getMe 调 GET /bot<token>/getMe：验 token 就是验这一跳（规格 §5.6 第一步）。
func (c *client) getMe(ctx context.Context) (botInfo, error) {
	body, status, err := c.call(ctx, http.MethodGet, "getMe", nil)
	if err != nil {
		return botInfo{}, err
	}

	parsed, apiErr := c.parse("getMe", status, body)
	if apiErr != nil {
		return botInfo{}, apiErr
	}

	var info botInfo
	if err := json.Unmarshal(parsed.Result, &info); err != nil {
		return botInfo{}, protocolError("getMe", status, "getMe 回执不是预期的 bot 信息: "+truncate(string(parsed.Result), 120), err)
	}
	return info, nil
}

// sendMessage 是 v1 唯一的发送方式：sendMessage + parse_mode=HTML，**无图**（规格 §5.6）。
func (c *client) sendMessage(ctx context.Context, chatID, text string) error {
	payload, err := json.Marshal(map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	})
	if err != nil {
		return protocolError("sendMessage", 0, "拼发送请求体失败: "+err.Error(), err)
	}

	body, status, err := c.call(ctx, http.MethodPost, "sendMessage", payload)
	if err != nil {
		return err
	}
	_, apiErr := c.parse("sendMessage", status, body)
	return apiErr
}

// call 发一次请求并读完回执。错误只负责「请求撞上什么」，非 2xx 的含义留给 parse。
func (c *client) call(ctx context.Context, method, name string, payload []byte) ([]byte, int, error) {
	u := c.base.JoinPath("bot"+c.token, name)
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, 0, protocolError(name, 0, "构造请求失败: "+scrub(err.Error(), c.token), err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, transportError(name, ctx.Err(), err, c.token)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, transportError(name, ctx.Err(), err, c.token)
	}
	return body, resp.StatusCode, nil
}

// parse 解信封并判失败：ok=false 或非 2xx 都算上游拒绝。
//
// 状态码分类（规格 §5.6）：400 → business、401/403 → auth、其余（429/5xx/其它）
// → protocol。后两类在 IsPermanent 里都是**瞬时**，交给 sidefx 退避重试。
func (c *client) parse(op string, status int, body []byte) (envelope, error) {
	var parsed envelope
	decodeErr := json.Unmarshal(body, &parsed)

	if status >= 200 && status < 300 {
		if decodeErr == nil && parsed.OK {
			return parsed, nil
		}
		// 2xx 却不是预期信封：上游改版或中间层塞了别的东西，归 protocol（瞬时，会重试）。
		return envelope{}, protocolError(op, status,
			op+" 回执不是预期信封: "+truncate(string(body), 160), decodeErr)
	}

	// 上游说的错误码优先（Telegram 的 HTTP 状态与 error_code 通常一致，但不保证）。
	code := parsed.ErrorCode
	if code == 0 {
		code = status
	}
	description := strings.TrimSpace(parsed.Description)
	if description == "" && decodeErr != nil {
		description = truncate(string(body), 160)
	}
	detail := fmt.Sprintf("%s 被 Telegram 拒绝（%d）：%s", op, code, description)

	switch {
	case code == http.StatusBadRequest:
		return envelope{}, businessError(op, code, detail, decodeErr)
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return envelope{}, authError(op, code, detail, decodeErr)
	default:
		return envelope{}, protocolError(op, code, detail, decodeErr)
	}
}

// 编译期确认默认客户端满足最小接口。
var _ httpClient = (*http.Client)(nil)
