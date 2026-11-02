package organize_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/jellyfin"
	"github.com/2017fighting/oh-my-mdc/internal/organize"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
	"github.com/2017fighting/oh-my-mdc/internal/telegram"
)

// 这条用例是规格 §7 的端到端 demo：真实下载完成（qbt 回读）→ 目录成型 →
// tag 换装（真发 HTTP）→ 刷库与通知（旁路引擎真发 HTTP）。
//
// 只有上游是假的（httptest），其余全是真的：真 Postgres、真文件系统、
// 真的 qbittorrent / jellyfin / telegram 集成门面。

type recordedRequest struct {
	Method string
	Path   string
	Form   url.Values
	Body   []byte
}

type recorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (r *recorder) add(request recordedRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
}

func (r *recorder) to(method, path string) []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()

	matched := []recordedRequest{}
	for _, request := range r.requests {
		if request.Method == method && request.Path == path {
			matched = append(matched, request)
		}
	}
	return matched
}

// record 把请求记下来再交给 handler。
func record(rec *recorder, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		rec.add(recordedRequest{Method: r.Method, Path: r.URL.Path, Form: form, Body: body})
		handler(w, r)
	}
}

func TestEndToEndDownloadToFiledThenSideEffects(t *testing.T) {
	ctx := context.Background()

	// ---------------------------------------------------------- 文件系统
	mediaRoot := t.TempDir()
	downloads := t.TempDir()
	writeDownloads(t, downloads, map[string]string{
		"ABP-123-cd1.mkv":     "video-one",
		"ABP-123-cd2.mkv":     "video-two",
		"ABP-123-cd1.chs.srt": "sub",
	})

	// ---------------------------------------------------------- 假上游
	qbtRec, jellyfinRec, telegramRec := &recorder{}, &recorder{}, &recorder{}

	qbtServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/properties":
			record(qbtRec, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"hash":"` + hashA + `","name":"ABP-123","save_path":"` +
					downloads + `","content_path":"` + downloads + `","category":"mdc","tags":"ABP-123, ` +
					qbittorrent.TagInProgress + `"}`))
			})(w, r)
		case "/api/v2/torrents/info":
			record(qbtRec, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`[{"hash":"` + hashA + `","progress":1,"state":"uploading","content_path":"` + downloads + `"}]`))
			})(w, r)
		case "/api/v2/torrents/addTags", "/api/v2/torrents/removeTags":
			record(qbtRec, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qbtServer.Close)

	jellyfinServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(jellyfinRec, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })(w, r)
	}))
	t.Cleanup(jellyfinServer.Close)

	telegramServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(telegramRec, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		})(w, r)
	}))
	t.Cleanup(telegramServer.Close)

	// ---------------------------------------------------------- 库与设置
	database := dbtest.Migrated(t)
	if _, err := database.Pool().Exec(ctx,
		`INSERT INTO list_registry (list_id, purpose, name) VALUES ('wanted', 'want_watch', '想看清单')`,
	); err != nil {
		t.Fatalf("种清单登记: %v", err)
	}
	store := task.NewStore(database.Pool())
	outbox := sidefx.New(database.Pool())

	enabled := true
	settingsFake := &fakeSettings{
		snapshot: settings.Snapshot{
			Initialized: true,
			Sections: map[string]settings.SectionState{
				"app": {Configured: true, Data: map[string]any{"max_attempts": 3}},
			},
		},
		inputs: map[string]settings.TestInput{
			"qbittorrent": {Values: map[string]any{
				"url": qbtServer.URL, "username": "admin", "password": "pw",
				"category": "mdc", "download_root": downloads,
			}},
			"jellyfin": {Values: map[string]any{
				"url": jellyfinServer.URL, "api_key": "key", "media_root": mediaRoot,
			}},
			"telegram": {Enabled: enabled, Values: map[string]any{
				"bot_token": "bot-token", "chat_id": "42", "enabled": enabled,
				"notify_download_started": true, "notify_filed": true,
				"notify_processing_failed": true, "notify_download_stalled": true,
			}},
		},
	}

	// 一条已取齐元数据、等着整理的版本。
	sleeve := "https://pics.test/ssis001pl.jpg"
	images := &fakeImages{bodies: map[string][]byte{sleeve: imageBytes(t, 1200, 800, 220, "jpeg")}}
	seedProcessingVersion(t, store, movieID, number, hashA, "中文",
		metadata(time.Now().Add(-time.Hour).UTC(), []string{sleeve}, nil))

	// ---------------------------------------------------------- 整理入库（真 qbt 集成）
	qbtIntegration := qbittorrent.New(qbittorrent.Options{HTTP: qbtServer.Client()})
	organizer := organize.New(organize.Options{
		Store: store, Outbox: outbox, Settings: settingsFake,
		Qbt: qbtIntegration, Tags: qbtIntegration, Images: images,
		Owner: "e2e", Logger: discardLogger(),
	})

	progress, err := organizer.ProcessPending(ctx)
	if err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if progress.Filed != 1 {
		t.Fatalf("progress = %+v, want Filed=1", progress)
	}

	dir := mediaRoot + "/" + number
	for _, name := range []string{"ABP-123-中文-cd1.mkv", "ABP-123-中文-cd2.mkv", organize.NFOName, "poster.jpg", "fanart.jpg"} {
		mustExist(t, dir+"/"+name)
	}
	if version := versionOf(t, store, movieID, hashA); version.Status != task.StatusFinished {
		t.Fatalf("status = %q, want finished", version.Status)
	}

	// tag 换装真的发出去了：先补 finished、再摘 in-progress（顺序是崩溃安全的）。
	added := qbtRec.to(http.MethodPost, "/api/v2/torrents/addTags")
	removed := qbtRec.to(http.MethodPost, "/api/v2/torrents/removeTags")
	if len(added) != 1 || added[0].Form.Get("tags") != qbittorrent.TagFinished {
		t.Fatalf("addTags = %+v, want %s", added, qbittorrent.TagFinished)
	}
	if len(removed) != 1 || removed[0].Form.Get("tags") != qbittorrent.TagInProgress {
		t.Fatalf("removeTags = %+v, want %s", removed, qbittorrent.TagInProgress)
	}
	for _, request := range append(added, removed...) {
		if request.Form.Get("hashes") != hashA {
			t.Errorf("tag 换装打到了错的 infohash: %q", request.Form.Get("hashes"))
		}
	}

	// ---------------------------------------------------------- 副作用（真 jellyfin / telegram）
	engine := sidefx.NewEngine(sidefx.Options{
		Outbox:    outbox,
		Settings:  settingsFake,
		Refresher: jellyfin.New(jellyfin.Options{HTTP: jellyfinServer.Client()}),
		Notifier:  telegram.New(telegram.Options{Host: telegramServer.URL, HTTP: telegramServer.Client()}),
		Tasks:     store,
		Logger:    discardLogger(),
	})
	result, err := engine.RunOnce(ctx)
	if err != nil {
		t.Fatalf("sidefx RunOnce: %v", err)
	}
	if result.Done != 2 {
		t.Fatalf("sidefx = %+v, want 两条执行成功（刷新 + 通知）", result)
	}

	updates := jellyfinRec.to(http.MethodPost, "/Library/Media/Updated")
	if len(updates) != 1 {
		t.Fatalf("Jellyfin 刷新 = %d 次, want 1", len(updates))
	}
	var update struct {
		Updates []struct{ Path string } `json:"Updates"`
	}
	if err := json.Unmarshal(updates[0].Body, &update); err != nil {
		t.Fatalf("解刷新请求: %v", err)
	}
	if len(update.Updates) != 1 || update.Updates[0].Path != dir {
		t.Errorf("刷新目标 = %+v, want %s", update.Updates, dir)
	}

	sent := telegramRec.to(http.MethodPost, "/botbot-token/sendMessage")
	if len(sent) != 1 {
		t.Fatalf("Telegram = %d 次, want 1", len(sent))
	}
	if !strings.Contains(string(sent[0].Body), "已入库") || !strings.Contains(string(sent[0].Body), number) {
		t.Errorf("通知正文不像入库模板: %s", sent[0].Body)
	}
}

// versionOf 读一条版本（端到端用例不需要 fixture 的其它半边）。
func versionOf(t *testing.T, store *task.Store, movieID, infohash string) task.Version {
	t.Helper()
	row, err := store.Detail(context.Background(), movieID, 10)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	for _, version := range row.Versions {
		if version.Infohash == infohash {
			return version
		}
	}
	t.Fatalf("版本 %s 不存在", infohash)
	return task.Version{}
}

// discardLogger 是测试用的静音 logger。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
