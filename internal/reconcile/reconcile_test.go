package reconcile_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/organize"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/reconcile"
	"github.com/2017fighting/oh-my-mdc/internal/scrape"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：reconcile.Runner 的两个入口（HandleWebhook / RunOnce）+ 真 Postgres + 假 qbt。
// 断言只看版本状态、任务事件与「qbt 有没有被问」，不看引擎内部结构。

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

// fakeQbt 是 qbt 侧的权威读数：Lookup 按键查表，Tagged 回 tag 扫描结果。
type fakeQbt struct {
	mu       sync.Mutex
	statuses map[string]qbittorrent.TorrentStatus
	tagged   []qbittorrent.TorrentStatus
	found    map[string]bool
	lookups  []string
	tagCalls []string
	lookErr  error
	tagErr   error
}

func newFakeQbt() *fakeQbt {
	return &fakeQbt{
		statuses: map[string]qbittorrent.TorrentStatus{},
		found:    map[string]bool{},
	}
}

func (f *fakeQbt) Lookup(_ context.Context, _ qbittorrent.Config, hash string) (qbittorrent.TorrentStatus, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, hash)
	if f.lookErr != nil {
		return qbittorrent.TorrentStatus{}, false, f.lookErr
	}
	if !f.found[hash] {
		return qbittorrent.TorrentStatus{}, false, nil
	}
	status, ok := f.statuses[hash]
	if !ok {
		status = qbittorrent.TorrentStatus{Hash: hash}
	}
	return status, true, nil
}

func (f *fakeQbt) Tagged(_ context.Context, _ qbittorrent.Config, tag string) ([]qbittorrent.TorrentStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tagCalls = append(f.tagCalls, tag)
	if f.tagErr != nil {
		return nil, f.tagErr
	}
	return f.tagged, nil
}

func (f *fakeQbt) lookedUp() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lookups...)
}

// ------------------------------------------------------------------ fixture

type fixture struct {
	database *db.DB
	store    *task.Store
	qbt      *fakeQbt
	settings *fakeSettings
	runner   *reconcile.Runner
	opts     reconcile.Options
	now      time.Time
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
		database: database,
		store:    task.NewStore(database.Pool()),
		qbt:      newFakeQbt(),
		settings: readySettings(),
		now:      time.Date(2025, 4, 1, 12, 0, 0, 0, time.UTC),
	}
	f.opts = reconcile.Options{
		Store:    f.store,
		Outbox:   sidefx.New(database.Pool()),
		Settings: f.settings,
		Qbt:      f.qbt,
		Pool:     database.Pool(),
		Owner:    "test-owner",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:      func() time.Time { return f.now },
	}
	f.runner = reconcile.New(f.opts)
	return f
}

// withProcessor 换上加工阶段的假推进器，重建引擎（其它依赖不变）。
func (f *fixture) withProcessor(processor reconcile.Processor) {
	f.opts.Processor = processor
	f.runner = reconcile.New(f.opts)
}

// withOrganizer 换上整理段的假推进器。
func (f *fixture) withOrganizer(organizer reconcile.Organizer) {
	f.opts.Organizer = organizer
	f.runner = reconcile.New(f.opts)
}

func readySettings() *fakeSettings {
	return &fakeSettings{
		snapshot: settings.Snapshot{
			Initialized: true,
			Sections: map[string]settings.SectionState{
				"app": {Configured: true, Data: map[string]any{"reconcile_interval": "5m0s"}},
			},
		},
		inputs: map[string]settings.TestInput{
			"qbittorrent": {Values: map[string]any{
				"url": "http://qbt:8080", "category": "mdc", "download_root": "/media/downloads",
			}},
		},
	}
}

