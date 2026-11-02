package appapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestMagnetsDecodesCandidates 钉住磁力接口的两种 data 包装与字段映射（规格 §5.1）。
func TestMagnetsDecodesCandidates(t *testing.T) {
	cases := map[string]string{
		"data.magnets": `{"magnets":[
			{"hash":"ABCDEF0123456789ABCDEF0123456789ABCDEF01","cnsub":true,"hd":true,"size":6144,"created_at":"2024-03-02"},
			{"hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":1024}
		]}`,
		"data 数组": `[
			{"hash":"ABCDEF0123456789ABCDEF0123456789ABCDEF01","cnsub":true,"hd":true,"size":6144,"created_at":"2024-03-02"},
			{"hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":1024}
		]`,
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			var gotPath string
			c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte(envBody(data)))
			})

			magnets, err := c.Magnets(context.Background(), "YwrO3B")
			if err != nil {
				t.Fatalf("Magnets: %v", err)
			}
			if gotPath != "/api/v1/movies/YwrO3B/magnets" {
				t.Errorf("path = %q, want /api/v1/movies/YwrO3B/magnets", gotPath)
			}
			if len(magnets) != 2 {
				t.Fatalf("len(magnets) = %d, want 2", len(magnets))
			}
			first := magnets[0]
			if first.Hash != "abcdef0123456789abcdef0123456789abcdef01" {
				t.Errorf("hash = %q, want 小写（统一成小写才谈得上去重）", first.Hash)
			}
			if !first.Cnsub || !first.HD || first.SizeMiB != 6144 {
				t.Errorf("first = %+v, want cnsub/hd/size 原样", first)
			}
			if !first.CreatedAt.Equal(time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)) {
				t.Errorf("created_at = %v, want 2024-03-02 UTC", first.CreatedAt)
			}
		})
	}
}

// TestMagnetsDropsCandidatesWithoutHash 确认没有 infohash 的候选被丢掉：
// 没有身份就没法去重、没法加种。
func TestMagnetsDropsCandidatesWithoutHash(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(envBody(`{"magnets":[{"hash":"  ","size":1},{"hash":"cccccccccccccccccccccccccccccccccccccccc"}]}`)))
	})

	magnets, err := c.Magnets(context.Background(), "YwrO3B")
	if err != nil {
		t.Fatalf("Magnets: %v", err)
	}
	if len(magnets) != 1 || magnets[0].Hash != "cccccccccccccccccccccccccccccccccccccccc" {
		t.Fatalf("magnets = %+v, want 只留带 hash 的那条", magnets)
	}
}

// TestMagnetsEmptyIsNotAnError 确认空候选是合法答案：poll 的处置是零行落库（P7-3）。
func TestMagnetsEmptyIsNotAnError(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(envBody(`{"magnets":[]}`)))
	})

	magnets, err := c.Magnets(context.Background(), "YwrO3B")
	if err != nil {
		t.Fatalf("Magnets: %v", err)
	}
	if len(magnets) != 0 {
		t.Errorf("magnets = %+v, want 空", magnets)
	}
}

// TestMagnetLinkCarriesHashAndNumber 钉住交付形态：磁力接口只给 hash，串由本项目补。
func TestMagnetLinkCarriesHashAndNumber(t *testing.T) {
	got := MagnetLink("ABCDEF0123456789ABCDEF0123456789ABCDEF01", "ABP-123")
	want := "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01&dn=ABP-123"
	if got != want {
		t.Errorf("MagnetLink = %q, want %q", got, want)
	}
}

// TestAllListItemsStopsAtEmptyPageAndDedupes 钉住 poll 的全量扫（规格 §5.1/§6.1）：
// 上游不给总页数，空页即止；翻页期间的重复 movie_id 只留一条。
func TestAllListItemsStopsAtEmptyPageAndDedupes(t *testing.T) {
	pages := map[string]string{
		"1": `{"movies":[{"id":"YwrO3B","number":"ABP-123"},{"id":"k4EVE4","number":"ABP-124"}]}`,
		"2": `{"movies":[{"id":"k4EVE4","number":"ABP-124"},{"id":"e8r1OM","number":"ABP-125"}]}`,
		"3": `{"movies":[]}`,
	}

	var requested []string
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/movies/tags" {
			t.Errorf("path = %q, want /api/v1/movies/tags", r.URL.Path)
		}
		if got := r.URL.Query().Get("filter_by"); got != "0:l:wanted" {
			t.Errorf("filter_by = %q, want 0:l:wanted", got)
		}
		page := r.URL.Query().Get("page")
		requested = append(requested, page)
		body, ok := pages[page]
		if !ok {
			t.Fatalf("不该翻到第 %s 页：空页即止", page)
		}
		_, _ = w.Write([]byte(envBody(body)))
	})

	items, err := c.AllListItems(context.Background(), "wanted")
	if err != nil {
		t.Fatalf("AllListItems: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %+v, want 3 条去重后的项", items)
	}
	if fmt.Sprint(requested) != "[1 2 3]" {
		t.Errorf("requested pages = %v, want 1 2 3", requested)
	}
	if items[2].MovieID != "e8r1OM" || items[2].Number != "ABP-125" {
		t.Errorf("items[2] = %+v, want e8r1OM/ABP-125", items[2])
	}
}

// TestAllListItemsKeepsFirstNumberForDuplicate 确认翻页期间重复出现的 movie_id
// 以第一次的番号为准（清单在翻页时被改动是常态）。
func TestAllListItemsKeepsFirstNumberForDuplicate(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "1":
			_, _ = w.Write([]byte(envBody(`{"movies":[{"id":"YwrO3B","number":"ABP-123"}]}`)))
		case "2":
			_, _ = w.Write([]byte(envBody(`{"movies":[{"id":"YwrO3B","number":"ABP-999"}]}`)))
		default:
			_, _ = w.Write([]byte(envBody(`{"movies":[]}`)))
		}
	})

	items, err := c.AllListItems(context.Background(), "wanted")
	if err != nil {
		t.Fatalf("AllListItems: %v", err)
	}
	if len(items) != 1 || items[0].Number != "ABP-123" {
		t.Fatalf("items = %+v, want 1 条 ABP-123（保留第一次出现的番号）", items)
	}
}
