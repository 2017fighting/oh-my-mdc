package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/server"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// fakeQbt 是一个有状态的 qbt WebUI fake：setPreferences 会真的改掉它自己的现值，
// 好让「重复保存幂等」在 HTTP 面上被验一遍（而不是只在纯函数上）。
type fakeQbt struct {
	mu      sync.Mutex
	enabled bool
	program string
	writes  int
	login   func(w http.ResponseWriter)
}

func (f *fakeQbt) server(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/auth/login":
			if f.login != nil {
				f.login(w)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/app/version":
			_, _ = w.Write([]byte("v4.6.5\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/app/preferences":
			f.mu.Lock()
			prefs := map[string]any{
				"autorun_enabled":                  f.enabled,
				"autorun_program":                  f.program,
				"autorun_on_torrent_added_enabled": false,
				"autorun_on_torrent_added_program": "untouched",
			}
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(prefs)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/app/setPreferences":
			_ = r.ParseForm()
			var values map[string]any
			if err := json.Unmarshal([]byte(r.PostFormValue("json")), &values); err != nil {
				t.Errorf("setPreferences json 不是 JSON: %v", err)
			}
			f.mu.Lock()
			f.writes++
			if program, ok := values["autorun_program"].(string); ok {
				f.program = program
			}
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeQbt) state() (program string, writes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.program, f.writes
}

// newQbtHandler 装配一个把 qBittorrent 集成指向 fake 的 handler。
func newQbtHandler(t *testing.T, fake *fakeQbt) http.Handler {
	t.Helper()

	integration := qbittorrent.New(qbittorrent.Options{})
	database := dbtest.Migrated(t)
	h := newHandler(t, server.Options{
		DB:       pinger{},
		Settings: settings.NewStore(database.Pool(), settings.WithTester("qbittorrent", integration)),
		Qbt:      integration,
	})

	srv := fake.server(t)
	save(t, h, "qbittorrent", `"url": "`+srv.URL+`", "download_root": "/media/downloads/completed/av", "webhook_base_url": "http://oh-my-mdc.oh-my-mdc.svc:8080"`)
	return h
}

// TestQbtAutorunPlanShowsFullBeforeAndAfter 钉住 P3-2/P3-4：预览给出完整前后对比，
// 旧命令的双引号被归一成单引号，moviepilot 原命令还在。
func TestQbtAutorunPlanShowsFullBeforeAndAfter(t *testing.T) {
	fake := &fakeQbt{
		enabled: true,
		program: `curl "http://moviepilot.media.svc:3000/api/v1/transfer/now?token=abc123"`,
	}
	h := newQbtHandler(t, fake)

	rec := send(t, h, http.MethodGet, "/api/qbittorrent/autorun", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)

	before, _ := body["before"].(string)
	if before != fake.program {
		t.Errorf("before = %q, want 现值原样 %q", before, fake.program)
	}
	after, _ := body["after"].(string)
	want := `/bin/sh -c "curl 'http://moviepilot.media.svc:3000/api/v1/transfer/now?token=abc123' ; ` +
		`curl -fsS -m 10 'http://oh-my-mdc.oh-my-mdc.svc:8080/api/hooks/qbt?ih=%I'"`
	if after != want {
		t.Errorf("after = %s\nwant %s", after, want)
	}
	if body["changed"] != true || body["skip"] != false || body["applied"] != false {
		t.Errorf("changed/skip/applied = %v/%v/%v, want true/false/false", body["changed"], body["skip"], body["applied"])
	}
	if program, writes := fake.state(); writes != 0 || program != fake.program {
		t.Errorf("预览不该写上游：writes = %d, program = %q", writes, program)
	}
}

// TestQbtAutorunApplyWritesOnceAndIsIdempotent 钉住 P3-3：确认后落一次，
// 重复保存时现值已含本项目 URL → 跳过不改。
func TestQbtAutorunApplyWritesOnceAndIsIdempotent(t *testing.T) {
	fake := &fakeQbt{enabled: true, program: `curl "http://moviepilot/transfer/now"`}
	h := newQbtHandler(t, fake)

	first := jsonOf(t, send(t, h, http.MethodPost, "/api/qbittorrent/autorun", ""))
	if first["applied"] != true || first["skip"] != false {
		t.Fatalf("第一次 applied/skip = %v/%v, want true/false", first["applied"], first["skip"])
	}
	program, writes := fake.state()
	if writes != 1 {
		t.Fatalf("第一次写入 %d 次, want 1", writes)
	}
	if program != first["after"] {
		t.Errorf("qbt 里的 program = %q, want 预览过的 after", program)
	}

	second := jsonOf(t, send(t, h, http.MethodPost, "/api/qbittorrent/autorun", ""))
	if second["skip"] != true || second["applied"] != false {
		t.Errorf("第二次 skip/applied = %v/%v, want true/false（幂等）", second["skip"], second["applied"])
	}
	if _, writes := fake.state(); writes != 1 {
		t.Errorf("第二次又写了上游：writes = %d, want 1", writes)
	}
}

// TestQbtAutorunWarnsWhenAutorunDisabled 确认开关关着时计划照给、但带上警告。
func TestQbtAutorunWarnsWhenAutorunDisabled(t *testing.T) {
	fake := &fakeQbt{enabled: false, program: ""}
	h := newQbtHandler(t, fake)

	body := jsonOf(t, send(t, h, http.MethodGet, "/api/qbittorrent/autorun", ""))
	if body["enabled"] != false {
		t.Errorf("enabled = %v, want false", body["enabled"])
	}
	warning, _ := body["warning"].(string)
	if !strings.Contains(warning, "关闭") {
		t.Errorf("warning = %q, want 明说 autorun 关闭", warning)
	}
}

// TestQbtAutorunBeforeSavingIsRejected 确认没保存本步时 400，不白打上游。
func TestQbtAutorunBeforeSavingIsRejected(t *testing.T) {
	integration := qbittorrent.New(qbittorrent.Options{})
	database := dbtest.Migrated(t)
	h := newHandler(t, server.Options{
		DB:       pinger{},
		Settings: settings.NewStore(database.Pool()),
		Qbt:      integration,
	})

	rec := send(t, h, http.MethodGet, "/api/qbittorrent/autorun", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	if code, _ := errorOf(t, jsonOf(t, rec)); code != "validation_error" {
		t.Errorf("code = %q, want validation_error", code)
	}
}

// TestQbtAutorunMapsAuthErrorTo400 确认坏凭据是 400（要人改），不是 502。
func TestQbtAutorunMapsAuthErrorTo400(t *testing.T) {
	fake := &fakeQbt{login: func(w http.ResponseWriter) {
		_, _ = w.Write([]byte("Fails."))
	}}
	h := newQbtHandler(t, fake)

	rec := send(t, h, http.MethodPost, "/api/qbittorrent/autorun", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	if code, _ := errorOf(t, jsonOf(t, rec)); code != "auth" {
		t.Errorf("code = %q, want auth", code)
	}
}

// TestQbtAutorunMapsNetworkErrorTo502 确认连不上是 502（等一会儿重试）。
func TestQbtAutorunMapsNetworkErrorTo502(t *testing.T) {
	integration := qbittorrent.New(qbittorrent.Options{})
	database := dbtest.Migrated(t)
	h := newHandler(t, server.Options{
		DB:       pinger{},
		Settings: settings.NewStore(database.Pool()),
		Qbt:      integration,
	})

	// 指向一个已关闭的端口：保存能过（URL 校验不等同于连通），测试/改写才会打上游。
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	save(t, h, "qbittorrent", `"url": "`+deadURL+`", "webhook_base_url": "http://oh-my-mdc.svc:8080"`)

	rec := send(t, h, http.MethodGet, "/api/qbittorrent/autorun", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", rec.Code, rec.Body)
	}
	if code, _ := errorOf(t, jsonOf(t, rec)); code != "network" {
		t.Errorf("code = %q, want network", code)
	}
}

// TestQbtEndpointsAreUnavailableWithoutTheIntegration 确认没装配集成时回 503，
// 而不是打个空指针或假装成功。
func TestQbtEndpointsAreUnavailableWithoutTheIntegration(t *testing.T) {
	database := dbtest.Migrated(t)
	h := newHandler(t, server.Options{
		DB:       pinger{},
		Settings: settings.NewStore(database.Pool()),
	})

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := send(t, h, method, "/api/qbittorrent/autorun", "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503", method, rec.Code)
		}
		if code, _ := errorOf(t, jsonOf(t, rec)); code != "db_unavailable" {
			t.Errorf("%s code = %q, want db_unavailable", method, code)
		}
	}
}

// TestQbtTestConnectionReadsVersionAndAutorun 确认「测试连接」走的是集成门面：
// 绿 + detail 带上版本与 autorun 现值。
func TestQbtTestConnectionReadsVersionAndAutorun(t *testing.T) {
	fake := &fakeQbt{enabled: true, program: `curl "http://moviepilot"`}
	h := newQbtHandler(t, fake)

	rec := send(t, h, http.MethodPost, "/api/config/qbittorrent/test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)
	if body["ok"] != true {
		t.Fatalf("ok = %v, want true: %v", body["ok"], body)
	}
	detail, _ := body["detail"].(string)
	if !strings.Contains(detail, "v4.6.5") || !strings.Contains(detail, "autorun 现值") {
		t.Errorf("detail = %q, want 版本 + autorun 现值", detail)
	}
	if strings.Contains(detail, "moviepilot") {
		t.Errorf("detail = %q, 不该把 autorun 里的凭据抄进 chip（完整对比在面板里）", detail)
	}
}
