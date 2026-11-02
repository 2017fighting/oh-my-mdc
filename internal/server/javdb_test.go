package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/javdb"
	"github.com/2017fighting/oh-my-mdc/internal/server"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// newJavDBHandler 装配一个把 JavDB 集成指向 HTTP fake 的 handler。
func newJavDBHandler(t *testing.T, routes map[string]http.HandlerFunc) http.Handler {
	t.Helper()

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(fake.Close)

	integration := javdb.New(javdb.Options{Host: fake.URL})
	database := dbtest.Migrated(t)
	return newHandler(t, server.Options{
		DB:       pinger{},
		Settings: settings.NewStore(database.Pool(), settings.WithTester("javdb", integration)),
		JavDB:    integration,
	})
}

func javdbEnv(data string) string {
	return `{"success":1,"action":null,"message":null,"data":` + data + `}`
}

// TestJavDBListsProbesUserLists 确认向导探测按 id + 名字列出用户自建的清单。
func TestJavDBListsProbesUserLists(t *testing.T) {
	h := newJavDBHandler(t, map[string]http.HandlerFunc{
		"/api/v1/lists/simple": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(javdbEnv(`{"lists":[
				{"id":"k4EVE4","name":"遥控跳弹","is_default":false,"movies_count":1},
				{"id":"R9r77","name":"default","is_default":true,"movies_count":6}
			]}`)))
		},
	})
	save(t, h, "javdb", `"token": "tk"`)

	rec := send(t, h, http.MethodGet, "/api/javdb/lists", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	lists, ok := body["lists"].([]any)
	if !ok || len(lists) != 2 {
		t.Fatalf("lists = %v, want 2 张", body["lists"])
	}
	first := lists[0].(map[string]any)
	if first["id"] != "k4EVE4" || first["name"] != "遥控跳弹" {
		t.Errorf("第一张清单 = %v", first)
	}
	if lists[1].(map[string]any)["is_default"] != true {
		t.Errorf("is_default 没透出: %v", lists[1])
	}
}

// TestJavDBListsWithoutTokenIsRejected 确认没保存 token 时 400，不白打上游。
func TestJavDBListsWithoutTokenIsRejected(t *testing.T) {
	h := newJavDBHandler(t, nil)

	rec := send(t, h, http.MethodGet, "/api/javdb/lists", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400（先保存 token）: %s", rec.Code, rec.Body)
	}
}

// TestJavDBCreateListReturnsID 确认代建返回 data.id，且能被登记进 list_registry。
func TestJavDBCreateListReturnsID(t *testing.T) {
	h := newJavDBHandler(t, map[string]http.HandlerFunc{
		"/api/v1/lists": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			_, _ = w.Write([]byte(javdbEnv(`{"id":"e8r1OM","name":"想看清单"}`)))
		},
	})
	save(t, h, "javdb", `"token": "tk"`)

	rec := send(t, h, http.MethodPost, "/api/javdb/lists", `{"name": "想看清单", "description": "oh-my-mdc 自动创建"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)
	if body["id"] != "e8r1OM" {
		t.Errorf("id = %v, want e8r1OM（data.id）", body["id"])
	}

	// 拿回来的 id 直接登记（P2-2 的闭环）。
	save(t, h, "list_registry", `"list_id": "e8r1OM", "purpose": "want_watch", "name": "想看清单"`)
	registry := jsonOf(t, send(t, h, http.MethodGet, "/api/config/list_registry", ""))
	entries := dataOf(t, registry)["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["list_id"] != "e8r1OM" {
		t.Fatalf("登记没落库: %v", entries)
	}
}

// TestJavDBCreateListPassesThroughDuplicateNameError 确认同名拒原样透出（P2-2）。
func TestJavDBCreateListPassesThroughDuplicateNameError(t *testing.T) {
	h := newJavDBHandler(t, map[string]http.HandlerFunc{
		"/api/v1/lists": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":0,"action":null,"message":"名稱 您已創建了同名清單","data":null}`))
		},
	})
	save(t, h, "javdb", `"token": "tk"`)

	rec := send(t, h, http.MethodPost, "/api/javdb/lists", `{"name": "想看清单"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400（业务错）: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)
	code, message := errorOf(t, body)
	if code != "business" {
		t.Errorf("error.code = %q, want business", code)
	}
	if !strings.Contains(message, "同名清單") {
		t.Errorf("error.message = %q, want 原样透出上游文案", message)
	}
}

// TestJavDBCreateListRejectsShortNameAsBusiness 钉住：名字短于 4 字在本地被拦下，
// 归 business / 400（而不是 502 network）。这条锁住本地校验的错误码契约。
func TestJavDBCreateListRejectsShortNameAsBusiness(t *testing.T) {
	called := false
	h := newJavDBHandler(t, map[string]http.HandlerFunc{
		"/api/v1/lists": func(w http.ResponseWriter, _ *http.Request) {
			called = true
			_, _ = w.Write([]byte(javdbEnv(`{"id":"x"}`)))
		},
	})
	save(t, h, "javdb", `"token": "tk"`)

	rec := send(t, h, http.MethodPost, "/api/javdb/lists", `{"name": "想看"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400（本地校验）: %s", rec.Code, rec.Body)
	}
	code, _ := errorOf(t, jsonOf(t, rec))
	if code != "business" {
		t.Errorf("error.code = %q, want business", code)
	}
	if called {
		t.Error("名字过短不该发到上游")
	}
}

