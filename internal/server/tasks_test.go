package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/loops"
	"github.com/2017fighting/oh-my-mdc/internal/server"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 任务面板的测试都在「HTTP 契约 + 真 Postgres」这个接缝上（与设置域一致）：
// 数据用 task.Store 落，断言只按 GET /api/tasks 与 /api/overview 的 JSON 说话。

type taskFixture struct {
	handler  http.Handler
	database *db.DB
	store    *task.Store
	loops    *loops.Registry
}

func newTaskFixture(t *testing.T) *taskFixture {
	t.Helper()

	database := dbtest.Migrated(t)
	if _, err := database.Pool().Exec(context.Background(),
		`INSERT INTO list_registry (list_id, purpose, name) VALUES ('wanted', 'want_watch', '想看清单')`,
	); err != nil {
		t.Fatalf("种清单登记失败: %v", err)
	}

	store := task.NewStore(database.Pool())
	outbox := sidefx.New(database.Pool())
	registry := loops.NewRegistry()

	handler := newHandler(t, server.Options{
		DB:       pinger{},
		Settings: settings.NewStore(database.Pool()),
		Tasks:    store,
		Sidefx:   outbox,
		Loops:    registry,
	})
	return &taskFixture{handler: handler, database: database, store: store, loops: registry}
}

func (f *taskFixture) seed(t *testing.T, movieID, number, infohash string) {
	t.Helper()
	inserted, err := f.store.SaveCandidate(context.Background(), task.Candidate{
		MovieID:  movieID,
		Number:   number,
		ListID:   "wanted",
		Infohash: infohash,
		Source:   task.SourceJavDBMagnet,
		Delivery: task.DeliveryMagnet,
		Label:    "中文",
		Payload:  "magnet:?xt=urn:btih:" + infohash,
	})
	if err != nil || !inserted {
		t.Fatalf("种任务 %s: inserted=%v err=%v", movieID, inserted, err)
	}
}

func (f *taskFixture) setStatus(t *testing.T, infohash, status string) {
	t.Helper()
	if _, err := f.database.Pool().Exec(context.Background(),
		`UPDATE task_version SET status = $1 WHERE infohash = $2`, status, infohash); err != nil {
		t.Fatalf("设置状态: %v", err)
	}
}

