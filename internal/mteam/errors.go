package mteam

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// ValidationError 表示这次调用在本地就被判为不合规（缺番号、缺 key），
// 请求根本没发出去。它归 business：要人改配置/输入，重试无用。
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string { return e.Message }

// NotFoundError 表示搜索成功但一条候选都没有：这是**答案**，不是故障。
//
// 它单独成一型，是因为调用方的处置不同：没有候选就降级磁力，不该记一条
// 「M-Team 出错」的告警（那会让面板把「M-Team 上没有这部片」显示成故障）。
type NotFoundError struct {
	Number string
}

func (e NotFoundError) Error() string {
	return "M-Team 上搜不到 " + e.Number + " 的种子"
}

// Error 是一次 M-Team 调用的失败，带规格 §8.4 的稳定机器码。
//
// Code 只有四类：auth / network / timeout / protocol（外加本地校验的 business）。
// Detail 会原样回给前端并写进 last_test_error / task_event：写分类与摘要，
// 绝不写 api key 原文。
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
		return fmt.Sprintf("m-team %s: %v", e.Op, e.Err)
	}
	return "m-team " + e.Op
}

func (e *Error) Unwrap() error { return e.Err }

// Classify 把一次调用错误归成稳定分类码（规格 §8.4 / P5-4）。
//
// 分类是单点的：向导的测试 chip、poll 降级时的告警文案、将来的 upstream 指标
// （§9 的 permanent 判定）都走它。`code!="0"` 的两种：凭据类归 auth，
// 其余信封失败归 protocol —— 猜错分类比不分类更坏。
func Classify(err error) settings.TestCode {
	var invalid ValidationError
	if errors.As(err, &invalid) {
		return settings.CodeBusiness
	}
	var notFound NotFoundError
	if errors.As(err, &notFound) {
		// 「搜不到」不是故障，但测试 chip 只有四色：按业务结果（要人确认番号）报。
		return settings.CodeBusiness
	}
	var mteamErr *Error
	if errors.As(err, &mteamErr) {
		return mteamErr.Code
	}
	return settings.CodeNetwork
}

// IsNotFound 报告这次失败是不是「M-Team 上没有这部片」。
//
// 它是**答案**而不是故障：调用方据此降级磁力，但不该把它记成一条故障告警 ——
// 否则每部 M-Team 没收录的片都会在时间线上留一条红字。
func IsNotFound(err error) bool {
	var notFound NotFoundError
	return errors.As(err, &notFound)
}

// authError 是凭据类失败：key 无效/过期/缺失。处置是换 key，不是重试。
func authError(op, detail string, err error) error {
	return &Error{Code: settings.CodeAuth, Op: op, Detail: detail, Err: err}
}

// protocolError 是「上游回了响应，但不是我们认得的样子」：信封失败、非种子字节、
// 被 WAF 拦下的 302。处置是查代码/查上游，不是盲重试。
func protocolError(op, detail string, err error) error {
	return &Error{Code: settings.CodeProtocol, Op: op, Detail: detail, Err: err}
}

// transportError 把连不上归成 timeout 或 network。
func transportError(op string, ctxErr error, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctxErr, context.DeadlineExceeded) || isTimeout(err) {
		return &Error{Code: settings.CodeTimeout, Op: op, Detail: op + " 超时: " + err.Error(), Err: err}
	}
	return &Error{Code: settings.CodeNetwork, Op: op, Detail: "连不上 M-Team（" + op + "）: " + err.Error(), Err: err}
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// noRedirect 让 3xx 原样返回，而不是被跟随（API 端点用）。
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
