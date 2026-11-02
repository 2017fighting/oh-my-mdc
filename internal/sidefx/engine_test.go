package sidefx_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/jellyfin"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 引擎的测试缝：RunOnce + 真 Postgres + 假上游（javdb 删清单项 / jellyfin 刷新）。
// 断言只看待办行的 attempts/next_attempt_at/done_at、时间线与「上游被调了几次」，
// 不看引擎内部结构。

// ------------------------------------------------------------------ fakes

type fakeSettings struct {
	snapshot settings.Snapshot
	inputs   map[string]settings.TestInput
	loadErr  error
}

func (f *fakeSettings) Load(context.Context) (settings.Snapshot, error) {
	return f.snapshot, f.loadErr
}

func (f *fakeSettings) Input(_ context.Context, section string) (settings.TestInput, error) {
	input, ok := f.inputs[section]
	if !ok {
		return settings.TestInput{}, settings.ErrNotConfigured
	}
	return input, nil
}

// fakeRemover 记下 javdb 的删除调用，按 err 决定成功与否。
type fakeRemover struct {
	mu    sync.Mutex
	calls []removalCall
	err   error
}

type removalCall struct {
	token   string
	listID  string
	movieID string
}

func (f *fakeRemover) RemoveFromList(_ context.Context, token, listID, movieID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, removalCall{token: token, listID: listID, movieID: movieID})
	return f.err
}

func (f *fakeRemover) recorded() []removalCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]removalCall(nil), f.calls...)
}

// fakeRefresher 记下 jellyfin 的刷新调用。
type fakeRefresher struct {
	mu      sync.Mutex
	numbers []string
	err     error
	scope   jellyfin.RefreshScope
}

func (f *fakeRefresher) Refresh(_ context.Context, _ jellyfin.Config, number string) (jellyfin.RefreshResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.numbers = append(f.numbers, number)
	scope := f.scope
	if scope == "" {
		scope = jellyfin.ScopeDirectory
	}
	return jellyfin.RefreshResult{Scope: scope, Directory: "/media/jellyfin/jav/" + number, Accepted: f.err == nil}, f.err
}

func (f *fakeRefresher) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.numbers...)
}

// ------------------------------------------------------------------ fixture

