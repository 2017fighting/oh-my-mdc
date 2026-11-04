package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// 测试缝：Integration 对 HTTP fake（规格 §5.6 的两步测试与发送分流）。

// fakeTelegram 是一次假的 Bot API：按 path 分发，记下每一次请求。
type fakeTelegram struct {
	mu       sync.Mutex
	requests []recorded
	handlers map[string]http.HandlerFunc
}

type recorded struct {
	path string
	body string
}

func newFakeTelegram(t *testing.T) (*fakeTelegram, *httptest.Server) {
	t.Helper()
	f := &fakeTelegram{handlers: map[string]http.HandlerFunc{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{path: r.URL.Path, body: string(body)})
		handler := f.handlers[r.URL.Path]
		f.mu.Unlock()
		if handler != nil {
			handler(w, r)
			return
		}
		jsonReply(w, http.StatusOK, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"mdc_bot"}}`)
	}))
	t.Cleanup(server.Close)
	return f, server
}

func (f *fakeTelegram) recorded() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

func jsonReply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func testInput(values map[string]any) settings.TestInput {
	base := map[string]any{
		"bot_token":                "123456:secret-token",
		"chat_id":                  "-100200300",
		"enabled":                  true,
		"notify_download_started":  true,
		"notify_filed":             true,
		"notify_processing_failed": true,
		"notify_download_stalled":  true,
	}
	for key, value := range values {
		base[key] = value
	}
	return settings.TestInput{Values: base}
}

// TestTestConnectionRunsGetMeThenMessage 钉住两步绿（P12-1）：getMe 验 token，
// sendMessage 验 chat——两步都过才绿，第二步带 HTML parse_mode。
func TestTestConnectionRunsGetMeThenMessage(t *testing.T) {
	f, server := newFakeTelegram(t)
	f.handlers["/bot123456:secret-token/sendMessage"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, http.StatusOK, `{"ok":true,"result":{"message_id":7}}`)
	}

	integration := New(Options{Host: server.URL})
	result := integration.Test(context.Background(), settings.Section{}, testInput(nil))

	if !result.OK {
		t.Fatalf("Test = %+v, want OK", result)
	}
	if !strings.Contains(result.Detail, "mdc_bot") {
		t.Errorf("detail = %q, want 带上 bot 用户名（证明 getMe 真过了）", result.Detail)
	}
	paths := f.paths()
	if len(paths) != 2 || !strings.HasSuffix(paths[0], "/getMe") || !strings.HasSuffix(paths[1], "/sendMessage") {
		t.Fatalf("请求 = %v, want getMe → sendMessage 两步", paths)
	}
	if !strings.Contains(f.recorded()[1].body, `"parse_mode":"HTML"`) {
		t.Errorf("sendMessage 体 = %s, want parse_mode=HTML", f.recorded()[1].body)
	}
}

// TestTestConnectionTokenInvalid：token 被拒（401）就停在这一步，不再拿坏 token 去发消息。
func TestTestConnectionTokenInvalid(t *testing.T) {
	f, server := newFakeTelegram(t)
	f.handlers["/bot123456:secret-token/getMe"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, http.StatusUnauthorized, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	}

	integration := New(Options{Host: server.URL})
	result := integration.Test(context.Background(), settings.Section{}, testInput(nil))

	if result.OK || result.Code != settings.CodeTokenInvalid {
		t.Fatalf("Test = %+v, want code=token_invalid", result)
	}
	if paths := f.paths(); len(paths) != 1 {
		t.Errorf("请求 = %v, want 只打 getMe", paths)
	}
}

// TestTestConnectionChatUnavailable：token 没问题但 chat 未激活（400 chat not found）→ chat_unavailable。
func TestTestConnectionChatUnavailable(t *testing.T) {
	f, server := newFakeTelegram(t)
	f.handlers["/bot123456:secret-token/sendMessage"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, http.StatusBadRequest,
			`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	}

	integration := New(Options{Host: server.URL})
	result := integration.Test(context.Background(), settings.Section{}, testInput(nil))

	if result.OK || result.Code != settings.CodeChatUnavailable {
		t.Fatalf("Test = %+v, want code=chat_unavailable", result)
	}
	if !strings.Contains(result.Detail, "chat not found") {
		t.Errorf("detail = %q, want 原样带上上游文案（帮人定位）", result.Detail)
	}
}

