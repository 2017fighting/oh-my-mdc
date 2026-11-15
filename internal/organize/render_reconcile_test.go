package organize_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/organize"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 渲染对账（GLOSSARY，#41）：定时用库内元数据重新渲染 movie.nfo，与盘上文件
// 逐槽位比对，漂移才重写。缝是 organize.Runner.ReconcileRenders——真 Postgres、
// 真文件系统，上游照旧用假（对账不打任何网络：渲染输入全部来自库内三面）。
//
// 两个验收钉在这里：
//   - 补译落库后一个对账周期内，title 变「番号 译名」（MIDV-850 病灶）；
//   - 对账空转（无漂移）零写盘：mtime 不变，不触发 Jellyfin 重新扫描。

// enableTranslation 把 AI 翻译配成「已配置且启用」（P6-3 的排版判据）。
func enableTranslation(f *fixture) {
	enabled := true
	f.settings.snapshot.Sections["ai_translation"] = settings.SectionState{
		Configured: true, Enabled: &enabled, Data: map[string]any{"model": "gpt"},
	}
}

// disableTranslation 把 AI 翻译关掉（保留库内已有译文——数据是数据）。
func disableTranslation(f *fixture) {
	delete(f.settings.snapshot.Sections, "ai_translation")
}

// fileOneVersion 走一遍真实入库：一条版本 processing → ProcessPending → finished，
// 盘上出现 {media_root}/{番号}/movie.nfo。返回目录路径。
func fileOneVersion(t *testing.T, f *fixture, meta task.Metadata) string {
	t.Helper()
	writeDownloads(t, f.downloads, map[string]string{"ABP-123.mkv": "video"})
	f.addProcessingVersion(t, movieID, number, hashA, "", meta)
	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if version := f.version(t, movieID, hashA); version.Status != task.StatusFinished {
		t.Fatalf("status = %q, want finished（对账的前置是已入库）", version.Status)
	}
	return filepath.Join(f.mediaRoot, number)
}

// TestRenderReconcileFlowsTranslationIntoMovieNFO 钉住验收一：
// 入库时还没有译名（盘上 title 是裸番号），补译落库后的下一个对账周期，
// movie.nfo 的 title/plot 就要带上中文——库里有了，屏幕上也要有。
func TestRenderReconcileFlowsTranslationIntoMovieNFO(t *testing.T) {
	f := newFixture(t)
	dir := fileOneVersion(t, f, metadata(time.Now().Add(-time.Hour).UTC(), nil, nil))

	// 病灶实证（MIDV-850）：入库时还没译名（盘上是日文原题），补译只更新 DB。
	nfoPath := filepath.Join(dir, organize.NFOName)
	filed := readFile(t, nfoPath)
	if !strings.Contains(filed, "<title>"+number+" 一ヶ月間の禁欲の果てに</title>") ||
		strings.Contains(filed, "一个月禁欲的尽头") {
		t.Fatalf("前置不成立：入库时 title 就该是日文原题\n%s", filed)
	}
	// 补译 + 翻译在库（翻译是后来才配好的，跟生产时序一致）。
	enableTranslation(f)
	if err := f.store.SaveTranslation(context.Background(), movieID,
		task.Translated{TitleZH: "一个月禁欲的尽头", OutlineZH: "S1 纤细女优豪华共演"}); err != nil {
		t.Fatalf("SaveTranslation: %v", err)
	}

	progress, err := f.runner.ReconcileRenders(context.Background())
	if err != nil {
		t.Fatalf("ReconcileRenders: %v", err)
	}
	if progress.Scanned != 1 || progress.Rewritten != 1 {
		t.Fatalf("progress = %+v, want Scanned=1 Rewritten=1（有漂移就该重写）", progress)
	}

	nfo := readFile(t, nfoPath)
	for _, want := range []string{
		"<title>" + number + " 一个月禁欲的尽头</title>",
		"<plot>S1 纤细女优豪华共演</plot>",
	} {
		if !strings.Contains(nfo, want) {
			t.Errorf("nfo 缺 %s\n---\n%s", want, nfo)
		}
	}
	// 其余槽位照旧：对账只回流漂移，不重排其它槽。
	for _, want := range []string{
		"<sorttitle>" + number + "</sorttitle>",
		"<originaltitle>" + number + " 一ヶ月間の禁欲の果てに</originaltitle>",
		"<premiered>2021-02-19</premiered>",
	} {
		if !strings.Contains(nfo, want) {
			t.Errorf("nfo 缺 %s\n---\n%s", want, nfo)
		}
	}

	// 回流收敛：再跑一轮就是空转，零写盘（验收二的另一半）。
	mtimeBefore := statMtime(t, nfoPath)
	progress, err = f.runner.ReconcileRenders(context.Background())
	if err != nil {
		t.Fatalf("第二轮 ReconcileRenders: %v", err)
	}
	if progress.Rewritten != 0 || progress.Unchanged != 1 {
		t.Errorf("第二轮 progress = %+v, want Unchanged=1（收敛后空转）", progress)
	}
	if got := statMtime(t, nfoPath); !got.Equal(mtimeBefore) {
		t.Errorf("空转轮重写了盘：mtime %v → %v", mtimeBefore, got)
	}
}

