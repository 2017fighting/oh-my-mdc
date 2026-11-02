package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// ValidationError 表示这次调用在本地就被判为不合规（缺字段、路径不是本机可见目录），
// 请求根本没发往上游。它归 business：要人改配置，重试无用。
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string { return e.Message }

// Error 是一次 Jellyfin 调用的失败，带规格 §8.4 的稳定机器码。
//
// Code 只有四类：auth / network / timeout / protocol（外加本地校验的 business）。
// Detail 会原样回给前端并写进 last_test_error：写分类与摘要，绝不写凭据原文。
type Error struct {
	Code settings.TestCode
	Op   string
	// Status 是上游的 HTTP 状态码（没走到响应时为 0）。刷新用它区分「被拒」
	// （4xx，鉴权与重试类除外）与「上游一时不对」——只有前者才值得降级全库。
	Status int
	Detail string
	Err    error
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Op, e.Err)
	}
	return e.Op
}

func (e *Error) Unwrap() error { return e.Err }

// Classify 把一次调用错误归成稳定分类码（规格 §8.4 的测试错误码）。
//
// 分类是单点的：测试 chip、刷新的降级判断、以及将来 HTTP 面的错误都走它。
func Classify(err error) settings.TestCode {
	var invalid ValidationError
	if errors.As(err, &invalid) {
		return settings.CodeBusiness
	}
	var jellyfinErr *Error
	if errors.As(err, &jellyfinErr) {
		return jellyfinErr.Code
	}
	return settings.CodeNetwork
}

// isRejection 报告这次失败是不是服务端「拒收」：4xx，但鉴权错（401/403）与
// 重试类（408/429）除外。
//
// 只有拒收才降级全库：5xx / 429 / 408 是上游一时的状态，网络与超时更是——那类错
// 交给 side_effect 用自己的节奏重试，不该顺带触发一次昂贵的全库扫描。
func isRejection(err error) bool {
	var jellyfinErr *Error
	if !errors.As(err, &jellyfinErr) {
		return false
	}
	if jellyfinErr.Code != settings.CodeProtocol {
		return false
	}
	status := jellyfinErr.Status
	if status < 400 || status >= 500 {
		return false
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden,
		http.StatusRequestTimeout, http.StatusTooManyRequests:
		return false
	}
	return true
}

func authError(op, detail string, err error) error {
	return &Error{Code: settings.CodeAuth, Op: op, Detail: detail, Err: err}
}

func protocolError(op, detail string, err error) error {
	return &Error{Code: settings.CodeProtocol, Op: op, Detail: detail, Err: err}
}

func transportError(op string, ctxErr error, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctxErr, context.DeadlineExceeded) || isTimeout(err) {
		return &Error{Code: settings.CodeTimeout, Op: op, Detail: op + " 超时: " + err.Error(), Err: err}
	}
	return &Error{Code: settings.CodeNetwork, Op: op, Detail: "连不上 Jellyfin（" + op + "）: " + err.Error(), Err: err}
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
