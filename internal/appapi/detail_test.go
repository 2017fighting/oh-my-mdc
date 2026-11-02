package appapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 测试缝：Client.Detail —— JavDB 详情端点 `/api/v4/movies/{id}` 的传输与解析。
//
// 夹具是 mdc-lite 2026-10-29 的真实抓录（testdata/javdb-SSIS-001-detail.json，
// sha256 见 testdata/fixtures.json）：字段图（规格 §7.1 的 JavDB 列）按它定，
// 不按想象定。

func detailFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "javdb-SSIS-001-detail.json"))
	if err != nil {
		t.Fatalf("读夹具: %v", err)
	}
	return body
}

// TestDetailReadsTheV4Endpoint 钉住请求形状：路径、`from_rankings=false`、签名头。
func TestDetailReadsTheV4Endpoint(t *testing.T) {
	var got *http.Request
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		_, _ = w.Write(detailFixture(t))
	})

	if _, err := c.Detail(context.Background(), "ZY5eq"); err != nil {
		t.Fatalf("Detail: %v", err)
	}

	if got.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", got.Method)
	}
	if got.URL.Path != "/api/v4/movies/ZY5eq" {
		t.Errorf("path = %q, want /api/v4/movies/ZY5eq", got.URL.Path)
	}
	if got.URL.Query().Get("from_rankings") != "false" {
		t.Errorf("from_rankings = %q, want false", got.URL.Query().Get("from_rankings"))
	}
	if got.Header.Get("jdsignature") == "" {
		t.Error("缺 jdsignature 头")
	}
}

// TestDetailParsesTheCapturedPayload 是字段图的取值面：JavDB 侧每一列都要读得出来。
func TestDetailParsesTheCapturedPayload(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(detailFixture(t))
	})

	result, err := c.Detail(context.Background(), "ZY5eq")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	movie, raw := result.Movie, result.Raw

	if movie.ID != "ZY5eq" || movie.Number != "SSIS-001" {
		t.Errorf("id/number = %q/%q", movie.ID, movie.Number)
	}
	if !strings.HasPrefix(movie.Title, "刺激您五感") {
		t.Errorf("title = %q", movie.Title)
	}
	if !strings.HasPrefix(movie.OriginTitle, "一ヶ月間の禁欲") {
		t.Errorf("origin_title = %q, want 日文原题", movie.OriginTitle)
	}
	if !strings.HasPrefix(movie.Summary, "三上悠亞史上最棒") {
		t.Errorf("summary = %q", movie.Summary)
	}
	if movie.Score != "4.43" {
		t.Errorf("score = %q, want 字符串 4.43（上游是字符串，不是数字）", movie.Score)
	}
	if movie.ReleaseDate != "2021-02-19" {
		t.Errorf("release_date = %q", movie.ReleaseDate)
	}
	if movie.DurationMinutes != 150 {
		t.Errorf("duration = %d, want 150 分钟", movie.DurationMinutes)
	}
	if movie.MakerName != "S1 NO.1 STYLE" || movie.DirectorName != "苺原" {
		t.Errorf("maker/director = %q/%q", movie.MakerName, movie.DirectorName)
	}
	if movie.PublisherName != "" {
		t.Errorf("publisher_name = %q, want 空（捕捉里是 null）", movie.PublisherName)
	}
	if machine := movie.SeriesName; !strings.HasPrefix(machine, "一ヶ月間の禁欲") {
		t.Errorf("series = %q", machine)
	}
	if len(movie.Tags) != 6 || movie.Tags[0].Name != "戏剧" {
		t.Errorf("tags = %+v", movie.Tags)
	}
	if len(movie.Actors) != 3 {
		t.Fatalf("actors = %d, want 3", len(movie.Actors))
	}
	first := movie.Actors[0]
	if first.ID != "A5yq" || first.Name != "葵つかさ" || first.Gender != GenderFemale {
		t.Errorf("第一个演员 = %+v", first)
	}
	if first.AvatarURL == "" {
		t.Error("演员头像 URL 为空（NFO 的 <thumb> 直写它）")
	}
	if movie.Actors[2].Gender != GenderMale {
		t.Errorf("第三个演员 gender = %d, want 1（男优）", movie.Actors[2].Gender)
	}
	if len(movie.Preview) != 10 || movie.Preview[0].LargeURL == "" {
		t.Errorf("preview_images = %d, want 10 张且带 large_url", len(movie.Preview))
	}
	if movie.CoverURL == "" || movie.ThumbURL == "" {
		t.Errorf("cover/thumb = %q/%q", movie.CoverURL, movie.ThumbURL)
	}
	if movie.TrailerURL == "" {
		t.Error("trailer 为空（详情里的 preview_video_url 就是它）")
	}
	if len(raw) == 0 {
		t.Error("raw 快照为空：raw_snapshot 要按源原样留一份")
	}
	if !json.Valid(raw) {
		t.Error("raw 不是合法 JSON")
	}
}

// TestDetailToleratesMissingFields 确认 null/缺失字段不炸：字段级回退归字段图，
// 传输层只负责把「没有」诚实地交出去。
func TestDetailToleratesMissingFields(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":1,"action":null,"message":null,"data":{"movie":{"id":"x","number":"ABP-1","actors":null}}}`))
	})

	result, err := c.Detail(context.Background(), "x")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	movie := result.Movie
	if movie.Number != "ABP-1" {
		t.Errorf("number = %q", movie.Number)
	}
	if len(movie.Actors) != 0 || movie.Score != "" {
		t.Errorf("缺失字段应当零值: %+v", movie)
	}
}

// TestDetailClassifiesEnvelopeFailure 确认详情失败走同一套分类：
// 坏 token → auth；非信封 → protocol。
func TestDetailClassifiesEnvelopeFailure(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{body: `{"success":0,"action":"JWTVerificationError","message":"x","data":null}`, want: CodeAuth},
		{body: `<html>oops</html>`, want: CodeProtocol},
	}
	for _, tc := range cases {
		c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(tc.body))
		})
		_, err := c.Detail(context.Background(), "ZY5eq")
		if err == nil {
			t.Fatalf("body %q 应当报错", tc.body)
		}
		if got := Classify(err); got != tc.want {
			t.Errorf("body %q 分类 = %q, want %q", tc.body, got, tc.want)
		}
	}
}

// TestDetailRejectsEmptyIDLocally 确认缺 movie_id 不发请求。
func TestDetailRejectsEmptyIDLocally(t *testing.T) {
	called := false
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
	})
	if _, err := c.Detail(context.Background(), " "); err == nil {
		t.Fatal("空 movie_id 应当报错")
	}
	if called {
		t.Error("空 movie_id 发了请求")
	}
}
