package scrape_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/dmm"
	"github.com/2017fighting/oh-my-mdc/internal/scrape"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：scrape.Merge —— 字段映射全表（规格 §7.1）的纯函数。
// 断言的是「哪一列归谁、缺了怎么回退、两个 JSON 面记下了什么」。

var fetchedAt = time.Date(2025, 4, 1, 12, 0, 0, 0, time.UTC)

// TestMergeTakesTitleOutlineAndImagesFromDMM 钉住默认归属：
// 值本身、以及 provenance 指回 DMM。
func TestMergeTakesTitleOutlineAndImagesFromDMM(t *testing.T) {
	dmmResult := dmmFixture(t)
	detail := javdbFixture(t)

	fetched := scrape.Merge(fetchedAt, dmmResult, detail)

	if fetched.Values.TitleJA != dmmResult.Content.Title {
		t.Errorf("title_ja = %q, want DMM 日文标题", fetched.Values.TitleJA)
	}
	if fetched.Values.Outline != dmmResult.Content.Description {
		t.Errorf("outline = %q, want DMM 简介", fetched.Values.Outline)
	}
	if len(fetched.Values.Sleeve) != 2 {
		t.Fatalf("sleeve = %v, want pl+ps 两张候选", fetched.Values.Sleeve)
	}
	if fetched.Values.Sleeve[0] != "https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/ssis00001/ssis00001pl.jpg" {
		t.Errorf("sleeve[0] = %q, want pl.jpg", fetched.Values.Sleeve[0])
	}
	if len(fetched.Values.ExtraFanart) != 10 {
		t.Errorf("extrafanart = %d 张, want 10 张 jp-N", len(fetched.Values.ExtraFanart))
	}
	for _, field := range []string{scrape.FieldTitleJA, scrape.FieldOutline, scrape.FieldPoster, scrape.FieldFanart, scrape.FieldExtraFanart} {
		if got := fetched.Provenance[field].Source; got != scrape.SourceDMM {
			t.Errorf("%s 的来源 = %q, want dmm", field, got)
		}
	}
}

// TestMergeTakesTheRestFromJavDB 钉住另一侧的固定归属：演员（含性别）、tags、
// publisher、rating（5 分制→10 分制）、studio、series、日期、时长、导演、trailer。
func TestMergeTakesTheRestFromJavDB(t *testing.T) {
	dmmResult := dmmFixture(t)
	detail := javdbFixture(t)

	fetched := scrape.Merge(fetchedAt, dmmResult, detail)
	values := fetched.Values

	if len(values.Actors) != 3 {
		t.Fatalf("actors = %d, want 3（JavDB 全量落行）", len(values.Actors))
	}
	if values.Actors[0].JavDBID != "A5yq" || values.Actors[0].Gender != "female" {
		t.Errorf("演员 1 = %+v", values.Actors[0])
	}
	if values.Actors[2].Gender != "male" {
		t.Errorf("演员 3 gender = %q, want male", values.Actors[2].Gender)
	}
	if len(values.Tags) != 6 || values.Tags[0] != "戏剧" {
		t.Errorf("tags = %v, want JavDB 的 6 个", values.Tags)
	}
	if values.Rating == nil || *values.Rating != 8.86 {
		t.Errorf("rating = %v, want 8.86（上游 4.43 ×2）", values.Rating)
	}
	if values.Studio != "S1 NO.1 STYLE" {
		t.Errorf("studio = %q, want JavDB maker", values.Studio)
	}
	if values.Series == "" || values.Director != "苺原" {
		t.Errorf("series/director = %q/%q", values.Series, values.Director)
	}
	if values.ReleaseDate == nil || values.ReleaseDate.Format("2006-01-02") != "2021-02-19" {
		t.Errorf("release_date = %v, want 2021-02-19", values.ReleaseDate)
	}
	if values.RuntimeMinutes == nil || *values.RuntimeMinutes != 150 {
		t.Errorf("runtime = %v, want 150 分钟（这个端点的 duration 是分钟）", values.RuntimeMinutes)
	}
	if values.TrailerURL == "" {
		t.Error("trailer_url 为空，want JavDB preview_video_url")
	}
	// provenance 的 raw_value 是上游原文：rating 仍是 "4.43"，不是 8.86。
	if got := fetched.Provenance[scrape.FieldRating].RawValue; got != "4.43" {
		t.Errorf("rating 的 raw_value = %v, want 原文 \"4.43\"", got)
	}
}

