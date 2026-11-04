package task_test

import (
	"context"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 「需要人工」与「需人工筛选」是两个口径，这里把它们的差异钉住。
//
// 面板的徽章数的是**要动手的事**（一行一个对象：失败版本组、停滞组、驻留副作用）；
// ?state=failed 筛的是**任务**（全部版本都失败的番号，task_state 视图）。一部作品
// 中文版已入库、破解版失败时，它就同时满足「卡里有一行」和「筛选里一条都没有」——
// 这不是 bug，是两个问题；界面必须各自说清口径，才不会被人读成同一个数字。

func TestAttentionCountsFailedVersionsUnderAFinishedTask(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()

	const (
		hashCN    = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
		hashCrack = "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"
	)

	save(t, store, candidate("k4EVE4", "SSIS-001", hashCN))
	crack := candidate("k4EVE4", "SSIS-001", hashCrack)
	crack.Label = "破解"
	save(t, store, crack)

	if _, err := database.Pool().Exec(ctx,
		`UPDATE task_version SET status = 'finished' WHERE infohash = $1`, hashCN); err != nil {
		t.Fatalf("置中文版为已入库: %v", err)
	}
	if _, err := database.Pool().Exec(ctx,
		`UPDATE task_version SET status = 'failed', error_phase = 'scrape', error_reason = 'DMM 超时'
		 WHERE infohash = $1`, hashCrack); err != nil {
		t.Fatalf("置破解版为失败: %v", err)
	}

	// 一行要动手的东西在（失败版本），带番号与判别词。
	attention, err := store.Attention(ctx, 50)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	if len(attention.Failed) != 1 || attention.Failed[0].Infohash != hashCrack {
		t.Fatalf("failed = %+v, want 一条破解版", attention.Failed)
	}
	if attention.Failed[0].Number != "SSIS-001" || attention.Failed[0].Label != "破解" {
		t.Errorf("failed[0] = %+v, want 番号 + 破解（版本的身份要跟着行出来）", attention.Failed[0])
	}
	if attention.FailedTruncated || attention.StalledTruncated {
		t.Errorf("truncated = %v/%v, want false/false", attention.FailedTruncated, attention.StalledTruncated)
	}

	// 但它不是「失败任务」：任务状态跟着已入库的中文版走，筛选与计数都看不到它。
	failedTasks, err := store.List(ctx, task.ListFilter{State: "failed"})
	if err != nil {
		t.Fatalf("List(state=failed): %v", err)
	}
	if len(failedTasks) != 0 {
		t.Errorf("List(state=failed) = %+v, want 空（作品没有整个失败）", failedTasks)
	}

	counts, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Failed != 0 {
		t.Errorf("counts.Failed = %d, want 0：计数是任务级，别把它与「需要人工」的行数混为一谈", counts.Failed)
	}
}

// TestAttentionMarksTruncation 钉住「50+ 而不是 50」：被上限切过的那一半要自报截断，
// 否则队列总量会被界面说成一个它并不知道的数。
func TestAttentionMarksTruncation(t *testing.T) {
	store, database := migrated(t)
	ctx := context.Background()

	hashes := []string{
		"c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3",
		"d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4",
		"e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5",
	}
	for _, hash := range hashes {
		save(t, store, candidate("YwrO3B", "ABP-123", hash))
	}
	if _, err := database.Pool().Exec(ctx,
		`UPDATE task_version SET status = 'failed', error_phase = 'organize', error_reason = '硬链接失败'
		 WHERE movie_id = 'YwrO3B'`); err != nil {
		t.Fatalf("置三条版本为失败: %v", err)
	}

	cut, err := store.Attention(ctx, 2)
	if err != nil {
		t.Fatalf("Attention(2): %v", err)
	}
	if len(cut.Failed) != 2 {
		t.Fatalf("failed = %d, want 2（上限）", len(cut.Failed))
	}
	if !cut.FailedTruncated {
		t.Errorf("FailedTruncated = false, want true：取到 limit+1 说明后面还有")
	}

	exact, err := store.Attention(ctx, 3)
	if err != nil {
		t.Fatalf("Attention(3): %v", err)
	}
	if len(exact.Failed) != 3 {
		t.Fatalf("failed = %d, want 3", len(exact.Failed))
	}
	if exact.FailedTruncated {
		t.Errorf("FailedTruncated = true, want false：恰好 3 条不等于「还有更多」")
	}
}
