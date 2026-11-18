package appapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 测试缝：Client.ActressWorks / Client.Actress —— 女优订阅（#47）用到的两次上游读取。
//
// 取数（作品列表）匿名可读；女优详情（`/api/v1/actors/{id}`）同样匿名可读（§13）。
// 两条都用 httptest 夹具：上游没有 token 就打不通，测试不该依赖它。

// actressWorksBody 是 `/api/v1/movies/tags` 的女优形态回包（列表类端点，§5.1）。
const actressWorksBody = `{"movies":[
	{"id":"YwrO3B","number":"SSIS-001","release_date":"2026-11-14","title":"…","has_cnsub":true},
	{"id":"ZY5eq","number":"SSIS-002","release_date":"2026-10-01","title":"…"}
]}`

// TestActressWorksUsesTheActressMaskWithoutTheSingleSegment 钉住 ADR-0017 的裁决：
// 取数取**全集**，掩码就是 `0:a:{id}`（没有第 4 段的 `s`）——`s` 会在上游静默地排，
// 被排掉的作品连跳过表都进不去，「收下」那个出口对它们根本不可用。
//
// 排序也必须写明：列表默认序不是发行日，水位线的「见过即停」全靠 release/desc。
func TestActressWorksUsesTheActressMaskWithoutTheSingleSegment(t *testing.T) {
	var got *http.Request
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		_, _ = w.Write([]byte(envBody(actressWorksBody)))
	})

	works, err := c.ActressWorks(context.Background(), "EvkJ", 2)
	if err != nil {
		t.Fatalf("ActressWorks: %v", err)
	}

	if got.URL.Path != "/api/v1/movies/tags" {
		t.Errorf("path = %q, want /api/v1/movies/tags", got.URL.Path)
	}
	query := got.URL.Query()
	if mask := query.Get("filter_by"); mask != "0:a:EvkJ" {
		t.Errorf("filter_by = %q, want 0:a:EvkJ（不带第 4 段的 s）", mask)
	}
	if query.Get("sort_by") != "release" {
		t.Errorf("sort_by = %q, want release", query.Get("sort_by"))
	}
	if query.Get("order_by") != "desc" {
		t.Errorf("order_by = %q, want desc", query.Get("order_by"))
	}
	if query.Get("page") != "2" {
		t.Errorf("page = %q, want 2", query.Get("page"))
	}

	if len(works) != 2 {
		t.Fatalf("works = %d 项, want 2", len(works))
	}
	if works[0].MovieID != "YwrO3B" || works[0].Number != "SSIS-001" {
		t.Errorf("works[0] = %+v", works[0])
	}
	if want := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC); !works[0].ReleaseDate.Equal(want) {
		t.Errorf("release_date = %v, want %v（水位线靠它）", works[0].ReleaseDate, want)
	}
}

// TestActressWorksToleratesMissingReleaseDate 守住「没有发行日」的诚实：
// 上游给了空串或坏值时留零值（不进窗口、不推进水位线），而不是猜一个今天。
func TestActressWorksToleratesMissingReleaseDate(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(envBody(`{"movies":[{"id":"m1","number":"ABP-001","release_date":""},
			{"id":"m2","number":"ABP-002","release_date":"不是日期"}]}`)))
	})

	works, err := c.ActressWorks(context.Background(), "EvkJ", 1)
	if err != nil {
		t.Fatalf("ActressWorks: %v", err)
	}
	for _, work := range works {
		if !work.ReleaseDate.IsZero() {
			t.Errorf("%s 的 release_date = %v, want 零值", work.Number, work.ReleaseDate)
		}
	}
}

// TestActressWorksRequiresAnID 拦住「空 id 换来全站最新作品」这一类静默错误。
func TestActressWorksRequiresAnID(t *testing.T) {
	c, srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("空 id 不该打到上游")
	})
	_ = srv

	if _, err := c.ActressWorks(context.Background(), "  ", 1); err == nil {
		t.Fatal("空 id 应当报错")
	} else if _, ok := err.(*ValidationError); !ok {
		t.Errorf("err = %T, want *ValidationError", err)
	}
}

// actressDetailBody 是 `/api/v1/actors/{id}` 的回包形态（照 javdb_rss 的实测夹具：
// filter_tags 与 tags 在顶层，actor 里只有基本信息）。
const actressDetailBody = `{"actor":{"id":"EvkJ","name":"河北彩花","name_zht":"不該用這個","videos_count":229},
	"filter_tags":[{"id":"p","name":"可播放"}],"tags":[{"id":"10","name":"4小時以上作品","videos_count":117}]}`

// TestActressReadsNameAndVideosCount 钉住「贴 id → 名字」这一跳：
// 名字读 `name`（`name_zht` 实测恒为空，随 lang 变的是 name 本身）。
func TestActressReadsNameAndVideosCount(t *testing.T) {
	var got *http.Request
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		_, _ = w.Write([]byte(envBody(actressDetailBody)))
	})

	info, err := c.Actress(context.Background(), "EvkJ")
	if err != nil {
		t.Fatalf("Actress: %v", err)
	}

	if got.URL.Path != "/api/v1/actors/EvkJ" {
		t.Errorf("path = %q, want /api/v1/actors/EvkJ", got.URL.Path)
	}
	if info.ID != "EvkJ" {
		t.Errorf("id = %q, want 请求里那个 id", info.ID)
	}
	if info.Name != "河北彩花" {
		t.Errorf("name = %q, want 河北彩花（读 name，不读 name_zht）", info.Name)
	}
	if info.VideosCount != 229 {
		t.Errorf("videos_count = %d, want 229", info.VideosCount)
	}
}

// TestActressEscapesTheID 确认 id 进路径前被转义：它是用户贴进来的串，
// 一个斜杠就能把它变成另一条路径。
func TestActressEscapesTheID(t *testing.T) {
	var gotPath string
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(envBody(`{"actor":{"id":"a/b","name":"x"}}`)))
	})

	if _, err := c.Actress(context.Background(), "a/b"); err != nil {
		t.Fatalf("Actress: %v", err)
	}
	if !strings.Contains(gotPath, "a%2Fb") {
		t.Errorf("EscapedPath = %q, want 含 a%%2Fb", gotPath)
	}
}
