package pin

import (
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
)

// TestSelectFollowsTheSpecOrder 钉住规格 §6.1 的定选顺序：
// cnsub 降序 → hd 降序 → 体积降序 → created_at 降序。
func TestSelectFollowsTheSpecOrder(t *testing.T) {
	newer := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)
	older := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		magnets []appapi.Magnet
		want    string
	}{
		{
			name: "中字优先于一切",
			magnets: []appapi.Magnet{
				{Hash: "h1", Cnsub: false, HD: true, SizeMiB: 9999, CreatedAt: newer},
				{Hash: "h2", Cnsub: true, HD: false, SizeMiB: 1, CreatedAt: older},
			},
			want: "h2",
		},
		{
			name: "同为中字时 HD 优先于体积",
			magnets: []appapi.Magnet{
				{Hash: "h1", Cnsub: true, HD: false, SizeMiB: 9999, CreatedAt: newer},
				{Hash: "h2", Cnsub: true, HD: true, SizeMiB: 1, CreatedAt: older},
			},
			want: "h2",
		},
		{
			name: "同为中字 HD 时体积大的优先",
			magnets: []appapi.Magnet{
				{Hash: "h1", Cnsub: true, HD: true, SizeMiB: 1024, CreatedAt: newer},
				{Hash: "h2", Cnsub: true, HD: true, SizeMiB: 2048, CreatedAt: older},
			},
			want: "h2",
		},
		{
			name: "其余相同取更新的",
			magnets: []appapi.Magnet{
				{Hash: "h1", Cnsub: true, HD: true, SizeMiB: 2048, CreatedAt: older},
				{Hash: "h2", Cnsub: true, HD: true, SizeMiB: 2048, CreatedAt: newer},
			},
			want: "h2",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := Select(testCase.magnets)
			if !ok {
				t.Fatal("Select 返回 ok=false，want 选中一条")
			}
			if got.Hash != testCase.want {
				t.Errorf("Select = %s, want %s", got.Hash, testCase.want)
			}
		})
	}
}

// TestSelectIsDeterministicOnFullTies 确认四个键全等时也有稳定答案：
// 否则同一条清单每轮 poll 可能选到不同 infohash，白建版本行。
func TestSelectIsDeterministicOnFullTies(t *testing.T) {
	at := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)
	magnets := []appapi.Magnet{
		{Hash: "bbbb", SizeMiB: 10, CreatedAt: at},
		{Hash: "aaaa", SizeMiB: 10, CreatedAt: at},
	}

	for i := 0; i < 3; i++ {
		got, ok := Select(magnets)
		if !ok {
			t.Fatal("Select 返回 ok=false")
		}
		if got.Hash != "aaaa" {
			t.Errorf("Select = %s, want aaaa（全等时按 hash 升序，稳定且可复现）", got.Hash)
		}
	}
}

// TestSelectEmpty 确认空候选是「没有选择」，不是一条零值候选。
func TestSelectEmpty(t *testing.T) {
	if got, ok := Select(nil); ok {
		t.Errorf("Select(nil) = %+v, true, want false", got)
	}
}