// seedAddedVersion 种一条「已加种、还在下载」的版本（对账会看见它）。
func (f *fixture) seedAddedVersion(t *testing.T, movieID, number, infohash string) {
	t.Helper()
	ctx := context.Background()
	inserted, err := f.store.SaveCandidate(ctx, task.Candidate{
		MovieID: movieID, Number: number, ListID: "wanted",
		Infohash: infohash, Source: task.SourceJavDBMagnet, Delivery: task.DeliveryMagnet,
		Payload: "magnet:?xt=urn:btih:" + infohash,
	})
	if err != nil || !inserted {
		t.Fatalf("种版本 %s: inserted=%v err=%v", infohash, inserted, err)
	}
	if _, err := f.store.Claim(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("Claim(%s): %v", infohash, err)
	}
	if err := f.store.MarkAdded(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("MarkAdded(%s): %v", infohash, err)
	}
}

func (f *fixture) setStatus(t *testing.T, infohash string, status task.Status) {
	t.Helper()
	if _, err := f.database.Pool().Exec(context.Background(),
		`UPDATE task_version SET status = $1 WHERE infohash = $2`, string(status), infohash); err != nil {
		t.Fatalf("设置状态: %v", err)
	}
}

func (f *fixture) version(t *testing.T, infohash string) task.Version {
	t.Helper()
	version, err := f.store.Version(context.Background(), infohash)
	if err != nil {
		t.Fatalf("Version(%s): %v", infohash, err)
	}
	return version
}

func (f *fixture) events(t *testing.T, movieID string) []task.Event {
	t.Helper()
	events, err := f.store.Events(context.Background(), movieID, 100)
	if err != nil {
		t.Fatalf("Events(%s): %v", movieID, err)
	}
	return events
}

// completed 让 qbt 说这条种子下载完了：progress=1、状态 uploading、落盘路径可读含视频。
func (f *fixture) completed(t *testing.T, hash string) {
	t.Helper()
	f.qbt.found[hash] = true
	f.qbt.statuses[hash] = qbittorrent.TorrentStatus{
		Hash: hash, Progress: 1, State: "uploading",
		ContentPath: videoDir(t, hash),
	}
}

// downloading 让 qbt 说这条种子还在下载：progress 没到 1。
func (f *fixture) downloading(hash string, progress float64) {
	f.qbt.found[hash] = true
	f.qbt.statuses[hash] = qbittorrent.TorrentStatus{
		Hash: hash, Progress: progress, State: "downloading",
		ContentPath: "/media/downloads/" + hash,
	}
}

// videoDir 建一个含视频文件的目录（content_path 的常态形态）。
func videoDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ABP-123.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatalf("写视频: %v", err)
	}
	return dir
}

const (
	movieID = "YwrO3B"
	number  = "ABP-123"
	hashOne = "1111111111111111111111111111111111111111"
	hashTwo = "2222222222222222222222222222222222222222"
)

// ------------------------------------------------------------------ webhook

// TestWebhookAdvancesCompletedDownload 是 P8-1 的主链：回调只带 infohash，
// 引擎回读 qbt 权威值，三条件齐了就推进 processing 并留一条时间线。
func TestWebhookAdvancesCompletedDownload(t *testing.T) {
	f := newFixture(t)
	f.seedAddedVersion(t, movieID, number, hashOne)
	f.completed(t, hashOne)

	if err := f.runner.HandleWebhook(context.Background(), hashOne); err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}

	if got := f.version(t, hashOne).Status; got != task.StatusProcessing {
		t.Fatalf("status = %q, want processing", got)
	}
	if lookups := f.qbt.lookedUp(); len(lookups) != 1 || lookups[0] != hashOne {
		t.Errorf("qbt 回读 = %v, want 只回读 %s（载荷不可信）", lookups, hashOne)
	}

	changes := transitions(t, f, movieID)
	if len(changes) != 1 || changes[0].ToState != string(task.StatusProcessing) {
		t.Errorf("状态变化 = %+v, want 一条 downloading→processing", changes)
	}
}

