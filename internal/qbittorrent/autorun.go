package qbittorrent

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// ValidationError 表示这次调用在本地就被判为不合规（配置缺字段、命令没法安全归一），
// 请求根本没发往上游。它归 business：要人改输入，重试无用。
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string { return e.Message }

// Error 是一次 qBittorrent 调用的失败，带规格 §8.4 的稳定机器码。
//
// Code 只有四类：auth / network / timeout / protocol（外加本地校验的 business）。
// Detail 会原样回给前端并写进 last_test_error：写分类与摘要，绝不写凭据原文。
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
		return fmt.Sprintf("%s: %v", e.Op, e.Err)
	}
	return e.Op
}

func (e *Error) Unwrap() error { return e.Err }

// Classify 把一次调用错误归成稳定分类码（规格 §8.4 的测试错误码）。
//
// 分类是单点的：测试 chip 与 /api/qbittorrent/* 的 HTTP 错误都用它，
// 免得同一套 switch 在两处各写一遍。
func Classify(err error) settings.TestCode {
	var invalid ValidationError
	if errors.As(err, &invalid) {
		return settings.CodeBusiness
	}
	var qbtErr *Error
	if errors.As(err, &qbtErr) {
		return qbtErr.Code
	}
	return settings.CodeNetwork
}

func authError(op, detail string, err error) error {
	return &Error{Code: settings.CodeAuth, Op: op, Detail: detail, Err: err}
}

func protocolError(op, detail string, err error) error {
	return &Error{Code: settings.CodeProtocol, Op: op, Detail: detail, Err: err}
}

// HookPath 是 qbt autorun 回调的路径：%I 是 qbt 自己的 infohash 占位（纯 hex）。
const HookPath = "/api/hooks/qbt?ih=%I"

// HookURL 把回调 base URL 拼成 autorun 里要写的地址。
func HookURL(baseURL string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return "", ValidationError{Message: "qBittorrent 节还没设置回调 base URL"}
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", ValidationError{Message: "回调 base URL 需要 http(s) 地址: " + base}
	}
	return base + HookPath, nil
}

// Autorun 是 qbt「torrent 完成时运行外部程序」那一项的现值。
type Autorun struct {
	Enabled bool
	Program string
}

// AutorunPlan 是 autorun 改写的建议：Before/After 是给向导展示的完整对比。
//
// Skip 表示现值已含本项目 URL，什么都不用做（幂等，规格 P3-3）。
// Warning 是人要看见但不必拦下的事（主要就是 autorun 未启用）。
// Applied 只在落地路径上由 ApplyAutorun 置真：预览永远是 false。
type AutorunPlan struct {
	Before  string
	After   string
	Skip    bool
	Changed bool
	Enabled bool
	Warning string
	Applied bool
}

// autorunDisabledWarning 说明「program 写对了但开关没开」这个静默失败点。
//
// ADR-0007 只允许改 autorun_program，所以这里只警告不动手。
const autorunDisabledWarning = "qBittorrent 的 autorun 现在是关闭的：写进去的命令不会执行，回调也就不会来。" +
	"去 qbt 的「Torrent 完成时运行外部程序」勾上再保存。"

// PlanAutorun 算出 autorun 的共存改写（ADR-0007、规格 §5.4/P3-2/P3-3/P3-4）。
//
// 形状固定为 `/bin/sh -c "<旧命令> ; curl -fsS -m 10 '<hook>'"`：
// 外层双引号交给 qbt 拆 argv，payload 内一律单引号，`;` 让两条命令互不牵连。
// 不改 `autorun_on_torrent_added_*`：本函数只产出 autorun_program 这一个值。
func PlanAutorun(current Autorun, webhookBaseURL string) (AutorunPlan, error) {
	hook, err := HookURL(webhookBaseURL)
	if err != nil {
		return AutorunPlan{}, err
	}

	plan := AutorunPlan{Before: current.Program, After: current.Program, Enabled: current.Enabled}
	if !current.Enabled {
		plan.Warning = autorunDisabledWarning
	}

	// 幂等判据：现值已含本项目回调路径就跳过（换过 base URL 也不翻旧账）。
	if strings.Contains(current.Program, HookPath) {
		plan.Skip = true
		return plan, nil
	}

	payload, err := composePayload(current.Program)
	if err != nil {
		return AutorunPlan{}, err
	}

	hookCommand := "curl -fsS -m 10 '" + hook + "'"
	inner := hookCommand
	if payload != "" {
		inner = payload + " ; " + hookCommand
	}
	// 单引号必须成对：外层是双引号，payload 里的单引号一旦落单，sh -c 会把它后面
	// 的全部内容（含本项目回调）当成字符串吞掉。
	if strings.Count(inner, "'")%2 != 0 {
		return AutorunPlan{}, ValidationError{
			Message: "现有 autorun 命令里的单引号没法安全归一，请先手工整理: " + current.Program,
		}
	}

	plan.After = `/bin/sh -c "` + inner + `"`
	plan.Changed = plan.After != plan.Before
	return plan, nil
}

