package retry_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/material"
	"github.com/2017fighting/oh-my-mdc/internal/mteam"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/retry"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：retry.Runner.Retry —— 真 Postgres + 假 qBittorrent/设置接缝。
//
// 断言只看两件事：接口回的逐版结果（面板据此说话），以及库里的状态/事件
// （状态机与时间线是重试的产物）。qbt 的加种行为用「调了几次、带了什么」证明。

// ------------------------------------------------------------------ fakes

type fakeSettings struct {
	inputs   map[string]settings.TestInput
	snapshot settings.Snapshot
}

func (f *fakeSettings) Load(context.Context) (settings.Snapshot, error) {
	return f.snapshot, nil
}

func boolPtr(value bool) *bool { return &value }

func (f *fakeSettings) Input(_ context.Context, section string) (settings.TestInput, error) {
	input, ok := f.inputs[section]
	if !ok {
		return settings.TestInput{}, settings.ErrNotConfigured
	}
	return input, nil
}

type fakeQbt struct {
	found     map[string]qbittorrent.TorrentStatus
	lookupErr error
	ensureErr error
	ensured   []qbittorrent.Torrent
	// addSizeBytes 是「加种之后 qbt 会报出来的大小」，用于钉住 size_mib 的回读。
	addSizeBytes int64
}

func (f *fakeQbt) Lookup(_ context.Context, _ qbittorrent.Config, hash string) (qbittorrent.TorrentStatus, bool, error) {
	if f.lookupErr != nil {
		return qbittorrent.TorrentStatus{}, false, f.lookupErr
	}
	status, ok := f.found[hash]
	return status, ok, nil
}

func (f *fakeQbt) Ensure(_ context.Context, _ qbittorrent.Config, torrent qbittorrent.Torrent) (qbittorrent.Outcome, error) {
	if f.ensureErr != nil {
		return "", f.ensureErr
	}
	f.ensured = append(f.ensured, torrent)
	// 已在 qbt 里且带着项目 tag：Ensure 的幂等出口（真实实现见 §5.4 收编/已在）。
	if _, found := f.found[torrent.Hash]; found {
		return qbittorrent.AlreadyTracked, nil
	}
	// 新加进去的种子：qbt 从此报得出它（进度 0），加种后的回读才有大小。
	f.found[torrent.Hash] = qbittorrent.TorrentStatus{Hash: torrent.Hash, SizeBytes: f.addSizeBytes}
	return qbittorrent.Added, nil
}

// ------------------------------------------------------------------ fixture

// fakeTorrents 是 M-Team 取种接缝：重取私种备份时才被问到（#36）。
type fakeTorrents struct {
	torrent mteam.Torrent
	err     error
	calls   []string
}

func (f *fakeTorrents) Resolve(_ context.Context, _ string, number string) (mteam.Torrent, error) {
	f.calls = append(f.calls, number)
	if f.err != nil {
		return mteam.Torrent{}, f.err
	}
	return f.torrent, nil
}

const (
	movieID   = "YwrO3B"
	number    = "ABP-123"
	hash      = "1111111111111111111111111111111111111111"
	otherHash = "2222222222222222222222222222222222222222"
)

