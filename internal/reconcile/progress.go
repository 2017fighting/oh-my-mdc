package reconcile

import (
	"sync"
	"time"
)

// progressTracker 是停滞判定的进程内历史：每条版本最近一次观测到的 progress 与起点时刻。
//
// 为什么历史在内存里：它只回答「从什么时候起没动过」，重启后重新观察一轮就够
// （downloading 的版本重启后本来就要按 qbt 的真实进度重判）。
// 而「一轮停滞只告警一次」不在这里判：那条判据查 task_event（HasAlert，按重新加种分集），
// 重启后依然成立。
type progressTracker struct {
	mu      sync.Mutex
	entries map[string]progressEntry
}

type progressEntry struct {
	progress float64
	since    time.Time
}

func newProgressTracker() *progressTracker {
	return &progressTracker{entries: make(map[string]progressEntry)}
}

// observe 记一次进度观测，返回（这一轮是否已达到停滞阈值，已停滞多久）。
//
// 进度一变（前进或后退，例如重新校验）就是新的起点：停滞只看「一动不动」。
func (t *progressTracker) observe(infohash string, progress float64, at time.Time) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	entry, ok := t.entries[infohash]
	if !ok || progress != entry.progress || at.Before(entry.since) {
		t.entries[infohash] = progressEntry{progress: progress, since: at}
		return false, 0
	}

	elapsed := at.Sub(entry.since)
	return elapsed >= StallAfter, elapsed
}

// forget 丢掉一条版本的进度历史（它已经离开下载阶段）。
func (t *progressTracker) forget(infohash string) {
	t.mu.Lock()
	delete(t.entries, infohash)
	t.mu.Unlock()
}
