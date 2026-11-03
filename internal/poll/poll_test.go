package poll_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/loops"
	"github.com/2017fighting/oh-my-mdc/internal/material"
	"github.com/2017fighting/oh-my-mdc/internal/mteam"
	"github.com/2017fighting/oh-my-mdc/internal/poll"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：poll.Runner.RunOnce —— 真 Postgres + 假 JavDB/qbt。
// 它覆盖整条磁力流水线：门禁、清单全量扫、定选、先落库再加种、去重、入箱、单飞。

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
	return f.inputs[section], nil
}

type fakeSource struct {
	items       []appapi.ListItem
	magnets     map[string][]appapi.Magnet
	listErr     error
	magnetErr   error
	listCalls   int
	magnetCalls []string
}

func (f *fakeSource) AllListItems(context.Context, string, string) ([]appapi.ListItem, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.items, nil
}

func (f *fakeSource) Magnets(_ context.Context, _ string, movieID string) ([]appapi.Magnet, error) {
	f.magnetCalls = append(f.magnetCalls, movieID)
	if f.magnetErr != nil {
		return nil, f.magnetErr
	}
	return f.magnets[movieID], nil
}

type fakeAdder struct {
	calls        []qbittorrent.Torrent
	configs      []qbittorrent.Config
	outcome      qbittorrent.Outcome
	err          error
	beforeEnsure func()
}

func (f *fakeAdder) Ensure(_ context.Context, cfg qbittorrent.Config, torrent qbittorrent.Torrent) (qbittorrent.Outcome, error) {
	if f.beforeEnsure != nil {
		f.beforeEnsure()
	}
	f.calls = append(f.calls, torrent)
	f.configs = append(f.configs, cfg)
	if f.err != nil {
		return "", f.err
	}
	if f.outcome == "" {
		return qbittorrent.Added, nil
	}
	return f.outcome, nil
}

type fakeTorrents struct {
	calls   []string
	keys    []string
	torrent mteam.Torrent
	err     error
}

func (f *fakeTorrents) Resolve(_ context.Context, apiKey, number string) (mteam.Torrent, error) {
	f.calls = append(f.calls, number)
	f.keys = append(f.keys, apiKey)
	if f.err != nil {
		return mteam.Torrent{}, f.err
	}
	return f.torrent, nil
}

// ------------------------------------------------------------------ fixture

type fixture struct {
	database *db.DB
	store    *task.Store
	source   *fakeSource
	adder    *fakeAdder
	torrents *fakeTorrents
	settings *fakeSettings
	runner   *poll.Runner
	loops    *loops.Registry
	// torrentsDir 是 .torrent 备份目录（每个 fixture 一个临时目录）。
	torrentsDir string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	database := dbtest.Migrated(t)
	if _, err := database.Pool().Exec(context.Background(),
		`INSERT INTO list_registry (list_id, purpose, name) VALUES ('wanted', 'want_watch', '想看清单')`,
	); err != nil {
		t.Fatalf("种清单登记失败: %v", err)
	}

	source := &fakeSource{magnets: map[string][]appapi.Magnet{}}
	adder := &fakeAdder{}
	torrents := &fakeTorrents{}
	settingsFake := readySettings()

	torrentsDir := t.TempDir()
	store := task.NewStore(database.Pool())
	registry := loops.NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// 材料仓库显式注入：poll 自己也要落盘，测试要能指定备份目录并断言重取。
	materials := material.New(material.Options{
		Dir:      torrentsDir,
		Torrents: torrents,
		Settings: settingsFake,
		Logger:   logger,
	})
	runner := poll.New(poll.Options{
		Store:    store,
		Outbox:   sidefx.New(database.Pool()),
		Settings: settingsFake,
		Source:   source,
		Adder:    adder,
		Torrents: torrents,
		Material: materials,
		Pool:     database.Pool(),
		Loops:    registry,
		Owner:    "test-owner",
		Logger:   logger,
	})
	return &fixture{
		database:    database,
		store:       store,
		source:      source,
		adder:       adder,
		torrents:    torrents,
		settings:    settingsFake,
		runner:      runner,
		loops:       registry,
		torrentsDir: torrentsDir,
	}
}

