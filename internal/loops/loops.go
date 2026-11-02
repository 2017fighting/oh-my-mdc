// Package loops 记三条循环（poll / reconcile / sidefx）的健康位：最近一次跑的时间与成败。
//
// 面板的「循环健康」与规格 §9 的 mdc_loop_* 指标都读它。它是进程内状态而不是数据库
// 状态：循环活在进程里，进程重启本来就该显示「还没跑过」。
package loops

import (
	"sync"
	"time"
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
