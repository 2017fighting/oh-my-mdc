package organize

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 整理一段要读的元数据视图：强类型列直接读，图片与演员顺序从 provenance 里取。
//
// 为什么不查 actor 表：NFO 要的是「女优排前、组内保 JavDB 顺序」（规格 §7.1），
// 而那个顺序原样留在 provenance 的 raw_value 里（取数段写的是 JavDB 数组顺序，
// 见 scrape.rawActors）。从那里读，NFO 就是取数结果的一次纯排版。
type cleanMetadata struct {
	// Movie 是 NFO 的全部槽位（值 + 演员顺序），排版由 Render 负责。
	Movie
	// sleeve 是 poster 与 fanart 共用的横版候选（pl/ps），extraFanart 是预览图。
	sleeve      []sourcedImage
	extraFanart []sourcedImage
}

// sourcedImage 是一张图与它被刮到的时刻。
//
// 抓取时刻是「要不要重下」的判据：目标文件的 mtime 比它新就说明这张图已经是
// 按这份元数据落过的盘，重跑时零下载零改写（规格 §7.2 复用行）。
type sourcedImage struct {
	URL       string
	FetchedAt time.Time
}

// provenanceEntry 是 provenance 的一个字段条目（{source, fetched_at, raw_value}）。
type provenanceEntry struct {
	Source    string          `json:"source"`
	FetchedAt time.Time       `json:"fetched_at"`
	RawValue  json.RawMessage `json:"raw_value"`
}

// rawActor 是 provenance 里 actors 的原文形态（取数段写的源字段名）。
type rawActor struct {
	Name      string `json:"name"`
	Gender    *int   `json:"gender"`
	AvatarURL string `json:"avatar_url"`
}

// readMetadata 把任务元数据翻成整理用的视图；坏 JSON 当作「这个字段没有」，
// 绝不让一份旧数据把入库整个拦住。
func readMetadata(meta task.Metadata, number string) cleanMetadata {
	clean := cleanMetadata{Movie: Movie{
		Number:         strings.TrimSpace(number),
		TitleJA:        meta.TitleJA,
		TitleZH:        meta.TitleZH,
		Outline:        meta.Outline,
		OutlineZH:      meta.OutlineZH,
		ReleaseDate:    meta.ReleaseDate,
		RuntimeMinutes: meta.RuntimeMinutes,
		Rating:         meta.Rating,
		Studio:         meta.Studio,
		Publisher:      meta.Publisher,
		Series:         meta.Series,
		Director:       meta.Director,
		TrailerURL:     meta.TrailerURL,
		Tags:           meta.Tags,
	}}

	entries := parseProvenance(meta.Provenance)

	// poster 与 fanart 记同一份 raw_value（同一组 sleeve 候选的两个产物），
	// 取先有的那一条即可。
	for _, field := range []string{"poster", "fanart"} {
		if entry, ok := entries[field]; ok {
			clean.sleeve = imagesOf(entry)
			break
		}
	}
	if entry, ok := entries["extrafanart"]; ok {
		clean.extraFanart = imagesOf(entry)
	}
	if entry, ok := entries["actors"]; ok {
		clean.Actors = actorsOf(entry.RawValue)
	}
	return clean
}

// parseProvenance 解 provenance；解不开就是空表（每个字段都按缺失处理）。
func parseProvenance(raw json.RawMessage) map[string]provenanceEntry {
	entries := map[string]provenanceEntry{}
	if len(raw) == 0 {
		return entries
	}
	_ = json.Unmarshal(raw, &entries)
	return entries
}

// imagesOf 把一条 provenance 的 raw_value（URL 数组）翻成带抓取时刻的图片。
func imagesOf(entry provenanceEntry) []sourcedImage {
	var urls []string
	if err := json.Unmarshal(entry.RawValue, &urls); err != nil {
		return nil
	}
	images := make([]sourcedImage, 0, len(urls))
	for _, url := range urls {
		if url = strings.TrimSpace(url); url != "" {
			images = append(images, sourcedImage{URL: url, FetchedAt: entry.FetchedAt})
		}
	}
	return images
}

// actorsOf 按源顺序翻演员；gender 0 是女优、1 是男优、缺失/异值按「不知道」。
func actorsOf(raw json.RawMessage) []Actor {
	var raws []rawActor
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil
	}
	actors := make([]Actor, 0, len(raws))
	for _, actor := range raws {
		name := strings.TrimSpace(actor.Name)
		if name == "" {
			continue
		}
		actors = append(actors, Actor{
			Name:   name,
			Thumb:  strings.TrimSpace(actor.AvatarURL),
			Female: actor.Gender != nil && *actor.Gender == 0,
		})
	}
	return actors
}

// sleeveFetchTime 是 sleeve 那一组候选中最早（也是唯一）的抓取时刻。
func (m cleanMetadata) sleeveFetchTime() time.Time {
	if len(m.sleeve) == 0 {
		return time.Time{}
	}
	return m.sleeve[0].FetchedAt
}

// extraFetchTime 是预览图那一组的抓取时刻。
func (m cleanMetadata) extraFetchTime() time.Time {
	if len(m.extraFanart) == 0 {
		return time.Time{}
	}
	return m.extraFanart[0].FetchedAt
}

// title 是通知里的标题：有中文译名用译名，否则原文（规格 §5.6 filed 行）。
func (m cleanMetadata) title() string {
	return firstNonEmpty(m.TitleZH, m.TitleJA)
}

// translated 报告译文是不是到位了（AI 翻译可选的产物，规格 §5.7）。
func (m cleanMetadata) translated() bool {
	return strings.TrimSpace(m.TitleZH) != "" || strings.TrimSpace(m.OutlineZH) != ""
}
