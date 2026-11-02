package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/2017fighting/oh-my-mdc/internal/server"
)

// pinger 是 server 对数据库唯一的要求：一次 ping。测试用假实现，真实的一条在 internal/db。
type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func builtSPA() fstest.MapFS {
	return fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html><div id=root>壳页</div>")},
		"assets/app-1.js": {Data: []byte("console.log('壳')")},
	}
}

func newHandler(t *testing.T, opts server.Options) http.Handler {
	t.Helper()
	if opts.Assets == nil {
		opts.Assets = builtSPA()
	}
	h, err := server.New(opts)
	if err != nil {
		t.Fatalf("server.New() error = %v", err)
	}
	return h
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthzAnswersEvenWhenDatabaseIsDown(t *testing.T) {
	h := newHandler(t, server.Options{
		DB: pinger{err: errors.New("连接被拒")},
	})

	rec := get(t, h, "/healthz")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: liveness 不折叠数据库", rec.Code)
	}
}

func TestReadyzIsOKWhenDatabaseAnswers(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})

	rec := get(t, h, "/readyz")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body = %s", rec.Code, rec.Body)
	}
}

func TestReadyzReportsDatabaseUnavailableWithReason(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{err: errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")}})

	rec := get(t, h, "/readyz")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeBody(t, rec)
	if got := body["ok"]; got != false {
		t.Errorf("ok = %v, want false", got)
	}
	code, message := errorOf(t, body)
	if code != "db_unavailable" {
		t.Errorf("error.code = %q, want db_unavailable", code)
	}
	if !strings.Contains(message, "connection refused") {
		t.Errorf("error.message = %q, want it to carry the underlying reason", message)
	}
}

func TestReadyzReportsFailedMigrationUnavailable(t *testing.T) {
	h := newHandler(t, server.Options{
		DB:      pinger{},
		Unready: errors.New("数据库迁移失败: goose: 00002_bad.sql: syntax error at or near \"tabl\""),
	})

	rec := get(t, h, "/readyz")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: 迁移失败不 abort 启动，但 readyz 要回 503", rec.Code)
	}
	body := decodeBody(t, rec)
	code, message := errorOf(t, body)
	if code != "db_unavailable" {
		t.Errorf("error.code = %q, want db_unavailable", code)
	}
	if !strings.Contains(message, "00002_bad.sql") {
		t.Errorf("error.message = %q, want it to carry the migration failure", message)
	}
}

func TestVersionReportsInjectedValue(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}, Version: "v1.2.3"})

	rec := get(t, h, "/version")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body 不是 JSON: %v (%s)", err, rec.Body)
	}
	if got := body["version"]; got != "v1.2.3" {
		t.Errorf("version = %v, want v1.2.3", got)
	}
}

func TestVersionDefaultsToDev(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})

	rec := get(t, h, "/version")

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body 不是 JSON: %v", err)
	}
	if got := body["version"]; got != "dev" {
		t.Errorf("version = %v, want dev（未注入 ldflags 时的默认值）", got)
	}
}

func TestSPAFallbackServesShellForUnmatchedRoute(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})

	rec := get(t, h, "/tasks/ABC-123")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（未匹配 URL 导向 index.html）", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "壳页") {
		t.Errorf("body = %q, want the embedded index.html", rec.Body)
	}
}

func TestSPAFallbackAlsoCatchesDottedClientRoutes(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})

	rec := get(t, h, "/tasks/v1.2")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200：末段带点不代表是静态文件", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "壳页") {
		t.Errorf("body = %q, want the embedded index.html", rec.Body)
	}
}

func TestStaticAssetIsServedFromTheEmbeddedBuild(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})

	rec := get(t, h, "/assets/app-1.js")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "console.log") {
		t.Errorf("body = %q, want the asset bytes", rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc == "" {
		t.Error("Cache-Control 未设置：指纹资源应可长缓存")
	}
}

func TestMissingAssetDoesNotFallBackToShell(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})

	rec := get(t, h, "/assets/gone-2.js")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404（缺资源不许拿 index.html 冒充）", rec.Code)
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
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

// errorOf 取出规格 §8.4 的错误信封内容。
func errorOf(t *testing.T, body map[string]any) (code, message string) {
	t.Helper()
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("body 里没有 error 对象: %v", body)
	}
	code, _ = errObj["code"].(string)
	message, _ = errObj["message"].(string)
	return code, message
}
