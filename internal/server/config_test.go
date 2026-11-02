package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/server"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// 设置域的测试都在「HTTP 契约 + 真 Postgres」这个接缝上：
// 不碰内部函数，只按规格 §8.4 的请求/响应说话。

func newSettingsHandler(t *testing.T, options ...settings.Option) http.Handler {
	t.Helper()

	database := dbtest.Migrated(t)
	return newHandler(t, server.Options{
		DB:       pinger{},
		Settings: settings.NewStore(database.Pool(), options...),
	})
}

func send(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request)
	return rec
}

func jsonOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON (body %s)", ct, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body 不是 JSON: %v (%s)", err, rec.Body)
	}
	return body
}

func sectionsOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	body := jsonOf(t, rec)
	sections, ok := body["sections"].(map[string]any)
	if !ok {
		t.Fatalf("GET /api/config 没有 sections: %v", body)
	}
	return sections
}

func dataOf(t *testing.T, section map[string]any) map[string]any {
	t.Helper()

	data, ok := section["data"].(map[string]any)
	if !ok {
		t.Fatalf("section 没有 data: %v", section)
	}
	return data
}

// currentRevision 读一节当前的 revision 指纹（真实客户端也是先 GET 再 PATCH）。
func currentRevision(t *testing.T, h http.Handler, section string) string {
	t.Helper()

	rec := send(t, h, http.MethodGet, "/api/config/"+section, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/config/%s = %d, want 200: %s", section, rec.Code, rec.Body)
	}
	revision, ok := jsonOf(t, rec)["revision"].(string)
	if !ok || revision == "" {
		t.Fatalf("GET /api/config/%s 没给 revision: %s", section, rec.Body)
	}
	return revision
}

// save 带着当前 revision 发一次 PATCH，要求 200，并返回这一节的回执。
func save(t *testing.T, h http.Handler, section, fields string) map[string]any {
	t.Helper()

	body := `{"revision": "` + currentRevision(t, h, section) + `"`
	if fields != "" {
		body += ", " + fields
	}
	body += "}"

	rec := send(t, h, http.MethodPatch, "/api/config/"+section, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH %s = %d, want 200: %s", section, rec.Code, rec.Body)
	}
	return jsonOf(t, rec)
}

// initialized 读一次门禁判据（规格 §4.4）。
func initialized(t *testing.T, h http.Handler) bool {
	t.Helper()

	value, _ := jsonOf(t, send(t, h, http.MethodGet, "/api/config", ""))["initialized"].(bool)
	return value
}

// configureMainline 把主线三件套配齐（JavDB + 清单登记 + qBittorrent + Jellyfin）。
//
// media_root 用一次性的真实目录：门禁要看它在盘上可见（P4-2，本票把 media_root
// 搬进了 Jellyfin 集成表）。
func configureMainline(t *testing.T, h http.Handler) {
	t.Helper()

	save(t, h, "javdb", `"token": "eyJhbGciOiJIUzI1NiJ9.phone-token"`)
	save(t, h, "list_registry", `"list_id": "k4EVE4", "purpose": "want_watch", "name": "想看清单"`)
	save(t, h, "qbittorrent", `"url": "http://qbittorrent.media.svc:8080", "download_root": "/media/downloads/completed/av"`)
	save(t, h, "jellyfin", `"url": "http://jellyfin.media.svc:8096", "api_key": "0123456789abcdef", "media_root": "`+t.TempDir()+`"`)
}

