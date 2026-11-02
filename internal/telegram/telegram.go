// Package telegram 是单向通知集成：四事件即时发一条 HTML 消息（规格 §5.6）。
//
// 它拥有四件事：
//
//   - 「测试连接」两步绿：getMe（验 token）→ sendMessage（验 chat_id，错误区分
//     token_invalid / chat_unavailable）；
//   - 四个事件的模板：文案照规格 §5.6 原文，用户内容 HTML 转义，**无图**；
//   - 逐事件开关：集成 enabled + 事件自己的布尔列，关掉即不发；
//   - 发送失败分流：400/401/403 是永久（sidefx 立即驻留），网络/429/5xx 是瞬时
//     （sidefx 指数退避最多 5 次后驻留）。
//
// # 它不是什么
//
// v1 单向：没有 getUpdates/webhook/命令/按钮，单一 chat_id，人工重发只走面板。
// 它也**不改自己的设置**：驻留只是待办等人工，绝不自动禁用集成（P12-3）。
//
// # 与 sidefx 的分工
//
// 通知入箱发生在事件产生处（poll 的加种、整理票的入库/失败、对账的停滞）；
// 本包只负责「把一条待办变成一次 sendMessage」。去重靠 side_effect 的
// (kind, target_key="<event>:<infohash>") 部分唯一索引，不在这里重做。
package telegram

