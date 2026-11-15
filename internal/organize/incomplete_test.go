package organize_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/dmm"
	"github.com/2017fighting/oh-my-mdc/internal/organize"
	"github.com/2017fighting/oh-my-mdc/internal/scrape"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// #42 的入库面：图片下载的瞬时失败退避跨循环重试 5 轮，耗尽照常入库；
// 入库后按四面判据（无任何图 / 无演员 / 无简介 / 翻译启用却无中文标题）
// 标「待补齐」（task.missing_faces），四面补齐自动摘标。个别 tag 缺不算。

// missingFacesOf 是测试里读任务标注的单点：直接读列，不经读模型（读模型归面板票）。
func missingFacesOf(t *testing.T, f *fixture, movieID string) []string {
	t.Helper()
	var faces []string
	if err := f.database.Pool().QueryRow(context.Background(),
		`SELECT missing_faces FROM task WHERE movie_id = $1`, movieID,
	).Scan(&faces); err != nil {
		t.Fatalf("读 missing_faces: %v", err)
	}
	return faces
}

// expireBackoff 把退避推到过去：真实退避是分钟级，测试不等。
func expireBackoff(t *testing.T, f *fixture, infohash string) {
	t.Helper()
	if _, err := f.database.Pool().Exec(context.Background(),
		`UPDATE task_version SET next_attempt_at = now() - interval '1 second' WHERE infohash = $1`,
		infohash); err != nil {
		t.Fatalf("推进退避: %v", err)
	}
}

// seedActors 给任务种一位演员：四面判据的「无演员」看 task_actor 行数，
// 而 addProcessingVersion 落元数据时不带演员（SaveMetadata 会清表），所以
// 想要「演员齐全」的用例得在它之后补这一步。
func seedActors(t *testing.T, f *fixture, movieID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.database.Pool().Exec(ctx,
		`INSERT INTO actor (javdb_actor_id, name, gender) VALUES ('a1', '葵つかさ', 'female')
		 ON CONFLICT (javdb_actor_id) DO NOTHING`); err != nil {
		t.Fatalf("种演员: %v", err)
	}
	if _, err := f.database.Pool().Exec(ctx,
		`INSERT INTO task_actor (movie_id, javdb_actor_id, role) VALUES ($1, 'a1', 'actor')
		 ON CONFLICT DO NOTHING`, movieID); err != nil {
		t.Fatalf("种演员关联: %v", err)
	}
}

// TestMissingFacesFollowsTheFourCriteria 钉住四面判据（裁决原文）：
// 无任何图 / 无演员 / 无简介 / 翻译启用却无中文标题，任一命中即算；
// **个别 tag 缺不算**。判据是纯函数，整理段入库时调它落标注。
func TestMissingFacesFollowsTheFourCriteria(t *testing.T) {
	fetchedAt := time.Now().Add(-time.Hour).UTC()
	complete := metadata(fetchedAt, []string{"https://pics.test/pl.jpg"}, nil)

	// 全齐 + 图在盘上：一个面都不缺。
	full := t.TempDir()
	if err := os.WriteFile(filepath.Join(full, "poster.jpg"), []byte("jpeg"), 0o644); err != nil {
		t.Fatalf("写 poster: %v", err)
	}
	if faces := organize.MissingFaces(full, complete, 2, true); len(faces) != 0 {
		t.Errorf("全齐的任务缺 %v, want 空", faces)
	}

	dir := t.TempDir() // 什么图都没有
	bare := task.Metadata{}
	want := []string{task.FaceImages, task.FaceActors, task.FaceOutline, task.FaceTitleZH}
	if faces := organize.MissingFaces(dir, bare, 0, true); !equalFaces(faces, want) {
		t.Errorf("全空的缺 %v, want %v", faces, want)
	}

	// 翻译禁用时中文标题缺席不算缺（译文本该有才判）。
	if faces := organize.MissingFaces(dir, bare, 0, false); !equalFaces(faces,
		[]string{task.FaceImages, task.FaceActors, task.FaceOutline}) {
		t.Errorf("翻译禁用时的缺口 %v, want 不含 title_zh", faces)
	}

	// 个别 tag 缺不算：只有 tag 空不算待补齐（裁决原文「个别 tag 缺不算」）。
	tagless := complete
	tagless.Tags = nil
	if faces := organize.MissingFaces(dir, tagless, 2, true); !equalFaces(faces,
		[]string{task.FaceImages}) {
		t.Errorf("tag 空的缺口 %v, want 只有 images（图不在盘上）", faces)
	}
}