// composePayload 把旧 autorun 命令整理成能塞进外层双引号的 payload。
//
// 两条路，取决于旧命令本来是什么形态：
//
//   - 已经是 `sh -c "脚本"`：里面是 shell 脚本，原样保留（只把双引号归一成单引号）；
//   - 裸 argv 命令：qbt 原本按 argv 拆分执行，这里逐 token 重新做 shell 引用，
//     语义才不变（`?`、`&`、`;` 这些字符不加引号会被 sh 当成通配/控制符）。
func composePayload(program string) (string, error) {
	trimmed := strings.TrimSpace(program)
	if trimmed == "" {
		return "", nil
	}

	if inner, ok := shellWrapperInner(trimmed); ok {
		return strings.ReplaceAll(strings.TrimSpace(inner), `"`, `'`), nil
	}

	tokens, err := splitArgv(trimmed)
	if err != nil {
		return "", err
	}
	quoted := make([]string, 0, len(tokens))
	for index, token := range tokens {
		text, err := quoteToken(token, index)
		if err != nil {
			return "", err
		}
		quoted = append(quoted, text)
	}
	return strings.Join(quoted, " "), nil
}

// shellWrapperInner 认出 `sh -c "脚本"` 这类已经包了 shell 的命令，返回其中的脚本。
func shellWrapperInner(program string) (string, bool) {
	for _, wrapper := range []string{"/bin/sh -c ", "/bin/bash -c ", "/usr/bin/sh -c ", "sh -c ", "bash -c "} {
		if !strings.HasPrefix(program, wrapper) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(program, wrapper))
		if len(rest) >= 2 {
			first, last := rest[0], rest[len(rest)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				return rest[1 : len(rest)-1], true
			}
		}
	}
	return "", false
}

// splitArgv 按 qbt 的 argv 拆分规则拆旧命令：空白分隔，引号成对且不成 token 的一部分。
func splitArgv(program string) ([]string, error) {
	var (
		tokens  []string
		current strings.Builder
		quote   rune
		started bool
	)

	for _, r := range program {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
			started = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if started {
				tokens = append(tokens, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		return nil, ValidationError{Message: "现有 autorun 命令的引号没有成对: " + program}
	}
	if started {
		tokens = append(tokens, current.String())
	}
	return tokens, nil
}

// quoteToken 给一个 argv token 加 shell 引用。
//
// 规则照 ADR-0007「payload 内一律单引号」：可执行文件与选项本来就不含空格/特殊
// 字符，保持原样好读；其余参数（URL、路径、番号…）一律单引号包起来——
// 这样旧命令里的双引号会被归一成单引号，`?`、`&`、`;` 也不会被 sh 重新解释。
//
// 含单引号的 token 直接拒绝：在外层双引号里嵌单引号只能靠转义拼接，
// 那种命令不该由我们静默改写（宁可不改，也不要拼出一条会跑歪的 shell）。
func quoteToken(token string, index int) (string, error) {
	if strings.ContainsRune(token, '\'') {
		return "", ValidationError{Message: "现有 autorun 命令里有单引号，没法安全归一: " + token}
	}
	if token == "" {
		return "''", nil
	}
	if (index == 0 || strings.HasPrefix(token, "-")) && isShellSafe(token) {
		return token, nil
	}
	return "'" + token + "'", nil
}

// isShellSafe 判断 token 不加引号放进 sh -c 是否还是原样的一个词。
func isShellSafe(token string) bool {
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("_-./:=@%+,", r):
		default:
			return false
		}
	}
	return true
}
