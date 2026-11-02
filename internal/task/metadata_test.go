package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：task.Store 的元数据三面与加工租约（规格 §3/§4.1/§6.1）。
// 断言只走公开方法：Metadata/SaveMetadata/Actors/ProcessingVersions/ScheduleRetry。

func metadataFixture(t *testing.T) (*task.Store, *db.DB) {
	t.Helper()
	return migrated(t)
}

// processingVersion 种一条已经加种、已被推进到 processing 的版本。
func processingVersion(t *testing.T, store *task.Store, movieID, number, infohash string) {
	t.Helper()
	save(t, store, candidate(movieID, number, infohash))
	if _, err := store.Claim(context.Background(), infohash, "owner-add"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.MarkAdded(context.Background(), infohash, "owner-add"); err != nil {
		t.Fatalf("MarkAdded: %v", err)
	}
	if _, err := store.Claim(context.Background(), infohash, "owner-scrape"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.MarkProcessing(context.Background(), infohash, "owner-scrape"); err != nil {
		t.Fatalf("MarkProcessing: %v", err)
	}
}

func sampleMetadata(t *testing.T) task.Metadata {
	t.Helper()
	release := time.Date(2021, 2, 19, 0, 0, 0, 0, time.UTC)
	runtime := 150
	rating := 8.86
	return task.Metadata{
		TitleJA:        "一ヶ月間の禁欲の果てに",
		Outline:        "S1スリム美女優の豪華共演",
		ReleaseDate:    &release,
		RuntimeMinutes: &runtime,
		Studio:         "S1 NO.1 STYLE",
		Series:         "一ヶ月間の禁欲",
		Director:       "苺原",
		Rating:         &rating,
		Tags:           []string{"戏剧", "美乳"},
		Provenance:     json.RawMessage(`{"title_ja":{"source":"dmm"}}`),
		RawSnapshot:    json.RawMessage(`{"javdb":{"movie":{"id":"ZY5eq"}}}`),
	}
}

func sampleActors() []task.Actor {
	return []task.Actor{
		{JavDBID: "A5yq", Name: "葵つかさ", Gender: "female", AvatarURL: "https://img/a5.jpg"},
		{JavDBID: "8VNb9", Name: "平田司", Gender: "male", AvatarURL: "https://img/8v.jpg"},
		{JavDBID: "unknown", Name: "无性别", Gender: "", AvatarURL: ""},
	}
}

// TestMetadataIsAbsentBeforeTheFirstFetch 钉住复用判据的否定面：
// 元数据不存在 → 需要刮削。判据是 provenance，不是名称或标签。
func TestMetadataIsAbsentBeforeTheFirstFetch(t *testing.T) {
	store, _ := metadataFixture(t)
	save(t, store, candidate("YwrO3B", "ABP-123", "1111111111111111111111111111111111111111"))

	metadata, exists, err := store.Metadata(context.Background(), "YwrO3B")
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if exists {
		t.Fatal("还没刮过就报告元数据存在")
	}
	if metadata.TitleJA != "" {
		t.Errorf("空任务的标题 = %q, want 空", metadata.TitleJA)
	}
	if has, err := store.HasMetadata(context.Background(), "YwrO3B"); err != nil || has {
		t.Errorf("HasMetadata = %v err=%v, want false/nil", has, err)
	}
}

// TestSaveMetadataWritesEveryColumnAndTheJudgement 是元数据三面的落库：
// 强类型列、provenance、raw_snapshot 都到位，且复用判据立刻成立。
func TestSaveMetadataWritesEveryColumnAndTheJudgement(t *testing.T) {
	store, _ := metadataFixture(t)
	infohash := "1111111111111111111111111111111111111111"
	processingVersion(t, store, "YwrO3B", "ABP-123", infohash)
	if _, err := store.Claim(context.Background(), infohash, "owner-scrape"); err != nil {
		t.Fatalf("Claim（加工）: %v", err)
	}

	lease := task.Lease{Infohash: infohash, Owner: "owner-scrape"}
	if err := store.SaveMetadata(context.Background(), "YwrO3B", lease, sampleMetadata(t), sampleActors()); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}
	// 租约真的放回了：别人（整理票）能立刻接上同一条版本。
	if _, err := store.Claim(context.Background(), infohash, "owner-organize"); err != nil {
		t.Errorf("成功后租约没放回: %v", err)
	}

	metadata, exists, err := store.Metadata(context.Background(), "YwrO3B")
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if !exists {
		t.Fatal("存过元数据后复用判据仍不成立")
	}
	if metadata.TitleJA != "一ヶ月間の禁欲の果てに" || metadata.Outline != "S1スリム美女優の豪華共演" {
		t.Errorf("title/outline = %q/%q", metadata.TitleJA, metadata.Outline)
	}
	if metadata.ReleaseDate == nil || metadata.ReleaseDate.Format("2006-01-02") != "2021-02-19" {
		t.Errorf("release_date = %v, want 2021-02-19", metadata.ReleaseDate)
	}
	if metadata.RuntimeMinutes == nil || *metadata.RuntimeMinutes != 150 {
		t.Errorf("runtime = %v, want 150", metadata.RuntimeMinutes)
	}
	if metadata.Studio != "S1 NO.1 STYLE" || metadata.Series != "一ヶ月間の禁欲" || metadata.Director != "苺原" {
		t.Errorf("studio/series/director = %q/%q/%q", metadata.Studio, metadata.Series, metadata.Director)
	}
	if metadata.Rating == nil || *metadata.Rating != 8.86 {
		t.Errorf("rating = %v, want 8.86（10 分制）", metadata.Rating)
	}
	if len(metadata.Tags) != 2 || metadata.Tags[0] != "戏剧" {
		t.Errorf("tags = %v", metadata.Tags)
	}
	if string(metadata.Provenance) != `{"title_ja": {"source": "dmm"}}` {
		t.Errorf("provenance = %s（jsonb 会归一化空白）", metadata.Provenance)
	}
	if string(metadata.RawSnapshot) != `{"javdb": {"movie": {"id": "ZY5eq"}}}` {
		t.Errorf("raw_snapshot = %s", metadata.RawSnapshot)
	}

	// 加工租约在成功后放回：整理票（#29）要在同一条版本上接着跑。
	version := versionOf(detail(t, store, "YwrO3B"), infohash)
	if version.Status != task.StatusProcessing {
		t.Errorf("status = %q, want processing（取数完成后驻 processing 等整理票）", version.Status)
	}
	if version.Attempts != 0 {
		t.Errorf("attempts = %d, want 0（成功不留重试计数）", version.Attempts)
	}
}

// TestSaveMetadataKeepsTranslationColumns 确认取数不碰 AI 翻译列（#30 的活）：
// 重刮不该把已译好的中文标题抹掉。
func TestSaveMetadataKeepsTranslationColumns(t *testing.T) {
	store, database := metadataFixture(t)
	infohash := "1111111111111111111111111111111111111111"
	processingVersion(t, store, "YwrO3B", "ABP-123", infohash)

	if _, err := database.Pool().Exec(context.Background(),
		`UPDATE task SET title_zh = '一个月禁欲的尽头', outline_zh = '译文' WHERE movie_id = 'YwrO3B'`,
	); err != nil {
		t.Fatalf("种译文: %v", err)
	}
	if err := store.SaveMetadata(context.Background(), "YwrO3B", task.Lease{}, sampleMetadata(t), sampleActors()); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	metadata, _, err := store.Metadata(context.Background(), "YwrO3B")
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if metadata.TitleZH != "一个月禁欲的尽头" || metadata.OutlineZH != "译文" {
		t.Errorf("title_zh/outline_zh = %q/%q，want 原样保留", metadata.TitleZH, metadata.OutlineZH)
	}
}

// TestSaveMetadataUpsertsActorsAndTaskActor 是演员落行（P15-1）：
// actor 表按 javdb_actor_id upsert（含性别与头像），task_actor 全量换一份。
func TestSaveMetadataUpsertsActorsAndTaskActor(t *testing.T) {
	store, _ := metadataFixture(t)
	one := "1111111111111111111111111111111111111111"
	two := "2222222222222222222222222222222222222222"
	processingVersion(t, store, "YwrO3B", "ABP-123", one)
	processingVersion(t, store, "Zx9K1p", "ABP-124", two)

	actors := sampleActors()
	if err := store.SaveMetadata(context.Background(), "YwrO3B", task.Lease{}, sampleMetadata(t), actors); err != nil {
		t.Fatalf("SaveMetadata(一): %v", err)
	}
	// 第二部片共用同一个演员 + 只留一位：task_actor 必须是全量换，不是追加。
	if err := store.SaveMetadata(context.Background(), "Zx9K1p", task.Lease{}, sampleMetadata(t), actors[:1]); err != nil {
		t.Fatalf("SaveMetadata(二): %v", err)
	}

	first, err := store.Actors(context.Background(), "YwrO3B")
	if err != nil {
		t.Fatalf("Actors: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("第一部片的演员 = %d, want 3", len(first))
	}
	byID := map[string]task.Actor{}
	for _, actor := range first {
		byID[actor.JavDBID] = actor
	}
	if female := byID["A5yq"]; female.Gender != "female" || female.AvatarURL == "" || female.Name != "葵つかさ" {
		t.Errorf("演员 A5yq = %+v", female)
	}
	if male := byID["8VNb9"]; male.Gender != "male" {
		t.Errorf("演员 8VNb9 gender = %q, want male（性别必须落行）", male.Gender)
	}
	if unknown := byID["unknown"]; unknown.Gender != "" {
		t.Errorf("无性别演员 gender = %q, want 空（不知道就留空，不猜）", unknown.Gender)
	}

	second, err := store.Actors(context.Background(), "Zx9K1p")
	if err != nil {
		t.Fatalf("Actors: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("第二部片的演员 = %d, want 1（全量换一份，不追加）", len(second))
	}
}

// TestSaveMetadataRequiresTheTask 确认没有这条任务时明确报错（不是静默零写入）。
func TestSaveMetadataRequiresTheTask(t *testing.T) {
	store, _ := metadataFixture(t)
	err := store.SaveMetadata(context.Background(), "nope", task.Lease{}, sampleMetadata(t), nil)
	if !errors.Is(err, task.ErrTaskNotFound) {
		t.Fatalf("err = %v, want ErrTaskNotFound", err)
	}
}

// TestRecordProcessingFailureBacksOffAndReleasesTheLease 是取数失败的重试记账：
// attempts 递增、next_attempt_at 往后推、租约放回；状态仍是 processing。
func TestRecordProcessingFailureBacksOffAndReleasesTheLease(t *testing.T) {
	store, _ := metadataFixture(t)
	infohash := "1111111111111111111111111111111111111111"
	processingVersion(t, store, "YwrO3B", "ABP-123", infohash)
	if _, err := store.Claim(context.Background(), infohash, "owner-scrape"); err != nil {
		t.Fatalf("Claim（加工）: %v", err)
	}

	at := time.Now().UTC()
	failure, err := store.RecordProcessingFailure(context.Background(), infohash, "owner-scrape",
		task.PhaseScrape, "DMM 地区阻断", at, 5*time.Minute, 3)
	if err != nil {
		t.Fatalf("RecordProcessingFailure: %v", err)
	}
	if failure.Attempts != 1 || failure.Exhausted {
		t.Errorf("failure = %+v, want attempts=1 且未耗尽", failure)
	}

	version := versionOf(detail(t, store, "YwrO3B"), infohash)
	if version.Status != task.StatusProcessing {
		t.Errorf("status = %q, want processing（失败不换状态）", version.Status)
	}
	if version.Attempts != 1 {
		t.Errorf("落库 attempts = %d, want 1", version.Attempts)
	}

	// 退避期内这条版本不该再被捞起来。
	versions, err := store.ProcessingVersions(context.Background())
	if err != nil {
		t.Fatalf("ProcessingVersions: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("退避期内仍被列出 %d 条", len(versions))
	}

	// 抢输不是失败：别人的租约不归我写。
	if _, err := store.RecordProcessingFailure(context.Background(), infohash, "someone-else",
		task.PhaseScrape, "x", at, time.Minute, 3); !errors.Is(err, task.ErrClaimLost) {
		t.Errorf("err = %v, want ErrClaimLost", err)
	}
}

// TestProcessingVersionsOnlyListsProcessingOnes 钉住加工发现面：
// 只列 processing、租约空闲（或过期）且退避到期的版本。
func TestProcessingVersionsOnlyListsProcessingOnes(t *testing.T) {
	store, _ := metadataFixture(t)
	ready := "1111111111111111111111111111111111111111"
	downloading := "2222222222222222222222222222222222222222"
	processingVersion(t, store, "YwrO3B", "ABP-123", ready)
	save(t, store, candidate("Zx9K1p", "ABP-124", downloading))

	// 一条正在别处跑的加工版本（租约未过期）不该被重复捞起。
	leased := "3333333333333333333333333333333333333333"
	processingVersion(t, store, "3fG7hJ", "ABP-125", leased)
	if _, err := store.Claim(context.Background(), leased, "another-pod"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	versions, err := store.ProcessingVersions(context.Background())
	if err != nil {
		t.Fatalf("ProcessingVersions: %v", err)
	}
	if len(versions) != 1 || versions[0].Infohash != ready {
		t.Fatalf("versions = %+v, want 只有 %s", versions, ready)
	}
}
