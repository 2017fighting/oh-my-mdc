package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 人工重试的落库面（规格 §6.1 人工重试行 / #31）：
// 两条复位（回 processing 开头 / 回 downloading）与事件附注。
//
// 测试缝与 store_test.go 一致：只走 task.Store 的公开接口 + 真 Postgres，
// 断言用 Detail/Events 的读模型，不自己拼 SQL 读状态（claim 的「已放回」用再抢一次证明）。

func claim(t *testing.T, store *task.Store, infohash string) task.Version {
	t.Helper()
	version, err := store.Claim(context.Background(), infohash, "retry-test")
	if err != nil {
		t.Fatalf("Claim(%s): %v", infohash, err)
	}
	return version
}

// TestResetForRetryRewindsFailedVersionToProcessingStart 钉住 P13-1：
// 非 finished 版本回 processing 开头——状态、错误列、尝试计数、退避时间、租约一次复位干净。
func TestResetForRetryRewindsFailedVersionToProcessingStart(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()

	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	save(t, store, candidate("YwrO3B", "ABP-123", hash))
	if _, err := database.Pool().Exec(ctx,
		`UPDATE task_version
		 SET status = 'failed', error_phase = 'scrape', error_reason = 'DMM 地区阻断',
		     attempts = 3, next_attempt_at = now() + interval '20 minutes'
		 WHERE infohash = $1`, hash); err != nil {
		t.Fatalf("种下失败版本: %v", err)
	}
	claim(t, store, hash)

	reset, err := store.ResetForRetry(ctx, hash, "retry-test", false)
	if err != nil {
		t.Fatalf("ResetForRetry: %v", err)
	}
	if reset.Status != task.StatusProcessing {
		t.Errorf("ResetForRetry 返回状态 = %q, want processing", reset.Status)
	}
	if reset.ForceRescrape {
		t.Error("force_rescrape = true, want false（只点重试不该强制重刮）")
	}

	version := versionOf(detail(t, store, "YwrO3B"), hash)
	if version.Status != task.StatusProcessing {
		t.Errorf("status = %q, want processing", version.Status)
	}
	if version.Attempts != 0 {
		t.Errorf("attempts = %d, want 0（人工重试不继承自动重试预算）", version.Attempts)
	}
	if version.ErrorPhase != "" || version.ErrorReason != "" {
		t.Errorf("error = (%q, %q), want 清空（它们描述的是过去）", version.ErrorPhase, version.ErrorReason)
	}
	if version.AddPending {
		t.Error("add_pending = true, want false（这条版本早就加过种了）")
	}

	// 租约已放回：再抢一次能抢到（抢不到会得到 ErrClaimLost）。
	claim(t, store, hash)
}

// TestResetForRetryIsIdempotent 钉住「幂等」：连点两次得到的还是同一个起点。
func TestResetForRetryIsIdempotent(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()

	hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	save(t, store, candidate("k4EVE4", "SSIS-001", hash))
	if _, err := database.Pool().Exec(ctx,
		`UPDATE task_version SET status = 'failed', error_phase = 'organize', error_reason = '硬链接跨盘（EXDEV）'
		 WHERE infohash = $1`, hash); err != nil {
		t.Fatalf("种下失败版本: %v", err)
	}

	for round := 0; round < 2; round++ {
		claim(t, store, hash)
		if _, err := store.ResetForRetry(ctx, hash, "retry-test", false); err != nil {
			t.Fatalf("第 %d 次 ResetForRetry: %v", round+1, err)
		}
	}

	version := versionOf(detail(t, store, "k4EVE4"), hash)
	if version.Status != task.StatusProcessing || version.Attempts != 0 {
		t.Errorf("version = (%q, attempts=%d), want (processing, 0)", version.Status, version.Attempts)
	}
}