type fixture struct {
	database     *db.DB
	store        *task.Store
	outbox       *sidefx.Outbox
	settings     *fakeSettings
	qbt          *fakeQbt
	torrents     *fakeTorrents
	materialsDir string
	runner       *retry.Runner
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
		outbox:   sidefx.New(database.Pool()),
		qbt:      &fakeQbt{found: map[string]qbittorrent.TorrentStatus{}, addSizeBytes: 512 << 20},
		settings: &fakeSettings{
			inputs: map[string]settings.TestInput{
				"qbittorrent": {Values: map[string]any{
					"url": "http://qbt:8080", "download_root": "/media/downloads", "category": "mdc",
				}},
				"mteam": {Values: map[string]any{"api_key": "mteam-key"}, Enabled: true},
			},
			snapshot: settings.Snapshot{Sections: map[string]settings.SectionState{
				"mteam": {Configured: true, Enabled: boolPtr(true)},
			}},
		},
		torrents: &fakeTorrents{},
	}
	f.materialsDir = t.TempDir()
	materials := material.New(material.Options{
		Dir:      f.materialsDir,
		Torrents: f.torrents,
		Settings: f.settings,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	f.runner = retry.New(retry.Options{
		Store:    f.store,
		Outbox:   f.outbox,
		Settings: f.settings,
		Qbt:      f.qbt,
		Material: materials,
		Owner:    "test-retry",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return f
}

// seedVersion 种一条已加过种的版本（add_pending 清掉），并把它拨成要测的样子。
func (f *fixture) seedVersion(t *testing.T, infohash string, mutate func(id string)) {
	t.Helper()
	ctx := context.Background()
	inserted, err := f.store.SaveCandidate(ctx, task.Candidate{
		MovieID: movieID, Number: number, ListID: "wanted",
		Infohash: infohash, Source: task.SourceJavDBMagnet, Delivery: task.DeliveryMagnet,
		Label: "中文", Payload: "magnet:?xt=urn:btih:" + infohash + "&dn=" + number,
	})
	if err != nil || !inserted {
		t.Fatalf("种版本 %s: inserted=%v err=%v", infohash, inserted, err)
	}
	if _, err := f.store.Claim(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("Claim(加种): %v", err)
	}
	if err := f.store.MarkAdded(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("MarkAdded: %v", err)
	}
	if mutate != nil {
		mutate(infohash)
	}
}

// failedScrape 把版本拨成 failed(scrape, reason)，并留下自动重试的痕迹。
func (f *fixture) failedScrape(t *testing.T, infohash string) {
	t.Helper()
	f.exec(t, `UPDATE task_version
	 SET status = 'failed', error_phase = 'scrape', error_reason = 'DMM 地区阻断',
	     attempts = 3, next_attempt_at = NULL
	 WHERE infohash = $1`, infohash)
}

func (f *fixture) finished(t *testing.T, infohash string) {
	t.Helper()
	f.exec(t, `UPDATE task_version SET status = 'finished', attempts = 0 WHERE infohash = $1`, infohash)
}

func (f *fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.database.Pool().Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("执行 SQL 失败: %v\n%s", err, sql)
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

func (f *fixture) events(t *testing.T) []task.Event {
	t.Helper()
	events, err := f.store.Events(context.Background(), movieID, 50)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	return events
}

// manualRetries 是时间线上的「人工重试」事件，新的在前。
func (f *fixture) manualRetries(t *testing.T) []task.Event {
	t.Helper()
	retries := []task.Event{}
	for _, event := range f.events(t) {
		if event.Kind == task.EventManualRetry {
			retries = append(retries, event)
		}
	}
	return retries
}

func retried(t *testing.T, result retry.Result, infohash string) retry.Outcome {
	t.Helper()
	for _, outcome := range result.Versions {
		if outcome.Infohash == infohash {
			return outcome
		}
	}
	t.Fatalf("结果里没有版本 %s：%+v", infohash, result.Versions)
	return retry.Outcome{}
}

// ------------------------------------------------------------------ tests

// TestRetryRewindsFailedScrapeToProcessingStart 钉住 P13-1：
// 失败在加工阶段的版本回 processing 开头，且时间线上留下一条 manual_retry。
func TestRetryRewindsFailedScrapeToProcessingStart(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, func(id string) { f.failedScrape(t, id) })

	result, err := f.runner.Retry(context.Background(), movieID, nil, false)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	outcome := retried(t, result, hash)
	if outcome.Action != retry.ActionResetProcessing {
		t.Errorf("action = %q, want %q", outcome.Action, retry.ActionResetProcessing)
	}
	if outcome.FromState != task.StatusFailed || outcome.ToState != task.StatusProcessing {
		t.Errorf("from/to = %q→%q, want failed→processing", outcome.FromState, outcome.ToState)
	}

	version := f.version(t, hash)
	if version.Status != task.StatusProcessing || version.Attempts != 0 {
		t.Errorf("version = (%q, attempts=%d), want (processing, 0)", version.Status, version.Attempts)
	}
	if version.ForceRescrape {
		t.Error("force_rescrape = true, want false（普通重试不重刮上游）")
	}

	retries := f.manualRetries(t)
	if len(retries) != 1 {
		t.Fatalf("manual_retry 事件 = %d, want 1", len(retries))
	}
	if retries[0].FromState != "failed" || retries[0].ToState != "processing" || retries[0].Reason == "" {
		t.Errorf("事件 = %+v, want failed→processing + 人话 reason", retries[0])
	}
}

// TestRetryIsIdempotentOnAProcessingVersion 钉住「幂等」：连点两次仍是同一个起点。
func TestRetryIsIdempotentOnAProcessingVersion(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, func(id string) {
		f.exec(t, `UPDATE task_version SET status = 'processing', attempts = 2 WHERE infohash = $1`, id)
	})

	for round := 0; round < 2; round++ {
		if _, err := f.runner.Retry(context.Background(), movieID, nil, false); err != nil {
			t.Fatalf("第 %d 次 Retry: %v", round+1, err)
		}
	}

	version := f.version(t, hash)
	if version.Status != task.StatusProcessing || version.Attempts != 0 {
		t.Errorf("version = (%q, attempts=%d), want (processing, 0)", version.Status, version.Attempts)
	}
}

// TestRetrySkipsFinishedVersionsUnlessForced 钉住 P13-3 的前半：
// 已入库的版本在没有 force 时零动作，也不写事件。
func TestRetrySkipsFinishedVersionsUnlessForced(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, func(id string) { f.finished(t, id) })

	result, err := f.runner.Retry(context.Background(), movieID, nil, false)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	outcome := retried(t, result, hash)
	if outcome.Action != retry.ActionSkipped {
		t.Errorf("action = %q, want skipped", outcome.Action)
	}
	if outcome.Reason == "" {
		t.Error("reason 为空：面板要说清为什么没动")
	}
	if version := f.version(t, hash); version.Status != task.StatusFinished {
		t.Errorf("status = %q, want finished", version.Status)
	}
	if retries := f.manualRetries(t); len(retries) != 0 {
		t.Errorf("manual_retry 事件 = %d, want 0（没动手就不该留痕）", len(retries))
	}
}

// TestRetryForceRescrapesFinishedVersionAndKeepsTheComparisonBaseline 钉住 P13-3：
// force 才重刮已 finished 版本，且重刮前的 raw_snapshot 先落进事件 detail（对比基准）。
func TestRetryForceRescrapesFinishedVersionAndKeepsTheComparisonBaseline(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, func(id string) { f.finished(t, id) })
	f.exec(t, `UPDATE task SET raw_snapshot = '{"javdb":{"movie":{"id":"YwrO3B","title":"旧标题"}},"dmm_content":{"ppvContent":{"title":"旧日文标题"}}}'::jsonb
		WHERE movie_id = $1`, movieID)

	result, err := f.runner.Retry(context.Background(), movieID, nil, true)
	if err != nil {
		t.Fatalf("Retry(force): %v", err)
	}
	outcome := retried(t, result, hash)
	if outcome.Action != retry.ActionResetProcessing || !outcome.ForceRescrape {
		t.Errorf("outcome = %+v, want reset_processing + force_rescrape", outcome)
	}
	if outcome.FromState != task.StatusFinished || outcome.ToState != task.StatusProcessing {
		t.Errorf("from/to = %q→%q, want finished→processing", outcome.FromState, outcome.ToState)
	}

	version := f.version(t, hash)
	if version.Status != task.StatusProcessing {
		t.Errorf("status = %q, want processing", version.Status)
	}
	if !version.ForceRescrape {
		t.Error("force_rescrape = false, want true（取数段要跳过复用判据）")
	}

	retries := f.manualRetries(t)
	if len(retries) != 1 {
		t.Fatalf("manual_retry 事件 = %d, want 1", len(retries))
	}
	var detail struct {
		Action            string `json:"action"`
		RawSnapshotBefore struct {
			JavDB struct {
				Movie struct {
					Title string `json:"title"`
				} `json:"movie"`
			} `json:"javdb"`
		} `json:"raw_snapshot_before"`
	}
	if err := json.Unmarshal(retries[0].Detail, &detail); err != nil {
		t.Fatalf("事件 detail 不是 JSON: %v（%s）", err, retries[0].Detail)
	}
	if detail.Action != string(retry.ActionResetProcessing) {
		t.Errorf("detail.action = %q, want %q（机器可读的结局）", detail.Action, retry.ActionResetProcessing)
	}
	if detail.RawSnapshotBefore.JavDB.Movie.Title != "旧标题" {
		t.Errorf("detail.raw_snapshot_before.javdb.movie.title = %q, want 旧标题",
			detail.RawSnapshotBefore.JavDB.Movie.Title)
	}
	if !strings.Contains(retries[0].Reason, "对比") {
		t.Errorf("reason = %q, want 说清留了对比基准", retries[0].Reason)
	}
}