// TestRenderReconcileToleratesJellyfinRewrites 钉住验收二：
// Jellyfin 的 NFO writer 改写同一文件（lockdata/dateadded/playcount/watched/resume/
// fileinfo、演员头像本地化），槽位值一个没变 —— 这不算漂移，零写盘：mtime 不变，
// 不触发 Jellyfin 重新扫描。整文件比对会两边互覆永动，所以对账必须逐槽位。
func TestRenderReconcileToleratesJellyfinRewrites(t *testing.T) {
	f := newFixture(t)
	dir := fileOneVersion(t, f, metadata(time.Now().Add(-time.Hour).UTC(), nil, nil))
	nfoPath := filepath.Join(dir, organize.NFOName)

	// 先把译文补进库并回流一次：盘上的槽位值与库内元数据一致，才有「Jellyfin 只改格式」的前提。
	enableTranslation(f)
	if err := f.store.SaveTranslation(context.Background(), movieID,
		task.Translated{TitleZH: "一个月禁欲的尽头", OutlineZH: "S1 纤细女优豪华共演"}); err != nil {
		t.Fatalf("SaveTranslation: %v", err)
	}
	if progress, err := f.runner.ReconcileRenders(context.Background()); err != nil || progress.Rewritten != 1 {
		t.Fatalf("前置回流失败: progress = %+v, err = %v", progress, err)
	}

	// Jellyfin 改写后的样子（生产取证，2026-11-15 全库被批量重写）：槽位值原样，
	// 头像换本地路径，追加私有段。
	jellyfin := `<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<movie>
  <title>` + number + ` 一个月禁欲的尽头</title>
  <originaltitle>` + number + ` 一ヶ月間の禁欲の果てに</originaltitle>
  <sorttitle>` + number + `</sorttitle>
  <plot>S1 纤细女优豪华共演</plot>
  <runtime>150</runtime>
  <rating>8.86</rating>
  <premiered>2021-02-19</premiered>
  <releasedate>2021-02-19</releasedate>
  <studio>S1 NO.1 STYLE</studio>
  <director>苺原</director>
  <tag>戏剧</tag>
  <tag>美乳</tag>
  <tag>发行: S1</tag>
  <set>一ヶ月間の禁欲</set>
  <actor>
    <name>女优乙</name>
    <thumb>cached://metadata/library/ab/abp-123/f1.jpg</thumb>
  </actor>
  <actor>
    <name>男优甲</name>
    <thumb>cached://metadata/library/ab/abp-123/m1.jpg</thumb>
  </actor>
  <actor>
    <name>演员丙</name>
  </actor>
  <lockdata>true</lockdata>
  <dateadded>2026-11-15T14:28:00.0000000Z</dateadded>
  <playcount>3</playcount>
  <watched>true</watched>
  <resume>
    <position>60</position>
    <total>150</total>
  </resume>
  <fileinfo>
    <streamdetails>
      <video>
        <codec>h264</codec>
        <width>1920</width>
        <height>1080</height>
      </video>
      <audio>
        <codec>aac</codec>
      </audio>
    </streamdetails>
  </fileinfo>
</movie>
`
	if err := os.WriteFile(nfoPath, []byte(jellyfin), 0o644); err != nil {
		t.Fatalf("写 Jellyfin 版 movie.nfo: %v", err)
	}
	mtimeBefore := statMtime(t, nfoPath)

	progress, err := f.runner.ReconcileRenders(context.Background())
	if err != nil {
		t.Fatalf("ReconcileRenders: %v", err)
	}
	if progress.Rewritten != 0 || progress.Unchanged != 1 {
		t.Fatalf("progress = %+v, want Unchanged=1（槽位值没变就不写）", progress)
	}
	if got := statMtime(t, nfoPath); !got.Equal(mtimeBefore) {
		t.Errorf("空转却动了盘：mtime %v → %v", mtimeBefore, got)
	}
	if got := readFile(t, nfoPath); got != jellyfin {
		t.Error("空转却改了内容：Jellyfin 的私有段不该被抹掉")
	}
}

