package sidefx_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
	"github.com/2017fighting/oh-my-mdc/internal/telegram"
)

// 通知执行半段的测试缝：真 Postgres 的出箱 + 假 Notifier（真模板另有一条端到端）。

// fakeNotifier 记下每一次发送，按 err 决定成功与否。
type fakeNotifier struct {
	mu    sync.Mutex
	calls []notifyCall
	err   error
}

type notifyCall struct {
	cfg     telegram.Config
	event   string
	payload map[string]any
}

func (f *fakeNotifier) Notify(_ context.Context, cfg telegram.Config, event string, payload map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, notifyCall{cfg: cfg, event: event, payload: payload})
	return f.err
}

func (f *fakeNotifier) recorded() []notifyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]notifyCall(nil), f.calls...)
}

// TestEngineSendsNotifyWhenEnabled 是 P12-1 的正向：开关开着，四事件各发一条。
//
// 一条待办只发一次、成功即完成——面板的副作用队列不该留一条已发过的活。
func TestEngineSendsNotifyWhenEnabled(t *testing.T) {
	notifier := &fakeNotifier{}
	f := newFixtureWith(t, notifier)
	ctx := context.Background()
	for _, event := range []string{
		sidefx.EventDownloadStarted, sidefx.EventFiled,
		sidefx.EventProcessingFailed, sidefx.EventDownloadStalled,
	} {
		if err := f.outbox.EnqueueNotify(ctx, event, hashOne, map[string]any{
			"movie_id": movieID, "infohash": hashOne,
			"number": number, "label": "中文", "source": "javdb_magnet",
		}); err != nil {
			t.Fatalf("入箱 %s: %v", event, err)
		}
	}

	result := f.runOnce(t)

	if result.Due != 4 || result.Done != 4 || result.Stuck != 0 {
		t.Fatalf("result = %+v, want 四条都完成", result)
	}
	calls := notifier.recorded()
	if len(calls) != 4 {
		t.Fatalf("发送 = %d 次, want 4", len(calls))
	}
	for i, call := range calls {
		if !call.cfg.Enabled || call.cfg.ChatID != "-100200300" {
			t.Errorf("第 %d 条 cfg = %+v, want 沿用设置里的 chat_id", i+1, call.cfg)
		}
		if call.payload["number"] != number {
			t.Errorf("第 %d 条 payload = %+v, want 带上番号", i+1, call.payload)
		}
	}
}

// TestEngineDedupesNotifyPerEventAndInfohash 钉住 P12-4 的两半：
// 同一 (event, infohash) 只发一条；不同事件即使同 infohash 也各发各的。
func TestEngineDedupesNotifyPerEventAndInfohash(t *testing.T) {
	notifier := &fakeNotifier{}
	f := newFixtureWith(t, notifier)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := f.outbox.EnqueueNotify(ctx, sidefx.EventFiled, hashOne,
			map[string]any{"number": number}); err != nil {
			t.Fatalf("第 %d 次入箱: %v", i+1, err)
		}
	}
	if err := f.outbox.EnqueueNotify(ctx, sidefx.EventDownloadStarted, hashOne,
		map[string]any{"number": number}); err != nil {
		t.Fatalf("入箱下载开始: %v", err)
	}

	result := f.runOnce(t)

	if result.Due != 2 || result.Done != 2 {
		t.Fatalf("result = %+v, want 两条（filed 去重成一条 + download_started）", result)
	}
	if calls := notifier.recorded(); len(calls) != 2 {
		t.Errorf("发送 = %d 次, want 2", len(calls))
	}
}

