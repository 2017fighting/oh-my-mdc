package mteam

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// 测试缝：client 对 HTTP fake —— 锁全部请求形状（规格 §5.2、P5-1..4 的传输面）。
//
// 这些测试只对「发给 M-Team 的请求长什么样」与「回执怎么分类」下断言，不碰内部实现。

// recorded 是一次被 fake 收到的请求。
type recorded struct {
	Method      string
	Path        string
	ContentType string
	Headers     http.Header
	Body        string
}

// fakeMTeam 是一个记录请求的 M-Team fake。handlers 按 path 给回执。
type fakeMTeam struct {
	mu       sync.Mutex
	requests []recorded
	handlers map[string]http.HandlerFunc
}

func newFakeMTeam(t *testing.T) (*fakeMTeam, *httptest.Server) {
	t.Helper()

	f := &fakeMTeam{handlers: map[string]http.HandlerFunc{}}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{
			Method:      r.Method,
			Path:        r.URL.Path,
			ContentType: r.Header.Get("Content-Type"),
			Headers:     r.Header.Clone(),
			Body:        string(body),
		})
		f.mu.Unlock()

		handler, ok := f.handlers[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return f, server
}

// jsonEnvelope 回一个成功信封（data 原样塞进去）。
func jsonEnvelope(w http.ResponseWriter, data string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"code":"0","message":"SUCCESS","data":`+data+`}`)
}

func (f *fakeMTeam) recorded(t *testing.T) []recorded {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

// TestSearchRequestShape 钉住搜索的请求形状（规格 §5.2）：
// POST JSON、mode=adult、keyword=番号、visible=1，以及三个必带头。
func TestSearchRequestShape(t *testing.T) {
	f, server := newFakeMTeam(t)
	f.handlers["/api/torrent/search"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonEnvelope(w, `{"data":[{
			"id":"467487","name":"SSIS-001 4K","size":"130736",
			"labelsNew":["中字"],"createdDate":"2021-02-22 15:19:51","infoHash":null,
			"status":{"seeders":"122","leechers":"3","discount":"FREE"}}],"total":1}`)
	}

	client := &client{baseURL: server.URL, apiKey: "secret-key"}
	hits, err := client.search(context.Background(), "SSIS-001", searchPageSize)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	requests := f.recorded(t)
	if len(requests) != 1 {
		t.Fatalf("请求数 = %d, want 1", len(requests))
	}
	got := requests[0]
	if got.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", got.Method)
	}
	if got.Path != "/api/torrent/search" {
		t.Errorf("path = %s, want /api/torrent/search", got.Path)
	}
	if got.ContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json（search 要 JSON，form 会「參數錯誤」）", got.ContentType)
	}
	if got.Headers.Get("x-api-key") != "secret-key" {
		t.Errorf("x-api-key = %q, want secret-key", got.Headers.Get("x-api-key"))
	}
	ua := got.Headers.Get("User-Agent")
	if ua == "" || strings.Contains(strings.ToLower(ua), "curl") {
		t.Errorf("user-agent = %q, want 非空且不含 curl（否则 Cloudflare 302 到 google）", ua)
	}
	if got.Headers.Get("Origin") == "" {
		t.Error("缺 Origin 头（站点对 CORS 的校验）")
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(got.Body), &body); err != nil {
		t.Fatalf("请求体不是 JSON: %v（body=%s）", err, got.Body)
	}
	want := map[string]any{"mode": "adult", "keyword": "SSIS-001", "pageNumber": float64(1), "pageSize": float64(searchPageSize), "visible": float64(1)}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("请求体 %s = %v, want %v", key, body[key], value)
		}
	}

	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	hit := hits[0]
	if hit.ID != "467487" || hit.SizeBytes != 130736 || hit.Seeders != 122 || hit.Discount != "FREE" {
		t.Errorf("hit = %+v, want id/体积/做种/折扣都解自字符串线格式", hit)
	}
	if got := hit.Label(); got != LabelChinese {
		t.Errorf("label = %q, want %q（labelsNew 里有中字）", got, LabelChinese)
	}
}

// TestSearchClassifiesEnvelopeFailures 钉住 200+code 信封分类（P5-4）：
// 凭据类 → auth，其余 → protocol。**HTTP 状态码不参与判定**。
func TestSearchClassifiesEnvelopeFailures(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		location   string
		wantCode   settings.TestCode
		wantDetail string
	}{
		{
			name:       "key 无效",
			status:     http.StatusOK,
			body:       `{"code":"1","message":"key無效","data":null}`,
			wantCode:   settings.CodeAuth,
			wantDetail: "key無效",
		},
		{
			name:     "没带 key",
			status:   http.StatusOK,
			body:     `{"code":"401","message":"Full authentication is required to access this resource"}`,
			wantCode: settings.CodeAuth,
		},
		{
			name:       "认不出的 code 归 protocol",
			status:     http.StatusOK,
			body:       `{"code":"9","message":"什麼別的東西"}`,
			wantCode:   settings.CodeProtocol,
			wantDetail: "code=9",
		},
		{
			name:     "被 Cloudflare 按 UA 拦掉的 302",
			status:   http.StatusFound,
			location: "https://www.google.com/",
			wantCode: settings.CodeProtocol,
		},
		{
			name:     "HTTP 401",
			status:   http.StatusUnauthorized,
			wantCode: settings.CodeAuth,
		},
		{
			name:     "不是信封",
			status:   http.StatusOK,
			body:     "<html>cloudflare</html>",
			wantCode: settings.CodeProtocol,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f, server := newFakeMTeam(t)
			f.handlers["/api/torrent/search"] = func(w http.ResponseWriter, _ *http.Request) {
				if testCase.location != "" {
					w.Header().Set("Location", testCase.location)
				}
				w.WriteHeader(testCase.status)
				_, _ = io.WriteString(w, testCase.body)
			}

			client := &client{baseURL: server.URL, apiKey: "k"}
			_, err := client.search(context.Background(), "SSIS-001", searchPageSize)
			if err == nil {
				t.Fatal("期望报错，却成功了")
			}
			if got := Classify(err); got != testCase.wantCode {
				t.Errorf("Classify = %s, want %s（err=%v）", got, testCase.wantCode, err)
			}
			if testCase.wantDetail != "" && !strings.Contains(err.Error(), testCase.wantDetail) {
				t.Errorf("错误里应当提到 %q，实际是 %q", testCase.wantDetail, err)
			}
		})
	}
}

// TestGenDlTokenUsesFormEncoding 钉住 genDlToken 的 form 编码（规格 §5.2）：
// 给 JSON 会得到「參數錯誤」。
func TestGenDlTokenUsesFormEncoding(t *testing.T) {
	f, server := newFakeMTeam(t)
	f.handlers["/api/torrent/genDlToken"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonEnvelope(w, `"`+server.URL+`/link"`)
	}
	f.handlers["/link"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(torrentCanonical))
	}

	client := &client{baseURL: server.URL, apiKey: "k"}
	if _, err := client.download(context.Background(), "467487"); err != nil {
		t.Fatalf("download: %v", err)
	}

	requests := f.recorded(t)
	if len(requests) != 2 {
		t.Fatalf("请求数 = %d, want 2（genDlToken + 取种子）", len(requests))
	}
	token := requests[0]
	if token.ContentType != "application/x-www-form-urlencoded" {
		t.Errorf("content-type = %q, want form（genDlToken 不接受 JSON）", token.ContentType)
	}
	if token.Body != "id=467487" {
		t.Errorf("body = %q, want id=467487", token.Body)
	}

	// 取种子那一跳也要带 api key（规格 §5.2 的实测：GET 链接带 x-api-key）。
	if requests[1].Headers.Get("x-api-key") != "k" {
		t.Errorf("取种子请求的 x-api-key = %q, want k", requests[1].Headers.Get("x-api-key"))
	}
}

// TestDownloadFollowsRedirectToCDN 钉住两种重定向策略的分界（规格 §5.2）：
// 下载链接跟 302 到 CDN，API 端点不跟。
func TestDownloadFollowsRedirectToCDN(t *testing.T) {
	f := &fakeMTeam{handlers: map[string]http.HandlerFunc{}}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{
			Method: r.Method, Path: r.URL.Path,
			ContentType: r.Header.Get("Content-Type"),
			Headers:     r.Header.Clone(), Body: string(body),
		})
		f.mu.Unlock()

		switch r.URL.Path {
		case "/api/torrent/genDlToken":
			jsonEnvelope(w, `"`+server.URL+`/link"`)
		case "/link":
			http.Redirect(w, r, "/cdn/torrent", http.StatusFound)
		case "/cdn/torrent":
			_, _ = w.Write([]byte(torrentCanonical))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := &client{baseURL: server.URL, apiKey: "k"}
	raw, err := client.download(context.Background(), "467487")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if string(raw) != torrentCanonical {
		t.Errorf("取回的种子 != 夹具（长度 %d, want %d）", len(raw), len(torrentCanonical))
	}

	paths := make([]string, 0, 3)
	for _, request := range f.recorded(t) {
		paths = append(paths, request.Path)
	}
	if strings.Join(paths, ",") != "/api/torrent/genDlToken,/link,/cdn/torrent" {
		t.Errorf("请求路径 = %v, want 跟到 CDN 的第三跳", paths)
	}
}

// TestAPIEndpointDoesNotFollowRedirect 钉住 API 端点不跟 302：
// 那里的 302 是 Cloudflare 拦人，跟过去只会拿到 google 的页面。
func TestAPIEndpointDoesNotFollowRedirect(t *testing.T) {
	f, server := newFakeMTeam(t)
	f.handlers["/api/torrent/genDlToken"] = func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "https://www.google.com/", http.StatusFound)
	}
	f.handlers["/api/torrent/search"] = func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "https://www.google.com/", http.StatusFound)
	}

	client := &client{baseURL: server.URL, apiKey: "k"}
	_, err := client.download(context.Background(), "467487")
	if err == nil {
		t.Fatal("API 端点回 302 时应当报错")
	}
	if got := Classify(err); got != settings.CodeProtocol {
		t.Errorf("Classify = %s, want protocol（拦人不是凭据问题）", got)
	}
	if !strings.Contains(err.Error(), "Cloudflare") {
		t.Errorf("错误里应当点明 Cloudflare 拦截，实际是 %q", err)
	}
	if requests := f.recorded(t); len(requests) != 1 {
		t.Errorf("请求数 = %d, want 1（不跟重定向）", len(requests))
	}
}

// TestDownloadRejectsJSONInsteadOfTorrent 钉住过期链接的判据：
// 链接过期时 Content-Type 仍然是 application/x-bittorrent，只有体变成 JSON。
func TestDownloadRejectsJSONInsteadOfTorrent(t *testing.T) {
	f, server := newFakeMTeam(t)
	f.handlers["/api/torrent/genDlToken"] = func(w http.ResponseWriter, r *http.Request) {
		jsonEnvelope(w, `"`+server.URL+`/link"`)
	}
	f.handlers["/link"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = io.WriteString(w, `{"code":"1","message":"連結不可用！ 超出有效期","data":null}`)
	}

	client := &client{baseURL: server.URL, apiKey: "k"}
	_, err := client.download(context.Background(), "467487")
	if err == nil {
		t.Fatal("链接过期时应当报错")
	}
	if got := Classify(err); got != settings.CodeProtocol {
		t.Errorf("Classify = %s, want protocol（重试无用）", got)
	}
	if !strings.Contains(err.Error(), "过期") {
		t.Errorf("错误里应当点明链接过期，实际是 %q", err)
	}
}

// TestSearchParsesNoHits 确认空结果是合法答案（不是错误）。
func TestSearchParsesNoHits(t *testing.T) {
	f, server := newFakeMTeam(t)
	f.handlers["/api/torrent/search"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonEnvelope(w, `{"data":[],"total":0}`)
	}

	client := &client{baseURL: server.URL, apiKey: "k"}
	hits, err := client.search(context.Background(), "MIDA-821", searchPageSize)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("hits = %d, want 0", len(hits))
	}
}

// TestDownloadDoesNotLeakAPIKeyToCDN 确认跨主机的那一跳不携带存取令牌。
//
// Go 只自动剥 Authorization / Cookie 这类标准头；x-api-key 是自定义头，
// 不显式去掉就会被送到 CDN 域名上。
func TestDownloadDoesNotLeakAPIKeyToCDN(t *testing.T) {
	var cdnHeaders []http.Header
	var mu sync.Mutex

	var cdn *httptest.Server
	cdn = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		cdnHeaders = append(cdnHeaders, r.Header.Clone())
		mu.Unlock()

		if r.URL.Path == "/download" {
			http.Redirect(w, r, "/cdn/torrent", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(torrentCanonical))
	}))
	t.Cleanup(cdn.Close)

	f, server := newFakeMTeam(t)
	f.handlers["/api/torrent/genDlToken"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonEnvelope(w, `"`+cdn.URL+`/download"`)
	}

	client := &client{baseURL: server.URL, apiKey: "secret-key"}
	if _, err := client.download(context.Background(), "467487"); err != nil {
		t.Fatalf("download: %v", err)
	}

	tokenRequests := f.recorded(t)
	if len(tokenRequests) != 1 || tokenRequests[0].Headers.Get("x-api-key") != "secret-key" {
		t.Fatalf("genDlToken 请求 = %+v, want 带 x-api-key", tokenRequests)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(cdnHeaders) != 2 {
		t.Fatalf("CDN 收到的请求 = %d, want 2（302 + 文件）", len(cdnHeaders))
	}
	for i, header := range cdnHeaders {
		if key := header.Get("x-api-key"); key != "" {
			t.Errorf("CDN 第 %d 跳收到了 x-api-key = %q, want 空（跟踪链接不该拿到存取令牌）", i+1, key)
		}
	}
}