func tasksIn(t *testing.T, rec map[string]any) []map[string]any {
	t.Helper()
	raw, ok := rec["tasks"].([]any)
	if !ok {
		t.Fatalf("tasks = %v, want 数组", rec["tasks"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("tasks 行 = %v, want 对象", item)
		}
		out = append(out, row)
	}
	return out
}

// TestTasksListExposesViewStateAndVersionChips 钉住 GET /api/tasks 的形状与筛选。
func TestTasksListExposesViewStateAndVersionChips(t *testing.T) {
	f := newTaskFixture(t)
	f.seed(t, "YwrO3B", "ABP-123", "1111111111111111111111111111111111111111")
	f.seed(t, "k4EVE4", "SSIS-001", "2222222222222222222222222222222222222222")
	f.setStatus(t, "2222222222222222222222222222222222222222", "finished")

	all := tasksIn(t, jsonOf(t, send(t, f.handler, http.MethodGet, "/api/tasks", "")))
	if len(all) != 2 {
		t.Fatalf("tasks = %d, want 2", len(all))
	}
	for _, row := range all {
		versions, ok := row["versions"].([]any)
		if !ok || len(versions) != 1 {
			t.Fatalf("%v 的 versions = %v, want 一条版本摘要", row["number"], row["versions"])
		}
		version, _ := versions[0].(map[string]any)
		if version["infohash"] == "" {
			t.Errorf("version.infohash 为空: %v", version)
		}
		if version["source"] != "javdb_magnet" || version["label"] != "中文" {
			t.Errorf("version = %v, want source=javdb_magnet label=中文", version)
		}
	}

	finished := tasksIn(t, jsonOf(t, send(t, f.handler, http.MethodGet, "/api/tasks?state=finished", "")))
	if len(finished) != 1 || finished[0]["number"] != "SSIS-001" {
		t.Fatalf("state=finished = %+v, want 只有 SSIS-001", finished)
	}
	if finished[0]["state"] != "finished" {
		t.Errorf("state = %v, want finished（视图聚合）", finished[0]["state"])
	}

	searched := tasksIn(t, jsonOf(t, send(t, f.handler, http.MethodGet, "/api/tasks?q=ssis", "")))
	if len(searched) != 1 || searched[0]["number"] != "SSIS-001" {
		t.Fatalf("q=ssis = %+v, want 只有 SSIS-001", searched)
	}

	for _, bad := range []string{"/api/tasks?state=bogus", "/api/tasks?page=abc"} {
		rec := send(t, f.handler, http.MethodGet, bad, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400: %s", bad, rec.Code, rec.Body)
		}
		code, _ := errorOf(t, jsonOf(t, rec))
		if code != "validation_error" {
			t.Errorf("GET %s error.code = %q, want validation_error", bad, code)
		}
	}
}

// TestTaskDetailReturnsVersionsAndTimeline 钉住抽屉数据：任务 + 版本 + 最近事件。
func TestTaskDetailReturnsVersionsAndTimeline(t *testing.T) {
	f := newTaskFixture(t)
	f.seed(t, "YwrO3B", "ABP-123", "1111111111111111111111111111111111111111")

	body := jsonOf(t, send(t, f.handler, http.MethodGet, "/api/tasks/YwrO3B", ""))
	if body["ok"] != true {
		t.Fatalf("ok = %v, want true", body["ok"])
	}

	row, ok := body["task"].(map[string]any)
	if !ok || row["movie_id"] != "YwrO3B" {
		t.Fatalf("task = %v, want YwrO3B", body["task"])
	}
	if row["state"] != "downloading" {
		t.Errorf("state = %v, want downloading", row["state"])
	}

	events, ok := body["events"].([]any)
	if !ok || len(events) == 0 {
		t.Fatalf("events = %v, want 至少一条（版本创建）", body["events"])
	}
	first, _ := events[0].(map[string]any)
	if first["kind"] != "state_change" || first["reason"] == "" {
		t.Errorf("event = %v, want state_change 带原因", first)
	}

	missing := send(t, f.handler, http.MethodGet, "/api/tasks/nope", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("GET /api/tasks/nope = %d, want 404: %s", missing.Code, missing.Body)
	}
	code, _ := errorOf(t, jsonOf(t, missing))
	if code != "not_found" {
		t.Errorf("error.code = %q, want not_found", code)
	}
}

// TestOverviewCountsAndLoopHealth 钉住统计卡与循环健康位的接线：
// counts 来自 task_state 视图，needs_attention 还要算上驻留副作用。
func TestOverviewCountsAndLoopHealth(t *testing.T) {
	f := newTaskFixture(t)
	f.seed(t, "YwrO3B", "ABP-123", "1111111111111111111111111111111111111111")
	f.seed(t, "k4EVE4", "SSIS-001", "2222222222222222222222222222222222222222")
	f.setStatus(t, "2222222222222222222222222222222222222222", "finished")

	// 一条驻留副作用（done_at IS NULL AND next_attempt_at IS NULL）。
	if _, err := f.database.Pool().Exec(context.Background(),
		`INSERT INTO side_effect (kind, target_key, next_attempt_at) VALUES ('notify', 'stuck:1', NULL)`,
	); err != nil {
		t.Fatalf("种驻留副作用: %v", err)
	}

	ranAt := time.Date(2024, 3, 2, 1, 2, 3, 0, time.UTC)
	f.loops.Record(loops.Poll, ranAt, true)

	body := jsonOf(t, send(t, f.handler, http.MethodGet, "/api/overview", ""))

	counts, ok := body["counts"].(map[string]any)
	if !ok {
		t.Fatalf("counts = %v, want 对象", body["counts"])
	}
	if counts["downloading"] != float64(1) || counts["finished"] != float64(1) {
		t.Errorf("counts = %v, want downloading=1 finished=1", counts)
	}
	if counts["needs_attention"] != float64(1) {
		t.Errorf("needs_attention = %v, want 1（驻留副作用）", counts["needs_attention"])
	}

	loopRows, ok := body["loops"].(map[string]any)
	if !ok {
		t.Fatalf("loops = %v, want 对象", body["loops"])
	}
	poll, _ := loopRows["poll"].(map[string]any)
	if poll["last_ok"] != true || poll["last_run_at"] == nil {
		t.Errorf("loops.poll = %v, want 跑过一次且成功", poll)
	}
	reconcile, _ := loopRows["reconcile"].(map[string]any)
	if reconcile["last_run_at"] != nil {
		t.Errorf("loops.reconcile = %v, want 还没跑过的空位（#25 接线）", reconcile)
	}
}

// TestTaskEndpointsAreUnavailableWithoutTaskStore 确认没有任务域时端点明确回 503。
func TestTaskEndpointsAreUnavailableWithoutTaskStore(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})

	for _, path := range []string{"/api/tasks", "/api/tasks/YwrO3B"} {
		rec := send(t, h, http.MethodGet, path, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503: %s", path, rec.Code, rec.Body)
		}
	}
}
