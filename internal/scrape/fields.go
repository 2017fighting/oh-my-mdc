package scrape

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/dmm"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 字段图：规格 §7.1 的唯一实现。
//
// 两条规则，别的一概不做：
//
//  1. **固定归属**：title/outline/图片归 DMM（它更原始、是日文），
//     演员/tags/publisher/rating/studio/series/release_date/runtime/director/trailer
//     归 JavDB（它按番号收录得更全，演员还带性别）。
//  2. **唯一回退方向 DMM→JavDB**：DMM 缺（无码/FC2/素人，或该字段为空）时，
//     只有 title/outline/图片字段级回退 JavDB；**JavDB 字段缺失留空，绝不反向回退 DMM**。
//
// 字段值之外还要留两面（GLOSSARY「刮削元数据」）：provenance 逐字段
// `{source, fetched_at, raw_value}`，raw_snapshot 逐源原样。raw_value 是**源给的原文**
// 而不是规范化后的值 —— 将来「这个 rating 怎么来的」要有答案（上游 5 分制，
// 落库 10 分制，原始值只能从 provenance 找回来）。

// 来源名（provenance.source 与 raw_snapshot 的键）。
const (
	SourceDMM   = "dmm"
	SourceJavDB = "javdb"
)

// provenance 的字段名。值类用 task 的列名；图片不是列，用它在 NFO 里的槽名。
const (
	FieldTitleJA        = "title_ja"
	FieldOutline        = "outline"
	FieldPoster         = "poster"
	FieldFanart         = "fanart"
	FieldExtraFanart    = "extrafanart"
	FieldActors         = "actors"
	FieldTags           = "tags"
	FieldPublisher      = "publisher"
	FieldRating         = "rating"
	FieldStudio         = "studio"
	FieldSeries         = "series"
	FieldReleaseDate    = "release_date"
	FieldRuntimeMinutes = "runtime_minutes"
	FieldDirector       = "director"
	FieldTrailerURL     = "trailer_url"
)

// Provenance 是一个字段的来源记录。
type Provenance struct {
	Source    string    `json:"source"`
	FetchedAt time.Time `json:"fetched_at"`
	RawValue  any       `json:"raw_value"`
}

// Values 是字段图归一化后的值。
//
// 图片没有对应的 task 列（规格 §4.1 的列清单里没有）：它们的「值」落在
// provenance 的 raw_value 上，整理票从那里读 URL。Sleeve 是 poster 与 fanart
// **共用**的一组横版 sleeve 候选 —— pl/ps 哪张是横版未实测（规格 §13），
// 由整理票的探针取「宽>高」那张：右裁 2:3 当 poster、原样当 fanart。
type Values struct {
	TitleJA        string
	Outline        string
	Sleeve         []string
	ExtraFanart    []string
	Actors         []task.Actor
	Tags           []string
	Publisher      string
	Rating         *float64
	Studio         string
	Series         string
	ReleaseDate    *time.Time
	RuntimeMinutes *int
	Director       string
	TrailerURL     string
}

// Fetched 是一次刮削取数的产物：值 + 两个 JSON 面。
type Fetched struct {
	Values     Values
	Provenance map[string]Provenance
	Raw        map[string]json.RawMessage
}

// TaskMetadata 把取数结果翻成落库形态（两个 JSON 面序列化）。
func (f Fetched) TaskMetadata() (task.Metadata, error) {
	provenance, err := json.Marshal(f.Provenance)
	if err != nil {
		return task.Metadata{}, err
	}
	raw, err := json.Marshal(f.Raw)
	if err != nil {
		return task.Metadata{}, err
	}
	return task.Metadata{
		TitleJA:        f.Values.TitleJA,
		Outline:        f.Values.Outline,
		ReleaseDate:    f.Values.ReleaseDate,
		RuntimeMinutes: f.Values.RuntimeMinutes,
		Studio:         f.Values.Studio,
		Publisher:      f.Values.Publisher,
		Series:         f.Values.Series,
		Director:       f.Values.Director,
		TrailerURL:     f.Values.TrailerURL,
		Rating:         f.Values.Rating,
		Tags:           f.Values.Tags,
		Provenance:     provenance,
		RawSnapshot:    raw,
	}, nil
}