// TestTestConnectionNetworkErrorKeepsItsCode：连不上不是 token 的错，别误导用户去换 token。
//
// 连接被拒走注入的往返器，不碰真实网络：赌「某端口立刻回 RST」的用例在沙箱里会失效——
// SYN 被黑洞掉时既没有 RST 也没有回执，外层客户端超时先到，结论就从 network 变成 timeout，
// 同一份代码本机红、CI 绿。
func TestTestConnectionNetworkErrorKeepsItsCode(t *testing.T) {
	// 装上与正式运行同款的 defaultHTTPTimeout：这是「被拒」形状下最容易把它误判成超时的那层。
	// 换正式跑测试时，上面还有 settings.TestTimeout 的 10s 总预算，不再叠加一层。
	transport := &refusedTransport{}
	integration := New(Options{
		Host: "http://telegram.invalid", // .invalid 永不解析：万一注入失效也不会真去敲上游
		HTTP: &http.Client{Timeout: defaultHTTPTimeout, Transport: transport},
	})

	result := integration.Test(context.Background(), settings.Section{}, testInput(nil))

	if result.OK || result.Code != settings.CodeNetwork {
		t.Fatalf("Test = %+v, want code=network（连接被拒是 network，不是 timeout）", result)
	}
	if transport.calls == 0 {
		t.Fatal("注入的往返器一次都没被调用：请求绕过它出去了，这条用例就不再证明任何事")
	}
	if !strings.Contains(result.Detail, "连不上") {
		t.Errorf("detail = %q, want 说清是连不上，而不是含糊的失败", result.Detail)
	}
	assertNoTokenLeak(t, result.Detail)
}

