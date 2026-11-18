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
	"github.com/2017fighting/oh-my-mdc/internal/subscription"
)

// 「从 App 收藏导入」的 HTTP 契约（#50，规格 §6.4/§8.4）：
//
//	GET /api/javdb/collected-actors → {actresses:[{id, name, videos_count, gender, subscribed}]}
//
// 两个接缝一起断言：上游那一半用 httptest 夹具（收藏名单要 token，真上游打不通），
// 订阅那一半用 fakeSubscriber（`subscribed` 是「订阅表里有没有这个人」，不是上游给的）。
// **没有搜索端点**——上游不提供（§13 实测），面板也不开搜索框。

// newCollectedHandler 装配一个「JavDB 集成指向 HTTP fake + 订阅接缝是假的」的 handler。
func newCollectedHandler(t *testing.T, routes map[string]http.HandlerFunc, subscriber server.Subscriber) http.Handler {
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

	if subscriber == nil {
		subscriber = &fakeSubscriber{}
	}
	integration := javdb.New(javdb.Options{Host: fake.URL})
	database := dbtest.Migrated(t)
	return newHandler(t, server.Options{
		DB:            pinger{},
		Settings:      settings.NewStore(database.Pool(), settings.WithTester("javdb", integration)),
		JavDB:         integration,
		Subscriptions: subscriber,
	})
}

// collectedActs 是收藏名单的一份回包夹具（144 位真实收藏的缩放版：两位女优 + 一位男优）。
const collectedActs = `{"actors":[
	{"id":"D2EdJ","type":0,"name":"花守夏歩","gender":0,"videos_count":179},
	{"id":"EvkJ","type":0,"name":"河北彩花","gender":0,"videos_count":232},
	{"id":"m1","type":0,"name":"森林原人","gender":1,"videos_count":900}
]}`

// TestCollectedActorsMarksSubscribedRows 钉住 §8.4 的行形状与 subscribed 的来源：
// 已订阅的那一位带 subscribed=true（导入面板据此置灰），其余原样是 false。
func TestCollectedActorsMarksSubscribedRows(t *testing.T) {
	subscriber := &fakeSubscriber{rows: []subscription.Row{
		{ID: 3, Actor: subscription.Actor{ID: "EvkJ", Name: "河北彩花"}, Enabled: true},
	}}
	h := newCollectedHandler(t, map[string]http.HandlerFunc{
		"/api/v1/users/collected_actors": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(javdbEnv(collectedActs)))
		},
	}, subscriber)
	save(t, h, "javdb", `"token": "tk"`)

	rec := send(t, h, http.MethodGet, "/api/javdb/collected-actors", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	rows, ok := body["actresses"].([]any)
	if !ok || len(rows) != 3 {
		t.Fatalf("actresses = %v, want 3 位", body["actresses"])
	}

	first := rows[0].(map[string]any)
	if first["id"] != "D2EdJ" || first["name"] != "花守夏歩" {
		t.Errorf("第一行 = %v", first)
	}
	if first["videos_count"] != float64(179) {
		t.Errorf("videos_count = %v, want 179", first["videos_count"])
	}
	if first["gender"] != float64(0) {
		t.Errorf("gender = %v, want 0（上游的性别码原样透出）", first["gender"])
	}
	if first["subscribed"] != false {
		t.Errorf("subscribed = %v, want false（订阅表里没有她）", first["subscribed"])
	}

	// 已订阅的那一位：置灰靠这一个布尔。
	subscribed := rows[1].(map[string]any)
	if subscribed["id"] != "EvkJ" || subscribed["subscribed"] != true {
		t.Errorf("第二行 = %v, want subscribed=true", subscribed)
	}

	// 男优照原样列出来（gender=1），面板自己决定怎么显示——藏起来用户永远不知道为什么少人。
	if male := rows[2].(map[string]any); male["gender"] != float64(1) {
		t.Errorf("男优那行 = %v, want gender=1", male)
	}
}

// TestCollectedActorsKeepsTheUpstreamOrder 确认不重排：上游按收藏顺序给，
// 面板照那个顺序列（我们没有任何「更合理」的顺序依据）。
func TestCollectedActorsKeepsTheUpstreamOrder(t *testing.T) {
	h := newCollectedHandler(t, map[string]http.HandlerFunc{
		"/api/v1/users/collected_actors": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(javdbEnv(`{"actors":[]}`)))
				return
			}
			_, _ = w.Write([]byte(javdbEnv(collectedActs)))
		},
	}, nil)
	save(t, h, "javdb", `"token": "tk"`)

	rows := jsonOf(t, send(t, h, http.MethodGet, "/api/javdb/collected-actors", ""))["actresses"].([]any)
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.(map[string]any)["id"].(string))
	}
	if strings.Join(got, ",") != "D2EdJ,EvkJ,m1" {
		t.Errorf("id 顺序 = %v, want 上游顺序", got)
	}
}

