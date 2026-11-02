package qbittorrent

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// fakeRequest 是 fake 收到的一次请求（表单/多部分已解析好，断言直接读它）。
type fakeRequest struct {
	Method    string
	Path      string
	Query     url.Values
	Header    http.Header
	Form      url.Values
	Multipart *multipart.Form
	Raw       []byte
}

// fakeQbt 是一个最小的 qBittorrent WebUI API v2 fake：按 "METHOD path" 分派。
type fakeQbt struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	requests []fakeRequest
}

func newFakeQbt(t *testing.T, routes map[string]http.HandlerFunc) *fakeQbt {
	t.Helper()

	// 登录回执是每个用例都需要的公共半边；需要测坏凭据的用例自己覆写它。
	handlers := make(map[string]http.HandlerFunc, len(routes)+1)
	for key, handler := range routes {
		handlers[key] = handler
	}
	if _, ok := handlers["POST /api/v2/auth/login"]; !ok {
		handlers["POST /api/v2/auth/login"] = plainLogin(true)
	}

	fake := &fakeQbt{t: t}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读请求体: %v", err)
			return
		}
		record := fakeRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.Query(),
			Header: r.Header.Clone(),
			Raw:    raw,
		}
		if contentType := r.Header.Get("Content-Type"); contentType != "" {
			mediaType, params, _ := mime.ParseMediaType(contentType)
			switch mediaType {
			case "application/x-www-form-urlencoded":
				form, _ := url.ParseQuery(string(raw))
				record.Form = form
			case "multipart/form-data":
				reader := multipart.NewReader(strings.NewReader(string(raw)), params["boundary"])
				parsed, _ := reader.ReadForm(8 << 20)
				record.Multipart = parsed
			}
		}
		fake.mu.Lock()
		fake.requests = append(fake.requests, record)
		fake.mu.Unlock()

		handler, ok := handlers[r.Method+" "+r.URL.Path]
		if !ok {
			t.Logf("fake qbt: 没有为 %s %s 配 handler", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

// requestsTo 返回打到某条路径的请求（按发生顺序）。
func (f *fakeQbt) requestsTo(method, path string) []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	var matched []fakeRequest
	for _, request := range f.requests {
		if request.Method == method && request.Path == path {
			matched = append(matched, request)
		}
	}
	return matched
}

func (f *fakeQbt) config() Config {
	return Config{URL: f.server.URL, Username: "admin", Password: "pw", Category: DefaultCategory}
}

// loginOK 是两种登录回执的公共半边：4.6.5 的纯文本与 5.x 的 JSON。
func plainLogin(ok bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if ok {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid-4", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Fails."))
	}
}

func jsonLogin(ok bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if ok {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid-5", Path: "/"})
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":false,"error":"invalid credentials"}`))
	}
}

func versionHandler(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(version + "\n"))
	}
}

// TestLoginAcceptsPlainTextOkAndKeepsSID 钉住 4.6.5 形态：纯文本 "Ok." + SID cookie，
// 后续请求带着这个 cookie 与同源 Referer。
func TestLoginAcceptsPlainTextOkAndKeepsSID(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"POST /api/v2/auth/login":     plainLogin(true),
		"GET /api/v2/app/version":     versionHandler("v4.6.5"),
		"GET /api/v2/app/preferences": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) },
	})

	integration := New(Options{})
	result := integration.Test(context.Background(), settings.Section{Name: "qbittorrent"}, inputOf(fake.config()))
	if !result.OK {
		t.Fatalf("result = %+v, want 绿", result)
	}

	version := fake.requestsTo(http.MethodGet, "/api/v2/app/version")
	if len(version) != 1 {
		t.Fatalf("version 请求 %d 次, want 1", len(version))
	}
	if cookie := version[0].Header.Get("Cookie"); cookie != "SID=sid-4" {
		t.Errorf("Cookie = %q, want SID=sid-4", cookie)
	}
	if referer := version[0].Header.Get("Referer"); referer != fake.server.URL {
		t.Errorf("Referer = %q, want %q（qbt 的 CSRF 检查）", referer, fake.server.URL)
	}
	if !strings.Contains(result.Detail, "v4.6.5") {
		t.Errorf("detail = %q, want 版本号", result.Detail)
	}
}

