package task_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：task.Store 的公开接口（落库/占用/读模型），跑在真 Postgres 上。
// 聚合状态只能由 task_state 视图给出，所以用 List/Detail 断言，而不是自己拼 SQL。

func migrated(t *testing.T) (*task.Store, *db.DB) {
	t.Helper()
	database := dbtest.Migrated(t)
	seedList(t, database)
	return task.NewStore(database.Pool()), database
}

func seedList(t *testing.T, database *db.DB) {
	t.Helper()
	if _, err := database.Pool().Exec(context.Background(),
		`INSERT INTO list_registry (list_id, purpose, name)
		 VALUES ('wanted', 'want_watch', '想看清单')
		 ON CONFLICT (list_id) DO NOTHING`); err != nil {
		t.Fatalf("种清单登记失败: %v", err)
	}
}

func candidate(movieID, number, infohash string) task.Candidate {
	return task.Candidate{
		MovieID:  movieID,
		Number:   number,
		ListID:   "wanted",
		Infohash: infohash,
		Source:   task.SourceJavDBMagnet,
		Delivery: task.DeliveryMagnet,
		Label:    "中文",
		Payload:  "magnet:?xt=urn:btih:" + infohash,
	}
}

func save(t *testing.T, store *task.Store, cand task.Candidate) {
	t.Helper()
	inserted, err := store.SaveCandidate(context.Background(), cand)
	if err != nil {
		t.Fatalf("SaveCandidate(%s): %v", cand.Infohash, err)
	}
	if !inserted {
		t.Fatalf("SaveCandidate(%s) 没插入：前置条件坏了", cand.Infohash)
	}
}

func detail(t *testing.T, store *task.Store, movieID string) task.TaskRow {
	t.Helper()
	row, err := store.Detail(context.Background(), movieID, 50)
	if err != nil {
		t.Fatalf("Detail(%s): %v", movieID, err)
	}
	return row
}

func versionOf(row task.TaskRow, infohash string) task.Version {
	for _, version := range row.Versions {
		if version.Infohash == infohash {
			return version
		}
	}
	return task.Version{}
}

func setStatus(t *testing.T, database *db.DB, infohash string, status task.Status) {
	t.Helper()
	if _, err := database.Pool().Exec(context.Background(),
		`UPDATE task_version SET status = $1 WHERE infohash = $2`, string(status), infohash); err != nil {
		t.Fatalf("设置版本 %s 状态失败: %v", infohash, err)
	}
}