// TestEngineSkipsNotifyWhenEventSwitchOff 钉住 P12-1：关掉的那个事件不发，
// 待办按完成处理（不驻留、不重试）。
func TestEngineSkipsNotifyWhenEventSwitchOff(t *testing.T) {
	notifier := &fakeNotifier{}
	f := newFixtureWith(t, notifier)
	f.settings.inputs["telegram"] = settings.TestInput{Values: map[string]any{
		"bot_token":                "123456:token",
		"chat_id":                  "-100200300",
		"enabled":                  true,
		"notify_download_started":  true,
		"notify_filed":             false,
		"notify_processing_failed": true,
		"notify_download_stalled":  true,
	}}
	if err := f.outbox.EnqueueNotify(context.Background(), sidefx.EventFiled, hashOne,
		map[string]any{"number": number}); err != nil {
		t.Fatalf("入箱: %v", err)
	}

	result := f.runOnce(t)

	if result.Done != 1 || result.Stuck != 0 {
		t.Fatalf("result = %+v, want 按「不发」完成", result)
	}
	if calls := notifier.recorded(); len(calls) != 0 {
		t.Errorf("发送 = %d 次, want 0（开关关着）", len(calls))
	}
}

// TestEngineSkipsNotifyWhenIntegrationDisabled：总开关关着时四个事件都不发。
func TestEngineSkipsNotifyWhenIntegrationDisabled(t *testing.T) {
	notifier := &fakeNotifier{}
	f := newFixtureWith(t, notifier)
	input := f.settings.inputs["telegram"]
	input.Values["enabled"] = false
	f.settings.inputs["telegram"] = input
	if err := f.outbox.EnqueueNotify(context.Background(), sidefx.EventDownloadStarted, hashOne,
		map[string]any{"number": number}); err != nil {
		t.Fatalf("入箱: %v", err)
	}

	result := f.runOnce(t)

	if result.Done != 1 {
		t.Fatalf("result = %+v, want 一条完成", result)
	}
	if calls := notifier.recorded(); len(calls) != 0 {
		t.Errorf("发送 = %d 次, want 0（集成关着）", len(calls))
	}
}

// TestEngineCompletesNotifyWhenTelegramNotConfigured：可选集成没配时不积压通知待办——
// 告警兜底是事件流与指标，不是把队列撑爆（规格 §5.6）。
func TestEngineCompletesNotifyWhenTelegramNotConfigured(t *testing.T) {
	notifier := &fakeNotifier{}
	f := newFixtureWith(t, notifier)
	delete(f.settings.inputs, "telegram")
	if err := f.outbox.EnqueueNotify(context.Background(), sidefx.EventDownloadStarted, hashOne,
		map[string]any{"number": number}); err != nil {
		t.Fatalf("入箱: %v", err)
	}

	result := f.runOnce(t)

	if result.Done != 1 || result.Stuck != 0 {
		t.Fatalf("result = %+v, want 按「无需发送」完成", result)
	}
	if calls := notifier.recorded(); len(calls) != 0 {
		t.Errorf("发送 = %d 次, want 0", len(calls))
	}
}

// TestEngineSticksNotifyOnPermanentFailure：400/401/403 立即驻留（P12-3），
// 一次就够；下一轮不再自动碰它，人可以在需人工区重发。
func TestEngineSticksNotifyOnPermanentFailure(t *testing.T) {
	notifier := &fakeNotifier{err: &telegram.Error{
		Code: settings.CodeAuth, Status: 401, Detail: "Unauthorized",
	}}
	f := newFixtureWith(t, notifier)
	if err := f.outbox.EnqueueNotify(context.Background(), sidefx.EventFiled, hashOne,
		map[string]any{"number": number}); err != nil {
		t.Fatalf("入箱: %v", err)
	}

	result := f.runOnce(t)

	if result.Stuck != 1 {
		t.Fatalf("result = %+v, want 立即驻留", result)
	}
	stuck := f.jobs(t, sidefx.StateStuck)
	if len(stuck) != 1 || stuck[0].Attempts != 1 || stuck[0].NextAttemptAt != nil {
		t.Fatalf("驻留行 = %+v, want attempts=1 且不再排期", stuck)
	}
	// 绝不自动禁用集成（P12-3）：设置原样还在。
	if input := f.settings.inputs["telegram"]; !input.Bool("enabled") {
		t.Error("Telegram 被自动禁用了，want 只驻留")
	}
	if next := f.runOnce(t); next.Due != 0 {
		t.Errorf("驻留后再跑 = %+v, want 不再捡起", next)
	}
}