// TestRetryWithForceAlsoSkipsReuseForAFailedVersion 钉住 force 的语义边界：
// 它不是「只对 finished 生效」的开关，而是「下一次取数不复用」的要求。
func TestRetryWithForceAlsoSkipsReuseForAFailedVersion(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, func(id string) {
		f.exec(t, `UPDATE task_version SET status = 'failed', error_phase = 'organize', error_reason = '硬链接跨盘（EXDEV）'
			WHERE infohash = $1`, id)
	})

	result, err := f.runner.Retry(context.Background(), movieID, nil, true)
	if err != nil {
		t.Fatalf("Retry(force): %v", err)
	}
	if !retried(t, result, hash).ForceRescrape {
		t.Error("force_rescrape = false, want true")
	}
	if !f.version(t, hash).ForceRescrape {
		t.Error("库里的 force_rescrape = false, want true")
	}
}

// TestRetryReaddsFailedDownloadingFromStoredMagnet 钉住 P13-2：
// 下载阶段失败 → 按存的 payload 重新加种，成功后版本回 downloading，并发「下载开始」通知。
func TestRetryReaddsFailedDownloadingFromStoredMagnet(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, func(id string) {
		f.exec(t, `UPDATE task_version
		 SET status = 'failed', error_phase = 'downloading', error_reason = '种子已从 qBittorrent 移除',
		     attempts = 3
		 WHERE infohash = $1`, id)
	})

	result, err := f.runner.Retry(context.Background(), movieID, nil, false)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	outcome := retried(t, result, hash)
	if outcome.Action != retry.ActionReadded {
		t.Fatalf("action = %q, want %q（%s）", outcome.Action, retry.ActionReadded, outcome.Reason)
	}
	if outcome.FromState != task.StatusFailed || outcome.ToState != task.StatusDownloading {
		t.Errorf("from/to = %q→%q, want failed→downloading", outcome.FromState, outcome.ToState)
	}

	if len(f.qbt.ensured) != 1 {
		t.Fatalf("Ensure 调用 %d 次, want 1", len(f.qbt.ensured))
	}
	if want := "magnet:?xt=urn:btih:" + hash + "&dn=" + number; f.qbt.ensured[0].Magnet != want {
		t.Errorf("加种磁力 = %q, want %q（按存的 payload 重加）", f.qbt.ensured[0].Magnet, want)
	}

	version := f.version(t, hash)
	if version.Status != task.StatusDownloading {
		t.Errorf("status = %q, want downloading", version.Status)
	}
	if version.AddPending || version.Attempts != 0 || version.ErrorPhase != "" {
		t.Errorf("version = %+v, want add_pending=false attempts=0 error 清空", version)
	}
	if !version.ListRemovalPending {
		t.Error("list_removal_pending = false, want true（加种之后才删清单项）")
	}

	jobs, err := f.outbox.List(context.Background(), sidefx.StatePending, 10)
	if err != nil {
		t.Fatalf("List(待办): %v", err)
	}
	if len(jobs) != 1 || jobs[0].TargetKey != sidefx.EventDownloadStarted+":"+hash {
		t.Fatalf("待办 = %+v, want 一条 %s 通知", jobs, sidefx.EventDownloadStarted)
	}
	var notified map[string]any
	if err := json.Unmarshal(jobs[0].Payload, &notified); err != nil {
		t.Fatalf("通知 payload 不是 JSON: %v", err)
	}
	if size, _ := notified["size_mib"].(float64); size != 512 {
		t.Errorf("size_mib = %v, want 512（§5.6 payload 契约：加种后从 qbt 回读）", notified["size_mib"])
	}

	if retries := f.manualRetries(t); len(retries) != 1 || !strings.Contains(retries[0].Reason, "重新加种") {
		t.Errorf("manual_retry 事件 = %+v, want 一条说清重新加种", retries)
	}
}