// TestRenderReconcileFollowsTheTranslationSwitch 钉住 P6-3 的对账面：
// 渲染对账与整理一段用同一条排版规则——翻译禁用时译文不参与排版（哪怕库里有）。
// 关掉开关后对账把 NFO 收敛回日文原文（否则下一次整理会再改回去，两边打架）；
// 再跑一轮收敛为空转。
func TestRenderReconcileFollowsTheTranslationSwitch(t *testing.T) {
	f := newFixture(t)
	dir := fileOneVersion(t, f, metadata(time.Now().Add(-time.Hour).UTC(), nil, nil))
	nfoPath := filepath.Join(dir, organize.NFOName)

	// 先补译回流一次：盘上是中文。
	enableTranslation(f)
	if err := f.store.SaveTranslation(context.Background(), movieID,
		task.Translated{TitleZH: "一个月禁欲的尽头", OutlineZH: "S1 纤细女优豪华共演"}); err != nil {
		t.Fatalf("SaveTranslation: %v", err)
	}
	if progress, err := f.runner.ReconcileRenders(context.Background()); err != nil || progress.Rewritten != 1 {
		t.Fatalf("前置回流失败: progress = %+v, err = %v", progress, err)
	}
	if got := readFile(t, nfoPath); !strings.Contains(got, "一个月禁欲的尽头") {
		t.Fatalf("前置不成立：回流后盘上没落译文\n%s", got)
	}

	disableTranslation(f)
	progress, err := f.runner.ReconcileRenders(context.Background())
	if err != nil {
		t.Fatalf("ReconcileRenders: %v", err)
	}
	if progress.Rewritten != 1 {
		t.Fatalf("progress = %+v, want Rewritten=1（排版规则变了就该重写）", progress)
	}
	nfo := readFile(t, nfoPath)
	if strings.Contains(nfo, "一个月禁欲的尽头") {
		t.Errorf("翻译禁用却保留了译文:\n%s", nfo)
	}
	if !strings.Contains(nfo, "<title>"+number+" 一ヶ月間の禁欲の果てに</title>") {
		t.Errorf("title 没退回日文原文:\n%s", nfo)
	}

	mtimeBefore := statMtime(t, nfoPath)
	progress, err = f.runner.ReconcileRenders(context.Background())
	if err != nil {
		t.Fatalf("第二轮 ReconcileRenders: %v", err)
	}
	if progress.Rewritten != 0 || progress.Unchanged != 1 {
		t.Errorf("第二轮 progress = %+v, want Unchanged=1", progress)
	}
	if got := statMtime(t, nfoPath); !got.Equal(mtimeBefore) {
		t.Errorf("空转轮重写了盘：mtime %v → %v", mtimeBefore, got)
	}
}

