package organize_test

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/organize"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// TestProcessPendingFilesTheWholeTree 是 P9-3/P9-4/P11-1/P14-1 的主链：
// 分片硬链进一番号一目录 → movie.nfo → 三类图片（poster 右裁 2:3）→ 换 tag
// → 刷库/通知入箱 → 版本 finished。
func TestProcessPendingFilesTheWholeTree(t *testing.T) {
	f := newFixture(t)
	fetchedAt := time.Now().Add(-time.Hour).UTC()

	// pl 是横版、ps 是竖版（规格 §5.3：取宽>高者），都能下到。
	plURL := "https://pics.test/ssis001pl.jpg"
	psURL := "https://pics.test/ssis001ps.jpg"
	f.images.bodies[plURL] = imageBytes(t, 1200, 800, 255, "jpeg")
	f.images.bodies[psURL] = imageBytes(t, 800, 1200, 10, "jpeg")
	extras := []string{"https://pics.test/jp-1.jpg", "https://pics.test/jp-2.jpg"}
	for i, url := range extras {
		f.images.bodies[url] = imageBytes(t, 1600, 900, uint8(60+i), "jpeg")
	}

	writeDownloads(t, f.downloads, map[string]string{
		"ABP-123-cd1.mkv":     "video-one",
		"ABP-123-cd2.mkv":     "video-two",
		"ABP-123-cd1.chs.srt": "sub-one",
		"ABP-123-cd2.srt":     "sub-two",
		"readme.txt":          "广告",
		"cover.jpg":           "广告图",
		"ABP-123.nfo":         "种子自带的 nfo",
	})
	f.addProcessingVersion(t, movieID, number, hashA, "中文",
		metadata(fetchedAt, []string{plURL, psURL}, extras))

	progress, err := f.runner.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if progress.Filed != 1 || progress.Failed != 0 {
		t.Fatalf("progress = %+v, want Filed=1", progress)
	}

	dir := filepath.Join(f.mediaRoot, number)
	// 多片全显式含 cd1，且是硬链（零拷贝）；字幕随各自分片。
	for target, source := range map[string]string{
		"ABP-123-中文-cd1.mkv":     "ABP-123-cd1.mkv",
		"ABP-123-中文-cd2.mkv":     "ABP-123-cd2.mkv",
		"ABP-123-中文-cd1.chs.srt": "ABP-123-cd1.chs.srt",
		"ABP-123-中文-cd2.srt":     "ABP-123-cd2.srt",
	} {
		mustExist(t, filepath.Join(dir, target))
		if !sameInode(t, filepath.Join(dir, target), filepath.Join(f.downloads, source)) {
			t.Errorf("%s 不是硬链", target)
		}
	}
	// 视频/字幕之外的文件不落盘。
	for _, unwanted := range []string{"readme.txt", "cover.jpg", "ABP-123.nfo"} {
		mustNotExist(t, filepath.Join(dir, unwanted))
	}

	// movie.nfo：固定名 + 全槽位。
	nfo := readFile(t, filepath.Join(dir, organize.NFOName))
	for _, want := range []string{
		"<sorttitle>ABP-123</sorttitle>",
		"<originaltitle>ABP-123 一ヶ月間の禁欲の果てに</originaltitle>",
		"<title>ABP-123 一ヶ月間の禁欲の果てに</title>",
		"<plot>S1スリム美女優の豪華共演</plot>",
		"<premiered>2021-02-19</premiered>",
		"<releasedate>2021-02-19</releasedate>",
		"<tag>发行: S1</tag>",
		"<thumb>https://img.test/f.jpg</thumb>",
	} {
		if !strings.Contains(nfo, want) {
			t.Errorf("nfo 缺 %s\n---\n%s", want, nfo)
		}
	}
	if strings.Contains(nfo, "<genre>") {
		t.Error("标签走 <tag>，不许写 <genre>")
	}
	female := strings.Index(nfo, "女优乙")
	male := strings.Index(nfo, "男优甲")
	if female < 0 || male < 0 || female > male {
		t.Errorf("演员顺序不对（女优排前）：female=%d male=%d", female, male)
	}

	// poster = 横版 sleeve 右裁 2:3（1200×800 → 533×800）；fanart = 原横版。
	posterWidth, posterHeight := imageSize(t, filepath.Join(dir, "poster.jpg"))
	if posterWidth != 533 || posterHeight != 800 {
		t.Errorf("poster = %dx%d, want 533x800（右裁 2:3）", posterWidth, posterHeight)
	}
	fanartWidth, fanartHeight := imageSize(t, filepath.Join(dir, "fanart.jpg"))
	if fanartWidth != 1200 || fanartHeight != 800 {
		t.Errorf("fanart = %dx%d, want 1200x800（原横版）", fanartWidth, fanartHeight)
	}
	if callsContain(f.images.calls, psURL) {
		t.Error("竖版 ps 被下载了：应该只用横版那张")
	}

	// extrafanart/fanart{N}.jpg
	mustExist(t, filepath.Join(dir, organize.ExtraFanartDir, "fanart1.jpg"))
	mustExist(t, filepath.Join(dir, organize.ExtraFanartDir, "fanart2.jpg"))

	// tag 换装：in-progress → finished（失败不新增第三个 tag，这里只有这一次调用）。
	wantTag := hashA + ":mdc:in-progress→mdc:finished"
	if len(f.tags.calls) != 1 || f.tags.calls[0] != wantTag {
		t.Errorf("tag 调用 = %v, want [%s]", f.tags.calls, wantTag)
	}

	// 版本 finished + 分片账（cd1 是 primary）。
	version := f.version(t, movieID, hashA)
	if version.Status != task.StatusFinished {
		t.Errorf("status = %q, want finished", version.Status)
	}
	files, err := f.store.VersionFiles(context.Background(), hashA)
	if err != nil {
		t.Fatalf("VersionFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("version_file = %+v, want 两个分片", files)
	}
	if files[0].PartIndex != 1 || !files[0].IsPrimary || files[1].IsPrimary {
		t.Errorf("分片账 = %+v, want cd1 是 primary", files)
	}

	// 副作用入箱：jellyfin_refresh（target_key=movie_id）+ notify（filed:<infohash>）。
	if keys := f.sideEffectKeys(t, sidefx.KindJellyfinRefresh); len(keys) != 1 || keys[0] != movieID {
		t.Errorf("jellyfin_refresh = %v, want [%s]", keys, movieID)
	}
	if keys := f.sideEffectKeys(t, sidefx.KindNotify); len(keys) != 1 || keys[0] != "filed:"+hashA {
		t.Errorf("notify = %v, want [filed:%s]", keys, hashA)
	}

	payload := f.notifyPayload(t, "filed:"+hashA)
	// 译文是 AI 翻译那一段的产物（#30），这里没有译文 → 通知与 NFO 都降级成日文原文。
	if payload["title"] != "一ヶ月間の禁欲の果てに" {
		t.Errorf("通知标题 = %v, want 日文原文（译名缺席时降级）", payload["title"])
	}
	if payload["new_version"] == true {
		t.Error("首次入库不该带 new_version")
	}
	if size, _ := payload["size_mib"].(float64); size <= 0 {
		t.Errorf("size_mib = %v, want > 0", payload["size_mib"])
	}

	// 时间线：一条 processing→finished。
	found := false
	for _, event := range f.events(t, movieID) {
		if event.Kind == task.EventStateChange && event.FromState == "processing" && event.ToState == "finished" {
			found = true
		}
	}
	if !found {
		t.Error("时间线缺 processing→finished")
	}
}

// TestSecondVersionOnlyHardLinksItsVideo 钉住 P9-2 的复用判据：
// 元数据已存在 → 只硬链新视频，NFO/图片零改写零下载，通知是新版本文案。
func TestSecondVersionOnlyHardLinksItsVideo(t *testing.T) {
	f := newFixture(t)
	fetchedAt := time.Now().Add(-time.Hour).UTC()
	plURL := "https://pics.test/ssis001pl.jpg"
	extraURL := "https://pics.test/jp-1.jpg"
	f.images.bodies[plURL] = imageBytes(t, 1200, 800, 200, "jpeg")
	f.images.bodies[extraURL] = imageBytes(t, 1600, 900, 80, "jpeg")

	writeDownloads(t, f.downloads, map[string]string{"plain/ABP-123.mkv": "plain"})
	f.qbt.contentPath = filepath.Join(f.downloads, "plain")
	f.addProcessingVersion(t, movieID, number, hashA, "", metadata(fetchedAt, []string{plURL}, []string{extraURL}))
	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("第一轮 ProcessPending: %v", err)
	}

	dir := filepath.Join(f.mediaRoot, number)
	posterBefore := statMtime(t, filepath.Join(dir, "poster.jpg"))
	nfoBefore := readFile(t, filepath.Join(dir, organize.NFOName))

	// 第二条版本（中字）到来：同一任务、同一份元数据（复用），只多一个视频文件。
	f.images.calls = nil
	writeDownloads(t, f.downloads, map[string]string{"chinese/ABP-123.chs.mkv": "chinese"})
	f.qbt.contentPath = filepath.Join(f.downloads, "chinese")
	f.addProcessingVersion(t, movieID, number, hashB, "中文", metadata(fetchedAt, []string{plURL}, []string{extraURL}))
	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("第二轮 ProcessPending: %v", err)
	}

	// 多版本同目录：两个视频都在，各自硬链。
	for target, source := range map[string]string{
		"ABP-123.mkv":    "plain/ABP-123.mkv",
		"ABP-123-中文.mkv": "chinese/ABP-123.chs.mkv",
	} {
		mustExist(t, filepath.Join(dir, target))
		if !sameInode(t, filepath.Join(dir, target), filepath.Join(f.downloads, source)) {
			t.Errorf("%s 不是硬链", target)
		}
	}

	if len(f.images.calls) != 0 {
		t.Errorf("复用元数据却下载了图片: %v", f.images.calls)
	}
	if got := statMtime(t, filepath.Join(dir, "poster.jpg")); !got.Equal(posterBefore) {
		t.Errorf("poster 被重写了: %v → %v", posterBefore, got)
	}
	if got := readFile(t, filepath.Join(dir, organize.NFOName)); got != nfoBefore {
		t.Error("NFO 被重写了（内容相同就不该动）")
	}

	payload := f.notifyPayload(t, "filed:"+hashB)
	if payload["new_version"] != true {
		t.Error("任务重开再入库要带 new_version=true（新版本文案）")
	}
	if version := f.version(t, movieID, hashB); version.Status != task.StatusFinished {
		t.Errorf("第二条版本 status = %q, want finished", version.Status)
	}
}