// TestResetForRetryWithForceStaysFlaggedUntilScrapeSucceeds 钉住强制重刮的持久标记：
// force=true 置起 force_rescrape，普通重试不会把它抹掉（标记归取数段清，不归这里）。
func TestResetForRetryWithForceStaysFlaggedUntilScrapeSucceeds(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()

	hash := "cccccccccccccccccccccccccccccccccccccccc"
	save(t, store, candidate("LwE9Me", "SSIS-002", hash))
	if _, err := database.Pool().Exec(ctx,
		`UPDATE task_version SET status = 'finished' WHERE infohash = $1`, hash); err != nil {
		t.Fatalf("把版本拨成已入库: %v", err)
	}

	claim(t, store, hash)
	reset, err := store.ResetForRetry(ctx, hash, "retry-test", true)
	if err != nil {
		t.Fatalf("ResetForRetry(force): %v", err)
	}
	if !reset.ForceRescrape {
		t.Fatal("force_rescrape = false, want true（强制重刮要打破复用判据）")
	}

	claim(t, store, hash)
	if _, err := store.ResetForRetry(ctx, hash, "retry-test", false); err != nil {
		t.Fatalf("第二次 ResetForRetry: %v", err)
	}
	if !versionOf(detail(t, store, "LwE9Me"), hash).ForceRescrape {
		t.Error("force_rescrape = false, want true（标记只由取数成功清掉）")
	}
}

// TestResetForRetryWithoutTheLeaseIsClaimLost 钉住互斥：没拿着租约的复位是零写入，
// 抢输不是失败（规格 §4.4）。
func TestResetForRetryWithoutTheLeaseIsClaimLost(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()

	hash := "dddddddddddddddddddddddddddddddddddddddd"
	save(t, store, candidate("Zw1a2B", "ABP-124", hash))
	if _, err := database.Pool().Exec(ctx,
		`UPDATE task_version SET status = 'failed', error_phase = 'scrape', attempts = 2
		 WHERE infohash = $1`, hash); err != nil {
		t.Fatalf("种下失败版本: %v", err)
	}

	if _, err := store.ResetForRetry(ctx, hash, "retry-test", false); !errors.Is(err, task.ErrClaimLost) {
		t.Fatalf("ResetForRetry 无租约 = %v, want ErrClaimLost", err)
	}

	version := versionOf(detail(t, store, "Zw1a2B"), hash)
	if version.Status != task.StatusFailed || version.Attempts != 2 {
		t.Errorf("version = (%q, attempts=%d), want 原样 (failed, 2)", version.Status, version.Attempts)
	}
}

// TestMarkRetryDownloadingPutsVersionBackOnTheDownloadingLeg 钉住 P13-2 的落库一半：
// 重加种成功后版本回 downloading，add_pending 清掉、清单待删置起、尝试/错误/租约复位。
func TestMarkRetryDownloadingPutsVersionBackOnTheDownloadingLeg(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()

	hash := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	save(t, store, candidate("Ry7t3Y", "SSIS-003", hash))
	if _, err := database.Pool().Exec(ctx,
		`UPDATE task_version
		 SET status = 'failed', error_phase = 'downloading', error_reason = '种子已从 qBittorrent 移除',
		     attempts = 3, next_attempt_at = now() + interval '10 minutes'
		 WHERE infohash = $1`, hash); err != nil {
		t.Fatalf("种下下载失败版本: %v", err)
	}
	claim(t, store, hash)

	if _, err := store.MarkRetryDownloading(ctx, hash, "retry-test", false); err != nil {
		t.Fatalf("MarkRetryDownloading: %v", err)
	}

	version := versionOf(detail(t, store, "Ry7t3Y"), hash)
	if version.Status != task.StatusDownloading {
		t.Errorf("status = %q, want downloading", version.Status)
	}
	if version.AddPending {
		t.Error("add_pending = true, want false（重加种已经做过）")
	}
	if !version.ListRemovalPending {
		t.Error("list_removal_pending = false, want true（加种之后清单项该删）")
	}
	if version.Attempts != 0 || version.ErrorPhase != "" || version.ErrorReason != "" {
		t.Errorf("attempts/error 没复位: %+v", version)
	}

	claim(t, store, hash)
}