// TestWebhookIgnoresUnknownInfohash 钉住 P8-2：未知/非本项目 infohash 静默 no-op，零写入零上游调用。
func TestWebhookIgnoresUnknownInfohash(t *testing.T) {
	f := newFixture(t)
	unknown := "ffffffffffffffffffffffffffffffffffffffff"

	if err := f.runner.HandleWebhook(context.Background(), unknown); err != nil {
		t.Fatalf("HandleWebhook(未知) = %v, want nil（恒 200 的静默 no-op）", err)
	}
	if err := f.runner.HandleWebhook(context.Background(), "not-a-hash"); err != nil {
		t.Fatalf("HandleWebhook(非法) = %v, want nil", err)
	}

	if lookups := f.qbt.lookedUp(); len(lookups) != 0 {
		t.Errorf("qbt 回读 = %v, want 0（不是我们的就不问 qbt）", lookups)
	}
	rows, err := f.store.List(context.Background(), task.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("任务行 = %d, want 0", len(rows))
	}
	if events := f.events(t, movieID); len(events) != 0 {
		t.Errorf("事件 = %d, want 0", len(events))
	}
}

// TestWebhookWaitsWhileNotComplete 钉住三条件的否定面：checking/moving/progress<1 等下轮。
func TestWebhookWaitsWhileNotComplete(t *testing.T) {
	cases := []struct {
		name   string
		status qbittorrent.TorrentStatus
		found  bool
	}{
		{"checking 中", qbittorrent.TorrentStatus{Progress: 1, State: "checkingUP"}, true},
		{"moving 中", qbittorrent.TorrentStatus{Progress: 1, State: "moving"}, true},
		{"进度未满", qbittorrent.TorrentStatus{Progress: 0.99, State: "downloading"}, true},
		{"做种但状态不认识", qbittorrent.TorrentStatus{Progress: 1, State: "queuedUP"}, true},
		{"qbt 还没报出落盘路径", qbittorrent.TorrentStatus{Progress: 1, State: "uploading"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.seedAddedVersion(t, movieID, number, hashOne)
			f.qbt.found[hashOne] = tc.found
			f.qbt.statuses[hashOne] = tc.status

			if err := f.runner.HandleWebhook(context.Background(), hashOne); err != nil {
				t.Fatalf("HandleWebhook: %v", err)
			}
			if got := f.version(t, hashOne).Status; got != task.StatusDownloading {
				t.Errorf("status = %q, want downloading（条件不齐等下轮）", got)
			}
			if changes := transitions(t, f, movieID); len(changes) != 0 {
				t.Errorf("状态变化 = %+v, want 0（等待不是状态变化）", changes)
			}
		})
	}
}

// TestWebhookFailsWhenSeedIsGone 钉住「种子消失 → failed(下载阶段, 种子已移除) + 事件」。
func TestWebhookFailsWhenSeedIsGone(t *testing.T) {
	f := newFixture(t)
	f.seedAddedVersion(t, movieID, number, hashOne)
	f.qbt.found[hashOne] = false

	if err := f.runner.HandleWebhook(context.Background(), hashOne); err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}

	version := f.version(t, hashOne)
	if version.Status != task.StatusFailed {
		t.Fatalf("status = %q, want failed", version.Status)
	}
	if version.ErrorPhase != task.PhaseDownloading || version.ErrorReason != reconcile.ReasonSeedGone {
		t.Errorf("error = %q/%q, want downloading/%q", version.ErrorPhase, version.ErrorReason, reconcile.ReasonSeedGone)
	}

	changes := transitions(t, f, movieID)
	if len(changes) != 1 || changes[0].ToState != string(task.StatusFailed) ||
		changes[0].Phase != string(task.PhaseDownloading) || changes[0].Reason != reconcile.ReasonSeedGone {
		t.Errorf("状态变化 = %+v, want 一条 failed(下载阶段, %q)", changes, reconcile.ReasonSeedGone)
	}
}