// TestCollisionFailsTheLateVersion 钉住碰撞语义：目标被别的文件占着 → 后到者失败驻留，
// 绝不覆盖、绝不 -v2（规格 §7.2）。
func TestCollisionFailsTheLateVersion(t *testing.T) {
	f := newFixture(t)
	fetchedAt := time.Now().Add(-time.Hour).UTC()
	plURL := "https://pics.test/ssis001pl.jpg"
	f.images.bodies[plURL] = imageBytes(t, 1200, 800, 200, "jpeg")

	writeDownloads(t, f.downloads, map[string]string{"ABP-123.mkv": "new-version"})
	target := filepath.Join(f.mediaRoot, number, "ABP-123.mkv")
	writeDownloads(t, filepath.Dir(target), map[string]string{"ABP-123.mkv": "already-there"})
	f.addProcessingVersion(t, movieID, number, hashA, "", metadata(fetchedAt, []string{plURL}, nil))

	progress, err := f.runner.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if progress.Failed != 1 || progress.Filed != 0 {
		t.Fatalf("progress = %+v, want Failed=1", progress)
	}

	version := f.version(t, movieID, hashA)
	if version.Status != task.StatusFailed || version.ErrorPhase != task.PhaseOrganize {
		t.Fatalf("version = %+v, want failed(organize)", version)
	}
	if !strings.Contains(version.ErrorReason, "占着这个名字") {
		t.Errorf("reason = %q, want 碰撞说明", version.ErrorReason)
	}

	if got := readFile(t, target); got != "already-there" {
		t.Errorf("既有文件被覆盖了: %q", got)
	}
	if len(f.tags.calls) != 0 {
		t.Errorf("失败不该换 tag: %v", f.tags.calls)
	}
	if keys := f.sideEffectKeys(t, sidefx.KindJellyfinRefresh); len(keys) != 0 {
		t.Errorf("失败不该置刷新待办: %v", keys)
	}
	// 加工失败通知照发（给「需人工」区一个锚点）。
	if keys := f.sideEffectKeys(t, sidefx.KindNotify); len(keys) != 1 || keys[0] != "processing_failed:"+hashA {
		t.Errorf("notify = %v, want [processing_failed:%s]", keys, hashA)
	}
}

