package loops_test

import (
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/loops"
)

// TestRecordSeparatesLastRunFromLastSuccess 钉住规格 §9 的两条循环指标读的是同一份登记处：
// last_run 会跟着失败刷新，last_success 只在成功时前进——LoopStalled 判的是后者。
func TestRecordSeparatesLastRunFromLastSuccess(t *testing.T) {
	registry := loops.NewRegistry()
	successAt := time.Date(2024, 3, 2, 1, 2, 3, 0, time.UTC)
	failedAt := successAt.Add(5 * time.Minute)

	registry.Record(loops.Poll, successAt, true)
	registry.Record(loops.Poll, failedAt, false)

	health := registry.Health(loops.Poll)
	if health.LastRunAt == nil || !health.LastRunAt.Equal(failedAt) {
		t.Errorf("LastRunAt = %v, want %v", health.LastRunAt, failedAt)
	}
	if health.LastOK == nil || *health.LastOK {
		t.Errorf("LastOK = %v, want false", health.LastOK)
	}
	if health.LastSuccessAt == nil || !health.LastSuccessAt.Equal(successAt) {
		t.Errorf("LastSuccessAt = %v, want %v（失败不刷新最后一次成功）", health.LastSuccessAt, successAt)
	}
	if health.Iterations.OK != 1 || health.Iterations.Error != 1 {
		t.Errorf("Iterations = %+v, want ok=1 error=1", health.Iterations)
	}
}

// TestHealthOfLoopThatNeverRan 钉住「还没跑过」的语义：三个字段都空，不是零时间。
func TestHealthOfLoopThatNeverRan(t *testing.T) {
	registry := loops.NewRegistry()

	health := registry.Health(loops.Reconcile)
	if health.LastRunAt != nil || health.LastOK != nil || health.LastSuccessAt != nil {
		t.Errorf("没跑过的循环 = %+v, want 全空", health)
	}
	if health.Iterations.OK != 0 || health.Iterations.Error != 0 {
		t.Errorf("没跑过的循环计数 = %+v, want 0", health.Iterations)
	}
}

// TestRunningWindowStartsAndStops 钉住「正在跑」的生命周期：Start 起亮、
// Record 计一轮并收口；门禁拦下的轮次走 Stop——不计数也不留假标记。
func TestRunningWindowStartsAndStops(t *testing.T) {
	registry := loops.NewRegistry()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	registry.Start(loops.Reconcile, now)
	running := registry.Health(loops.Reconcile)
	if running.RunningSince == nil || !running.RunningSince.Equal(now) {
		t.Errorf("Start 后 RunningSince = %v, want %v", running.RunningSince, now)
	}
	if running.LastRunAt != nil || running.Iterations.OK != 0 {
		t.Errorf("Start 不该顺手计一轮: %+v", running)
	}

	registry.Record(loops.Reconcile, now.Add(3*time.Second), true)
	done := registry.Health(loops.Reconcile)
	if done.RunningSince != nil {
		t.Errorf("Record 后 RunningSince = %v, want nil", done.RunningSince)
	}
	if done.LastRunAt == nil || done.Iterations.OK != 1 {
		t.Errorf("Record 该计一轮: %+v", done)
	}

	// 门禁轮次：Start 后被 Stop，不留「正在跑」也不计数。
	registry.Start(loops.Reconcile, now.Add(time.Minute))
	registry.Stop(loops.Reconcile)
	stopped := registry.Health(loops.Reconcile)
	if stopped.RunningSince != nil {
		t.Errorf("Stop 后 RunningSince = %v, want nil", stopped.RunningSince)
	}
	if stopped.Iterations.OK != 1 || stopped.LastRunAt == nil {
		t.Errorf("Stop 不该动计数与上轮结果: %+v", stopped)
	}
}

// TestRecordOnNilRegistryIsInert：引擎没接登记处时（测试场景）记账不该 panic。
func TestRecordOnNilRegistryIsInert(t *testing.T) {
	var registry *loops.Registry
	registry.Record(loops.Sidefx, time.Now(), true)

	if health := registry.Health(loops.Sidefx); health.LastRunAt != nil {
		t.Errorf("nil 登记处读出了 %+v", health)
	}
}

// TestAllListsTheThreeLoops 钉住指标与面板共用的固定顺序。
func TestAllListsTheThreeLoops(t *testing.T) {
	want := []string{loops.Poll, loops.Reconcile, loops.Sidefx}
	if len(loops.All) != len(want) {
		t.Fatalf("All = %v, want %v", loops.All, want)
	}
	for i := range want {
		if loops.All[i] != want[i] {
			t.Errorf("All[%d] = %q, want %q", i, loops.All[i], want[i])
		}
	}
}