import (
	"context"
	"net/http"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// 通知事件名（规格 §5.6）。target_key 是 "<event>:<infohash>"。
//
// 它们是模板的选择键，也是开关列的映射键。sidefx 包把同名常量指到这里，
// 免得「发的时候叫 A、渲染的时候认 B」。
const (
	EventDownloadStarted  = "download_started"
	EventFiled            = "filed"
	EventProcessingFailed = "processing_failed"
	EventDownloadStalled  = "download_stalled"
)

// DefaultHost 是 Telegram Bot API 的入口。
const DefaultHost = "https://api.telegram.org"

// Config 是一次 Telegram 调用要用的设置原文（含 token，只许在进程内流转）。
type Config struct {
	BotToken string
	ChatID   string
	// Enabled 是集成总开关；关着时四个事件都不发。
	Enabled bool
	// 四事件逐条开关（规格 §4.3，默认全开）。
	NotifyDownloadStarted  bool
	NotifyFiled            bool
	NotifyProcessingFailed bool
	NotifyDownloadStalled  bool
}

// settingsInput 是 settings.TestInput 里本包用到的那一小片。
type settingsInput interface {
	Text(name string) string
	Bool(name string) bool
}

// ConfigFrom 把设置域的原文翻成调用配置（单点：向导测试与执行半段共用）。
func ConfigFrom(input settingsInput) Config {
	return Config{
		BotToken:               strings.TrimSpace(input.Text("bot_token")),
		ChatID:                 strings.TrimSpace(input.Text("chat_id")),
		Enabled:                input.Bool("enabled"),
		NotifyDownloadStarted:  input.Bool("notify_download_started"),
		NotifyFiled:            input.Bool("notify_filed"),
		NotifyProcessingFailed: input.Bool("notify_processing_failed"),
		NotifyDownloadStalled:  input.Bool("notify_download_stalled"),
	}
}

// KnownEvent 报告 event 是不是四个模板之一。
//
// 引擎用它区分「开关关着」（正常不发、待办完成）与「事件名写错了」（本地错误，驻留等人工）。
func KnownEvent(event string) bool {
	_, ok := events[event]
	return ok
}

// EnabledFor 报告这个事件是否该发：集成开着且它自己的开关开着。
//
// 关掉即不发（P12-1）：待办照常完成，不在队列里留一条永远不发的活。
func (c Config) EnabledFor(event string) bool {
	spec, ok := events[event]
	if !ok || !c.Enabled {
		return false
	}
	return spec.flag(c)
}

// Options 装配一个 Telegram 集成门面所需的依赖。
type Options struct {
	// Host 覆盖默认入口（测试用）；空则用 DefaultHost。
	Host string
	// HTTP 允许注入测试用的 transport；空则用带超时的默认客户端。
	HTTP *http.Client
}

// httpClient 是本包对 *http.Client 的最小要求（测试替换用）。
type httpClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Integration 是 Telegram 集成门面：一个 settings.Tester + 一个 Notifier。
type Integration struct {
	host string
	http *http.Client
}

// New 构造 Telegram 集成门面。
func New(opts Options) *Integration {
	host := strings.TrimRight(strings.TrimSpace(opts.Host), "/")
	if host == "" {
		host = DefaultHost
	}
	return &Integration{host: host, http: opts.HTTP}
}

// testMessage 是「测试连接」第二步发出去的那条消息（固定文案，无用户内容）。
const testMessage = "oh-my-mdc 测试消息：通知链路正常。\n" +
	"收到即说明 bot token 与 chat_id 两关都过了。"

// Test 实现 settings.Tester：getMe（验 token）→ sendMessage（验 chat_id），两步都过才绿。
//
// 错误码用两步测试的专用词汇（规格 §8.4）：token 被拒 → token_invalid；
// token 没问题但 chat 未激活/被踢 → chat_unavailable；连不上/超时仍是 network/timeout。
// 本地缺字段先于任何请求：缺 token 报 token_invalid，缺 chat_id 报 chat_unavailable。
func (i *Integration) Test(ctx context.Context, _ settings.Section, input settings.TestInput) settings.TestResult {
	cfg := ConfigFrom(input)
	if cfg.BotToken == "" {
		return testResult(ValidationError{Code: settings.CodeTokenInvalid, Message: "Telegram 节还没设置 bot token"})
	}
	if cfg.ChatID == "" {
		return testResult(ValidationError{Code: settings.CodeChatUnavailable, Message: "Telegram 节还没设置 chat id"})
	}

	c, err := newClient(i.host, cfg, i.http)
	if err != nil {
		return testResult(err)
	}

	bot, err := c.getMe(ctx)
	if err != nil {
		if isTokenInvalid(err) {
			return testResult(tokenInvalidError(err))
		}
		return testResult(err)
	}

	if err := c.sendMessage(ctx, cfg.ChatID, testMessage); err != nil {
		// 第二步只可能是 chat 的问题：token 刚刚被 getMe 验过。401 是 token 在两步之间
		// 被吊销（罕见但要说得准），401/403/400 的其余情形都指向 chat_id 或 bot 被踢。
		if isTokenRevoked(err) {
			return testResult(tokenInvalidError(err))
		}
		if isChatUnavailable(err) {
			return testResult(chatUnavailableError(cfg.ChatID, err))
		}
		return testResult(err)
	}

	detail := "Bot @" + bot.Username + " 可用 · 测试消息已送达 chat " + cfg.ChatID
	return settings.TestResult{OK: true, Detail: detail}
}

// Notify 发一条事件通知：渲染模板 → sendMessage（HTML、无图）。
//
// 缺设置、事件名不认识、模板缺番号都是本地校验错（business，驻留等人工）；
// 上游 400/401/403 是永久错，网络/429/5xx 是瞬时错——怎么重试由 sidefx 的
// 错误分类决定，这里只把错误类说清楚。
func (i *Integration) Notify(ctx context.Context, cfg Config, event string, payload map[string]any) error {
	if cfg.BotToken == "" {
		return ValidationError{Code: settings.CodeTokenInvalid, Message: "Telegram 节还没设置 bot token"}
	}
	if cfg.ChatID == "" {
		return ValidationError{Code: settings.CodeChatUnavailable, Message: "Telegram 节还没设置 chat id"}
	}

	text, err := Render(event, payload)
	if err != nil {
		return err
	}

	c, err := newClient(i.host, cfg, i.http)
	if err != nil {
		return err
	}
	return c.sendMessage(ctx, cfg.ChatID, text)
}

// testResult 把一次失败翻成 chip 的判词。
func testResult(err error) settings.TestResult {
	return settings.TestResult{Code: Classify(err), Detail: err.Error()}
}

// 编译期确认门面满足测试接缝。
var _ settings.Tester = (*Integration)(nil)
