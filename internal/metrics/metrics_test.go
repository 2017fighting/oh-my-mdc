package metrics_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/2017fighting/oh-my-mdc/internal/loops"
	"github.com/2017fighting/oh-my-mdc/internal/metrics"
)

// fakeStuck 是 StuckSource 的测试替身：真实实现（sidefx.Outbox）要真 Postgres，
// 而指标包只关心「按 kind 的那几个数」，不关心它们怎么来的。
type fakeStuck map[string]int

func (f fakeStuck) StuckByKind(context.Context) (map[string]int, error) { return f, nil }

type failingStuck struct{}

func (failingStuck) StuckByKind(context.Context) (map[string]int, error) {
	return nil, errors.New("库不通")
}

// render 把指标写成文本并解析回 Prometheus 的模型。
//
// 解析本身就是格式校验：解析器不认的文本（坏标签、坏转义、缺 TYPE）会直接失败。
func render(t *testing.T, registry *metrics.Registry) map[string]*dto.MetricFamily {
	t.Helper()

	var buf bytes.Buffer
	registry.Write(context.Background(), &buf)

	// 本包的指标名与标签都是 legacy 形态（字母数字下划线），按老口径校验。
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("输出不是合法的 Prometheus 文本格式: %v\n---\n%s", err, buf.String())
	}
	return families
}

func sampleValue(t *testing.T, families map[string]*dto.MetricFamily, name string, labels map[string]string) float64 {
	t.Helper()

	family, ok := families[name]
	if !ok {
		t.Fatalf("指标族 %s 不在输出里", name)
	}
	for _, metric := range family.Metric {
		if !labelsMatch(metric, labels) {
			continue
		}
		switch {
		case metric.Counter != nil:
			return metric.Counter.GetValue()
		case metric.Gauge != nil:
			return metric.Gauge.GetValue()
		default:
			t.Fatalf("%s 的样本既不是 counter 也不是 gauge", name)
		}
	}
	t.Fatalf("%s 里没有标签为 %v 的样本", name, labels)
	return 0
}

func labelsMatch(metric *dto.Metric, want map[string]string) bool {
	got := make(map[string]string, len(metric.Label))
	for _, pair := range metric.Label {
		got[pair.GetName()] = pair.GetValue()
	}
	if len(got) != len(want) {
		return false
	}
	for name, value := range want {
		if got[name] != value {
			return false
		}
	}
	return true
}

