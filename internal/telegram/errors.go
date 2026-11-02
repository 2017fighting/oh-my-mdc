package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// ValidationError 表示这次调用在本地就被判为不合规（缺字段、事件名不认识、模板缺番号），
// 请求根本没发往上游。它归 business：要人改配置/数据，重试无用。
//
// Code 允许覆盖分类：两步测试的 token_invalid / chat_unavailable 也是本地判词。
type ValidationError struct {
	Message string
	Code    settings.TestCode
}

func (e ValidationError) Error() string { return e.Message }

// Error 是一次 Telegram 调用的失败，带规格 §8.4 的稳定机器码。
//
// Code 只有四类：auth / network / timeout / protocol（外加本地校验的 business）。
// Status 是上游 HTTP 状态码（没走到响应时为 0），发送分流与测试判词都看它。
// Detail 会原样回给前端并写进 last_test_error：写分类与摘要，**绝不写 bot token**。
type Error struct {
	Code   settings.TestCode
	Op     string
	Status int
	Detail string
	Err    error
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	if e.Err != nil {
		return fmt.Sprintf("telegram %s: %v", e.Op, e.Err)
	}
	return "telegram " + e.Op
}

func (e *Error) Unwrap() error { return e.Err }

// Classify 把一次调用错误归成稳定分类码（规格 §8.4 的测试错误码）。
func Classify(err error) settings.TestCode {
	var invalid ValidationError
	if errors.As(err, &invalid) {
		if invalid.Code != "" {
			return invalid.Code
		}
		return settings.CodeBusiness
	}
	var telegramErr *Error
	if errors.As(err, &telegramErr) {
		return telegramErr.Code
	}
	return settings.CodeNetwork
}

// IsPermanent 报告一次发送失败是否**永久**：规格 §5.6 的 400/401/403 立即驻留，
// 网络 / 429 / 5xx 是瞬时错，按指数退避重试。
//
// 它就是 §9 上游指标 `mdc_upstream_requests_total{result="permanent"}` 的 Telegram 分类钩子。
// 与 JavDB 的 IsPermanent 有一处不同：Telegram 的 5xx 是**瞬时**（上游一时不对），
// 所以 protocol 不算永久——只有凭据/请求本身有问题的 auth/business 才立即驻留。
func IsPermanent(err error) bool {
	return settings.IsPermanentCode(Classify(err))
}

// isTokenInvalid 报告这次失败是否指向「token 本身不能用」（第一步 getMe 的判据）。
//
// 401/403 是标准答案；404 是 Telegram 对**病态 token 路径**的回应（token 字符串
// 里没有合法的 bot id），性质相同——都该让用户去换 token，而不是重试。
func isTokenInvalid(err error) bool {
	var telegramErr *Error
	if !errors.As(err, &telegramErr) {
		return false
	}
	switch telegramErr.Status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	}
	return Classify(err) == settings.CodeAuth
}

// isTokenRevoked 是第二步的 token 判据：只认 401。
//
// getMe 刚过，403 在 sendMessage 上是「bot 不在这个 chat 里」（被踢出群/没被加进来），
// 不是 token 无效——把它报成 token_invalid 会让人去换一把好 token（见 isChatUnavailable）。
func isTokenRevoked(err error) bool {
	var telegramErr *Error
	return errors.As(err, &telegramErr) && telegramErr.Status == http.StatusUnauthorized
}

// isChatUnavailable 报告 token 没问题、但这次消息送不到这个 chat。
//
// 400（chat not found / bot 被拉黑 / 没权限）与 403（被踢出群）都是同一个处置：
// 去确认 chat_id 或让 bot 先被激活。401 不在此列——那是 token 的问题。
func isChatUnavailable(err error) bool {
	var telegramErr *Error
	if !errors.As(err, &telegramErr) {
		return false
	}
	switch telegramErr.Status {
	case http.StatusBadRequest, http.StatusForbidden:
		return true
	}
	return Classify(err) == settings.CodeBusiness
}

// tokenInvalidError 把一次上游失败翻成「token 无效」的判词，保留上游的原因。
func tokenInvalidError(cause error) error {
	return &Error{
		Code:   settings.CodeTokenInvalid,
		Op:     "getMe",
		Detail: "bot token 无效或已失效: " + cause.Error(),
		Err:    cause,
	}
}

// chatUnavailableError 是「chat 未激活」的判词：带上 chat_id 与上游原话，
// 提示怎么拿到 chat_id（规格 §5.6 的向导提示）。
func chatUnavailableError(chatID string, cause error) error {
	return &Error{
		Code: settings.CodeChatUnavailable,
		Op:   "sendMessage",
		Detail: "chat " + chatID + " 收不到消息（先跟 bot 说句话，或用 @userinfobot 取 chat id）: " +
			cause.Error(),
		Err: cause,
	}
}

func authError(op string, status int, detail string, err error) error {
	return &Error{Code: settings.CodeAuth, Op: op, Status: status, Detail: detail, Err: err}
}

func businessError(op string, status int, detail string, err error) error {
	return &Error{Code: settings.CodeBusiness, Op: op, Status: status, Detail: detail, Err: err}
}

func protocolError(op string, status int, detail string, err error) error {
	return &Error{Code: settings.CodeProtocol, Op: op, Status: status, Detail: detail, Err: err}
}

func transportError(op string, ctxErr error, err error, secret string) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctxErr, context.DeadlineExceeded) || isTimeout(err) {
		return &Error{Code: settings.CodeTimeout, Op: op, Detail: op + " 超时: " + scrub(err.Error(), secret), Err: err}
	}
	return &Error{Code: settings.CodeNetwork, Op: op, Detail: "连不上 Telegram（" + op + "）: " + scrub(err.Error(), secret), Err: err}
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// scrub 把凭据从错误文案里抹掉：token 在请求路径里，net/http 的错误会把它带出来，
// 而这条文案会进 last_test_error 与 task_event。
func scrub(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "***")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