// TestEngineRecordsAlertWhenNotifySticks 钉住「告警兜底不依赖 Telegram」（规格 §5.6/§9）：
// 通知驻留时时间线仍然留一条 alert——面板与指标不靠这条消息送达与否。
//
// payload 用入箱处的真实形状（poll 的下载开始），确认兜底不是靠测试手写的字段。
func TestEngineRecordsAlertWhenNotifySticks(t *testing.T) {
	notifier := &fakeNotifier{err: &telegram.Error{
		Code: settings.CodeBusiness, Status: 400, Detail: "chat not found",
	}}
	f := newFixtureWith(t, notifier)
	seedPendingRemoval(t, f.store, movieID, number, hashOne)
	ctx := context.Background()
	if err := f.outbox.EnqueueNotify(ctx, sidefx.EventDownloadStarted, hashOne, map[string]any{
		"movie_id": movieID, "infohash": hashOne, "number": number, "source": "javdb_magnet",
	}); err != nil {
		t.Fatalf("入箱: %v", err)
	}

	if result := f.runOnce(t); result.Stuck != 1 {
		t.Fatalf("result = %+v, want 通知驻留", result)
	}

	if !hasEvent(f.events(t), task.EventAlert, "发 Telegram 驻留等人工") {
		t.Errorf("事件 = %+v, want 一条 alert（不依赖 Telegram 送达）", f.events(t))
	}
}

// TestEngineRecordsSideEffectWhenNotifySent：发成功也在时间线上留一条——
// 面板的最近事件要说得清「这条通知已经发出去了」。
func TestEngineRecordsSideEffectWhenNotifySent(t *testing.T) {
	f := newFixtureWith(t, &fakeNotifier{})
	// 时间线挂在任务上（task_event 有外键），所以先种一条任务/版本。
	seedPendingRemoval(t, f.store, movieID, number, hashOne)
	ctx := context.Background()
	if err := f.outbox.EnqueueNotify(ctx, sidefx.EventFiled, hashOne, map[string]any{
		"movie_id": movieID, "infohash": hashOne, "number": number,
	}); err != nil {
		t.Fatalf("入箱: %v", err)
	}

	// 一轮里有两条活：新种的待删清单项 + 这条通知。
	if result := f.runOnce(t); result.Done != 2 {
		t.Fatalf("result = %+v, want 两条都完成（通知发送成功）", result)
	}
	if !hasEvent(f.events(t), task.EventSideEffect, "Telegram 通知已发送") {
		t.Errorf("事件 = %+v, want 一条 side_effect", f.events(t))
	}
}

// TestEngineBacksOffNotifyFiveTimes：瞬时错（网络/429/5xx）退避到 notify 专用上限 5 次，
// 第 5 次失败才驻留（规格 §5.6）。
func TestEngineBacksOffNotifyFiveTimes(t *testing.T) {
	notifier := &fakeNotifier{err: &telegram.Error{
		Code: settings.CodeProtocol, Status: 502, Detail: "Bad Gateway",
	}}
	f := newFixtureWith(t, notifier)
	ctx := context.Background()
	if err := f.outbox.EnqueueNotify(ctx, sidefx.EventDownloadStalled, hashOne,
		map[string]any{"number": number}); err != nil {
		t.Fatalf("入箱: %v", err)
	}

	for attempt := 1; attempt <= sidefx.NotifyMaxAttempts; attempt++ {
		result := f.runOnce(t)
		jobs := f.jobs(t, sidefx.StatePending)
		if attempt < sidefx.NotifyMaxAttempts {
			if result.Retried != 1 || len(jobs) != 1 || jobs[0].Attempts != attempt {
				t.Fatalf("第 %d 轮 = %+v / %+v, want 继续退避", attempt, result, jobs)
			}
			f.makeDue(t, jobs[0].ID)
			continue
		}

		if result.Stuck != 1 {
			t.Fatalf("第 %d 轮 = %+v, want 驻留", attempt, result)
		}
	}
	if calls := notifier.recorded(); len(calls) != sidefx.NotifyMaxAttempts {
		t.Errorf("发送 = %d 次, want %d 次（不到上限不放弃）", len(calls), sidefx.NotifyMaxAttempts)
	}
}