// TestWriteRendersAllFourFamilies 钉住规格 §9 的四族指标：名字、类型、标签取值。
//
// 三循环与七上游的样本必须**恒列满**（没跑过/没打过也是 0），否则
// `increase(mdc_upstream_requests_total{result="permanent"}[15m]) > 0` 这类表达式
// 在无流量时没有系列可算。
func TestWriteRendersAllFourFamilies(t *testing.T) {
	registry := loops.NewRegistry()
	okAt := time.Now().Add(-2 * time.Minute)
	registry.Record(loops.Poll, okAt, true)
	registry.Record(loops.Reconcile, okAt, false)

	m := metrics.New(registry, fakeStuck{"notify": 2, "list_removal": 0, "jellyfin_refresh": 1}, nil)
	m.RecordUpstream(metrics.UpstreamJavDB, metrics.ResultPermanent)
	m.RecordUpstream(metrics.UpstreamTelegram, metrics.ResultOK)
	m.RecordUpstream(metrics.UpstreamTelegram, metrics.ResultOK)

	families := render(t, m)

	for name, wantType := range map[string]dto.MetricType{
		"mdc_loop_last_success_unix_seconds": dto.MetricType_GAUGE,
		"mdc_loop_iterations_total":          dto.MetricType_COUNTER,
		"mdc_upstream_requests_total":        dto.MetricType_COUNTER,
		"mdc_side_effect_stuck":              dto.MetricType_GAUGE,
	} {
		family, ok := families[name]
		if !ok {
			t.Fatalf("缺少指标族 %s", name)
		}
		if family.GetType() != wantType {
			t.Errorf("%s 的类型 = %v, want %v", name, family.GetType(), wantType)
		}
		if family.GetHelp() == "" {
			t.Errorf("%s 缺 HELP 行", name)
		}
	}

	// 三循环 × 一条时间 + 两条迭代计数。
	if got := len(families["mdc_loop_last_success_unix_seconds"].Metric); got != 3 {
		t.Errorf("last_success 样本数 = %d, want 3（poll/reconcile/sidefx）", got)
	}
	if got := len(families["mdc_loop_iterations_total"].Metric); got != 6 {
		t.Errorf("iterations 样本数 = %d, want 6（三循环 × ok/error）", got)
	}
	if got := len(families["mdc_upstream_requests_total"].Metric); got != 21 {
		t.Errorf("upstream 样本数 = %d, want 21（七上游 × 三结局）", got)
	}

	if got := sampleValue(t, families, "mdc_loop_last_success_unix_seconds", map[string]string{"loop": "poll"}); got != float64(okAt.Unix()) {
		t.Errorf("poll last_success = %v, want %v", got, okAt.Unix())
	}
	if got := sampleValue(t, families, "mdc_loop_iterations_total", map[string]string{"loop": "poll", "result": "ok"}); got != 1 {
		t.Errorf("poll ok iterations = %v, want 1", got)
	}
	if got := sampleValue(t, families, "mdc_loop_iterations_total", map[string]string{"loop": "reconcile", "result": "error"}); got != 1 {
		t.Errorf("reconcile error iterations = %v, want 1", got)
	}
	if got := sampleValue(t, families, "mdc_upstream_requests_total", map[string]string{"upstream": "javdb", "result": "permanent"}); got != 1 {
		t.Errorf("javdb permanent = %v, want 1", got)
	}
	if got := sampleValue(t, families, "mdc_upstream_requests_total", map[string]string{"upstream": "telegram", "result": "ok"}); got != 2 {
		t.Errorf("telegram ok = %v, want 2", got)
	}
	if got := sampleValue(t, families, "mdc_upstream_requests_total", map[string]string{"upstream": "openai", "result": "transient"}); got != 0 {
		t.Errorf("openai transient = %v, want 0（没打过也是 0，不是缺样本）", got)
	}
	if got := sampleValue(t, families, "mdc_side_effect_stuck", map[string]string{"kind": "notify"}); got != 2 {
		t.Errorf("stuck notify = %v, want 2", got)
	}
	if got := sampleValue(t, families, "mdc_side_effect_stuck", map[string]string{"kind": "list_removal"}); got != 0 {
		t.Errorf("stuck list_removal = %v, want 0", got)
	}
}

// TestLoopStalledThresholdsAreDerivable 钉住 LoopStalled 的三种阈值在本地可复现：
// poll 40 分钟、reconcile 15 分钟、sidefx 10 分钟（规格 §9 的 VMRule）。
func TestLoopStalledThresholdsAreDerivable(t *testing.T) {
	now := time.Now()
	registry := loops.NewRegistry()
	registry.Record(loops.Poll, now.Add(-41*time.Minute), true)      // 停滞
	registry.Record(loops.Reconcile, now.Add(-14*time.Minute), true) // 未到 15 分钟
	registry.Record(loops.Sidefx, now.Add(-11*time.Minute), true)    // 上一次成功
	registry.Record(loops.Sidefx, now.Add(-time.Minute), false)      // 最近一次是失败（不刷新 last_success）

	m := metrics.New(registry, nil, nil)
	families := render(t, m)

	cases := []struct {
		loop      string
		threshold time.Duration
		stalled   bool
	}{
		{loop: loops.Poll, threshold: 40 * time.Minute, stalled: true},
		{loop: loops.Reconcile, threshold: 15 * time.Minute, stalled: false},
		// sidefx 的 last_success 还停在 11 分钟前的那次成功（失败不刷新它）。
		{loop: loops.Sidefx, threshold: 10 * time.Minute, stalled: true},
	}
	for _, tc := range cases {
		lastSuccess := int64(sampleValue(t, families, "mdc_loop_last_success_unix_seconds", map[string]string{"loop": tc.loop}))
		stalled := now.Unix()-lastSuccess > int64(tc.threshold.Seconds())
		if stalled != tc.stalled {
			t.Errorf("%s: time()-last_success > %s 判为 %v, want %v", tc.loop, tc.threshold, stalled, tc.stalled)
		}
	}
}