// TestLoginAcceptsJSONSuccess 钉住 5.x 形态：{"success":true}。
func TestLoginAcceptsJSONSuccess(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"POST /api/v2/auth/login": jsonLogin(true),
		"GET /api/v2/app/version": versionHandler("v5.0.3"),
		"GET /api/v2/app/preferences": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"autorun_enabled":true,"autorun_program":"curl 'http://x'"}`))
		},
	})

	result := New(Options{}).Test(context.Background(), settings.Section{Name: "qbittorrent"}, inputOf(fake.config()))
	if !result.OK {
		t.Fatalf("result = %+v, want 绿（5.x JSON 登录）", result)
	}
}

// TestLoginRejectsBadCredentialsAsAuth 确认两种失败回执都归 auth，而不是 protocol。
func TestLoginRejectsBadCredentialsAsAuth(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"4.6.5 Fails.": plainLogin(false),
		"5.x JSON":     jsonLogin(false),
		"403 封 IP": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeQbt(t, map[string]http.HandlerFunc{"POST /api/v2/auth/login": handler})

			result := New(Options{}).Test(context.Background(), settings.Section{Name: "qbittorrent"}, inputOf(fake.config()))
			if result.OK {
				t.Fatal("坏凭据不该绿")
			}
			if result.Code != settings.CodeAuth {
				t.Errorf("code = %q, want auth（%s）", result.Code, result.Detail)
			}
		})
	}
}

// TestTestReadsVersionAndAutorun 钉住测试内容：登录 + 版本读 + autorun 现值读（供 diff）。
func TestConnectionReadsVersionAndAutorun(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"POST /api/v2/auth/login": plainLogin(true),
		"GET /api/v2/app/version": versionHandler("v4.6.5"),
		"GET /api/v2/app/preferences": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"autorun_enabled":false,"autorun_program":"curl \"http://moviepilot\""}`))
		},
	})

	result := New(Options{}).Test(context.Background(), settings.Section{Name: "qbittorrent"}, inputOf(fake.config()))
	if !result.OK {
		t.Fatalf("result = %+v, want 绿", result)
	}
	if !strings.Contains(result.Detail, "autorun 现值") {
		t.Errorf("detail = %q, want 说明 autorun 现值已读到（供 diff）", result.Detail)
	}
	if strings.Contains(result.Detail, "moviepilot") {
		t.Errorf("detail = %q, 不该把 autorun 原命令（含 token）抄进 chip", result.Detail)
	}
	if !strings.Contains(result.Detail, "关闭") {
		t.Errorf("detail = %q, want autorun 未启用的警告", result.Detail)
	}
}

// TestTestClassifiesProtocolOnUnexpectedLoginBody 确认认不出的登录回执归 protocol：
// 那是版本行为不一致，要查代码，不是改密码。
func TestConnectionClassifiesProtocolOnUnexpectedLoginBody(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"POST /api/v2/auth/login": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>hello</html>"))
		},
	})

	result := New(Options{}).Test(context.Background(), settings.Section{Name: "qbittorrent"}, inputOf(fake.config()))
	if result.Code != settings.CodeProtocol {
		t.Errorf("code = %q, want protocol", result.Code)
	}
}

// TestTestClassifiesNetworkAndTimeout 确认连不上与超时分开报。
func TestConnectionClassifiesNetworkAndTimeout(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	network := New(Options{}).Test(context.Background(), settings.Section{Name: "qbittorrent"},
		inputOf(Config{URL: closedURL, Category: DefaultCategory}))
	if network.Code != settings.CodeNetwork {
		t.Errorf("code = %q, want network（%s）", network.Code, network.Detail)
	}

	slow := newFakeQbt(t, map[string]http.HandlerFunc{
		"POST /api/v2/auth/login": func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write([]byte("Ok."))
		},
	})
	integration := New(Options{
		HTTP: &http.Client{Timeout: 50 * time.Millisecond},
	})
	timedOut := integration.Test(context.Background(), settings.Section{Name: "qbittorrent"}, inputOf(slow.config()))
	if timedOut.Code != settings.CodeTimeout {
		t.Errorf("code = %q, want timeout（%s）", timedOut.Code, timedOut.Detail)
	}
}

