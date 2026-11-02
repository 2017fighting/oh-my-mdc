package qbittorrent

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

const testMagnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=ABP-123"

// TestEnsureAddsMagnetWhenAbsent 钉住 P7-2：种子不在 qbt 里就加种，显式 savepath、
// 固定 category、番号 tag + mdc:in-progress，一次带齐。
func TestEnsureAddsMagnetWhenAbsent(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties": func(w http.ResponseWriter, _ *http.Request) {
			http.NotFound(w, nil)
		},
		"POST /api/v2/torrents/createCategory": func(w http.ResponseWriter, _ *http.Request) {},
		"POST /api/v2/torrents/add": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("Ok."))
		},
	})
	cfg := addConfig(fake)

	outcome, err := New(Options{}).Ensure(context.Background(), cfg, Torrent{
		Magnet: testMagnet,
		Number: "ABP-123",
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if outcome != Added {
		t.Errorf("outcome = %q, want %q", outcome, Added)
	}

	// 分类由加种的 createCategories=true 显式建：同一件事不摆两个机制。
	if created := fake.requestsTo(http.MethodPost, "/api/v2/torrents/createCategory"); len(created) != 0 {
		t.Errorf("新加种不该再先建一次分类: %v", created)
	}

	added := fake.requestsTo(http.MethodPost, "/api/v2/torrents/add")
	if len(added) != 1 {
		t.Fatalf("add 调用 %d 次, want 1", len(added))
	}
	// 磁力走 urlencoded 的 urls=（规格 §5.4：磁力 urls=，私种 multipart torrents=）。
	form := added[0].Form
	if form == nil {
		t.Fatalf("磁力加种应当是 urlencoded，实际 Content-Type = %q", added[0].Header.Get("Content-Type"))
	}
	if got := form.Get("urls"); got != testMagnet {
		t.Errorf("urls = %q, want %q", got, testMagnet)
	}
	if got := form.Get("savepath"); got != cfg.DownloadRoot {
		t.Errorf("savepath = %q, want %q", got, cfg.DownloadRoot)
	}
	if got := form.Get("tags"); got != "ABP-123,"+TagInProgress {
		t.Errorf("tags = %q, want 番号 + %s", got, TagInProgress)
	}
	if got := form.Get("createCategories"); got != "true" {
		t.Errorf("createCategories = %q, want true（显式建分类）", got)
	}
	if len(fake.requestsTo(http.MethodPost, "/api/v2/torrents/addTags")) != 0 {
		t.Error("新加种不该再补 tag：一次带齐")
	}
	if len(fake.requestsTo(http.MethodPost, "/api/v2/torrents/setCategory")) != 0 {
		t.Error("新加种不该再补分类：一次带齐")
	}
}

// TestEnsureAddsPrivateTorrent 确认私种走 multipart 的 torrents=，infohash 由调用方给。
func TestEnsureAddsPrivateTorrent(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties":      func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
		"POST /api/v2/torrents/createCategory": func(w http.ResponseWriter, _ *http.Request) {},
		"POST /api/v2/torrents/add":            func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("Ok.")) },
	})

	outcome, err := New(Options{}).Ensure(context.Background(), addConfig(fake), Torrent{
		Hash:   "fedcba9876543210fedcba9876543210fedcba98",
		Body:   []byte("d8:announce..."),
		Number: "ABP-124",
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if outcome != Added {
		t.Errorf("outcome = %q, want %q", outcome, Added)
	}

	form := fake.requestsTo(http.MethodPost, "/api/v2/torrents/add")[0].Multipart
	if len(form.File["torrents"]) != 1 {
		t.Errorf("torrents 文件 = %v, want 1 个", form.File["torrents"])
	}
	if len(form.Value["urls"]) != 0 {
		t.Error("私种路径不该带 urls=")
	}
}

// TestEnsureAdoptsUntaggedTorrent 钉住 P8-4：同 infohash 在 qbt 里但没有项目 tag →
// 收编（补 tag + category），savepath 不动，版本继续走流程。
func TestEnsureAdoptsUntaggedTorrent(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"hash":"0123456789abcdef0123456789abcdef01234567","name":"ABP-123","save_path":"/someone/elses/path","category":"","tags":"手动"}`))
		},
		"POST /api/v2/torrents/createCategory": func(w http.ResponseWriter, _ *http.Request) {},
		"POST /api/v2/torrents/setCategory":    func(w http.ResponseWriter, _ *http.Request) {},
		"POST /api/v2/torrents/addTags":        func(w http.ResponseWriter, _ *http.Request) {},
	})

	outcome, err := New(Options{}).Ensure(context.Background(), addConfig(fake), Torrent{
		Magnet: testMagnet,
		Number: "ABP-123",
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if outcome != Adopted {
		t.Errorf("outcome = %q, want %q", outcome, Adopted)
	}

	if len(fake.requestsTo(http.MethodPost, "/api/v2/torrents/add")) != 0 {
		t.Error("已存在的种子不该再加种（savepath 不能被动）")
	}
	tags := fake.requestsTo(http.MethodPost, "/api/v2/torrents/addTags")
	if len(tags) != 1 {
		t.Fatalf("addTags 调用 %d 次, want 1", len(tags))
	}
	if got := tags[0].Form.Get("hashes"); got != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("addTags hashes = %q", got)
	}
	if got := tags[0].Form.Get("tags"); got != "ABP-123,"+TagInProgress {
		t.Errorf("addTags tags = %q, want 番号 + %s", got, TagInProgress)
	}

	category := fake.requestsTo(http.MethodPost, "/api/v2/torrents/setCategory")
	if len(category) != 1 || category[0].Form.Get("category") != "mdc" {
		t.Fatalf("setCategory = %v, want 一次 mdc", category)
	}
	if got := category[0].Form.Get("hashes"); got != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("setCategory hashes = %q", got)
	}
}

// TestEnsureLeavesTaggedTorrentAlone 确认「在且有项目 tag」→ 直接推进，零写入。
func TestEnsureLeavesTaggedTorrentAlone(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"hash":"0123456789abcdef0123456789abcdef01234567","category":"mdc","tags":"ABP-123, ` + TagFinished + `"}`))
		},
	})

	outcome, err := New(Options{}).Ensure(context.Background(), addConfig(fake), Torrent{Magnet: testMagnet, Number: "ABP-123"})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if outcome != AlreadyTracked {
		t.Errorf("outcome = %q, want %q", outcome, AlreadyTracked)
	}
	for _, path := range []string{"/api/v2/torrents/add", "/api/v2/torrents/addTags", "/api/v2/torrents/setCategory", "/api/v2/torrents/createCategory"} {
		if requests := fake.requestsTo(http.MethodPost, path); len(requests) != 0 {
			t.Errorf("%s 被调用了 %d 次, want 0（已有项目 tag 就什么都不写）", path, len(requests))
		}
	}
}

