package organize_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/organize"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 补录（#43）与整理段的接缝：source=backfill 的版本重入库时不问 qBittorrent、
// 不硬链、不动 tag、不下图（视频与图片原地复用，GLOSSARY「补录」）——
// 只按取好的元数据重写 movie.nfo，然后走与普通入库同一条收尾：刷库/「入库完成」
// 通知入箱、落 finished、按四面判据标「待补齐」。

func TestBackfillRefileRewritesNfoWithoutQbt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fetchedAt := time.Now().Add(-time.Hour).UTC()

	// 目录里只有补录前的现场：视频已就位，旧 NFO 已在补录时删掉，movie.nfo 不存在。
	dir := filepath.Join(f.mediaRoot, backfillNumber)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	for name, content := range map[string]string{
		"SONE-682.mkv":     "video-main",
		"SONE-682-C.mkv":   "video-cn",
		"poster.jpg":       "old-img",
		"SONE-682.chs.srt": "sub",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("写 %s: %v", name, err)
		}
	}

	// 一条补录登记出来的版本：processing + force_rescrape + 分片账已落。
	hashMain := task.BackfillInfohash(number, "SONE-682.mkv")
	hashCN := task.BackfillInfohash(number, "SONE-682-C.mkv")
	seedBackfillVersion(t, f, backfillNumber, hashMain, "SONE-682.mkv", 111)
	seedBackfillVersion(t, f, backfillNumber, hashCN, "SONE-682-C.mkv", 222)

	// 元数据已取齐（取数段的前一步成果）。
	sleeve := "https://pics.test/ssis001pl.jpg"
	f.images.bodies[sleeve] = imageBytes(t, 1200, 800, 220, "jpeg")
	if err := f.store.SaveMetadata(ctx, backfillNumber,
		task.Lease{Infohash: hashMain, Owner: "scrape"}, metadata(fetchedAt, []string{sleeve}, nil), nil,
	); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	progress, err := f.runner.ProcessPending(ctx)
	if err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if progress.Filed != 2 || progress.Failed != 0 {
		t.Fatalf("progress = %+v, want 两条版本都入库", progress)
	}

	// 没碰过 qBittorrent：回读与 tag 换装都不该发生（零下载，GLOSSARY「补录」）。
	if f.qbt.calls != 0 {
		t.Errorf("qbt 回读 %d 次, want 0", f.qbt.calls)
	}
	if len(f.tags.calls) != 0 {
		t.Errorf("tag 换装 %v, want 0 次", f.tags.calls)
	}

	// omd 形状的 movie.nfo 出现在目录里，槽位值来自元数据。
	nfo, err := os.ReadFile(filepath.Join(dir, organize.NFOName))
	if err != nil {
		t.Fatalf("movie.nfo 不存在: %v", err)
	}
	for _, want := range []string{"<title>SONE-682 一ヶ月間の禁欲の果てに</title>", "<originaltitle>SONE-682", "<plot>", nfoTagSlot} {
		if !strings.Contains(string(nfo), want) {
			t.Errorf("movie.nfo 缺 %q：\n%s", want, nfo)
		}
	}

	// 视频与字幕原样（不重命名、不硬链、不多文件）。
	for name, content := range map[string]string{
		"SONE-682.mkv": "video-main", "SONE-682-C.mkv": "video-cn", "SONE-682.chs.srt": "sub",
	} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(body) != content {
			t.Errorf("%s 变了（err=%v）", name, err)
		}
	}

	// 两条版本都 finished；分片账保持登记时的原样（不重算）。
	for _, hash := range []string{hashMain, hashCN} {
		version := f.version(t, backfillNumber, hash)
		if version.Status != task.StatusFinished {
			t.Errorf("版本 %s 状态 = %q, want finished", hash, version.Status)
		}
		files, err := f.store.VersionFiles(ctx, hash)
		if err != nil {
			t.Fatalf("VersionFiles: %v", err)
		}
		if len(files) != 1 {
			t.Errorf("版本 %s 的分片账 = %d 行, want 1（登记时的原账）", hash, len(files))
		}
	}

	// 副作用照发：每条版本一条「入库完成」通知 + 一次刷库（同任务 coalesce 成一条）。
	var notifies, refreshes, downloadStarts int
	rows, err := f.database.Pool().Query(ctx,
		`SELECT kind, target_key FROM side_effect WHERE done_at IS NULL`)
	if err != nil {
		t.Fatalf("查副作用待办: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, target string
		if err := rows.Scan(&kind, &target); err != nil {
			t.Fatalf("扫待办: %v", err)
		}
		switch {
		case kind == "notify" && strings.HasPrefix(target, "filed:"):
			notifies++
		case kind == "jellyfin_refresh":
			refreshes++
		case kind == "notify" && strings.HasPrefix(target, "download_started:"):
			downloadStarts++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历待办: %v", err)
	}
	if notifies != 2 || refreshes != 1 || downloadStarts != 0 {
		t.Errorf("副作用 = filed:%d refresh:%d download_started:%d, want 2/1/0",
			notifies, refreshes, downloadStarts)
	}
}

const (
	backfillNumber = "SONE-682"
	nfoTagSlot     = "<tag>"
)

// seedBackfillVersion 种一条补录登记形状的版本（与 task.SaveBackfill 落出的一致）。
func seedBackfillVersion(t *testing.T, f *fixture, number, infohash, relPath string, size int64) {
	t.Helper()
	ctx := context.Background()

	if _, err := f.database.Pool().Exec(ctx,
		`INSERT INTO task (movie_id, number, list_id) VALUES ($1, $1, NULL)
		 ON CONFLICT (movie_id) DO NOTHING`,
		number,
	); err != nil {
		t.Fatalf("种补录任务: %v", err)
	}
	if _, err := f.database.Pool().Exec(ctx,
		`INSERT INTO task_version
		     (infohash, movie_id, source, delivery, status, force_rescrape, add_pending, payload)
		 VALUES ($1, $2, 'backfill', 'none', 'processing', true, false, '')`,
		infohash, number,
	); err != nil {
		t.Fatalf("种补录版本: %v", err)
	}
	if _, err := f.database.Pool().Exec(ctx,
		`INSERT INTO version_file (infohash, rel_path, size_bytes, part_index, is_primary)
		 VALUES ($1, $2, $3, 1, true)`,
		infohash, relPath, size,
	); err != nil {
		t.Fatalf("种分片账: %v", err)
	}
}
