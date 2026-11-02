package qbittorrent

import (
	"context"
	"net/http"
	"testing"
)

// testHash 是本文件里到处用的 infohash（40 位十六进制）。
const testHash = "0123456789abcdef0123456789abcdef01234567"

// fakeInfo 是 qbt torrents/info 的一行（只给本包读的那几个字段）。
func fakeInfo(hash, state string, progress float64, contentPath string) string {
	return `{"hash":"` + hash + `","name":"ABP-123","progress":` + formatFloat(progress) + `,
		"state":"` + state + `","content_path":"` + contentPath + `","save_path":"/media/downloads",
		"category":"mdc","tags":"mdc:in-progress,ABP-123"}`
}

func formatFloat(value float64) string {
	if value == 1 {
		return "1"
	}
	return "0.5"
}

// TestLookupMergesPropertiesAndInfo 钉住「回读权威值」：回调只带 infohash，
// progress/state 在 torrents/info，content_path 在 torrents/properties（规格 §5.4/§6.1）。
func TestLookupMergesPropertiesAndInfo(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"hash":"` + testHash + `","name":"ABP-123","save_path":"/media/downloads/completed/av","content_path":"/media/downloads/completed/av/ABP-123","category":"mdc","tags":"mdc:in-progress,ABP-123"}`))
		},
		"GET /api/v2/torrents/info": func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("hashes"); got != testHash {
				t.Errorf("hashes = %q, want %q", got, testHash)
			}
			_, _ = w.Write([]byte(`[` + fakeInfo(testHash, "uploading", 1, "") + `]`))
		},
	})

	status, found, err := New(Options{}).Lookup(context.Background(), addConfig(fake), testHash)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true（种子在 qbt 里）")
	}
	if status.Progress != 1 {
		t.Errorf("progress = %v, want 1", status.Progress)
	}
	if status.State != "uploading" {
		t.Errorf("state = %q, want uploading", status.State)
	}
	// content_path 优先取 properties（规格 §5.4 点名的权威端点）；info 没有就用 info 的。
	wantPath := "/media/downloads/completed/av/ABP-123"
	if status.ContentPath != wantPath {
		t.Errorf("content_path = %q, want %q", status.ContentPath, wantPath)
	}
}

// TestLookupFallsBackToInfoContentPath 确认 info 的 content_path 在 properties 缺省时补上。
func TestLookupFallsBackToInfoContentPath(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"hash":"` + testHash + `","tags":""}`))
		},
		"GET /api/v2/torrents/info": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[` + fakeInfo(testHash, "stalledUP", 1, "/from/info/ABP-123") + `]`))
		},
	})

	status, found, err := New(Options{}).Lookup(context.Background(), addConfig(fake), testHash)
	if err != nil || !found {
		t.Fatalf("Lookup: found=%v err=%v", found, err)
	}
	if status.ContentPath != "/from/info/ABP-123" {
		t.Errorf("content_path = %q, want info 兜底", status.ContentPath)
	}
}

// TestLookupReportsMissingTorrent 钉住「种子消失」的发现面：properties 404 就是不在。
func TestLookupReportsMissingTorrent(t *testing.T) {
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/properties": func(w http.ResponseWriter, _ *http.Request) {
			http.NotFound(w, nil)
		},
	})

	_, found, err := New(Options{}).Lookup(context.Background(), addConfig(fake), testHash)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if found {
		t.Error("found = true, want false（properties 404）")
	}
}

// TestTaggedListsInProgressTorrents 钉住对账的发现面：按项目 tag 扫描（规格 §6.2）。
func TestTaggedListsInProgressTorrents(t *testing.T) {
	otherHash := "fedcba9876543210fedcba9876543210fedcba98"
	fake := newFakeQbt(t, map[string]http.HandlerFunc{
		"GET /api/v2/torrents/info": func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("tag"); got != TagInProgress {
				t.Errorf("tag = %q, want %q", got, TagInProgress)
			}
			_, _ = w.Write([]byte(`[` + fakeInfo(testHash, "downloading", 0.5, "/a") + `,` +
				fakeInfo(otherHash, "uploading", 1, "/b") + `]`))
		},
	})

	statuses, err := New(Options{}).Tagged(context.Background(), addConfig(fake), TagInProgress)
	if err != nil {
		t.Fatalf("Tagged: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("Tagged = %d 条, want 2", len(statuses))
	}
	if statuses[0].Hash != testHash || statuses[1].Hash != otherHash {
		t.Errorf("hashes = %q/%q, want %q/%q", statuses[0].Hash, statuses[1].Hash, testHash, otherHash)
	}
	if statuses[1].State != "uploading" || statuses[1].Progress != 1 {
		t.Errorf("第二条 = %+v, want state=uploading progress=1", statuses[1])
	}
}