// TestRetryReaddsTorrentDeliveryFromItsBackup 钉住另一条交付形态：
// 私种的 payload 是备份路径，重加时要读回字节（multipart 那一支）。
func TestRetryReaddsTorrentDeliveryFromItsBackup(t *testing.T) {
	f := newFixture(t)
	backup := filepath.Join(t.TempDir(), "backup.torrent")
	body := []byte("d4:infod4:name4:testee")
	if err := os.WriteFile(backup, body, 0o600); err != nil {
		t.Fatalf("写种子备份: %v", err)
	}

	f.seedVersion(t, otherHash, func(id string) {
		f.exec(t, `UPDATE task_version
		 SET source = 'mteam_torrent', delivery = 'torrent', payload = $2,
		     status = 'failed', error_phase = 'downloading', error_reason = '种子已从 qBittorrent 移除'
		 WHERE infohash = $1`, id, backup)
	})

	result, err := f.runner.Retry(context.Background(), movieID, nil, false)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if outcome := retried(t, result, otherHash); outcome.Action != retry.ActionReadded {
		t.Fatalf("action = %q, want readded（%s）", outcome.Action, outcome.Reason)
	}
	if len(f.qbt.ensured) != 1 || string(f.qbt.ensured[0].Body) != string(body) {
		t.Errorf("Ensure 收到 = %+v, want 备份文件原文", f.qbt.ensured)
	}
}