func readySettings() *fakeSettings {
	return &fakeSettings{
		snapshot: settings.Snapshot{
			Initialized: true,
			Sections: map[string]settings.SectionState{
				"list_registry": {
					Configured: true,
					Data: map[string]any{"entries": []settings.ListEntry{
						{ListID: "wanted", Purpose: "want_watch", Name: "想看清单"},
					}},
				},
				"app": {Configured: true, Data: map[string]any{"poll_interval": "15m0s"}},
			},
		},
		inputs: map[string]settings.TestInput{
			"javdb":       {Values: map[string]any{"token": "tk"}},
			"qbittorrent": {Values: map[string]any{"url": "http://qbt:8080", "category": "mdc", "download_root": "/media/downloads"}},
		},
	}
}

const (
	movieID     = "YwrO3B"
	number      = "ABP-123"
	plainHash   = "1111111111111111111111111111111111111111"
	chineseHash = "2222222222222222222222222222222222222222"
)

// withOneMovie 让清单里有这一部片，并给它两条磁力（一条普通大体积、一条中字小体积）。
func (f *fixture) withOneMovie(t *testing.T) {
	t.Helper()
	f.source.items = []appapi.ListItem{{MovieID: movieID, Number: number}}
	f.source.magnets[movieID] = []appapi.Magnet{
		{Hash: plainHash, Cnsub: false, HD: true, SizeMiB: 8192, CreatedAt: time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)},
		{Hash: chineseHash, Cnsub: true, HD: false, SizeMiB: 1024, CreatedAt: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)},
	}
}

func (f *fixture) detail(t *testing.T) task.TaskRow {
	t.Helper()
	row, err := f.store.Detail(context.Background(), movieID, 50)
	if err != nil {
		t.Fatalf("Detail(%s): %v", movieID, err)
	}
	return row
}

func (f *fixture) sideEffectKeys(t *testing.T, kind sidefx.Kind) []string {
	t.Helper()
	rows, err := f.database.Pool().Query(context.Background(),
		`SELECT target_key FROM side_effect WHERE kind = $1 ORDER BY id`, string(kind))
	if err != nil {
		t.Fatalf("读 side_effect: %v", err)
	}
	defer rows.Close()

	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("扫 side_effect: %v", err)
		}
		keys = append(keys, key)
	}
	return keys
}

// ------------------------------------------------------------------ tests

// TestPollPersistsBeforeAdding 是 P7-1/P7-2 的主链：
// 定选 → 版本行先落库（add_pending=true）→ 加种（显式 savepath/番号）→ 清 pending、
// 置清单项待删、通知入箱、记事件。
func TestPollPersistsBeforeAdding(t *testing.T) {
	f := newFixture(t)
	f.withOneMovie(t)

	f.adder.beforeEnsure = func() {
		row := f.detail(t)
		if len(row.Versions) != 1 {
			t.Fatalf("加种时版本数 = %d, want 1（先落库再加种）", len(row.Versions))
		}
		if !row.Versions[0].AddPending {
			t.Fatal("加种时 add_pending = false, want true（先落库再加种）")
		}
	}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Scanned != 1 || result.Queried != 1 || result.Candidates != 1 || result.Added != 1 {
		t.Errorf("result = %+v, want scanned/queried/candidates/added 各 1", result)
	}

	row := f.detail(t)
	if row.State != string(task.StatusDownloading) {
		t.Errorf("state = %q, want downloading", row.State)
	}
	version := row.Versions[0]
	if version.Infohash != chineseHash {
		t.Errorf("选中的 infohash = %s, want 中字那条 %s（cnsub 优先）", version.Infohash, chineseHash)
	}
	if version.AddPending {
		t.Error("add_pending = true, want false（加种成功要清）")
	}
	if !version.ListRemovalPending {
		t.Error("list_removal_pending = false, want true（置清单项待删）")
	}
	if version.Label != "中文" {
		t.Errorf("label = %q, want 中文（cnsub → 中文）", version.Label)
	}
	if version.Source != task.SourceJavDBMagnet || version.Delivery != task.DeliveryMagnet {
		t.Errorf("source/delivery = %s/%s, want javdb_magnet/magnet", version.Source, version.Delivery)
	}
	wantMagnet := "magnet:?xt=urn:btih:" + chineseHash + "&dn=" + number
	if version.Payload != wantMagnet {
		t.Errorf("payload = %q, want %q", version.Payload, wantMagnet)
	}

	if len(f.adder.calls) != 1 {
		t.Fatalf("加种调用 = %d, want 1", len(f.adder.calls))
	}
	added := f.adder.calls[0]
	if added.Number != number {
		t.Errorf("加种番号 = %q, want %q（tag 三件套靠它）", added.Number, number)
	}
	if added.SavePath != "/media/downloads" {
		t.Errorf("加种 savepath = %q, want 显式用 download_root", added.SavePath)
	}
	if added.Magnet != wantMagnet {
		t.Errorf("加种磁力 = %q, want %q", added.Magnet, wantMagnet)
	}

	if keys := f.sideEffectKeys(t, sidefx.KindNotify); len(keys) != 1 || keys[0] != sidefx.EventDownloadStarted+":"+chineseHash {
		t.Errorf("通知待办 = %v, want [download_started:%s]", keys, chineseHash)
	}
}

