package appapi

import (
	"context"
	"net/http"
	"testing"
)

// 测试缝：Client.TagVocabulary —— 标签词表 `GET /api/v2/tags?type=0`（规格 §6.4 筛子）。
//
// 筛子要的三条判据里两条按标签认，而标签 id **不硬编码**（ADR-0017）：id 得按名字去
// 这份词表里查。该端点 2026-11 起匿名一律 JWTVerificationError（§13），所以 token 在场
// 必须带上——生产有这个 token，测试用 httptest 夹具。

// tagsBody 是 `/api/v2/tags?type=0` 的负载形态（与女优详情里那张词表同形：id + name）。
const tagsBody = `{"tags":[
	{"id":"8","name":"精選綜合","videos_count":1234},
	{"id":"10","name":"4小時以上作品","videos_count":117},
	{"id":"11","name":"16小時以上作品","videos_count":48},
	{"id":"","name":"没有 id 的不认"},
	{"id":"12","name":"  "}
]}`

// TestTagsReadsTheVocabularyEndpoint 钉住请求形状：路径、type=0、token 随行（该端点要
// token，匿名一律 JWTVerificationError，§13）。
func TestTagsReadsTheVocabularyEndpoint(t *testing.T) {
	var got *http.Request
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		_, _ = w.Write([]byte(envBody(tagsBody)))
	})
	c.Token = "tk"

	if _, err := c.TagVocabulary(context.Background(), 0); err != nil {
		t.Fatalf("Tags: %v", err)
	}

	if got.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", got.Method)
	}
	if got.URL.Path != "/api/v2/tags" {
		t.Errorf("path = %q, want /api/v2/tags", got.URL.Path)
	}
	if got.URL.Query().Get("type") != "0" {
		t.Errorf("type = %q, want 0（片库号）", got.URL.Query().Get("type"))
	}
	if token := got.Header.Get("authorization"); token != "Bearer tk" {
		t.Errorf("authorization = %q, want Bearer tk", token)
	}
}

// TestTagsParsesTheVocabulary 钉住词表的取值面：id/name 都trim，缺 id 或空名字的条目
// 不认（拿着一条空 id 去比对详情，等于把每一部作品都判成合集）。
func TestTagsParsesTheVocabulary(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(envBody(tagsBody)))
	})

	tags, err := c.TagVocabulary(context.Background(), 0)
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}

	if len(tags) != 3 {
		t.Fatalf("tags = %+v, want 3 项（缺 id 与空名字的条目不算）", tags)
	}
	want := []Tag{
		{ID: "8", Name: "精選綜合"},
		{ID: "10", Name: "4小時以上作品"},
		{ID: "11", Name: "16小時以上作品"},
	}
	for i, tag := range want {
		if tags[i] != tag {
			t.Errorf("tags[%d] = %+v, want %+v", i, tags[i], tag)
		}
	}
}

// TestTagsAlsoTakesABareArray 钉住宽容的那一半：这个端点没被本仓实测过（§13 只记了它要
// token），所以两种信封内形态都认——`{tags:[…]}` 与裸数组。两种都解不出来才算协议错。
func TestTagsAlsoTakesABareArray(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(envBody(`[{"id":"8","name":"精選綜合"}]`)))
	})

	tags, err := c.TagVocabulary(context.Background(), 0)
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(tags) != 1 || tags[0] != (Tag{ID: "8", Name: "精選綜合"}) {
		t.Errorf("tags = %+v, want [{8 精選綜合}]", tags)
	}
}

// TestTagsRejectsAPayloadItCannotRead 把「上游改版」变成 protocol 错（红字要人查代码），
// 而不是一份空词表——空词表会让三条判据里的两条静默失效，而屏幕上什么都不会变。
func TestTagsRejectsAPayloadItCannotRead(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(envBody(`"不是词表"`)))
	})

	if _, err := c.TagVocabulary(context.Background(), 0); err == nil {
		t.Fatal("Tags = nil error, want 协议错")
	} else if Classify(err) != CodeProtocol {
		t.Errorf("Classify = %q, want %q", Classify(err), CodeProtocol)
	}
}

// TestTagsTakesAnEmptyVocabularyAsAnAnswer 钉住「空词表不是改版」：`{"tags":[]}` 是一份
// 合法的空（虽然现实里不该发生），归 protocol 会让整条标签判据看起来像上游改版。
func TestTagsTakesAnEmptyVocabularyAsAnAnswer(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(envBody(`{"tags":[]}`)))
	})

	tags, err := c.TagVocabulary(context.Background(), 0)
	if err != nil {
		t.Fatalf("Tags: %v（空词表是个空的答案，不是协议错）", err)
	}
	if len(tags) != 0 {
		t.Errorf("tags = %+v, want 空", tags)
	}
}