// TestTestConnectionTimeoutKeepsItsCode：真等不到回执才叫 timeout，外层客户端超时只服务这一类。
// 两种形状各一条：响应头迟迟不来，与响应体读到一半停住（client 在 io.ReadAll 上等）。
func TestTestConnectionTimeoutKeepsItsCode(t *testing.T) {
	cases := []struct {
		name    string
		respond func(http.ResponseWriter)
	}{
		{"等不到响应头", func(http.ResponseWriter) {}},
		{"响应体读到一半", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"ok":true,`)
			w.(http.Flusher).Flush()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				tc.respond(w)
				<-release // 之后一个字节都不再写：客户端只能等到自己的超时
			}))
			t.Cleanup(func() {
				close(release) // 先放走挂住的 handler，server.Close 才不会等它
				server.Close()
			})

			integration := New(Options{
				Host: server.URL,
				HTTP: &http.Client{Timeout: 50 * time.Millisecond},
			})
			result := integration.Test(context.Background(), settings.Section{}, testInput(nil))

			if result.OK || result.Code != settings.CodeTimeout {
				t.Fatalf("Test = %+v, want code=timeout（等不到回执）", result)
			}
			if !strings.Contains(result.Detail, "超时") {
				t.Errorf("detail = %q, want 说清是超时", result.Detail)
			}
			assertNoTokenLeak(t, result.Detail)
		})
	}
}

// TestTestConnectionWithoutTokenDoesNotTouchUpstream：缺 token/chat 是本地就能判定的，
// 不发请求（错误码仍用两步测试的词汇，chip 才说得清该改哪一格）。
func TestTestConnectionWithoutTokenDoesNotTouchUpstream(t *testing.T) {
	f, server := newFakeTelegram(t)
	integration := New(Options{Host: server.URL})

	noToken := integration.Test(context.Background(), settings.Section{},
		testInput(map[string]any{"bot_token": ""}))
	if noToken.OK || noToken.Code != settings.CodeTokenInvalid {
		t.Errorf("缺 token = %+v, want token_invalid", noToken)
	}
	noChat := integration.Test(context.Background(), settings.Section{},
		testInput(map[string]any{"chat_id": ""}))
	if noChat.OK || noChat.Code != settings.CodeChatUnavailable {
		t.Errorf("缺 chat = %+v, want chat_unavailable", noChat)
	}
	if requests := f.recorded(); len(requests) != 0 {
		t.Errorf("请求 = %d 次, want 0（本地校验先于上游）", len(requests))
	}
}

// TestNotifySendsRenderedHTML 验证执行半段：渲染 + sendMessage（HTML、无图）。
func TestNotifySendsRenderedHTML(t *testing.T) {
	f, server := newFakeTelegram(t)
	f.handlers["/bot123456:secret-token/sendMessage"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, http.StatusOK, `{"ok":true,"result":{"message_id":8}}`)
	}

	integration := New(Options{Host: server.URL})
	err := integration.Notify(context.Background(), ConfigFrom(testInput(nil)), EventFiled, map[string]any{
		"number": "SSIS-001",
		"title":  "标题 <b>不该是标签</b>",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	requests := f.recorded()
	if len(requests) != 1 || !strings.HasSuffix(requests[0].path, "/sendMessage") {
		t.Fatalf("请求 = %v, want 一条 sendMessage", requests)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(requests[0].body), &body); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if body["parse_mode"] != "HTML" {
		t.Errorf("parse_mode = %v, want HTML", body["parse_mode"])
	}
	if body["chat_id"] != "-100200300" {
		t.Errorf("chat_id = %v, want 设置里的值", body["chat_id"])
	}
	text, _ := body["text"].(string)
	if !strings.Contains(text, "&lt;b&gt;不该是标签&lt;/b&gt;") {
		t.Errorf("text = %q, want 用户内容转义", text)
	}
	if strings.Contains(text, "sendPhoto") {
		t.Errorf("text = %q, 不该有图", text)
	}
}

// TestNotifyRejectsMissingSettings：没配 bot_token/chat_id 是本地错，不打扰上游。
func TestNotifyRejectsMissingSettings(t *testing.T) {
	f, server := newFakeTelegram(t)
	integration := New(Options{Host: server.URL})

	err := integration.Notify(context.Background(), Config{BotToken: "tk", ChatID: ""}, EventFiled,
		map[string]any{"number": "SSIS-001"})
	if err == nil {
		t.Fatal("缺 chat_id 应当报错")
	}
	if requests := f.recorded(); len(requests) != 0 {
		t.Errorf("请求 = %d 次, want 0", len(requests))
	}
}

// TestClassifySeparatesRetryableFromPermanent 钉住发送分流（P12-3）：
// 400/401/403 是永久（立即驻留），429/5xx 与网络错是瞬时（退避重试）。
func TestClassifySeparatesRetryableFromPermanent(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		code      settings.TestCode
		permanent bool
	}{
		{"400 被拒", &Error{Code: settings.CodeBusiness, Status: 400}, settings.CodeBusiness, true},
		{"401 token", &Error{Code: settings.CodeAuth, Status: 401}, settings.CodeAuth, true},
		{"403 被踢", &Error{Code: settings.CodeAuth, Status: 403}, settings.CodeAuth, true},
		{"429 限流", &Error{Code: settings.CodeProtocol, Status: 429}, settings.CodeProtocol, false},
		{"500 上游", &Error{Code: settings.CodeProtocol, Status: 500}, settings.CodeProtocol, false},
		{"网络", &Error{Code: settings.CodeNetwork}, settings.CodeNetwork, false},
		{"超时", &Error{Code: settings.CodeTimeout}, settings.CodeTimeout, false},
		{"本地校验", ValidationError{Message: "缺 chat_id"}, settings.CodeBusiness, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.err); got != tc.code {
				t.Errorf("Classify = %s, want %s", got, tc.code)
			}
			if got := IsPermanent(tc.err); got != tc.permanent {
				t.Errorf("IsPermanent = %v, want %v", got, tc.permanent)
			}
		})
	}
}

// TestErrorNeverLeaksToken：token 在 URL 路径里，任何错误文案都不许把它带出来
// （会写进 last_test_error 与 task_event）。
func TestErrorNeverLeaksToken(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(`Get "https://api.telegram.org/bot123456:secret-token/getMe": dial tcp: no route`)
	})
	integration := New(Options{Host: "https://api.telegram.org", HTTP: &http.Client{Transport: transport}})

	result := integration.Test(context.Background(), settings.Section{}, testInput(nil))
	assertNoTokenLeak(t, result.Detail)
}

// assertNoTokenLeak：token 在请求路径里，net/http 的 *url.Error 会把整条 URL 带出来，
// 而这些文案会进 last_test_error 与 task_event。
func assertNoTokenLeak(t *testing.T, detail string) {
	t.Helper()
	if strings.Contains(detail, "secret-token") {
		t.Errorf("detail = %q, want 不泄露 bot token", detail)
	}
}

// refusedTransport 复现「端口上没有进程监听」的真实失败形状：net 层拿到 ECONNREFUSED。
// 必须是真的 ECONNREFUSED 而不是一句自造文案——分类看类型，不看字符串。
// calls 是注入生效的证据：请求绕过去出网了，这条用例就什么也没证明。
type refusedTransport struct{ calls int }

func (t *refusedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (f *fakeTelegram) paths() []string {
	requests := f.recorded()
	paths := make([]string, 0, len(requests))
	for _, request := range requests {
		paths = append(paths, request.path)
	}
	return paths
}

// TestTestConnectionChatForbiddenAtStepTwoIsChatUnavailable 钉住两步测试的判词方向：
// getMe 已经过了，sendMessage 上的 403 是「bot 不在这个 chat 里」，不是 token 无效——
// 报错 token_invalid 会让人去换一把好 token（规格 §5.6 的两个码的意义就在此）。
func TestTestConnectionChatForbiddenAtStepTwoIsChatUnavailable(t *testing.T) {
	f, server := newFakeTelegram(t)
	f.handlers["/bot123456:secret-token/sendMessage"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, http.StatusForbidden,
			`{"ok":false,"error_code":403,"description":"Forbidden: bot was kicked from the supergroup chat"}`)
	}

	integration := New(Options{Host: server.URL})
	result := integration.Test(context.Background(), settings.Section{}, testInput(nil))

	if result.OK {
		t.Fatal("被踢出群的 chat 不该报绿")
	}
	if result.Code != settings.CodeChatUnavailable {
		t.Fatalf("code = %s, want chat_unavailable（token 刚刚被 getMe 验过）", result.Code)
	}
	if !strings.Contains(result.Detail, "kicked") {
		t.Errorf("detail = %q, want 原样带上上游原话", result.Detail)
	}
}

// TestTestConnectionTokenRevokedAtStepTwoIsTokenInvalid：401 是例外——两步之间 token 被吊销，
// 那确实是 token 的问题，别让人去翻 chat_id。
func TestTestConnectionTokenRevokedAtStepTwoIsTokenInvalid(t *testing.T) {
	f, server := newFakeTelegram(t)
	f.handlers["/bot123456:secret-token/sendMessage"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, http.StatusUnauthorized, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	}

	integration := New(Options{Host: server.URL})
	result := integration.Test(context.Background(), settings.Section{}, testInput(nil))

	if result.OK || result.Code != settings.CodeTokenInvalid {
		t.Fatalf("Test = %+v, want code=token_invalid", result)
	}
}

// TestEventSwitchNamesAreRealSettingsColumns 是「事件表 ↔ 设置列」的反射守卫：
// telegram 表新增/改名勾选框时，事件表漏改会在这里红，而不是在向导里静默不生效。
func TestEventSwitchNamesAreRealSettingsColumns(t *testing.T) {
	section, ok := settings.Lookup("telegram")
	if !ok {
		t.Fatal("settings 里没有 telegram 节")
	}
	for event, spec := range events {
		field, ok := section.Field(spec.switchName)
		if !ok || field.Type != settings.TypeBool {
			t.Errorf("%s 的开关列 %q 不是 telegram 节的布尔字段", event, spec.switchName)
		}
	}
}