// TestRetryRefetchesAMissingTorrentBackup 钉住 #36 在人工重试那条路上的形态：
// 备份没了就回 M-Team 重取（只认 infohash 相符的那条），重取成功才重新加种，
// 并把新的备份路径写回 payload（下一轮 poll 与下一次重试读的就是它）。
func TestRetryRefetchesAMissingTorrentBackup(t *testing.T) {
	f := newFixture(t)
	body := []byte("d4:infod4:name4:testee")
	f.torrents.torrent = mteam.Torrent{Infohash: otherHash, Bytes: body}

	missing := filepath.Join(t.TempDir(), "gone.torrent")
	f.seedVersion(t, otherHash, func(id string) {
		f.exec(t, `UPDATE task_version
		 SET source = 'mteam_torrent', delivery = 'torrent', payload = $2,
		     status = 'failed', error_phase = 'downloading', error_reason = '种子已从 qBittorrent 移除'
		 WHERE infohash = $1`, id, missing)
	})

	result, err := f.runner.Retry(context.Background(), movieID, nil, false)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if outcome := retried(t, result, otherHash); outcome.Action != retry.ActionReadded {
		t.Fatalf("action = %q, want readded（%s）", outcome.Action, outcome.Reason)
	}
	if len(f.torrents.calls) != 1 {
		t.Errorf("重取调用 = %v, want 按番号重取一次", f.torrents.calls)
	}
	if len(f.qbt.ensured) != 1 || string(f.qbt.ensured[0].Body) != string(body) {
		t.Errorf("Ensure 收到 = %+v, want 重取回来的种子字节", f.qbt.ensured)
	}

	version := f.version(t, otherHash)
	wantPath := filepath.Join(f.materialsDir, otherHash+".torrent")
	if version.Payload != wantPath {
		t.Errorf("payload = %q, want %q（重取落盘的新路径要写回库）", version.Payload, wantPath)
	}
	if version.Status != task.StatusDownloading {
		t.Errorf("status = %q, want downloading", version.Status)
	}
}

