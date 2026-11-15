package backfill_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/backfill"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/dmm"
	"github.com/2017fighting/oh-my-mdc/internal/organize"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/scrape"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 这条用例是 issue #43 验收原文的端到端追踪：对 SONE-682 补录 →
//   - 目录里出现 omd 形状 movie.nfo；
//   - SONE-682.nfo / SONE-682-C.nfo / SONE-682-4k.nfo 消失；
//   - 视频文件字节不变（硬链不变多——目录里没有多出来的文件）；
//   - 副作用照发（Jellyfin 刷新、「入库完成」通知）、「下载开始」不发；
//   - 「未补录目录」列表与任务表实时一致；重复补录幂等；演员面结构性缺席不计
//     待补齐（规格 §6.3：取数只问 DMM，缺演员不是强刮能修的，标了就永远摘不掉）。
//
// 只有上游是假的（httptest / 进程内假件），其余全是真的：真 Postgres、真文件系统、
// 真的扫描-登记-取数-整理四段。

func TestEndToEndBackfillToRescrapeToFiled(t *testing.T) {
	ctx := context.Background()
	const number = "SONE-682"

	// ---------------------------------------------------------- 文件系统：mdcng 时代的现场
	mediaRoot := t.TempDir()
	dir := filepath.Join(mediaRoot, number)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	mustWrite(t, filepath.Join(dir, "SONE-682.mkv"), "video-main-bytes")
	mustWrite(t, filepath.Join(dir, "SONE-682-C.mkv"), "video-chinese-bytes")
	mustWrite(t, filepath.Join(dir, "SONE-682.nfo"), "mdcng-era nfo")
	mustWrite(t, filepath.Join(dir, "SONE-682-C.nfo"), "mdcng-era nfo C")
	mustWrite(t, filepath.Join(dir, "SONE-682-4k.nfo"), "mdcng-era nfo 4k")
	mustWrite(t, filepath.Join(dir, "poster.jpg"), "mdcng-era poster")
	mustWrite(t, filepath.Join(dir, "SONE-682.chs.srt"), "subtitle")

	// ---------------------------------------------------------- 库与设置
	database := dbtest.Migrated(t)
	if _, err := database.Pool().Exec(ctx,
		`INSERT INTO list_registry (list_id, purpose, name) VALUES ('wanted', 'want_watch', '想看清单')
		 ON CONFLICT (list_id) DO NOTHING`); err != nil {
		t.Fatalf("种清单登记: %v", err)
	}
	store := task.NewStore(database.Pool())
	outbox := sidefx.New(database.Pool())

	settingsFake := &pipelineSettings{
		snapshot: settings.Snapshot{
			Initialized: true,
			Sections: map[string]settings.SectionState{
				"app": {Configured: true, Data: map[string]any{"max_attempts": 3}},
			},
		},
		inputs: map[string]settings.TestInput{
			"javdb":       {Values: map[string]any{"token": "tk"}},
			"jellyfin":    {Values: map[string]any{"media_root": mediaRoot}},
			"qbittorrent": {Values: map[string]any{"url": "http://qbt.invalid", "category": "mdc"}},
		},
	}

	// ---------------------------------------------------------- 补录：扫描 + 一键
	backfiller := backfill.New(backfill.Options{
		Store: store, Settings: settingsFake,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	dirs, err := backfiller.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(dirs) != 1 || dirs[0].Number != number {
		t.Fatalf("未补录目录 = %+v, want [SONE-682]", dirs)
	}

	result, err := backfiller.Backfill(ctx, number)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if result.Outcome != backfill.OutcomeBackfilled || result.Versions != 2 {
		t.Fatalf("回执 = %+v, want backfilled/2 版本", result)
	}

	// 旧 NFO 当场消失；视频、字幕、旧图原样。
	for _, name := range []string{"SONE-682.nfo", "SONE-682-C.nfo", "SONE-682-4k.nfo"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s 没删掉（err=%v）", name, err)
		}
	}
	mainBytes, err := os.ReadFile(filepath.Join(dir, "SONE-682.mkv"))
	if err != nil || string(mainBytes) != "video-main-bytes" {
		t.Fatalf("视频字节变了（err=%v）", err)
	}

	// 列表与任务表实时一致：任务一落，目录就从「未补录」里消失。
	dirs, err = backfiller.Scan(ctx)
	if err != nil {
		t.Fatalf("补录后 Scan: %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("补录后列表 = %+v, want 空", dirs)
	}

	// ---------------------------------------------------------- 取数段（DMM 假件）
	pics := newPicsServer(t)
	sleeve := pics.URL + "/pl.jpg"
	dmmResult := dmm.Result{
		Found: true,
		Content: dmm.Content{
			ID:               "sonex00682",
			Title:            "モテる秘訣はマシンガン拼音",
			Description:      "补录后的简介来自 DMM。",
			ReleaseDate:      "2024-05-21",
			PackageLargeURL:  sleeve,
			PackageMediumURL: "https://pics.test/ps.jpg",
		},
		SearchRaw:  json.RawMessage(`{"found":"by-search"}`),
		ContentRaw: json.RawMessage(`{"found":"by-content"}`),
	}
	scrapeRunner := scrape.New(scrape.Options{
		Store: store, Settings: settingsFake, Outbox: outbox,
		DMM: &staticDMM{result: dmmResult},
		// 补录任务没有 movie_id：JavDB 假件一旦被碰就让测试炸出来。
		JavDB:  &forbiddenJavDB{t: t},
		Owner:  "e2e-scrape",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	fetchProgress, err := scrapeRunner.ProcessPending(ctx)
	if err != nil {
		t.Fatalf("scrape ProcessPending: %v", err)
	}
	if fetchProgress.Fetched != 2 {
		t.Fatalf("取数进度 = %+v, want 两条版本都取齐", fetchProgress)
	}

	// ---------------------------------------------------------- 整理段（重入库）
	organizer := organize.New(organize.Options{
		Store: store, Outbox: outbox, Settings: settingsFake,
		Qbt:    &forbiddenQbt{t: t},
		Tags:   &forbiddenTags{t: t},
		Images: organize.NewHTTPImages(pics.Client()),
		Owner:  "e2e-organize",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	fileProgress, err := organizer.ProcessPending(ctx)
	if err != nil {
		t.Fatalf("organize ProcessPending: %v", err)
	}
	if fileProgress.Filed != 2 || fileProgress.Failed != 0 {
		t.Fatalf("整理进度 = %+v, want 两条版本都入库", fileProgress)
	}

	// ---------------------------------------------------------- 验收逐条
	// omd 形状 movie.nfo（槽位来自 DMM 元数据）。
	nfo, err := os.ReadFile(filepath.Join(dir, organize.NFOName))
	if err != nil {
		t.Fatalf("movie.nfo 不存在: %v", err)
	}
	for _, want := range []string{
		"<title>SONE-682 モテる秘訣はマシンガン拼音</title>",
		"<plot>补录后的简介来自 DMM。</plot>",
	} {
		if !strings.Contains(string(nfo), want) {
			t.Errorf("movie.nfo 缺 %q：\n%s", want, nfo)
		}
	}
	// release_date 按 §7.1 字段图只归 JavDB，补录没有 movie_id 可查 → 没有 premiered。
	// 这是「演员/发行信息等 JavDB 侧面缺席」的同一件事的另一面，钉在这里防回归猜错。
	if strings.Contains(string(nfo), "<premiered>") {
		t.Errorf("movie.nfo 竟有 premiered（字段图没这么映射）：\n%s", nfo)
	}

	// 视频字节不变、目录里没有多出来的硬链。图片按整理段常规逻辑重写
	// （GLOSSARY「补录」：强制重刮重写 NFO 与图片）——poster 从 DMM sleeve
	// 右裁、fanart 原横版，都在原地；旧 mdcng 图被新图顶替。
	afterBytes, err := os.ReadFile(filepath.Join(dir, "SONE-682.mkv"))
	if err != nil || string(afterBytes) != "video-main-bytes" {
		t.Errorf("重入库后视频字节变了（err=%v）", err)
	}
	if poster, err := os.ReadFile(filepath.Join(dir, "poster.jpg")); err != nil || string(poster) == "mdcng-era poster" {
		t.Errorf("poster 没按新元数据重写（err=%v）", err)
	}
	names := dirNames(t, dir)
	wantNames := map[string]bool{
		"SONE-682.mkv": true, "SONE-682-C.mkv": true, "SONE-682.chs.srt": true,
		"movie.nfo": true, "poster.jpg": true, "fanart.jpg": true,
	}
	for name := range names {
		if !wantNames[name] {
			t.Errorf("目录里多出 %s（硬链/重写不该发生）", name)
		}
	}
	for name := range wantNames {
		if !names[name] {
			t.Errorf("目录里缺 %s", name)
		}
	}

	// 两条版本 finished，任务状态聚合为 finished。
	row, err := store.Detail(ctx, number, 50)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if row.State != string(task.StatusFinished) {
		t.Errorf("任务状态 = %q, want finished", row.State)
	}

	// 副作用照发：「入库完成」通知两条 + Jellyfin 刷新一条（同任务合并）；
	// 「下载开始」不发——无下载。
	todos, err := database.Pool().Query(ctx,
		`SELECT kind, target_key FROM side_effect WHERE done_at IS NULL`)
	if err != nil {
		t.Fatalf("查副作用待办: %v", err)
	}
	var filed, started, refresh int
	for todos.Next() {
		var kind, target string
		if err := todos.Scan(&kind, &target); err != nil {
			t.Fatalf("扫待办: %v", err)
		}
		switch {
		case kind == "notify" && strings.HasPrefix(target, "filed:"):
			filed++
		case kind == "notify" && strings.HasPrefix(target, "download_started:"):
			started++
		case kind == "jellyfin_refresh":
			refresh++
		}
	}
	todos.Close()
	if filed != 2 || started != 0 || refresh != 1 {
		t.Errorf("副作用 = filed:%d download_started:%d refresh:%d, want 2/0/1", filed, started, refresh)
	}

	// 待补齐不标：DMM 把图/简介/日文标题三面带齐了，剩下的演员面对补录是
	// 结构性永缺（规格 §6.3：取数只问 DMM，演员只来自 JavDB，强刮也变不出来），
	// 不计入待补齐——否则 #42 的一键强刮摘标永远摘不掉。
	incomplete, err := store.Incomplete(ctx, 10)
	if err != nil {
		t.Fatalf("Incomplete: %v", err)
	}
	for _, item := range incomplete {
		if item.MovieID == number {
			t.Errorf("补录任务竟标了待补齐 %v（演员面应豁免，其余三面 DMM 已带齐）", item.MissingFaces)
		}
	}

	// 重复补录幂等：再点一次零写入、零删除，版本还是两条。
	again, err := backfiller.Backfill(ctx, number)
	if err != nil {
		t.Fatalf("重复补录: %v", err)
	}
	if again.Outcome != backfill.OutcomeDuplicate {
		t.Errorf("重复补录结局 = %q, want duplicate", again.Outcome)
	}
	row, err = store.Detail(ctx, number, 100)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(row.Versions) != 2 {
		t.Errorf("重复补录后版本数 = %d, want 2", len(row.Versions))
	}
}

// ------------------------------------------------------------------ 假件

// imageBytes 造一张纯色图（默认 jpeg）。与 organize_test.go 同一形状：
// 整理段的探针要拿「宽>高」的真图才能右裁。
func imageBytes(t *testing.T, width, height int, shade uint8, format string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	var buffer bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&buffer, img)
	} else {
		err = jpeg.Encode(&buffer, img, nil)
	}
	if err != nil {
		t.Fatalf("encode image: %v", err)
	}
	return buffer.Bytes()
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写 %s: %v", path, err)
	}
}

func dirNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读目录: %v", err)
	}
	names := map[string]bool{}
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	return names
}

// staticDMM 是恒命中的 DMM 假件。
type staticDMM struct{ result dmm.Result }

func (f *staticDMM) Lookup(context.Context, string) (dmm.Result, error) { return f.result, nil }

// forbiddenJavDB 让「补录流程碰了 JavDB」直接炸测试。
type forbiddenJavDB struct{ t *testing.T }

func (f *forbiddenJavDB) Detail(_ context.Context, _, movieID string) (appapi.DetailResult, error) {
	f.t.Errorf("补录流程碰了 JavDB 详情（movie_id=%s）", movieID)
	return appapi.DetailResult{}, nil
}

// forbiddenQbt / forbiddenTags 同理：补录的整理段不该有 qBittorrent 动作。
type forbiddenQbt struct{ t *testing.T }

func (f *forbiddenQbt) Lookup(context.Context, qbittorrent.Config, string) (qbittorrent.TorrentStatus, bool, error) {
	f.t.Errorf("补录的整理段碰了 qBittorrent 回读")
	return qbittorrent.TorrentStatus{}, false, nil
}

type forbiddenTags struct{ t *testing.T }

func (f *forbiddenTags) TransitionTag(context.Context, qbittorrent.Config, string, string, string) error {
	f.t.Errorf("补录的整理段碰了 tag 换装")
	return nil
}

// pipelineSettings 是四段共用的设置假件（Load + Input 同一形状）。
type pipelineSettings struct {
	snapshot settings.Snapshot
	inputs   map[string]settings.TestInput
}

func (f *pipelineSettings) Load(context.Context) (settings.Snapshot, error) {
	return f.snapshot, nil
}

func (f *pipelineSettings) Input(_ context.Context, section string) (settings.TestInput, error) {
	input, ok := f.inputs[section]
	if !ok {
		return settings.TestInput{}, settings.ErrNotConfigured
	}
	return input, nil
}

// newPicsServer 起一个只回一张横版 sleeve 的图片站（整理段真发 HTTP 下图用）。
func newPicsServer(t *testing.T) *httptest.Server {
	t.Helper()
	body := imageBytes(t, 1200, 800, 200, "jpeg")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pl.jpg" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}