func TestConfigStartsUninitializedWithEverySectionPresent(t *testing.T) {
	h := newSettingsHandler(t)

	rec := send(t, h, http.MethodGet, "/api/config", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)
	if body["initialized"] != false {
		t.Errorf("initialized = %v, want false（全新部署三件套不齐）", body["initialized"])
	}
	if body["media_root_visible"] != false {
		t.Errorf("media_root_visible = %v, want false（还没有 media_root）", body["media_root_visible"])
	}

	sections := sectionsOf(t, rec)
	for _, name := range []string{
		"javdb", "list_registry", "qbittorrent", "jellyfin", "mteam", "ai_translation", "telegram", "app",
	} {
		section, ok := sections[name].(map[string]any)
		if !ok {
			t.Fatalf("sections 里没有 %s: %v", name, sections)
		}
		if section["configured"] != false && name != "app" {
			t.Errorf("%s.configured = %v, want false", name, section["configured"])
		}
	}

	javdb := sections["javdb"].(map[string]any)
	token := dataOf(t, javdb)["token"].(map[string]any)
	if token["configured"] != false || token["masked"] != "" {
		t.Errorf("javdb.token = %v, want {configured:false, masked:\"\"}", token)
	}

	app := dataOf(t, sections["app"].(map[string]any))
	if app["poll_interval"] != "15m0s" {
		t.Errorf("app.poll_interval = %v, want 15m0s（迁移里的默认值）", app["poll_interval"])
	}
	if app["max_attempts"] != float64(3) {
		t.Errorf("app.max_attempts = %v, want 3", app["max_attempts"])
	}
	if app["paused"] != false {
		t.Errorf("app.paused = %v, want false", app["paused"])
	}

	// media_root 属于 Jellyfin 那一节（本票从 app_config 搬过来）。
	if _, ok := app["media_root"]; ok {
		t.Error("app 节仍有 media_root：媒体库目录已搬到 Jellyfin 表")
	}
	jellyfin := dataOf(t, sections["jellyfin"].(map[string]any))
	if jellyfin["media_root"] != "" {
		t.Errorf("jellyfin.media_root = %v, want \"\"", jellyfin["media_root"])
	}
}

func TestSavingTheMainlineTrioOpensTheGate(t *testing.T) {
	h := newSettingsHandler(t)

	save(t, h, "javdb", `"token": "eyJhbGciOiJIUzI1NiJ9.phone-token"`)
	if got := initialized(t, h); got {
		t.Error("initialized = true：只有 token 还不能开循环")
	}

	save(t, h, "list_registry", `"list_id": "k4EVE4", "purpose": "want_watch", "name": "想看清单"`)
	save(t, h, "qbittorrent", `"url": "http://qbittorrent.media.svc:8080"`)

	if got := initialized(t, h); got {
		t.Error("initialized = true：Jellyfin 还没配")
	}

	// P4-2：路径不在盘上就不算「Jellyfin 可写」——三个容器没挂同一路径，
	// 硬链接与刷新都会失败，这时不能把面板打开。
	save(t, h, "jellyfin", `"url": "http://jellyfin.media.svc:8096", "api_key": "0123456789abcdef", "media_root": "`+t.TempDir()+"/not-mounted"+`"`)
	if got := initialized(t, h); got {
		t.Error("initialized = true：media_root 在这台机器上看不见")
	}
	if got := jsonOf(t, send(t, h, http.MethodGet, "/api/config", ""))["media_root_visible"]; got != false {
		t.Errorf("media_root_visible = %v, want false（向导要靠它说清是哪一半没到）", got)
	}

	// 末尾空格不该让门禁与 Test/Refresh 看到两个值（都按去空白后的路径判）。
	save(t, h, "jellyfin", `"media_root": "`+t.TempDir()+` "`)
	if got := initialized(t, h); !got {
		t.Error("initialized = false：三件套齐了，循环才该启动")
	}

	if got := jsonOf(t, send(t, h, http.MethodGet, "/api/config", ""))["media_root_visible"]; got != true {
		t.Errorf("media_root_visible = %v, want true", got)
	}

	overview := jsonOf(t, send(t, h, http.MethodGet, "/api/overview", ""))
	if overview["initialized"] != true {
		t.Errorf("overview.initialized = %v, want true（跟 /api/config 同一个判据）", overview["initialized"])
	}
}

