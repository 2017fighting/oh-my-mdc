package appapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// 测试缝：Client.CollectedActors —— 「从 App 收藏导入」那一次上游读取（#50，规格 §6.4/§8.4）。
//
// 端点 `/api/v1/users/collected_actors` **需要 token**（匿名实测 JWTVerificationError），
// 所以这里只用 httptest 夹具：真实上游打不通也不该让测试变红。

// collectedFixture 起一个按 page 返回收藏名单的上游。
//
// pages 是「第 N 页返回哪些行」，键从 1 开始；没写到的页返回空（＝到底）。
func collectedFixture(t *testing.T, pages map[int]string) *Client {
	t.Helper()
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/v1/users/collected_actors" {
			t.Errorf("path = %q, want /api/v1/users/collected_actors", got)
		}
		var page int
		_, _ = fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		rows, ok := pages[page]
		if !ok {
			rows = "[]"
		}
		_, _ = w.Write([]byte(envBody(`{"actors":` + rows + `}`)))
	})
	c.Token = "tk"
	return c
}

// TestCollectedActorsPagesUntilAnEmptyPageAndDedupesByID 钉住三件事：
//
//  1. 「到底了」只能靠空页判断（上游不给总数）；
//  2. 按演员 id 去重——翻页期间收藏会变，同一行可能落在两页上；没有 id 的行丢掉
//     （身份不明的东西不该进一份按身份去重的名单）；
//  3. gender 原样透出（0 = 女优、1 = 男优，实测；面板靠它分男女）。
func TestCollectedActorsPagesUntilAnEmptyPageAndDedupesByID(t *testing.T) {
	c := collectedFixture(t, map[int]string{
		1: `[{"id":"D2EdJ","name":"花守夏歩","videos_count":179,"gender":0},
		    {"id":"EvkJ","name":"河北彩花","videos_count":232,"gender":0}]`,
		// 第 2 页重复了 D2EdJ（翻页期间名单在动），并带回一位男优与一条没有 id 的行。
		2: `[{"id":"D2EdJ","name":"花守夏歩","videos_count":179,"gender":0},
		    {"id":"m1","name":"森林原人","videos_count":900,"gender":1},
		    {"id":"","name":"没有 id 的行","videos_count":1,"gender":0}]`,
	})

	got, err := c.CollectedActors(context.Background())
	if err != nil {
		t.Fatalf("CollectedActors: %v", err)
	}

	if len(got.Actresses) != 3 {
		t.Fatalf("actresses = %d 位, want 3（去重、丢掉没有 id 的行）: %+v", len(got.Actresses), got.Actresses)
	}
	if got.Truncated {
		t.Error("Truncated = true, want false（读到空页就是完整名单）")
	}
	first := got.Actresses[0]
	if first.ID != "D2EdJ" || first.Name != "花守夏歩" || first.VideosCount != 179 || first.Gender != 0 {
		t.Errorf("第一行 = %+v", first)
	}
	if male := got.Actresses[2]; male.ID != "m1" || male.Gender != 1 {
		t.Errorf("男优那行 = %+v, want gender 原样透出 1", male)
	}
}

// TestCollectedActorsCarriesTheToken 守住「需要 token」：anonymous 请求在上游一定被拒
// （JWTVerificationError），所以这里断言那条 Bearer 头真的发出去了。
func TestCollectedActorsCarriesTheToken(t *testing.T) {
	var auth string
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("authorization")
		_, _ = w.Write([]byte(envBody(`{"actors":[]}`)))
	})
	c.Token = "tk"

	if _, err := c.CollectedActors(context.Background()); err != nil {
		t.Fatalf("CollectedActors: %v", err)
	}
	if auth != "Bearer tk" {
		t.Errorf("authorization = %q, want Bearer tk", auth)
	}
}

// TestCollectedActorsReportsAnAuthError 确认匿名/坏 token 的结局归 auth（不是 network）：
// 上游回 JWTVerificationError，面板据此说「去重新导出 token」，而不是「等一会儿再试」。
func TestCollectedActorsReportsAnAuthError(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":0,"action":"JWTVerificationError","message":"未登录","data":null}`))
	})

	_, err := c.CollectedActors(context.Background())
	if err == nil {
		t.Fatal("匿名请求应当报错")
	}
	if !IsAuthError(err) {
		t.Errorf("err = %v (%T), want auth", err, err)
	}
}

// TestCollectedActorsProbesOneMorePageWhenTheCapIsFull 是截断探针：恰好读满上限
// （200 位）与「被上限切掉尾巴」是两件事，只有多探一页才分得清。
//
// 这条与 `internal/task/attention.go` 的「多取一条」是同一个判据的两种写法：假的截断
// 警告会让真响的那一次也没人信。
func TestCollectedActorsProbesOneMorePageWhenTheCapIsFull(t *testing.T) {
	exactly200 := map[int]string{}
	for page := 1; page <= maxCollectedPages; page++ {
		exactly200[page] = fullCollectedPage(page)
	}
	c := collectedFixture(t, exactly200)

	got, err := c.CollectedActors(context.Background())
	if err != nil {
		t.Fatalf("CollectedActors: %v", err)
	}
	if len(got.Actresses) != maxCollectedPages*collectedPageSize {
		t.Errorf("actresses = %d 位, want %d", len(got.Actresses), maxCollectedPages*collectedPageSize)
	}
	if got.Truncated {
		t.Error("Truncated = true, want false：恰好 200 位不等于「还有更多」")
	}
}

// TestCollectedActorsMarksTruncationBeyondTheCap 守住另一半：上限之外还有数据时
// 必须自报截断（面板据此说「只列到前 200 位」），而不是静默少给。
func TestCollectedActorsMarksTruncationBeyondTheCap(t *testing.T) {
	alwaysFull := map[int]string{}
	for page := 1; page <= maxCollectedPages+1; page++ {
		alwaysFull[page] = fullCollectedPage(page)
	}
	c := collectedFixture(t, alwaysFull)

	got, err := c.CollectedActors(context.Background())
	if err != nil {
		t.Fatalf("CollectedActors: %v", err)
	}
	if len(got.Actresses) != maxCollectedPages*collectedPageSize {
		t.Errorf("actresses = %d 位, want 只给到上限 %d 位", len(got.Actresses), maxCollectedPages*collectedPageSize)
	}
	if !got.Truncated {
		t.Error("Truncated = false, want true：上限之外还有数据")
	}
}

// fullCollectedPage 造一整页 10 位互不相同的演员（id 带页码，免得被去重吃掉）。
func fullCollectedPage(page int) string {
	rows := make([]string, 0, collectedPageSize)
	for i := range collectedPageSize {
		rows = append(rows, fmt.Sprintf(`{"id":"a%d-%d","name":"女优%d-%d","videos_count":%d,"gender":0}`,
			page, i, page, i, i))
	}
	return "[" + strings.Join(rows, ",") + "]"
}
