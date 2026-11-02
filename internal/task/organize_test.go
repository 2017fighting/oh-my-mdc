package task_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：整理段（#29）要用的落库动作——失败记账、终态、目录归属与分片账。
// 断言走公开方法 + Detail/VersionFiles，不自己拼 SQL 读状态。

// TestRecordProcessingFailureRetriesThenLandsFailed 钉住「三次耗尽」：
// 前两次只记账（仍 processing + 退避），第三次落 failed(phase, reason)。
func TestRecordProcessingFailureRetriesThenLandsFailed(t *testing.T) {
	store, _ := metadataFixture(t)
	infohash := "1111111111111111111111111111111111111111"
	processingVersion(t, store, "YwrO3B", "ABP-123", infohash)

	at := time.Now().UTC()
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := store.Claim(context.Background(), infohash, "owner-organize"); err != nil {
			t.Fatalf("Claim（第 %d 次）: %v", attempt, err)
		}
		failure, err := store.RecordProcessingFailure(context.Background(), infohash, "owner-organize",
			task.PhaseOrganize, "硬链失败", at, time.Minute, 3)
		if err != nil {
			t.Fatalf("RecordProcessingFailure（第 %d 次）: %v", attempt, err)
		}
		if failure.Attempts != attempt {
			t.Errorf("attempts = %d, want %d", failure.Attempts, attempt)
		}
		if failure.Exhausted {
			t.Fatalf("第 %d 次就耗尽了，want 仍可重试", attempt)
		}

		version := versionOf(detail(t, store, "YwrO3B"), infohash)
		if version.Status != task.StatusProcessing {
			t.Fatalf("status = %q, want processing（未耗尽不换状态）", version.Status)
		}
		if version.ErrorPhase != "" || version.ErrorReason != "" {
			t.Fatalf("未耗尽就写了错误列: %q / %q", version.ErrorPhase, version.ErrorReason)
		}
	}

	if _, err := store.Claim(context.Background(), infohash, "owner-organize"); err != nil {
		t.Fatalf("Claim（第三次）: %v", err)
	}
	failure, err := store.RecordProcessingFailure(context.Background(), infohash, "owner-organize",
		task.PhaseOrganize, "硬链失败", at, time.Minute, 3)
	if err != nil {
		t.Fatalf("RecordProcessingFailure（第三次）: %v", err)
	}
	if !failure.Exhausted || failure.Attempts != 3 {
		t.Fatalf("failure = %+v, want 第三次耗尽", failure)
	}

	version := versionOf(detail(t, store, "YwrO3B"), infohash)
	if version.Status != task.StatusFailed {
		t.Errorf("status = %q, want failed", version.Status)
	}
	if version.ErrorPhase != task.PhaseOrganize || version.ErrorReason != "硬链失败" {
		t.Errorf("failed(%q, %q), want (organize, 硬链失败)", version.ErrorPhase, version.ErrorReason)
	}

	// 驻留：落地后既不在加工发现面里，也不再自动重试。
	versions, err := store.ProcessingVersions(context.Background())
	if err != nil {
		t.Fatalf("ProcessingVersions: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("failed 版本仍被列进加工发现面: %+v", versions)
	}

	// 抢输不是失败：别人的租约不归我写。
	if _, err := store.RecordProcessingFailure(context.Background(), infohash, "someone-else",
		task.PhaseOrganize, "x", at, time.Minute, 3); !errors.Is(err, task.ErrClaimLost) {
		t.Errorf("err = %v, want ErrClaimLost", err)
	}
}

