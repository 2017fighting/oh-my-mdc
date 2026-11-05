// Package loops 记三条循环（poll / reconcile / sidefx）的健康位：最近一次跑的时间与成败、
// 最近一次成功的时间、以及 ok/error 的迭代计数。
//
// 面板的「循环健康」与规格 §9 的 mdc_loop_* 指标都读它。它是进程内状态而不是数据库
// 状态：循环活在进程里，进程重启本来就该显示「还没跑过」。
//
// last_run 与 last_success 分开：连续失败的循环「跑过」但「没成功」，而 LoopStalled
// 告警判的是后者——只记 last_run 会让一个每轮都失败的循环看起来完全健康。
package loops

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 三条循环的名字（规格 §6.2）。
const (
	Poll      = "poll"
	Reconcile = "reconcile"
	Sidefx    = "sidefx"
)

// All 是三条循环的固定顺序：指标与面板都按它列，免得顺序随机。
var All = []string{Poll, Reconcile, Sidefx}

// Iterations 是一条循环的迭代计数（规格 §9 的 result=ok|error）。
//
// 只有真跑了才算：门禁没开、抢锁抢输的轮次既不记 last_run 也不计数（被跳过的
// 不算一次迭代——否则空转会在指标上看起来像健康）。
type Iterations struct {
	OK    uint64
	Error uint64
}

// Status 是一条循环的健康位。
type Status struct {
	LastRunAt *time.Time
	LastOK    *bool
	// LastSuccessAt 是最近一次 ok=true 的时间；从没成功过时为 nil。
	//
	// 它与 LastRunAt 分开存：连续失败的循环「跑过」但「没成功」，
	// LoopStalled 告警判的是后者（规格 §9）。
	LastSuccessAt *time.Time
	Iterations    Iterations
	// RunningSince 非空表示这一轮正在跑（Start 起、Record/Stop 止）。
	//
	// 它同样是进程内状态：循环 panic 会连带进程退出，重启后回到「还没跑过」，
	// 不会留下一个永远在跑的假标记。
	RunningSince *time.Time
}

// Registry 是三条循环健康位的进程内登记处。
type Registry struct {
	mu     sync.RWMutex
	status map[string]Status
}

// NewRegistry 建一个空的登记处。
func NewRegistry() *Registry {
	return &Registry{status: make(map[string]Status, 3)}
}

// Start 标记一条循环开始一轮：抢到锁、真的要跑了才调（与 Iterations 同一口径，
// 门禁没开、抢锁抢输的轮次不调）。结束由 Record（计一轮）或 Stop（不计）收口。
func (r *Registry) Start(name string, at time.Time) {
	if r == nil {
		return
	}
	utc := at.UTC()
	r.mu.Lock()
	status := r.status[name]
	status.RunningSince = &utc
	r.status[name] = status
	r.mu.Unlock()
}

// Stop 清掉 running 标记但不计一轮：门禁拦下的轮次确实「跑过零毫秒」，
// 但它不是一次迭代，面板上也不该挂着「正在跑」。
func (r *Registry) Stop(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	status := r.status[name]
	status.RunningSince = nil
	r.status[name] = status
	r.mu.Unlock()
}

// Record 记一次循环结果（ok=false 也算跑过：面板要能看见失败），并收掉 running 标记。
func (r *Registry) Record(name string, at time.Time, ok bool) {
	if r == nil {
		return
	}
	utc := at.UTC()
	lastOK := ok
	r.mu.Lock()
	status := r.status[name]
	status.RunningSince = nil
	status.LastRunAt = &utc
	status.LastOK = &lastOK
	if ok {
		status.LastSuccessAt = &utc
		status.Iterations.OK++
	} else {
		status.Iterations.Error++
	}
	r.status[name] = status
	r.mu.Unlock()
}

// Health 读一条循环的健康位；没跑过时两个字段都是 nil。
func (r *Registry) Health(name string) Status {
	if r == nil {
		return Status{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status[name]
}

// Guard 抢一条循环的 pg advisory lock，返回释放函数与是否抢到。
//
// 单飞是三条循环共有的不变式（规格 §4.4）：防 Flux 滚动更新时新旧 pod 重叠跑同一轮。
// 它用一条专用连接持有锁，释放时先 unlock 再还连接；释放本身用独立 ctx，
// 进程被终止时也要把锁还回去，别让下一轮空等。
//
// pool 为 nil 时不设锁（测试或单进程场景）：返回 no-op 释放函数与 locked=true。
func Guard(ctx context.Context, pool *pgxpool.Pool, key int64, name string, logger *slog.Logger) (func(), bool, error) {
	if pool == nil {
		return func() {}, true, nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("%s: 取单飞连接: %w", name, err)
	}

	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("%s: 抢 advisory lock: %w", name, err)
	}
	if !locked {
		conn.Release()
		return func() {}, false, nil
	}

	release := func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", key); err != nil {
			logger.Error(name+": 释放 advisory lock 失败", "err", err)
		}
		conn.Release()
	}
	return release, true, nil
}
