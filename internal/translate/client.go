package translate

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

	"github.com/2017fighting/oh-my-mdc/internal/metrics"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

const (
	// chatPath 是 OpenAI 兼容的对话补全端点（拼在 base_url 之后）。
	chatPath = "/chat/completions"
	// probeTokens 是「测试连接」那一次补全的 max_tokens（规格 §5.7：1 token 极小请求）。
	probeTokens = 1
	// defaultHTTPTimeout 是单次翻译调用的上限。
	//
	// 它比翻译本身的需要宽一点，但必须有：刮削在循环里同步调它，一个挂住的上游
	// 不该把一轮对账拖成无尽等待。测试连接另有 10s 总预算（settings.TestTimeout）。
	defaultHTTPTimeout = 30 * time.Second
	// maxBody 是读响应体的上限：一份回包不该有兆级大小，别让坏响应吃光内存。
	maxBody = 4 << 20
)

// chatMessage 是一条对话消息（OpenAI 兼容形状）。
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest 是 POST {base_url}/chat/completions 的请求体。
//
// temperature 恒为 0：译文要稳，同一部片两次刮削不该出两种中文。
// max_tokens 只给测试连接用（1 token），正式翻译不设上限（omitempty）。
type chatRequest struct {
	Model       string        `json:"model"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Messages    []chatMessage `json:"messages"`
}

// chatResponse 是回包里本包用得到的那一面。
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// client 是一个带凭据的翻译客户端。
type client struct {
	endpoint string
	apiKey   string
	model    string
	http     *http.Client
	metrics  metrics.Recorder
}

// newClient 校验入口地址并装配客户端。
//
// base_url 是「OpenAI 兼容端点的根」（用户填 https://api.openai.com/v1），所以这里
// 补上 /chat/completions：让用户填到补全路径是一次性的坑，不如实现兜住。
func newClient(cfg Config, httpClient *http.Client, recorder metrics.Recorder) (*client, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		return nil, ValidationError{Code: settings.CodeBusiness, Message: "AI 翻译节还没保存 base_url"}
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, ValidationError{
			Code:    settings.CodeBusiness,
			Message: "base_url 不是 http(s) 地址: " + base,
		}
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &client{endpoint: base + chatPath, apiKey: cfg.APIKey, model: cfg.Model, http: httpClient, metrics: recorder}, nil
}

// complete 发一次对话补全，返回模型的文本内容。
//
// maxTokens 为 0 表示不设上限（正式翻译）；测试连接传 1。
//
// 上游计量挂在这一次出口上：401/403 是凭据类永久错（换 key），
// 400/404/422 也是永久（改 base_url/model），网络/429/5xx 与信封错是瞬时。
func (c *client) complete(ctx context.Context, messages []chatMessage, maxTokens int) (string, error) {
	text, err := c.request(ctx, messages, maxTokens)
	metrics.ObserveUpstream(c.metrics, metrics.UpstreamOpenAI, err, IsPermanent)
	return text, err
}

// request 是 complete 去掉计量后的那一半。
func (c *client) request(ctx context.Context, messages []chatMessage, maxTokens int) (string, error) {
	payload, err := json.Marshal(chatRequest{
		Model:       c.model,
		Temperature: 0,
		MaxTokens:   maxTokens,
		Messages:    messages,
	})
	if err != nil {
		return "", protocolError("chat", "构造翻译请求失败: "+err.Error(), err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", protocolError("chat", "构造翻译请求失败: "+err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", transportError("chat", ctx.Err(), err, c.apiKey)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", transportError("chat", ctx.Err(), err, c.apiKey)
	}
	if err := classifyStatus(resp.StatusCode, raw, c.apiKey); err != nil {
		return "", err
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", protocolError("chat", "回包不是合法 JSON: "+preview(string(raw)), err)
	}
	if len(parsed.Choices) == 0 {
		return "", protocolError("chat", "回包没有 choices: "+preview(string(raw)), nil)
	}
	return parsed.Choices[0].Message.Content, nil
}

// classifyStatus 把非 2xx 映射成稳定分类（规格 §8.4/§5.7）。
//
// 401/403 是凭据（换 key）；429/5xx 是上游一时不对（可重试）；400/404/422 是请求本身
// 不合规（错 base_url、错 model —— 改配置，重试变不出来）；其余状态与 3xx 一律 protocol。
func classifyStatus(status int, raw []byte, apiKey string) error {
	if status >= 200 && status < 300 {
		return nil
	}
	detail := fmt.Sprintf("上游回 HTTP %d", status)
	if message := upstreamMessage(raw); message != "" {
		detail += "：" + message
	}
	detail = scrub(detail, apiKey)

	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &Error{Code: settings.CodeAuth, Op: "chat",
			Detail: detail + "（api key 被拒：去 AI 翻译步换凭据）"}
	case status == http.StatusTooManyRequests || status >= 500:
		return &Error{Code: settings.CodeNetwork, Op: "chat", Detail: detail}
	case status == http.StatusBadRequest || status == http.StatusNotFound || status == http.StatusUnprocessableEntity:
		return &Error{Code: settings.CodeBusiness, Op: "chat",
			Detail: detail + "（检查 base_url 与 model）"}
	default:
		return &Error{Code: settings.CodeProtocol, Op: "chat", Detail: detail}
	}
}

// upstreamMessage 取错误信封里的 message（OpenAI 兼容形状）；取不到就空。
func upstreamMessage(raw []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return preview(string(raw))
	}
	if envelope.Error.Message == "" {
		return preview(string(raw))
	}
	return truncate(envelope.Error.Message, 200)
}

// preview 截一小段响应体用于报错（HTML 网关页一眼就能看出来）。
func preview(body string) string {
	return truncate(strings.TrimSpace(body), 120)
}
