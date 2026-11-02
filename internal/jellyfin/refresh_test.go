package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// mediaRootWith 建一个临时媒体库根目录，并按需在里面建好番号目录。
func mediaRootWith(t *testing.T, numbers ...string) string {
	t.Helper()

	root := t.TempDir()
	for _, number := range numbers {
		if err := os.MkdirAll(filepath.Join(root, number), 0o755); err != nil {
			t.Fatalf("建目录 %s: %v", number, err)
		}
	}
	return root
}

func configOf(url, root string) Config {
	return Config{URL: url, APIKey: "0123456789abcdef", MediaRoot: root}
}

// noContent 是 Jellyfin 这些端点的正常回执（204，空体）。
func noContent(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// TestRefreshCallsDirectoryEndpointWithExactShape 钉住 §5.5 的请求形状：
// POST /Library/Media/Updated，body 是 {Updates:[{Path,UpdateType:"Created"}]}，
// 目标是 {media_root}/{番号} 目录，鉴权头是 X-Emby-Token。
func TestRefreshCallsDirectoryEndpointWithExactShape(t *testing.T) {
	root := mediaRootWith(t, "KV-328")
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		"POST /Library/Media/Updated": noContent,
	})
	integration := New(Options{})

	result, err := integration.Refresh(context.Background(), configOf(fake.server.URL, root), "KV-328")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	wantDir := filepath.Join(root, "KV-328")
	if result.Scope != ScopeDirectory {
		t.Errorf("scope = %q, want directory", result.Scope)
	}
	if result.Directory != wantDir {
		t.Errorf("directory = %q, want %q", result.Directory, wantDir)
	}
	if !result.Accepted {
		t.Error("accepted = false, want true（204 就是已受理）")
	}

	requests := fake.recorded()
	if len(requests) != 1 {
		t.Fatalf("请求数 = %d, want 1: %+v", len(requests), requests)
	}
	request := requests[0]
	if request.Method != http.MethodPost || request.Path != "/Library/Media/Updated" {
		t.Errorf("请求 = %s %s, want POST /Library/Media/Updated", request.Method, request.Path)
	}
	if got := request.Header.Get("X-Emby-Token"); got != "0123456789abcdef" {
		t.Errorf("X-Emby-Token = %q, want api_key", got)
	}
	if got := request.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	want := `{"Updates":[{"Path":` + quote(wantDir) + `,"UpdateType":"Created"}]}`
	if got := string(request.Body); got != want {
		t.Errorf("body = %s\nwant %s", got, want)
	}
}

// TestRefreshSkipsWhenDirectoryIsNotOnDisk 钉住「目录在盘才调」：目录不在就一个请求都不发。
//
// Skipped 不是失败：这条版本可能还没入库（或布局不同），重试刷新也变不出目录。
func TestRefreshSkipsWhenDirectoryIsNotOnDisk(t *testing.T) {
	root := mediaRootWith(t)
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		"POST /Library/Media/Updated": noContent,
		"POST /Library/Refresh":       noContent,
	})
	integration := New(Options{})

	result, err := integration.Refresh(context.Background(), configOf(fake.server.URL, root), "KV-328")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if result.Scope != ScopeSkipped {
		t.Errorf("scope = %q, want skipped", result.Scope)
	}
	if result.Accepted {
		t.Error("accepted = true, want false（没调上游，谈不上受理）")
	}
	if requests := fake.recorded(); len(requests) != 0 {
		t.Errorf("请求数 = %d, want 0（目录不在盘就不许调）: %+v", len(requests), requests)
	}
}

