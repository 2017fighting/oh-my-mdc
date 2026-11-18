package server_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/server"
	"github.com/2017fighting/oh-my-mdc/internal/subscription"
)

// 订阅区的 HTTP 契约（规格 §8.4，GLOSSARY「女优订阅」）。
//
// 断言只看 HTTP 面：路径、body 形状、状态码与稳定机器码，以及接缝收到的是不是原样值。
// 数据库与上游那一半（幂等、actor upsert、窗口、水位线）在 subscription 包的接缝上测。

type fakeSubscriber struct {
	rows []subscription.Row
	err  error

	addRequests []subscription.Request
	addOutcome  string
	addErr      error

	enabledCalls []bool
	disableErr   error

	backfillRequests []subscription.BackfillRequest
	backfillErr      error

	// 跳过面（#48）：GET skips 与「收下」。
	skips          []subscription.Skip
	skipsErr       error
	subscriptionID int64
	acceptCalls    []acceptCall
	acceptErr      error
	acceptBody     subscription.AcceptResult
}

// acceptCall 是「收下」接缝收到的一对 id（订阅 id 与跳过 id 是两件事）。
type acceptCall struct {
	subscriptionID int64
	skipID         int64
}

func (f *fakeSubscriber) List(context.Context) ([]subscription.Row, error) {
	return f.rows, f.err
}

func (f *fakeSubscriber) Add(_ context.Context, request subscription.Request) (subscription.Row, string, error) {
	f.addRequests = append(f.addRequests, request)
	if f.addErr != nil {
		return subscription.Row{}, "", f.addErr
	}
	outcome := f.addOutcome
	if outcome == "" {
		outcome = subscription.OutcomeCreated
	}
	return subscription.Row{ID: 7, Actor: subscription.Actor{ID: "EvkJ", Name: "河北彩花"}}, outcome, nil
}

func (f *fakeSubscriber) SetEnabled(_ context.Context, id int64, enabled bool) (subscription.Row, error) {
	if f.disableErr != nil {
		return subscription.Row{}, f.disableErr
	}
	f.enabledCalls = append(f.enabledCalls, enabled)
	return subscription.Row{ID: id, Enabled: enabled, Actor: subscription.Actor{ID: "EvkJ", Name: "河北彩花"}}, nil
}