// TestWebhookFailsWhenFilesAreMissing 钉住「完成但文件缺 → failed(下载阶段, 文件丢失)」。
func TestWebhookFailsWhenFilesAreMissing(t *testing.T) {
	cases := []struct {
		name        string
		state       string
		contentPath func(t *testing.T) string
	}{
		{"路径不可读", "uploading", func(t *testing.T) string { return filepath.Join(t.TempDir(), "gone") }},
		{"目录里没有视频", "stalledUP", func(t *testing.T) string {
			dir := filepath.Join(t.TempDir(), "only-text")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("建目录: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0o644); err != nil {
				t.Fatalf("写文件: %v", err)
			}
			return dir
		}},
		{"qbt 说文件缺失", "missingFiles", func(t *testing.T) string { return videoDir(t, "present") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.seedAddedVersion(t, movieID, number, hashOne)
			f.qbt.found[hashOne] = true
			f.qbt.statuses[hashOne] = qbittorrent.TorrentStatus{
				Hash: hashOne, Progress: 1, State: tc.state, ContentPath: tc.contentPath(t),
			}

			if err := f.runner.HandleWebhook(context.Background(), hashOne); err != nil {
				t.Fatalf("HandleWebhook: %v", err)
			}

			version := f.version(t, hashOne)
			if version.Status != task.StatusFailed {
				t.Fatalf("status = %q, want failed", version.Status)
			}
			if version.ErrorPhase != task.PhaseDownloading || version.ErrorReason != reconcile.ReasonFilesMissing {
				t.Errorf("error = %q/%q, want downloading/%q", version.ErrorPhase, version.ErrorReason, reconcile.ReasonFilesMissing)
			}
		})
	}
}

// TestWebhookLosesClaimSilently 钉住并发互斥：别人拿着租约时，回调不推进、不报失败。
func TestWebhookLosesClaimSilently(t *testing.T) {
	f := newFixture(t)
	f.seedAddedVersion(t, movieID, number, hashOne)
	f.completed(t, hashOne)

	if _, err := f.store.Claim(context.Background(), hashOne, "other-owner"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := f.runner.HandleWebhook(context.Background(), hashOne); err != nil {
		t.Fatalf("HandleWebhook: %v（抢输不是失败）", err)
	}
	if got := f.version(t, hashOne).Status; got != task.StatusDownloading {
		t.Errorf("status = %q, want downloading（别人的账不许改）", got)
	}
}

// TestConcurrentWebhooksAdvanceOnce 钉住「webhook 与对账同版本竞争时 claim 生效」：
// 同一版本并发推进只落一次状态变化。
func TestConcurrentWebhooksAdvanceOnce(t *testing.T) {
	f := newFixture(t)
	f.seedAddedVersion(t, movieID, number, hashOne)
	f.completed(t, hashOne)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- f.runner.HandleWebhook(context.Background(), hashOne)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("HandleWebhook: %v", err)
		}
	}

	if got := f.version(t, hashOne).Status; got != task.StatusProcessing {
		t.Fatalf("status = %q, want processing", got)
	}
	if changes := transitions(t, f, movieID); len(changes) != 1 {
		t.Errorf("状态变化 = %+v, want 1 条（不双跑）", changes)
	}
}

// TestWebhookAndReconcileRaceAdvanceOnce 钉住「webhook 与对账同版本竞争时 claim 生效」：
// 两条入口并发推进同一版本，只落一次状态变化。
func TestWebhookAndReconcileRaceAdvanceOnce(t *testing.T) {
	f := newFixture(t)
	f.seedAddedVersion(t, movieID, number, hashOne)
	f.completed(t, hashOne)
	f.qbt.tagged = []qbittorrent.TorrentStatus{{Hash: hashOne, Progress: 1, State: "uploading"}}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs <- f.runner.HandleWebhook(context.Background(), hashOne)
	}()
	go func() {
		defer wg.Done()
		_, err := f.runner.RunOnce(context.Background())
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发推进: %v", err)
		}
	}

	if got := f.version(t, hashOne).Status; got != task.StatusProcessing {
		t.Fatalf("status = %q, want processing", got)
	}
	if changes := transitions(t, f, movieID); len(changes) != 1 {
		t.Errorf("状态变化 = %+v, want 1 条（claim 互斥，不双跑）", changes)
	}
}

// ------------------------------------------------------------------ reconcile