// TestRenderReconcileDoesNotResurrectDeletedNFO 钉住「缺文件不重建」：
// movie.nfo 被删（或媒体库目录挂载抖动变空）时对账零动作——它是漂移的兜底回流，
// 不是文件恢复；挂载抖动时批量重建只会喂给 Jellyfin 一堆假目录。重建归补录（#43）。
func TestRenderReconcileDoesNotResurrectDeletedNFO(t *testing.T) {
	f := newFixture(t)
	dir := fileOneVersion(t, f, metadata(time.Now().Add(-time.Hour).UTC(), nil, nil))
	nfoPath := filepath.Join(dir, organize.NFOName)
	if err := os.Remove(nfoPath); err != nil {
		t.Fatalf("删 movie.nfo: %v", err)
	}

	progress, err := f.runner.ReconcileRenders(context.Background())
	if err != nil {
		t.Fatalf("ReconcileRenders: %v", err)
	}
	if progress.Rewritten != 0 || progress.Skipped != 1 {
		t.Fatalf("progress = %+v, want Skipped=1（缺文件不算漂移）", progress)
	}
	if _, err := os.Stat(nfoPath); !os.IsNotExist(err) {
		t.Error("被删的 movie.nfo 被重建了")
	}
}

// TestRenderReconcileLeavesForeignDirectoriesAlone 钉住目录归属：
// 同番号的任务已由先到者拥有（规格 §7.2 碰撞行）时，对账不碰那个目录——
// 否则两边元数据不同就会互相重写。等人工的碰撞不需要对账再掺一脚。
func TestRenderReconcileLeavesForeignDirectoriesAlone(t *testing.T) {
	f := newFixture(t)
	// 先到者：更早创建的同番号任务（没有版本也拥有番号，见 TaskOwningNumber）。
	if _, err := f.database.Pool().Exec(context.Background(),
		`INSERT INTO task (movie_id, number, list_id, created_at)
		 VALUES ('firstTask', $1, 'wanted', now() - interval '1 hour')`, number); err != nil {
		t.Fatalf("种先到任务: %v", err)
	}
	// 我的任务直接落 finished + 一个漂移中的 movie.nfo（不经整理段：整理段会让它输）。
	fetchedAt := time.Now().Add(-time.Hour).UTC()
	f.addProcessingVersion(t, movieID, number, hashA, "", metadata(fetchedAt, nil, nil))
	if _, err := f.database.Pool().Exec(context.Background(),
		`UPDATE task_version SET status = 'finished' WHERE infohash = $1`, hashA); err != nil {
		t.Fatalf("置 finished: %v", err)
	}
	dir := filepath.Join(f.mediaRoot, number)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	nfoPath := filepath.Join(dir, organize.NFOName)
	foreign := "<movie><title>别人家的</title></movie>"
	if err := os.WriteFile(nfoPath, []byte(foreign), 0o644); err != nil {
		t.Fatalf("写 NFO: %v", err)
	}

	progress, err := f.runner.ReconcileRenders(context.Background())
	if err != nil {
		t.Fatalf("ReconcileRenders: %v", err)
	}
	if progress.Rewritten != 0 || progress.Skipped != 1 {
		t.Fatalf("progress = %+v, want Skipped=1（目录归别的任务）", progress)
	}
	if got := readFile(t, nfoPath); got != foreign {
		t.Error("别人的目录被重写了")
	}
}

// ---------------------------------------------------------------- 缺面扫场（#44）

// taskUpdatedAt 读任务的 updated_at：零写库断言用（SetMissingFaces 会更新它，
// 没写就不动——比自报计数更硬的证据）。
func taskUpdatedAt(t *testing.T, f *fixture, movieID string) time.Time {
	t.Helper()
	var at time.Time
	if err := f.database.Pool().QueryRow(context.Background(),
		`SELECT updated_at FROM task WHERE movie_id = $1`, movieID,
	).Scan(&at); err != nil {
		t.Fatalf("读 updated_at: %v", err)
	}
	return at
}