// TestEnsureRepairsCategoryOnTaggedTorrent 钉住收编可重入：上次补 tag 成功、补分类
// 失败的话，重入时种子已带项目 tag，但分类还得补齐才算收编完。
func TestEnsureRepairsCategoryOnTaggedTorrent(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"hash":"0123456789abcdef0123456789abcdef01234567","category":"","tags":"ABP-123,` + TagInProgress + `"}`))
		},
		"POST /api/v2/torrents/setCategory": func(w http.ResponseWriter, _ *http.Request) {},
	})

	outcome, err := New(Options{}).Ensure(context.Background(), addConfig(fake), Torrent{Magnet: testMagnet, Number: "ABP-123"})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if outcome != AlreadyTracked {
		t.Errorf("outcome = %q, want %q", outcome, AlreadyTracked)
	}
	repair := fake.requestsTo(http.MethodPost, "/api/v2/torrents/setCategory")
	if len(repair) != 1 || repair[0].Form.Get("category") != "mdc" {
		t.Fatalf("setCategory = %v, want 一次补齐 mdc", repair)
	}
	if len(fake.requestsTo(http.MethodPost, "/api/v2/torrents/addTags")) != 0 {
		t.Error("tag 已经在，不该重复补")
	}
}

// TestEnsureTreatsAddRaceAsSuccess 钉住「竞态『已存在』→ 视为成功」：
// add 回 Fails.，但再查 properties 种子已在 → 收编并当成功。
func TestEnsureTreatsAddRaceAsSuccess(t *testing.T) {
	lookups := 0
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties": func(w http.ResponseWriter, _ *http.Request) {
			lookups++
			if lookups == 1 {
				http.NotFound(w, nil)
				return
			}
			_, _ = w.Write([]byte(`{"hash":"0123456789abcdef0123456789abcdef01234567","save_path":"/other","tags":""}`))
		},
		"POST /api/v2/torrents/createCategory": func(w http.ResponseWriter, _ *http.Request) {},
		"POST /api/v2/torrents/setCategory":    func(w http.ResponseWriter, _ *http.Request) {},
		"POST /api/v2/torrents/addTags":        func(w http.ResponseWriter, _ *http.Request) {},
		"POST /api/v2/torrents/add": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("Fails."))
		},
	})

	outcome, err := New(Options{}).Ensure(context.Background(), addConfig(fake), Torrent{Magnet: testMagnet, Number: "ABP-123"})
	if err != nil {
		t.Fatalf("Ensure: %v（竞态已存在应当视为成功）", err)
	}
	if outcome != Adopted {
		t.Errorf("outcome = %q, want %q", outcome, Adopted)
	}
}

// TestEnsureReportsAddFailureWhenTorrentStillAbsent 确认加种真失败时报出来。
func TestEnsureReportsAddFailureWhenTorrentStillAbsent(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties":      func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
		"POST /api/v2/torrents/createCategory": func(w http.ResponseWriter, _ *http.Request) {},
		"POST /api/v2/torrents/add":            func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("Fails.")) },
	})

	if _, err := New(Options{}).Ensure(context.Background(), addConfig(fake), Torrent{Magnet: testMagnet, Number: "ABP-123"}); err == nil {
		t.Fatal("加种失败且种子仍不在，应当报错")
	}
}

// TestEnsureDerivesInfohashFromMagnet 确认调用方可以只给磁力串：infohash 从 xt 里取。
func TestEnsureDerivesInfohashFromMagnet(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties": func(w http.ResponseWriter, _ *http.Request) {
			http.NotFound(w, nil)
		},
		"POST /api/v2/torrents/createCategory": func(w http.ResponseWriter, _ *http.Request) {},
		"POST /api/v2/torrents/add":            func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("Ok.")) },
	})

	if _, err := New(Options{}).Ensure(context.Background(), addConfig(fake), Torrent{Magnet: testMagnet, Number: "ABP-123"}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	lookup := fake.requestsTo(http.MethodGet, "/api/v2/torrents/properties")
	if len(lookup) != 1 {
		t.Fatalf("properties 查询 %d 次, want 1", len(lookup))
	}
	if got := lookup[0].Query.Get("hash"); got != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("hash = %q, want 磁力里的 infohash", got)
	}
}

// TestEnsureRequiresDownloadRoot 确认没有下载根目录时本地就拒（savepath 必须显式）。
func TestEnsureRequiresDownloadRoot(t *testing.T) {
	fake := newFakeQbt(t, nil)
	cfg := addConfig(fake)
	cfg.DownloadRoot = ""

	_, err := New(Options{}).Ensure(context.Background(), cfg, Torrent{Magnet: testMagnet, Number: "ABP-123"})
	if err == nil {
		t.Fatal("没有 download_root 应当报错")
	}
	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if len(fake.requests) != 0 {
		t.Errorf("校验失败不该打上游，实际打了 %d 次", len(fake.requests))
	}
}

// TestEnsureRequiresOneDeliveryForm 确认磁力与私种二选一。
func TestEnsureRequiresOneDeliveryForm(t *testing.T) {
	fake := newFakeQbt(t, nil)

	for name, torrent := range map[string]Torrent{
		"都没有":  {Hash: "0123456789abcdef0123456789abcdef01234567", Number: "ABP-123"},
		"两个都给": {Hash: "0123456789abcdef0123456789abcdef01234567", Number: "ABP-123", Magnet: testMagnet, Body: []byte("x")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(Options{}).Ensure(context.Background(), addConfig(fake), torrent); err == nil {
				t.Fatal("交付形态不合法应当报错")
			}
		})
	}
}

// TestTransitionTagSwapsProjectTags 备好 tag 换装：入库成功时 in-progress → finished。
func TestTransitionTagSwapsProjectTags(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"POST /api/v2/torrents/addTags":    func(w http.ResponseWriter, _ *http.Request) {},
		"POST /api/v2/torrents/removeTags": func(w http.ResponseWriter, _ *http.Request) {},
	})

	hash := "0123456789abcdef0123456789abcdef01234567"
	if err := New(Options{}).TransitionTag(context.Background(), addConfig(fake), hash, TagInProgress, TagFinished); err != nil {
		t.Fatalf("TransitionTag: %v", err)
	}

	added := fake.requestsTo(http.MethodPost, "/api/v2/torrents/addTags")
	if len(added) != 1 || added[0].Form.Get("tags") != TagFinished {
		t.Fatalf("addTags = %v, want 先补 %s", added, TagFinished)
	}
	removed := fake.requestsTo(http.MethodPost, "/api/v2/torrents/removeTags")
	if len(removed) != 1 || removed[0].Form.Get("tags") != TagInProgress {
		t.Fatalf("removeTags = %v, want 再摘 %s", removed, TagInProgress)
	}
}

func addConfig(fake *fakeQbt) Config {
	cfg := fake.config()
	cfg.DownloadRoot = "/media/downloads/completed/av"
	cfg.WebhookBaseURL = "http://oh-my-mdc.oh-my-mdc.svc:8080"
	return cfg
}