// TestMarkProcessingFailedSkipsTheRetryBudget 钉住永久错：碰撞/EXDEV 不进退避，
// 一次就落 failed(phase, reason) 进需人工区。
func TestMarkProcessingFailedSkipsTheRetryBudget(t *testing.T) {
	store, _ := metadataFixture(t)
	infohash := "2222222222222222222222222222222222222222"
	processingVersion(t, store, "Zx9K1p", "ABP-124", infohash)
	if _, err := store.Claim(context.Background(), infohash, "owner-organize"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	reason := "跨设备：下载目录与媒体库不在同一挂载，请换挂载（不降级 copy）"
	if err := store.MarkProcessingFailed(context.Background(), infohash, "owner-organize", task.PhaseOrganize, reason); err != nil {
		t.Fatalf("MarkProcessingFailed: %v", err)
	}

	version := versionOf(detail(t, store, "Zx9K1p"), infohash)
	if version.Status != task.StatusFailed || version.ErrorPhase != task.PhaseOrganize {
		t.Fatalf("version = %+v, want failed(organize)", version)
	}
	if version.ErrorReason != reason {
		t.Errorf("reason = %q, want %q", version.ErrorReason, reason)
	}
	if version.Attempts != 0 {
		// 永久错不烧自动重试预算：attempts 不该被加账。
		t.Errorf("attempts = %d, want 0", version.Attempts)
	}

	if err := store.MarkProcessingFailed(context.Background(), infohash, "someone-else", task.PhaseOrganize, "x"); !errors.Is(err, task.ErrClaimLost) {
		t.Errorf("err = %v, want ErrClaimLost", err)
	}
}

// TestMarkFiledFinishesTheVersionAndClearsPastErrors 是入库成功的终态。
func TestMarkFiledFinishesTheVersionAndClearsPastErrors(t *testing.T) {
	store, _ := metadataFixture(t)
	infohash := "3333333333333333333333333333333333333333"
	processingVersion(t, store, "3fG7hJ", "ABP-125", infohash)

	// 先记一次失败（留下 attempts 与错误措辞），再成功：过去的事该被清掉。
	at := time.Now().UTC()
	if _, err := store.Claim(context.Background(), infohash, "owner-organize"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := store.RecordProcessingFailure(context.Background(), infohash, "owner-organize",
		task.PhaseOrganize, "第一次不成就退避", at, time.Minute, 3); err != nil {
		t.Fatalf("RecordProcessingFailure: %v", err)
	}
	if _, err := store.Claim(context.Background(), infohash, "owner-organize"); err != nil {
		t.Fatalf("Claim（第二次）: %v", err)
	}
	if err := store.MarkFiled(context.Background(), infohash, "owner-organize"); err != nil {
		t.Fatalf("MarkFiled: %v", err)
	}

	version := versionOf(detail(t, store, "3fG7hJ"), infohash)
	if version.Status != task.StatusFinished {
		t.Errorf("status = %q, want finished", version.Status)
	}
	if version.ErrorPhase != "" || version.ErrorReason != "" {
		t.Errorf("终态仍带错误列: %q / %q", version.ErrorPhase, version.ErrorReason)
	}
	if version.Attempts != 0 {
		t.Errorf("attempts = %d, want 0（成功复位重试计数）", version.Attempts)
	}

	if err := store.MarkFiled(context.Background(), infohash, "owner-organize"); !errors.Is(err, task.ErrClaimLost) {
		t.Errorf("重复 MarkFiled err = %v, want ErrClaimLost", err)
	}
}

// TestHasFinishedVersionDrivesTheNewVersionWording 是「新版本入库」文案的判据。
func TestHasFinishedVersionDrivesTheNewVersionWording(t *testing.T) {
	store, _ := metadataFixture(t)
	first := "4444444444444444444444444444444444444444"
	processingVersion(t, store, "YwrO3B", "ABP-126", first)

	exists, err := store.HasFinishedVersion(context.Background(), "YwrO3B", first)
	if err != nil {
		t.Fatalf("HasFinishedVersion: %v", err)
	}
	if exists {
		t.Fatal("还没有任何入库版本就被判成新版本")
	}

	if _, err := store.Claim(context.Background(), first, "owner-organize"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.MarkFiled(context.Background(), first, "owner-organize"); err != nil {
		t.Fatalf("MarkFiled: %v", err)
	}

	// 第二条版本（v2 中字）到来：历史里有入库版本 → 新版本文案。
	second := "5555555555555555555555555555555555555555"
	processingVersion(t, store, "YwrO3B", "ABP-126", second)
	exists, err = store.HasFinishedVersion(context.Background(), "YwrO3B", second)
	if err != nil {
		t.Fatalf("HasFinishedVersion: %v", err)
	}
	if !exists {
		t.Error("已有入库版本，want new_version=true")
	}

	// 对第一条版本自己问，答案仍是「没有别的入库版本」。
	exists, err = store.HasFinishedVersion(context.Background(), "YwrO3B", first)
	if err != nil {
		t.Fatalf("HasFinishedVersion: %v", err)
	}
	if exists {
		t.Error("只算别的版本，自己那条不该算进来")
	}
}

// TestTaskOwningNumberPicksTheFirstArrival 钉住同番号异 movie_id 的归属：
// 先到者进目录，后到者拿得到先到者的 movie_id（据此失败驻留）。
func TestTaskOwningNumberPicksTheFirstArrival(t *testing.T) {
	store, database := metadataFixture(t)
	first := "6666666666666666666666666666666666666666"
	save(t, store, candidate("aaaaaa", "ABP-127", first))
	save(t, store, candidate("bbbbbb", "ABP-127", "7777777777777777777777777777777777777777"))

	// 同一毫秒也可能落库：把先后固定下来，判据才是确定的。
	if _, err := database.Pool().Exec(context.Background(),
		`UPDATE task SET created_at = CASE movie_id WHEN 'aaaaaa' THEN now() - interval '1 minute' ELSE now() END`); err != nil {
		t.Fatalf("调整 created_at: %v", err)
	}

	earlier, err := store.TaskOwningNumber(context.Background(), "ABP-127", "bbbbbb")
	if err != nil {
		t.Fatalf("TaskOwningNumber: %v", err)
	}
	if earlier != "aaaaaa" {
		t.Errorf("earlier = %q, want aaaaaa", earlier)
	}

	earlier, err = store.TaskOwningNumber(context.Background(), "ABP-127", "aaaaaa")
	if err != nil {
		t.Fatalf("TaskOwningNumber: %v", err)
	}
	if earlier != "" {
		t.Errorf("先到者不该被判成后到，got %q", earlier)
	}

	// 反向兜底：另一个任务（哪怕晚到）已经入库 → 目录归它，别再写 movie.nfo 盖掉它。
	if _, err := database.Pool().Exec(context.Background(),
		`INSERT INTO task_version (infohash, movie_id, source, delivery, status)
		 VALUES ('9999999999999999999999999999999999999999', 'bbbbbb', 'javdb_magnet', 'magnet', 'finished')`,
	); err != nil {
		t.Fatalf("种已入库版本: %v", err)
	}
	owner, err := store.TaskOwningNumber(context.Background(), "ABP-127", "aaaaaa")
	if err != nil {
		t.Fatalf("TaskOwningNumber: %v", err)
	}
	if owner != "bbbbbb" {
		t.Errorf("owner = %q, want bbbbbb（已入库者拥有目录）", owner)
	}
}

// TestSaveVersionFilesReplacesTheSet 钉住分片账：重跑覆盖而不是累积。
func TestSaveVersionFilesReplacesTheSet(t *testing.T) {
	store, _ := metadataFixture(t)
	infohash := "8888888888888888888888888888888888888888"
	processingVersion(t, store, "YwrO3B", "ABP-128", infohash)

	files := []task.VersionFile{
		{RelPath: "ABP-128-cd2.mkv", SizeBytes: 2048, PartIndex: 2},
		{RelPath: "ABP-128-cd1.mkv", SizeBytes: 1024, PartIndex: 1, IsPrimary: true},
	}
	if err := store.SaveVersionFiles(context.Background(), infohash, files); err != nil {
		t.Fatalf("SaveVersionFiles: %v", err)
	}

	stored, err := store.VersionFiles(context.Background(), infohash)
	if err != nil {
		t.Fatalf("VersionFiles: %v", err)
	}
	if len(stored) != 2 || stored[0].RelPath != "ABP-128-cd1.mkv" || !stored[0].IsPrimary {
		t.Fatalf("stored = %+v, want 两条且 cd1 在前、is_primary", stored)
	}

	if err := store.SaveVersionFiles(context.Background(), infohash, files[:1]); err != nil {
		t.Fatalf("SaveVersionFiles（覆盖）: %v", err)
	}
	stored, err = store.VersionFiles(context.Background(), infohash)
	if err != nil {
		t.Fatalf("VersionFiles: %v", err)
	}
	if len(stored) != 1 || stored[0].RelPath != "ABP-128-cd2.mkv" {
		t.Fatalf("stored = %+v, want 只剩覆盖后的那一条", stored)
	}
}
