package metrics_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/dmm"
	"github.com/2017fighting/oh-my-mdc/internal/javdb"
	"github.com/2017fighting/oh-my-mdc/internal/jellyfin"
	"github.com/2017fighting/oh-my-mdc/internal/metrics"
	"github.com/2017fighting/oh-my-mdc/internal/mteam"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/telegram"
	"github.com/2017fighting/oh-my-mdc/internal/translate"
)

// fakeUpstream 起一个只回一种响应的假上游。
func fakeUpstream(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// TestPermanentClassificationPerUpstream 逐一核验规格 §9 点名的永久错分类：
//
//	javdb  JWTVerificationError（auth 信封）
//	mteam  HTTP 401 与 code!="0" 的凭据类信封
//	telegram 400 / 401 / 403
//	qbittorrent 登录 403、jellyfin 401、openai 401
//
// 每个子测试都走**真集成门面 + 假上游**：分类在客户端里，这条路径正是 sidefx
// 与告警看到的同一条。同时钉住「七上游全部有数」——一个 upstream 标签都不能漏。
func TestPermanentClassificationPerUpstream(t *testing.T) {
	cases := []struct {
		name     string
		upstream string
		server   func(t *testing.T) *httptest.Server
		drive    func(t *testing.T, m *metrics.Registry, server *httptest.Server) error
		result   metrics.Result
	}{
		{
			name:     "javdb JWTVerificationError",
			upstream: metrics.UpstreamJavDB,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusOK,
					`{"success":0,"action":"JWTVerificationError","message":"token 无效","data":null}`)
			},
			drive: func(t *testing.T, m *metrics.Registry, server *httptest.Server) error {
				integration := javdb.New(javdb.Options{Host: server.URL, Metrics: m})
				_, err := integration.Lists(context.Background(), "token")
				return err
			},
			result: metrics.ResultPermanent,
		},
		{
			name:     "javdb 成功",
			upstream: metrics.UpstreamJavDB,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusOK, `{"success":1,"action":null,"message":"","data":{"lists":[]}}`)
			},
			drive: func(t *testing.T, m *metrics.Registry, server *httptest.Server) error {
				integration := javdb.New(javdb.Options{Host: server.URL, Metrics: m})
				_, err := integration.Lists(context.Background(), "token")
				return err
			},
			result: metrics.ResultOK,
		},
		{
			name:     "javdb HTTP 401",
			upstream: metrics.UpstreamJavDB,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusUnauthorized, "<html>Unauthorized</html>")
			},
			drive: func(t *testing.T, m *metrics.Registry, server *httptest.Server) error {
				integration := javdb.New(javdb.Options{Host: server.URL, Metrics: m})
				_, err := integration.Lists(context.Background(), "token")
				return err
			},
			result: metrics.ResultPermanent,
		},
		{
			name:     "javdb HTTP 403",
			upstream: metrics.UpstreamJavDB,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusForbidden, "<html>Forbidden</html>")
			},
			drive: func(t *testing.T, m *metrics.Registry, server *httptest.Server) error {
				integration := javdb.New(javdb.Options{Host: server.URL, Metrics: m})
				_, err := integration.Lists(context.Background(), "token")
				return err
			},
			result: metrics.ResultPermanent,
		},
		{
			name:     "mteam HTTP 401",
			upstream: metrics.UpstreamMTeam,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusUnauthorized, `{"code":"401","message":"unauthorized"}`)
			},
			drive: func(t *testing.T, m *metrics.Registry, server *httptest.Server) error {
				integration := mteam.New(mteam.Options{Host: server.URL, Metrics: m})
				_, err := integration.Resolve(context.Background(), "api-key", "SSIS-001")
				return err
			},
			result: metrics.ResultPermanent,
		},
		{
			name:     "mteam 信封鉴权 code",
			upstream: metrics.UpstreamMTeam,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusOK, `{"code":"1","message":"key無效","data":null}`)
			},
			drive: func(t *testing.T, m *metrics.Registry, server *httptest.Server) error {
				integration := mteam.New(mteam.Options{Host: server.URL, Metrics: m})
				_, err := integration.Resolve(context.Background(), "api-key", "SSIS-001")
				return err
			},
			result: metrics.ResultPermanent,
		},
		{
			name:     "telegram 400",
			upstream: metrics.UpstreamTelegram,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"chat not found"}`)
			},
			drive:  driveTelegram,
			result: metrics.ResultPermanent,
		},
		{
			name:     "telegram 401",
			upstream: metrics.UpstreamTelegram,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusUnauthorized, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
			},
			drive:  driveTelegram,
			result: metrics.ResultPermanent,
		},
		{
			name:     "telegram 403",
			upstream: metrics.UpstreamTelegram,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusForbidden, `{"ok":false,"error_code":403,"description":"bot was kicked"}`)
			},
			drive:  driveTelegram,
			result: metrics.ResultPermanent,
		},
		{
			name:     "qbittorrent 登录 403",
			upstream: metrics.UpstreamQbittorrent,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusForbidden, "Forbidden")
			},
			drive: func(t *testing.T, m *metrics.Registry, server *httptest.Server) error {
				integration := qbittorrent.New(qbittorrent.Options{Metrics: m})
				result := integration.Test(context.Background(), settings.Section{Name: "qbittorrent"},
					settings.TestInput{Values: map[string]any{
						"url": server.URL, "username": "u", "password": "p",
					}})
				if result.OK {
					return nil
				}
				return &testFailure{code: result.Code}
			},
			result: metrics.ResultPermanent,
		},
		{
			name:     "jellyfin 401",
			upstream: metrics.UpstreamJellyfin,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusUnauthorized, `{"error":"bad key"}`)
			},
			drive: func(t *testing.T, m *metrics.Registry, server *httptest.Server) error {
				integration := jellyfin.New(jellyfin.Options{Metrics: m})
				result := integration.Test(context.Background(), settings.Section{Name: "jellyfin"},
					settings.TestInput{Values: map[string]any{
						"url": server.URL, "api_key": "bad", "media_root": t.TempDir(),
					}})
				if result.OK {
					return nil
				}
				return &testFailure{code: result.Code}
			},
			result: metrics.ResultPermanent,
		},
		{
			name:     "openai 401",
			upstream: metrics.UpstreamOpenAI,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusUnauthorized, `{"error":{"message":"invalid api key"}}`)
			},
			drive: func(t *testing.T, m *metrics.Registry, server *httptest.Server) error {
				integration := translate.New(translate.Options{Metrics: m})
				_, err := integration.Translate(context.Background(), translate.Config{
					BaseURL: server.URL, APIKey: "bad", Model: "gpt-4o-mini", Enabled: true,
				}, translate.Text{Title: "日文标题"})
				return err
			},
			result: metrics.ResultPermanent,
		},
		{
			name:     "dmm 5xx",
			upstream: metrics.UpstreamDMM,
			server: func(t *testing.T) *httptest.Server {
				return fakeUpstream(t, http.StatusInternalServerError, `{"errors":[{"message":"boom"}]}`)
			},
			drive: func(t *testing.T, m *metrics.Registry, server *httptest.Server) error {
				client := dmm.New(dmm.Options{Host: server.URL, Metrics: m})
				_, err := client.Lookup(context.Background(), "SSIS-001")
				return err
			},
			result: metrics.ResultTransient,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := metrics.New(nil, nil, nil)
			server := tc.server(t)

			err := tc.drive(t, m, server)
			switch tc.result {
			case metrics.ResultOK:
				if err != nil {
					t.Fatalf("假上游回成功，集成却报错: %v", err)
				}
			default:
				if err == nil {
					t.Fatal("假上游回失败，集成却没报错")
				}
			}

			if got := m.Count(tc.upstream, tc.result); got != 1 {
				t.Errorf("%s 的 %s 计数 = %d, want 1", tc.upstream, tc.result, got)
			}
			var total uint64
			for _, result := range metrics.Results {
				total += m.Count(tc.upstream, result)
			}
			if total != 1 {
				t.Errorf("%s 的请求总数 = %d, want 1（一次调用记一条）", tc.upstream, total)
			}
		})
	}
}

// TestEveryUpstreamHasAFamily 钉住七上游的标签取值：增一个上游不跟指标走，这里会红。
func TestEveryUpstreamHasAFamily(t *testing.T) {
	want := []string{"javdb", "mteam", "dmm", "qbittorrent", "jellyfin", "telegram", "openai"}
	if len(metrics.Upstreams) != len(want) {
		t.Fatalf("Upstreams = %v, want %v", metrics.Upstreams, want)
	}
	for i, name := range want {
		if metrics.Upstreams[i] != name {
			t.Errorf("Upstreams[%d] = %q, want %q", i, metrics.Upstreams[i], name)
		}
	}
}

func driveTelegram(t *testing.T, m *metrics.Registry, server *httptest.Server) error {
	t.Helper()
	integration := telegram.New(telegram.Options{Host: server.URL, Metrics: m})
	return integration.Notify(context.Background(), telegram.Config{
		BotToken: "123:abc", ChatID: "42", Enabled: true,
	}, telegram.EventFiled, map[string]any{
		"number": "ABP-123", "title": "标题", "label": "中文",
		"source": "javdb_magnet", "size_mib": 1024,
	})
}

// testFailure 让「测试连接」的判词能当 error 用（drive 的返回值只看 nil/非 nil）。
type testFailure struct {
	code settings.TestCode
}

func (f *testFailure) Error() string { return "测试连接失败: " + string(f.code) }