// TestRefreshFallsBackToLibraryWhenDirectoryIsRejected 钉住 P11-2 的前半：
// 目录级被拒 → 降级全库刷新，降级成功仍算「已受理」。
func TestRefreshFallsBackToLibraryWhenDirectoryIsRejected(t *testing.T) {
	root := mediaRootWith(t, "KV-328")
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		"POST /Library/Media/Updated": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no such library", http.StatusBadRequest)
		},
		"POST /Library/Refresh": noContent,
	})
	integration := New(Options{})

	result, err := integration.Refresh(context.Background(), configOf(fake.server.URL, root), "KV-328")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if result.Scope != ScopeLibrary {
		t.Errorf("scope = %q, want library（目录级被拒后降到全库）", result.Scope)
	}
	if !result.Accepted {
		t.Error("accepted = false, want true（全库刷新回了 204）")
	}

	requests := fake.recorded()
	if len(requests) != 2 {
		t.Fatalf("请求数 = %d, want 2（先目录、再降级）: %+v", len(requests), requests)
	}
	if requests[0].Path != "/Library/Media/Updated" || requests[1].Path != "/Library/Refresh" {
		t.Errorf("调用顺序 = %s → %s, want Updated → Refresh", requests[0].Path, requests[1].Path)
	}
}

// TestRefreshDoesNotFallBackOnTransientServerError 确认 5xx 不降级：上游一时不对时，
// 顺带触发一次全库扫描只会放大问题；交给 side_effect 退避重试。
func TestRefreshDoesNotFallBackOnTransientServerError(t *testing.T) {
	// 5xx 与 429 都是上游一时的状态：降级会顺带触发一次全库扫描，只会放大问题。
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			root := mediaRootWith(t, "KV-328")
			fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
				"POST /Library/Media/Updated": func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "library scanner busy", status)
				},
				"POST /Library/Refresh": noContent,
			})
			integration := New(Options{})

			_, err := integration.Refresh(context.Background(), configOf(fake.server.URL, root), "KV-328")
			if err == nil {
				t.Fatalf("Refresh() error = nil, want HTTP %d 错", status)
			}
			if code := Classify(err); code != settings.CodeProtocol {
				t.Errorf("code = %q, want protocol", code)
			}
			if requests := fake.recorded(); len(requests) != 1 {
				t.Errorf("请求数 = %d, want 1（HTTP %d 不降级）: %+v", len(requests), status, requests)
			}
		})
	}
}

// TestRefreshDoesNotFallBackOnAuthRejection 确认鉴权失败不降级：同一个 key 换端点也一样被拒，
// 多打一次上游没有意义，直接报 auth 让用户去修 key。
func TestRefreshDoesNotFallBackOnAuthRejection(t *testing.T) {
	root := mediaRootWith(t, "KV-328")
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		"POST /Library/Media/Updated": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "Forbidden", http.StatusForbidden)
		},
		"POST /Library/Refresh": noContent,
	})
	integration := New(Options{})

	_, err := integration.Refresh(context.Background(), configOf(fake.server.URL, root), "KV-328")
	if err == nil {
		t.Fatal("Refresh() error = nil, want auth 错")
	}
	if code := Classify(err); code != settings.CodeAuth {
		t.Errorf("code = %q, want auth", code)
	}
	if requests := fake.recorded(); len(requests) != 1 {
		t.Errorf("请求数 = %d, want 1（鉴权错不降级）: %+v", len(requests), requests)
	}
}

// TestRefreshTreats204AsAcceptedNotAsEvidence 是「未知路径也回 204」的陷阱用例。
//
// Jellyfin 的 /Library/Media/Updated 不看路径是否存在，一律 204。所以客户端的 2xx
// 只能是「已受理」：它既不能证明目录被扫了，也不能用来判断路径对不对。消费者
// 不许把 Accepted 翻成「成功/已入库」事件（P11-2）。
func TestRefreshTreats204AsAcceptedNotAsEvidence(t *testing.T) {
	root := mediaRootWith(t, "KV-328")
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		// 兜底路由：这个 fake 对任何 POST 路径都回 204，正是 Jellyfin 的真实行为
		// （它从不校验路径是否存在，所以客户端无法从回执里看出自己拼错了目录）。
		"POST *": noContent,
	})
	integration := New(Options{})

	result, err := integration.Refresh(context.Background(), configOf(fake.server.URL, root), "KV-328")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if !result.Accepted {
		t.Fatal("accepted = false, want true：204 只说明收到了")
	}
	if result.Scope != ScopeDirectory {
		t.Errorf("scope = %q, want directory（2xx 结束这次调用，不降级）", result.Scope)
	}
}