// TestPollWritesNothingWhenMagnetsAreEmpty 钉住 P7-3：磁力为空零行落库。
func TestPollWritesNothingWhenMagnetsAreEmpty(t *testing.T) {
	f := newFixture(t)
	f.source.items = []appapi.ListItem{{MovieID: movieID, Number: number}}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Candidates != 0 || result.Added != 0 {
		t.Errorf("result = %+v, want 0 候选 0 加种", result)
	}
	if len(f.source.magnetCalls) != 1 {
		t.Errorf("磁力查询 = %v, want 查了一次（空也是答案）", f.source.magnetCalls)
	}
	if len(f.adder.calls) != 0 {
		t.Errorf("加种调用 = %d, want 0", len(f.adder.calls))
	}

	rows, err := f.store.List(context.Background(), task.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("任务行 = %d, want 0（磁力为空不落任何行）", len(rows))
	}
}

// TestPollDoesNotQuerySourceAgainWhenTaskHasVersion 钉住 P7-6。
func TestPollDoesNotQuerySourceAgainWhenTaskHasVersion(t *testing.T) {
	f := newFixture(t)
	seedCompletedVersion(t, f)
	f.source.items = []appapi.ListItem{{MovieID: movieID, Number: number}}

	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(f.source.magnetCalls) != 0 {
		t.Errorf("磁力查询 = %v, want 0（任务已有版本不再查源）", f.source.magnetCalls)
	}
	if len(f.adder.calls) != 0 {
		t.Errorf("加种调用 = %d, want 0", len(f.adder.calls))
	}
}

// TestPollReentryIsZeroWrite 钉住 P7-4：同 infohash 重入不重复加种、不重复入箱。
func TestPollReentryIsZeroWrite(t *testing.T) {
	f := newFixture(t)
	f.withOneMovie(t)

	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("第一轮 RunOnce: %v", err)
	}
	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("第二轮 RunOnce: %v", err)
	}

	if len(f.adder.calls) != 1 {
		t.Errorf("加种调用 = %d, want 1（重入不重复加种）", len(f.adder.calls))
	}
	if len(f.source.magnetCalls) != 1 {
		t.Errorf("磁力查询 = %v, want 只查一次（任务已有版本）", f.source.magnetCalls)
	}
	if keys := f.sideEffectKeys(t, sidefx.KindNotify); len(keys) != 1 {
		t.Errorf("通知待办 = %v, want 1", keys)
	}

	row := f.detail(t)
	if len(row.Versions) != 1 {
		t.Errorf("版本数 = %d, want 1（同 infohash 不新建行）", len(row.Versions))
	}
}

// TestPollRetriesPendingAddFromCrashedRun 钉住 ADR-0004 的自愈：
// 版本行已落库、加种没做成（进程崩了），下一轮不再查源也能补上。
func TestPollRetriesPendingAddFromCrashedRun(t *testing.T) {
	f := newFixture(t)
	inserted, err := f.store.SaveCandidate(context.Background(), candidate())
	if err != nil || !inserted {
		t.Fatalf("预置版本行: inserted=%v err=%v", inserted, err)
	}
	f.source.items = []appapi.ListItem{{MovieID: movieID, Number: number}}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Added != 1 {
		t.Errorf("added = %d, want 1（pending 标记照旧重试）", result.Added)
	}
	if len(f.source.magnetCalls) != 0 {
		t.Errorf("磁力查询 = %v, want 0（任务已有版本）", f.source.magnetCalls)
	}
	if f.detail(t).Versions[0].AddPending {
		t.Error("add_pending = true, want false")
	}
}

