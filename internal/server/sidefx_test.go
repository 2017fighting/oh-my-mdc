package server_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/server"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 副作用队列与「需要人工」区的 HTTP 契约（规格 §8.2/§8.4）。
//
// 接缝仍是「HTTP + 真 Postgres」：待办用 sidefx.Outbox 入箱，断言只看 JSON。

type sidefxFixture struct {
	handler  http.Handler
	database *db.DB
	outbox   *sidefx.Outbox
	store    *task.Store
}

func newSidefxFixture(t *testing.T) *sidefxFixture {
	t.Helper()

	database := dbtest.Migrated(t)
	if _, err := database.Pool().Exec(context.Background(),
		`INSERT INTO list_registry (list_id, purpose, name) VALUES ('wanted', 'want_watch', '想看清单')`,
	); err != nil {
		t.Fatalf("种清单登记失败: %v", err)
	}

	outbox := sidefx.New(database.Pool())
	store := task.NewStore(database.Pool())
	handler := newHandler(t, server.Options{
		DB:       pinger{},
		Settings: settings.NewStore(database.Pool()),
		Tasks:    store,
		Sidefx:   outbox,
	})
	return &sidefxFixture{handler: handler, database: database, outbox: outbox, store: store}
}

func (f *sidefxFixture) enqueue(t *testing.T, kind sidefx.Kind, targetKey string, payload map[string]any) int64 {
	t.Helper()
	if err := f.outbox.Enqueue(context.Background(), kind, targetKey, payload); err != nil {
		t.Fatalf("入箱: %v", err)
	}
	jobs, err := f.outbox.List(context.Background(), "", 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, job := range jobs {
		if job.Kind == kind && job.TargetKey == targetKey {
			return job.ID
		}
	}
	t.Fatalf("入箱之后找不到 %s/%s", kind, targetKey)
	return 0
}

func sideEffectsIn(t *testing.T, rec map[string]any) []map[string]any {
	t.Helper()
	raw, ok := rec["side_effects"].([]any)
	if !ok {
		t.Fatalf("side_effects = %v, want 数组", rec["side_effects"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("side_effects 行 = %v, want 对象", item)
		}
		out = append(out, row)
	}
	return out
}

// TestSideEffectCreatedAtIsNullForLegacyRows 钉住「不知道就说不知道」：
// 迁移前入箱的行没有 created_at，面板拿到 null 就不说年龄，
// 而不是拿迁移时刻或零值冒充一个「刚刚」。
func TestSideEffectCreatedAtIsNullForLegacyRows(t *testing.T) {
	f := newSidefxFixture(t)
	id := f.enqueue(t, sidefx.KindNotify, "filed:legacy1", map[string]any{"number": "ABP-123"})
	if _, err := f.database.Pool().Exec(context.Background(),
		`UPDATE side_effect SET created_at = NULL WHERE id = $1`, id); err != nil {
		t.Fatalf("把 created_at 改回老行的样子: %v", err)
	}

	rows := sideEffectsIn(t, jsonOf(t, send(t, f.handler, http.MethodGet, "/api/side-effects", "")))
	if len(rows) != 1 {
		t.Fatalf("side_effects = %+v, want 一条", rows)
	}
	if rows[0]["created_at"] != nil {
		t.Errorf("created_at = %v, want null（老行没有入箱时间）", rows[0]["created_at"])
	}
}

// TestSideEffectsListSplitsPendingAndStuck 钉住两个筛选项的语义（规格 §4.2/§8.4）：
// stuck = 驻留（需人工），pending = 排队中，不传 state = 全部未完成。
func TestSideEffectsListSplitsPendingAndStuck(t *testing.T) {
	f := newSidefxFixture(t)
	stuckID := f.enqueue(t, sidefx.KindListRemoval, "wanted:YwrO3B", map[string]any{
		"movie_id": "YwrO3B", "list_id": "wanted", "number": "ABP-123",
	})
	f.enqueue(t, sidefx.KindJellyfinRefresh, "k4EVE4", map[string]any{"movie_id": "k4EVE4"})
	if err := f.outbox.Stick(context.Background(), stuckID, "永久错"); err != nil {
		t.Fatalf("Stick: %v", err)
	}

	stuck := sideEffectsIn(t, jsonOf(t, send(t, f.handler, http.MethodGet, "/api/side-effects?state=stuck", "")))
	if len(stuck) != 1 {
		t.Fatalf("state=stuck = %+v, want 一条", stuck)
	}
	if stuck[0]["kind"] != "list_removal" || stuck[0]["stuck"] != true {
		t.Errorf("驻留行 = %v, want kind=list_removal stuck=true", stuck[0])
	}
	if stuck[0]["last_error"] != "永久错" {
		t.Errorf("last_error = %v, want 永久错", stuck[0]["last_error"])
	}
	// 入箱时间必须跟着出来：面板拿它说「最早 N 天前」。
	if created, _ := stuck[0]["created_at"].(string); created == "" {
		t.Errorf("created_at = %v, want 入箱时间", stuck[0]["created_at"])
	}
	payload, _ := stuck[0]["payload"].(map[string]any)
	if payload["number"] != "ABP-123" || payload["list_id"] != "wanted" {
		t.Errorf("payload = %v, want 原样透出（面板要说清这一条在做什么）", payload)
	}

	pending := sideEffectsIn(t, jsonOf(t, send(t, f.handler, http.MethodGet, "/api/side-effects?state=pending", "")))
	if len(pending) != 1 || pending[0]["kind"] != "jellyfin_refresh" {
		t.Fatalf("state=pending = %+v, want 一条 jellyfin_refresh", pending)
	}
	if pending[0]["stuck"] != false {
		t.Errorf("排队行的 stuck = %v, want false", pending[0]["stuck"])
	}

	all := sideEffectsIn(t, jsonOf(t, send(t, f.handler, http.MethodGet, "/api/side-effects", "")))
	if len(all) != 2 {
		t.Fatalf("不传 state = %+v, want 全部未完成两条", all)
	}

	bad := send(t, f.handler, http.MethodGet, "/api/side-effects?state=done", "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("state=done = %d, want 400", bad.Code)
	}
}

// TestSideEffectRetryClearsResidency 钉住人工重发（P12-3 的重发面）：
// 清驻留、重排、回新的一行；已完成/不存在的 id 回 404。
func TestSideEffectRetryClearsResidency(t *testing.T) {
	f := newSidefxFixture(t)
	id := f.enqueue(t, sidefx.KindNotify, "filed:abc", map[string]any{"number": "ABP-123"})
	if err := f.outbox.Stick(context.Background(), id, "chat 未激活"); err != nil {
		t.Fatalf("Stick: %v", err)
	}

	rec := send(t, f.handler, http.MethodPost, "/api/side-effects/"+itoa(id)+"/retry", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)
	row, _ := body["side_effect"].(map[string]any)
	if row["stuck"] != false {
		t.Errorf("重发之后 stuck = %v, want false", row["stuck"])
	}
	if row["next_attempt_at"] == nil {
		t.Error("重发之后 next_attempt_at 还是 null（还驻留着）")
	}

	jobs, err := f.outbox.List(context.Background(), sidefx.StatePending, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Attempts != 0 {
		t.Fatalf("重发之后 = %+v, want 一条排队且预算归零", jobs)
	}

	for _, path := range []string{"/api/side-effects/999999/retry", "/api/side-effects/abc/retry"} {
		rec := send(t, f.handler, http.MethodPost, path, "")
		want := http.StatusNotFound
		if path == "/api/side-effects/abc/retry" {
			want = http.StatusBadRequest
		}
		if rec.Code != want {
			t.Errorf("POST %s = %d, want %d: %s", path, rec.Code, want, rec.Body)
		}
	}
}

// TestAttentionListsFailedAndStalled 钉住「需要人工」区的任务侧两半（规格 §8.2）：
// failed(phase, reason) 与「仍在下载但已告过停滞」的版本。
func TestAttentionListsFailedAndStalled(t *testing.T) {
	f := newSidefxFixture(t)
	ctx := context.Background()

	// 一条失败的版本（加工阶段）。
	inserted, err := f.store.SaveCandidate(ctx, task.Candidate{
		MovieID: "YwrO3B", Number: "ABP-123", ListID: "wanted",
		Infohash: "1111111111111111111111111111111111111111",
		Source:   task.SourceJavDBMagnet, Delivery: task.DeliveryMagnet,
	})
	if err != nil || !inserted {
		t.Fatalf("种版本: inserted=%v err=%v", inserted, err)
	}
	if _, err := f.database.Pool().Exec(ctx,
		`UPDATE task_version
		 SET status = 'failed', error_phase = 'scrape', error_reason = 'DMM 超时'
		 WHERE infohash = '1111111111111111111111111111111111111111'`); err != nil {
		t.Fatalf("置失败: %v", err)
	}

	// 一条停滞的下载：仍在 downloading，但时间线上有一条停滞告警。
	inserted, err = f.store.SaveCandidate(ctx, task.Candidate{
		MovieID: "k4EVE4", Number: "SSIS-001", ListID: "wanted",
		Infohash: "2222222222222222222222222222222222222222",
		Source:   task.SourceJavDBMagnet, Delivery: task.DeliveryMagnet,
	})
	if err != nil || !inserted {
		t.Fatalf("种版本: inserted=%v err=%v", inserted, err)
	}
	if _, err := f.database.Pool().Exec(ctx,
		`UPDATE task_version SET add_pending = false WHERE infohash = '2222222222222222222222222222222222222222'`); err != nil {
		t.Fatalf("清 add_pending: %v", err)
	}
	if err := f.store.AppendEvent(ctx, task.Event{
		MovieID:  "k4EVE4",
		Infohash: "2222222222222222222222222222222222222222",
		Kind:     task.EventAlert,
		Phase:    string(task.PhaseDownloading),
		Reason:   task.StallReasonPrefix + "：进度 42%，已 60 分钟未动",
	}); err != nil {
		t.Fatalf("记停滞告警: %v", err)
	}

	body := jsonOf(t, send(t, f.handler, http.MethodGet, "/api/attention", ""))

	failed, _ := body["failed"].([]any)
	if len(failed) != 1 {
		t.Fatalf("failed = %v, want 一条", body["failed"])
	}
	failedRow, _ := failed[0].(map[string]any)
	if failedRow["number"] != "ABP-123" || failedRow["phase"] != "scrape" || failedRow["reason"] != "DMM 超时" {
		t.Errorf("失败行 = %v, want 番号 + phase + reason", failedRow)
	}

	stalled, _ := body["stalled"].([]any)
	if len(stalled) != 1 {
		t.Fatalf("stalled = %v, want 一条", body["stalled"])
	}
	stalledRow, _ := stalled[0].(map[string]any)
	if stalledRow["number"] != "SSIS-001" {
		t.Errorf("停滞行 = %v, want SSIS-001", stalledRow)
	}
}

// TestSideEffectsEndpointsWithoutDomainAnswer503：数据库/域没装配时不是 500 也不是空数组。
func TestSideEffectsEndpointsWithoutDomainAnswer503(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})

	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/api/side-effects"},
		{http.MethodPost, "/api/side-effects/1/retry"},
	} {
		rec := send(t, h, req.method, req.path, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", req.method, req.path, rec.Code)
		}
	}
}

// itoa 是测试里的小工具：路径参数要字符串。
func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
