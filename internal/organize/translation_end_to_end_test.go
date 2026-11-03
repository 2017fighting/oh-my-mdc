package organize_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	"github.com/2017fighting/oh-my-mdc/internal/translate"
)

// 这条用例把 #30 的两段接起来：取数段（真翻译客户端 + 假 OpenAI 端点）产出译文 →
// 整理段把译文排进 NFO。规格 §7.1 的两个槽位是验收点：
// `<title>` = 番号 + 译名（`<originaltitle>` 保留日文原题），`<plot>` = 译文。

type e2eDMM struct{}

func (e2eDMM) Lookup(context.Context, string) (dmm.Result, error) {
	// DMM 没收录：标题/简介/图片字段级回退 JavDB（规格 §5.3 的回退方向）。
	return dmm.Result{Found: false}, nil
}

type e2eJavDB struct{ coverURL string }

func (j e2eJavDB) Detail(context.Context, string, string) (appapi.DetailResult, error) {
	return appapi.DetailResult{Movie: appapi.MovieDetail{
		ID:          movieID,
		Number:      number,
		Title:       "一個月禁慾的盡頭",
		OriginTitle: "一ヶ月間の禁欲の果てに",
		Summary:     "S1スリム美女優の豪華共演",
		CoverURL:    j.coverURL,
		ReleaseDate: "2021-02-19",
	}}, nil
}

func TestTranslationReachesTheNFO(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// 假 OpenAI 兼容端点：回一条 JSON 译文（形状与 translate 的契约一致）。
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"title\":\"一个月禁欲的尽头\",\"outline\":\"S1 纤细女优豪华共演\"}"}}]}`)
	}))
	t.Cleanup(ai.Close)

	enabled := true
	f.settings.snapshot.Sections["ai_translation"] = settings.SectionState{
		Configured: true, Enabled: &enabled, Data: map[string]any{"model": "gpt-4o-mini"},
	}
	f.settings.inputs["javdb"] = settings.TestInput{Values: map[string]any{"token": "tk"}}
	f.settings.inputs["ai_translation"] = settings.TestInput{
		Values: map[string]any{
			"base_url": ai.URL + "/v1", "api_key": "sk-test", "model": "gpt-4o-mini", "enabled": true,
		},
		Enabled: true,
	}

	coverURL := "https://pics.test/cover.jpg"
	f.images.bodies[coverURL] = imageBytes(t, 900, 1200, 140, "jpeg")
	writeDownloads(t, f.downloads, map[string]string{number + ".mkv": "video"})

	scraper := scrape.New(scrape.Options{
		Store:      f.store,
		Settings:   f.settings,
		DMM:        e2eDMM{},
		JavDB:      e2eJavDB{coverURL: coverURL},
		Translator: translate.New(translate.Options{}),
		Owner:      "test-scrape",
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        func() time.Time { return time.Now().UTC() },
	})

	// 一条已经进 processing、还没有元数据的版本（取数段负责取齐并翻译）。
	seedBareProcessingVersion(t, f, hashA)

	progress, err := scraper.ProcessPending(ctx)
	if err != nil || progress.Fetched != 1 {
		t.Fatalf("取数: progress = %+v err = %v, want Fetched=1", progress, err)
	}
	metadata, _, err := f.store.Metadata(ctx, movieID)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if metadata.TitleZH != "一个月禁欲的尽头" || metadata.OutlineZH != "S1 纤细女优豪华共演" {
		t.Fatalf("译文没落库: %q/%q", metadata.TitleZH, metadata.OutlineZH)
	}

	if _, err := f.runner.ProcessPending(ctx); err != nil {
		t.Fatalf("整理: %v", err)
	}

	nfo := readFile(t, filepath.Join(f.mediaRoot, number, organize.NFOName))
	for _, want := range []string{
		"<title>" + number + " 一个月禁欲的尽头</title>",
		"<originaltitle>" + number + " 一ヶ月間の禁欲の果てに</originaltitle>",
		"<plot>S1 纤细女优豪华共演</plot>",
	} {
		if !strings.Contains(nfo, want) {
			t.Errorf("NFO 缺 %q:\n%s", want, nfo)
		}
	}
}

// seedBareProcessingVersion 种一条已加种、已进 processing、但还没有元数据的版本。
func seedBareProcessingVersion(t *testing.T, f *fixture, infohash string) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.store.SaveCandidate(ctx, task.Candidate{
		MovieID: movieID, Number: number, ListID: "wanted", Infohash: infohash,
		Source: task.SourceJavDBMagnet, Delivery: task.DeliveryMagnet,
		Payload: "magnet:?xt=urn:btih:" + infohash,
	}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	if _, err := f.store.Claim(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("Claim（加种）: %v", err)
	}
	if err := f.store.MarkAdded(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("MarkAdded: %v", err)
	}
	if _, err := f.store.Claim(ctx, infohash, "owner-scrape"); err != nil {
		t.Fatalf("Claim（取数）: %v", err)
	}
	if err := f.store.MarkProcessing(ctx, infohash, "owner-scrape"); err != nil {
		t.Fatalf("MarkProcessing: %v", err)
	}
}