// TestReconcileCatchesMissedWebhookByTag 是 P8-3：webhook 丢了，5 分钟对账按
// mdc:in-progress 的 tag 扫到它，再照同一条判定推进。
func TestReconcileCatchesMissedWebhookByTag(t *testing.T) {
	f := newFixture(t)
	f.seedAddedVersion(t, movieID, number, hashOne)
	f.completed(t, hashOne)
	f.qbt.tagged = []qbittorrent.TorrentStatus{{Hash: hashOne, Progress: 0.4, State: "downloading"}}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Advanced != 1 {
		t.Errorf("result = %+v, want 1 条推进", result)
	}
	if got := f.version(t, hashOne).Status; got != task.StatusProcessing {
		t.Errorf("status = %q, want processing（tag 是线索，判定回读 qbt）", got)
	}
	if calls := f.qbt.tagCalls; len(calls) != 1 || calls[0] != qbittorrent.TagInProgress {
		t.Errorf("tag 扫描 = %v, want [%s]", calls, qbittorrent.TagInProgress)
	}
}

// TestReconcileSkipsFailedVersions 钉住「已 failed 的版本直接跳过（tag 只是发现线索）」。
func TestReconcileSkipsFailedVersions(t *testing.T) {
	f := newFixture(t)
	f.seedAddedVersion(t, movieID, number, hashOne)
	f.setStatus(t, hashOne, task.StatusFailed)
	f.completed(t, hashOne)
	f.qbt.tagged = []qbittorrent.TorrentStatus{{Hash: hashOne, Progress: 1, State: "uploading"}}

	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if lookups := f.qbt.lookedUp(); len(lookups) != 0 {
		t.Errorf("qbt 回读 = %v, want 0（DB 说已失败就不必再看 qbt）", lookups)
	}
	if got := f.version(t, hashOne).Status; got != task.StatusFailed {
		t.Errorf("status = %q, want failed（不许被 tag 改回来）", got)
	}
}

// TestReconcileFailsWhenSeedDisappeared 钉住「对账发现种子消失 → failed + 事件」：
// 种子消失后它连 tag 扫描都不会出现，所以发现面必须包含库里所有下载中的版本。
func TestReconcileFailsWhenSeedDisappeared(t *testing.T) {
	f := newFixture(t)
	f.seedAddedVersion(t, movieID, number, hashOne)
	// qbt 里没有它（found 默认 false），tag 扫描也扫不到。

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Failed != 1 {
		t.Errorf("result = %+v, want 1 条失败", result)
	}

	version := f.version(t, hashOne)
	if version.Status != task.StatusFailed || version.ErrorReason != reconcile.ReasonSeedGone {
		t.Errorf("version = %+v, want failed/%q", version, reconcile.ReasonSeedGone)
	}
	if changes := transitions(t, f, movieID); len(changes) != 1 || changes[0].ToState != string(task.StatusFailed) {
		t.Errorf("状态变化 = %+v, want 1 条 failed 事件", changes)
	}
}