// TestSameNumberOtherTaskFailsTheLatecomer 钉住「同番号异 movie_id」：
// 先到者拥有目录，后到者失败驻留（绝不合并两份元数据）。
func TestSameNumberOtherTaskFailsTheLatecomer(t *testing.T) {
	f := newFixture(t)
	if _, err := f.database.Pool().Exec(context.Background(),
		`INSERT INTO task (movie_id, number, list_id, created_at) VALUES ('firstTask', $1, 'wanted', now() - interval '1 hour')`,
		number); err != nil {
		t.Fatalf("种先到任务: %v", err)
	}

	fetchedAt := time.Now().Add(-time.Hour).UTC()
	writeDownloads(t, f.downloads, map[string]string{"ABP-123.mkv": "video"})
	f.addProcessingVersion(t, movieID, number, hashA, "", metadata(fetchedAt, nil, nil))

	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}

	version := f.version(t, movieID, hashA)
	if version.Status != task.StatusFailed {
		t.Fatalf("status = %q, want failed", version.Status)
	}
	if !strings.Contains(version.ErrorReason, "firstTask") {
		t.Errorf("reason = %q, want 指出先到者", version.ErrorReason)
	}
	mustNotExist(t, filepath.Join(f.mediaRoot, number, "ABP-123.mkv"))
}