func TestSensitiveValuesAreMaskedAndNeverEchoed(t *testing.T) {
	const token = "eyJhbGciOiJIUzI1NiJ9.phone-token"
	h := newSettingsHandler(t)

	save(t, h, "javdb", `"token": "`+token+`"`)

	// 六张集成表的凭据列各存一份，逐个确认读接口里一个字都不回显。
	secrets := []struct{ where, value string }{
		{"javdb.token", token},
		{"qbittorrent.password", "qbittorrent-password-value"},
		{"jellyfin.api_key", "jellyfin-api-key-value"},
		{"mteam.api_key", "mteam-api-key-value"},
		{"ai_translation.api_key", "openai-compatible-key-value"},
		{"telegram.bot_token", "123456:telegram-bot-token-value"},
	}
	save(t, h, "qbittorrent", `"url": "http://qbt.media.svc:8080", "password": "`+secrets[1].value+`"`)
	save(t, h, "jellyfin", `"url": "http://jellyfin.media.svc:8096", "api_key": "`+secrets[2].value+`", "media_root": "`+t.TempDir()+`"`)
	save(t, h, "mteam", `"api_key": "`+secrets[3].value+`", "enabled": true`)
	save(t, h, "ai_translation", `"base_url": "https://api.example.com/v1", "api_key": "`+secrets[4].value+`", "model": "gpt-4o-mini"`)
	save(t, h, "telegram", `"bot_token": "`+secrets[5].value+`", "chat_id": "12345"`)

	rec := send(t, h, http.MethodGet, "/api/config", "")
	for _, secret := range secrets {
		if strings.Contains(rec.Body.String(), secret.value) {
			t.Fatalf("GET /api/config 回显了 %s 原文", secret.where)
		}
	}

	javdb := sectionsOf(t, rec)["javdb"].(map[string]any)
	tokenField := dataOf(t, javdb)["token"].(map[string]any)
	if tokenField["configured"] != true {
		t.Errorf("configured = %v, want true", tokenField["configured"])
	}
	if want := "ey***en"; tokenField["masked"] != want {
		t.Errorf("masked = %v, want %q", tokenField["masked"], want)
	}

	// 单节读同样只给掩码。
	single := send(t, h, http.MethodGet, "/api/config/javdb", "")
	if strings.Contains(single.Body.String(), token) {
		t.Fatalf("GET /api/config/javdb 回显了 token 原文: %s", single.Body)
	}
}

func TestPatchKeepsOmittedFieldsAndBlankSensitiveOnes(t *testing.T) {
	const token = "eyJhbGciOiJIUzI1NiJ9.phone-token"
	h := newSettingsHandler(t)

	saved := save(t, h, "javdb", `"token": "`+token+`"`)
	revision := saved["revision"].(string)

	// 空串＝不改：掩码框没被碰过时前端就把空串发回来。
	saved = save(t, h, "javdb", `"token": ""`)
	tokenField := dataOf(t, saved)["token"].(map[string]any)
	if tokenField["configured"] != true {
		t.Errorf("token.configured = %v, want true：空串不该清掉凭据", tokenField["configured"])
	}
	if saved["revision"] != revision {
		t.Errorf("revision 变了 (%v → %v)：空串请求不该算一次修改", revision, saved["revision"])
	}

	// 省略＝不改：只改 category，url 原样。
	save(t, h, "qbittorrent", `"url": "http://qbt.media.svc:8080"`)
	saved = save(t, h, "qbittorrent", `"category": "oh-my-mdc"`)
	data := dataOf(t, saved)
	if data["url"] != "http://qbt.media.svc:8080" {
		t.Errorf("url = %v, want 原值：省略的字段不该被清空", data["url"])
	}
	if data["category"] != "oh-my-mdc" {
		t.Errorf("category = %v, want oh-my-mdc", data["category"])
	}
}

