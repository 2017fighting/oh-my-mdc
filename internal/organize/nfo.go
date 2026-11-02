package organize

import (
	"encoding/xml"
	"strings"
	"time"
)

// movie.nfo 的生成（规格 §7.1「NFO 约定」）。
//
// 这一层的全部输入都来自库里的元数据三面，没有网络：整理一段只做「读值 → 排版 → 落盘」。
// 约定逐条对着规格写：
//
//   - 固定名 `movie.nfo`（多片目录里番号命名不会被 Jellyfin 读）由调用方给；
//   - `<sorttitle>` = **纯番号**（Jellyfin 排序靠它，标签/译名都不许混进去）；
//   - `<plot>` 单槽（译文或原文，outline_zh 有就用它）；
//   - `premiered` + `releasedate` 双写；
//   - `<tag>` 承载 tags，**不写 `<genre>`**；发行商降级成一个 tag「发行: X」；
//   - 演员全写、女优排前、组内保 JavDB 顺序，`<thumb>` 直写 URL；
//   - 零值省略（runtime/rating 没有就不写），多值不拼接（一个值一个元素）。

// Actor 是 NFO 里的一个演员槽（顺序已按「女优排前、组内保 JavDB 顺序」排好）。
type Actor struct {
	Name string
	// Thumb 是 JavDB 的头像 URL，直写（Jellyfin 自拉，v1 不下载头像）。
	Thumb string
	// Female 决定「女优排前」的那一档（源数据 gender == 0）。
	Female bool
}

// Movie 是 movie.nfo 的内容。字符串空值一律省略，指针为 nil 一律不写。
type Movie struct {
	Number         string
	TitleJA        string
	TitleZH        string
	Outline        string
	OutlineZH      string
	ReleaseDate    *time.Time
	RuntimeMinutes *int
	Rating         *float64
	Studio         string
	Publisher      string
	Series         string
	Director       string
	TrailerURL     string
	Tags           []string
	Actors         []Actor
}

// Render 生成 movie.nfo 的字节（含 XML 声明，缩进两格）。
func Render(movie Movie) ([]byte, error) {
	number := strings.TrimSpace(movie.Number)

	doc := nfoMovie{
		Title:         titled(number, firstNonEmpty(movie.TitleZH, movie.TitleJA)),
		OriginalTitle: titled(number, movie.TitleJA),
		SortTitle:     number,
		Plot:          firstNonEmpty(movie.OutlineZH, movie.Outline),
		Studio:        strings.TrimSpace(movie.Studio),
		Director:      strings.TrimSpace(movie.Director),
		Set:           strings.TrimSpace(movie.Series),
		Trailer:       strings.TrimSpace(movie.TrailerURL),
	}
	// 既有标题就该有 originaltitle；连 originaltitle 都没有时（两个标题都空）
	// 只留 title = 番号，不写空的 originaltitle。
	if doc.OriginalTitle == number {
		doc.OriginalTitle = ""
	}

	if movie.ReleaseDate != nil {
		day := movie.ReleaseDate.Format("2006-01-02")
		// 双写：Jellyfin 读 premiered，别的兼容实现读 releasedate（规格 §7.1）。
		doc.Premiered = day
		doc.ReleasedDate = day
	}
	if movie.RuntimeMinutes != nil && *movie.RuntimeMinutes > 0 {
		doc.Runtime = *movie.RuntimeMinutes
	}
	if movie.Rating != nil && *movie.Rating > 0 {
		doc.Rating = *movie.Rating
	}

	for _, tag := range movie.Tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			doc.Tags = append(doc.Tags, tag)
		}
	}
	// 发行商没有独立槽：降级成 tag（规格 §7.1 publisher 行）。
	if publisher := strings.TrimSpace(movie.Publisher); publisher != "" {
		doc.Tags = append(doc.Tags, "发行: "+publisher)
	}

	for _, actor := range femaleFirst(movie.Actors) {
		if actor.Name = strings.TrimSpace(actor.Name); actor.Name == "" {
			continue
		}
		doc.Actors = append(doc.Actors, nfoActor{
			Name:  actor.Name,
			Thumb: strings.TrimSpace(actor.Thumb),
		})
	}

	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

// femaleFirst 把女优排到前面，组内保持原顺序（JavDB 的演员数组顺序）。
//
// 它必须是稳定分区而不是排序：同一性别内的先后是源数据的顺序，规格 §7.1 明确要保住。
func femaleFirst(actors []Actor) []Actor {
	ordered := make([]Actor, 0, len(actors))
	rest := make([]Actor, 0, len(actors))
	for _, actor := range actors {
		if actor.Female {
			ordered = append(ordered, actor)
		} else {
			rest = append(rest, actor)
		}
	}
	return append(ordered, rest...)
}

// titled 是「番号 + 标题」的拼法：标题为空时只留番号（不留尾随空格）。
func titled(number, title string) string {
	if title = strings.TrimSpace(title); title == "" {
		return number
	}
	return number + " " + title
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// nfoMovie / nfoActor 是 movie.nfo 的 XML 形状（Kodi 约定，Jellyfin 兼容）。
type nfoMovie struct {
	XMLName       xml.Name   `xml:"movie"`
	Title         string     `xml:"title"`
	OriginalTitle string     `xml:"originaltitle,omitempty"`
	SortTitle     string     `xml:"sorttitle"`
	Plot          string     `xml:"plot,omitempty"`
	Runtime       int        `xml:"runtime,omitempty"`
	Rating        float64    `xml:"rating,omitempty"`
	Premiered     string     `xml:"premiered,omitempty"`
	ReleasedDate  string     `xml:"releasedate,omitempty"`
	Studio        string     `xml:"studio,omitempty"`
	Director      string     `xml:"director,omitempty"`
	Tags          []string   `xml:"tag"`
	Set           string     `xml:"set,omitempty"`
	Trailer       string     `xml:"trailer,omitempty"`
	Actors        []nfoActor `xml:"actor"`
}

type nfoActor struct {
	Name  string `xml:"name"`
	Thumb string `xml:"thumb,omitempty"`
}