// Merge 按 §7.1 归并两个源，产出值、provenance 与 raw_snapshot。
//
// 它是纯函数：没有网络、没有 DB、没有时钟。测试因此可以拿真实抓录喂它，
// 而「哪一列归谁、怎么回退」的每一条规则都在这里读得出来。
func Merge(fetchedAt time.Time, dmmResult dmm.Result, detail appapi.DetailResult) Fetched {
	javdb, javdbRaw := detail.Movie, detail.Raw

	fetched := Fetched{
		Provenance: make(map[string]Provenance, 15),
		Raw:        make(map[string]json.RawMessage, 3),
	}

	if len(dmmResult.SearchRaw) > 0 {
		fetched.Raw["dmm_search"] = dmmResult.SearchRaw
	}
	if dmmResult.Found && len(dmmResult.ContentRaw) > 0 {
		fetched.Raw["dmm_content"] = dmmResult.ContentRaw
	}
	if len(javdbRaw) > 0 {
		fetched.Raw[SourceJavDB] = javdbRaw
	}

	dmmContent := dmmResult.Content
	dmmHit := dmmResult.Found

	// title_ja：DMM 日文标题 → JavDB 的**日文原题**（origin_title）→ JavDB 标题。
	// 第一顺位回退用 origin_title 而不是 title：JavDB 的 title 常是繁中译名，
	// 而这一列叫 title_ja（NFO 的 <originaltitle> 靠它）。
	switch {
	case dmmHit && dmmContent.Title != "":
		fetched.Values.TitleJA = dmmContent.Title
		fetched.Provenance[FieldTitleJA] = entry(SourceDMM, fetchedAt, dmmContent.Title)
	case javdb.OriginTitle != "":
		fetched.Values.TitleJA = javdb.OriginTitle
		fetched.Provenance[FieldTitleJA] = entry(SourceJavDB, fetchedAt, javdb.OriginTitle)
	case javdb.Title != "":
		fetched.Values.TitleJA = javdb.Title
		fetched.Provenance[FieldTitleJA] = entry(SourceJavDB, fetchedAt, javdb.Title)
	default:
		fetched.Provenance[FieldTitleJA] = entry(SourceJavDB, fetchedAt, "")
	}

	// outline：DMM description → JavDB summary。
	switch {
	case dmmHit && dmmContent.Description != "":
		fetched.Values.Outline = dmmContent.Description
		fetched.Provenance[FieldOutline] = entry(SourceDMM, fetchedAt, dmmContent.Description)
	case javdb.Summary != "":
		fetched.Values.Outline = javdb.Summary
		fetched.Provenance[FieldOutline] = entry(SourceJavDB, fetchedAt, javdb.Summary)
	default:
		fetched.Provenance[FieldOutline] = entry(SourceJavDB, fetchedAt, "")
	}

	// 图片：DMM 的 pl/ps（横版 sleeve 候选）+ jp-N（预览）；整组缺失才回退 JavDB。
	var sleeve []string
	if dmmHit {
		sleeve = appendNonEmpty(sleeve, dmmContent.PackageLargeURL, dmmContent.PackageMediumURL)
	}
	if len(sleeve) > 0 {
		fetched.Values.Sleeve = sleeve
		// poster 与 fanart 是同一组候选的两个产物（右裁的竖版 / 原横版），
		// 所以两条 provenance 记同一份 raw_value。
		fetched.Provenance[FieldPoster] = entry(SourceDMM, fetchedAt, sleeve)
		fetched.Provenance[FieldFanart] = entry(SourceDMM, fetchedAt, sleeve)
	} else if javdb.CoverURL != "" {
		fetched.Values.Sleeve = []string{javdb.CoverURL}
		fetched.Provenance[FieldPoster] = entry(SourceJavDB, fetchedAt, []string{javdb.CoverURL})
		fetched.Provenance[FieldFanart] = entry(SourceJavDB, fetchedAt, []string{javdb.CoverURL})
	} else {
		fetched.Provenance[FieldPoster] = entry(SourceJavDB, fetchedAt, []string{})
		fetched.Provenance[FieldFanart] = entry(SourceJavDB, fetchedAt, []string{})
	}

	if dmmHit && len(dmmContent.SampleURLs) > 0 {
		fetched.Values.ExtraFanart = dmmContent.SampleURLs
		fetched.Provenance[FieldExtraFanart] = entry(SourceDMM, fetchedAt, dmmContent.SampleURLs)
	} else {
		previews := previewURLs(javdb.Preview)
		fetched.Values.ExtraFanart = previews
		fetched.Provenance[FieldExtraFanart] = entry(SourceJavDB, fetchedAt, previews)
	}

	// 以下全部归 JavDB（固定归属）：DMM 有值也不用，缺失就留空。
	fetched.Values.Actors = actorsOf(javdb.Actors)
	fetched.Provenance[FieldActors] = entry(SourceJavDB, fetchedAt, rawActors(javdb.Actors))

	fetched.Values.Tags = tagNames(javdb.Tags)
	fetched.Provenance[FieldTags] = entry(SourceJavDB, fetchedAt, rawTags(javdb.Tags))

	fetched.Values.Publisher = javdb.PublisherName
	fetched.Provenance[FieldPublisher] = entry(SourceJavDB, fetchedAt, javdb.PublisherName)

	fetched.Values.Rating = ratingOf(javdb.Score)
	fetched.Provenance[FieldRating] = entry(SourceJavDB, fetchedAt, javdb.Score)

	fetched.Values.Studio = javdb.MakerName
	fetched.Provenance[FieldStudio] = entry(SourceJavDB, fetchedAt, javdb.MakerName)

	fetched.Values.Series = javdb.SeriesName
	fetched.Provenance[FieldSeries] = entry(SourceJavDB, fetchedAt, javdb.SeriesName)

	fetched.Values.ReleaseDate = dateOf(javdb.ReleaseDate)
	fetched.Provenance[FieldReleaseDate] = entry(SourceJavDB, fetchedAt, javdb.ReleaseDate)

	fetched.Values.RuntimeMinutes = minutesOf(javdb.DurationMinutes)
	fetched.Provenance[FieldRuntimeMinutes] = entry(SourceJavDB, fetchedAt, javdb.DurationMinutes)

	fetched.Values.Director = javdb.DirectorName
	fetched.Provenance[FieldDirector] = entry(SourceJavDB, fetchedAt, javdb.DirectorName)

	fetched.Values.TrailerURL = javdb.TrailerURL
	fetched.Provenance[FieldTrailerURL] = entry(SourceJavDB, fetchedAt, javdb.TrailerURL)

	return fetched
}