// clearMissingFaces 把标注抹成空集：模拟 0.3.0 前入库的存量任务——那时入库路径
// 还没有缺面评估，missing_faces 全空，「待补齐」区对旧伤失明（#44 的病灶）。
func clearMissingFaces(t *testing.T, f *fixture, movieID string) {
	t.Helper()
	if _, err := f.database.Pool().Exec(context.Background(),
		`UPDATE task SET missing_faces = '{}' WHERE movie_id = $1`, movieID); err != nil {
		t.Fatalf("抹标注: %v", err)
	}
}

// TestRenderReconcileAssessesMissingFacesForLegacyTasks 钉住 #44 的主验收：
// 存量任务（标注全空）盘上无图、库里无演员，渲染对账扫场用同一份四面判据代评，
// 把缺面落进 task.missing_faces——待补齐区对旧伤不再失明（SVMGM-032 型）。
func TestRenderReconcileAssessesMissingFacesForLegacyTasks(t *testing.T) {
	f := newFixture(t)
	fileOneVersion(t, f, metadata(time.Now().Add(-time.Hour).UTC(), nil, nil))
	clearMissingFaces(t, f, movieID)

	progress, err := f.runner.ReconcileRenders(context.Background())
	if err != nil {
		t.Fatalf("ReconcileRenders: %v", err)
	}
	if progress.FacesUpdated != 1 {
		t.Fatalf("progress = %+v, want FacesUpdated=1（缺图缺演员，该落标注）", progress)
	}
	if faces := missingFacesOf(t, f, movieID); !equalFaces(faces, []string{task.FaceImages, task.FaceActors}) {
		t.Errorf("missing_faces = %v, want [images actors]（默认夹具翻译禁用，title_zh 不判）", faces)
	}
}

// TestRenderReconcileExemptsActorFaceForBackfillTasks 钉住补录豁免（规格 §6.3）：
// 扫场代评与入库处同一条规则——补录任务（全部版本 source=backfill）的演员面是
// 结构性永缺，不计入待补齐；其余面照评照写。
func TestRenderReconcileExemptsActorFaceForBackfillTasks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fetchedAt := time.Now().Add(-time.Hour).UTC()

	dir := filepath.Join(f.mediaRoot, backfillNumber)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SONE-682.mkv"), []byte("video-main"), 0o644); err != nil {
		t.Fatalf("写视频: %v", err)
	}
	hash := task.BackfillInfohash(backfillNumber, "SONE-682.mkv")
	seedBackfillVersion(t, f, backfillNumber, hash, "SONE-682.mkv", 111)
	// 取到过元数据：简介在、无图、无演员（补录结构性取不到）。翻译禁用 → title_zh 不判。
	if err := f.store.SaveMetadata(ctx, backfillNumber,
		task.Lease{Infohash: hash, Owner: "scrape"}, metadata(fetchedAt, nil, nil), nil,
	); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}
	if progress, err := f.runner.ProcessPending(ctx); err != nil || progress.Filed != 1 {
		t.Fatalf("progress = %+v err = %v, want Filed=1", progress, err)
	}
	clearMissingFaces(t, f, backfillNumber)

	progress, err := f.runner.ReconcileRenders(ctx)
	if err != nil {
		t.Fatalf("ReconcileRenders: %v", err)
	}
	if progress.FacesUpdated != 1 {
		t.Fatalf("progress = %+v, want FacesUpdated=1（缺图该落标注）", progress)
	}
	if faces := missingFacesOf(t, f, backfillNumber); !equalFaces(faces, []string{task.FaceImages}) {
		t.Errorf("missing_faces = %v, want [images]（补录豁免演员面）", faces)
	}
}

