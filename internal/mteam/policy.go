// 本文件是 M-Team 候选的定选策略（规格 §5.2「种子选择策略」，本票落定、可调）。
//
// 裁决顺序照规格：
//
//	seeders 降序 → 折扣（FREE 优先）→ 体积降序
//
// 三个键全等时按 torrent id 升序做最后裁决：选择必须是确定性的，否则同一部片
// 每轮都可能选到不同的种子，白下载一次种子（那是 M-Team 唯一的瓶颈配额）。
package mteam

import "strings"

// Hit 是搜索结果里的一行 —— 只保留本项目要用的字段。
type Hit struct {
	// ID 是种子在 M-Team 上的 id。它只用来下载（身份是下载后本地算出的 infohash）。
	ID string
	// Name 是种子名（通常以番号开头）。
	Name string
	// SizeBytes 是体积。线上传的是**字符串**，解析失败退化为 0。
	SizeBytes int64
	// Seeders 是做种数。解析失败退化为 0。
	Seeders int
	// Discount 是折扣标记（FREE / PERCENT_50 / …，空串表示没有折扣）。
	Discount string
	// Labels 是 labelsNew 原样。
	//
	// ⚠️ 中字只信它：detail 里那个 hasChineseSubtitle 实测在 labelsNew 明明写着
	// 「中字」时仍然是 false（一个看起来权威、实际不填的字段，规格 §5.2）。
	Labels []string
}

// Label 是这条候选的 best-effort 展示标签：labelsNew 里有「中字」就是「中文」。
func (h Hit) Label() string {
	if h.HasChineseSubtitle() {
		return LabelChinese
	}
	return ""
}

// HasChineseSubtitle 报告 labelsNew 里有没有「中字」。
func (h Hit) HasChineseSubtitle() bool {
	for _, label := range h.Labels {
		if strings.Contains(label, "中字") {
			return true
		}
	}
	return false
}

// SelectHit 按规格顺序挑一条候选。空候选返回 false（调用方据此降级）。
func SelectHit(hits []Hit) (Hit, bool) {
	if len(hits) == 0 {
		return Hit{}, false
	}

	best := hits[0]
	for _, hit := range hits[1:] {
		if better(hit, best) {
			best = hit
		}
	}
	return best, true
}

// better 报告 a 是否应该排在 b 前面（三个键 + id 的确定性裁决）。
func better(a, b Hit) bool {
	if a.Seeders != b.Seeders {
		return a.Seeders > b.Seeders
	}
	if rankA, rankB := discountRank(a.Discount), discountRank(b.Discount); rankA != rankB {
		return rankA < rankB
	}
	if a.SizeBytes != b.SizeBytes {
		return a.SizeBytes > b.SizeBytes
	}
	return a.ID < b.ID
}

// discountRank 把折扣标记排成「越小越划算」。
//
// 认不出的值一律按「没有折扣」处理：猜错优惠力度只会让人多下一份收费种，
// 而把认不出的值当成 FREE 会让人以为免费。
func discountRank(discount string) int {
	switch strings.ToUpper(strings.TrimSpace(discount)) {
	case "FREE", "2XFREE":
		return 0
	case "PERCENT_50", "PERCENT_50_2X":
		return 1
	case "PERCENT_70", "PERCENT_70_2X":
		return 2
	default:
		return 3
	}
}
