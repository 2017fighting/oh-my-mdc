package server_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/loops"
	"github.com/2017fighting/oh-my-mdc/internal/metrics"
	"github.com/2017fighting/oh-my-mdc/internal/server"
)

// errDatabaseDown 是 /readyz 的失败因由（/healthz 与 /metrics 都不该被它拖下水）。
var errDatabaseDown = errors.New("连接被拒")

// TestMetricsEndpointIsWired 钉住 /metrics 挂在前面的那一步：
// handler 出自指标包（Content-Type 与文本格式），不是 SPA 壳页，也不是 404。
func TestMetricsEndpointIsWired(t *testing.T) {
	registry := loops.NewRegistry()
	m := metrics.New(registry, nil, nil)
	m.RecordUpstream(metrics.UpstreamJavDB, metrics.ResultOK)

	h := newHandler(t, server.Options{Metrics: m})
	rec := get(t, h, "/metrics")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain（Prometheus 文本格式）", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"mdc_loop_last_success_unix_seconds{loop=\"poll\"}",
		"mdc_loop_iterations_total{loop=\"sidefx\",result=\"error\"}",
		"mdc_upstream_requests_total{upstream=\"javdb\",result=\"ok\"} 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics 输出缺少 %q\n%s", want, body)
		}
	}
}

// TestMetricsEndpointSaysSoWhenUnwired 钉住空位：没接线时 503，而不是空 200。
func TestMetricsEndpointSaysSoWhenUnwired(t *testing.T) {
	h := newHandler(t, server.Options{})
	rec := get(t, h, "/metrics")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeBody(t, rec)
	code, _ := errorOf(t, body)
	if code != "metrics_unavailable" {
		t.Errorf("error.code = %q, want metrics_unavailable", code)
	}
}

// TestMetricsDoesNotFoldLivenessOrReadiness 钉住探针回归（规格 §9）：
// /metrics 接线与否都不影响 /healthz（只看进程）与 /readyz（只看 Postgres）。
func TestMetricsDoesNotFoldLivenessOrReadiness(t *testing.T) {
	h := newHandler(t, server.Options{
		DB:      pinger{err: errDatabaseDown},
		Metrics: metrics.New(nil, nil, nil),
	})

	if rec := get(t, h, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d, want 200", rec.Code)
	}
	if rec := get(t, h, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d, want 503（库挂了）", rec.Code)
	}
	if rec := get(t, h, "/metrics"); rec.Code != http.StatusOK {
		t.Errorf("/metrics = %d, want 200（库挂了也要能抓）", rec.Code)
	}
}
