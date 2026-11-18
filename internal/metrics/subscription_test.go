package metrics_test

import (
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/2017fighting/oh-my-mdc/internal/metrics"
)

// 测试缝：`mdc_subscription_skipped_total{reason}`（规格 §9）。
//
// 三路 reason **恒列满**（没有命中也是 0）：这条指标是「判据还活着吗」的探针——
// 长期全场只有 long_form 就说明共演与精選綜合那两条可能已失效（§9 的用途一行）。
// 缺样本的 0 与被计数的 0 在 PromQL 里不是同一件事。

// TestSubscriptionSkippedCounterHasThreeReasons 钉住三路 reason 的名字与恒列满。
func TestSubscriptionSkippedCounterHasThreeReasons(t *testing.T) {
	registry := metrics.New(nil, nil, nil, nil)

	families := render(t, registry)
	family, ok := families["mdc_subscription_skipped_total"]
	if !ok {
		t.Fatal("缺少指标族 mdc_subscription_skipped_total")
	}
	if family.GetType() != dto.MetricType_COUNTER {
		t.Errorf("类型 = %v, want counter", family.GetType())
	}
	if family.GetHelp() == "" {
		t.Error("缺 HELP 行")
	}
	if got := len(family.Metric); got != 3 {
		t.Fatalf("样本数 = %d, want 3（三路 reason）", got)
	}
	// 三条判据的名字来自 ADR-0017 与迁移 00016 的 CHECK，一个字母都不许漂。
	for _, reason := range []string{"not_single", "compilation", "long_form"} {
		if got := sampleValue(t, families, "mdc_subscription_skipped_total", map[string]string{"reason": reason}); got != 0 {
			t.Errorf("reason=%s = %v, want 0（没命中也是 0，不是缺样本）", reason, got)
		}
	}
}

// TestSubscriptionSkippedCountsPerReason 钉住计数落在对应那一路：
// 「长期只有 long_form」这条告警判据的前提是三路各数各的。
func TestSubscriptionSkippedCountsPerReason(t *testing.T) {
	registry := metrics.New(nil, nil, nil, nil)
	registry.RecordSubscriptionSkipped(metrics.ReasonCompilation)
	registry.RecordSubscriptionSkipped(metrics.ReasonLongForm)
	registry.RecordSubscriptionSkipped(metrics.ReasonLongForm)

	families := render(t, registry)
	for reason, want := range map[string]float64{
		"not_single":  0,
		"compilation": 1,
		"long_form":   2,
	} {
		if got := sampleValue(t, families, "mdc_subscription_skipped_total", map[string]string{"reason": reason}); got != want {
			t.Errorf("reason=%s = %v, want %v", reason, got, want)
		}
	}
}

// TestNilSkipRecorderIsInert 是「没装配就不记」的那一半：nil 登记处不许 panic
// （测试与未装配场景都靠它）。
func TestNilSkipRecorderIsInert(t *testing.T) {
	var registry *metrics.Registry
	metrics.ObserveSubscriptionSkipped(registry, metrics.ReasonNotSingle)
}
