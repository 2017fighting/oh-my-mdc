package translate_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/translate"
)

// 测试缝：translate.Integration 的两个公开入口 —— Test（向导的「测试连接」）与
// Translate（刮削时的译文来源）。
//
// fake 端点锁两件事：**请求形状**（端点、鉴权头、model、temperature、messages 的角色与原文）
// 与**错误分类**（auth/network/timeout/protocol/business 的唯一稳定码）。
// 断言只看这两面，不看客户端内部怎么拼 URL/怎么剥代码围栏。

// ------------------------------------------------------------------ fake

type recorded struct {
	method string
	path   string
	auth   string
	ctype  string
	body   chatRequest
	raw    string
}

type chatRequest struct {
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxTokens   *int    `json:"max_tokens"`
	Messages    []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

type fakeServer struct {
	mu       sync.Mutex
	requests []recorded
	// respond 决定这次回什么；nil 时回一条正常补全。
	respond func(w http.ResponseWriter, req recorded)
}

func newFakeServer(t *testing.T) (*fakeServer, *httptest.Server) {
	t.Helper()
	fake := &fakeServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		req := recorded{
			method: r.Method,
			path:   r.URL.Path,
			auth:   r.Header.Get("Authorization"),
			ctype:  r.Header.Get("Content-Type"),
			raw:    string(raw),
		}
		_ = json.Unmarshal(raw, &req.body)

		fake.mu.Lock()
		fake.requests = append(fake.requests, req)
		respond := fake.respond
		fake.mu.Unlock()

		if respond != nil {
			respond(w, req)
			return
		}
		completion(w, `{"title":"禁欲一月的尽头","outline":"S1 苗条女优的豪华共演"}`)
	}))
	t.Cleanup(srv.Close)
	return fake, srv
}

func (f *fakeServer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeServer) last(t *testing.T) recorded {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("fake 端点没收到任何请求")
	}
	return f.requests[len(f.requests)-1]
}

func (f *fakeServer) setRespond(respond func(w http.ResponseWriter, req recorded)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.respond = respond
}

// completion 回一条 OpenAI 兼容的 chat completions 成功响应。
func completion(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	body, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}},
	})
	_, _ = w.Write(body)
}

func errorEnvelope(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"message":"` + message + `","type":"invalid_request_error"}}`))
}

func config(server *httptest.Server) translate.Config {
	return translate.Config{
		BaseURL: server.URL + "/v1",
		APIKey:  "sk-test-key",
		Model:   "gpt-4o-mini",
		Enabled: true,
	}
}

const (
	jaTitle   = "一ヶ月間の禁欲の果てに"
	jaOutline = "S1スリム美女優の豪華共演"
)

// ------------------------------------------------------------------ Translate

// TestTranslateSendsOneJSONRequest 是 P6-1 的请求形状：一次 POST 到
// {base_url}/chat/completions，Bearer 鉴权，model 照设置，temperature=0，
// 两条消息（固定 system 提示 + 含日文原文的 user），回包 JSON 被解析成两个译文字段。
func TestTranslateSendsOneJSONRequest(t *testing.T) {
	fake, srv := newFakeServer(t)
	integration := translate.New(translate.Options{})

	got, err := integration.Translate(context.Background(), config(srv),
		translate.Text{Title: jaTitle, Outline: jaOutline})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}

	if got.Title != "禁欲一月的尽头" || got.Outline != "S1 苗条女优的豪华共演" {
		t.Errorf("译文 = %+v, want 回包里的两个字段", got)
	}
	if fake.count() != 1 {
		t.Fatalf("请求数 = %d, want 1（标题与简介合并在一次请求里）", fake.count())
	}

	req := fake.last(t)
	if req.method != http.MethodPost || req.path != "/v1/chat/completions" {
		t.Errorf("%s %s, want POST /v1/chat/completions", req.method, req.path)
	}
	if req.auth != "Bearer sk-test-key" {
		t.Errorf("Authorization = %q, want Bearer 设置里的 api_key", req.auth)
	}
	if !strings.HasPrefix(req.ctype, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", req.ctype)
	}
	if req.body.Model != "gpt-4o-mini" {
		t.Errorf("model = %q, want 设置里的 model", req.body.Model)
	}
	if req.body.Temperature != 0 {
		t.Errorf("temperature = %v, want 0（译文要稳）", req.body.Temperature)
	}
	if len(req.body.Messages) != 2 {
		t.Fatalf("messages = %d 条, want 2（system 提示 + user 原文）", len(req.body.Messages))
	}
	if req.body.Messages[0].Role != "system" || req.body.Messages[1].Role != "user" {
		t.Errorf("角色 = %q/%q, want system/user", req.body.Messages[0].Role, req.body.Messages[1].Role)
	}
	if !strings.Contains(req.body.Messages[0].Content, "JSON") {
		t.Errorf("system 提示没说清要 JSON 回包: %q", req.body.Messages[0].Content)
	}
	if user := req.body.Messages[1].Content; !strings.Contains(user, jaTitle) || !strings.Contains(user, jaOutline) {
		t.Errorf("user 消息没带上日文原文: %q", user)
	}
}

// TestTranslateStripsCodeFences 确认回包的围栏被剥掉：模型常把 JSON 包在 ```json 里，
// 不剥就等于「每次都翻译失败」。
func TestTranslateStripsCodeFences(t *testing.T) {
	fake, srv := newFakeServer(t)
	fake.setRespond(func(w http.ResponseWriter, _ recorded) {
		completion(w, "```json\n{\"title\":\"禁欲一月\",\"outline\":\"简介\"}\n```")
	})
	integration := translate.New(translate.Options{})

	got, err := integration.Translate(context.Background(), config(srv),
		translate.Text{Title: jaTitle, Outline: jaOutline})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if got.Title != "禁欲一月" || got.Outline != "简介" {
		t.Errorf("译文 = %+v, want 剥掉围栏后的 JSON", got)
	}
}

