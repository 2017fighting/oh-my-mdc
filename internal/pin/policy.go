// Package pin 是 JavDB 磁力候选的定选策略（规格 §6.1「定源」行）。
//
// 它回答一个问题：JavDB 对一部作品给回多条磁力时，先下哪一条。裁决顺序照规格：
//
//	cnsub 降序 → hd 降序 → 体积降序 → created_at 降序
//
// 四个键全等时按 infohash 升序做最后裁决：选择必须是确定性的，否则同一轮清单
// 每跑一次都可能选到不同的 hash，白建版本行、白查 M-Team。
//
// 这是从 javdb_rss 移植的策略种子（规格 §2）：将来要调顺序，只改这里。
package pin

import (
	"sort"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
)

// Select 按规格顺序挑一条候选。空候选返回 false（不落任何行是调用方的处置）。
func Select(magnets []appapi.Magnet) (appapi.Magnet, bool) {
	if len(magnets) == 0 {
		return appapi.Magnet{}, false
	}

	ranked := make([]appapi.Magnet, len(magnets))
	copy(ranked, magnets)

	sort.SliceStable(ranked, func(i, j int) bool {
		left, right := ranked[i], ranked[j]
		if left.Cnsub != right.Cnsub {
			return left.Cnsub
		}
		if left.HD != right.HD {
			return left.HD
		}
		if left.SizeMiB != right.SizeMiB {
			return left.SizeMiB > right.SizeMiB
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		return left.Hash < right.Hash
	})

	return ranked[0], true
}