// TestPollKeepsPendingWhenAddFailsAndRetriesNextRound 钉住「失败 → 告警下轮重试」。
func TestPollKeepsPendingWhenAddFailsAndRetriesNextRound(t *testing.T) {
	f := newFixture(t)
	f.withOneMovie(t)
	f.adder.err = errors.New("qBittorrent 不可达")

	if _, err := f.runner.RunOnce(context.Background()); err == nil {
		t.Fatal("加种失败时 RunOnce 应当回错误（循环健康位要红）")
	}
	row := f.detail(t)
	if !row.Versions[0].AddPending {
		t.Error("add_pending = false, want true（失败保留 pending）")
	}
	if !hasEvent(row, task.EventAlert) {
		t.Errorf("events = %+v, want 一条加种失败告警", row.Events)
	}
	if keys := f.sideEffectKeys(t, sidefx.KindNotify); len(keys) != 0 {
		t.Errorf("通知待办 = %v, want 0（没加种成功不该发下载开始）", keys)
	}

	f.adder.err = nil
	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("第二轮 RunOnce: %v", err)
	}
	if result.Added != 1 {
		t.Errorf("added = %d, want 1（下轮重试成功）", result.Added)
	}
	if f.detail(t).Versions[0].AddPending {
		t.Error("add_pending = true, want false（重试成功后清）")
	}
}

// TestPollTreatsEverySuccessOutcomeTheSame 确认收编（Adopted）与已带项目 tag（AlreadyTracked）
// 和 Added 一样：清 pending、置待删、发「下载开始」（规格 §5.6）。
func TestPollTreatsEverySuccessOutcomeTheSame(t *testing.T) {
	for _, outcome := range []qbittorrent.Outcome{qbittorrent.Added, qbittorrent.Adopted, qbittorrent.AlreadyTracked} {
		t.Run(string(outcome), func(t *testing.T) {
			f := newFixture(t)
			f.withOneMovie(t)
			f.adder.outcome = outcome

			if _, err := f.runner.RunOnce(context.Background()); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			version := f.detail(t).Versions[0]
			if version.AddPending || !version.ListRemovalPending {
				t.Errorf("add_pending/list_removal_pending = %v/%v, want false/true",
					version.AddPending, version.ListRemovalPending)
			}
			if keys := f.sideEffectKeys(t, sidefx.KindNotify); len(keys) != 1 {
				t.Errorf("通知待办 = %v, want 1（收编同发）", keys)
			}
		})
	}
}

// TestPollIsGatedUntilInitializedAndNotPaused 钉住门禁与暂停：零上游调用、零落库。
func TestPollIsGatedUntilInitializedAndNotPaused(t *testing.T) {
	t.Run("未初始化", func(t *testing.T) {
		f := newFixture(t)
		f.settings.snapshot.Initialized = false
		f.withOneMovie(t)

		result, err := f.runner.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if !result.Gated {
			t.Error("result.Gated = false, want true")
		}
		if f.source.listCalls != 0 || len(f.adder.calls) != 0 {
			t.Errorf("上游调用 = list:%d add:%d, want 0", f.source.listCalls, len(f.adder.calls))
		}
	})

	t.Run("已暂停", func(t *testing.T) {
		f := newFixture(t)
		f.settings.snapshot.Paused = true
		f.withOneMovie(t)

		result, err := f.runner.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if !result.Gated {
			t.Error("result.Gated = false, want true")
		}
		if f.source.listCalls != 0 {
			t.Errorf("清单调用 = %d, want 0", f.source.listCalls)
		}
	})
}