// TestTranslateIgnoresFieldsThatWereNotAskedFor 确认没原文的字段不会被填上：
// 模型爱给空简介编一段，把编出来的东西写进库比「没有译文」更坏。
func TestTranslateIgnoresFieldsThatWereNotAskedFor(t *testing.T) {
	fake, srv := newFakeServer(t)
	integration := translate.New(translate.Options{})

	got, err := integration.Translate(context.Background(), config(srv), translate.Text{Title: jaTitle})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if got.Outline != "" {
		t.Errorf("outline = %q, want 空（原文没有简介）", got.Outline)
	}
	if fake.count() != 1 {
		t.Errorf("请求数 = %d, want 1", fake.count())
	}
}

// TestTranslateSkipsTheRequestWhenThereIsNothingToTranslate 钉住「没有原文就不发请求」：
// 空标题空简介不该花掉一次上游调用，更不该把空串写回译文列。
func TestTranslateSkipsTheRequestWhenThereIsNothingToTranslate(t *testing.T) {
	fake, srv := newFakeServer(t)
	integration := translate.New(translate.Options{})

	got, err := integration.Translate(context.Background(), config(srv), translate.Text{})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if got.Title != "" || got.Outline != "" {
		t.Errorf("译文 = %+v, want 空", got)
	}
	if fake.count() != 0 {
		t.Errorf("请求数 = %d, want 0", fake.count())
	}
}

// ------------------------------------------------------------------ Test

// TestTestSendsOneTokenProbe 是 P6-1 的「测试连接」：1 token 的极小补全，
// 绿了才写 last_test_ok。
func TestTestSendsOneTokenProbe(t *testing.T) {
	fake, srv := newFakeServer(t)
	integration := translate.New(translate.Options{})

	result := integration.Test(context.Background(), settings.Section{Name: "ai_translation"},
		settings.TestInput{Values: map[string]any{
			"base_url": srv.URL + "/v1", "api_key": "sk-test-key", "model": "gpt-4o-mini",
		}, Enabled: true})

	if !result.OK || result.Code != "" {
		t.Fatalf("result = %+v, want 绿", result)
	}
	if !strings.Contains(result.Detail, "gpt-4o-mini") {
		t.Errorf("detail = %q, want 带上 model（人要知道测的是哪个模型）", result.Detail)
	}

	req := fake.last(t)
	if req.path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", req.path)
	}
	if req.body.MaxTokens == nil || *req.body.MaxTokens != 1 {
		t.Errorf("max_tokens = %v, want 1（测试连接是 1 token 的极小请求）", req.body.MaxTokens)
	}
	if len(req.body.Messages) == 0 {
		t.Error("测试请求没有 messages")
	}
}

// TestTestRejectsIncompleteConfigWithoutNetwork 确认本地校验先于任何请求：
// 缺 key 报 auth（换凭据），缺 base_url/model 报 business（改配置）。
func TestTestRejectsIncompleteConfigWithoutNetwork(t *testing.T) {
	fake, srv := newFakeServer(t)
	integration := translate.New(translate.Options{})

	cases := map[string]struct {
		values map[string]any
		want   settings.TestCode
	}{
		"缺 api_key":  {map[string]any{"base_url": srv.URL, "model": "gpt-4o-mini"}, settings.CodeAuth},
		"缺 base_url": {map[string]any{"api_key": "sk", "model": "gpt-4o-mini"}, settings.CodeBusiness},
		"缺 model":    {map[string]any{"base_url": srv.URL, "api_key": "sk"}, settings.CodeBusiness},
	}
	for name, c := range cases {
		result := integration.Test(context.Background(), settings.Section{Name: "ai_translation"},
			settings.TestInput{Values: c.values})
		if result.OK || result.Code != c.want {
			t.Errorf("%s: result = %+v, want code %q", name, result, c.want)
		}
	}
	if fake.count() != 0 {
		t.Errorf("本地校验失败仍然上网了 %d 次", fake.count())
	}
}

