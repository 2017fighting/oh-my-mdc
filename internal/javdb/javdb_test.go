package javdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"unicode/utf8"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// fakeJavDB 起一个 JavDB App API 的 HTTP fake，按路径分派。
func fakeJavDB(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func env(data string) string {
	return `{"success":1,"action":null,"message":null,"data":` + data + `}`
}

func tokenInput(token string, lists ...settings.ListEntry) settings.TestInput {
	return settings.TestInput{Values: map[string]any{"token": token}, Lists: lists}
}

// TestTestSucceedsOnUserDomainRead 确认：token 可用即绿，未登记清单时也绿。
func TestTestSucceedsOnUserDomainRead(t *testing.T) {
	srv := fakeJavDB(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api/v1/lists/simple": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(env(`{"lists":[{"id":"k4EVE4","name":"default"}]}`)))
		},
	})
	integration := New(Options{Host: srv.URL})

	result := integration.Test(context.Background(), settings.Section{Name: "javdb"}, tokenInput("tk"))
	if !result.OK {
		t.Fatalf("result = %+v, want 绿（token 可用）", result)
	}
}

// TestTestReadsRegisteredList 确认：登记了想看清单时，测试会真的去读它。
func TestTestReadsRegisteredList(t *testing.T) {
	var gotFilter string
	srv := fakeJavDB(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api/v1/lists/simple": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(env(`{"lists":[{"id":"k4EVE4","name":"想看清单"}]}`)))
		},
		"/api/v1/movies/tags": func(w http.ResponseWriter, r *http.Request) {
			gotFilter = r.URL.Query().Get("filter_by")
			_, _ = w.Write([]byte(env(`{"movies":[{"id":"YwrO3B","number":"ATID-637"}]}`)))
		},
	})
	integration := New(Options{Host: srv.URL})

	result := integration.Test(context.Background(), settings.Section{Name: "javdb"},
		tokenInput("tk", settings.ListEntry{ListID: "k4EVE4", Purpose: "want_watch", Name: "想看清单"}))
	if !result.OK {
		t.Fatalf("result = %+v, want 绿", result)
	}
	if gotFilter != "0:l:k4EVE4" {
		t.Errorf("filter_by = %q, want 0:l:k4EVE4（清单读的实体字母）", gotFilter)
	}
}

// TestTestClassifiesBadTokenAsAuth 钉住 P1-2：坏 token → auth，且与网络错可区分。
func TestTestClassifiesBadTokenAsAuth(t *testing.T) {
	srv := fakeJavDB(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api/v1/lists/simple": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":0,"action":"JWTVerificationError","message":"token 无效","data":null}`))
		},
	})
	integration := New(Options{Host: srv.URL})

	result := integration.Test(context.Background(), settings.Section{Name: "javdb"}, tokenInput("bad"))
	if result.OK {
		t.Fatal("坏 token 不该绿")
	}
	if result.Code != settings.CodeAuth {
		t.Errorf("code = %q, want auth", result.Code)
	}
}

// TestTestClassifiesBusinessError 确认业务错（success:0 + action:null）归 business。
func TestTestClassifiesBusinessError(t *testing.T) {
	srv := fakeJavDB(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api/v1/lists/simple": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":0,"action":null,"message":"名稱 過短","data":null}`))
		},
	})
	integration := New(Options{Host: srv.URL})

	result := integration.Test(context.Background(), settings.Section{Name: "javdb"}, tokenInput("tk"))
	if result.Code != settings.CodeBusiness {
		t.Errorf("code = %q, want business", result.Code)
	}
}

// TestTestClassifiesSignatureErrorAsProtocol 确认 InvalidSignature 归 protocol（改代码，不是换 token）。
func TestTestClassifiesSignatureErrorAsProtocol(t *testing.T) {
	srv := fakeJavDB(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api/v1/lists/simple": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"success":0,"action":"InvalidSignature","message":"無效的簽名","data":null}`))
		},
	})
	integration := New(Options{Host: srv.URL})

	result := integration.Test(context.Background(), settings.Section{Name: "javdb"}, tokenInput("tk"))
	if result.Code != settings.CodeProtocol {
		t.Errorf("code = %q, want protocol", result.Code)
	}
}

// TestTestClassifiesNetworkError 确认连不上 → network（不是 auth）。
func TestTestClassifiesNetworkError(t *testing.T) {
	// 指向一个已关闭的 server，保证连接被拒。
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	integration := New(Options{Host: url})
	result := integration.Test(context.Background(), settings.Section{Name: "javdb"}, tokenInput("tk"))
	if result.OK {
		t.Fatal("连不上不该绿")
	}
	if result.Code != settings.CodeNetwork {
		t.Errorf("code = %q, want network", result.Code)
	}
}

// TestDefaultWantWatchListNameSatisfiesServerRule 钉住向导默认名合规：
// 服务端要求 4–30 字符，「想看」二字不合规（规格 §5.1 清单名陷阱）。
// 这条测试在默认名被人改动而不合规时会红。
func TestDefaultWantWatchListNameSatisfiesServerRule(t *testing.T) {
	n := utf8.RuneCountInString(DefaultWantWatchListName)
	if n < appapi.MinListNameLen || n > appapi.MaxListNameLen {
		t.Errorf("默认名 %q 是 %d 字，不合服务端 %d–%d 规则", DefaultWantWatchListName, n, appapi.MinListNameLen, appapi.MaxListNameLen)
	}
}

// TestTestClassifiesNonEnvelopeAsProtocol 钉住：裸 HTTP 5xx（如 Cloudflare HTML）
// 归 protocol 而不是 network —— 它不是网络抖了，是服务端行为与预期不一致。
func TestTestClassifiesNonEnvelopeAsProtocol(t *testing.T) {
	srv := fakeJavDB(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api/v1/lists/simple": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<html>Internal Server Error</html>`))
		},
	})
	integration := New(Options{Host: srv.URL})

	result := integration.Test(context.Background(), settings.Section{Name: "javdb"}, tokenInput("tk"))
	if result.Code != settings.CodeProtocol {
		t.Errorf("code = %q, want protocol（裸 5xx 不是网络错）", result.Code)
	}
}

// TestListsWithoutTokenDoesNotCallUpstream 确认没 token 时本地报错、不发请求：
// 发了也必然被拒（JWTVerificationError），而把「空清单」当成「你没建过清单」是最难排查的错。
func TestListsWithoutTokenDoesNotCallUpstream(t *testing.T) {
	called := false
	srv := fakeJavDB(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api/v1/lists/simple": func(w http.ResponseWriter, _ *http.Request) {
			called = true
		},
	})
	integration := New(Options{Host: srv.URL})

	if _, err := integration.Lists(context.Background(), ""); err == nil {
		t.Fatal("没 token 应当报错")
	}
	if called {
		t.Error("没 token 不该发请求")
	}
}

// TestCreateListReturnsNewID 确认代建返回 data.id。
func TestCreateListReturnsNewID(t *testing.T) {
	srv := fakeJavDB(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api/v1/lists": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			_, _ = w.Write([]byte(env(`{"id":"e8r1OM","name":"想看清单"}`)))
		},
	})
	integration := New(Options{Host: srv.URL})

	id, err := integration.CreateList(context.Background(), "tk", DefaultWantWatchListName, DefaultWantWatchListDescription)
	if err != nil {
		t.Fatalf("CreateList: %v", err)
	}
	if id != "e8r1OM" {
		t.Errorf("id = %q, want e8r1OM", id)
	}
}