// TestRenderReconcileClearsStaleFacesWhenEverythingIsComplete 钉住摘标方向：
// 四面事实都齐（图在盘、演员在库、简介与译文在列）而标注还是存量坏值时，
// 扫场用同一份判据把空集写回去——代评不只「落标」，也包括「摘标」。
func TestRenderReconcileClearsStaleFacesWhenEverythingIsComplete(t *testing.T) {
	f := newFixture(t)
	enableTranslation(f)
	fetchedAt := time.Now().Add(-time.Hour).UTC()
	plURL := "https://pics.test/abp123pl.jpg"
	f.images.bodies[plURL] = imageBytes(t, 1200, 800, 255, "jpeg")
	writeDownloads(t, f.downloads, map[string]string{number + ".mkv": "video"})
	f.addProcessingVersion(t, movieID, number, hashA, "",
		metadata(fetchedAt, []string{plURL}, nil))
	// 译文是取数后的独立一步（SaveMetadata 不碰 zh 两列）。
	if err := f.store.SaveTranslation(context.Background(), movieID,
		task.Translated{TitleZH: "一个月禁欲的尽头", OutlineZH: "S1 纤细女优豪华共演"}); err != nil {
		t.Fatalf("SaveTranslation: %v", err)
	}
	seedActors(t, f, movieID)
	if progress, err := f.runner.ProcessPending(context.Background()); err != nil || progress.Filed != 1 {
		t.Fatalf("progress = %+v err = %v, want Filed=1", progress, err)
	}
	// 存量坏标注：明明四面齐，老数据却带着缺口。
	if _, err := f.database.Pool().Exec(context.Background(),
		`UPDATE task SET missing_faces = '{images,actors}' WHERE movie_id = $1`, movieID); err != nil {
		t.Fatalf("种坏标注: %v", err)
	}

	progress, err := f.runner.ReconcileRenders(context.Background())
	if err != nil {
		t.Fatalf("ReconcileRenders: %v", err)
	}
	if progress.FacesUpdated != 1 {
		t.Fatalf("progress = %+v, want FacesUpdated=1（标注与事实不符，该摘标）", progress)
	}
	if faces := missingFacesOf(t, f, movieID); len(faces) != 0 {
		t.Errorf("missing_faces = %v, want 空（四面齐写空集）", faces)
	}
}

// TestRenderReconcileLeavesUnchangedFacesUnwritten 钉住空转面：标注已是判据算出的
// 定态时零写库——updated_at 不动，不制造假动静（与渲染回流「空转零写盘」同一纪律）。
func TestRenderReconcileLeavesUnchangedFacesUnwritten(t *testing.T) {
	f := newFixture(t)
	fileOneVersion(t, f, metadata(time.Now().Add(-time.Hour).UTC(), nil, nil))
	// 入库已标 [images actors]，与扫场将算出的一致。
	before := taskUpdatedAt(t, f, movieID)

	progress, err := f.runner.ReconcileRenders(context.Background())
	if err != nil {
		t.Fatalf("ReconcileRenders: %v", err)
	}
	if progress.FacesUpdated != 0 || progress.Unchanged != 1 {
		t.Fatalf("progress = %+v, want FacesUpdated=0 Unchanged=1（定态零写库）", progress)
	}
	if after := taskUpdatedAt(t, f, movieID); !after.Equal(before) {
		t.Errorf("无变化却写了库：updated_at %v → %v", before, after)
	}
}

// TestRenderReconcileGatesLikeTheLoops 钉住门禁：未初始化/已暂停时对账零动作，
// 与三条循环同一句判词（它跑在对账循环里，门禁不该另起一套）。
func TestRenderReconcileGatesLikeTheLoops(t *testing.T) {
	f := newFixture(t)
	f.settings.snapshot = settings.Snapshot{Paused: true, Sections: map[string]settings.SectionState{}}

	progress, err := f.runner.ReconcileRenders(context.Background())
	if err != nil {
		t.Fatalf("ReconcileRenders: %v", err)
	}
	if !progress.Gated || progress.Reason == "" {
		t.Fatalf("progress = %+v, want Gated 带判词", progress)
	}
}