// TestMergeLeavesMissingJavDBFieldsEmpty 钉住「唯一回退方向」的另一半：
// JavDB 的 publisher 是 null（录制里就是），**不许**反向拿 DMM 的 label 补上。
func TestMergeLeavesMissingJavDBFieldsEmpty(t *testing.T) {
	dmmResult := dmmFixture(t)
	if dmmResult.Content.Label == "" {
		t.Fatal("夹具里 DMM label 该有值（否则这条测试证明不了反向回退被挡住）")
	}
	detail := javdbFixture(t)

	fetched := scrape.Merge(fetchedAt, dmmResult, detail)

	if fetched.Values.Publisher != "" {
		t.Errorf("publisher = %q, want 空（JavDB 没给就留空，不反向回退 DMM label %q）",
			fetched.Values.Publisher, dmmResult.Content.Label)
	}
	if got := fetched.Provenance[scrape.FieldPublisher]; got.Source != scrape.SourceJavDB || got.RawValue != "" {
		t.Errorf("publisher provenance = %+v, want javdb + 空", got)
	}
}

// TestMergeFallsBackToJavDBWhenDMMHasNoTitle 是 DMM 整体缺失（无码/FC2/素人）：
// title/outline/图片整组回退 JavDB，其余 JavDB 字段照旧。
func TestMergeFallsBackToJavDBWhenDMMHasNoTitle(t *testing.T) {
	detail := javdbFixture(t)
	absent := dmm.Result{Found: false, SearchRaw: json.RawMessage(`{"data":{}}`)}

	fetched := scrape.Merge(fetchedAt, absent, detail)
	movie := detail.Movie

	if fetched.Values.TitleJA != movie.OriginTitle {
		t.Errorf("title_ja = %q, want JavDB origin_title（日文原题）%q", fetched.Values.TitleJA, movie.OriginTitle)
	}
	if fetched.Values.Outline != movie.Summary {
		t.Errorf("outline = %q, want JavDB summary", fetched.Values.Outline)
	}
	if len(fetched.Values.Sleeve) != 1 || fetched.Values.Sleeve[0] != movie.CoverURL {
		t.Errorf("sleeve = %v, want JavDB cover_url（横版封面，整理票右裁）", fetched.Values.Sleeve)
	}
	if len(fetched.Values.ExtraFanart) != len(movie.Preview) || fetched.Values.ExtraFanart[0] != movie.Preview[0].LargeURL {
		t.Errorf("extrafanart = %v, want JavDB 预览图", fetched.Values.ExtraFanart)
	}
	for _, field := range []string{scrape.FieldTitleJA, scrape.FieldOutline, scrape.FieldPoster, scrape.FieldExtraFanart} {
		if got := fetched.Provenance[field].Source; got != scrape.SourceJavDB {
			t.Errorf("%s 的来源 = %q, want javdb（回退）", field, got)
		}
	}
	if len(fetched.Raw) == 0 || fetched.Raw["dmm_search"] == nil {
		t.Error("DMM 没命中也要留搜索腿的原文（证明是站点没有这条）")
	}
	if fetched.Raw["dmm_content"] != nil {
		t.Error("没命中不该有内容腿原文")
	}
}

