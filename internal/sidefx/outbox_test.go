package sidefx_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 出箱层的测试都在「真 Postgres + 表上的判据」这个接缝上：部分唯一索引、驻留定义、
// 待删入箱与清旗同事务——这些正是规格 §4.2 点名的语义，用假实现测不出来。

const (
	movieID = "YwrO3B"
	number  = "ABP-123"
	hashOne = "1111111111111111111111111111111111111111"
)

func newOutbox(t *testing.T) (*sidefx.Outbox, *task.Store) {
	t.Helper()
	database := dbtest.Migrated(t)
	if _, err := database.Pool().Exec(context.Background(),
		`INSERT INTO list_registry (list_id, purpose, name) VALUES ('wanted', 'want_watch', '想看清单')`,
	); err != nil {
		t.Fatalf("种清单登记失败: %v", err)
	}
	return sidefx.New(database.Pool()), task.NewStore(database.Pool())
}

// seedPendingRemoval 种一条「加种成功、清单项待删」的版本（poll 的正常产物）。
func seedPendingRemoval(t *testing.T, store *task.Store, movieID, number, infohash string) {
	t.Helper()
	ctx := context.Background()
	inserted, err := store.SaveCandidate(ctx, task.Candidate{
		MovieID: movieID, Number: number, ListID: "wanted",
		Infohash: infohash, Source: task.SourceJavDBMagnet, Delivery: task.DeliveryMagnet,
		Payload: "magnet:?xt=urn:btih:" + infohash,
	})
	if err != nil || !inserted {
		t.Fatalf("种版本: inserted=%v err=%v", inserted, err)
	}
	if _, err := store.Claim(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.MarkAdded(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("MarkAdded: %v", err)
	}
}

// TestEnqueueDedupesWhilePendingButAllowsAfterDone 钉住部分唯一索引的两个方向：
// 同 (kind,target_key) 只能有一条未完成；完成之后同一个事件可以再来一条（§4.2）。
func TestEnqueueDedupesWhilePendingButAllowsAfterDone(t *testing.T) {
	outbox, _ := newOutbox(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := outbox.EnqueueNotify(ctx, sidefx.EventFiled, hashOne, map[string]any{"number": number}); err != nil {
			t.Fatalf("第 %d 次入箱: %v", i+1, err)
		}
	}
	jobs, err := outbox.List(ctx, "", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("未完成待办 = %d 条, want 1（同事件只排一条）", len(jobs))
	}
	if jobs[0].TargetKey != sidefx.EventFiled+":"+hashOne {
		t.Errorf("target_key = %q, want filed:<infohash>", jobs[0].TargetKey)
	}

	if err := outbox.MarkDone(ctx, jobs[0].ID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	if err := outbox.EnqueueNotify(ctx, sidefx.EventFiled, hashOne, map[string]any{"number": number}); err != nil {
		t.Fatalf("完成后再入箱: %v", err)
	}
	after, err := outbox.List(ctx, "", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(after) != 1 || after[0].ID == jobs[0].ID {
		t.Fatalf("完成后再入箱 = %+v, want 一条新的未完成待办", after)
	}
}

// TestDrainPendingRemovalsEnqueuesOnceAndClearsFlag：待删标记 → 待办，一次且只一次。
//
// 判据是「同一个清单项删两遍没有意义」：入箱与清旗同事务，第二轮必须是 0 条。
func TestDrainPendingRemovalsEnqueuesOnceAndClearsFlag(t *testing.T) {
	outbox, store := newOutbox(t)
	seedPendingRemoval(t, store, movieID, number, hashOne)
	ctx := context.Background()

	swept, err := outbox.DrainPendingRemovals(ctx, 10)
	if err != nil {
		t.Fatalf("DrainPendingRemovals: %v", err)
	}
	if swept != 1 {
		t.Fatalf("入箱 %d 条, want 1", swept)
	}

	jobs, err := outbox.List(ctx, sidefx.StatePending, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("待办 = %d 条, want 1", len(jobs))
	}
	if jobs[0].Kind != sidefx.KindListRemoval {
		t.Errorf("kind = %q, want list_removal", jobs[0].Kind)
	}
	if want := "wanted:" + movieID; jobs[0].TargetKey != want {
		t.Errorf("target_key = %q, want %q（<list_id>:<movie_id>）", jobs[0].TargetKey, want)
	}

	var payload map[string]any
	if err := json.Unmarshal(jobs[0].Payload, &payload); err != nil {
		t.Fatalf("payload 不是 JSON: %v", err)
	}
	if payload["movie_id"] != movieID || payload["list_id"] != "wanted" || payload["number"] != number {
		t.Errorf("payload = %v, want movie_id/list_id/number", payload)
	}

	version, err := store.Version(ctx, hashOne)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version.ListRemovalPending {
		t.Error("list_removal_pending 还是 true, want 入箱时清掉")
	}

	again, err := outbox.DrainPendingRemovals(ctx, 10)
	if err != nil {
		t.Fatalf("第二轮 DrainPendingRemovals: %v", err)
	}
	if again != 0 {
		t.Errorf("第二轮入箱 %d 条, want 0", again)
	}
}

// TestResidentIsDoneAtNullAndNextAttemptNull 钉住驻留定义（规格 §4.2）：
// 只有 next_attempt_at 为 NULL 才算需人工；排队中的不算。
func TestResidentIsDoneAtNullAndNextAttemptNull(t *testing.T) {
	outbox, _ := newOutbox(t)
	ctx := context.Background()

	if err := outbox.EnqueueNotify(ctx, sidefx.EventFiled, hashOne, nil); err != nil {
		t.Fatalf("入箱: %v", err)
	}
	jobs, err := outbox.List(ctx, "", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if jobs[0].Stuck() {
		t.Fatal("刚入箱的待办不该算驻留")
	}

	if err := outbox.Stick(ctx, jobs[0].ID, "重试耗尽"); err != nil {
		t.Fatalf("Stick: %v", err)
	}
	stuck, err := outbox.List(ctx, sidefx.StateStuck, 10)
	if err != nil {
		t.Fatalf("List(stuck): %v", err)
	}
	if len(stuck) != 1 || !stuck[0].Stuck() {
		t.Fatalf("驻留 = %+v, want 一条 next_attempt_at IS NULL", stuck)
	}
	if stuck[0].Attempts != 1 || stuck[0].LastError != "重试耗尽" {
		t.Errorf("驻留行 = %+v, want attempts+1 且留下原因", stuck[0])
	}

	count, err := outbox.StuckCount(ctx)
	if err != nil {
		t.Fatalf("StuckCount: %v", err)
	}
	if count != 1 {
		t.Errorf("StuckCount = %d, want 1", count)
	}
}

// TestRetryClearsResidency：人工重发＝清驻留、重排、给一份新预算（规格 §8.4）。
func TestRetryClearsResidency(t *testing.T) {
	outbox, _ := newOutbox(t)
	ctx := context.Background()

	if err := outbox.EnqueueNotify(ctx, sidefx.EventFiled, hashOne, nil); err != nil {
		t.Fatalf("入箱: %v", err)
	}
	jobs, err := outbox.List(ctx, "", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	id := jobs[0].ID
	if err := outbox.Stick(ctx, id, "永久错"); err != nil {
		t.Fatalf("Stick: %v", err)
	}

	if err := outbox.Retry(ctx, id); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	job, err := outbox.Get(ctx, id)
	if err != nil {
		t.Fatalf("Job: %v", err)
	}
	if job.NextAttemptAt == nil {
		t.Fatal("重发之后 next_attempt_at 还是 NULL（还驻留着）")
	}
	if job.Attempts != 0 {
		t.Errorf("attempts = %d, want 0（人工重发给一份新预算）", job.Attempts)
	}
	if job.LastError != "永久错" {
		t.Errorf("last_error = %q, want 保留上一次的原因", job.LastError)
	}

	due, err := outbox.Due(ctx, 10)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 1 || due[0].ID != id {
		t.Fatalf("Due = %+v, want 重发的那一条立即可跑", due)
	}
}

// TestRetryUnknownJobIsNotFound：面板点了一条已经完成/不存在的待办要能拿到 404。
func TestRetryUnknownJobIsNotFound(t *testing.T) {
	outbox, _ := newOutbox(t)
	ctx := context.Background()

	if err := outbox.EnqueueNotify(ctx, sidefx.EventFiled, hashOne, nil); err != nil {
		t.Fatalf("入箱: %v", err)
	}
	jobs, _ := outbox.List(ctx, "", 10)
	if err := outbox.MarkDone(ctx, jobs[0].ID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	if err := outbox.Retry(ctx, jobs[0].ID); err != sidefx.ErrNotFound {
		t.Errorf("Retry(已完成) = %v, want ErrNotFound", err)
	}
	if err := outbox.Retry(ctx, 999999); err != sidefx.ErrNotFound {
		t.Errorf("Retry(不存在) = %v, want ErrNotFound", err)
	}
}

// TestDueSkipsJobsScheduledInTheFuture：退避中的待办不该被下一轮立刻捡起来。
func TestDueSkipsJobsScheduledInTheFuture(t *testing.T) {
	outbox, _ := newOutbox(t)
	ctx := context.Background()

	if err := outbox.EnqueueNotify(ctx, sidefx.EventFiled, hashOne, nil); err != nil {
		t.Fatalf("入箱: %v", err)
	}
	jobs, _ := outbox.List(ctx, "", 10)
	if err := outbox.Reschedule(ctx, jobs[0].ID, "网络抖了", 10*time.Minute); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	due, err := outbox.Due(ctx, 10)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("Due = %+v, want 空（还在退避窗口里）", due)
	}

	pending, err := outbox.List(ctx, sidefx.StatePending, 10)
	if err != nil {
		t.Fatalf("List(pending): %v", err)
	}
	if len(pending) != 1 || pending[0].Attempts != 1 {
		t.Fatalf("pending = %+v, want 一条 attempts=1 的排队待办", pending)
	}
}