// TestTransientFailureBacksOffThenLandsFailed 钉住失败语义（P9-5）：
// 三次重试耗尽 → failed(phase, reason) + 通知一条；期间不换 tag。
//
// 种子在加工期被移除，账记在**下载**阶段（规格 §6.1「种子消失」），这样人工重试
// 才会按存的 payload 重新加种。
func TestTransientFailureBacksOffThenLandsFailed(t *testing.T) {
	f := newFixture(t)
	f.qbt.found = false // 种子被移除：定位不到下载文件

	fetchedAt := time.Now().Add(-time.Hour).UTC()
	f.addProcessingVersion(t, movieID, number, hashA, "", metadata(fetchedAt, nil, nil))

	for round := 1; round <= 3; round++ {
		progress, err := f.runner.ProcessPending(context.Background())
		if err != nil {
			t.Fatalf("第 %d 轮 ProcessPending: %v", round, err)
		}
		if progress.Failed != 1 {
			t.Fatalf("第 %d 轮 progress = %+v, want Failed=1", round, progress)
		}
		// 真实退避是分钟级：把下一次尝试推到过去，测试不必等。
		if _, err := f.database.Pool().Exec(context.Background(),
			`UPDATE task_version SET next_attempt_at = now() - interval '1 second' WHERE infohash = $1`,
			hashA); err != nil {
			t.Fatalf("推进退避: %v", err)
		}
	}

	version := f.version(t, movieID, hashA)
	if version.Status != task.StatusFailed || version.ErrorPhase != task.PhaseDownloading {
		t.Fatalf("version = %+v, want failed(downloading)", version)
	}
	if !strings.Contains(version.ErrorReason, "种子已从 qBittorrent 移除") {
		t.Errorf("reason = %q", version.ErrorReason)
	}
	if version.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", version.Attempts)
	}
	if len(f.tags.calls) != 0 {
		t.Errorf("失败不该换 tag: %v", f.tags.calls)
	}
	if keys := f.sideEffectKeys(t, sidefx.KindNotify); len(keys) != 1 || keys[0] != "processing_failed:"+hashA {
		t.Errorf("notify = %v, want 一条 processing_failed", keys)
	}
	payload := f.notifyPayload(t, "processing_failed:"+hashA)
	if payload["phase"] != "下载" || payload["reason"] == "" {
		t.Errorf("payload = %+v, want phase=下载 与原因", payload)
	}

	alerts := 0
	for _, event := range f.events(t, movieID) {
		if event.Kind == task.EventAlert {
			alerts++
		}
	}
	if alerts != 1 {
		t.Errorf("告警事件 = %d 条, want 1", alerts)
	}
}

