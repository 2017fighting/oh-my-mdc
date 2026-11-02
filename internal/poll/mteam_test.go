package poll_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/mteam"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：poll.Runner.RunOnce 的 M-Team 分支 —— 真 Postgres + 假 M-Team/qbt。
//
// 覆盖 P5-1（未配置/禁用零调用）、P5-2（命中走私有种子）、P5-3（取种失败同轮降级
// 磁力并产生版本）、P10-2（降级后任务聚合仍然正确）。

const (
	// torrentHash 是夹具种子的真实 infohash（mteam.Infohash 对同一份字节的答案）。
	torrentHash = "f875b7bedb38e072dde96440fb79e5fc55699d68"
	// torrentBencode 是一份能用（v1、announce 带凭据）的 .torrent 字节。
	torrentBencode = "d8:announce47:https://tra1.m-team.cc/announce?credential=YWJj13:creation datei1791873283e4:infod6:lengthi1024e4:name5:a.mp412:piece lengthi16384e6:pieces20:012345678901234567897:privatei1eee"
)

// enableMTeam 把 M-Team 节配好并启用（向导里配了 api key 并打了勾）。
func (f *fixture) enableMTeam(t *testing.T) {
	t.Helper()
	enabled := true
	f.settings.snapshot.Sections["mteam"] = settings.SectionState{Configured: true, Enabled: &enabled}
	f.settings.inputs["mteam"] = settings.TestInput{
		Values:  map[string]any{"api_key": "mteam-key"},
		Enabled: true,
	}
}

// withTorrent 让假 M-Team 回一份可用的私有种子。
func (f *fixture) withTorrent() {
	f.torrents.torrent = mteam.Torrent{
		ID:        "467487",
		Name:      number + " 4K",
		Infohash:  torrentHash,
		Label:     mteam.LabelChinese,
		SizeBytes: 8 << 30,
		Bytes:     []byte(torrentBencode),
	}
}

// TestPollPrefersMTeamTorrent 钉住 P5-2 的主链：
// 搜索 → 命中 → 私有种子 → 本地 infohash → **先落库再加种**（multipart，不是磁力）。
func TestPollPrefersMTeamTorrent(t *testing.T) {
	f := newFixture(t)
	f.enableMTeam(t)
	f.withTorrent()
	f.withOneMovie(t)

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Torrents != 1 || result.Fallbacks != 0 || result.Candidates != 1 {
		t.Errorf("result = %+v, want 1 个种子、0 次降级、1 条候选", result)
	}

	if len(f.torrents.calls) != 1 || f.torrents.calls[0] != number {
		t.Errorf("M-Team 搜索 = %v, want 只按番号搜一次", f.torrents.calls)
	}
	if len(f.torrents.keys) != 1 || f.torrents.keys[0] != "mteam-key" {
		t.Errorf("M-Team api key = %v, want 设置里的原文", f.torrents.keys)
	}

	row := f.detail(t)
	if len(row.Versions) != 1 {
		t.Fatalf("版本数 = %d, want 1（命中就只走种子，不再落磁力版本）", len(row.Versions))
	}
	version := row.Versions[0]
	if version.Source != task.SourceMTeamTorrent || version.Delivery != task.DeliveryTorrent {
		t.Errorf("source/delivery = %s/%s, want mteam_torrent/torrent", version.Source, version.Delivery)
	}
	if version.Infohash != torrentHash {
		t.Errorf("infohash = %s, want %s（本地算出来的）", version.Infohash, torrentHash)
	}
	if version.Label != mteam.LabelChinese {
		t.Errorf("label = %q, want %q（labelsNew 判中字）", version.Label, mteam.LabelChinese)
	}
	if version.AddPending || !version.ListRemovalPending {
		t.Errorf("add_pending/list_removal_pending = %v/%v, want false/true",
			version.AddPending, version.ListRemovalPending)
	}
	if row.State != string(task.StatusDownloading) {
		t.Errorf("state = %q, want downloading", row.State)
	}

	// payload 存的是备份路径，字节原样在盘上（加种与人工重试都靠它）。
	stored, err := os.ReadFile(version.Payload)
	if err != nil {
		t.Fatalf("读 payload 备份 %s: %v", version.Payload, err)
	}
	if string(stored) != torrentBencode {
		t.Errorf("备份的种子字节与取回的不一致（长度 %d, want %d）", len(stored), len(torrentBencode))
	}

	if len(f.adder.calls) != 1 {
		t.Fatalf("加种调用 = %d, want 1", len(f.adder.calls))
	}
	added := f.adder.calls[0]
	if added.Magnet != "" {
		t.Errorf("加种磁力 = %q, want 空（私种只走 multipart）", added.Magnet)
	}
	if string(added.Body) != torrentBencode {
		t.Error("加种 Body 应当是 .torrent 字节")
	}
	if added.Filename != torrentHash+".torrent" {
		t.Errorf("multipart 文件名 = %q, want <infohash>.torrent", added.Filename)
	}
	if added.Number != number {
		t.Errorf("加种番号 = %q, want %q（tag 三件套靠它）", added.Number, number)
	}

	if keys := f.sideEffectKeys(t, "notify"); len(keys) != 1 || keys[0] != "download_started:"+torrentHash {
		t.Errorf("通知待办 = %v, want [download_started:%s]", keys, torrentHash)
	}
}

