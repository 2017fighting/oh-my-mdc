package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/retry"
	"github.com/2017fighting/oh-my-mdc/internal/server"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 人工重试的 HTTP 契约（规格 §8.4 / #31）。
//
// 行为本身在 internal/retry 的接缝上钉（真 Postgres + 假 qbt）；这里只钉形状：
// 路径、force 透传、逐版结果信封，以及「版本侧被拒不是 HTTP 错误」这条语义。

// stubRetrier 记录调用参数并回一份固定结果。
type stubRetrier struct {
	movieID string
	force   bool
	result  retry.Result
	err     error
}

func (s *stubRetrier) Retry(_ context.Context, movieID string, force bool) (retry.Result, error) {
	s.movieID = movieID
	s.force = force
	return s.result, s.err
}

func retryHandler(t *testing.T, retrier server.Retrier) http.Handler {
	t.Helper()
	return newHandler(t, server.Options{DB: pinger{}, Retrier: retrier})
}

// TestTaskRetryPassesForceAndReturnsPerVersionOutcomes 钉住请求/回执形状。
func TestTaskRetryPassesForceAndReturnsPerVersionOutcomes(t *testing.T) {
	stub := &stubRetrier{result: retry.Result{
		MovieID: "YwrO3B",
		Versions: []retry.Outcome{
			{
				Infohash:  "1111111111111111111111111111111111111111",
				Action:    retry.ActionResetProcessing,
				FromState: task.StatusFinished, ToState: task.StatusProcessing,
				Reason: "强制重刮：回到加工开头", ForceRescrape: true,
			},
			{
				Infohash:  "2222222222222222222222222222222222222222",
				Action:    retry.ActionSkipped,
				FromState: task.StatusDownloading, ToState: task.StatusDownloading,
				Reason: "qBittorrent 里仍在下载：永不重下",
			},
		},
	}}
	handler := retryHandler(t, stub)

	rec := jsonOf(t, send(t, handler, http.MethodPost, "/api/tasks/YwrO3B/retry", `{"force":true}`))
	if stub.movieID != "YwrO3B" || !stub.force {
		t.Errorf("Retry(%q, force=%v), want (YwrO3B, true)", stub.movieID, stub.force)
	}
	if rec["ok"] != true || rec["movie_id"] != "YwrO3B" || rec["force"] != true {
		t.Errorf("回执 = %v, want ok/movie_id/force", rec)
	}

	results, ok := rec["results"].([]any)
	if !ok || len(results) != 2 {
		t.Fatalf("results = %v, want 两条逐版结果", rec["results"])
	}
	first, _ := results[0].(map[string]any)
	if first["infohash"] != "1111111111111111111111111111111111111111" ||
		first["action"] != "reset_processing" ||
		first["from_state"] != "finished" || first["to_state"] != "processing" ||
		first["force_rescrape"] != true || first["reason"] == "" {
		t.Errorf("第一条结果 = %v", first)
	}
	second, _ := results[1].(map[string]any)
	if second["action"] != "skipped" || second["reason"] == "" {
		t.Errorf("第二条结果 = %v, want skipped + 人话 reason", second)
	}
}

// TestTaskRetryDefaultsToPlainRetry 钉住缺省语义：没有请求体 / 只有 {} 都是 force=false。
func TestTaskRetryDefaultsToPlainRetry(t *testing.T) {
	for _, body := range []string{"", "{}"} {
		stub := &stubRetrier{result: retry.Result{MovieID: "YwrO3B"}}
		handler := retryHandler(t, stub)

		rec := jsonOf(t, send(t, handler, http.MethodPost, "/api/tasks/YwrO3B/retry", body))
		if stub.force {
			t.Errorf("body=%q: force = true, want false", body)
		}
		if rec["ok"] != true {
			t.Errorf("body=%q: 回执 = %v, want ok", body, rec)
		}
	}
}

// TestTaskRetryMapsMissingTaskToNotFound 钉住 404：任务不存在用错误信封。
func TestTaskRetryMapsMissingTaskToNotFound(t *testing.T) {
	handler := retryHandler(t, &stubRetrier{err: task.ErrTaskNotFound})

	raw := send(t, handler, http.MethodPost, "/api/tasks/no-such/retry", "{}")
	if raw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", raw.Code)
	}
	rec := jsonOf(t, raw)
	envelope, _ := rec["error"].(map[string]any)
	if rec["ok"] != false || envelope["code"] != "not_found" {
		t.Errorf("错误信封 = %v, want {ok:false, error:{code:not_found}}", rec)
	}
}

// TestTaskRetryRejectsBadJSON 钉住 400：重试只收一个布尔，不是 JSON 就不猜。
func TestTaskRetryRejectsBadJSON(t *testing.T) {
	handler := retryHandler(t, &stubRetrier{})

	raw := send(t, handler, http.MethodPost, "/api/tasks/YwrO3B/retry", "{")
	if raw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", raw.Code)
	}
	rec := jsonOf(t, raw)
	envelope, _ := rec["error"].(map[string]any)
	if envelope["code"] != "validation_error" {
		t.Errorf("错误信封 = %v, want validation_error", rec)
	}
}

// TestTaskRetryIsUnavailableWithoutTheEngine 钉住 503：引擎没装配时不许假装成功。
func TestTaskRetryIsUnavailableWithoutTheEngine(t *testing.T) {
	handler := retryHandler(t, nil)

	raw := send(t, handler, http.MethodPost, "/api/tasks/YwrO3B/retry", "{}")
	if raw.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", raw.Code)
	}
	rec := jsonOf(t, raw)
	envelope, _ := rec["error"].(map[string]any)
	if envelope["code"] != "db_unavailable" {
		t.Errorf("错误信封 = %v, want db_unavailable", rec)
	}
}

// ensure the stub satisfies the server seam.
var _ server.Retrier = (*stubRetrier)(nil)
