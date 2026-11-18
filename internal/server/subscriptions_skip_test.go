package server_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/metrics"
	"github.com/2017fighting/oh-my-mdc/internal/server"
	"github.com/2017fighting/oh-my-mdc/internal/subscription"
)

// 订阅跳过表的 HTTP 契约（规格 §8.4，GLOSSARY「跳过 / 收下」）。
//
// 断言只看 HTTP 面：路径、body 形状、状态码与稳定机器码，以及接缝收到的是不是原样值。
// 跳过表的幂等与「收下走同一 ingest」在 subscription 包的接缝上测。

// TestSubscriptionSkipsListsTheRecentOnes 钉住 §8.4 的行形状：
// `{id, movie_id, number, title, release_date, reason, skipped_at}`。
func TestSubscriptionSkipsListsTheRecentOnes(t *testing.T) {
	released := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	skippedAt := time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC)
	fake := &fakeSubscriber{skips: []subscription.Skip{
		{
			ID: 11, SubscriptionID: 5, MovieID: "m1", Number: "OFJE-186",
			Title: "初ベスト 6 タイトル 8 時間", ReleaseDate: &released,
			Reason: metrics.ReasonCompilation, SkippedAt: skippedAt,
		},
		{
			// 上游没给发行日的那一件：date 是 null，不编一个今天。
			ID: 12, SubscriptionID: 5, MovieID: "m2", Number: "REBD-725",
			Title: "两位女优共演", Reason: metrics.ReasonNotSingle, SkippedAt: skippedAt,
		},
	}}
	h := newSubscriptionFixture(t, fake)

	rec := send(t, h, http.MethodGet, "/api/subscriptions/5/skips", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if fake.subscriptionID != 5 {
		t.Errorf("接缝收到的订阅 id = %d, want 5", fake.subscriptionID)
	}

	body := jsonOf(t, rec)
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	skips, ok := body["skips"].([]any)
	if !ok || len(skips) != 2 {
		t.Fatalf("skips = %v, want 2 行", body["skips"])
	}
	first := skips[0].(map[string]any)
	for key, want := range map[string]any{
		"id":           float64(11),
		"movie_id":     "m1",
		"number":       "OFJE-186",
		"title":        "初ベスト 6 タイトル 8 時間",
		"release_date": "2026-11-14",
		"reason":       metrics.ReasonCompilation,
		"skipped_at":   "2026-11-16T09:30:00Z",
	} {
		if first[key] != want {
			t.Errorf("第一行 %s = %v, want %v", key, first[key], want)
		}
	}
	second := skips[1].(map[string]any)
	if got, ok := second["release_date"]; !ok || got != nil {
		t.Errorf("第二行 release_date = %v, want null（上游没给发行日）", second["release_date"])
	}
}

// TestSubscriptionSkipsErrors 钉住三件事：未知订阅 404、路由里的 id 不是数字 400、
// 装配缺失 503（与订阅区其余端点同一口径）。
func TestSubscriptionSkipsErrors(t *testing.T) {
	fake := &fakeSubscriber{skipsErr: subscription.ErrNotFound}
	h := newSubscriptionFixture(t, fake)

	if rec := send(t, h, http.MethodGet, "/api/subscriptions/9/skips", ""); rec.Code != http.StatusNotFound {
		t.Errorf("未知订阅的 status = %d, want 404", rec.Code)
	}
	if rec := send(t, h, http.MethodGet, "/api/subscriptions/abc/skips", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("非数字 id 的 status = %d, want 400", rec.Code)
	}
	if rec := send(t, h, http.MethodGet, "/api/subscriptions/0/skips", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("id=0 的 status = %d, want 400", rec.Code)
	}

	unwired := newHandler(t, server.Options{DB: pinger{}})
	if rec := send(t, unwired, http.MethodGet, "/api/subscriptions/5/skips", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("未装配的 status = %d, want 503", rec.Code)
	}
}

// TestAcceptSubscriptionSkip 钉住「收下」的回执形状 `{movie_id, outcome}`，
// 以及路径里两个 id 原样递到接缝（订阅 id 与跳过 id 是两件事）。
func TestAcceptSubscriptionSkip(t *testing.T) {
	fake := &fakeSubscriber{acceptBody: subscription.AcceptResult{
		MovieID: "m1", Outcome: subscription.OutcomeAccepted,
	}}
	h := newSubscriptionFixture(t, fake)

	rec := send(t, h, http.MethodPost, "/api/subscriptions/5/skips/11/accept", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)
	if body["movie_id"] != "m1" || body["outcome"] != subscription.OutcomeAccepted {
		t.Errorf("回执 = %v, want {movie_id:m1 outcome:accepted}", body)
	}
	if len(fake.acceptCalls) != 1 || fake.acceptCalls[0] != (acceptCall{subscriptionID: 5, skipID: 11}) {
		t.Errorf("接缝收到 = %+v, want {5 11}", fake.acceptCalls)
	}

	// 还没有磁力：同一个 200，outcome 说清「收下了但没东西可下」。
	noMagnet := &fakeSubscriber{acceptBody: subscription.AcceptResult{
		MovieID: "m2", Outcome: subscription.OutcomeNoMagnet,
	}}
	if rec := send(t, newSubscriptionFixture(t, noMagnet), http.MethodPost,
		"/api/subscriptions/5/skips/11/accept", ""); rec.Code != http.StatusOK {
		t.Errorf("没有磁力的 status = %d, want 200", rec.Code)
	}
}

// TestAcceptSubscriptionSkipErrors 把三条失败路径钉在状态码上：
// 跳过不存在 404、上游读不到 502、路径 id 不是数字 400。
func TestAcceptSubscriptionSkipErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
		code string
	}{
		{"跳过不存在", subscription.ErrSkipNotFound, http.StatusNotFound, "not_found"},
		{"上游读不到", fmt.Errorf("%w：磁力读取失败", subscription.ErrUpstream), http.StatusBadGateway, "upstream_error"},
		{"订阅不存在", subscription.ErrNotFound, http.StatusNotFound, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSubscriber{acceptErr: tc.err}
			h := newSubscriptionFixture(t, fake)

			rec := send(t, h, http.MethodPost, "/api/subscriptions/5/skips/11/accept", "")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
			body := jsonOf(t, rec)
			errBody, ok := body["error"].(map[string]any)
			if !ok || errBody["code"] != tc.code {
				t.Errorf("error = %v, want code=%s", body["error"], tc.code)
			}
		})
	}

	fake := &fakeSubscriber{}
	h := newSubscriptionFixture(t, fake)
	for _, path := range []string{
		"/api/subscriptions/5/skips/xyz/accept",
		"/api/subscriptions/5/skips/0/accept",
	} {
		if rec := send(t, h, http.MethodPost, path, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s 的 status = %d, want 400", path, rec.Code)
		}
	}
	if len(fake.acceptCalls) != 0 {
		t.Errorf("坏路径也打到了接缝: %+v", fake.acceptCalls)
	}

	// 不等于「随便什么错都 4xx」：库抖了是 503，与订阅区其余端点一致。
	broken := &fakeSubscriber{acceptErr: errors.New("连接池耗尽")}
	if rec := send(t, newSubscriptionFixture(t, broken), http.MethodPost,
		"/api/subscriptions/5/skips/11/accept", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("库抖的 status = %d, want 503", rec.Code)
	}
}