// TestMissingVideoFilesFailsTheDownloadPhase 钉住「完成但文件缺」（规格 §6.1）：
// content_path 里一个视频都没有时，账记在下载阶段，理由是「文件丢失」。
func TestMissingVideoFilesFailsTheDownloadPhase(t *testing.T) {
	f := newFixture(t)
	writeDownloads(t, f.downloads, map[string]string{"readme.txt": "只有广告"})
	f.addProcessingVersion(t, movieID, number, hashA, "", metadata(time.Now().Add(-time.Hour).UTC(), nil, nil))

	for round := 1; round <= 3; round++ {
		if _, err := f.runner.ProcessPending(context.Background()); err != nil {
			t.Fatalf("第 %d 轮 ProcessPending: %v", round, err)
		}
		if _, err := f.database.Pool().Exec(context.Background(),
			`UPDATE task_version SET next_attempt_at = now() - interval '1 second' WHERE infohash = $1`,
			hashA); err != nil {
			t.Fatalf("推进退避: %v", err)
		}
	}

	version := f.version(t, movieID, hashA)
	if version.Status != task.StatusFailed || version.ErrorPhase != task.PhaseDownloading {
		t.Fatalf("version = %+v, want failed(downloading)", version)
	}
	if !strings.Contains(version.ErrorReason, "文件丢失") {
		t.Errorf("reason = %q, want 文件丢失", version.ErrorReason)
	}
}

// TestCrossDeviceFailsWithRemountAdvice 钉住 EXDEV：绝不降级 copy，提示换挂载。
func TestCrossDeviceFailsWithRemountAdvice(t *testing.T) {
	other, err := os.MkdirTemp("/dev/shm", "mdc-organize-")
	if err != nil {
		t.Skipf("/dev/shm 不可用（%v）：跳过跨设备验证", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })

	f := newFixture(t)
	f.settings.inputs["jellyfin"] = settings.TestInput{Values: map[string]any{"media_root": other}}
	fetchedAt := time.Now().Add(-time.Hour).UTC()
	writeDownloads(t, f.downloads, map[string]string{"ABP-123.mkv": "video"})
	f.addProcessingVersion(t, movieID, number, hashA, "", metadata(fetchedAt, nil, nil))

	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}

	version := f.version(t, movieID, hashA)
	if version.Status != task.StatusFailed {
		t.Fatalf("status = %q, want failed", version.Status)
	}
	if !strings.Contains(version.ErrorReason, "跨设备") || !strings.Contains(version.ErrorReason, "挂载") {
		t.Errorf("reason = %q, want 明示换挂载", version.ErrorReason)
	}
	// 绝不降级 copy：目标目录里只剩建出来的空目录，没有视频。
	if _, err := os.Stat(filepath.Join(other, number, "ABP-123.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Error("跨设备时竟然落了盘（降级 copy）")
	}
}

// TestCropFailureIsPartialSuccess 钉住「裁剪失败=部分成功」（mdc-lite ADR-0015）：
// 图下不下来/裁不裁得了都不失败任务，只留一条告警。
func TestCropFailureIsPartialSuccess(t *testing.T) {
	f := newFixture(t)
	fetchedAt := time.Now().Add(-time.Hour).UTC()
	brokenURL := "https://pics.test/brokenpl.jpg"
	f.images.bodies[brokenURL] = []byte("not an image")

	writeDownloads(t, f.downloads, map[string]string{"ABP-123.mkv": "video"})
	f.addProcessingVersion(t, movieID, number, hashA, "", metadata(fetchedAt, []string{brokenURL}, nil))

	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}

	version := f.version(t, movieID, hashA)
	if version.Status != task.StatusFinished {
		t.Fatalf("status = %q, want finished（图片失败不失败任务）", version.Status)
	}

	dir := filepath.Join(f.mediaRoot, number)
	mustNotExist(t, filepath.Join(dir, "poster.jpg"))
	// 源不横就没法「原样落 fanart」：宁缺勿歪（规格 §7.1 fanart 原横版）。
	mustNotExist(t, filepath.Join(dir, "fanart.jpg"))

	found := false
	for _, event := range f.events(t, movieID) {
		if event.Kind == task.EventAlert && strings.Contains(event.Reason, organize.ImageFailurePrefix) {
			found = true
		}
	}
	if !found {
		t.Error("裁剪失败没有留告警事件")
	}
}

