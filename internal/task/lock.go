package task

import "sync"

// keyedLocks 是进程内 per-version 互斥锁（规格 §4.4）。
//
// 它只解决「同一进程里两条 goroutine 同时处理同一版本」；跨进程的互斥归数据库里的
// claim 租约。没有等待者时把锁从表里删掉，避免一次 poll 的 key 永久占着内存。
type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	mu   sync.Mutex
	refs int
}

func newKeyedLocks() *keyedLocks {
	return &keyedLocks{locks: make(map[string]*keyedLock)}
}

// lock 拿到 key 对应的锁，返回释放函数（幂等）。
func (k *keyedLocks) lock(key string) func() {
	k.mu.Lock()
	entry, ok := k.locks[key]
	if !ok {
		entry = &keyedLock{}
		k.locks[key] = entry
	}
	entry.refs++
	k.mu.Unlock()

	entry.mu.Lock()

	var once sync.Once
	return func() {
		once.Do(func() {
			entry.mu.Unlock()

			k.mu.Lock()
			entry.refs--
			if entry.refs == 0 {
				delete(k.locks, key)
			}
			k.mu.Unlock()
		})
	}
}
