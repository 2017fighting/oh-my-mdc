package processing

import (
	"testing"
	"time"
)

// 测试缝：加工失败的退避节奏（规格 §6.1「失败」行：指数退避）。
//
// 只钉这一条曲线：两段（取数 #27 / 整理 #29）共用它，所以它必须只在这里定义一次。

func TestBackoffDoublesAndCaps(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, RetryBase}, // 0 与 1 同档：第一次失败等一个基数
		{1, RetryBase},
		{2, 2 * RetryBase},
		{3, 4 * RetryBase},
		{4, 8 * RetryBase},
		{5, 16 * RetryBase},
		{6, RetryCap}, // 32m 封顶到 30m
		{100, RetryCap},
	}
	for _, c := range cases {
		if got := Backoff(c.attempts); got != c.want {
			t.Errorf("Backoff(%d) = %v, want %v", c.attempts, got, c.want)
		}
	}
}