// TestEngineSticksNotifyOnUnknownEvent：事件名写错是本地错，驻留等人工——
// 静默完成会把一条永远发不出的通知从面板上藏起来。
func TestEngineSticksNotifyOnUnknownEvent(t *testing.T) {
	notifier := &fakeNotifier{}
	f := newFixtureWith(t, notifier)
	if err := f.outbox.EnqueueNotify(context.Background(), "download_finished", hashOne,
		map[string]any{"number": number}); err != nil {
		t.Fatalf("入箱: %v", err)
	}

	result := f.runOnce(t)

	if result.Stuck != 1 {
		t.Fatalf("result = %+v, want 驻留", result)
	}
	if calls := notifier.recorded(); len(calls) != 0 {
		t.Errorf("发送 = %d 次, want 0", len(calls))
	}
}

// TestEngineSendsRealTelegramMessagesEndToEnd 是「下载开始 / 入库完成」的端到端（fake Telegram）：
// 出箱 → 引擎读开关 → telegram 模板与转义 → sendMessage HTTP 全链路。
//
// 收编也发下载开始（P12-1）：收编与新增在入箱处就是同一个事件，这里不再分叉。
func TestEngineSendsRealTelegramMessagesEndToEnd(t *testing.T) {
	var (
		mu    sync.Mutex
		texts []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		texts = append(texts, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	t.Cleanup(server.Close)

	integration := telegram.New(telegram.Options{Host: server.URL})
	f := newFixtureWith(t, integration)
	ctx := context.Background()
	const hashTwo = "2222222222222222222222222222222222222222"
	// payload 形状照入箱处（poll 的加种）：movie_id/infohash 是驻留兜底的锚点。
	if err := f.outbox.EnqueueNotify(ctx, sidefx.EventDownloadStarted, hashOne, map[string]any{
		"movie_id": movieID, "infohash": hashOne,
		"number": number, "label": "中文", "source": "javdb_magnet", "size_mib": 1536,
	}); err != nil {
		t.Fatalf("入箱下载开始: %v", err)
	}
	if err := f.outbox.EnqueueNotify(ctx, sidefx.EventFiled, hashTwo, map[string]any{
		"movie_id": movieID, "infohash": hashTwo,
		"number": number, "title": "标题要 <转义>", "label": "中文",
		"source": "mteam_torrent", "size_mib": 2048, "new_version": true,
	}); err != nil {
		t.Fatalf("入箱入库完成: %v", err)
	}

	result := f.runOnce(t)
	if result.Done != 2 {
		t.Fatalf("result = %+v, want 两条都发出", result)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(texts) != 2 {
		t.Fatalf("sendMessage = %d 次, want 2", len(texts))
	}
	first, second := decodeSend(t, texts[0]), decodeSend(t, texts[1])
	if first["parse_mode"] != "HTML" {
		t.Errorf("parse_mode = %v, want HTML", first["parse_mode"])
	}
	if text, _ := first["text"].(string); !strings.Contains(text, "🎬 <b>"+number+"</b> 开始下载") {
		t.Errorf("下载开始 text = %q, want 模板首行", text)
	}
	secondText, _ := second["text"].(string)
	if !strings.Contains(secondText, "✅ <b>"+number+"</b> 新版本入库") ||
		!strings.Contains(secondText, "标题要 &lt;转义&gt;") {
		t.Errorf("入库完成 text = %q, want 新版本分支 + 转义", secondText)
	}
}

// decodeSend 解一条 sendMessage 请求体（JSON 会把 < > & 转成 \u003c 这种形式）。
func decodeSend(t *testing.T, body string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("sendMessage 请求体不是 JSON: %v", err)
	}
	return decoded
}

// 编译期确认假通知器满足引擎的接缝（与 telegram.Integration 同一形状）。
var _ sidefx.Notifier = (*fakeNotifier)(nil)
var _ sidefx.Notifier = (*telegram.Integration)(nil)