// TestAppendEventKeepsStructuredDetail 钉住「对比留痕」的落库面：
// 旧 raw_snapshot 走事件的 detail（jsonb），reason 仍只放人话。
func TestAppendEventKeepsStructuredDetail(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()

	save(t, store, candidate("Ph8r4A", "ABP-125", "ffffffffffffffffffffffffffffffffffffffff"))

	before := json.RawMessage(`{"javdb":{"movie":{"id":"Ph8r4A","title":"旧标题"}}}`)
	if err := store.AppendEvent(ctx, task.Event{
		MovieID:   "Ph8r4A",
		Kind:      task.EventManualRetry,
		FromState: "finished",
		ToState:   "processing",
		Reason:    "强制重刮：对比基准已留痕",
		Detail:    mustJSON(t, map[string]any{"raw_snapshot_before": before}),
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	events, err := store.Events(ctx, "Ph8r4A", 10)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2（创建 + 人工重试）", len(events))
	}
	retryEvent := events[0]
	if retryEvent.Kind != task.EventManualRetry || retryEvent.Reason != "强制重刮：对比基准已留痕" {
		t.Errorf("事件 = %+v, want manual_retry + 人话 reason", retryEvent)
	}

	var detail struct {
		RawSnapshotBefore struct {
			JavDB struct {
				Movie struct {
					Title string `json:"title"`
				} `json:"movie"`
			} `json:"javdb"`
		} `json:"raw_snapshot_before"`
	}
	if err := json.Unmarshal(retryEvent.Detail, &detail); err != nil {
		t.Fatalf("detail 不是 JSON: %v（%s）", err, retryEvent.Detail)
	}
	if detail.RawSnapshotBefore.JavDB.Movie.Title != "旧标题" {
		t.Errorf("detail.raw_snapshot_before.javdb.movie.title = %q, want 旧标题",
			detail.RawSnapshotBefore.JavDB.Movie.Title)
	}
}

// TestEventsWithoutDetailCarryAnEmptyObject 钉住默认值：
// 没有附注的事件仍是一份合法 JSON（面板/前端不必null判断）。
func TestEventsWithoutDetailCarryAnEmptyObject(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()

	save(t, store, candidate("Xx1y2Z", "ABP-126", "9999999999999999999999999999999999999999"))

	events, err := store.Events(ctx, "Xx1y2Z", 10)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) == 0 || string(events[0].Detail) != "{}" {
		t.Fatalf("detail = %s, want {}", events[0].Detail)
	}
}

