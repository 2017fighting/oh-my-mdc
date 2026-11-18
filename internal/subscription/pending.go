package subscription

import "sync"

// Pending 是「回看窗口里已发现、被每轮限量留在门外」的现数（规格 §6.4「节流与去重」、
// §9 的 mdc_subscription_pending、§8.4 面板订阅区）。
//
// 它是**上一轮的事实**，不是库里的状态：订阅循环每轮末尾整体置数，下一轮重新算一遍。
// 因此不落库也是刻意的——落库只会多一份会撒谎的旧数（一个窗口里还有余量的老数，
// 在余量走完之后仍然读得出来）；重启后第一个轮次几秒内就会算出真值，中间读 0 是
// 「还没跑过」，与「真的没有了」在面板上是同一句话（两种情况都不显示这一句）。
//
// 与 loops.Registry 同一路数：进程内的现读数，面板（Service → Row.DeferredCount）
// 与指标（metrics.Registry）都从它出发。nil 是合法的 no-op（测试或未装配）。
type Pending struct {
	mu    sync.Mutex
	bySub map[int64]int
	total int
}

// NewPending 造一份空的现数账。
func NewPending() *Pending {
	return &Pending{bySub: make(map[int64]int)}
}

// Set 用一轮的结果整体替换。
//
// 是替换不是累加：每轮的窗口都从头重算，累加出来的数没有任何含义。
func (p *Pending) Set(bySub map[int64]int) {
	if p == nil {
		return
	}
	total := 0
	counts := make(map[int64]int, len(bySub))
	for id, count := range bySub {
		if count <= 0 {
			continue
		}
		counts[id] = count
		total += count
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.bySub = counts
	p.total = total
}

// Of 读一条订阅的现数（没被限量挡住就是 0）。
func (p *Pending) Of(id int64) int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bySub[id]
}

// Total 读全场的现数：它就是 mdc_subscription_pending。
func (p *Pending) Total() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.total
}
