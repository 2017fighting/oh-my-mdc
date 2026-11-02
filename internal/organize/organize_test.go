package organize_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/organize"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：organize.Runner.ProcessPending —— 真 Postgres + 真文件系统 + 假 qbt/图片。
// 它覆盖入库全链路：分片布局、硬链、NFO、图片、tag 换装、副作用入箱、失败语义。

// ------------------------------------------------------------------ fakes

type fakeSettings struct {
	snapshot settings.Snapshot
	inputs   map[string]settings.TestInput
}

func (f *fakeSettings) Load(context.Context) (settings.Snapshot, error) { return f.snapshot, nil }

func (f *fakeSettings) Input(_ context.Context, section string) (settings.TestInput, error) {
	input, ok := f.inputs[section]
	if !ok {
		return settings.TestInput{}, settings.ErrNotConfigured
	}
	return input, nil
}

type fakeQbt struct {
	contentPath string
	found       bool
	lookupErr   error
	calls       int
}

func (f *fakeQbt) Lookup(context.Context, qbittorrent.Config, string) (qbittorrent.TorrentStatus, bool, error) {
	f.calls++
	if f.lookupErr != nil {
		return qbittorrent.TorrentStatus{}, false, f.lookupErr
	}
	if !f.found {
		return qbittorrent.TorrentStatus{}, false, nil
	}
	return qbittorrent.TorrentStatus{Progress: 1, State: "uploading", ContentPath: f.contentPath}, true, nil
}

type fakeTags struct {
	calls []string
	err   error
}

func (f *fakeTags) TransitionTag(_ context.Context, _ qbittorrent.Config, hash, from, to string) error {
	f.calls = append(f.calls, hash+":"+from+"→"+to)
	return f.err
}

type fakeImages struct {
	bodies map[string][]byte
	errs   map[string]error
	calls  []string
}

func (f *fakeImages) Get(_ context.Context, url string) ([]byte, error) {
	f.calls = append(f.calls, url)
	if err, ok := f.errs[url]; ok {
		return nil, err
	}
	body, ok := f.bodies[url]
	if !ok {
		return nil, fmt.Errorf("404 %s", url)
	}
	return body, nil
}

// ------------------------------------------------------------------ fixture

type fixture struct {
	database  *db.DB
	store     *task.Store
	outbox    *sidefx.Outbox
	settings  *fakeSettings
	qbt       *fakeQbt
	tags      *fakeTags
	images    *fakeImages
	runner    *organize.Runner
	mediaRoot string
	downloads string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	database := dbtest.Migrated(t)
	if _, err := database.Pool().Exec(context.Background(),
		`INSERT INTO list_registry (list_id, purpose, name) VALUES ('wanted', 'want_watch', '想看清单')`,
	); err != nil {
		t.Fatalf("种清单登记失败: %v", err)
	}

	mediaRoot := t.TempDir()
	downloads := t.TempDir()

	settingsFake := &fakeSettings{
		snapshot: settings.Snapshot{
			Initialized: true,
			Sections: map[string]settings.SectionState{
				"app": {Configured: true, Data: map[string]any{"max_attempts": 3}},
			},
		},
		inputs: map[string]settings.TestInput{
			"qbittorrent": {Values: map[string]any{"url": "http://qbt:8080", "category": "mdc", "download_root": downloads}},
			"jellyfin":    {Values: map[string]any{"media_root": mediaRoot}},
		},
	}

	qbt := &fakeQbt{contentPath: downloads, found: true}
	tags := &fakeTags{}
	images := &fakeImages{bodies: map[string][]byte{}, errs: map[string]error{}}
	store := task.NewStore(database.Pool())
	outbox := sidefx.New(database.Pool())

	runner := organize.New(organize.Options{
		Store:    store,
		Outbox:   outbox,
		Settings: settingsFake,
		Qbt:      qbt,
		Tags:     tags,
		Images:   images,
		Owner:    "test-owner",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return &fixture{
		database:  database,
		store:     store,
		outbox:    outbox,
		settings:  settingsFake,
		qbt:       qbt,
		tags:      tags,
		images:    images,
		runner:    runner,
		mediaRoot: mediaRoot,
		downloads: downloads,
	}
}

// addProcessingVersion 种一条已经取齐元数据、等着整理的版本（status=processing）。
func (f *fixture) addProcessingVersion(t *testing.T, movieID, number, infohash, label string, meta task.Metadata) {
	t.Helper()
	seedProcessingVersion(t, f.store, movieID, number, infohash, label, meta)
}

// seedProcessingVersion 是「种一条待整理版本」的单点实现（fixture 与端到端用例共用）。
func seedProcessingVersion(t *testing.T, store *task.Store, movieID, number, infohash, label string, meta task.Metadata) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.SaveCandidate(ctx, task.Candidate{
		MovieID: movieID, Number: number, ListID: "wanted", Infohash: infohash,
		Source: task.SourceJavDBMagnet, Delivery: task.DeliveryMagnet,
		Label: label, Payload: "magnet:?xt=urn:btih:" + infohash,
	}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	if _, err := store.Claim(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("Claim（加种）: %v", err)
	}
	if err := store.MarkAdded(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("MarkAdded: %v", err)
	}
	if _, err := store.Claim(ctx, infohash, "owner-scrape"); err != nil {
		t.Fatalf("Claim（取数）: %v", err)
	}
	if err := store.MarkProcessing(ctx, infohash, "owner-scrape"); err != nil {
		t.Fatalf("MarkProcessing: %v", err)
	}
	if err := store.SaveMetadata(ctx, movieID, task.Lease{Infohash: infohash, Owner: "owner-scrape"}, meta, nil); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}
}

func (f *fixture) version(t *testing.T, movieID, infohash string) task.Version {
	t.Helper()
	row, err := f.store.Detail(context.Background(), movieID, 50)
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

func (f *fixture) sideEffectKeys(t *testing.T, kind sidefx.Kind) []string {
	t.Helper()
	rows, err := f.database.Pool().Query(context.Background(),
		`SELECT target_key FROM side_effect WHERE kind = $1 ORDER BY id`, string(kind))
	if err != nil {
		t.Fatalf("读 side_effect: %v", err)
	}
	defer rows.Close()

	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("扫 side_effect: %v", err)
		}
		keys = append(keys, key)
	}
	return keys
}

