package backfill_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/backfill"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：internal/backfill 的两个公开方法——Scan（「未补录目录」列表）与
// Backfill（一键补录）。真 Postgres + 真文件系统，只有设置域是假的。

// fixture 是补录测试的一套挂具。
type fixture struct {
	store     *task.Store
	mediaRoot string
	runner    *backfill.Runner
	settings  *fakeSettings
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	database := dbtest.Migrated(t)
	if _, err := database.Pool().Exec(context.Background(),
		`INSERT INTO list_registry (list_id, purpose, name)
		 VALUES ('wanted', 'want_watch', '想看清单')
		 ON CONFLICT (list_id) DO NOTHING`); err != nil {
		t.Fatalf("种清单登记: %v", err)
	}
	store := task.NewStore(database.Pool())
	mediaRoot := t.TempDir()
	f := &fixture{
		store:     store,
		mediaRoot: mediaRoot,
		settings:  &fakeSettings{mediaRoot: mediaRoot, initialized: true},
	}
	f.runner = backfill.New(backfill.Options{
		Store:    store,
		Settings: f.settings,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return f
}

// writeDir 造一个补录目录：文件名 → 内容。
func writeDir(t *testing.T, root string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, "SONE-682")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("写 %s: %v", name, err)
		}
	}
	return dir
}

// ------------------------------------------------------------------ 扫描

func TestScanListsUnclaimedDirs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	writeDir(t, f.mediaRoot, map[string]string{
		"SONE-682.mkv":   "v1",
		"SONE-682-C.mkv": "v2",
		"poster.jpg":     "img",
	})
	if err := os.MkdirAll(filepath.Join(f.mediaRoot, "SONE-720"), 0o755); err != nil {
		t.Fatalf("建空目录: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(f.mediaRoot, ".stfolder"), 0o755); err != nil {
		t.Fatalf("建隐藏目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.mediaRoot, "loose.mkv"), []byte("v"), 0o644); err != nil {
		t.Fatalf("写散文件: %v", err)
	}

	// 任务表里已有 SONE-720：它不算「未补录」。
	if _, err := f.store.SaveCandidate(ctx, task.Candidate{
		MovieID: "abc123", Number: "SONE-720", ListID: "wanted",
		Infohash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Source:   task.SourceJavDBMagnet, Delivery: task.DeliveryMagnet,
		Payload: "magnet:?xt=urn:btih:aaaaaaaa",
	}); err != nil {
		t.Fatalf("种已有任务: %v", err)
	}

	dirs, err := f.runner.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("未补录目录 = %+v, want 只剩 SONE-682", dirs)
	}
	if dirs[0].Number != "SONE-682" {
		t.Errorf("number = %q, want SONE-682", dirs[0].Number)
	}
	if dirs[0].Videos != 2 {
		t.Errorf("videos = %d, want 2（散文件与隐藏目录不算）", dirs[0].Videos)
	}
	if dirs[0].Bytes != int64(len("v1")+len("v2")) {
		t.Errorf("bytes = %d, want %d", dirs[0].Bytes, len("v1")+len("v2"))
	}
}

func TestScanTracksTaskTableLive(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	writeDir(t, f.mediaRoot, map[string]string{"SONE-682.mkv": "v"})

	dirs, err := f.runner.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("补录前 = %+v, want 1 条", dirs)
	}

	// 补录成功后同一个目录立刻从列表里消失：列表与任务表实时一致（验收原文）。
	if _, err := f.runner.Backfill(ctx, "SONE-682"); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	dirs, err = f.runner.Scan(ctx)
	if err != nil {
		t.Fatalf("补录后 Scan: %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("补录后 = %+v, want 空", dirs)
	}
}

func TestScanGated(t *testing.T) {
	f := newFixture(t)
	f.settings.initialized = false

	if _, err := f.runner.Scan(context.Background()); !errors.Is(err, backfill.ErrGated) {
		t.Fatalf("err = %v, want ErrGated", err)
	}
}

// ------------------------------------------------------------------ 一键补录