// TestJavDBTokenTestGoesGreenThroughTheStack 端到端：保存 token → 测试读取 → last_test_ok。
//
// 这是 P1-1 的验收：通过写 last_test_ok=true。
func TestJavDBTokenTestGoesGreenThroughTheStack(t *testing.T) {
	h := newJavDBHandler(t, map[string]http.HandlerFunc{
		"/api/v1/lists/simple": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(javdbEnv(`{"lists":[{"id":"k4EVE4","name":"想看清单"}]}`)))
		},
	})
	save(t, h, "javdb", `"token": "tk"`)

	rec := send(t, h, http.MethodPost, "/api/config/javdb/test", "")
	body := jsonOf(t, rec)
	if body["ok"] != true {
		t.Fatalf("body = %v, want {ok:true}", body)
	}

	javdbSection := jsonOf(t, send(t, h, http.MethodGet, "/api/config/javdb", ""))
	lastTest, ok := javdbSection["last_test"].(map[string]any)
	if !ok || lastTest["ok"] != true {
		t.Fatalf("last_test = %v, want ok:true", javdbSection["last_test"])
	}
}

// TestJavDBTokenTestReportsAuthForBadToken 端到端：坏 token → auth chip（P1-2）。
func TestJavDBTokenTestReportsAuthForBadToken(t *testing.T) {
	h := newJavDBHandler(t, map[string]http.HandlerFunc{
		"/api/v1/lists/simple": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":0,"action":"JWTVerificationError","message":"token 无效","data":null}`))
		},
	})
	save(t, h, "javdb", `"token": "bad"`)

	body := jsonOf(t, send(t, h, http.MethodPost, "/api/config/javdb/test", ""))
	if body["ok"] != false || body["code"] != "auth" {
		t.Fatalf("body = %v, want {ok:false, code:auth}", body)
	}
}

// TestJavDBEndpointsAreUnavailableWithoutTheIntegration 确认没装配时 503。
func TestJavDBEndpointsAreUnavailableWithoutTheIntegration(t *testing.T) {
	h := newSettingsHandler(t)

	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/javdb/lists", ""},
		{http.MethodPost, "/api/javdb/lists", `{"name": "x"}`},
	} {
		rec := send(t, h, request.method, request.path, request.body)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503: %s", request.method, request.path, rec.Code, rec.Body)
		}
	}
}

// TestJavDBTestReadsRegisteredListThroughTheStack 确认清单步的「测试读取」走 JavDB 那一节时，
// 也会真的去读登记清单（规格 §8.1）。
func TestJavDBTestReadsRegisteredListThroughTheStack(t *testing.T) {
	var readList bool
	h := newJavDBHandler(t, map[string]http.HandlerFunc{
		"/api/v1/lists/simple": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(javdbEnv(`{"lists":[{"id":"k4EVE4","name":"想看清单"}]}`)))
		},
		"/api/v1/movies/tags": func(w http.ResponseWriter, _ *http.Request) {
			readList = true
			_, _ = w.Write([]byte(javdbEnv(`{"movies":[{"id":"YwrO3B","number":"ATID-637"}]}`)))
		},
	})
	save(t, h, "javdb", `"token": "tk"`)
	save(t, h, "list_registry", `"list_id": "k4EVE4", "purpose": "want_watch", "name": "想看清单"`)

	body := jsonOf(t, send(t, h, http.MethodPost, "/api/config/javdb/test", ""))
	if body["ok"] != true {
		t.Fatalf("body = %v, want ok:true", body)
	}
	if !readList {
		t.Error("登记了想看清单，测试读取却没去读它")
	}
}

// TestRegistryRenameDoesNotChangePollingIdentity 是 P2-4 的回归测试：
// 判定永远按 id，改名字只是更新快照，轮询读到的 filter_by 不变。
func TestRegistryRenameDoesNotChangePollingIdentity(t *testing.T) {
	var filters []string
	h := newJavDBHandler(t, map[string]http.HandlerFunc{
		"/api/v1/lists/simple": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(javdbEnv(`{"lists":[{"id":"k4EVE4","name":"改名后的清单"}]}`)))
		},
		"/api/v1/movies/tags": func(w http.ResponseWriter, r *http.Request) {
			filters = append(filters, r.URL.Query().Get("filter_by"))
			_, _ = w.Write([]byte(javdbEnv(`{"movies":[]}`)))
		},
	})
	save(t, h, "javdb", `"token": "tk"`)
	save(t, h, "list_registry", `"list_id": "k4EVE4", "purpose": "want_watch", "name": "想看清单"`)
	send(t, h, http.MethodPost, "/api/config/javdb/test", "")

	// 清单在 JavDB 那边改名了：只更新快照里的 name，id 不变。
	save(t, h, "list_registry", `"list_id": "k4EVE4", "purpose": "want_watch", "name": "改名后的清单"`)
	send(t, h, http.MethodPost, "/api/config/javdb/test", "")

	if len(filters) != 2 {
		t.Fatalf("读了 %d 次清单，want 2", len(filters))
	}
	if filters[0] != "0:l:k4EVE4" || filters[1] != "0:l:k4EVE4" {
		t.Errorf("filter_by 序列 = %v：改名不该影响按 id 的轮询", filters)
	}
}
