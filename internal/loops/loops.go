// Package loops 记三条循环（poll / reconcile / sidefx）的健康位：最近一次跑的时间与成败。
//
// 面板的「循环健康」与规格 §9 的 mdc_loop_* 指标都读它。它是进程内状态而不是数据库
// 状态：循环活在进程里，进程重启本来就该显示「还没跑过」。
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

// Status 是一条循环的健康位。
type Status struct {
	LastRunAt *time.Time
	LastOK    *bool
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

// Record 记一次循环结果（ok=false 也算跑过：面板要能看见失败）。
func (r *Registry) Record(name string, at time.Time, ok bool) {
	if r == nil {
		return
	}
	utc := at.UTC()
	lastOK := ok
	r.mu.Lock()
	r.status[name] = Status{LastRunAt: &utc, LastOK: &lastOK}
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
