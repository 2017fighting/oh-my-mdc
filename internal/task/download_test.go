package task_test

import (
	"context"
	"errors"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 下载阶段的状态推进（规格 §6.1「→ processing」「失败」「重启」三行）在 store 上的接缝。
// 断言只走公开读模型（Version / DownloadingVersions / Detail / Claim），不查 SQL 细节。

const (
	hashOne   = "1111111111111111111111111111111111111111"
	hashTwo   = "2222222222222222222222222222222222222222"
	hashThree = "3333333333333333333333333333333333333333"
	hashFour  = "4444444444444444444444444444444444444444"
)

// TestVersionReadsOneVersion 钉住 webhook 进门的读法：读得到就读，读不到给哨兵错误。
func TestVersionReadsOneVersion(t *testing.T) {
	store, _ := migrated(t)
	save(t, store, candidate("movie-1", "ABP-123", hashOne))

	version, err := store.Version(context.Background(), hashOne)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version.MovieID != "movie-1" || version.Number != "ABP-123" {
		t.Errorf("version = %+v, want movie-1/ABP-123", version)
	}
	if version.Status != task.StatusDownloading {
		t.Errorf("status = %q, want downloading", version.Status)
	}

	if _, err := store.Version(context.Background(), "ffffffffffffffffffffffffffffffffffffffff"); !errors.Is(err, task.ErrVersionNotFound) {
		t.Errorf("Version(未知) error = %v, want ErrVersionNotFound", err)
	}
}

// TestDownloadingVersionsSkipsPendingAdds 钉住对账的发现面：只列已经加过种、
// 还在下载的版本。add_pending=true 的还没进 qbt，误判成「种子已移除」是致命的。
func TestDownloadingVersionsSkipsPendingAdds(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()

	save(t, store, candidate("movie-1", "ABP-123", hashOne))
	save(t, store, candidate("movie-2", "ABP-124", hashTwo))
	save(t, store, candidate("movie-3", "ABP-125", hashThree))

	// 第二条已经加种（add_pending=false），第三条已入库。
	claimAndAdd(t, store, hashTwo)
	setStatus(t, database, hashThree, task.StatusFinished)

	versions, err := store.DownloadingVersions(ctx)
	if err != nil {
		t.Fatalf("DownloadingVersions: %v", err)
	}
	if len(versions) != 1 || versions[0].Infohash != hashTwo {
		t.Fatalf("DownloadingVersions = %+v, want 只有已加种的 %s", versions, hashTwo)
	}
}

// TestMarkProcessingRequiresTheClaim 钉住「抢输别当失败」：只有拿着租约的人能推进，
// 推进后状态、错误列与租约一起落定。
func TestMarkProcessingRequiresTheClaim(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()
	save(t, store, candidate("movie-1", "ABP-123", hashOne))

	// 先落一条失败原因，推进时要清掉（上一次下载期的残留不该跟着进加工）。
	if _, err := database.Pool().Exec(ctx,
		`UPDATE task_version SET error_reason = '旧原因', error_phase = 'downloading' WHERE infohash = $1`, hashOne); err != nil {
		t.Fatalf("种旧错误: %v", err)
	}

	if _, err := store.Claim(ctx, hashOne, "owner-a"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.MarkProcessing(ctx, hashOne, "owner-b"); !errors.Is(err, task.ErrClaimLost) {
		t.Errorf("别人 MarkProcessing error = %v, want ErrClaimLost", err)
	}
	if got := versionOf(detail(t, store, "movie-1"), hashOne); got.Status != task.StatusDownloading {
		t.Errorf("抢输后 status = %q, want downloading（别人的账不许改）", got.Status)
	}

	if err := store.MarkProcessing(ctx, hashOne, "owner-a"); err != nil {
		t.Fatalf("MarkProcessing: %v", err)
	}
	version := versionOf(detail(t, store, "movie-1"), hashOne)
	if version.Status != task.StatusProcessing {
		t.Errorf("status = %q, want processing", version.Status)
	}
	if version.ErrorPhase != "" || version.ErrorReason != "" {
		t.Errorf("error = %q/%q, want 清空", version.ErrorPhase, version.ErrorReason)
	}

	// 租约已清：别人可以立刻接手加工（§4.4）。
	if _, err := store.Claim(ctx, hashOne, "owner-b"); err != nil {
		t.Errorf("推进后 Claim error = %v, want 租约已释放", err)
	}
}

// TestMarkDownloadFailedRecordsPhaseAndReason 钉住 failed(downloading, reason)。
func TestMarkDownloadFailedRecordsPhaseAndReason(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()
	save(t, store, candidate("movie-1", "ABP-123", hashOne))

	if _, err := store.Claim(ctx, hashOne, "owner-a"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.MarkDownloadFailed(ctx, hashOne, "owner-b", "种子已移除"); !errors.Is(err, task.ErrClaimLost) {
		t.Errorf("别人 MarkDownloadFailed error = %v, want ErrClaimLost", err)
	}

	if err := store.MarkDownloadFailed(ctx, hashOne, "owner-a", "种子已移除"); err != nil {
		t.Fatalf("MarkDownloadFailed: %v", err)
	}
	version := versionOf(detail(t, store, "movie-1"), hashOne)
	if version.Status != task.StatusFailed {
		t.Errorf("status = %q, want failed", version.Status)
	}
	if version.ErrorPhase != task.PhaseDownloading || version.ErrorReason != "种子已移除" {
		t.Errorf("error = %q/%q, want downloading/种子已移除", version.ErrorPhase, version.ErrorReason)
	}

	if _, err := store.Claim(ctx, hashOne, "owner-b"); err != nil {
		t.Errorf("失败后 Claim error = %v, want 租约已释放", err)
	}
}

// TestResetProcessingLeavesOtherStatusesAlone 钉住规格 §6.1 重启行：
// downloading 不动（真实进度在 qbt）、processing 阶段级复位重跑、finished/failed 不动。
func TestResetProcessingLeavesOtherStatusesAlone(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()
	save(t, store, candidate("movie-1", "ABP-123", hashOne))
	save(t, store, candidate("movie-2", "ABP-124", hashTwo))
	save(t, store, candidate("movie-3", "ABP-125", hashThree))
	save(t, store, candidate("movie-4", "ABP-126", hashFour))

	for hash, status := range map[string]task.Status{
		hashOne: task.StatusDownloading, hashTwo: task.StatusProcessing,
		hashThree: task.StatusFinished, hashFour: task.StatusFailed,
	} {
		setStatus(t, database, hash, status)
	}
	// 每条都带着上一进程留下的租约与重试计数。
	for _, hash := range []string{hashOne, hashTwo, hashThree, hashFour} {
		if _, err := database.Pool().Exec(ctx,
			`UPDATE task_version SET attempts = 2, next_attempt_at = now() WHERE infohash = $1`, hash); err != nil {
			t.Fatalf("种 attempts: %v", err)
		}
		if _, err := store.Claim(ctx, hash, "死进程"); err != nil {
			t.Fatalf("Claim(%s): %v", hash, err)
		}
	}
	// processing 那条的租约已过期：复位只接手过期的租约（§4.4 的同一个 10 分钟判据）。
	if _, err := database.Pool().Exec(ctx,
		`UPDATE task_version SET claimed_at = now() - interval '11 minutes' WHERE infohash = $1`, hashTwo); err != nil {
		t.Fatalf("让租约过期: %v", err)
	}

	reset, err := store.ResetProcessing(ctx)
	if err != nil {
		t.Fatalf("ResetProcessing: %v", err)
	}
	if len(reset) != 1 || reset[0].Infohash != hashTwo {
		t.Fatalf("reset = %+v, want 只有 processing 的 %s", reset, hashTwo)
	}

	// processing：状态照旧、重试计数清零、租约清空（下一位立刻能接手）。
	claimed, err := store.Claim(ctx, hashTwo, "新进程")
	if err != nil {
		t.Fatalf("复位后 Claim(processing) error = %v, want 可接手", err)
	}
	if claimed.Status != task.StatusProcessing {
		t.Errorf("复位后 status = %q, want processing（阶段级复位，不降级）", claimed.Status)
	}
	if claimed.Attempts != 0 {
		t.Errorf("复位后 attempts = %d, want 0（重跑不背上一进程的账）", claimed.Attempts)
	}

	// downloading / finished / failed：租约与计数一概不动。
	for _, hash := range []string{hashOne, hashThree, hashFour} {
		if _, err := store.Claim(ctx, hash, "新进程"); !errors.Is(err, task.ErrClaimLost) {
			t.Errorf("Claim(%s) error = %v, want ErrClaimLost（重启不碰这些状态）", hash, err)
		}
	}
}

// claimAndAdd 走一遍「加种成功」的落库：清 add_pending、清租约。
func claimAndAdd(t *testing.T, store *task.Store, infohash string) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.Claim(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("Claim(%s): %v", infohash, err)
	}
	if err := store.MarkAdded(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("MarkAdded(%s): %v", infohash, err)
	}
}
