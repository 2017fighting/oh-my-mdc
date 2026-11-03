package metrics_test

// 这条测试要真 Postgres：驻留那族指标的判据是「与库里的行数一致」，
// 用假实现测只能证明渲染，证明不了数据来源。没有 MDC_TEST_DATABASE_URL 时 dbtest 跳过。

import (
	"context"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/metrics"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
)

func TestStuckGaugeMatchesResidentRows(t *testing.T) {
	database := dbtest.Migrated(t)
	ctx := context.Background()

	seed := []struct {
		kind  string
		query string
		args  []any
	}{
		// 两条驻留的 notify：done_at 与 next_attempt_at 都 NULL。
		{kind: "notify", query: `INSERT INTO side_effect (kind, target_key, next_attempt_at) VALUES ($1, 'filed:aaa', NULL)`, args: []any{"notify"}},
		{kind: "notify", query: `INSERT INTO side_effect (kind, target_key, next_attempt_at) VALUES ($1, 'filed:bbb', NULL)`, args: []any{"notify"}},
		// 一条驻留的 list_removal。
		{kind: "list_removal", query: `INSERT INTO side_effect (kind, target_key, next_attempt_at) VALUES ($1, 'movie:ccc', NULL)`, args: []any{"list_removal"}},
		// 在排队（有 next_attempt_at）：不是驻留。
		{kind: "notify", query: `INSERT INTO side_effect (kind, target_key, next_attempt_at) VALUES ($1, 'filed:ddd', now())`, args: []any{"notify"}},
		// 已完成：不是驻留。
		{kind: "jellyfin_refresh", query: `INSERT INTO side_effect (kind, target_key, next_attempt_at, done_at) VALUES ($1, 'dir:eee', now(), now())`, args: []any{"jellyfin_refresh"}},
	}
	for _, row := range seed {
		if _, err := database.Pool().Exec(ctx, row.query, row.args...); err != nil {
			t.Fatalf("种副作用 %s: %v", row.kind, err)
		}
	}

	m := metrics.New(nil, sidefx.New(database.Pool()), nil)
	families := render(t, m)

	want := map[string]float64{"notify": 2, "list_removal": 1, "jellyfin_refresh": 0}
	for kind, count := range want {
		if got := sampleValue(t, families, "mdc_side_effect_stuck", map[string]string{"kind": kind}); got != count {
			t.Errorf("mdc_side_effect_stuck{kind=%q} = %v, want %v", kind, got, count)
		}
	}

	// 再与「驻留 = done_at IS NULL AND next_attempt_at IS NULL」那条 SQL 对一次账。
	var resident float64
	if err := database.Pool().QueryRow(ctx,
		`SELECT count(*) FROM side_effect WHERE done_at IS NULL AND next_attempt_at IS NULL`,
	).Scan(&resident); err != nil {
		t.Fatalf("数驻留行: %v", err)
	}
	var rendered float64
	for _, kind := range []string{"notify", "list_removal", "jellyfin_refresh"} {
		rendered += sampleValue(t, families, "mdc_side_effect_stuck", map[string]string{"kind": kind})
	}
	if rendered != resident {
		t.Errorf("指标求和 = %v, 库里的驻留行数 = %v", rendered, resident)
	}
}