func TestBackfillRegistersDirAndDeletesLegacyNfos(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	dir := writeDir(t, f.mediaRoot, map[string]string{
		"SONE-682.mkv":     "video-main",
		"SONE-682-C.mkv":   "video-chinese",
		"SONE-682.nfo":     "mdcng-era nfo",
		"SONE-682-C.nfo":   "mdcng-era nfo C",
		"SONE-682-4k.nfo":  "mdcng-era nfo 4k",
		"movie.nfo":        " movie.nfo 占位（补录时还没有 omd 形状，重刮后才重写）",
		"poster.jpg":       "img",
		"SONE-682.chs.srt": "sub",
	})

	result, err := f.runner.Backfill(ctx, "SONE-682")
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if result.Outcome != backfill.OutcomeBackfilled {
		t.Fatalf("outcome = %q, want %q", result.Outcome, backfill.OutcomeBackfilled)
	}
	if result.Number != "SONE-682" || result.MovieID != "SONE-682" {
		t.Errorf("回执 = %+v, want 番号即任务主键", result)
	}
	if result.Versions != 2 {
		t.Errorf("versions = %d, want 2", result.Versions)
	}

	// mdcng 时代的 {番号}*.nfo 消失；movie.nfo、图片、字幕、视频原样。
	for _, name := range []string{"SONE-682.nfo", "SONE-682-C.nfo", "SONE-682-4k.nfo"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s 还在（err=%v），want 已删除", name, err)
		}
	}
	for _, name := range []string{"movie.nfo", "poster.jpg", "SONE-682.chs.srt", "SONE-682.mkv", "SONE-682-C.mkv"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s 不该被动（err=%v）", name, err)
		}
	}

	row, err := f.store.Detail(ctx, "SONE-682", 50)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(row.Versions) != 2 {
		t.Fatalf("版本数 = %d, want 2", len(row.Versions))
	}
	for _, version := range row.Versions {
		if version.Source != task.SourceBackfill || version.Delivery != task.DeliveryNone ||
			version.Status != task.StatusProcessing || !version.ForceRescrape || version.AddPending {
			t.Errorf("版本 %+v 不像补录登记的样子", version)
		}
	}
}

func TestBackfillIsIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	writeDir(t, f.mediaRoot, map[string]string{
		"SONE-682.mkv":   "video-main",
		"SONE-682.nfo":   "mdcng-era nfo",
		"SONE-682-C.nfo": "mdcng-era nfo C",
	})

	if _, err := f.runner.Backfill(ctx, "SONE-682"); err != nil {
		t.Fatalf("第一次 Backfill: %v", err)
	}
	afterFirst, err := os.ReadDir(filepath.Join(f.mediaRoot, "SONE-682"))
	if err != nil {
		t.Fatalf("读目录: %v", err)
	}

	result, err := f.runner.Backfill(ctx, "SONE-682")
	if err != nil {
		t.Fatalf("第二次 Backfill: %v", err)
	}
	if result.Outcome != backfill.OutcomeDuplicate {
		t.Fatalf("outcome = %q, want %q（幂等放行）", result.Outcome, backfill.OutcomeDuplicate)
	}

	afterSecond, err := os.ReadDir(filepath.Join(f.mediaRoot, "SONE-682"))
	if err != nil {
		t.Fatalf("读目录: %v", err)
	}
	if len(afterFirst) != len(afterSecond) {
		t.Fatalf("重复点击改动了目录：%d → %d 项", len(afterFirst), len(afterSecond))
	}

	row, err := f.store.Detail(ctx, "SONE-682", 100)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(row.Versions) != 1 {
		t.Fatalf("版本数 = %d, want 1（不重复建任务/版本）", len(row.Versions))
	}
}

// TestBackfillKeepsLegacyNfosWhenRegistrationFails 钉住顺序（规格 §6.3）：删旧
// NFO 排在登记成功之后。先删后登记的崩溃窗口是「旧件已删、任务未建」——目录
// 从「未补录」里消失却没有任何东西接管它；反过来（登记→删）登记失败时旧件
// 原地不动，最坏也只是留下无害死重（Jellyfin 读 movie.nfo 优先）。
func TestBackfillKeepsLegacyNfosWhenRegistrationFails(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 查重预检之后、落库之前取消上下文（落库事务起不来）：模拟登记失败。
	f.settings.inputHook = func(section string) {
		if section == "jellyfin" {
			cancel()
		}
	}

	dir := writeDir(t, f.mediaRoot, map[string]string{
		"SONE-682.mkv": "video-main",
		"SONE-682.nfo": "mdcng-era nfo",
	})

	if _, err := f.runner.Backfill(ctx, "SONE-682"); err == nil {
		t.Fatal("登记失败竟被吞掉，want 报错")
	}

	// 登记没成：旧 NFO 原样，任务也没建（零写入）。
	if _, err := os.Stat(filepath.Join(dir, "SONE-682.nfo")); err != nil {
		t.Errorf("登记失败后旧 NFO 被动了（err=%v），want 原样", err)
	}
	if _, taken, err := f.store.TaskIDByNumber(context.Background(), "SONE-682", ""); err != nil || taken {
		t.Errorf("任务竟建了（taken=%v err=%v），want 零写入", taken, err)
	}
}