func TestRevisionConflictIsRejectedAndLeavesTheWinnerIntact(t *testing.T) {
	h := newSettingsHandler(t)

	first := save(t, h, "javdb", `"token": "first-token-value"`)
	staleRevision := first["revision"].(string)

	winner := save(t, h, "javdb", `"token": "winner-token-value"`)
	if winner["revision"] == staleRevision {
		t.Fatalf("revision 没变：保存没有真的写入")
	}

	rec := send(t, h, http.MethodPatch, "/api/config/javdb",
		`{"revision": "`+staleRevision+`", "token": "loser-token-value"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（旧 revision 的保存要被拦下）: %s", rec.Code, rec.Body)
	}
	code, _ := errorOf(t, jsonOf(t, rec))
	if code != "revision_conflict" {
		t.Errorf("error.code = %q, want revision_conflict", code)
	}

	// 赢家的值还在：冲突的请求不许落库。
	current := jsonOf(t, send(t, h, http.MethodGet, "/api/config/javdb", ""))
	if current["revision"] != winner["revision"] {
		t.Errorf("revision = %v, want %v（冲突请求不该改内容）", current["revision"], winner["revision"])
	}
}

func TestPatchWithoutRevisionIsAConflict(t *testing.T) {
	h := newSettingsHandler(t)
	save(t, h, "javdb", `"token": "first-token-value"`)

	rec := send(t, h, http.MethodPatch, "/api/config/javdb", `{"token": "unprotected-value"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（缺 revision 就是关掉了并发保护）: %s", rec.Code, rec.Body)
	}
	code, _ := errorOf(t, jsonOf(t, rec))
	if code != "revision_conflict" {
		t.Errorf("error.code = %q, want revision_conflict", code)
	}
}

func TestPatchValidationErrors(t *testing.T) {
	h := newSettingsHandler(t)

	cases := []struct {
		name    string
		section string
		fields  string
	}{
		{"未知字段", "javdb", `"token": "abc", "nope": 1`},
		{"类型不对", "jellyfin", `"url": 42`},
		{"缺必填", "qbittorrent", `"category": "mdc"`},
		{"URL 形状", "jellyfin", `"url": "jellyfin.media.svc:8096", "api_key": "k"`},
		{"整数范围", "app", `"max_attempts": 0`},
		{"时长范围", "app", `"poll_interval": "1s"`},
		{"时长写法", "app", `"reconcile_interval": " souvent"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"revision": "` + currentRevision(t, h, tc.section) + `", ` + tc.fields + `}`
			rec := send(t, h, http.MethodPatch, "/api/config/"+tc.section, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
			code, _ := errorOf(t, jsonOf(t, rec))
			if code != "validation_error" {
				t.Errorf("error.code = %q, want validation_error", code)
			}
		})
	}
}

func TestDeletingAMainlineSectionClosesTheGate(t *testing.T) {
	h := newSettingsHandler(t)
	configureMainline(t, h)

	if got := jsonOf(t, send(t, h, http.MethodGet, "/api/config", ""))["initialized"]; got != true {
		t.Fatalf("initialized = %v, want true（前置条件没搭起来）", got)
	}

	rec := send(t, h, http.MethodDelete, "/api/config/jellyfin", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if body := jsonOf(t, rec); body["configured"] != false {
		t.Errorf("configured = %v, want false（删行＝解除配置）", body["configured"])
	}
	if got := initialized(t, h); got {
		t.Error("initialized = true：Jellyfin 被删，系统该回向导")
	}

	// 解除配置后行真的没了：再读是「未配置」而不是空字符串的「已配置」。
	sections := sectionsOf(t, send(t, h, http.MethodGet, "/api/config", ""))
	if sections["jellyfin"].(map[string]any)["configured"] != false {
		t.Error("jellyfin.configured 仍是 true")
	}
}

func TestRegistryTakesOneListPerPurpose(t *testing.T) {
	h := newSettingsHandler(t)

	save(t, h, "list_registry", `"list_id": "k4EVE4", "purpose": "want_watch", "name": "想看清单"`)

	rec := send(t, h, http.MethodPatch, "/api/config/list_registry",
		`{"revision": "`+currentRevision(t, h, "list_registry")+`", "list_id": "e8r1OM", "purpose": "want_watch", "name": "另一张"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400（一个用途只许一张清单）: %s", rec.Code, rec.Body)
	}

	if rec := send(t, h, http.MethodDelete, "/api/config/list_registry", ""); rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if rec := send(t, h, http.MethodPatch, "/api/config/list_registry",
		`{"revision": "`+currentRevision(t, h, "list_registry")+`", "list_id": "e8r1OM", "purpose": "want_watch", "name": "另一张"}`); rec.Code != http.StatusOK {
		t.Fatalf("解除后重新登记 status = %d, want 200: %s", rec.Code, rec.Body)
	}

	registry := jsonOf(t, send(t, h, http.MethodGet, "/api/config/list_registry", ""))
	entries := dataOf(t, registry)["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want 1 张", entries)
	}
	if entry := entries[0].(map[string]any); entry["list_id"] != "e8r1OM" {
		t.Errorf("list_id = %v, want e8r1OM", entry["list_id"])
	}
}

func TestUnknownSectionIsNotFound(t *testing.T) {
	h := newSettingsHandler(t)

	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/config/nope", ""},
		{http.MethodPatch, "/api/config/nope", `{}`},
		{http.MethodDelete, "/api/config/nope", ""},
		{http.MethodPost, "/api/config/nope/test", ""},
	} {
		rec := send(t, h, request.method, request.path, request.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404: %s", request.method, request.path, rec.Code, rec.Body)
		}
		code, _ := errorOf(t, jsonOf(t, rec))
		if code != "unknown_section" {
			t.Errorf("%s %s error.code = %q, want unknown_section", request.method, request.path, code)
		}
	}
}

func TestTestEndpointIsAPlaceholderUntilIntegrationsLand(t *testing.T) {
	h := newSettingsHandler(t)
	save(t, h, "javdb", `"token": "eyJhbGciOiJIUzI1NiJ9.phone-token"`)

	rec := send(t, h, http.MethodPost, "/api/config/javdb/test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)
	if body["ok"] != false {
		t.Errorf("ok = %v, want false（占位不是成功）", body["ok"])
	}
	if body["code"] != "not_implemented" {
		t.Errorf("code = %v, want not_implemented", body["code"])
	}
	if detail, _ := body["detail"].(string); !strings.Contains(detail, "还没实现") {
		t.Errorf("detail = %q, want 说清楚这一节还没接线", detail)
	}
	if _, ok := body["duration_ms"]; !ok {
		t.Error("缺 duration_ms：规格 §8.4 规定每次测试都回它")
	}

	// 占位不算一次真实测试，所以账本保持空白（向导不会因此显示红叉）。
	javdb := jsonOf(t, send(t, h, http.MethodGet, "/api/config/javdb", ""))
	if javdb["last_test"] != nil {
		t.Errorf("last_test = %v, want null：占位结果不写 last_test_*", javdb["last_test"])
	}
}

func TestTestEndpointRecordsRealVerdicts(t *testing.T) {
	h := newSettingsHandler(t, settings.WithTester("javdb", settings.TesterFunc(
		func(_ context.Context, _ settings.Section, input settings.TestInput) settings.TestResult {
			if input.Text("token") == "" {
				return settings.TestResult{Code: settings.CodeBusiness, Detail: "测试实现拿不到凭据原文"}
			}
			return settings.TestResult{Code: settings.CodeAuth, Detail: "token 被踢下线"}
		})))

	save(t, h, "javdb", `"token": "eyJhbGciOiJIUzI1NiJ9.phone-token"`)

	rec := send(t, h, http.MethodPost, "/api/config/javdb/test", "")
	body := jsonOf(t, rec)
	if body["ok"] != false || body["code"] != "auth" {
		t.Fatalf("body = %v, want {ok:false, code:auth}", body)
	}

	javdb := jsonOf(t, send(t, h, http.MethodGet, "/api/config/javdb", ""))
	lastTest, ok := javdb["last_test"].(map[string]any)
	if !ok {
		t.Fatalf("last_test = %v, want 一条账本", javdb["last_test"])
	}
	if lastTest["ok"] != false {
		t.Errorf("last_test.ok = %v, want false", lastTest["ok"])
	}
	if errorText, _ := lastTest["error"].(string); !strings.Contains(errorText, "auth") {
		t.Errorf("last_test.error = %q, want 带上错误码", errorText)
	}
}

func TestTestEndpointTimesOutAfterTheLimit(t *testing.T) {
	h := newSettingsHandler(t,
		settings.WithTestTimeout(20*time.Millisecond),
		settings.WithTester("javdb", settings.TesterFunc(
			func(ctx context.Context, _ settings.Section, _ settings.TestInput) settings.TestResult {
				<-ctx.Done() // 模拟一个挂住的上游
				return settings.TestResult{OK: true}
			})))

	save(t, h, "javdb", `"token": "eyJhbGciOiJIUzI1NiJ9.phone-token"`)

	body := jsonOf(t, send(t, h, http.MethodPost, "/api/config/javdb/test", ""))
	if body["ok"] != false || body["code"] != "timeout" {
		t.Errorf("body = %v, want {ok:false, code:timeout}", body)
	}
}

func TestOptionalIntegrationCanBeConfiguredAndSwitchedOff(t *testing.T) {
	h := newSettingsHandler(t)

	saved := save(t, h, "mteam", `"api_key": "mteam-api-key-value", "enabled": true`)
	if saved["enabled"] != true {
		t.Fatalf("enabled = %v, want true", saved["enabled"])
	}

	revision := saved["revision"].(string)
	saved = save(t, h, "mteam", `"revision": "`+revision+`", "enabled": false`)
	if saved["enabled"] != false {
		t.Errorf("enabled = %v, want false：可选集成配了也能关着", saved["enabled"])
	}
	if _, ok := dataOf(t, saved)["enabled"]; ok {
		t.Error("data.enabled 与节级 enabled 重复：规格 §8.4 把 enabled 放在节一级")
	}
	if saved["configured"] != true {
		t.Errorf("configured = %v, want true：关掉不等于解除配置", saved["configured"])
	}
}

func TestNullIsNotAValue(t *testing.T) {
	h := newSettingsHandler(t)

	save(t, h, "qbittorrent", `"url": "http://qbt.media.svc:8080", "category": "mdc"`)

	rec := send(t, h, http.MethodPatch, "/api/config/qbittorrent", `{"category": null}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400（null 不是「不改」）: %s", rec.Code, rec.Body)
	}
}

func TestTestEndpointNeedsAConfiguredIntegration(t *testing.T) {
	h := newSettingsHandler(t)

	rec := send(t, h, http.MethodPost, "/api/config/javdb/test", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400（还没保存过就测试）: %s", rec.Code, rec.Body)
	}

	rec = send(t, h, http.MethodPost, "/api/config/app/test", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("app 测试 status = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestAppSectionIsSingleRowAndNotDeletable(t *testing.T) {
	h := newSettingsHandler(t)

	saved := save(t, h, "app", `"poll_interval": "20m", "max_attempts": 5, "paused": true`)
	data := dataOf(t, saved)
	if data["poll_interval"] != "20m0s" {
		t.Errorf("poll_interval = %v, want 20m0s", data["poll_interval"])
	}
	if data["max_attempts"] != float64(5) {
		t.Errorf("max_attempts = %v, want 5", data["max_attempts"])
	}

	overview := jsonOf(t, send(t, h, http.MethodGet, "/api/overview", ""))
	if overview["paused"] != true {
		t.Errorf("overview.paused = %v, want true", overview["paused"])
	}

	rec := send(t, h, http.MethodDelete, "/api/config/app", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("DELETE app = %d, want 400: %s", rec.Code, rec.Body)
	}
	code, _ := errorOf(t, jsonOf(t, rec))
	if code != "validation_error" {
		t.Errorf("error.code = %q, want validation_error", code)
	}
}

func TestOverviewIsASkeletonUntilLoopsAndTasksExist(t *testing.T) {
	h := newSettingsHandler(t)

	body := jsonOf(t, send(t, h, http.MethodGet, "/api/overview", ""))

	if body["initialized"] != false || body["paused"] != false {
		t.Errorf("overview 门禁位 = %v/%v, want false/false", body["initialized"], body["paused"])
	}
	counts, ok := body["counts"].(map[string]any)
	if !ok {
		t.Fatalf("counts = %v, want 对象", body["counts"])
	}
	for _, key := range []string{"downloading", "processing", "finished", "needs_attention"} {
		if counts[key] != float64(0) {
			t.Errorf("counts.%s = %v, want 0", key, counts[key])
		}
	}
	loops, ok := body["loops"].(map[string]any)
	if !ok {
		t.Fatalf("loops = %v, want 对象", body["loops"])
	}
	for _, key := range []string{"poll", "reconcile", "sidefx"} {
		loop, ok := loops[key].(map[string]any)
		if !ok {
			t.Fatalf("loops.%s = %v, want 对象", key, loops[key])
		}
		if loop["last_run_at"] != nil || loop["last_ok"] != nil {
			t.Errorf("loops.%s = %v, want 还没跑过的空位", key, loop)
		}
	}
}

func TestConfigEndpointsAreUnavailableWithoutADatabase(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})

	for _, path := range []string{"/api/config", "/api/overview"} {
		rec := send(t, h, http.MethodGet, path, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503: %s", path, rec.Code, rec.Body)
		}
		code, _ := errorOf(t, jsonOf(t, rec))
		if code != "db_unavailable" {
			t.Errorf("GET %s error.code = %q, want db_unavailable", path, code)
		}
	}
}