// TestReconcileAlertsStallOnce 钉住停滞语义：progress 60 分钟不动 → 一次性告警，
// 保持 downloading（不 failed、不动状态）。
func TestReconcileAlertsStallOnce(t *testing.T) {
	f := newFixture(t)
	f.seedAddedVersion(t, movieID, number, hashOne)
	f.downloading(hashOne, 0.4)

	// 第一轮只是记下进度起点。
	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce(1): %v", err)
	}
	f.now = f.now.Add(30 * time.Minute)
	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce(2): %v", err)
	}
	if alerts := stallAlerts(t, f); len(alerts) != 0 {
		t.Fatalf("30 分钟就是停滞了？告警 = %+v, want 0", alerts)
	}

	f.now = f.now.Add(31 * time.Minute) // 累计 61 分钟
	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce(3): %v", err)
	}
	if result.Stalled != 1 {
		t.Errorf("result = %+v, want 1 条停滞告警", result)
	}
	alerts := stallAlerts(t, f)
	if len(alerts) != 1 {
		t.Fatalf("停滞告警 = %d, want 1", len(alerts))
	}
	if alerts[0].Phase != string(task.PhaseDownloading) {
		t.Errorf("告警 phase = %q, want downloading", alerts[0].Phase)
	}
	if got := f.version(t, hashOne).Status; got != task.StatusDownloading {
		t.Errorf("status = %q, want downloading（停滞只告警不动状态）", got)
	}

	// 再跑一轮：同一段停滞不再重复告警。
	f.now = f.now.Add(5 * time.Minute)
	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce(4): %v", err)
	}
	if alerts := stallAlerts(t, f); len(alerts) != 1 {
		t.Errorf("停滞告警 = %d, want 1（只告警一次）", len(alerts))
	}

	// 通知入箱一条（执行归旁路引擎）。
	keys := notifyKeys(t, f, sidefx.EventDownloadStalled)
	if len(keys) != 1 || keys[0] != sidefx.EventDownloadStalled+":"+hashOne {
		t.Errorf("停滞通知待办 = %v, want 一条", keys)
	}

	// 模拟重启：新的引擎实例没有任何进程内历史。它重新观察一轮进度；
	// 60 分钟后再次判定到停滞，但「只告警一次」的判据在 task_event，所以不重复告警。
	restarted := reconcile.New(reconcile.Options{
		Store:    f.store,
		Outbox:   sidefx.New(f.database.Pool()),
		Settings: f.settings,
		Qbt:      f.qbt,
		Pool:     f.database.Pool(),
		Owner:    "restarted-owner",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:      func() time.Time { return f.now },
	})
	f.now = f.now.Add(time.Hour)
	if _, err := restarted.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce(重启后第一轮): %v", err)
	}
	f.now = f.now.Add(61 * time.Minute)
	result, err = restarted.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce(重启后再停滞): %v", err)
	}
	if result.Stalled != 0 {
		t.Errorf("result = %+v, want 0 条新停滞告警（判据在 task_event）", result)
	}
	if alerts := stallAlerts(t, f); len(alerts) != 1 {
		t.Errorf("重启后停滞告警 = %d, want 1（判据在 task_event，不在进程内）", len(alerts))
	}
	if keys := notifyKeys(t, f, sidefx.EventDownloadStalled); len(keys) != 1 {
		t.Errorf("重启后停滞通知待办 = %v, want 1 条（不重发）", keys)
	}
}

// TestReconcileStillJudgesWhenTagScanFails 钉住降级路径：按 tag 扫描打不通时，
// 库里那条发现面照常走完（否则 qbt 抖一次，所有已知的下载中版本就全停了）。
func TestReconcileStillJudgesWhenTagScanFails(t *testing.T) {
	f := newFixture(t)
	f.seedAddedVersion(t, movieID, number, hashOne)
	f.completed(t, hashOne)
	f.qbt.tagErr = errors.New("torrents/info 502")

	result, err := f.runner.RunOnce(context.Background())
	if err == nil {
		t.Error("RunOnce error = nil, want 带着 tag 扫描失败（健康位要看得到）")
	}
	if result.Advanced != 1 {
		t.Errorf("result = %+v, want 仍然推进 1 条", result)
	}
	if got := f.version(t, hashOne).Status; got != task.StatusProcessing {
		t.Errorf("status = %q, want processing（tag 扫描失败不挡库内发现面）", got)
	}
}

// TestReconcileIgnoresForeignTaggedTorrents 钉住 P8-2 在对账侧的对称面：
// tag 扫到库里没有的 infohash → 静默跳过，零写入。
func TestReconcileIgnoresForeignTaggedTorrents(t *testing.T) {
	f := newFixture(t)
	foreign := "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	f.qbt.tagged = []qbittorrent.TorrentStatus{{Hash: foreign, Progress: 1, State: "uploading"}}

	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if lookups := f.qbt.lookedUp(); len(lookups) != 0 {
		t.Errorf("qbt 回读 = %v, want 0（不是我们的版本）", lookups)
	}
	rows, err := f.store.List(context.Background(), task.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("任务行 = %d, want 0", len(rows))
	}
}

// ------------------------------------------------------------------ restart