// TestRetryReportsAFailedReaddWhenTheTorrentIsGone 钉住重取不到时的口径：
// 站点上已没有这条种子 → 这次重试是 failed（带一句人看得懂的因），
// 还在下载中的版本**当场**进需人工区（而不是等下轮 poll 再花一次 M-Team 配额）。
func TestRetryReportsAFailedReaddWhenTheTorrentIsGone(t *testing.T) {
	t.Run("仍卡在下载中", func(t *testing.T) {
		f := newFixture(t)
		f.torrents.err = mteam.NotFoundError{Number: number}
		missing := filepath.Join(t.TempDir(), "gone.torrent")
		// #36 的现场：版本从未加种成功（add_pending 还在），而备份已经没了。
		f.seedVersion(t, otherHash, func(id string) {
			f.exec(t, `UPDATE task_version
			 SET source = 'mteam_torrent', delivery = 'torrent', payload = $2,
			     status = 'downloading', add_pending = true
			 WHERE infohash = $1`, id, missing)
		})

		result, err := f.runner.Retry(context.Background(), movieID, nil, false)
		if err != nil {
			t.Fatalf("Retry: %v", err)
		}
		outcome := retried(t, result, otherHash)
		if outcome.Action != retry.ActionFailed || outcome.ToState != task.StatusFailed {
			t.Fatalf("action/from→to = %q/%q→%q, want failed/downloading→failed", outcome.Action, outcome.FromState, outcome.ToState)
		}
		if !strings.Contains(outcome.Reason, "重取") {
			t.Errorf("reason = %q, want 说清重取也失败", outcome.Reason)
		}

		version := f.version(t, otherHash)
		if version.Status != task.StatusFailed || version.ErrorPhase != task.PhaseDownloading {
			t.Errorf("status/phase = %q/%q, want failed/downloading", version.Status, version.ErrorPhase)
		}
		if version.AddPending {
			t.Error("add_pending = true, want false（不再每轮静默重试）")
		}
		if !strings.Contains(version.ErrorReason, "重取") {
			t.Errorf("error_reason = %q, want 说清重取也失败", version.ErrorReason)
		}
		attention, err := f.store.Attention(context.Background(), 50)
		if err != nil {
			t.Fatalf("Attention: %v", err)
		}
		if len(attention.Failed) != 1 || attention.Failed[0].Infohash != otherHash {
			t.Errorf("attention.failed = %+v, want 这条版本", attention.Failed)
		}
	})

	t.Run("已经失败", func(t *testing.T) {
		f := newFixture(t)
		f.torrents.err = mteam.NotFoundError{Number: number}
		missing := filepath.Join(t.TempDir(), "gone.torrent")
		f.seedVersion(t, otherHash, func(id string) {
			f.exec(t, `UPDATE task_version
			 SET source = 'mteam_torrent', delivery = 'torrent', payload = $2,
			     status = 'failed', error_phase = 'downloading', error_reason = '种子已从 qBittorrent 移除'
			 WHERE infohash = $1`, id, missing)
		})

		result, err := f.runner.Retry(context.Background(), movieID, nil, false)
		if err != nil {
			t.Fatalf("Retry: %v", err)
		}
		outcome := retried(t, result, otherHash)
		if outcome.Action != retry.ActionFailed || outcome.ToState != task.StatusFailed {
			t.Fatalf("action/from→to = %q/%q→%q, want failed/failed→failed", outcome.Action, outcome.FromState, outcome.ToState)
		}
		if len(f.qbt.ensured) != 0 {
			t.Errorf("加种调用 = %d, want 0", len(f.qbt.ensured))
		}

		version := f.version(t, otherHash)
		if version.Status != task.StatusFailed || version.ErrorReason != "种子已从 qBittorrent 移除" {
			t.Errorf("version = %q/%q, want 状态与原因都保持原样", version.Status, version.ErrorReason)
		}
		if version.Payload != missing {
			t.Errorf("payload = %q, want 原样 %q", version.Payload, missing)
		}
		if retries := f.manualRetries(t); len(retries) != 1 || !strings.Contains(retries[0].Reason, "重取") {
			t.Errorf("manual_retry 事件 = %+v, want 一条说清重取也失败", retries)
		}
	})
}

// TestRetryRefusesToRedownloadWhatQbtIsStillDownloading 钉住那条硬边界：
// qBittorrent 里仍在下载的版本永不重下——跳过、零加种、留一条说明。
func TestRetryRefusesToRedownloadWhatQbtIsStillDownloading(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, func(id string) {
		f.exec(t, `UPDATE task_version
		 SET status = 'failed', error_phase = 'downloading', error_reason = '文件丢失'
		 WHERE infohash = $1`, id)
	})
	f.qbt.found[hash] = qbittorrent.TorrentStatus{Hash: hash, Progress: 0.42, State: "downloading"}

	result, err := f.runner.Retry(context.Background(), movieID, nil, false)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	outcome := retried(t, result, hash)
	if outcome.Action != retry.ActionSkipped {
		t.Errorf("action = %q, want skipped", outcome.Action)
	}
	if !strings.Contains(outcome.Reason, "仍在下载") {
		t.Errorf("reason = %q, want 说清 qbt 里仍在下载", outcome.Reason)
	}
	if len(f.qbt.ensured) != 0 {
		t.Fatalf("Ensure 调用 %d 次, want 0（永不重下）", len(f.qbt.ensured))
	}
	if version := f.version(t, hash); version.Status != task.StatusFailed {
		t.Errorf("status = %q, want failed（拒绝重下不该改状态）", version.Status)
	}
	if retries := f.manualRetries(t); len(retries) != 1 || !strings.Contains(retries[0].Reason, "仍在下载") {
		t.Errorf("manual_retry 事件 = %+v, want 一条说清被拒", retries)
	}
}

