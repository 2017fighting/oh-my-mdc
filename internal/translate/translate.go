// Package translate 是可选集成 AI 翻译：OpenAI 兼容 chat completions 的客户端（规格 §5.7）。
//
// 它拥有三件事：
//
//   - 「测试连接」：1 token 的极小补全，证明 key、model 与回包形状三关都过（§8.4）；
//   - 翻译：一次请求把日文 title/outline 译成简体中文（回包要求 JSON 对象）；
//   - 错误分类：auth / network / timeout / protocol / business 的稳定机器码，
//     向导的 chip 与刮削降级时的告警文案都读它。
//
// # 它不是什么
//
// 它不认识任务、不写库、不重试：译文落哪一列是 task 的事，失败怎么降级是 scrape 的事。
// 这样「一次翻译调用」的全部事实（端点、请求形状、分类）只在一个包里。
package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/metrics"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// Config 是一次 AI 翻译调用要用的设置原文（含 api key，只许在进程内流转）。
type Config struct {
	// BaseURL 是 OpenAI 兼容端点的根地址，如 https://api.openai.com/v1。
	// 请求打到 {BaseURL}/chat/completions。
	BaseURL string
	APIKey  string
	Model   string
	// Enabled 是可选集成的总开关：关着时刮削一次都不问它（P6-3）。
	Enabled bool
}

// settingsInput 是 settings.TestInput 里本包用到的那一小片。
type settingsInput interface {
	Text(name string) string
	Bool(name string) bool
}

// ConfigFrom 把设置域的原文翻成调用配置（单点：向导测试与刮削共用）。
func ConfigFrom(input settingsInput) Config {
	return Config{
		BaseURL: strings.TrimSpace(input.Text("base_url")),
		APIKey:  strings.TrimSpace(input.Text("api_key")),
		Model:   strings.TrimSpace(input.Text("model")),
		Enabled: input.Bool("enabled"),
	}
}

// Text 是一次翻译的输入与输出：日文原文进，简体中文译文出。
//
// 两个字段合并进一次请求（规格 §5.7 允许）：一部片一次调用，token 与延迟都省一半。
type Text struct {
	Title   string
	Outline string
}

// Empty 报告有没有可译的原文。
func (t Text) Empty() bool {
	return strings.TrimSpace(t.Title) == "" && strings.TrimSpace(t.Outline) == ""
}

// systemPrompt 是固定的翻译指令。
//
// 它是请求形状的一部分（fake 端点会核对它有「只输出 JSON」这一句）：回包必须能被
// 解析成 {title, outline}，否则一次成功也就变成了 protocol 错。
const systemPrompt = `你是日本成人影片元数据的简体中文译者。输入是一个 JSON 对象：` +
	`{"title": <日文标题>, "outline": <日文简介>}，字段可能是空串。` +
	`把 title 与 outline 译成自然、准确的简体中文：不增删内容，不保留日文假名` +
	`（人名、番号、厂商名按业界习惯处理）。` +
	`只输出一个 JSON 对象：{"title": "…", "outline": "…"}，不要 markdown 代码块，不要任何解释。` +
	`输入为空串的字段，输出也用空串。`

// probePrompt 是「测试连接」那一次 1 token 极小的请求。
const probePrompt = "ping"

// Options 装配一个 AI 翻译集成门面所需的依赖。
type Options struct {
	// HTTP 允许注入测试用的 transport；空则用带超时的默认客户端。
	HTTP *http.Client
	// Metrics 记每次上游请求的结局（规格 §9 的 openai 指标）；nil 时什么都不记。
	Metrics metrics.Recorder
}

// Integration 是 AI 翻译集成门面：一个 settings.Tester + 一个 Translator。
type Integration struct {
	http    *http.Client
	metrics metrics.Recorder
}

// New 构造 AI 翻译集成门面。
func New(opts Options) *Integration {
	return &Integration{http: opts.HTTP, metrics: opts.Metrics}
}

// Test 实现 settings.Tester：1 token 的极小补全（规格 §5.7/§8.4）。
//
// 一个 max_tokens=1 的补全同时证明三件事：鉴权头被接受、模型名可用、回包可解析。
// 本地缺字段先于任何请求：缺 key 报 auth（去换凭据），缺 base_url/model 报 business（去改配置）。
func (i *Integration) Test(ctx context.Context, _ settings.Section, input settings.TestInput) settings.TestResult {
	cfg := ConfigFrom(input)
	if err := validateConfig(cfg); err != nil {
		return settings.TestResult{Code: Classify(err), Detail: err.Error()}
	}

	c, err := newClient(cfg, i.http, i.metrics)
	if err != nil {
		return settings.TestResult{Code: Classify(err), Detail: err.Error()}
	}
	if _, err := c.complete(ctx, []chatMessage{
		{Role: "user", Content: probePrompt},
	}, probeTokens); err != nil {
		return settings.TestResult{Code: Classify(err), Detail: err.Error()}
	}

	return settings.TestResult{
		OK:     true,
		Detail: fmt.Sprintf("模型 %s 可用；1 token 补全成功（翻译一次请求出 title/outline）", cfg.Model),
	}
}