// TestResetOnStartupResetsProcessingOnly 钉住重启复位语义（规格 §6.1）：
// processing 阶段级复位重跑、downloading/finished/failed 一概不动。
func TestResetOnStartupResetsProcessingOnly(t *testing.T) {
	f := newFixture(t)
	f.seedAddedVersion(t, movieID, number, hashOne)
	f.seedAddedVersion(t, "movie-2", "ABP-124", hashTwo)

	f.setStatus(t, hashOne, task.StatusProcessing)
	f.setStatus(t, hashTwo, task.StatusFinished)
	if _, err := f.store.Claim(context.Background(), hashOne, "死进程"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// 死进程的租约已过期（§4.4 的 10 分钟）：这是重启复位该接手的形态。
	expireClaim(t, f, hashOne)

	reset, err := f.runner.ResetOnStartup(context.Background())
	if err != nil {
		t.Fatalf("ResetOnStartup: %v", err)
	}
	if reset != 1 {
		t.Errorf("复位 = %d 条, want 1（只有 processing）", reset)
	}

	// 复位后下一位能立刻接手，且状态仍是 processing（阶段级重跑，不降级）。
	claimed, err := f.store.Claim(context.Background(), hashOne, "新进程")
	if err != nil {
		t.Fatalf("复位后 Claim error = %v, want 可接手", err)
	}
	if claimed.Status != task.StatusProcessing {
		t.Errorf("复位后 status = %q, want processing", claimed.Status)
	}

	// finished 的租约没被碰过。
	if _, err := f.store.Claim(context.Background(), hashTwo, "新进程"); errors.Is(err, task.ErrClaimLost) {
		t.Error("finished 版本的租约被复位了：重启只该碰 processing")
	}
}

// TestResetOnStartupKeepsLiveLease 钉住滚动更新语义：旧 pod 还活着（租约未过期）时，
// 新进程的复位不许抢走它的版本——版本级互斥归 claim，不归复位（规格 §4.4）。
func TestResetOnStartupKeepsLiveLease(t *testing.T) {
	f := newFixture(t)
	f.seedAddedVersion(t, movieID, number, hashOne)
	f.setStatus(t, hashOne, task.StatusProcessing)
	if _, err := f.store.Claim(context.Background(), hashOne, "旧 pod"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	reset, err := f.runner.ResetOnStartup(context.Background())
	if err != nil {
		t.Fatalf("ResetOnStartup: %v", err)
	}
	if reset != 0 {
		t.Errorf("复位 = %d 条, want 0（未过期的租约不归新进程）", reset)
	}
	if _, err := f.store.Claim(context.Background(), hashOne, "新进程"); !errors.Is(err, task.ErrClaimLost) {
		t.Errorf("Claim error = %v, want ErrClaimLost（旧 pod 的租约还在）", err)
	}
}

// expireClaim 把一条版本的租约调成已过期（测试要模拟死进程留下的租约）。
func expireClaim(t *testing.T, f *fixture, infohash string) {
	t.Helper()
	if _, err := f.database.Pool().Exec(context.Background(),
		`UPDATE task_version SET claimed_at = now() - interval '11 minutes' WHERE infohash = $1`, infohash); err != nil {
		t.Fatalf("让租约过期: %v", err)
	}
}

// notifyKeys 读某类通知的待办 target_key。
func notifyKeys(t *testing.T, f *fixture, event string) []string {
	t.Helper()
	rows, err := f.database.Pool().Query(context.Background(),
		`SELECT target_key FROM side_effect WHERE kind = 'notify' AND target_key LIKE $1 ORDER BY id`,
		event+":%")
	if err != nil {
		t.Fatalf("读通知待办: %v", err)
	}
	defer rows.Close()

	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("扫通知待办: %v", err)
		}
		keys = append(keys, key)
	}
	return keys
}

// transitions 取下载阶段的状态变化（FromState=downloading）：版本创建那条发现事件不算。
func transitions(t *testing.T, f *fixture, movie string) []task.Event {
	t.Helper()
	found := []task.Event{}
	for _, event := range f.events(t, movie) {
		if event.Kind == task.EventStateChange && event.FromState == string(task.StatusDownloading) {
			found = append(found, event)
		}
	}
	return found
}

// stallAlerts 取「下载停滞」告警事件。
func stallAlerts(t *testing.T, f *fixture) []task.Event {
	t.Helper()
	alerts := []task.Event{}
	for _, event := range f.events(t, movieID) {
		if event.Kind == task.EventAlert && event.Phase == string(task.PhaseDownloading) {
			alerts = append(alerts, event)
		}
	}
	return alerts
}

