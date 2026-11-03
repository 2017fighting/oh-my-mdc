package translate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// ValidationError 表示这次调用在本地就被判为不合规（缺字段、base_url 不是 http(s)），
// 请求根本没发出去。
//
// Code 允许覆盖分类：缺 api_key 归 auth（去换凭据），缺 base_url/model 归 business
// （去改配置）。两者都是永久错 —— 重试变不出来。
type ValidationError struct {
	Code    settings.TestCode
	Message string
}

func (e ValidationError) Error() string { return e.Message }

// Error 是一次上游调用的失败，带规格 §8.4 的稳定机器码。
//
// Code 只有四类：auth / network / timeout / protocol，外加「要人改配置」的 business
// （本地缺 base_url/model，或上游 400/404/422 —— 两种处置相同：改配置，不是重试）。
// Detail 会原样回给前端并写进 last_test_error 与 task_event：写分类与摘要，
// **绝不写 api key**（凭据在请求头里，不是文案的一部分）。
type Error struct {
	Code   settings.TestCode
	Op     string
	Detail string
	Err    error
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	if e.Err != nil {
		return fmt.Sprintf("ai 翻译 %s: %v", e.Op, e.Err)
	}
	return "ai 翻译 " + e.Op
}

func (e *Error) Unwrap() error { return e.Err }

// Classify 把一次调用错误归成稳定分类码（规格 §8.4 / P6-1）。
//
// 单点分类：向导的测试 chip、刮削降级时的告警文案都走它。
func Classify(err error) settings.TestCode {
	var invalid ValidationError
	if errors.As(err, &invalid) {
		if invalid.Code != "" {
			return invalid.Code
		}
		return settings.CodeBusiness
	}
	var translateErr *Error
	if errors.As(err, &translateErr) {
		return translateErr.Code
	}
	return settings.CodeNetwork
}

// protocolError 是「上游回了响应，但不是我们认得的样子」：非 JSON、没有 choices、
// 译文不是 JSON 对象。处置是查代码/查上游，不是盲重试。
func protocolError(op, detail string, err error) error {
	return &Error{Code: settings.CodeProtocol, Op: op, Detail: detail, Err: err}
}

// transportError 把连不上归成 timeout 或 network。
func transportError(op string, ctxErr error, err error, apiKey string) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctxErr, context.DeadlineExceeded) || isTimeout(err) {
		return &Error{Code: settings.CodeTimeout, Op: op, Detail: op + " 超时: " + scrub(err.Error(), apiKey), Err: err}
	}
	return &Error{Code: settings.CodeNetwork, Op: op, Detail: "连不上翻译端点（" + op + "）: " + scrub(err.Error(), apiKey), Err: err}
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// scrub 把凭据从错误文案里抹掉：这条文案会进 last_test_error 与 task_event。
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