// TestRefreshRejectsNumbersThatAreNotPathComponents 确认番号被当成目录名用之前先验形状：
// 带分隔符或 .. 的值不许拼进路径。
func TestRefreshRejectsNumbersThatAreNotPathComponents(t *testing.T) {
	root := mediaRootWith(t)
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		"POST /Library/Media/Updated": noContent,
	})

	for _, number := range []string{"", "  ", "../etc", "A/B", `A\B`, "/abs"} {
		_, err := New(Options{}).Refresh(context.Background(), configOf(fake.server.URL, root), number)
		if err == nil {
			t.Errorf("Refresh(%q) error = nil, want business 错", number)
			continue
		}
		if code := Classify(err); code != settings.CodeBusiness {
			t.Errorf("Refresh(%q) code = %q, want business", number, code)
		}
	}
	if requests := fake.recorded(); len(requests) != 0 {
		t.Errorf("请求数 = %d, want 0（形状不对就不许打上游）", len(requests))
	}
}

// TestRefreshRequiresMediaRoot 确认没填 media_root 时刷新是 business 错，而不是「跳过」：
// 配置缺项和「目录不在盘」是两件事，前者要人改设置。
func TestRefreshRequiresMediaRoot(t *testing.T) {
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		"POST /Library/Media/Updated": noContent,
	})

	cfg := Config{URL: fake.server.URL, APIKey: "key"} // MediaRoot 空
	_, err := New(Options{}).Refresh(context.Background(), cfg, "KV-328")
	if code := Classify(err); code != settings.CodeBusiness {
		t.Errorf("code = %q, want business: %v", code, err)
	}

	// 空 api_key 同样是本地缺项：不许把空 token 发出去换回一个 auth 错。
	cfg = Config{URL: fake.server.URL, MediaRoot: mediaRootWith(t)}
	_, err = New(Options{}).Refresh(context.Background(), cfg, "KV-328")
	if code := Classify(err); code != settings.CodeBusiness {
		t.Errorf("空 api_key: code = %q, want business: %v", code, err)
	}
}

// TestRefreshClassifiesConnectionFailure 确认连不上归 network（瞬时错，交给 side_effect 重试）。
func TestRefreshClassifiesConnectionFailure(t *testing.T) {
	root := mediaRootWith(t, "KV-328")
	fake := newFakeJellyfin(t, map[string]http.HandlerFunc{
		"POST /Library/Media/Updated": noContent,
	})
	url := fake.server.URL
	fake.server.Close()

	_, err := New(Options{}).Refresh(context.Background(), configOf(url, root), "KV-328")
	if code := Classify(err); code != settings.CodeNetwork {
		t.Errorf("code = %q, want network: %v", code, err)
	}
}

// TestShouldWarnAfterTwoFailures 钉住 P11-2 的「两次失败 = warning」：判据在这里，
// 重试本身归 side_effect（ADR-0005），消费者票接线。
func TestShouldWarnAfterTwoFailures(t *testing.T) {
	cases := map[int]bool{0: false, 1: false, 2: true, 3: true}
	for attempts, want := range cases {
		if got := ShouldWarn(attempts); got != want {
			t.Errorf("ShouldWarn(%d) = %v, want %v", attempts, got, want)
		}
	}
}

// TestMediaDirectoryJoinsMediaRootAndNumber 确认目录名就是番号，且不越出媒体库根目录。
func TestDirectoryJoinsMediaRootAndNumber(t *testing.T) {
	root := mediaRootWith(t)

	got, err := MediaDirectory(root, "KV-328")
	if err != nil {
		t.Fatalf("MediaDirectory() error = %v", err)
	}
	if want := filepath.Join(root, "KV-328"); got != want {
		t.Errorf("MediaDirectory() = %q, want %q", got, want)
	}
	if strings.Contains(got, "..") {
		t.Errorf("MediaDirectory() = %q, want 规范化的路径", got)
	}
}

// quote 把路径包成 JSON 字符串（拼期望值时用，避免手写转义）。
func quote(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