// ------------------------------------------------------------------ 加工阶段

// fakeProcessor 是加工推进接缝的假实现：只数「有没有被叫到」。
type fakeProcessor struct {
	progress scrape.Progress
	err      error
	calls    int
	// order 非 nil 时记下调用顺序（与 fakeOrganizer 共用一份）。
	order *[]string
}

func (f *fakeProcessor) ProcessPending(context.Context) (scrape.Progress, error) {
	f.calls++
	if f.order != nil {
		*f.order = append(*f.order, "scrape")
	}
	return f.progress, f.err
}

// TestRunOnceDrivesTheProcessingPass 钉住 §6.2：reconcile 的第三件事是「推进 processing」，
// 取数段（#27）与整理段（#29）都挂在这条通道上。
func TestRunOnceDrivesTheProcessingPass(t *testing.T) {
	f := newFixture(t)
	processor := &fakeProcessor{progress: scrape.Progress{Scanned: 1, Fetched: 1}}
	f.withProcessor(processor)

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processor.calls != 1 {
		t.Fatalf("加工推进被叫了 %d 次, want 1", processor.calls)
	}
	if result.Processing.Fetched != 1 {
		t.Errorf("result.Processing = %+v, want 取数 1 条", result.Processing)
	}
}

// TestRunOnceSkipsTheProcessingPassWhenGated 确认门禁关着时加工也不跑
// （未初始化＝整站只剩向导，不该有上游取数）。
func TestRunOnceSkipsTheProcessingPassWhenGated(t *testing.T) {
	f := newFixture(t)
	f.settings.snapshot = settings.Snapshot{Paused: true, Sections: map[string]settings.SectionState{}}
	processor := &fakeProcessor{progress: scrape.Progress{Scanned: 1, Fetched: 1}}
	f.withProcessor(processor)

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !result.Gated {
		t.Fatalf("result = %+v, want Gated", result)
	}
	if processor.calls != 0 {
		t.Errorf("门禁关着仍推进了加工 %d 次", processor.calls)
	}
}

// fakeOrganizer 是整理段的假实现。
type fakeOrganizer struct {
	progress organize.Progress
	err      error
	calls    int
	// order 非 nil 时记下调用顺序（与 fakeProcessor 共用一份）。
	order *[]string
}

func (f *fakeOrganizer) ProcessPending(context.Context) (organize.Progress, error) {
	f.calls++
	if f.order != nil {
		*f.order = append(*f.order, "organize")
	}
	return f.progress, f.err
}

// TestRunOnceRunsScrapeThenOrganize 钉住两段的接续与顺序：
// 取齐元数据（#27）之后，同一条通道上接着整理入库（#29）。
func TestRunOnceRunsScrapeThenOrganize(t *testing.T) {
	f := newFixture(t)
	order := []string{}
	processor := &fakeProcessor{progress: scrape.Progress{Scanned: 1, Fetched: 1}, order: &order}
	organizer := &fakeOrganizer{progress: organize.Progress{Scanned: 1, Filed: 1}, order: &order}
	f.withProcessor(processor)
	f.withOrganizer(organizer)

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processor.calls != 1 || organizer.calls != 1 {
		t.Fatalf("调用次数 = 取数 %d / 整理 %d, want 各 1", processor.calls, organizer.calls)
	}
	if len(order) != 2 || order[0] != "scrape" || order[1] != "organize" {
		t.Errorf("顺序 = %v, want 先取数后整理", order)
	}
	if result.Organized.Filed != 1 {
		t.Errorf("result.Organized = %+v, want Filed=1", result.Organized)
	}
}

// TestRunOnceReportsProcessingErrors 确认取数失败会记进这一轮的错误（循环日志看得见），
// 但不会吞掉下载侧已经做完的工作。
func TestRunOnceReportsProcessingErrors(t *testing.T) {
	f := newFixture(t)
	processor := &fakeProcessor{err: errors.New("dmm region blocked")}
	f.withProcessor(processor)

	if _, err := f.runner.RunOnce(context.Background()); err == nil {
		t.Fatal("加工错误应当传出来")
	}
}