// TestVersionsOfListsEveryVersionOfOneTask 钉住重试的发现面：一次读回任务的版本列表。
func TestVersionsOfListsEveryVersionOfOneTask(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()

	save(t, store, candidate("YwrO3B", "ABP-123", "1010101010101010101010101010101010101010"))
	save(t, store, candidate("YwrO3B", "ABP-123", "2020202020202020202020202020202020202020"))
	save(t, store, candidate("k4EVE4", "SSIS-001", "3030303030303030303030303030303030303030"))

	versions, err := store.VersionsOf(ctx, "YwrO3B")
	if err != nil {
		t.Fatalf("VersionsOf: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(versions))
	}
	seen := map[string]time.Time{}
	for _, version := range versions {
		if version.Number != "ABP-123" {
			t.Errorf("version.Number = %q, want ABP-123", version.Number)
		}
		seen[version.Infohash] = version.CreatedAt
	}
	if len(seen) != 2 {
		t.Errorf("infohash 集合 = %v, want 两条不同版本", seen)
	}

	if _, err := store.VersionsOf(ctx, "no-such-movie"); !errors.Is(err, task.ErrTaskNotFound) {
		t.Errorf("VersionsOf(未知任务) = %v, want ErrTaskNotFound", err)
	}
}

// TestRawSnapshotIsReadableForTheComparisonBaseline 钉住重刮前的对比基准：
// 强制重刮要在覆盖 raw_snapshot 之前把它原样读出来。
func TestRawSnapshotIsReadableForTheComparisonBaseline(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()

	save(t, store, candidate("YwrO3B", "ABP-123", "4040404040404040404040404040404040404040"))
	if _, err := database.Pool().Exec(ctx,
		`UPDATE task SET raw_snapshot = '{"javdb":{"movie":{"id":"YwrO3B"}},"dmm_content":{"ppvContent":{"title":"旧"}}}'::jsonb
		 WHERE movie_id = 'YwrO3B'`); err != nil {
		t.Fatalf("种下 raw_snapshot: %v", err)
	}

	snapshot, err := store.RawSnapshot(ctx, "YwrO3B")
	if err != nil {
		t.Fatalf("RawSnapshot: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(snapshot, &decoded); err != nil {
		t.Fatalf("raw_snapshot 不是 JSON: %v", err)
	}
	if len(decoded) != 2 {
		t.Errorf("raw_snapshot 源 = %v, want javdb + dmm_content", decoded)
	}

	if _, err := store.RawSnapshot(ctx, "no-such-movie"); !errors.Is(err, task.ErrTaskNotFound) {
		t.Errorf("RawSnapshot(未知任务) = %v, want ErrTaskNotFound", err)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return raw
}

// TestAttentionDropsAStaleStallAlertAfterAReadd 钉住「需人工区与重试贯通」：
// 重新加种之后，上一次的停滞告警不再描述当前事实；再停一次会重新出现。
func TestAttentionDropsAStaleStallAlertAfterAReadd(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()

	hash := "5050505050505050505050505050505050505050"
	save(t, store, candidate("YwrO3B", "ABP-123", hash))

	stall := task.Event{
		MovieID: "YwrO3B", Infohash: hash, Kind: task.EventAlert,
		Phase: string(task.PhaseDownloading), Reason: task.StallReasonPrefix + "：进度 42%，已 90 分钟未动",
	}
	if err := store.AppendEvent(ctx, stall); err != nil {
		t.Fatalf("AppendEvent(停滞): %v", err)
	}
	attention, err := store.Attention(ctx, 10)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	if len(attention.Stalled) != 1 {
		t.Fatalf("stalled = %d, want 1（告过警的下载在需人工区）", len(attention.Stalled))
	}
	// 版本的身份要跟着行一起出来：一部作品两个版本都停住时，人要能分辨是哪一条。
	if attention.Stalled[0].Label != "中文" || attention.Stalled[0].Source != string(task.SourceJavDBMagnet) {
		t.Errorf("stalled[0] = %+v, want label=中文 source=javdb_magnet", attention.Stalled[0])
	}

	// 人工重试重新加种：这条版本从头下载，旧告警不再算数。
	if err := store.AppendEvent(ctx, task.Event{
		MovieID: "YwrO3B", Infohash: hash, Kind: task.EventManualRetry,
		FromState: "failed", ToState: "downloading", Reason: "重新加种（新加种），等下一轮对账确认",
		Detail: mustJSON(t, map[string]any{"action": task.RetryActionReadded}),
	}); err != nil {
		t.Fatalf("AppendEvent(人工重试): %v", err)
	}
	attention, err = store.Attention(ctx, 10)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	if len(attention.Stalled) != 0 {
		t.Errorf("stalled = %d, want 0（重新加种之后的旧停滞告警该退场）", len(attention.Stalled))
	}

	// 再停一次：新告警比重新加种新，重新出现在需人工区。
	stall.Reason = task.StallReasonPrefix + "：进度 10%，已 70 分钟未动"
	if err := store.AppendEvent(ctx, stall); err != nil {
		t.Fatalf("AppendEvent(第二次停滞): %v", err)
	}
	attention, err = store.Attention(ctx, 10)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	if len(attention.Stalled) != 1 {
		t.Errorf("stalled = %d, want 1（新的停滞仍要报）", len(attention.Stalled))
	}
}