func TestBackfillDefersToExistingTaskOfSameNumber(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	writeDir(t, f.mediaRoot, map[string]string{"SONE-682.mkv": "v"})
	// 同番号的任务已经存在（轮询/手工登记来的，另一个 movie_id）：
	// 一番号一目录（ADR-0008），补录不抢，幂等放行。
	if _, err := f.store.SaveCandidate(ctx, task.Candidate{
		MovieID: "abc123", Number: "SONE-682", ListID: "wanted",
		Infohash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Source:   task.SourceJavDBMagnet, Delivery: task.DeliveryMagnet,
		Payload: "magnet:?xt=urn:btih:aaaaaaaa",
	}); err != nil {
		t.Fatalf("种已有任务: %v", err)
	}

	result, err := f.runner.Backfill(ctx, "SONE-682")
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if result.Outcome != backfill.OutcomeDuplicate {
		t.Fatalf("outcome = %q, want %q", result.Outcome, backfill.OutcomeDuplicate)
	}

	// 零写入：已有任务毫发无损，目录也没被清。
	row, err := f.store.Detail(ctx, "abc123", 50)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(row.Versions) != 1 {
		t.Errorf("已有任务的版本数 = %d, want 1", len(row.Versions))
	}
	if _, err := os.Stat(filepath.Join(f.mediaRoot, "SONE-682", "SONE-682.mkv")); err != nil {
		t.Errorf("目录被动了: %v", err)
	}
}

func TestBackfillGated(t *testing.T) {
	f := newFixture(t)
	f.settings.initialized = false

	if _, err := f.runner.Backfill(context.Background(), "SONE-682"); !errors.Is(err, backfill.ErrGated) {
		t.Fatalf("err = %v, want ErrGated", err)
	}
}

func TestBackfillRejectsEmptyInput(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.runner.Backfill(ctx, ""); !errors.Is(err, backfill.ErrBadNumber) {
		t.Errorf("空番号: err = %v, want ErrBadNumber", err)
	}
	if _, err := f.runner.Backfill(ctx, "../escape"); err == nil {
		t.Errorf("带路径分隔符的番号竟被接受")
	}
	if _, err := f.runner.Backfill(ctx, "GONE-999"); !errors.Is(err, backfill.ErrDirMissing) {
		t.Errorf("目录不在盘上: err = %v, want ErrDirMissing", err)
	}

	// 没有视频的目录不是可登记的作品目录。
	if err := os.MkdirAll(filepath.Join(f.mediaRoot, "EMPTY-001"), 0o755); err != nil {
		t.Fatalf("建空目录: %v", err)
	}
	if _, err := f.runner.Backfill(ctx, "EMPTY-001"); !errors.Is(err, backfill.ErrNoVideos) {
		t.Errorf("空目录: err = %v, want ErrNoVideos", err)
	}
}

// ------------------------------------------------------------------ 假件

type fakeSettings struct {
	mediaRoot   string
	initialized bool
	// inputHook 在每次 Input 被调时触发（测试用它把上下文取消在预检之后、
	// 落库之前：模拟登记失败）。
	inputHook func(section string)
}

func (f *fakeSettings) Load(context.Context) (settings.Snapshot, error) {
	return settings.Snapshot{Initialized: f.initialized}, nil
}

func (f *fakeSettings) Input(_ context.Context, section string) (settings.TestInput, error) {
	if f.inputHook != nil {
		f.inputHook(section)
	}
	if section != "jellyfin" {
		return settings.TestInput{}, settings.ErrNotConfigured
	}
	return settings.TestInput{Values: map[string]any{"media_root": f.mediaRoot}}, nil
}