func (f *fakeSubscriber) Backfill(_ context.Context, id int64, request subscription.BackfillRequest) (subscription.Row, error) {
	if f.backfillErr != nil {
		return subscription.Row{}, f.backfillErr
	}
	f.backfillRequests = append(f.backfillRequests, request)
	from := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	if request.All {
		from = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return subscription.Row{ID: id, BackfillFrom: &from}, nil
}

func (f *fakeSubscriber) Skips(_ context.Context, id int64) ([]subscription.Skip, error) {
	f.subscriptionID = id
	return f.skips, f.skipsErr
}

func (f *fakeSubscriber) AcceptSkip(_ context.Context, id, skipID int64) (subscription.AcceptResult, error) {
	f.acceptCalls = append(f.acceptCalls, acceptCall{subscriptionID: id, skipID: skipID})
	if f.acceptErr != nil {
		return subscription.AcceptResult{}, f.acceptErr
	}
	return f.acceptBody, nil
}

func newSubscriptionFixture(t *testing.T, subscriber server.Subscriber) http.Handler {
	t.Helper()
	if subscriber == nil {
		subscriber = &fakeSubscriber{}
	}
	return newHandler(t, server.Options{DB: pinger{}, Subscriptions: subscriber})
}

// TestSubscriptionsListShowsActorWatermarkAndCounts 钉住 §8.4 的列表形状：
// 面板一行要的字段一个不少（actor 快照、水位线、回看窗口、回灌下界、启停、带来 N 部）。
func TestSubscriptionsListShowsActorWatermarkAndCounts(t *testing.T) {
	watermark := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	from := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	fake := &fakeSubscriber{rows: []subscription.Row{{
		ID:            3,
		Actor:         subscription.Actor{ID: "EvkJ", Name: "河北彩花", AvatarURL: "https://x.invalid/e.jpg", Gender: "female"},
		WatermarkDate: &watermark,
		LookbackDays:  30,
		BackfillFrom:  &from,
		Enabled:       true,
		TasksCount:    12,
		PendingCount:  2,
		DeferredCount: 4,
	}}}
	h := newSubscriptionFixture(t, fake)

	rec := send(t, h, http.MethodGet, "/api/subscriptions", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)
	rows, ok := body["actresses"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("actresses = %v, want 一行", body["actresses"])
	}
	row, _ := rows[0].(map[string]any)
	if row["id"] != float64(3) {
		t.Errorf("id = %v, want 3", row["id"])
	}
	if row["watermark_date"] != "2026-11-01" {
		t.Errorf("watermark_date = %v, want 2026-11-01（日期不是时间戳）", row["watermark_date"])
	}
	if row["backfill_from"] != "2026-10-02" {
		t.Errorf("backfill_from = %v, want 2026-10-02", row["backfill_from"])
	}
	if row["lookback_days"] != float64(30) || row["enabled"] != true {
		t.Errorf("lookback/enabled = %v/%v", row["lookback_days"], row["enabled"])
	}
	if row["tasks_count"] != float64(12) || row["pending_count"] != float64(2) {
		t.Errorf("tasks_count/pending_count = %v/%v, want 12/2", row["tasks_count"], row["pending_count"])
	}
	// deferred_count 是「还有 N 部待入队」（§6.4/§9）：与 pending_count（等加种的版本）
	// 两个数各管一段，面板两处都读，别把它们合成一个。
	if row["deferred_count"] != float64(4) {
		t.Errorf("deferred_count = %v, want 4（还有 4 部被每轮限量留在门外）", row["deferred_count"])
	}
	actor, _ := row["actor"].(map[string]any)
	if actor["javdb_actor_id"] != "EvkJ" || actor["name"] != "河北彩花" || actor["gender"] != "female" {
		t.Errorf("actor = %v, want 上游快照", actor)
	}
}

// TestSubscriptionsListIsEmptyArrayNotNull 守住前端契约：空列表要是 []，
// 而不是 null（面板拿它渲染「还没订阅任何女优」，null 会把它变成一次崩溃）。
func TestSubscriptionsListIsEmptyArrayNotNull(t *testing.T) {
	h := newSubscriptionFixture(t, &fakeSubscriber{})

	rec := send(t, h, http.MethodGet, "/api/subscriptions", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	rows, ok := jsonOf(t, rec)["actresses"].([]any)
	if !ok {
		t.Fatalf("actresses = %v, want []", jsonOf(t, rec)["actresses"])
	}
	if len(rows) != 0 {
		t.Errorf("actresses = %v, want []", rows)
	}
}

// TestCreateSubscriptionAcceptsIDOrURL 钉住 §8.4 的两种输入：贴 id 与贴女优页链接。
func TestCreateSubscriptionAcceptsIDOrURL(t *testing.T) {
	fake := &fakeSubscriber{}
	h := newSubscriptionFixture(t, fake)

	rec := send(t, h, http.MethodPost, "/api/subscriptions", `{"actress_id":"EvkJ"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := jsonOf(t, rec)
	if body["outcome"] != subscription.OutcomeCreated {
		t.Errorf("outcome = %v, want created", body["outcome"])
	}
	if len(fake.addRequests) != 1 || fake.addRequests[0].ActressID != "EvkJ" {
		t.Errorf("接缝收到 = %+v, want actress_id=EvkJ", fake.addRequests)
	}

	rec = send(t, h, http.MethodPost, "/api/subscriptions", `{"javdb_url":"https://javdb.com/actors/Av2e"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if len(fake.addRequests) != 2 || fake.addRequests[1].JavdbURL == "" {
		t.Errorf("接缝收到 = %+v, want 第二次带 javdb_url", fake.addRequests)
	}
}

// TestCreateSubscriptionIsIdempotent 是验收原文：同一位女优重复添加仍 200，
// 回执说清是幂等放行（duplicate），而不是一个新行。
func TestCreateSubscriptionIsIdempotent(t *testing.T) {
	fake := &fakeSubscriber{addOutcome: subscription.OutcomeDuplicate}
	h := newSubscriptionFixture(t, fake)

	rec := send(t, h, http.MethodPost, "/api/subscriptions", `{"actress_id":"EvkJ"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if outcome := jsonOf(t, rec)["outcome"]; outcome != subscription.OutcomeDuplicate {
		t.Errorf("outcome = %v, want duplicate", outcome)
	}
}

// TestCreateSubscriptionErrorsFollowTheEnvelope 钉住三档错误码（§8.4）：
// 形状 400 validation_error、门禁 409 gate_closed、id 读不到 502 upstream_error。
func TestCreateSubscriptionErrorsFollowTheEnvelope(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"形状":   {subscription.ErrBadInput, http.StatusBadRequest, "validation_error"},
		"门禁":   {subscription.ErrGated, http.StatusConflict, "gate_closed"},
		"上游":   {subscription.ErrUpstream, http.StatusBadGateway, "upstream_error"},
		"库抖":   {errors.New("数据库连不上"), http.StatusServiceUnavailable, "db_unavailable"},
		"未知元素": {subscription.ErrNotFound, http.StatusNotFound, "not_found"},
	}
	for name, tc := range cases {
		h := newSubscriptionFixture(t, &fakeSubscriber{addErr: tc.err})
		rec := send(t, h, http.MethodPost, "/api/subscriptions", `{"actress_id":"EvkJ"}`)
		if rec.Code != tc.status {
			t.Errorf("%s: status = %d, want %d（body=%s）", name, rec.Code, tc.status, rec.Body)
			continue
		}
		errorBody, _ := jsonOf(t, rec)["error"].(map[string]any)
		if errorBody["code"] != tc.code {
			t.Errorf("%s: code = %v, want %s", name, errorBody["code"], tc.code)
		}
	}
}

// TestCreateSubscriptionRejectsBadJSON 挡住「形状不对的请求体」：400，不是 500。
func TestCreateSubscriptionRejectsBadJSON(t *testing.T) {
	h := newSubscriptionFixture(t, &fakeSubscriber{})
	rec := send(t, h, http.MethodPost, "/api/subscriptions", `{`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	if code := jsonOf(t, rec)["error"].(map[string]any)["code"]; code != "validation_error" {
		t.Errorf("code = %v, want validation_error", code)
	}
}

// TestEnableAndDisableSubscription 钉住启停两条路：路径里的 id 是行号，动的是那一位。
func TestEnableAndDisableSubscription(t *testing.T) {
	fake := &fakeSubscriber{}
	h := newSubscriptionFixture(t, fake)

	// 启停的回执用自己的 outcome 词表（enabled / disabled），不借创建的 created / duplicate。
	disabled := send(t, h, http.MethodPost, "/api/subscriptions/5/disable", "")
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want 200: %s", disabled.Code, disabled.Body)
	}
	if outcome := jsonOf(t, disabled)["outcome"]; outcome != "disabled" {
		t.Errorf("disable 的 outcome = %v, want disabled", outcome)
	}
	enabled := send(t, h, http.MethodPost, "/api/subscriptions/5/enable", "")
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable status = %d, want 200: %s", enabled.Code, enabled.Body)
	}
	if outcome := jsonOf(t, enabled)["outcome"]; outcome != "enabled" {
		t.Errorf("enable 的 outcome = %v, want enabled", outcome)
	}
	if len(fake.enabledCalls) != 2 || fake.enabledCalls[0] != false || fake.enabledCalls[1] != true {
		t.Errorf("启停调用 = %v, want [false true]", fake.enabledCalls)
	}

	rec := send(t, h, http.MethodPost, "/api/subscriptions/abc/enable", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非数字 id 的 status = %d, want 400", rec.Code)
	}
}

// TestBackfillSubscriptionPassesDays 钉住 §8.4 的回执：{backfill_from} 是日期串。
func TestBackfillSubscriptionPassesDays(t *testing.T) {
	fake := &fakeSubscriber{}
	h := newSubscriptionFixture(t, fake)

	rec := send(t, h, http.MethodPost, "/api/subscriptions/5/backfill", `{"days":90}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if body := jsonOf(t, rec); body["backfill_from"] != "2026-10-19" {
		t.Errorf("backfill_from = %v, want 2026-10-19", body["backfill_from"])
	}
	if len(fake.backfillRequests) != 1 || fake.backfillRequests[0] != (subscription.BackfillRequest{Days: 90}) {
		t.Errorf("接缝收到 = %+v, want {Days:90}", fake.backfillRequests)
	}

	// 形状错（没有 days 也没有 all、days 不是整数）在这里就挡下，不打到接缝：
	// 「days 太小」是服务层的规则（它要读回看窗口），服务层用例管那一条。
	for _, body := range []string{`{}`, `{"days":30.5}`, `{"days":"三十"}`, `{"all":"yes"}`} {
		if rec := send(t, h, http.MethodPost, "/api/subscriptions/5/backfill", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s 的 status = %d, want 400", body, rec.Code)
		}
	}
	if len(fake.backfillRequests) != 1 {
		t.Errorf("坏形状也打到了接缝: %+v", fake.backfillRequests)
	}
}

// TestBackfillSubscriptionAcceptsAll 钉住第二种请求形状：{all:true} 是「翻到她最早的作品」，
// 回执仍是同一个 {backfill_from}（1970-01-01 就是「没有下界」）。
func TestBackfillSubscriptionAcceptsAll(t *testing.T) {
	fake := &fakeSubscriber{}
	h := newSubscriptionFixture(t, fake)

	rec := send(t, h, http.MethodPost, "/api/subscriptions/5/backfill", `{"all":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if body := jsonOf(t, rec); body["backfill_from"] != "1970-01-01" {
		t.Errorf("backfill_from = %v, want 1970-01-01", body["backfill_from"])
	}
	if len(fake.backfillRequests) != 1 || !fake.backfillRequests[0].All {
		t.Errorf("接缝收到 = %+v, want {All:true}", fake.backfillRequests)
	}
}

// TestBackfillSubscriptionRejectsNoOpReplay 钉住 §8.4 的那条 400：落点不比起追日更早
// （＝空操作）时服务层回 ErrBadInput，HTTP 翻成 400 validation_error，message 原样带到
// 面板（它要说清「这一段已经被覆盖」）。「days ≤ 回看窗口」不再是判断依据。
func TestBackfillSubscriptionRejectsNoOpReplay(t *testing.T) {
	fake := &fakeSubscriber{backfillErr: fmt.Errorf(
		"%w：回灌 30 天只到 2026-10-19，起追日已经在 2026-09-19——这一段已经覆盖了", subscription.ErrBadInput)}
	h := newSubscriptionFixture(t, fake)

	rec := send(t, h, http.MethodPost, "/api/subscriptions/5/backfill", `{"days":30}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	errorBody, _ := jsonOf(t, rec)["error"].(map[string]any)
	if errorBody["code"] != "validation_error" {
		t.Errorf("code = %v, want validation_error", errorBody["code"])
	}
	message, _ := errorBody["message"].(string)
	if !strings.Contains(message, "覆盖") {
		t.Errorf("message = %q, want 说清为什么不行", message)
	}
}

// TestSubscriptionsRequiresTheAssembler 与补录一致：没装配时 503，而不是空 200。
func TestSubscriptionsRequiresTheAssembler(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})
	rec := send(t, h, http.MethodGet, "/api/subscriptions", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