// Translate 把日文标题与简介译成简体中文。
//
// 失败**不吞**：调用方据此降级日文原文并留一条告警（P6-2）。空输入不问上游，
// 直接回空 —— 一部没标题没简介的片不该花掉一次调用。
func (i *Integration) Translate(ctx context.Context, cfg Config, in Text) (Text, error) {
	if in.Empty() {
		return Text{}, nil
	}
	if err := validateConfig(cfg); err != nil {
		return Text{}, err
	}

	c, err := newClient(cfg, i.http, i.metrics)
	if err != nil {
		return Text{}, err
	}

	payload, err := json.Marshal(map[string]string{"title": in.Title, "outline": in.Outline})
	if err != nil {
		return Text{}, protocolError("translate", "构造翻译输入失败: "+err.Error(), err)
	}
	content, err := c.complete(ctx, []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: string(payload)},
	}, 0)
	if err != nil {
		return Text{}, err
	}

	translated, err := parseTranslation(content)
	if err != nil {
		return Text{}, err
	}
	// 只取上游真给了的字段：原文为空时译出来也该是空，别把空串写成「已翻译」。
	if strings.TrimSpace(in.Title) == "" {
		translated.Title = ""
	}
	if strings.TrimSpace(in.Outline) == "" {
		translated.Outline = ""
	}
	if translated.Empty() {
		return Text{}, protocolError("translate", "上游没有给出任何译文: "+preview(content), nil)
	}
	return translated, nil
}

// validateConfig 是本地校验：缺什么就报什么，请求根本不发出去。
func validateConfig(cfg Config) error {
	switch {
	case cfg.APIKey == "":
		return ValidationError{Code: settings.CodeAuth, Message: "AI 翻译节还没保存 api key"}
	case cfg.BaseURL == "":
		return ValidationError{Code: settings.CodeBusiness, Message: "AI 翻译节还没保存 base_url"}
	case cfg.Model == "":
		return ValidationError{Code: settings.CodeBusiness, Message: "AI 翻译节还没保存 model"}
	}
	return nil
}

// parseTranslation 从模型的回包里取出 {title, outline}。
//
// 宽容一处：模型爱把 JSON 包进 ```json 围栏或前后加一句客套，所以先剥围栏、
// 再取第一个 '{' 到最后一个 '}'。除此之外不做猜测 —— 解析不出来是 protocol 错，
// 把模型的散文当译文写进库比失败更坏。
func parseTranslation(content string) (Text, error) {
	body := strings.TrimSpace(content)
	if body == "" {
		return Text{}, protocolError("translate", "上游返回了空译文", nil)
	}
	body = stripFences(body)
	start := strings.Index(body, "{")
	end := strings.LastIndex(body, "}")
	if start < 0 || end <= start {
		return Text{}, protocolError("translate", "回包不是一个 JSON 对象: "+preview(content), nil)
	}

	var parsed struct {
		Title   string `json:"title"`
		Outline string `json:"outline"`
	}
	if err := json.Unmarshal([]byte(body[start:end+1]), &parsed); err != nil {
		return Text{}, protocolError("translate", "回包的 JSON 解不开: "+preview(content), err)
	}
	return Text{Title: strings.TrimSpace(parsed.Title), Outline: strings.TrimSpace(parsed.Outline)}, nil
}

// stripFences 把 ```json … ``` 这类围栏剥掉（围栏语言标记一并去掉）。
func stripFences(body string) string {
	if !strings.HasPrefix(body, "```") {
		return body
	}
	body = strings.TrimPrefix(body, "```")
	// 第一行是语言标记（json / JSON / 空）。
	if newline := strings.IndexByte(body, '\n'); newline >= 0 {
		if marker := strings.TrimSpace(body[:newline]); !strings.Contains(marker, "{") {
			body = body[newline+1:]
		}
	}
	body = strings.TrimSuffix(strings.TrimSpace(body), "```")
	return strings.TrimSpace(body)
}

// 编译期确认门面满足测试接缝。
var _ settings.Tester = (*Integration)(nil)