// TestLoopNeverSucceededUsesStartBaseline 钉住「从没成功过」的取值：
// 不是 0（那会让 time()-0 立刻变成停滞告警），而是进程启动时间。
func TestLoopNeverSucceededUsesStartBaseline(t *testing.T) {
	m := metrics.New(loops.NewRegistry(), nil, nil)
	families := render(t, m)

	started := sampleValue(t, families, "mdc_loop_last_success_unix_seconds", map[string]string{"loop": loops.Poll})
	if drift := float64(time.Now().Unix()) - started; drift < 0 || drift > 5 {
		t.Errorf("从没成功过的 poll last_success = %v，应约等于启动时间（差 %v 秒）", started, drift)
	}
}

// TestUpstreamCredentialsInvalidThresholdIsDerivable 钉住第二条 VMRule 的数据前提：
// 平时 permanent 恒 0（increase>0 不成立），凭据失效类失败后立刻 > 0。
func TestUpstreamCredentialsInvalidThresholdIsDerivable(t *testing.T) {
	m := metrics.New(nil, nil, nil)

	families := render(t, m)
	for _, upstream := range metrics.Upstreams {
		if got := sampleValue(t, families, "mdc_upstream_requests_total", map[string]string{"upstream": upstream, "result": "permanent"}); got != 0 {
			t.Fatalf("%s 的 permanent 初始值 = %v, want 0", upstream, got)
		}
	}
	if got := sampleValue(t, families, "mdc_upstream_requests_total", map[string]string{"upstream": "telegram", "result": "permanent"}); got > 0 {
		t.Fatalf("没有失败时 permanent = %v，不该触发告警", got)
	}

	m.RecordUpstream(metrics.UpstreamTelegram, metrics.ResultPermanent)

	families = render(t, m)
	if got := sampleValue(t, families, "mdc_upstream_requests_total", map[string]string{"upstream": "telegram", "result": "permanent"}); got != 1 {
		t.Errorf("一次凭据失效后 permanent = %v, want 1（increase>0 成立）", got)
	}
}

// TestStuckFamilyNeedsTheDatabase 钉住「读库失败只让驻留那一族缺席」：
// /metrics 不折叠数据库，循环与上游照常出数。
func TestStuckFamilyNeedsTheDatabase(t *testing.T) {
	m := metrics.New(loops.NewRegistry(), failingStuck{}, nil)
	families := render(t, m)

	if _, ok := families["mdc_side_effect_stuck"]; ok {
		t.Error("库不通时不该输出 mdc_side_effect_stuck（0 会被读成「没有驻留」）")
	}
	for _, name := range []string{"mdc_loop_last_success_unix_seconds", "mdc_loop_iterations_total", "mdc_upstream_requests_total"} {
		if _, ok := families[name]; !ok {
			t.Errorf("库不通时 %s 仍应输出", name)
		}
	}
}

// TestHandlerServesPrometheusContentType 钉住端点本身：200 + 正式文本格式的 Content-Type。
func TestHandlerServesPrometheusContentType(t *testing.T) {
	m := metrics.New(loops.NewRegistry(), fakeStuck{"notify": 0}, nil)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	if _, err := parser.TextToMetricFamilies(rec.Body); err != nil {
		t.Errorf("端点输出不是合法的 Prometheus 文本格式: %v\n%s", err, rec.Body.String())
	}
}

// TestNilRegistryIsInert 钉住 nil 的 no-op 语义：测试与「还没接线」都不该 panic。
func TestNilRegistryIsInert(t *testing.T) {
	var m *metrics.Registry
	m.RecordUpstream(metrics.UpstreamJavDB, metrics.ResultOK)

	var buf bytes.Buffer
	m.Write(context.Background(), &buf)
	if buf.Len() != 0 {
		t.Errorf("nil 登记处写出了 %q", buf.String())
	}
}