// TestPropertiesNotFoundIsNotAnError 确认 404 是「不在」，不是失败。
func TestPropertiesNotFoundIsNotAnError(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties": func(w http.ResponseWriter, _ *http.Request) {
			http.NotFound(w, nil)
		},
	})
	c := mustLogin(t, fake)

	_, found, err := c.properties(context.Background(), "abc")
	if err != nil {
		t.Fatalf("properties: %v", err)
	}
	if found {
		t.Error("404 应当算「不在」")
	}
	if got := fake.requestsTo(http.MethodGet, "/api/v2/torrents/properties")[0].Query.Get("hash"); got != "abc" {
		t.Errorf("hash 参数 = %q, want abc", got)
	}
}

// TestSetPreferencesWritesOnlyAutorunProgram 钉住 ADR-0007：
// 只写 autorun_program，绝不碰 autorun_on_torrent_added_*。
func TestSetPreferencesWritesOnlyAutorunProgram(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties": func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
		"POST /api/v2/app/setPreferences": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
	})
	c := mustLogin(t, fake)

	if err := c.setPreferences(context.Background(), map[string]any{"autorun_program": "next"}); err != nil {
		t.Fatalf("setPreferences: %v", err)
	}

	sent := fake.requestsTo(http.MethodPost, "/api/v2/app/setPreferences")
	if len(sent) != 1 {
		t.Fatalf("setPreferences 调用 %d 次, want 1", len(sent))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(sent[0].Form.Get("json")), &payload); err != nil {
		t.Fatalf("json 字段不是 JSON: %v", err)
	}
	if len(payload) != 1 || payload["autorun_program"] != "next" {
		t.Errorf("payload = %v, want 只有 autorun_program", payload)
	}
	for key := range payload {
		if strings.Contains(key, "autorun_on_torrent_added") {
			t.Errorf("绝不碰 %s（ADR-0007）", key)
		}
	}
}

// TestAddMagnetSendsExplicitAddFields 钉住 §5.4 的加种参数面：
// 显式 savepath/category/createCategories + tags，且不设 autoTMM、contentLayout。
func TestAddMagnetSendsExplicitAddFields(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"POST /api/v2/torrents/add": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("Ok."))
		},
	})
	c := mustLogin(t, fake)

	request := addRequest{
		Magnet:   "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		SavePath: "/media/downloads/completed/av",
		Category: "mdc",
		Tags:     "ABP-123," + TagInProgress,
	}
	if err := c.addTorrent(context.Background(), request); err != nil {
		t.Fatalf("addTorrent: %v", err)
	}

	sent := fake.requestsTo(http.MethodPost, "/api/v2/torrents/add")
	if len(sent) != 1 {
		t.Fatalf("add 调用 %d 次, want 1", len(sent))
	}
	// 磁力走 urlencoded 的 urls=（规格 §5.4：磁力 urls=，私种 multipart torrents=）。
	form := sent[0].Form
	if form == nil {
		t.Fatalf("磁力加种应当是 application/x-www-form-urlencoded，实际 Content-Type = %q", sent[0].Header.Get("Content-Type"))
	}
	value := form.Get
	if got := value("urls"); got != request.Magnet {
		t.Errorf("urls = %q, want %q", got, request.Magnet)
	}
	if got := value("savepath"); got != request.SavePath {
		t.Errorf("savepath = %q, want %q", got, request.SavePath)
	}
	if got := value("category"); got != "mdc" {
		t.Errorf("category = %q, want mdc", got)
	}
	if got := value("createCategories"); got != "true" {
		t.Errorf("createCategories = %q, want true（显式建分类）", got)
	}
	if got := value("tags"); got != request.Tags {
		t.Errorf("tags = %q, want %q", got, request.Tags)
	}
	for _, forbidden := range []string{"autoTMM", "contentLayout", "paused", "root_folder"} {
		if form.Has(forbidden) {
			t.Errorf("%s 不该出现在加种参数里（用户偏好原样）", forbidden)
		}
	}
}

