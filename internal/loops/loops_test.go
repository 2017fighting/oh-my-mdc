package loops_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
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

// TestAllListsTheFourLoops 钉住指标与面板共用的固定顺序。
//
// subscription（女优订阅，#47）排在最后：它是第四个循环，独立 advisory lock 与节拍，
// 但面板与 /metrics 的 loop 标签必须一条不缺。
func TestAllListsTheFourLoops(t *testing.T) {
	want := []string{loops.Poll, loops.Reconcile, loops.Sidefx, loops.Subscription}
	if len(loops.All) != len(want) {
		t.Fatalf("All = %v, want %v", loops.All, want)
	}
	for i := range want {
		if loops.All[i] != want[i] {
			t.Errorf("All[%d] = %q, want %q", i, loops.All[i], want[i])
		}
	}
}

// TestGuardDoesNotStarveThePool 钉住 2026-11-18 的生产事故：守锁连接不许从池里取。
//
// 事故的形状：pgxpool 默认池容量是 max(4, NumCPU)，4 核节点算出 4；四个循环各拿一条
// 池内连接抢 advisory lock 并整轮握着，池就此归零 → /readyz 的 Ping 永远等不到连接，
// pod 卡在启动探针上（`db_unavailable: context deadline exceeded`）。三条循环的时代
// 只是恰好剩一条能用。池容量钉成 1 是同一件事的最小化：只要守锁还在池里，这里必挂。
func TestGuardDoesNotStarveThePool(t *testing.T) {
	_, dsn := dbtest.Open(t)

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("解析 DSN: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("建池: %v", err)
	}
	t.Cleanup(pool.Close)

	const key int64 = 9029001
	release, locked, err := loops.Guard(context.Background(), pool, key, "test", nil)
	if err != nil || !locked {
		t.Fatalf("Guard = (%v, %v), want 抢到且无错", locked, err)
	}

	// 失败路径上也要把锁/连接还回去：旧实现下池被占满，pgxpool.Close 会一直等
	// 还没归还的连接，测试会挂死而不是失败——挂死比红更难查，所以这里做一层幂等释放。
	released := false
	releaseOnce := func() {
		if !released {
			released = true
			release()
		}
	}
	t.Cleanup(releaseOnce)

	// 守锁期间池仍须可用：这就是事故当时拿不到的那一条连接。
	acquireCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := pool.Acquire(acquireCtx)
	if err != nil {
		t.Fatalf("守锁期间取不到池内连接（池被守锁连接占满了）: %v", err)
	}
	conn.Release()

	releaseOnce()

	// 释放之后锁真的还回去了：同一个 key 能再抢到。
	release2, locked2, err := loops.Guard(context.Background(), pool, key, "test", nil)
	if err != nil || !locked2 {
		t.Fatalf("释放后再抢 = (%v, %v), want 抢到且无错", locked2, err)
	}
	release2()
}