func (f *fixture) events(t *testing.T, movieID string) []task.Event {
	t.Helper()
	events, err := f.store.Events(context.Background(), movieID, 50)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	return events
}

// ------------------------------------------------------------------ 素材

const (
	movieID = "YwrO3B"
	number  = "ABP-123"
	hashA   = "1111111111111111111111111111111111111111"
	hashB   = "2222222222222222222222222222222222222222"
)

// imageBytes 造一张纯色图（默认 jpeg）。
func imageBytes(t *testing.T, width, height int, shade uint8, format string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	var buffer bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&buffer, img)
	} else {
		err = jpeg.Encode(&buffer, img, nil)
	}
	if err != nil {
		t.Fatalf("encode image: %v", err)
	}
	return buffer.Bytes()
}

// metadata 造一份「已经取齐」的元数据：值 + 图片/演员的 provenance。
//
// sleeve 是 poster 与 fanart 共用的候选（规格 §7.1 图片行）；演员按源顺序写，
// 女优靠 gender=0 排在 NFO 前面（组内保序）。
func metadata(fetchedAt time.Time, sleeves, extras []string) task.Metadata {
	provenance := map[string]any{
		"poster":      entry("dmm", fetchedAt, sleeves),
		"fanart":      entry("dmm", fetchedAt, sleeves),
		"extrafanart": entry("dmm", fetchedAt, extras),
		"actors": entry("javdb", fetchedAt, []map[string]any{
			{"id": "m1", "name": "男优甲", "gender": 1, "avatar_url": "https://img.test/m.jpg"},
			{"id": "f1", "name": "女优乙", "gender": 0, "avatar_url": "https://img.test/f.jpg"},
			{"id": "u1", "name": "演员丙", "gender": nil, "avatar_url": ""},
		}),
	}
	encoded, err := json.Marshal(provenance)
	if err != nil {
		panic(err)
	}
	release := time.Date(2021, time.February, 19, 0, 0, 0, 0, time.UTC)
	runtime := 150
	rating := 8.86
	return task.Metadata{
		TitleJA:        "一ヶ月間の禁欲の果てに",
		TitleZH:        "一个月禁欲的尽头",
		Outline:        "S1スリム美女優の豪華共演",
		OutlineZH:      "S1 纤细女优豪华共演",
		ReleaseDate:    &release,
		RuntimeMinutes: &runtime,
		Studio:         "S1 NO.1 STYLE",
		Publisher:      "S1",
		Series:         "一ヶ月間の禁欲",
		Director:       "苺原",
		Rating:         &rating,
		Tags:           []string{"戏剧", "美乳"},
		Provenance:     encoded,
		RawSnapshot:    json.RawMessage(`{"javdb":{"movie":{}}}`),
	}
}

func entry(source string, at time.Time, raw any) map[string]any {
	return map[string]any{"source": source, "fetched_at": at, "raw_value": raw}
}

// writeDownloads 在下载目录里落一批文件（路径可以带子目录）。
func writeDownloads(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func sameInode(t *testing.T, a, b string) bool {
	t.Helper()
	aInfo, err := os.Stat(a)
	if err != nil {
		t.Fatalf("stat %s: %v", a, err)
	}
	bInfo, err := os.Stat(b)
	if err != nil {
		t.Fatalf("stat %s: %v", b, err)
	}
	return os.SameFile(aInfo, bInfo)
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s 不存在: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s 不该存在（err=%v）", path, err)
	}
}