// TestPollIsSingleFlightAcrossInstances 钉住 §4.4：别人持着 advisory lock 时这一轮让路。
func TestPollIsSingleFlightAcrossInstances(t *testing.T) {
	f := newFixture(t)
	f.withOneMovie(t)
	ctx := context.Background()

	holder, err := f.database.Pool().Acquire(ctx)
	if err != nil {
		t.Fatalf("取占锁连接: %v", err)
	}
	defer holder.Release()

	var locked bool
	if err := holder.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", poll.LockIDPoll).Scan(&locked); err != nil || !locked {
		t.Fatalf("前置条件坏了：拿不到 advisory lock (locked=%v err=%v)", locked, err)
	}

	result, err := f.runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !result.Locked {
		t.Error("result.Locked = false, want true")
	}
	if f.source.listCalls != 0 {
		t.Errorf("清单调用 = %d, want 0（单飞让路）", f.source.listCalls)
	}

	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", poll.LockIDPoll); err != nil {
		t.Fatalf("放锁: %v", err)
	}
	if _, err := f.runner.RunOnce(ctx); err != nil {
		t.Fatalf("放锁后 RunOnce: %v", err)
	}
	if f.source.listCalls != 1 {
		t.Errorf("清单调用 = %d, want 1（放锁后正常跑）", f.source.listCalls)
	}
}

// ------------------------------------------------------------------ helpers

func candidate() task.Candidate {
	return task.Candidate{
		MovieID:  movieID,
		Number:   number,
		ListID:   "wanted",
		Infohash: chineseHash,
		Source:   task.SourceJavDBMagnet,
		Delivery: task.DeliveryMagnet,
		Label:    "中文",
		Payload:  "magnet:?xt=urn:btih:" + chineseHash,
	}
}

func seedCompletedVersion(t *testing.T, f *fixture) {
	t.Helper()
	ctx := context.Background()
	if inserted, err := f.store.SaveCandidate(ctx, candidate()); err != nil || !inserted {
		t.Fatalf("预置版本: inserted=%v err=%v", inserted, err)
	}
	if _, err := f.store.Claim(ctx, chineseHash, "seed-owner"); err != nil {
		t.Fatalf("预置 claim: %v", err)
	}
	if err := f.store.MarkAdded(ctx, chineseHash, "seed-owner"); err != nil {
		t.Fatalf("预置 MarkAdded: %v", err)
	}
}

func hasEvent(row task.TaskRow, kind task.EventKind) bool {
	for _, event := range row.Events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

// TestRunOnceRecordsLoopHealth 钉住 poll 与 §9 循环指标的接线：成功的轮次推 last_success、
// 失败的轮次只涨 error 计数（last_success 停在原处），门禁没开的轮次什么都不记。
func TestRunOnceRecordsLoopHealth(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.runner.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	health := f.loops.Health(loops.Poll)
	if health.LastOK == nil || !*health.LastOK {
		t.Fatalf("LastOK = %v, want true", health.LastOK)
	}
	if health.LastSuccessAt == nil {
		t.Fatal("成功的轮次该写 last_success")
	}
	if health.Iterations.OK != 1 || health.Iterations.Error != 0 {
		t.Errorf("Iterations = %+v, want ok=1 error=0", health.Iterations)
	}
	successAt := *health.LastSuccessAt

	f.source.listErr = errors.New("JavDB 挂了")
	if _, err := f.runner.RunOnce(ctx); err == nil {
		t.Fatal("清单读失败该让这一轮返回错误")
	}
	health = f.loops.Health(loops.Poll)
	if health.LastOK == nil || *health.LastOK {
		t.Errorf("LastOK = %v, want false", health.LastOK)
	}
	if health.LastSuccessAt == nil || !health.LastSuccessAt.Equal(successAt) {
		t.Errorf("LastSuccessAt = %v, want 停在 %v（失败不刷新它）", health.LastSuccessAt, successAt)
	}
	if health.Iterations.OK != 1 || health.Iterations.Error != 1 {
		t.Errorf("Iterations = %+v, want ok=1 error=1", health.Iterations)
	}

	// 门禁没开（未初始化）：这一轮不算一次迭代。
	f.settings.snapshot.Initialized = false
	if _, err := f.runner.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce(gated): %v", err)
	}
	if health := f.loops.Health(loops.Poll); health.Iterations.OK != 1 || health.Iterations.Error != 1 {
		t.Errorf("门禁轮次不该计入迭代: %+v", health.Iterations)
	}
}