func entry(source string, at time.Time, raw any) Provenance {
	return Provenance{Source: source, FetchedAt: at.UTC(), RawValue: raw}
}

// actorsOf 把 JavDB 演员翻成 actor 表的行（性别 0/1 → female/male，其余留空）。
func actorsOf(actors []appapi.Actor) []task.Actor {
	out := make([]task.Actor, 0, len(actors))
	for _, actor := range actors {
		out = append(out, task.Actor{
			JavDBID:   actor.ID,
			Name:      actor.Name,
			Gender:    genderOf(actor.Gender),
			AvatarURL: actor.AvatarURL,
		})
	}
	return out
}

func genderOf(gender appapi.Gender) string {
	switch gender {
	case appapi.GenderFemale:
		return "female"
	case appapi.GenderMale:
		return "male"
	default:
		// 不知道就留空（落 NULL）：不替上游猜一个最常见的。
		return ""
	}
}

// rawActors 是 provenance 里的演员原文：用源自己的键名，不借用本仓的结构体字段名。
//
// 性别缺失/不认识时写 null（源给的就是 null）：把本仓的 -1 写进去就不再是原文。
func rawActors(actors []appapi.Actor) []map[string]any {
	out := make([]map[string]any, 0, len(actors))
	for _, actor := range actors {
		var gender any
		if actor.Gender != appapi.GenderUnknown {
			gender = int(actor.Gender)
		}
		out = append(out, map[string]any{
			"id":         actor.ID,
			"name":       actor.Name,
			"gender":     gender,
			"avatar_url": actor.AvatarURL,
		})
	}
	return out
}

func tagNames(tags []appapi.Tag) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		out = append(out, tag.Name)
	}
	return out
}

func rawTags(tags []appapi.Tag) []map[string]string {
	out := make([]map[string]string, 0, len(tags))
	for _, tag := range tags {
		out = append(out, map[string]string{"id": tag.ID, "name": tag.Name})
	}
	return out
}

func previewURLs(previews []appapi.PreviewImage) []string {
	out := make([]string, 0, len(previews))
	for _, preview := range previews {
		url := preview.LargeURL
		if url == "" {
			url = preview.ThumbURL
		}
		if url != "" {
			out = append(out, url)
		}
	}
	return out
}

// ratingOf 把站点的 5 分制字符串换成 10 分制数值（规格 §7.1：rating 落 JavDB）。
//
// 换算是字段图的活，不是传输层的：provenance 的 raw_value 仍是上游那个 "4.43"。
func ratingOf(score string) *float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(score), 64)
	if err != nil || value <= 0 {
		return nil
	}
	converted := value * 2
	return &converted
}

func dateOf(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if len(raw) < 10 {
		return nil
	}
	at, err := time.Parse("2006-01-02", raw[:10])
	if err != nil {
		return nil
	}
	return &at
}

func minutesOf(minutes int) *int {
	if minutes <= 0 {
		return nil
	}
	return &minutes
}

func appendNonEmpty(into []string, values ...string) []string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			into = append(into, value)
		}
	}
	return into
}
