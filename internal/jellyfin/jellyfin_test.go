package jellyfin

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

// fakeRequest 是 fake 收到的一次请求：断言直接读它，不碰客户端内部。
type fakeRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// fakeJellyfin 起一个按 "METHOD path" 分派的 Jellyfin HTTP fake，并留住每个请求。
type fakeJellyfin struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	requests []fakeRequest
}

func newFakeJellyfin(t *testing.T, routes map[string]http.HandlerFunc) *fakeJellyfin {
	t.Helper()

	fake := &fakeJellyfin{t: t}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读请求体: %v", err)
			return
		}
		fake.mu.Lock()
		fake.requests = append(fake.requests, fakeRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Header: r.Header.Clone(),
			Body:   raw,
		})
		fake.mu.Unlock()

		handler, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			// "METHOD *" 是兜底路由：真实 Jellyfin 对不认识的路径也回 204，
			// refresh 的 204 陷阱用例靠它把这个事实锁进 fake（默认仍回 404）。
			handler, ok = routes[r.Method+" *"]
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

// recorded 返回到目前为止收到的请求（拷贝，测试断言时不会再改）。
func (f *fakeJellyfin) recorded() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRequest(nil), f.requests...)
}

// inputOf 装一份 jellyfin 节的设置原文（media_root 已随本票搬进这一节）。
func inputOf(url, apiKey, mediaRoot string) settings.TestInput {
	return settings.TestInput{Values: map[string]any{
		"url":        url,
		"api_key":    apiKey,
		"media_root": mediaRoot,
	}}
}

func systemInfo(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ServerName": "jellyfin",
			"Version":    version,
			"Id":         "0123456789abcdef",
		})
	}
}

// TestTestPassesOnAuthAndVisibleMediaRoot 钉住 P4-1 的两关都过才绿：
// System/Info 鉴权（X-Emby-Token）+ media_root 本机可见。
func TestTestPassesOnAuthAndVisibleMediaRoot(t *testing.T) {
	root := t.TempDir()
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		"GET /System/Info": systemInfo("10.10.7"),
	})
	integration := New(Options{})

	result := integration.Test(context.Background(), settings.Section{Name: "jellyfin"},
		inputOf(fake.server.URL, "0123456789abcdef", root))
	if !result.OK {
		t.Fatalf("result = %+v, want 绿（鉴权过 + 目录在盘）", result)
	}
	if result.Code != "" {
		t.Errorf("code = %q, want 空（绿没有分类码）", result.Code)
	}

	requests := fake.recorded()
	if len(requests) != 1 {
		t.Fatalf("请求数 = %d, want 1（只读 System/Info，测试不改上游）: %+v", len(requests), requests)
	}
	request := requests[0]
	if request.Method != http.MethodGet || request.Path != "/System/Info" {
		t.Errorf("请求 = %s %s, want GET /System/Info", request.Method, request.Path)
	}
	if got := request.Header.Get("X-Emby-Token"); got != "0123456789abcdef" {
		t.Errorf("X-Emby-Token = %q, want 设置里的 api_key", got)
	}
}

// TestTestClassifiesRejectedKeyAsAuth 钉住 §8.4 的分类：401/403 是凭据问题，不是协议问题。
func TestTestClassifiesRejectedKeyAsAuth(t *testing.T) {
	root := t.TempDir()
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		"GET /System/Info": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		},
	})
	integration := New(Options{})

	result := integration.Test(context.Background(), settings.Section{Name: "jellyfin"},
		inputOf(fake.server.URL, "wrong-key", root))
	if result.OK {
		t.Fatal("被拒的 key 不该绿")
	}
	if result.Code != settings.CodeAuth {
		t.Errorf("code = %q, want auth", result.Code)
	}
}

// TestTestClassifiesUnexpectedSystemInfoAsProtocol 确认 2xx 但不是 System/Info 的形态 → protocol。
func TestTestClassifiesUnexpectedSystemInfoAsProtocol(t *testing.T) {
	root := t.TempDir()
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		"GET /System/Info": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>not jellyfin</html>"))
		},
	})
	integration := New(Options{})

	result := integration.Test(context.Background(), settings.Section{Name: "jellyfin"},
		inputOf(fake.server.URL, "key", root))
	if result.Code != settings.CodeProtocol {
		t.Errorf("code = %q, want protocol", result.Code)
	}
}

// TestTestFailsWhenMediaRootIsNotOnDisk 钉住 P4-2：目录不可见 → 明确错误（哪条路径 + 去查挂载），
// 即使鉴权已经过了也不给绿（两关都过才绿）。
func TestTestFailsWhenMediaRootIsNotOnDisk(t *testing.T) {
	missing := t.TempDir() + "/not-mounted"
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		"GET /System/Info": systemInfo("10.10.7"),
	})
	integration := New(Options{})

	result := integration.Test(context.Background(), settings.Section{Name: "jellyfin"},
		inputOf(fake.server.URL, "key", missing))
	if result.OK {
		t.Fatal("媒体库目录不可见时不该绿")
	}
	if result.Code != settings.CodeBusiness {
		t.Errorf("code = %q, want business（要人查挂载）", result.Code)
	}
	if !strings.Contains(result.Detail, missing) {
		t.Errorf("detail = %q, want 指出是哪条路径不可见", result.Detail)
	}
	if !strings.Contains(result.Detail, "挂") {
		t.Errorf("detail = %q, want 指向「三个容器同路径挂载」这条原因", result.Detail)
	}
}

// TestTestFailsWithoutMediaRootBeforeTouchingUpstream 确认缺 media_root 是本地校验，
// 不发请求：向导里还没填目录时不必打扰 Jellyfin。
func TestTestFailsWithoutMediaRootBeforeTouchingUpstream(t *testing.T) {
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		"GET /System/Info": systemInfo("10.10.7"),
	})
	integration := New(Options{})

	result := integration.Test(context.Background(), settings.Section{Name: "jellyfin"},
		inputOf(fake.server.URL, "key", ""))
	if result.Code != settings.CodeBusiness {
		t.Errorf("code = %q, want business", result.Code)
	}
	if requests := fake.recorded(); len(requests) != 0 {
		t.Errorf("请求数 = %d, want 0（缺项时不该打上游）: %+v", len(requests), requests)
	}
}

// TestTestRequiresBaseURLAndKey 确认地址与 key 的本地校验同样归 business。
func TestTestRequiresBaseURLAndKey(t *testing.T) {
	integration := New(Options{})
	root := t.TempDir()

	for name, input := range map[string]settings.TestInput{
		"缺地址":  inputOf("", "key", root),
		"缺key": inputOf("http://jellyfin.media.svc:8096", "", root),
	} {
		result := integration.Test(context.Background(), settings.Section{Name: "jellyfin"}, input)
		if result.Code != settings.CodeBusiness {
			t.Errorf("%s: code = %q, want business", name, result.Code)
		}
	}
}