// TestPollFallsBackToMagnetWhenMTeamFails 钉住 P5-3：
// 取种失败 → 同轮落磁力版本（source/delivery/label 正确）+ 一条告警事件；
// 降级是设计好的路径，所以整轮不算失败（循环健康位不红）。
func TestPollFallsBackToMagnetWhenMTeamFails(t *testing.T) {
	f := newFixture(t)
	f.enableMTeam(t)
	f.torrents.err = errors.New("M-Team 下载链接已过期")
	f.withOneMovie(t)

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("降级不该让一轮 poll 失败: %v", err)
	}
	if result.Fallbacks != 1 || result.Torrents != 0 || result.Candidates != 1 {
		t.Errorf("result = %+v, want 1 次降级、0 个种子、1 条候选", result)
	}

	row := f.detail(t)
	if len(row.Versions) != 1 {
		t.Fatalf("版本数 = %d, want 1（降级只落磁力版本：M-Team 侧没有 infohash 可落）", len(row.Versions))
	}
	version := row.Versions[0]
	if version.Source != task.SourceJavDBMagnet || version.Delivery != task.DeliveryMagnet {
		t.Errorf("source/delivery = %s/%s, want javdb_magnet/magnet", version.Source, version.Delivery)
	}
	if version.Infohash != chineseHash {
		t.Errorf("infohash = %s, want %s（磁力定选的 cnsub 优先那条）", version.Infohash, chineseHash)
	}
	if version.Label != "中文" {
		t.Errorf("label = %q, want 中文", version.Label)
	}
	if row.State != string(task.StatusDownloading) {
		t.Errorf("state = %q, want downloading（聚合状态正确）", row.State)
	}

	if !hasEventWithReason(row, "M-Team") {
		t.Errorf("events = %+v, want 一条说明「M-Team 降级」的告警", row.Events)
	}

	if len(f.adder.calls) != 1 || f.adder.calls[0].Magnet == "" || len(f.adder.calls[0].Body) != 0 {
		t.Errorf("加种 = %+v, want 走磁力（降级后一条版本只下一种形态）", f.adder.calls)
	}
}

// TestPollFallsBackQuietlyWhenMTeamHasNoHit 确认「M-Team 上没有这部片」是答案不是故障：
// 照样降级磁力，但不在时间线上留一条故障告警（否则每部没收录的片都红一次）。
func TestPollFallsBackQuietlyWhenMTeamHasNoHit(t *testing.T) {
	f := newFixture(t)
	f.enableMTeam(t)
	f.torrents.err = mteam.NotFoundError{Number: number}
	f.withOneMovie(t)

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Fallbacks != 1 || result.Candidates != 1 {
		t.Errorf("result = %+v, want 1 次降级、1 条磁力候选", result)
	}

	row := f.detail(t)
	if row.Versions[0].Source != task.SourceJavDBMagnet {
		t.Errorf("source = %s, want javdb_magnet", row.Versions[0].Source)
	}
	if hasEvent(row, task.EventAlert) {
		t.Errorf("events = %+v, want 没有告警（搜不到不是故障）", row.Events)
	}
}

// TestPollDoesNotAskMTeamUnlessConfiguredAndEnabled 钉住 P5-1：未配置/禁用 → 零调用。
func TestPollDoesNotAskMTeamUnlessConfiguredAndEnabled(t *testing.T) {
	t.Run("未配置", func(t *testing.T) {
		f := newFixture(t)
		f.withOneMovie(t)

		if _, err := f.runner.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if len(f.torrents.calls) != 0 {
			t.Errorf("M-Team 搜索 = %v, want 0（未配置不打上游）", f.torrents.calls)
		}
		if f.detail(t).Versions[0].Source != task.SourceJavDBMagnet {
			t.Error("版本来源应当是 javdb_magnet")
		}
	})

	t.Run("配了但关着", func(t *testing.T) {
		f := newFixture(t)
		disabled := false
		f.settings.snapshot.Sections["mteam"] = settings.SectionState{Configured: true, Enabled: &disabled}
		f.settings.inputs["mteam"] = settings.TestInput{Values: map[string]any{"api_key": "mteam-key"}}
		f.withTorrent()
		f.withOneMovie(t)

		if _, err := f.runner.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if len(f.torrents.calls) != 0 {
			t.Errorf("M-Team 搜索 = %v, want 0（禁用不打上游）", f.torrents.calls)
		}
	})

	t.Run("启用了但没有 key", func(t *testing.T) {
		f := newFixture(t)
		enabled := true
		f.settings.snapshot.Sections["mteam"] = settings.SectionState{Configured: true, Enabled: &enabled}
		f.settings.inputs["mteam"] = settings.TestInput{Values: map[string]any{}}
		f.withTorrent()
		f.withOneMovie(t)

		if _, err := f.runner.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if len(f.torrents.calls) != 0 {
			t.Errorf("M-Team 搜索 = %v, want 0（没 key 不打上游）", f.torrents.calls)
		}
		// 已启用却用不了：降级要留痕，否则面板上「为什么走的是磁力」无从追溯。
		if !hasEventWithReason(f.detail(t), "api_key") {
			t.Errorf("events = %+v, want 一条说明 api_key 缺失的告警", f.detail(t).Events)
		}
	})
}

