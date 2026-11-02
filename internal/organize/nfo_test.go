package organize

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

// 测试缝：movie.nfo 的排版（规格 §7.1「NFO 约定」逐条）。

func date(y int, m time.Month, d int) *time.Time {
	at := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &at
}

func ptr[T any](value T) *T { return &value }

func fullMovie() Movie {
	return Movie{
		Number:         "ABP-123",
		TitleJA:        "一ヶ月間の禁欲の果てに",
		TitleZH:        "一个月禁欲的尽头",
		Outline:        "S1スリム美女優の豪華共演",
		OutlineZH:      "S1 纤细女优豪华共演",
		ReleaseDate:    date(2021, time.February, 19),
		RuntimeMinutes: ptr(150),
		Rating:         ptr(8.86),
		Studio:         "S1 NO.1 STYLE",
		Publisher:      "S1",
		Series:         "一ヶ月間の禁欲",
		Director:       "苺原",
		TrailerURL:     "https://example.test/trailer.mp4",
		Tags:           []string{"戏剧", "美乳"},
		Actors: []Actor{
			{Name: "男优甲", Thumb: "https://example.test/m.jpg"},
			{Name: "女优乙<&>", Thumb: "https://example.test/f.jpg", Female: true},
			{Name: "女优丙", Female: true},
		},
	}
}

// TestRenderWritesEverySlot 钉住全槽位：标题/排序/剧情/日期双写/工作室/标签/演员。
func TestRenderWritesEverySlot(t *testing.T) {
	body, err := Render(fullMovie())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	text := string(body)

	for _, want := range []string{
		`<title>ABP-123 一个月禁欲的尽头</title>`,
		`<originaltitle>ABP-123 一ヶ月間の禁欲の果てに</originaltitle>`,
		`<sorttitle>ABP-123</sorttitle>`,
		`<plot>S1 纤细女优豪华共演</plot>`,
		`<runtime>150</runtime>`,
		`<rating>8.86</rating>`,
		`<premiered>2021-02-19</premiered>`,
		`<releasedate>2021-02-19</releasedate>`,
		`<studio>S1 NO.1 STYLE</studio>`,
		`<director>苺原</director>`,
		`<tag>戏剧</tag>`,
		`<tag>美乳</tag>`,
		`<tag>发行: S1</tag>`,
		`<set>一ヶ月間の禁欲</set>`,
		`<trailer>https://example.test/trailer.mp4</trailer>`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("nfo 缺 %s\n---\n%s", want, text)
		}
	}

	if strings.Contains(text, "<genre>") {
		t.Error("规格 §7.1：标签走 <tag>，不许写 <genre>")
	}
	if !strings.HasPrefix(text, xml.Header) {
		t.Errorf("缺 XML 声明: %q", text[:min(len(text), 60)])
	}

	// 演员：女优排前、组内保 JavDB 顺序，thumb 直写 URL，内容转义。
	wantActors := []string{"女优乙<&>", "女优丙", "男优甲"}
	if got := actorNames(t, body); !equal(got, wantActors) {
		t.Errorf("actors = %v, want %v（女优排前，组内保序）", got, wantActors)
	}
	if !strings.Contains(text, "<thumb>https://example.test/f.jpg</thumb>") {
		t.Errorf("演员 thumb 没有直写 URL:\n%s", text)
	}
	if !strings.Contains(text, "女优乙&lt;&amp;&gt;") {
		t.Error("演员名没有 XML 转义")
	}
}

// TestRenderOmitsZeroValues 钉住零值省略：没有的字段不写空元素。
func TestRenderOmitsZeroValues(t *testing.T) {
	body, err := Render(Movie{Number: "ABP-123"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	text := string(body)

	for _, unwanted := range []string{
		"<originaltitle>", "<plot>", "<runtime>", "<rating>",
		"<premiered>", "<releasedate>", "<studio>", "<director>",
		"<tag>", "<set>", "<trailer>", "<actor>",
	} {
		if strings.Contains(text, unwanted) {
			t.Errorf("空值不该写 %s:\n%s", unwanted, text)
		}
	}
	if !strings.Contains(text, "<title>ABP-123</title>") {
		t.Errorf("没有标题时 title 仍是番号:\n%s", text)
	}
	if !strings.Contains(text, "<sorttitle>ABP-123</sorttitle>") {
		t.Errorf("sorttitle 必须是纯番号:\n%s", text)
	}
}

// TestRenderPlotFallsBackToTheOriginal 钉住 plot 单槽与降级：
// 有译文用译文，译文缺席（AI 翻译没配/失败）用日文原文，绝不留空。
func TestRenderPlotFallsBackToTheOriginal(t *testing.T) {
	movie := fullMovie()
	movie.OutlineZH = ""
	movie.TitleZH = ""

	body, err := Render(movie)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "<plot>S1スリム美女優の豪華共演</plot>") {
		t.Errorf("plot 没降级成日文原文:\n%s", text)
	}
	if !strings.Contains(text, "<title>ABP-123 一ヶ月間の禁欲の果てに</title>") {
		t.Errorf("title 没降级成日文原文:\n%s", text)
	}
	if count := strings.Count(text, "<plot>"); count != 1 {
		t.Errorf("plot 是单槽，write %d 次", count)
	}
}

// TestRenderSortTitleIsTheBareNumber 钉住 sorttitle 不被标签/译名污染。
func TestRenderSortTitleIsTheBareNumber(t *testing.T) {
	movie := fullMovie()
	movie.Tags = []string{"中文字幕"}
	movie.Publisher = "S1"
	body, err := Render(movie)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(body), "<sorttitle>ABP-123</sorttitle>") {
		t.Errorf("sorttitle 必须是纯番号:\n%s", body)
	}
}

func actorNames(t *testing.T, body []byte) []string {
	t.Helper()
	var doc struct {
		Actors []struct {
			Name string `xml:"name"`
		} `xml:"actor"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("解 nfo: %v", err)
	}
	names := make([]string, 0, len(doc.Actors))
	for _, actor := range doc.Actors {
		names = append(names, actor.Name)
	}
	return names
}