type fixture struct {
	database  *db.DB
	outbox    *sidefx.Outbox
	store     *task.Store
	remover   *fakeRemover
	refresher *fakeRefresher
	settings  *fakeSettings
	engine    *sidefx.Engine
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	database := dbtest.Migrated(t)
	if _, err := database.Pool().Exec(context.Background(),
		`INSERT INTO list_registry (list_id, purpose, name) VALUES ('wanted', 'want_watch', '想看清单')`,
	); err != nil {
		t.Fatalf("种清单登记失败: %v", err)
	}

	f := &fixture{
		database:  database,
		outbox:    sidefx.New(database.Pool()),
		store:     task.NewStore(database.Pool()),
		remover:   &fakeRemover{},
		refresher: &fakeRefresher{},
		settings:  readySettings(),
	}
	f.engine = sidefx.NewEngine(sidefx.Options{
		Outbox:    f.outbox,
		Settings:  f.settings,
		Remover:   f.remover,
		Refresher: f.refresher,
		Tasks:     f.store,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return f
}

func readySettings() *fakeSettings {
	return &fakeSettings{
		snapshot: settings.Snapshot{
			Initialized: true,
			Sections: map[string]settings.SectionState{
				"app": {Configured: true, Data: map[string]any{
					"sidefx_interval": "1m0s",
					"max_attempts":    3,
				}},
			},
		},
		inputs: map[string]settings.TestInput{
			"javdb":    {Values: map[string]any{"token": "tk"}},
			"jellyfin": {Values: map[string]any{"url": "http://jellyfin:8096", "api_key": "key", "media_root": "/media/jellyfin/jav"}},
		},
	}
}

// makeDue 把一条待办的时间拨到过去：模拟「退避窗口过去了」。
func (f *fixture) makeDue(t *testing.T, id int64) {
	t.Helper()
	if _, err := f.database.Pool().Exec(context.Background(),
		`UPDATE side_effect SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatalf("拨时间: %v", err)
	}
}

func (f *fixture) jobs(t *testing.T, state sidefx.State) []sidefx.Job {
	t.Helper()
	jobs, err := f.outbox.List(context.Background(), state, 20)
	if err != nil {
		t.Fatalf("List(%s): %v", state, err)
	}
	return jobs
}

func (f *fixture) events(t *testing.T) []task.Event {
	t.Helper()
	events, err := f.store.Events(context.Background(), movieID, 100)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	return events
}

func (f *fixture) runOnce(t *testing.T) sidefx.Result {
	t.Helper()
	result, err := f.engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	return result
}

// ------------------------------------------------------------------ list_removal

// TestEngineRemovesListItem 是 P7-1 的执行半段：加种后置起的待删，被旁路循环真的删掉，
// 且用的是代码常量 remove（javdb 侧另有测试钉字面量）。
func TestEngineRemovesListItem(t *testing.T) {
	f := newFixture(t)
	seedPendingRemoval(t, f.store, movieID, number, hashOne)

	result := f.runOnce(t)

	if result.Swept != 1 || result.Due != 1 || result.Done != 1 {
		t.Fatalf("result = %+v, want swept/due/done 各 1", result)
	}
	calls := f.remover.recorded()
	if len(calls) != 1 {
		t.Fatalf("删除调用 = %d 次, want 1", len(calls))
	}
	if calls[0] != (removalCall{token: "tk", listID: "wanted", movieID: movieID}) {
		t.Errorf("删除调用 = %+v, want token/wanted/movie_id", calls[0])
	}

	done, err := f.outbox.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("Job: %v", err)
	}
	if err := f.outbox.MarkDone(context.Background(), 1); err == nil {
		t.Error("对已完成的待办再 MarkDone 应该报 ErrNotFound")
	}
	if done.DoneAt == nil {
		t.Error("done_at 还是 NULL, want 完成")
	}
	if done.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", done.Attempts)
	}

	// 时间线留痕：面板的最近事件要说得出「已经删过了」。
	if !hasEvent(f.events(t), task.EventSideEffect, "已从清单删除") {
		t.Errorf("事件 = %+v, want 一条 side_effect「已从清单删除」", f.events(t))
	}

	// 再跑一轮：没有待删标记了，零入箱零执行。
	again := f.runOnce(t)
	if again.Swept != 0 || again.Due != 0 {
		t.Errorf("第二轮 = %+v, want 无活可做", again)
	}
}

// TestEngineBacksOffTransientFailureThenSticks 钉住指数退避与「耗尽驻留」（规格 §4.2）：
// 瞬时错每次都排下一次；第 3 次失败（app_config.max_attempts）之后驻留等人工。
func TestEngineBacksOffTransientFailureThenSticks(t *testing.T) {
	f := newFixture(t)
	f.remover.err = &appapi.TransportError{Path: "/api/v1/lists/wanted/movie_actions", Err: errors.New("连接被拒")}
	seedPendingRemoval(t, f.store, movieID, number, hashOne)

	first := f.runOnce(t)
	if first.Retried != 1 || first.Stuck != 0 {
		t.Fatalf("第一轮 = %+v, want 一条退避重试", first)
	}
	job := f.jobs(t, sidefx.StatePending)[0]
	if job.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", job.Attempts)
	}
	if job.NextAttemptAt == nil || !job.NextAttemptAt.After(time.Now().Add(30*time.Second)) {
		t.Errorf("next_attempt_at = %v, want 排到将来（退避窗口）", job.NextAttemptAt)
	}
	if job.LastError == "" {
		t.Error("last_error 为空, want 留下这一次的原因")
	}

	f.makeDue(t, job.ID)
	f.runOnce(t)
	if job := f.jobs(t, sidefx.StatePending)[0]; job.Attempts != 2 {
		t.Fatalf("第二轮后 attempts = %d, want 2", job.Attempts)
	}

	f.makeDue(t, job.ID)
	third := f.runOnce(t)
	if third.Stuck != 1 {
		t.Fatalf("第三轮 = %+v, want 一条驻留", third)
	}
	stuck := f.jobs(t, sidefx.StateStuck)
	if len(stuck) != 1 {
		t.Fatalf("驻留 = %d 条, want 1", len(stuck))
	}
	if stuck[0].Attempts != 3 || stuck[0].NextAttemptAt != nil {
		t.Errorf("驻留行 = %+v, want attempts=3 且 next_attempt_at IS NULL", stuck[0])
	}
	if !hasEvent(f.events(t), task.EventAlert, "删清单项 驻留等人工") {
		t.Errorf("事件 = %+v, want 一条 alert", f.events(t))
	}

	// 驻留之后不再自动碰它：下一轮没有活。
	if next := f.runOnce(t); next.Due != 0 {
		t.Errorf("驻留后再跑 = %+v, want 不再捡起", next)
	}
}

// TestEngineSticksOnPermanentFailureImmediately 是「永久错立即驻留」（P12-3 的机制面）：
// token 被踢不烧重试预算——重试只会刷无效请求，人换了 token 再重发。
func TestEngineSticksOnPermanentFailureImmediately(t *testing.T) {
	f := newFixture(t)
	f.remover.err = &appapi.AuthError{Action: "JWTVerificationError", Message: "token 无效"}
	seedPendingRemoval(t, f.store, movieID, number, hashOne)

	result := f.runOnce(t)

	if result.Stuck != 1 {
		t.Fatalf("result = %+v, want 立即驻留", result)
	}
	stuck := f.jobs(t, sidefx.StateStuck)
	if len(stuck) != 1 || stuck[0].Attempts != 1 {
		t.Fatalf("驻留 = %+v, want attempts=1（一次就够）", stuck)
	}
	// 集成绝不被禁用：设置原样还在，行驻留而已。
	if len(f.settings.inputs) != 2 {
		t.Errorf("设置被改动了: %+v", f.settings.inputs)
	}
}

// TestEngineUsesConfiguredMaxAttempts：上限是设置项（app_config.max_attempts）。
func TestEngineUsesConfiguredMaxAttempts(t *testing.T) {
	f := newFixture(t)
	f.settings.snapshot.Sections["app"] = settings.SectionState{
		Configured: true,
		Data:       map[string]any{"sidefx_interval": "1m0s", "max_attempts": 1},
	}
	f.remover.err = &appapi.TransportError{Path: "/api/v1/lists", Err: errors.New("超时")}
	seedPendingRemoval(t, f.store, movieID, number, hashOne)

	result := f.runOnce(t)

	if result.Stuck != 1 {
		t.Fatalf("result = %+v, want 上限 1 → 第一次失败就驻留", result)
	}
}

// TestEngineSticksWhenTodoIsMalformed：待办缺字段是本地校验错（永久），
// 一次就驻留——重试变不出 payload 里没有的 id。
func TestEngineSticksWhenTodoIsMalformed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.outbox.Enqueue(ctx, sidefx.KindListRemoval, "wanted:"+movieID,
		map[string]any{"list_id": "wanted"}); err != nil {
		t.Fatalf("入箱: %v", err)
	}

	result := f.runOnce(t)

	if result.Stuck != 1 {
		t.Fatalf("result = %+v, want 立即驻留", result)
	}
	if len(f.remover.recorded()) != 0 {
		t.Error("坏待办不该打到上游")
	}
}

// ------------------------------------------------------------------ notify

// TestEngineSkipsNotifyWithoutConsumer：Telegram 的执行者が另一张票；
// 在那之前 notify 待办留在箱里、不烧预算、也不被误标成驻留。
func TestEngineSkipsNotifyWithoutConsumer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.outbox.EnqueueNotify(ctx, sidefx.EventDownloadStarted, hashOne, map[string]any{"number": number}); err != nil {
		t.Fatalf("入箱: %v", err)
	}

	result := f.runOnce(t)

	if result.Skipped != 1 || result.Done != 0 || result.Stuck != 0 {
		t.Fatalf("result = %+v, want 一条跳过", result)
	}
	jobs := f.jobs(t, sidefx.StatePending)
	if len(jobs) != 1 || jobs[0].Attempts != 0 || jobs[0].Stuck() {
		t.Fatalf("待办 = %+v, want 原样排队（attempts=0、没驻留）", jobs)
	}
}

// ------------------------------------------------------------------ jellyfin

// TestJellyfinRefreshCoalescesPerMovie 钉住 P11-1 的合并：同任务多版本只刷一次。
func TestJellyfinRefreshCoalescesPerMovie(t *testing.T) {
	f := newFixture(t)
	seedPendingRemoval(t, f.store, movieID, number, hashOne)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := f.outbox.Enqueue(ctx, sidefx.KindJellyfinRefresh, movieID, map[string]any{
			"movie_id": movieID, "number": number, "infohash": hashOne,
		}); err != nil {
			t.Fatalf("第 %d 次入箱: %v", i+1, err)
		}
	}

	f.runOnce(t)

	if got := f.refresher.recorded(); len(got) != 1 || got[0] != number {
		t.Fatalf("刷新调用 = %v, want 一条（同 movie 合并）", got)
	}
}

// TestJellyfinRefreshResolvesNumberFromTask：payload 没带番号时从任务行兜底——
// 番号是目录名，缺了就什么都刷不了。
func TestJellyfinRefreshResolvesNumberFromTask(t *testing.T) {
	f := newFixture(t)
	seedPendingRemoval(t, f.store, movieID, number, hashOne)
	if err := f.outbox.Enqueue(context.Background(), sidefx.KindJellyfinRefresh, movieID,
		map[string]any{"movie_id": movieID}); err != nil {
		t.Fatalf("入箱: %v", err)
	}

	f.runOnce(t)

	if got := f.refresher.recorded(); len(got) != 1 || got[0] != number {
		t.Fatalf("刷新调用 = %v, want [%s]（从任务行取番号）", got, number)
	}
}

// TestJellyfinRefreshFailureDoesNotTouchTask 钉住 P11-3：刷新失败只让待办退避，
// 任务状态仍是 finished、事件流照样说话；第 2 次失败起留 warning。
func TestJellyfinRefreshFailureDoesNotTouchTask(t *testing.T) {
	f := newFixture(t)
	seedPendingRemoval(t, f.store, movieID, number, hashOne)
	ctx := context.Background()
	if _, err := f.database.Pool().Exec(ctx,
		`UPDATE task_version SET status = 'finished' WHERE infohash = $1`, hashOne); err != nil {
		t.Fatalf("置 finished: %v", err)
	}
	if err := f.outbox.Enqueue(ctx, sidefx.KindJellyfinRefresh, movieID, map[string]any{
		"movie_id": movieID, "number": number, "infohash": hashOne,
	}); err != nil {
		t.Fatalf("入箱: %v", err)
	}
	f.refresher.err = &jellyfin.Error{Code: settings.CodeNetwork, Op: "Library/Media/Updated", Detail: "连不上 Jellyfin"}

	f.runOnce(t)

	version, err := f.store.Version(ctx, hashOne)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version.Status != task.StatusFinished {
		t.Fatalf("版本状态 = %q, want finished（刷新失败不阻塞入库）", version.Status)
	}
	// 第 1 次失败还没到 warning 阈值（规格 §5.5：两次失败 = warning）。
	for _, event := range f.events(t) {
		if event.Kind == task.EventAlert {
			t.Fatalf("第 1 次失败就告警了: %+v", event)
		}
	}

	job := f.jobs(t, sidefx.StatePending)[0]
	f.makeDue(t, job.ID)
	f.runOnce(t)

	if !hasEvent(f.events(t), task.EventAlert, "Jellyfin 刷新连续失败 2 次") {
		t.Errorf("事件 = %+v, want 第 2 次失败留 warning", f.events(t))
	}
}

// TestGatedRoundDoesNothing：门禁没开（未初始化/已暂停）时一条都不执行。
func TestGatedRoundDoesNothing(t *testing.T) {
	f := newFixture(t)
	f.settings.snapshot.Initialized = false
	seedPendingRemoval(t, f.store, movieID, number, hashOne)

	result := f.runOnce(t)

	if !result.Gated {
		t.Fatalf("result = %+v, want Gated", result)
	}
	if len(f.remover.recorded()) != 0 {
		t.Error("门禁没开却调了上游")
	}
	// 连入箱都不做：待删标记原样留着，门禁开了下一轮再翻成待办。
	version, err := f.store.Version(context.Background(), hashOne)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if !version.ListRemovalPending {
		t.Error("门禁没开时 list_removal_pending 被清掉了, want 原样留着")
	}
	if jobs := f.jobs(t, ""); len(jobs) != 0 {
		t.Errorf("待办 = %+v, want 零条（门禁没开不入箱）", jobs)
	}
}

// hasEvent 报告时间线里有没有一条 kind + reason 前缀都匹配的事件。
func hasEvent(events []task.Event, kind task.EventKind, reasonPrefix string) bool {
	for _, event := range events {
		if event.Kind == kind && len(event.Reason) >= len(reasonPrefix) && event.Reason[:len(reasonPrefix)] == reasonPrefix {
			return true
		}
	}
	return false
}