// TestPollDoesNotAskMTeamWhenJavDBHasNoMagnets 钉住限速意识（规格 §5.2）：
// JavDB 没有磁力就直接结束，不问 M-Team（搜索也是配额）。
func TestPollDoesNotAskMTeamWhenJavDBHasNoMagnets(t *testing.T) {
	f := newFixture(t)
	f.enableMTeam(t)
	f.withTorrent()
	f.source.items = []appapi.ListItem{{MovieID: movieID, Number: number}} // 没有磁力

	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(f.torrents.calls) != 0 {
		t.Errorf("M-Team 搜索 = %v, want 0（JavDB 没有磁力就不查 M-Team）", f.torrents.calls)
	}
}

// TestPollRetriesPendingTorrentVersionFromBackup 钉住 ADR-0004 的自愈在种子路径上的形态：
// 版本行已落库、加种没做成，下一轮从 payload 备份路径读字节补上。
func TestPollRetriesPendingTorrentVersionFromBackup(t *testing.T) {
	f := newFixture(t)
	path := writeTorrentBackup(t, f.torrentsDir, torrentBencode)
	inserted, err := f.store.SaveCandidate(context.Background(), torrentCandidate(path))
	if err != nil || !inserted {
		t.Fatalf("预置种子版本: inserted=%v err=%v", inserted, err)
	}
	f.source.items = []appapi.ListItem{{MovieID: movieID, Number: number}}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Added != 1 {
		t.Errorf("added = %d, want 1（pending 标记照旧重试）", result.Added)
	}
	if len(f.adder.calls) != 1 || string(f.adder.calls[0].Body) != torrentBencode {
		t.Errorf("加种 = %+v, want 从备份路径读出的种子字节", f.adder.calls)
	}
	if len(f.source.magnetCalls) != 0 {
		t.Errorf("磁力查询 = %v, want 0（任务已有版本）", f.source.magnetCalls)
	}
}

// TestPollKeepsPendingWhenTorrentBackupIsGone 钉住「瞬态目录被清空」的处置：
// 保留 pending、记一条告警，下轮再试（人工重试票 #31 收口），不静默丢版本。
func TestPollKeepsPendingWhenTorrentBackupIsGone(t *testing.T) {
	f := newFixture(t)
	missing := filepath.Join(t.TempDir(), "gone.torrent")
	inserted, err := f.store.SaveCandidate(context.Background(), torrentCandidate(missing))
	if err != nil || !inserted {
		t.Fatalf("预置种子版本: inserted=%v err=%v", inserted, err)
	}

	if _, err := f.runner.RunOnce(context.Background()); err == nil {
		t.Fatal("种子备份读不到时 RunOnce 应当回错误（循环健康位要红）")
	}
	if len(f.adder.calls) != 0 {
		t.Errorf("加种调用 = %d, want 0", len(f.adder.calls))
	}

	row := f.detail(t)
	if !row.Versions[0].AddPending {
		t.Error("add_pending = false, want true（保留 pending 下轮再试）")
	}
	if !hasEventWithReason(row, "私种备份") {
		t.Errorf("events = %+v, want 一条说明种子备份读不到的告警", row.Events)
	}
}

// ------------------------------------------------------------------ helpers

// torrentCandidate 是一条待加种的种子候选（payload 是备份路径）。
func torrentCandidate(path string) task.Candidate {
	return task.Candidate{
		MovieID:  movieID,
		Number:   number,
		ListID:   "wanted",
		Infohash: torrentHash,
		Source:   task.SourceMTeamTorrent,
		Delivery: task.DeliveryTorrent,
		Label:    mteam.LabelChinese,
		Payload:  path,
	}
}

// writeTorrentBackup 把种子字节写进备份目录，返回它的路径。
func writeTorrentBackup(t *testing.T, dir, data string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("建备份目录: %v", err)
	}
	path := filepath.Join(dir, torrentHash+".torrent")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("写种子备份: %v", err)
	}
	return path
}

// hasEventWithReason 报告事件时间线里有没有一条提到 needle 的事件。
func hasEventWithReason(row task.TaskRow, needle string) bool {
	for _, event := range row.Events {
		if strings.Contains(event.Reason, needle) {
			return true
		}
	}
	return false
}