// TestStaleExtraFanartIsCleanedOnShrink 钉住收缩清理：预览图变少时旧图不留在盘上。
func TestStaleExtraFanartIsCleanedOnShrink(t *testing.T) {
	f := newFixture(t)
	first := time.Now().Add(-2 * time.Hour).UTC()
	plURL := "https://pics.test/pl.jpg"
	f.images.bodies[plURL] = imageBytes(t, 1200, 800, 120, "jpeg")
	extras := []string{"https://pics.test/jp-1.jpg", "https://pics.test/jp-2.jpg", "https://pics.test/jp-3.jpg"}
	for _, url := range extras {
		f.images.bodies[url] = imageBytes(t, 1600, 900, 90, "jpeg")
	}

	writeDownloads(t, f.downloads, map[string]string{"plain/ABP-123.mkv": "plain"})
	f.qbt.contentPath = filepath.Join(f.downloads, "plain")
	f.addProcessingVersion(t, movieID, number, hashA, "", metadata(first, []string{plURL}, extras))
	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("第一轮 ProcessPending: %v", err)
	}
	for i := 1; i <= 3; i++ {
		mustExist(t, filepath.Join(f.mediaRoot, number, organize.ExtraFanartDir, "fanart"+strconv.Itoa(i)+".jpg"))
	}

	// 重刮（抓取时刻变新）后预览图只剩一张：旧的 fanart2/3 必须被清掉。
	second := time.Now().UTC()
	more := []string{"https://pics.test/jp-new.jpg"}
	f.images.bodies[more[0]] = imageBytes(t, 1600, 900, 30, "jpeg")
	// sleeve 换成 png 源：扩展名随源，旧扩展名的那张要清掉（不留僵尸图）。
	pngURL := "https://pics.test/pl.png"
	f.images.bodies[pngURL] = imageBytes(t, 1200, 800, 120, "png")
	writeDownloads(t, f.downloads, map[string]string{"chinese/ABP-123.chs.mkv": "chinese"})
	f.qbt.contentPath = filepath.Join(f.downloads, "chinese")
	f.addProcessingVersion(t, movieID, number, hashB, "中文", metadata(second, []string{pngURL}, more))
	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("第二轮 ProcessPending: %v", err)
	}

	extraDir := filepath.Join(f.mediaRoot, number, organize.ExtraFanartDir)
	mustExist(t, filepath.Join(extraDir, "fanart1.jpg"))
	mustNotExist(t, filepath.Join(extraDir, "fanart2.jpg"))
	mustNotExist(t, filepath.Join(extraDir, "fanart3.jpg"))

	dir := filepath.Join(f.mediaRoot, number)
	mustExist(t, filepath.Join(dir, "poster.png"))
	mustExist(t, filepath.Join(dir, "fanart.png"))
	mustNotExist(t, filepath.Join(dir, "poster.jpg"))
	mustNotExist(t, filepath.Join(dir, "fanart.jpg"))
}

// TestVerticalCoverFallbackStillWritesPoster 钉住 JavDB 封面兜底：
// 只有竖版封面时 poster 仍由算法裁成 2:3，但没有横版可原样当 fanart。
func TestVerticalCoverFallbackStillWritesPoster(t *testing.T) {
	f := newFixture(t)
	fetchedAt := time.Now().Add(-time.Hour).UTC()
	coverURL := "https://pics.test/javdb-cover.jpg"
	f.images.bodies[coverURL] = imageBytes(t, 900, 1200, 140, "jpeg")

	writeDownloads(t, f.downloads, map[string]string{"ABP-123.mkv": "video"})
	f.addProcessingVersion(t, movieID, number, hashA, "", metadata(fetchedAt, []string{coverURL}, nil))

	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}

	dir := filepath.Join(f.mediaRoot, number)
	mustExist(t, filepath.Join(dir, "poster.jpg"))
	mustNotExist(t, filepath.Join(dir, "fanart.jpg"))

	width, height := imageSize(t, filepath.Join(dir, "poster.jpg"))
	if width != 800 || height != 1200 {
		t.Errorf("poster = %dx%d, want 800x1200（2:3）", width, height)
	}
}