// TestAddTorrentSendsMultipartFile 钉住私种路径：multipart 里是 torrents= 文件。
func TestAddTorrentSendsMultipartFile(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"POST /api/v2/torrents/add": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("Ok."))
		},
	})
	c := mustLogin(t, fake)

	body := []byte("d8:announce...")
	request := addRequest{
		Torrent:  body,
		Filename: "0123456789abcdef0123456789abcdef01234567.torrent",
		SavePath: "/media/downloads/completed/av",
		Category: "mdc",
		Tags:     "ABP-123," + TagInProgress,
	}
	if err := c.addTorrent(context.Background(), request); err != nil {
		t.Fatalf("addTorrent: %v", err)
	}

	form := fake.requestsTo(http.MethodPost, "/api/v2/torrents/add")[0].Multipart
	if form == nil {
		t.Fatal("私种加种应当是 multipart/form-data")
	}
	files := form.File["torrents"]
	if len(files) != 1 {
		t.Fatalf("torrents 文件 %d 个, want 1", len(files))
	}
	if files[0].Filename != request.Filename {
		t.Errorf("filename = %q, want %q", files[0].Filename, request.Filename)
	}
	if contentType := files[0].Header.Get("Content-Type"); contentType != "application/x-bittorrent" {
		t.Errorf("种子 part 的 Content-Type = %q, want application/x-bittorrent（规格 §5.4）", contentType)
	}
	file, err := files[0].Open()
	if err != nil {
		t.Fatalf("打开上传的种子: %v", err)
	}
	defer file.Close()
	read, _ := io.ReadAll(file)
	if string(read) != string(body) {
		t.Errorf("上传内容 = %q, want 原始字节（不重编码）", read)
	}
	if len(form.Value["urls"]) != 0 {
		t.Error("私种路径不该带 urls=")
	}
}

// TestNumberFromTagsReadsTheNumber 钉住 P7-5：番号从 tag 取，项目 tag 不算番号。
func TestNumberFromTagsReadsTheNumber(t *testing.T) {
	props := Properties{Tags: "mdc:in-progress, ABP-123"}
	number, ok := props.Number()
	if !ok || number != "ABP-123" {
		t.Errorf("Number() = %q/%v, want ABP-123/true", number, ok)
	}

	if _, ok := NumberFromTags("mdc:in-progress, mdc:finished"); ok {
		t.Error("只有项目 tag 时不该编出一个番号")
	}
}

// TestCreateCategoryTreatsConflictAsSuccess 确认分类已存在（409）不算失败：幂等。
func TestCreateCategoryTreatsConflictAsSuccess(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"POST /api/v2/torrents/createCategory": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
		},
	})
	c := mustLogin(t, fake)

	if err := c.createCategory(context.Background(), "mdc"); err != nil {
		t.Fatalf("createCategory 已存在应当成功: %v", err)
	}
}

// TestPropertyRequestAfterSessionLossIsAuth 确认登录之后 403 归 auth（会话失效）。
func TestPropertyRequestAfterSessionLossIsAuth(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		},
	})
	c := mustLogin(t, fake)

	_, _, err := c.properties(context.Background(), "abc")
	if Classify(err) != settings.CodeAuth {
		t.Errorf("code = %q, want auth（%v）", Classify(err), err)
	}
}

// TestGarbagePreferencesIsProtocol 确认非 JSON 回执归 protocol。
func TestGarbagePreferencesIsProtocol(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/app/preferences": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>login page</html>"))
		},
	})
	c := mustLogin(t, fake)

	_, err := c.preferences(context.Background())
	if Classify(err) != settings.CodeProtocol {
		t.Errorf("code = %q, want protocol（%v）", Classify(err), err)
	}
}

// mustLogin 用假登录拿一个已登录的 client。
func mustLogin(t *testing.T, fake *fakeQbt) *client {
	t.Helper()

	c, err := newClient(fake.config(), nil)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if err := c.login(context.Background(), fake.config()); err != nil {
		t.Fatalf("login: %v", err)
	}
	return c
}

// inputOf 把 Config 包成 settings.TestInput（与真实装配同一份字段名）。
func inputOf(cfg Config) settings.TestInput {
	return settings.TestInput{Values: map[string]any{
		"url":              cfg.URL,
		"username":         cfg.Username,
		"password":         cfg.Password,
		"category":         cfg.Category,
		"download_root":    cfg.DownloadRoot,
		"webhook_base_url": cfg.WebhookBaseURL,
	}}
}
