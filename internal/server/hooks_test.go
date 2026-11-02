package server_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/server"
)

// qbt autorun 回调的 HTTP 契约（规格 §5.4/§8.4）：恒 200，载荷只有 infohash。
//
// 断言只看 HTTP 面：状态码、body、以及接缝收到的是不是 query 里的原样值。
// 判定逻辑（回读 qbt、推进 processing、未知 infohash 零写入）在 reconcile 的接缝上测。

type fakeHooks struct {
	calls []string
	err   error
}

func (f *fakeHooks) HandleWebhook(_ context.Context, infohash string) error {
	f.calls = append(f.calls, infohash)
	return f.err
}

// TestQbtHookAnswers200AndForwardsInfohash 钉住 P8-1 的传输面。
func TestQbtHookAnswers200AndForwardsInfohash(t *testing.T) {
	hooks := &fakeHooks{}
	h := newHandler(t, server.Options{DB: pinger{}, Hooks: hooks})

	const hash = "1111111111111111111111111111111111111111"
	rec := send(t, h, http.MethodGet, "/api/hooks/qbt?ih="+hash, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if len(hooks.calls) != 1 || hooks.calls[0] != hash {
		t.Errorf("接缝收到 = %v, want [%s]", hooks.calls, hash)
	}
}

// TestQbtHookStays200WhenTheEngineFails 钉住「恒 200」：qbt 的 autorun 是
// `curl -fsS -m 10`，非 2xx 只会让 qbt 记一次失败，对这次下载没有任何补救。
func TestQbtHookStays200WhenTheEngineFails(t *testing.T) {
	hooks := &fakeHooks{err: errors.New("数据库连不上")}
	h := newHandler(t, server.Options{DB: pinger{}, Hooks: hooks})

	rec := send(t, h, http.MethodGet, "/api/hooks/qbt?ih=1111111111111111111111111111111111111111", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（引擎失败也不改变回执）: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
}

// TestQbtHookToleratesMissingInfohash 钉住「未知 ih 静默 no-op」的输入面：
// 缺参数也回 200，把空串交给引擎（引擎自己判成 no-op）。
func TestQbtHookToleratesMissingInfohash(t *testing.T) {
	hooks := &fakeHooks{}
	h := newHandler(t, server.Options{DB: pinger{}, Hooks: hooks})

	rec := send(t, h, http.MethodGet, "/api/hooks/qbt", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if len(hooks.calls) != 1 || hooks.calls[0] != "" {
		t.Errorf("接缝收到 = %v, want [\"\"]", hooks.calls)
	}
}

// TestQbtHookAnswersWithoutEngine 确认引擎没装配时也不回 5xx：
// 回调是外部系统打的，没有义务告诉它我们的依赖状态。
func TestQbtHookAnswersWithoutEngine(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})

	rec := send(t, h, http.MethodGet, "/api/hooks/qbt?ih=1111111111111111111111111111111111111111", "")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200（未装配引擎也是 200）: %s", rec.Code, rec.Body)
	}
}