// TestSaveCandidateIsIdempotentPerInfohash 钉住去重判据（规格 §6.1）：
// 同 infohash 第二次落库什么都不写，任务只有一条版本。
func TestSaveCandidateIsIdempotentPerInfohash(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()

	save(t, store, candidate("YwrO3B", "ABP-123", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	inserted, err := store.SaveCandidate(ctx, candidate("YwrO3B", "ABP-123", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	if err != nil {
		t.Fatalf("第二次 SaveCandidate: %v", err)
	}
	if inserted {
		t.Fatal("同 infohash 第二次落库报告 inserted=true：去重失效")
	}

	row := detail(t, store, "YwrO3B")
	if len(row.Versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(row.Versions))
	}
	version := row.Versions[0]
	if !version.AddPending {
		t.Error("add_pending = false, want true（先落库再加种）")
	}
	if version.Label != "中文" {
		t.Errorf("label = %q, want 中文", version.Label)
	}
	if row.State != string(task.StatusDownloading) {
		t.Errorf("state = %q, want downloading", row.State)
	}
	if len(row.Events) != 1 || row.Events[0].Kind != task.EventStateChange {
		t.Errorf("events = %+v, want 一条 state_change", row.Events)
	}
}

// TestSaveCandidateRollsBackTaskWhenInfohashBelongsToAnotherMovie 钉住全局唯一身份：
// infohash 冲突时连新任务行一起回滚，不留下没有版本的任务（NULL 状态）。
func TestSaveCandidateRollsBackTaskWhenInfohashBelongsToAnotherMovie(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	save(t, store, candidate("YwrO3B", "ABP-123", hash))

	inserted, err := store.SaveCandidate(ctx, candidate("k4EVE4", "SSIS-001", hash))
	if err != nil {
		t.Fatalf("冲突的 SaveCandidate: %v", err)
	}
	if inserted {
		t.Fatal("同 infohash 落给另一部作品时报告 inserted=true")
	}

	if _, err := store.Detail(ctx, "k4EVE4", 10); !errors.Is(err, task.ErrTaskNotFound) {
		t.Fatalf("Detail(k4EVE4) err = %v, want ErrTaskNotFound（任务行应一起回滚）", err)
	}
	if _, err := store.Detail(ctx, "YwrO3B", 10); err != nil {
		t.Fatalf("原任务不该受影响: %v", err)
	}
}

// TestTaskStateAggregatesByPriority 钉住 §3 的聚合优先级（P10-1/P10-3）：
// 任一 processing → processing；否则任一 downloading；否则任一 finished；全败 → failed。
// 每次改版本状态后立即重读，确认视图没有缓存。
func TestTaskStateAggregatesByPriority(t *testing.T) {
	store, database := migrated(t)

	const (
		first  = "1111111111111111111111111111111111111111"
		second = "2222222222222222222222222222222222222222"
	)
	save(t, store, candidate("YwrO3B", "ABP-123", first))
	save(t, store, candidate("YwrO3B", "ABP-123", second))

	cases := []struct {
		name   string
		first  task.Status
		second task.Status
		want   task.Status
	}{
		{"下载 + 失败 → 下载中", task.StatusDownloading, task.StatusFailed, task.StatusDownloading},
		{"加工压过下载", task.StatusDownloading, task.StatusProcessing, task.StatusProcessing},
		{"已入库压过失败", task.StatusFinished, task.StatusFailed, task.StatusFinished},
		{"全失败才是失败", task.StatusFailed, task.StatusFailed, task.StatusFailed},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			setStatus(t, database, first, testCase.first)
			setStatus(t, database, second, testCase.second)
			row := detail(t, store, "YwrO3B")
			if row.State != string(testCase.want) {
				t.Errorf("state = %q, want %q", row.State, testCase.want)
			}
		})
	}

	// 两条版本各自独立：改一条不碰另一条（P10-2 的结构前提）。
	setStatus(t, database, first, task.StatusFailed)
	setStatus(t, database, second, task.StatusFinished)
	row := detail(t, store, "YwrO3B")
	if row.State != string(task.StatusFinished) {
		t.Errorf("state = %q, want finished", row.State)
	}
	if got := versionOf(row, first).Status; got != task.StatusFailed {
		t.Errorf("first.status = %q, want failed（互不干扰）", got)
	}
}

// TestClaimIsExclusiveUntilLeaseExpires 钉住 §4.4 的占用语义：
// 未过期时抢输是 ErrClaimLost；过期后可被接手；释放后可再抢。
func TestClaimIsExclusiveUntilLeaseExpires(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	save(t, store, candidate("YwrO3B", "ABP-123", hash))

	if _, err := store.Claim(ctx, hash, "owner-a"); err != nil {
		t.Fatalf("第一次 Claim: %v", err)
	}
	if _, err := store.Claim(ctx, hash, "owner-b"); !errors.Is(err, task.ErrClaimLost) {
		t.Fatalf("第二次 Claim err = %v, want ErrClaimLost", err)
	}

	// 把租约改成 11 分钟前：过期。
	if _, err := database.Pool().Exec(ctx,
		`UPDATE task_version SET claimed_at = now() - interval '11 minutes' WHERE infohash = $1`, hash,
	); err != nil {
		t.Fatalf("模拟租约过期: %v", err)
	}
	if _, err := store.Claim(ctx, hash, "owner-b"); err != nil {
		t.Fatalf("过期后 Claim: %v", err)
	}

	if err := store.Release(ctx, hash, "owner-b"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := store.Claim(ctx, hash, "owner-c"); err != nil {
		t.Fatalf("释放后 Claim: %v", err)
	}

	// 不是自己的租约不许释放。
	if err := store.Release(ctx, hash, "owner-c"); err != nil {
		t.Fatalf("Release 自己: %v", err)
	}
	if _, err := store.Claim(ctx, hash, "owner-a"); err != nil {
		t.Fatalf("释放后再 Claim: %v", err)
	}
}

// TestClaimPendingWalksPendingVersions 钉住「本轮没活」的出口：
// 每条 pending 只被领一次，领完返回 ErrNoPendingTask。
func TestClaimPendingWalksPendingVersions(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()
	save(t, store, candidate("YwrO3B", "ABP-123", "1111111111111111111111111111111111111111"))
	save(t, store, candidate("k4EVE4", "ABP-124", "2222222222222222222222222222222222222222"))

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		version, err := store.ClaimPending(ctx, "owner")
		if err != nil {
			t.Fatalf("第 %d 次 ClaimPending: %v", i+1, err)
		}
		if seen[version.Infohash] {
			t.Fatalf("ClaimPending 重复领到 %s", version.Infohash)
		}
		seen[version.Infohash] = true
		if version.Number == "" {
			t.Errorf("version.Number 为空：claim 没带上任务番号")
		}
	}

	if _, err := store.ClaimPending(ctx, "owner"); !errors.Is(err, task.ErrNoPendingTask) {
		t.Fatalf("领完后 ClaimPending err = %v, want ErrNoPendingTask", err)
	}
}

// TestMarkAddedClearsPendingAndArmsListRemoval 钉住 ADR-0004 的成功落库：
// add_pending 清掉、清单项待删置起、租约释放；租约丢了不算成功。
func TestMarkAddedClearsPendingAndArmsListRemoval(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	save(t, store, candidate("YwrO3B", "ABP-123", hash))

	if _, err := store.Claim(ctx, hash, "owner"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.MarkAdded(ctx, hash, "owner"); err != nil {
		t.Fatalf("MarkAdded: %v", err)
	}

	version := versionOf(detail(t, store, "YwrO3B"), hash)
	if version.AddPending {
		t.Error("add_pending = true, want false")
	}
	if !version.ListRemovalPending {
		t.Error("list_removal_pending = false, want true（置清单项待删）")
	}

	if err := store.MarkAdded(ctx, hash, "owner"); !errors.Is(err, task.ErrClaimLost) {
		t.Fatalf("无租约时 MarkAdded err = %v, want ErrClaimLost", err)
	}
}

// TestListFiltersAndCounts 钉住面板读模型（规格 §8.4）：
// state/q 筛选、版本摘要、统计卡四个数。
func TestListFiltersAndCounts(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()
	save(t, store, candidate("YwrO3B", "ABP-123", "1111111111111111111111111111111111111111"))
	save(t, store, candidate("k4EVE4", "SSIS-001", "2222222222222222222222222222222222222222"))
	setStatus(t, database, "2222222222222222222222222222222222222222", task.StatusFinished)

	all, err := store.List(ctx, task.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List 全部 = %d, want 2", len(all))
	}
	for _, row := range all {
		if len(row.Versions) != 1 {
			t.Errorf("%s versions = %d, want 1", row.Number, len(row.Versions))
		}
	}

	finished, err := store.List(ctx, task.ListFilter{State: "finished"})
	if err != nil {
		t.Fatalf("List(finished): %v", err)
	}
	if len(finished) != 1 || finished[0].MovieID != "k4EVE4" {
		t.Fatalf("List(finished) = %+v, want k4EVE4", finished)
	}

	byNumber, err := store.List(ctx, task.ListFilter{Query: "ssis"})
	if err != nil {
		t.Fatalf("List(q): %v", err)
	}
	if len(byNumber) != 1 || byNumber[0].Number != "SSIS-001" {
		t.Fatalf("List(q=ssis) = %+v, want SSIS-001（番号大小写不敏感）", byNumber)
	}

	wildcard, err := store.List(ctx, task.ListFilter{Query: "%"})
	if err != nil {
		t.Fatalf("List(q=%%): %v", err)
	}
	if len(wildcard) != 0 {
		t.Fatalf("List(q=%%) = %d, want 0：LIKE 元字符应当按字面量搜索", len(wildcard))
	}

	paged, err := store.List(ctx, task.ListFilter{Page: 2, PageSize: 1})
	if err != nil {
		t.Fatalf("List(page 2): %v", err)
	}
	if len(paged) != 1 {
		t.Fatalf("List(page 2, size 1) = %d, want 1", len(paged))
	}

	counts, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Downloading != 1 || counts.Finished != 1 || counts.Failed != 0 {
		t.Errorf("counts = %+v, want downloading=1 finished=1 failed=0", counts)
	}

	// Count 与 List 同一个筛选：面板靠它说「共 N 条」并关掉最后一页的下一页。
	for _, probe := range []struct {
		name   string
		filter task.ListFilter
		want   int
	}{
		{"全部", task.ListFilter{}, 2},
		{"state=finished", task.ListFilter{State: "finished"}, 1},
		{"q=ssis", task.ListFilter{Query: "ssis"}, 1},
		{"q=%", task.ListFilter{Query: "%"}, 0},
	} {
		total, err := store.Count(ctx, probe.filter)
		if err != nil {
			t.Fatalf("Count(%s): %v", probe.name, err)
		}
		if total != probe.want {
			t.Errorf("Count(%s) = %d, want %d", probe.name, total, probe.want)
		}
	}
}

// TestDetailReportsNotFound 确认抽屉对不存在的任务回一个可识别的错。
func TestDetailReportsNotFound(t *testing.T) {
	store, _ := migrated(t)
	if _, err := store.Detail(context.Background(), "nope", 10); !errors.Is(err, task.ErrTaskNotFound) {
		t.Fatalf("Detail(不存在) err = %v, want ErrTaskNotFound", err)
	}
}

// TestEventsAreAppendOnlyAndNewestFirst 确认事件时间线按新到旧返回。
func TestEventsAreAppendOnlyAndNewestFirst(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	save(t, store, candidate("YwrO3B", "ABP-123", hash))

	if err := store.AppendEvent(ctx, task.Event{
		MovieID: "YwrO3B", Infohash: hash, Kind: task.EventAlert,
		Reason: "加种失败：qbt 不可达", At: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	events, err := store.Events(ctx, "YwrO3B", 10)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Kind != task.EventAlert {
		t.Errorf("最新事件 kind = %q, want alert", events[0].Kind)
	}
	if events[1].Kind != task.EventStateChange {
		t.Errorf("最早事件 kind = %q, want state_change", events[1].Kind)
	}
}
