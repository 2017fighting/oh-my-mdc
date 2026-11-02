package mteam

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// 测试缝：Integration 对 HTTP fake —— 向导的「测试检索」与取种主链。

// TestTestUsesPageSizeOne 钉住「测试连接」= pageSize=1 的单行搜索（规格 §8.4）。
func TestTestUsesPageSizeOne(t *testing.T) {
	f, server := newFakeMTeam(t)
	f.handlers["/api/torrent/search"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonEnvelope(w, `{"data":[{"id":"1","name":"SSIS-001 测试行","size":"1","labelsNew":[],"status":{"seeders":"1"}}],"total":1}`)
	}

	integration := New(Options{Host: server.URL})
	result := integration.Test(context.Background(), settings.Section{},
		settings.TestInput{Values: map[string]any{"api_key": "secret"}})

	if !result.OK {
		t.Fatalf("Test = %+v, want OK", result)
	}
	if !strings.Contains(result.Detail, "SSIS-001") {
		t.Errorf("detail = %q, want 带上首条种子名（证明真搜到了东西）", result.Detail)
	}

	requests := f.recorded(t)
	if len(requests) != 1 {
		t.Fatalf("请求数 = %d, want 1（测试只搜一次）", len(requests))
	}
	if !strings.Contains(requests[0].Body, `"pageSize":1`) {
		t.Errorf("请求体 = %s, want pageSize=1（配额成本最小）", requests[0].Body)
	}
	if requests[0].Headers.Get("x-api-key") != "secret" {
		t.Errorf("x-api-key = %q, want secret", requests[0].Headers.Get("x-api-key"))
	}
}

// TestTestClassifiesAuthFailure 钉住「换 key」与「查代码」的分界：
// 200 + code=401 归 auth（P5-4）。
func TestTestClassifiesAuthFailure(t *testing.T) {
	f, server := newFakeMTeam(t)
	f.handlers["/api/torrent/search"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":"401","message":"Full authentication is required to access this resource"}`)
	}

	integration := New(Options{Host: server.URL})
	result := integration.Test(context.Background(), settings.Section{},
		settings.TestInput{Values: map[string]any{"api_key": "bad"}})

	if result.OK {
		t.Fatal("坏 key 时 Test 应当不 OK")
	}
	if result.Code != settings.CodeAuth {
		t.Errorf("code = %s, want auth", result.Code)
	}
}

// TestTestWithoutKeyIsAuth 确认「还没保存 key」不发请求就归 auth。
func TestTestWithoutKeyIsAuth(t *testing.T) {
	f, server := newFakeMTeam(t)

	integration := New(Options{Host: server.URL})
	result := integration.Test(context.Background(), settings.Section{}, settings.TestInput{})

	if result.OK || result.Code != settings.CodeAuth {
		t.Errorf("Test = %+v, want 不 OK 且 code=auth", result)
	}
	if requests := f.recorded(t); len(requests) != 0 {
		t.Errorf("请求数 = %d, want 0（本地就能判定）", len(requests))
	}
}

// TestResolveFollowsTheWholePath 钉住取种主链（P5-2）：
// 搜索 → 按策略选 → genDlToken → 下载 → **本地算 infohash** → label 取 labelsNew。
func TestResolveFollowsTheWholePath(t *testing.T) {
	f, server := newFakeMTeam(t)
	f.handlers["/api/torrent/search"] = func(w http.ResponseWriter, _ *http.Request) {
		// 两条候选：做种多的那条没有中字，做种少的那条有中字且 FREE。
		// 策略第一条是 seeders，所以选做种多的那条。
		jsonEnvelope(w, `{"data":[
			{"id":"popular","name":"SSIS-001 4K","size":"8192","labelsNew":[],
			 "status":{"seeders":"50","discount":""}},
			{"id":"chinese","name":"SSIS-001 中字","size":"4096","labelsNew":["中字"],
			 "status":{"seeders":"5","discount":"FREE"}}
		],"total":2}`)
	}
	f.handlers["/api/torrent/genDlToken"] = func(w http.ResponseWriter, r *http.Request) {
		jsonEnvelope(w, `"`+server.URL+`/link"`)
	}
	f.handlers["/link"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(torrentCanonical))
	}

	integration := New(Options{Host: server.URL})
	torrent, err := integration.Resolve(context.Background(), "secret", "SSIS-001")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if torrent.ID != "popular" {
		t.Errorf("选中的 id = %s, want popular（seeders 优先于折扣与中字）", torrent.ID)
	}
	if torrent.Infohash != "f875b7bedb38e072dde96440fb79e5fc55699d68" {
		t.Errorf("infohash = %s, want 由 .torrent 的 info 原始字节算出", torrent.Infohash)
	}
	if string(torrent.Bytes) != torrentCanonical {
		t.Error("Bytes 应当原样保留取回的种子字节（加种与 payload 备份都用它）")
	}
	if torrent.Label != "" {
		t.Errorf("label = %q, want 空（选中的那条没有中字标签）", torrent.Label)
	}

	for _, request := range f.recorded(t) {
		if request.Path == "/api/torrent/genDlToken" && request.Body != "id=popular" {
			t.Errorf("genDlToken body = %q, want id=popular", request.Body)
		}
	}
}

// TestResolveNoHitsIsAnAnswer 钉住「M-Team 上没有这部片」不是故障：
// 它是 NotFound，调用方据此降级磁力，而面板不该把它显示成 M-Team 故障。
func TestResolveNoHitsIsAnAnswer(t *testing.T) {
	f, server := newFakeMTeam(t)
	f.handlers["/api/torrent/search"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonEnvelope(w, `{"data":[],"total":0}`)
	}

	integration := New(Options{Host: server.URL})
	_, err := integration.Resolve(context.Background(), "secret", "MIDA-821")
	if err == nil {
		t.Fatal("没有候选时 Resolve 应当报错")
	}
	var notFound NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v, want NotFoundError", err)
	}
	if requests := f.recorded(t); len(requests) != 1 {
		t.Errorf("请求数 = %d, want 1（没有候选就不再 genDlToken/下载，省配额）", len(requests))
	}
}

// TestResolveRejectsUnusableTorrent 确认取回的字节不能算 infohash 时归 protocol：
// 那是上游行为异常，不是网络问题。
func TestResolveRejectsUnusableTorrent(t *testing.T) {
	f, server := newFakeMTeam(t)
	f.handlers["/api/torrent/search"] = func(w http.ResponseWriter, _ *http.Request) {
		jsonEnvelope(w, `{"data":[{"id":"1","name":"X","size":"1","labelsNew":[],"status":{"seeders":"1"}}]}`)
	}
	f.handlers["/api/torrent/genDlToken"] = func(w http.ResponseWriter, r *http.Request) {
		jsonEnvelope(w, `"`+server.URL+`/link"`)
	}
	f.handlers["/link"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>not a torrent</html>"))
	}

	integration := New(Options{Host: server.URL})
	_, err := integration.Resolve(context.Background(), "secret", "SSIS-001")
	if err == nil {
		t.Fatal("不可用的种子应当报错")
	}
	if got := Classify(err); got != settings.CodeProtocol {
		t.Errorf("Classify = %s, want protocol（上游行为异常）", got)
	}
}