// TestMergeFallsBackPerFieldOnSchemaDrift 是已知风险 §13 的落点：
// DMM 命中但字段整块漂走了 → **逐字段**回退 JavDB，不失败任务。
func TestMergeFallsBackPerFieldOnSchemaDrift(t *testing.T) {
	detail := javdbFixture(t)
	drifted := dmm.Result{
		Found: true,
		Content: dmm.Content{
			ID:    "ssis00001",
			Title: "只剩下的标题",
			// description/packageImage/sampleImages 都不见了。
		},
		SearchRaw:  json.RawMessage(`{"data":{}}`),
		ContentRaw: json.RawMessage(`{"data":{"ppvContent":{"id":"ssis00001","title":"只剩下的标题"}}}`),
	}

	fetched := scrape.Merge(fetchedAt, drifted, detail)
	movie := detail.Movie

	if fetched.Values.TitleJA != "只剩下的标题" {
		t.Errorf("title_ja = %q, want DMM 剩下的字段", fetched.Values.TitleJA)
	}
	if fetched.Values.Outline != movie.Summary {
		t.Errorf("outline = %q, want JavDB 回退", fetched.Values.Outline)
	}
	if len(fetched.Values.Sleeve) != 1 || fetched.Values.Sleeve[0] != movie.CoverURL {
		t.Errorf("sleeve = %v, want JavDB 回退", fetched.Values.Sleeve)
	}
	if got := fetched.Provenance[scrape.FieldTitleJA].Source; got != scrape.SourceDMM {
		t.Errorf("title_ja 来源 = %q, want dmm", got)
	}
	if got := fetched.Provenance[scrape.FieldOutline].Source; got != scrape.SourceJavDB {
		t.Errorf("outline 来源 = %q, want javdb（逐字段回退）", got)
	}
}

// TestMergeFallsBackToJavDBTitleWhenOriginTitleIsMissing 是 title 的第二顺位：
// 没有 origin_title 时用 JavDB 的 title（而不是留空）。
func TestMergeFallsBackToJavDBTitleWhenOriginTitleIsMissing(t *testing.T) {
	dmmResult := dmmFixture(t)
	detail := javdbFixture(t)
	detail.Movie.OriginTitle = ""
	movie := detail.Movie

	fetched := scrape.Merge(fetchedAt, dmmResult, detail)

	// DMM 命中时 title_ja 仍归 DMM；这条断言的是「DMM 缺 + 没有 origin_title」。
	absent := dmm.Result{}
	fetched = scrape.Merge(fetchedAt, absent, detail)
	if fetched.Values.TitleJA != movie.Title {
		t.Errorf("title_ja = %q, want JavDB title %q", fetched.Values.TitleJA, movie.Title)
	}
}

// TestMergeRecordsProvenanceAndRawSnapshots 是两个 JSON 面的形状：
// 每个字段一条 {source, fetched_at, raw_value}；raw_snapshot 逐源原样。
func TestMergeRecordsProvenanceAndRawSnapshots(t *testing.T) {
	dmmResult := dmmFixture(t)
	detail := javdbFixture(t)

	fetched := scrape.Merge(fetchedAt, dmmResult, detail)

	allFields := []string{
		scrape.FieldTitleJA, scrape.FieldOutline, scrape.FieldPoster, scrape.FieldFanart,
		scrape.FieldExtraFanart, scrape.FieldActors, scrape.FieldTags, scrape.FieldPublisher,
		scrape.FieldRating, scrape.FieldStudio, scrape.FieldSeries, scrape.FieldReleaseDate,
		scrape.FieldRuntimeMinutes, scrape.FieldDirector, scrape.FieldTrailerURL,
	}
	if len(fetched.Provenance) != len(allFields) {
		t.Errorf("provenance 有 %d 条, want %d（字段图每行一条）", len(fetched.Provenance), len(allFields))
	}
	for _, field := range allFields {
		got, ok := fetched.Provenance[field]
		if !ok {
			t.Errorf("provenance 缺 %q", field)
			continue
		}
		if got.Source != scrape.SourceDMM && got.Source != scrape.SourceJavDB {
			t.Errorf("%s 的来源 = %q", field, got.Source)
		}
		if !got.FetchedAt.Equal(fetchedAt) {
			t.Errorf("%s 的 fetched_at = %v, want %v", field, got.FetchedAt, fetchedAt)
		}
	}

	if len(fetched.Raw) != 3 {
		t.Errorf("raw_snapshot 有 %d 个源, want 3（dmm_search/dmm_content/javdb）: %v",
			len(fetched.Raw), keysOf(fetched.Raw))
	}
	var content struct {
		Data struct {
			PPVContent struct {
				ID string `json:"id"`
			} `json:"ppvContent"`
		} `json:"data"`
	}
	if err := json.Unmarshal(fetched.Raw["dmm_content"], &content); err != nil {
		t.Fatalf("dmm_content 不是原样的 JSON: %v", err)
	}
	if content.Data.PPVContent.ID != "ssis00001" {
		t.Errorf("dmm_content 的原样内容丢了：id = %q", content.Data.PPVContent.ID)
	}
	var snapshotDetail struct {
		Movie struct {
			ID string `json:"id"`
		} `json:"movie"`
	}
	if err := json.Unmarshal(fetched.Raw[scrape.SourceJavDB], &snapshotDetail); err != nil {
		t.Fatalf("javdb 快照不是原样的 JSON: %v", err)
	}
	if snapshotDetail.Movie.ID != "ZY5eq" {
		t.Errorf("javdb 快照的原样内容丢了：id = %q", snapshotDetail.Movie.ID)
	}
}

