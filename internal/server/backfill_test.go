package server_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/backfill"
	"github.com/2017fighting/oh-my-mdc/internal/server"
)

// 补录（#43）的 HTTP 契约：列表是读模型（GET），一键补录是回执式动作（POST，
// 幂等放行 duplicate 也是 200）。错误信封按原因分状态码与稳定机器码。

type fakeBackfiller struct {
	scanDirs []backfill.Candidate
	scanErr  error
	result   backfill.Result
	err      error
	calls    []string
}

func (f *fakeBackfiller) Scan(context.Context) ([]backfill.Candidate, error) {
	return f.scanDirs, f.scanErr
}

func (f *fakeBackfiller) Backfill(_ context.Context, number string) (backfill.Result, error) {
	f.calls = append(f.calls, number)
	return f.result, f.err
}

func newBackfillFixture(t *testing.T, assembler server.Backfiller) http.Handler {
	t.Helper()
	if assembler == nil {
		assembler = &fakeBackfiller{}
	}
	return newHandler(t, server.Options{DB: pinger{}, Backfiller: assembler})
}

func TestBackfillScanReturnsDirs(t *testing.T) {
	fake := &fakeBackfiller{scanDirs: []backfill.Candidate{
		{Number: "SONE-682", Videos: 2, Bytes: 333},
	}}
	h := newBackfillFixture(t, fake)

	rec := send(t, h, http.MethodGet, "/api/backfill/scan", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	body := jsonOf(t, rec)
	dirs, ok := body["dirs"].([]any)
	if !ok || len(dirs) != 1 {
		t.Fatalf("dirs = %v, want 一行", body["dirs"])
	}
	row, _ := dirs[0].(map[string]any)
	if row["number"] != "SONE-682" || row["videos"] != float64(2) || row["bytes"] != float64(333) {
		t.Errorf("行 = %v, want number/videos/bytes", row)
	}
}

func TestBackfillScanGated(t *testing.T) {
	h := newBackfillFixture(t, &fakeBackfiller{scanErr: backfill.ErrGated})
	rec := send(t, h, http.MethodGet, "/api/backfill/scan", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if code := jsonOf(t, rec)["error"].(map[string]any)["code"]; code != "gate_closed" {
		t.Errorf("code = %v, want gate_closed", code)
	}
}

func TestBackfillScanRequiresAssembler(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})
	rec := send(t, h, http.MethodGet, "/api/backfill/scan", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestBackfillRunReturnsOutcome(t *testing.T) {
	fake := &fakeBackfiller{result: backfill.Result{
		Number: "SONE-682", MovieID: "SONE-682",
		Outcome: backfill.OutcomeBackfilled, Versions: 2,
	}}
	h := newBackfillFixture(t, fake)

	rec := send(t, h, http.MethodPost, "/api/backfill", `{"number":"SONE-682"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	body := jsonOf(t, rec)
	if body["ok"] != true || body["outcome"] != backfill.OutcomeBackfilled ||
		body["movie_id"] != "SONE-682" || body["versions"] != float64(2) {
		t.Errorf("body = %v, want ok/outcome/movie_id/versions", body)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "SONE-682" {
		t.Errorf("补录器收到 %v, want [SONE-682]", fake.calls)
	}
}

func TestBackfillRunDuplicateIsOK(t *testing.T) {
	fake := &fakeBackfiller{result: backfill.Result{
		Number: "SONE-682", MovieID: "SONE-682",
		Outcome: backfill.OutcomeDuplicate, Reason: "这个番号已经有任务了",
	}}
	h := newBackfillFixture(t, fake)

	rec := send(t, h, http.MethodPost, "/api/backfill", `{"number":"SONE-682"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（幂等放行）", rec.Code)
	}
	if body := jsonOf(t, rec); body["outcome"] != backfill.OutcomeDuplicate || body["message"] != "这个番号已经有任务了" {
		t.Errorf("body = %v, want duplicate + message", body)
	}
}

func TestBackfillRunErrorEnvelope(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"番号形状", backfill.ErrBadNumber, http.StatusBadRequest, "validation_error"},
		{"门禁", backfill.ErrGated, http.StatusConflict, "gate_closed"},
		{"目录没了", backfill.ErrDirMissing, http.StatusConflict, "dir_missing"},
		{"没有视频", backfill.ErrNoVideos, http.StatusConflict, "no_videos"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newBackfillFixture(t, &fakeBackfiller{err: c.err})
			rec := send(t, h, http.MethodPost, "/api/backfill", `{"number":"X"}`)
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d", rec.Code, c.status)
			}
			if code := jsonOf(t, rec)["error"].(map[string]any)["code"]; code != c.code {
				t.Errorf("code = %v, want %s", code, c.code)
			}
		})
	}

	// 其它错误一律 503 db_unavailable（数据库抖了，不是用户的错）。
	h := newBackfillFixture(t, &fakeBackfiller{err: errors.New("连接池炸了")})
	rec := send(t, h, http.MethodPost, "/api/backfill", `{"number":"X"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestBackfillRunRejectsBadBody(t *testing.T) {
	h := newBackfillFixture(t, nil)
	rec := send(t, h, http.MethodPost, "/api/backfill", "not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	// 缺 number 的请求体也进不了补录器：形状在门面就拦住。
	fake := &fakeBackfiller{}
	h = newBackfillFixture(t, fake)
	rec = send(t, h, http.MethodPost, "/api/backfill", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 number: status = %d, want 400", rec.Code)
	}
	if len(fake.calls) != 0 {
		t.Errorf("坏请求体也打了补录器")
	}
}
