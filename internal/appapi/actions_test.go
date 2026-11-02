package appapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
)

// TestRemoveFromListSendsRemoveForm 钉住清单项动作的传输形状（规格 §5.1）：
// form-encoded，动作在 `name` 里、作品在 `movie_id` 里，路径带清单 id。
func TestRemoveFromListSendsRemoveForm(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotType   string
		gotForm   url.Values
	)
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotType = r.Header.Get("content-type")
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		_, _ = w.Write([]byte(envBody(`null`)))
	})

	if err := c.RemoveFromList(context.Background(), "e8r1OM", "YwrO3B"); err != nil {
		t.Fatalf("RemoveFromList: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if want := "/api/v1/lists/e8r1OM/movie_actions"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotType != "application/x-www-form-urlencoded" {
		t.Errorf("content-type = %q, want form（JSON body 会被服务端拒）", gotType)
	}
	if got := gotForm.Get("name"); got != "remove" {
		t.Errorf("form name = %q, want 字面量 remove", got)
	}
	if got := gotForm.Get("movie_id"); got != "YwrO3B" {
		t.Errorf("form movie_id = %q, want YwrO3B", got)
	}
}

// TestMovieActionRejectsUnknownValue 是「未知 name 默认 remove」那个陷阱的防护：
// 白名单以外的值必须在**本地**就被拒绝，请求一个字节都不许发出去。
//
// 反例的后果是数据丢失：把一个想 add 的动作发成未知值，服务端会执行 remove。
func TestMovieActionRejectsUnknownValue(t *testing.T) {
	for _, value := range []string{"", "delete", "Remove", " remove", "remove;", "add/../remove"} {
		t.Run(value, func(t *testing.T) {
			calls := 0
			c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				_, _ = w.Write([]byte(envBody(`null`)))
			})

			err := c.SetListMembership(context.Background(), "e8r1OM", "YwrO3B", MovieAction(value))
			if !errors.Is(err, ErrUnknownMovieAction) {
				t.Fatalf("err = %v, want ErrUnknownMovieAction", err)
			}
			if calls != 0 {
				t.Errorf("发出去 %d 个请求，want 0（未知动作绝不带着上路）", calls)
			}
		})
	}
}

// TestMovieActionConstantsAreTheOnlyWireValues 把两个合法字面量钉在测试里：
// 服务端只认 add / remove，改动它们等于改动协议。
func TestMovieActionConstantsAreTheOnlyWireValues(t *testing.T) {
	if MovieActionAdd != "add" || MovieActionRemove != "remove" {
		t.Fatalf("动作字面量 = %q/%q, want add/remove", MovieActionAdd, MovieActionRemove)
	}
	if !MovieActionAdd.valid() || !MovieActionRemove.valid() {
		t.Fatal("两个常量都必须通过白名单")
	}
}

// TestRemoveFromListIsIdempotent 确认重复 remove 不算失败：实测服务端回 success:1，
// 所以「删成功了但没来得及落库」的下一次尝试会直接成功。
func TestRemoveFromListIsIdempotent(t *testing.T) {
	c, srv := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(envBody(`null`)))
	})
	_ = srv

	for i := 0; i < 3; i++ {
		if err := c.RemoveFromList(context.Background(), "e8r1OM", "YwrO3B"); err != nil {
			t.Fatalf("第 %d 次 RemoveFromList: %v", i+1, err)
		}
	}
}

// TestRemoveFromListNeedsIDs：空清单 id / 空 movie_id 是本地校验错（business），不发请求。
func TestRemoveFromListNeedsIDs(t *testing.T) {
	calls := 0
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(envBody(`null`)))
	})

	if err := c.RemoveFromList(context.Background(), " ", "YwrO3B"); err == nil {
		t.Fatal("空清单 id 应该报错")
	}
	if err := c.RemoveFromList(context.Background(), "e8r1OM", ""); err == nil {
		t.Fatal("空 movie_id 应该报错")
	}
	if calls != 0 {
		t.Errorf("发出去 %d 个请求，want 0", calls)
	}
}