// TestCollectedActorsIsEmptyArrayNotNull 守住前端契约：空名单是 []，不是 null。
func TestCollectedActorsIsEmptyArrayNotNull(t *testing.T) {
	h := newCollectedHandler(t, map[string]http.HandlerFunc{
		"/api/v1/users/collected_actors": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(javdbEnv(`{"actors":[]}`)))
		},
	}, nil)
	save(t, h, "javdb", `"token": "tk"`)

	rec := send(t, h, http.MethodGet, "/api/javdb/collected-actors", "")
	rows, ok := jsonOf(t, rec)["actresses"].([]any)
	if !ok || len(rows) != 0 {
		t.Fatalf("actresses = %v, want []", jsonOf(t, rec)["actresses"])
	}
}

// TestCollectedActorsWithoutATokenSaysSo 是验收原文「未配 token 时导入入口不可用且写明原因」：
// 400 + 一句能读的话，不是一份静默的空名单。
//
// 这里走的是设置域既有的那句（ErrNotConfigured → 400 validation_error），与
// `/api/javdb/lists` 同一个口径：Token 是必填字段，PATCH 根本存不进空值，所以「没配 token」
// 在实际运行里只有这一种形状——不新造错误码，也不假装 503。
// 「节存了但 token 是空的」那一档由 appapi/javdb 两层的用例守住（本地报错、不发请求）。
func TestCollectedActorsWithoutATokenSaysSo(t *testing.T) {
	called := false
	h := newCollectedHandler(t, map[string]http.HandlerFunc{
		"/api/v1/users/collected_actors": func(w http.ResponseWriter, _ *http.Request) {
			called = true
		},
	}, nil)

	rec := send(t, h, http.MethodGet, "/api/javdb/collected-actors", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400（还没保存 token）: %s", rec.Code, rec.Body)
	}
	code, message := errorOf(t, jsonOf(t, rec))
	if code != "validation_error" {
		t.Errorf("error.code = %q, want validation_error（设置域既有的口径）", code)
	}
	if !strings.Contains(message, "保存") {
		t.Errorf("error.message = %q, want 说清先保存", message)
	}
	if called {
		t.Error("没 token 不该发到上游")
	}
}

// TestCollectedActorsReportsABadTokenAsAuth 坏 token（上游 JWTVerificationError）归 auth / 400：
// 面板据此说「去重新导出 token」，而不是「等一会儿再试」。
func TestCollectedActorsReportsABadTokenAsAuth(t *testing.T) {
	h := newCollectedHandler(t, map[string]http.HandlerFunc{
		"/api/v1/users/collected_actors": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":0,"action":"JWTVerificationError","message":"未登录","data":null}`))
		},
	}, nil)
	save(t, h, "javdb", `"token": "bad"`)

	rec := send(t, h, http.MethodGet, "/api/javdb/collected-actors", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	if code, _ := errorOf(t, jsonOf(t, rec)); code != "auth" {
		t.Errorf("error.code = %q, want auth", code)
	}
}

// TestCollectedActorsNeedsBothAssemblers：集成或订阅域缺一个都回 503，
// 而不是给一份 subscribed 全是 false 的名单（那会把已订的又导一遍）。
func TestCollectedActorsNeedsBothAssemblers(t *testing.T) {
	// 没有任何装配（JavDB 与 Subscriptions 都是 nil）。
	h := newSettingsHandler(t)
	rec := send(t, h, http.MethodGet, "/api/javdb/collected-actors", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503: %s", rec.Code, rec.Body)
	}

	// 有 JavDB 但没有订阅域：subscribed 算不出来，同样 503。
	database := dbtest.Migrated(t)
	integration := javdb.New(javdb.Options{Host: "http://127.0.0.1:1"})
	noSubs := newHandler(t, server.Options{
		DB:       pinger{},
		Settings: settings.NewStore(database.Pool()),
		JavDB:    integration,
	})
	rec = send(t, noSubs, http.MethodGet, "/api/javdb/collected-actors", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("没有订阅域时 status = %d, want 503: %s", rec.Code, rec.Body)
	}
}
