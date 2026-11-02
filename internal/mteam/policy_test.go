package mteam

import "testing"

// TestSelectHitFollowsTheSpecOrder 钉住规格 §5.2 的定选顺序：
// seeders 降序 → 折扣（FREE 优先）→ 体积降序。
func TestSelectHitFollowsTheSpecOrder(t *testing.T) {
	cases := []struct {
		name string
		hits []Hit
		want string
	}{
		{
			name: "做种多的优先于折扣",
			hits: []Hit{
				{ID: "free", Seeders: 3, Discount: "FREE", SizeBytes: 1},
				{ID: "popular", Seeders: 42, Discount: "", SizeBytes: 1},
			},
			want: "popular",
		},
		{
			name: "同为做种时 FREE 优先于五折",
			hits: []Hit{
				{ID: "half", Seeders: 10, Discount: "PERCENT_50", SizeBytes: 1},
				{ID: "free", Seeders: 10, Discount: "FREE", SizeBytes: 1},
			},
			want: "free",
		},
		{
			name: "认不出的折扣按没有折扣算",
			hits: []Hit{
				{ID: "unknown", Seeders: 10, Discount: "MYSTERY", SizeBytes: 1},
				{ID: "half", Seeders: 10, Discount: "PERCENT_50", SizeBytes: 1},
			},
			want: "half",
		},
		{
			name: "做种与折扣都相同时体积大的优先",
			hits: []Hit{
				{ID: "small", Seeders: 10, Discount: "FREE", SizeBytes: 1024},
				{ID: "big", Seeders: 10, Discount: "FREE", SizeBytes: 8192},
			},
			want: "big",
		},
		{
			name: "全等时按 id 升序（选择必须确定性）",
			hits: []Hit{
				{ID: "b", Seeders: 10, SizeBytes: 1024},
				{ID: "a", Seeders: 10, SizeBytes: 1024},
			},
			want: "a",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := SelectHit(testCase.hits)
			if !ok {
				t.Fatal("SelectHit 返回 ok=false，want 选中一条")
			}
			if got.ID != testCase.want {
				t.Errorf("SelectHit = %s, want %s", got.ID, testCase.want)
			}
		})
	}
}

// TestSelectHitIsDeterministic 确认同一批候选多次选择结果一致：
// 否则同一部片每轮可能选到不同种子，白下载一份（那是 M-Team 的瓶颈配额）。
func TestSelectHitIsDeterministic(t *testing.T) {
	hits := []Hit{
		{ID: "bbbb", Seeders: 5, SizeBytes: 10},
		{ID: "aaaa", Seeders: 5, SizeBytes: 10},
	}
	for i := 0; i < 3; i++ {
		got, ok := SelectHit(hits)
		if !ok || got.ID != "aaaa" {
			t.Fatalf("第 %d 次 SelectHit = %s, ok=%v, want aaaa", i+1, got.ID, ok)
		}
	}
}

// TestSelectHitEmpty 确认空候选是「没有选择」，不是一条零值候选。
func TestSelectHitEmpty(t *testing.T) {
	if got, ok := SelectHit(nil); ok {
		t.Errorf("SelectHit(nil) = %+v, true, want false", got)
	}
}

// TestHitLabelUsesLabelsNew 钉住中字判据：**只**认 labelsNew 里的「中字」。
//
// 线上 detail 里那个 hasChineseSubtitle 实测在 labelsNew 写着「中字」时仍是 false，
// 因此它不参与判定（规格 §5.2）。
func TestHitLabelUsesLabelsNew(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
		want   string
	}{
		{name: "labelsNew 里有中字", labels: []string{"中字", "4k"}, want: LabelChinese},
		{name: "中字带后缀的标签", labels: []string{"中字版"}, want: LabelChinese},
		{name: "没有中字", labels: []string{"4k"}, want: ""},
		{name: "空标签", labels: nil, want: ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			hit := Hit{Labels: testCase.labels}
			if got := hit.Label(); got != testCase.want {
				t.Errorf("Label = %q, want %q", got, testCase.want)
			}
		})
	}
}