// TestRetryKeepsFailedWhenReaddFails 钉住「重加失败保持 failed 可再试」：
// 第一次加种失败只是这一次没成，状态不动；第二次成功就回下载中。
func TestRetryKeepsFailedWhenReaddFails(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, func(id string) {
		f.exec(t, `UPDATE task_version
		 SET status = 'failed', error_phase = 'downloading', error_reason = '种子已从 qBittorrent 移除'
		 WHERE infohash = $1`, id)
	})
	f.qbt.ensureErr = errors.New("qBittorrent 拒绝了加种：boom")

	result, err := f.runner.Retry(context.Background(), movieID, nil, false)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	outcome := retried(t, result, hash)
	if outcome.Action != retry.ActionFailed {
		t.Fatalf("action = %q, want failed（%s）", outcome.Action, outcome.Reason)
	}
	if version := f.version(t, hash); version.Status != task.StatusFailed || version.ErrorReason != "种子已从 qBittorrent 移除" {
		t.Errorf("version = (%q, %q), want 原样 failed + 旧原因", version.Status, version.ErrorReason)
	}

	// 再试一次：这次加种成功，版本回下载中。
	f.qbt.ensureErr = nil
	result, err = f.runner.Retry(context.Background(), movieID, nil, false)
	if err != nil {
		t.Fatalf("第二次 Retry: %v", err)
	}
	if outcome := retried(t, result, hash); outcome.Action != retry.ActionReadded {
		t.Errorf("第二次 action = %q, want readded（%s）", outcome.Action, outcome.Reason)
	}
	if version := f.version(t, hash); version.Status != task.StatusDownloading {
		t.Errorf("status = %q, want downloading", version.Status)
	}
}

// TestRetryDoesNotStealALeasedVersion 钉住互斥：别人拿着未过期的租约时零动作，
// 原因是「稍后再试」而不是失败（规格 §4.4：抢输不是失败）。
func TestRetryDoesNotStealALeasedVersion(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, nil)
	if _, err := f.store.Claim(context.Background(), hash, "someone-else"); err != nil {
		t.Fatalf("Claim(别人): %v", err)
	}

	result, err := f.runner.Retry(context.Background(), movieID, nil, false)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	outcome := retried(t, result, hash)
	if outcome.Action != retry.ActionSkipped || !strings.Contains(outcome.Reason, "稍后") {
		t.Errorf("outcome = %+v, want skipped + 稍后再试", outcome)
	}
	if version := f.version(t, hash); version.Status != task.StatusDownloading || version.Attempts != 0 {
		t.Errorf("version = (%q, attempts=%d), want 原样", version.Status, version.Attempts)
	}
}

// TestRetryWithoutTaskIsNotFound 钉住 404 的来源：任务不存在与「没有版本」是两件事。
func TestRetryWithoutTaskIsNotFound(t *testing.T) {
	f := newFixture(t)

	if _, err := f.runner.Retry(context.Background(), "no-such-movie", nil, false); !errors.Is(err, task.ErrTaskNotFound) {
		t.Fatalf("Retry(未知任务) = %v, want ErrTaskNotFound", err)
	}
}

// TestRetryDoesNotReAnnounceAnAlreadyTrackedTorrent 钉住通知去噪：
// 种子本来就在 qbt 里且带着项目 tag 时，重试只把版本放回下载中，不再喊一次「下载开始」。
func TestRetryDoesNotReAnnounceAnAlreadyTrackedTorrent(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, func(id string) {
		f.exec(t, `UPDATE task_version
		 SET status = 'failed', error_phase = 'downloading', error_reason = '文件丢失'
		 WHERE infohash = $1`, id)
	})
	f.qbt.found[hash] = qbittorrent.TorrentStatus{Hash: hash, Progress: 1, State: "uploading"}

	result, err := f.runner.Retry(context.Background(), movieID, nil, false)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if outcome := retried(t, result, hash); outcome.Action != retry.ActionReadded {
		t.Fatalf("action = %q, want readded（%s）", outcome.Action, outcome.Reason)
	}
	jobs, err := f.outbox.List(context.Background(), sidefx.StatePending, 10)
	if err != nil {
		t.Fatalf("List(待办): %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("待办 = %+v, want 空（已带项目 tag 的种子不重喊下载开始）", jobs)
	}
}

// TestRetryReaddsAStaleDownloadingVersionWhoseSeedIsGone 钉住「人工重试＝重新加种」
// （规格 §5.3 对账行）：对账还没判失败的下载中版本，种子里已经没了，点重试就重新加种。
func TestRetryReaddsAStaleDownloadingVersionWhoseSeedIsGone(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, nil)

	result, err := f.runner.Retry(context.Background(), movieID, nil, false)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if outcome := retried(t, result, hash); outcome.Action != retry.ActionReadded {
		t.Fatalf("action = %q, want readded（%s）", outcome.Action, outcome.Reason)
	}
	if len(f.qbt.ensured) != 1 {
		t.Errorf("Ensure 调用 %d 次, want 1", len(f.qbt.ensured))
	}
	if version := f.version(t, hash); version.Status != task.StatusDownloading || version.Attempts != 0 {
		t.Errorf("version = (%q, attempts=%d), want (downloading, 0)", version.Status, version.Attempts)
	}
}