// ------------------------------------------------------------------ 分类

// TestClassifiesUpstreamFailures 钉住规格 §8.4 的错误分类：
// 凭据 → auth；上游一时不对（429/5xx）→ network（可重试）；请求本身不合规 → business；
// 不认得的响应 → protocol。翻译失败的降级路径与告警文案都读这个分类。
func TestClassifiesUpstreamFailures(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   settings.TestCode
	}{
		"401":          {http.StatusUnauthorized, "", settings.CodeAuth},
		"403":          {http.StatusForbidden, "", settings.CodeAuth},
		"500":          {http.StatusInternalServerError, "", settings.CodeNetwork},
		"503":          {http.StatusServiceUnavailable, "", settings.CodeNetwork},
		"429":          {http.StatusTooManyRequests, "", settings.CodeNetwork},
		"404 模型/路径不存在": {http.StatusNotFound, "", settings.CodeBusiness},
		"400 参数错误":     {http.StatusBadRequest, "", settings.CodeBusiness},
	}

	for name, c := range cases {
		fake, srv := newFakeServer(t)
		fake.setRespond(func(w http.ResponseWriter, _ recorded) {
			errorEnvelope(w, c.status, "upstream says no")
		})
		integration := translate.New(translate.Options{})

		_, err := integration.Translate(context.Background(), config(srv),
			translate.Text{Title: jaTitle})
		if got := translate.Classify(err); got != c.want {
			t.Errorf("%s: Classify = %q, want %q (err=%v)", name, got, c.want, err)
		}
	}
}

// TestUnparsableResponsesAreProtocolErrors 钉住「认不出的回包」：
// 不是 JSON、没有 choices、译文不是 JSON —— 都不许被当成成功（空译文入库比失败更坏）。
func TestUnparsableResponsesAreProtocolErrors(t *testing.T) {
	cases := map[string]string{
		"不是 JSON":    "<html>gateway</html>",
		"没有 choices": `{"choices":[]}`,
	}
	for name, body := range cases {
		fake, srv := newFakeServer(t)
		fake.setRespond(func(w http.ResponseWriter, _ recorded) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		})
		integration := translate.New(translate.Options{})

		result := integration.Test(context.Background(), settings.Section{Name: "ai_translation"},
			settings.TestInput{Values: map[string]any{
				"base_url": srv.URL, "api_key": "sk", "model": "m",
			}})
		if result.Code != settings.CodeProtocol {
			t.Errorf("%s: test code = %q, want protocol", name, result.Code)
		}

		_, err := integration.Translate(context.Background(), config(srv),
			translate.Text{Title: jaTitle})
		if got := translate.Classify(err); got != settings.CodeProtocol {
			t.Errorf("%s: translate classify = %q, want protocol (err=%v)", name, got, err)
		}
	}

	// 译文不是 JSON：同上，不能把「模型的散文」当译文写进库。
	fake, srv := newFakeServer(t)
	fake.setRespond(func(w http.ResponseWriter, _ recorded) {
		completion(w, "当然可以！以下是译文：……")
	})
	integration := translate.New(translate.Options{})
	if _, err := integration.Translate(context.Background(), config(srv),
		translate.Text{Title: jaTitle}); translate.Classify(err) != settings.CodeProtocol {
		t.Errorf("散文回包 classify = %q, want protocol (err=%v)", translate.Classify(err), err)
	}
}

// TestTimeoutIsClassifiedAsTimeout 钉住超时这条分类：上游挂住时 chip 要显示 timeout，
// 而不是含糊的 network。
func TestTimeoutIsClassifiedAsTimeout(t *testing.T) {
	fake, srv := newFakeServer(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	fake.setRespond(func(w http.ResponseWriter, _ recorded) {
		<-release
		completion(w, `{"title":"x"}`)
	})
	integration := translate.New(translate.Options{})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := integration.Translate(ctx, config(srv), translate.Text{Title: jaTitle})
	if got := translate.Classify(err); got != settings.CodeTimeout {
		t.Errorf("Classify = %q, want timeout (err=%v)", got, err)
	}
}

// TestErrorsNeverLeakTheAPIKey 确认错误文案里不含凭据：它会进 last_test_error 与
// task_event，凭据原文不许出现在那两处（规格 §4.4 脱敏）。
func TestErrorsNeverLeakTheAPIKey(t *testing.T) {
	fake, srv := newFakeServer(t)
	fake.setRespond(func(w http.ResponseWriter, req recorded) {
		errorEnvelope(w, http.StatusInternalServerError, "boom")
	})
	integration := translate.New(translate.Options{})

	_, err := integration.Translate(context.Background(), config(srv), translate.Text{Title: jaTitle})
	if err == nil {
		t.Fatal("500 应当报错")
	}
	if strings.Contains(err.Error(), "sk-test-key") {
		t.Errorf("错误文案里带出了 api key: %v", err)
	}
}