// TestTranslationMissingIsAnAlertNotABlocker 钉住 AI 翻译缺席的降级路径：
// title/plot 用日文原文照常入库，只在「翻译本该有」时留一条告警。
func TestTranslationMissingIsAnAlertNotABlocker(t *testing.T) {
	f := newFixture(t)
	fetchedAt := time.Now().Add(-time.Hour).UTC()
	enabled := true
	f.settings.snapshot.Sections["ai_translation"] = settings.SectionState{
		Configured: true, Enabled: &enabled, Data: map[string]any{"model": "gpt"},
	}

	untranslated := metadata(fetchedAt, nil, nil)
	untranslated.TitleZH = ""
	untranslated.OutlineZH = ""
	writeDownloads(t, f.downloads, map[string]string{"ABP-123.mkv": "video"})
	f.addProcessingVersion(t, movieID, number, hashA, "", untranslated)

	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}

	if version := f.version(t, movieID, hashA); version.Status != task.StatusFinished {
		t.Fatalf("status = %q, want finished（翻译缺席不阻塞入库）", version.Status)
	}
	nfo := readFile(t, filepath.Join(f.mediaRoot, number, organize.NFOName))
	if !strings.Contains(nfo, "<plot>S1スリム美女優の豪華共演</plot>") {
		t.Errorf("plot 没降级成日文原文:\n%s", nfo)
	}

	found := false
	for _, event := range f.events(t, movieID) {
		if event.Kind == task.EventAlert && strings.Contains(event.Reason, organize.TranslationMissingPrefix) {
			found = true
		}
	}
	if !found {
		t.Error("翻译缺席没有留告警事件")
	}
}

// TestWithoutMetadataNothingHappens 钉住两段的边界：元数据还没取齐的版本
// 归取数段，整理一段零动作（不抢租约、不碰文件）。
func TestWithoutMetadataNothingHappens(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.store.SaveCandidate(ctx, task.Candidate{
		MovieID: movieID, Number: number, ListID: "wanted", Infohash: hashA,
		Source: task.SourceJavDBMagnet, Delivery: task.DeliveryMagnet,
		Payload: "magnet:?xt=urn:btih:" + hashA,
	}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	if _, err := f.store.Claim(ctx, hashA, "owner-add"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := f.store.MarkAdded(ctx, hashA, "owner-add"); err != nil {
		t.Fatalf("MarkAdded: %v", err)
	}
	if _, err := f.store.Claim(ctx, hashA, "owner-scrape"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := f.store.MarkProcessing(ctx, hashA, "owner-scrape"); err != nil {
		t.Fatalf("MarkProcessing: %v", err)
	}

	progress, err := f.runner.ProcessPending(ctx)
	if err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if progress.Skipped != 1 || progress.Filed != 0 {
		t.Fatalf("progress = %+v, want Skipped=1（还没元数据）", progress)
	}
	if len(f.tags.calls) != 0 || f.qbt.calls != 0 {
		t.Errorf("零动作被打破了: tags=%v qbt=%d", f.tags.calls, f.qbt.calls)
	}
}

// ------------------------------------------------------------------ helpers

func (f *fixture) notifyPayload(t *testing.T, targetKey string) map[string]any {
	t.Helper()
	var raw []byte
	if err := f.database.Pool().QueryRow(context.Background(),
		`SELECT payload FROM side_effect WHERE kind = 'notify' AND target_key = $1`, targetKey,
	).Scan(&raw); err != nil {
		t.Fatalf("读通知 payload（%s）: %v", targetKey, err)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("解 payload: %v", err)
	}
	return payload
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func statMtime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.ModTime()
}

func imageSize(t *testing.T, path string) (int, int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func callsContain(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}