// TestRetryNeverTouchesATorrentQbtIsStillDownloadingEvenWhenPending 钉住硬边界的字面：
// add_pending=true（上次崩在加种与清标记之间）也不能借重试去碰一条正在下载的种子。
func TestRetryNeverTouchesATorrentQbtIsStillDownloadingEvenWhenPending(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, func(id string) {
		f.exec(t, `UPDATE task_version SET add_pending = true WHERE infohash = $1`, id)
	})
	f.qbt.found[hash] = qbittorrent.TorrentStatus{Hash: hash, Progress: 0.5, State: "downloading"}

	result, err := f.runner.Retry(context.Background(), movieID, nil, false)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if outcome := retried(t, result, hash); outcome.Action != retry.ActionSkipped {
		t.Errorf("action = %q, want skipped（%s）", outcome.Action, outcome.Reason)
	}
	if len(f.qbt.ensured) != 0 {
		t.Errorf("Ensure 调用 %d 次, want 0", len(f.qbt.ensured))
	}
	if version := f.version(t, hash); !version.AddPending {
		t.Error("add_pending = false, want true（待加种标记归 poll 清，重试不碰）")
	}
}

// TestRetryWithForceOnADownloadingVersionFlagsTheNextScrape 钉住 force 不被下载分支吞掉：
// 下载中/下载失败的版本被强制重刮时，同样置起「下一次取数不复用」。
func TestRetryWithForceOnADownloadingVersionFlagsTheNextScrape(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, func(id string) {
		f.exec(t, `UPDATE task_version
		 SET status = 'failed', error_phase = 'downloading', error_reason = '种子已从 qBittorrent 移除'
		 WHERE infohash = $1`, id)
	})

	result, err := f.runner.Retry(context.Background(), movieID, nil, true)
	if err != nil {
		t.Fatalf("Retry(force): %v", err)
	}
	outcome := retried(t, result, hash)
	if outcome.Action != retry.ActionReadded || !outcome.ForceRescrape {
		t.Errorf("outcome = %+v, want readded + force_rescrape", outcome)
	}
	if version := f.version(t, hash); !version.ForceRescrape {
		t.Error("库里的 force_rescrape = false, want true")
	}
}

// TestRetryOnlyTouchesTheNamedVersions 钉住面板那一行的爆炸半径。
//
// 面板的行是**版本组**，按钮上写「重试 2 条版本」时后端必须只动那 2 条：
// 同组里还有一条宿主机病因（权限/只读/满盘）的版本时，它不该被顺手再放一遍——
// remedy 模块刚立过「重试不会变好就别给按钮」的规矩，接口不能反着来。
func TestRetryOnlyTouchesTheNamedVersions(t *testing.T) {
	f := newFixture(t)
	f.seedVersion(t, hash, func(id string) { f.failedScrape(t, id) })
	f.seedVersion(t, otherHash, func(id string) {
		f.exec(t, `UPDATE task_version
		 SET status = 'failed', error_phase = 'organize',
		     error_reason = 'permission denied: /media/movies/ABP-123', attempts = 2, next_attempt_at = NULL
		 WHERE infohash = $1`, id)
	})

	result, err := f.runner.Retry(context.Background(), movieID, []string{hash}, false)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if len(result.Versions) != 1 || result.Versions[0].Infohash != hash {
		t.Fatalf("结果 = %+v, want 只有点名的 %s", result.Versions, hash)
	}

	// 没被点名的那条原封不动：状态仍是 failed，时间线上没有它的 manual_retry。
	untouched := f.version(t, otherHash)
	if untouched.Status != task.StatusFailed || untouched.Attempts != 2 {
		t.Errorf("未点名的版本 = (%q, attempts=%d), want (failed, 2)", untouched.Status, untouched.Attempts)
	}
	retries := f.manualRetries(t)
	if len(retries) != 1 || retries[0].Infohash != hash {
		t.Errorf("manual_retry 事件 = %+v, want 只有 %s 一条", retries, hash)
	}

	// 点名了但一条也对不上：回空结果，不退回「整个任务」。
	none, err := f.runner.Retry(context.Background(), movieID, []string{"deadbeef"}, false)
	if err != nil {
		t.Fatalf("Retry(陌生 infohash): %v", err)
	}
	if len(none.Versions) != 0 {
		t.Errorf("结果 = %+v, want 空（点名的版本不属于这个任务）", none.Versions)
	}
}