func equalFaces(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestImageDownloadRetriesThenFilesIncomplete 钉住图片下载的韧性面：
// poster/fanart 的下载是瞬时失败（网络拒）→ 退避跨循环重试 5 轮（与取数同一节奏），
// 耗尽那轮照常入库（部分成功告警在时间线），盘上无任何图 → 标「缺图」待补齐。
func TestImageDownloadRetriesThenFilesIncomplete(t *testing.T) {
	f := newFixture(t)
	fetchedAt := time.Now().Add(-time.Hour).UTC()
	plURL := "https://pics.test/abp123pl.jpg"
	f.images.errs[plURL] = errors.New("dial tcp: connection refused")
	writeDownloads(t, f.downloads, map[string]string{number + ".mkv": "video"})
	f.addProcessingVersion(t, movieID, number, hashA, "",
		metadata(fetchedAt, []string{plURL}, nil))
	seedActors(t, f, movieID)

	ctx := context.Background()
	for round := 1; round <= 4; round++ {
		progress, err := f.runner.ProcessPending(ctx)
		if err != nil {
			t.Fatalf("第 %d 轮 ProcessPending: %v", round, err)
		}
		if progress.Filed != 0 || progress.Failed != 1 {
			t.Fatalf("第 %d 轮 progress = %+v, want Failed=1 未入库", round, progress)
		}
		version := f.version(t, movieID, hashA)
		if version.Status != task.StatusProcessing || version.Attempts != round {
			t.Fatalf("第 %d 轮 version = %q/%d, want processing/%d",
				round, version.Status, version.Attempts, round)
		}
		expireBackoff(t, f, hashA)
	}

	// 第 5 轮：耗尽 → 照常入库，缺图标注待补齐。
	progress, err := f.runner.ProcessPending(ctx)
	if err != nil {
		t.Fatalf("耗尽轮 ProcessPending: %v", err)
	}
	if progress.Filed != 1 || progress.Failed != 0 {
		t.Fatalf("progress = %+v, want Filed=1（耗尽后照常入库）", progress)
	}
	version := f.version(t, movieID, hashA)
	if version.Status != task.StatusFinished {
		t.Errorf("version = %q, want finished", version.Status)
	}
	if faces := missingFacesOf(t, f, movieID); !equalFaces(faces, []string{task.FaceImages}) {
		t.Errorf("missing_faces = %v, want [images]", faces)
	}
	// 时间线上有部分成功告警（口径与 #29 一致：入库不被图片拖死）。
	found := false
	for _, event := range f.events(t, movieID) {
		if event.Kind == task.EventAlert && strings.HasPrefix(event.Reason, organize.ImageFailurePrefix) {
			found = true
		}
	}
	if !found {
		t.Error("时间线没有图片部分成功告警")
	}
}

// TestCompleteFilingLeavesNoMissingFaces 钉住标注的另一面：图下到了、演员在、
// 简介在 → 标注恒为空（一个面都不缺）。
func TestCompleteFilingLeavesNoMissingFaces(t *testing.T) {
	f := newFixture(t)
	fetchedAt := time.Now().Add(-time.Hour).UTC()
	plURL := "https://pics.test/abp123pl.jpg"
	f.images.bodies[plURL] = imageBytes(t, 1200, 800, 255, "jpeg")
	writeDownloads(t, f.downloads, map[string]string{number + ".mkv": "video"})
	f.addProcessingVersion(t, movieID, number, hashA, "",
		metadata(fetchedAt, []string{plURL}, nil))
	seedActors(t, f, movieID)

	if progress, err := f.runner.ProcessPending(context.Background()); err != nil || progress.Filed != 1 {
		t.Fatalf("progress = %+v err = %v, want Filed=1", progress, err)
	}
	if faces := missingFacesOf(t, f, movieID); len(faces) != 0 {
		t.Errorf("missing_faces = %v, want 空", faces)
	}
}

// TestForcedRescrapeClearsMissingFaces 钉住验收的闭环：断 DMM 降级入库的任务带着
// 待补齐标注，上游恢复后一键强刮（置 force_rescrape → 重取 → 重新入库）
// 四面补齐 → 标注自动摘掉。这就是「断网入库 → 恢复后摘标」的全链路。
func TestForcedRescrapeClearsMissingFaces(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// 取数段的设置面：JavDB token（organize 夹具没带这一节）。
	f.settings.inputs["javdb"] = settings.TestInput{Values: map[string]any{"token": "tk"}}

	// 第一幕：DMM 断（EOF），JavDB 活着但没有图与演员。五轮耗尽 → 按 JavDB 一面
	// 降级入库，标「缺图/缺演员」（简介走 §7.1 的回退，不算缺）。
	writeDownloads(t, f.downloads, map[string]string{number + ".mkv": "video"})
	javdbDown := &resiJavDB{detail: appapi.DetailResult{Movie: appapi.MovieDetail{
		ID: movieID, Number: number,
		Title:       "一個月禁慾的盡頭",
		OriginTitle: "一ヶ月間の禁欲の果てに",
		Summary:     "S1スリム美女優の豪華共演",
	}}}
	scraper := scrape.New(scrape.Options{
		Store:    f.store,
		Settings: f.settings,
		DMM:      &flakyDMM{err: errors.New("EOF")},
		JavDB:    javdbDown,
		Owner:    "test-scrape",
		Logger:   discardLogger(),
	})
	seedBareProcessingVersion(t, f, hashA)
	for round := 1; round <= 4; round++ {
		if _, err := scraper.ProcessPending(ctx); err != nil {
			t.Fatalf("第 %d 轮取数: %v", round, err)
		}
		expireBackoff(t, f, hashA)
	}
	if progress, err := scraper.ProcessPending(ctx); err != nil || progress.Degraded != 1 {
		t.Fatalf("第 5 轮 progress = %+v err = %v, want Degraded=1", progress, err)
	}
	if progress, err := f.runner.ProcessPending(ctx); err != nil || progress.Filed != 1 {
		t.Fatalf("降级入库: progress = %+v err = %v, want Filed=1", progress, err)
	}
	if faces := missingFacesOf(t, f, movieID); !equalFaces(faces, []string{task.FaceImages, task.FaceActors}) {
		t.Fatalf("降级后的缺口 = %v, want [images actors]", faces)
	}

	// 第二幕：一键强刮。retry 包置 force_rescrape 后版本回加工开头；
	// 这里直接打同一个库判据（retry.Runner 的行为归 #31 的测试管）。
	if _, err := f.store.Claim(ctx, hashA, "owner-force"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := f.store.ResetForRetry(ctx, hashA, "owner-force", true); err != nil {
		t.Fatalf("ResetForRetry: %v", err)
	}

	// 上游恢复：DMM 回来了（图与简介都在），JavDB 也给出了演员。
	cover := "https://pics.test/abp123pl.jpg"
	f.images.bodies[cover] = imageBytes(t, 1200, 800, 255, "jpeg")
	recovered := &flakyDMM{result: dmm.Result{Found: true, Content: dmm.Content{
		Title:           "一ヶ月間の禁欲の果てに",
		Description:     "S1スリム美女優の豪華共演",
		PackageLargeURL: cover,
	}}}
	recoveredJavDB := &resiJavDB{detail: appapi.DetailResult{Movie: appapi.MovieDetail{
		ID: movieID, Number: number,
		Title:       "一個月禁慾的盡頭",
		OriginTitle: "一ヶ月間の禁欲の果てに",
		Summary:     "S1スリム美女優の豪華共演",
		Actors: []appapi.Actor{
			{ID: "a1", Name: "葵つかさ", Gender: appapi.GenderFemale, AvatarURL: "https://img.test/a.jpg"},
		},
	}}}
	scraper = scrape.New(scrape.Options{
		Store:    f.store,
		Settings: f.settings,
		DMM:      recovered,
		JavDB:    recoveredJavDB,
		Owner:    "test-scrape",
		Logger:   discardLogger(),
	})
	if progress, err := scraper.ProcessPending(ctx); err != nil || progress.Fetched != 1 {
		t.Fatalf("重取: progress = %+v err = %v, want Fetched=1", progress, err)
	}
	if progress, err := f.runner.ProcessPending(ctx); err != nil || progress.Filed != 1 {
		t.Fatalf("重新入库: progress = %+v err = %v, want Filed=1", progress, err)
	}
	if faces := missingFacesOf(t, f, movieID); len(faces) != 0 {
		t.Errorf("强刮后缺口 = %v, want 空（四面补齐自动摘标）", faces)
	}
}

// ------------------------------------------------------------------ 取数假源

// flakyDMM 是可拨动的 DMM 假源：err 非空时失败，否则回 result。
type flakyDMM struct {
	result dmm.Result
	err    error
}

func (f *flakyDMM) Lookup(context.Context, string) (dmm.Result, error) {
	if f.err != nil {
		return dmm.Result{}, f.err
	}
	return f.result, nil
}

// resiJavDB 是可拨动的 JavDB 详情假源。
type resiJavDB struct {
	detail appapi.DetailResult
	err    error
}

func (j *resiJavDB) Detail(context.Context, string, string) (appapi.DetailResult, error) {
	if j.err != nil {
		return appapi.DetailResult{}, j.err
	}
	return j.detail, nil
}

var (
	_ scrape.DMM   = (*flakyDMM)(nil)
	_ scrape.JavDB = (*resiJavDB)(nil)
)