// TestTaskMetadataCarriesBothFacesToTheStore 确认落库形态：值进列，
// 两个 JSON 面序列化后原样交给 task.Store（序列化失败是不可能的，这里只验形状）。
func TestTaskMetadataCarriesBothFacesToTheStore(t *testing.T) {
	fetched := scrape.Merge(fetchedAt, dmmFixture(t), javdbFixture(t))

	metadata, err := fetched.TaskMetadata()
	if err != nil {
		t.Fatalf("TaskMetadata: %v", err)
	}
	if metadata.TitleJA != fetched.Values.TitleJA || metadata.RuntimeMinutes == nil {
		t.Errorf("列值没搬过去: %+v", metadata)
	}
	var provenance map[string]scrape.Provenance
	if err := json.Unmarshal(metadata.Provenance, &provenance); err != nil {
		t.Fatalf("provenance 不是 JSON: %v", err)
	}
	if provenance[scrape.FieldTitleJA].Source != scrape.SourceDMM {
		t.Errorf("provenance 落库后丢了来源: %+v", provenance[scrape.FieldTitleJA])
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(metadata.RawSnapshot, &snapshot); err != nil {
		t.Fatalf("raw_snapshot 不是 JSON: %v", err)
	}
	if len(snapshot) != 3 {
		t.Errorf("raw_snapshot = %v", keysOf(snapshot))
	}
	// 演员也从同一份取值出去（actor/task_actor 的行来自这里）。
	if len(fetched.Values.Actors) != 3 || fetched.Values.Actors[0] != (task.Actor{
		JavDBID: "A5yq", Name: "葵つかさ", Gender: "female",
		AvatarURL: "https://tp.spfcas.com/rhe951l4q/avatars/a5/A5yq.jpg",
	}) {
		t.Errorf("演员取值 = %+v", fetched.Values.Actors)
	}
}

// TestMergeTreatsUnknownGenderAsEmpty 确认性别 0/1 之外的取值不猜。
func TestMergeTreatsUnknownGenderAsEmpty(t *testing.T) {
	movie := appapi.MovieDetail{
		Actors: []appapi.Actor{
			{ID: "a", Name: "甲", Gender: appapi.GenderUnknown},
			{ID: "b", Name: "乙", Gender: appapi.GenderFemale},
		},
	}
	fetched := scrape.Merge(fetchedAt, dmm.Result{}, appapi.DetailResult{Movie: movie})

	if fetched.Values.Actors[0].Gender != "" {
		t.Errorf("未知性别 = %q, want 空", fetched.Values.Actors[0].Gender)
	}
	if fetched.Values.Actors[1].Gender != "female" {
		t.Errorf("0 号性别 = %q, want female", fetched.Values.Actors[1].Gender)
	}
	// provenance 的 raw_value 仍是源给的形状：不知道就是 null，不是本仓的 -1。
	raw, ok := fetched.Provenance[scrape.FieldActors].RawValue.([]map[string]any)
	if !ok || len(raw) != 2 {
		t.Fatalf("演员 raw_value = %#v", fetched.Provenance[scrape.FieldActors].RawValue)
	}
	if raw[0]["gender"] != nil {
		t.Errorf("未知性别的 raw_value = %v, want null（源给的就是 null）", raw[0]["gender"])
	}
	if raw[1]["gender"] != 0 {
		t.Errorf("女优的 raw_value = %v, want 0", raw[1]["gender"])
	}
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
