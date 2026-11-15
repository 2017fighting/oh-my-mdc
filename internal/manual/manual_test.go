package manual_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/manual"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：manual.Registrar.Register —— 真 Postgres + 假 JavDB 详情/qbt。
// 它覆盖整条登记流水线：解析、校验、番号预检、先落库再加种、幂等、入箱、deferred。

// ------------------------------------------------------------------ fakes

type fakeSettings struct {
	snapshot settings.Snapshot
	inputs   map[string]settings.TestInput
}

func (f *fakeSettings) Load(context.Context) (settings.Snapshot, error) {
	return f.snapshot, nil
}

func (f *fakeSettings) Input(_ context.Context, section string) (settings.TestInput, error) {
	return f.inputs[section], nil
}

type fakeSource struct {
	details map[string]appapi.DetailResult
	err     error
	calls   []string
}

func (f *fakeSource) Detail(_ context.Context, _, movieID string) (appapi.DetailResult, error) {
	f.calls = append(f.calls, movieID)
	if f.err != nil {
		return appapi.DetailResult{}, f.err
	}
	return f.details[movieID], nil
}

type fakeAdder struct {
	calls   []qbittorrent.Torrent
	outcome qbittorrent.Outcome
	err     error
}

func (f *fakeAdder) Ensure(_ context.Context, _ qbittorrent.Config, torrent qbittorrent.Torrent) (qbittorrent.Outcome, error) {
	f.calls = append(f.calls, torrent)
	if f.err != nil {
		return "", f.err
	}
	if f.outcome == "" {
		return qbittorrent.Added, nil
	}
	return f.outcome, nil
}

type fakeLooker struct {
	status qbittorrent.TorrentStatus
	found  bool
}

func (f *fakeLooker) Lookup(_ context.Context, _ qbittorrent.Config, _ string) (qbittorrent.TorrentStatus, bool, error) {
	return f.status, f.found, nil
}

// ------------------------------------------------------------------ fixture

type fixture struct {
	database *db.DB
	store    *task.Store
	source   *fakeSource
	adder    *fakeAdder
	looker   *fakeLooker
	settings *fakeSettings
	outbox   *sidefx.Outbox
	r        *manual.Registrar
}

const (
	movieID   = "YwrO3B"
	otherID   = "k4EVE4"
	number    = "ABP-123"
	otherHash = "2222222222222222222222222222222222222222"
)

var magnet = appapi.MagnetLink(plainHash, number)

const plainHash = "1111111111111111111111111111111111111111"

func newFixture(t *testing.T) *fixture {
	t.Helper()

	database := dbtest.Migrated(t)
	store := task.NewStore(database.Pool())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	settingsFake := &fakeSettings{
		snapshot: settings.Snapshot{
			Initialized: true,
			Sections: map[string]settings.SectionState{
				"app": {Configured: true, Data: map[string]any{}},
			},
		},
		inputs: map[string]settings.TestInput{
			"javdb":       {Values: map[string]any{"token": "tk"}},
			"qbittorrent": {Values: map[string]any{"url": "http://qbt:8080", "category": "mdc", "download_root": "/media/downloads"}},
		},
	}
	source := &fakeSource{details: map[string]appapi.DetailResult{
		movieID: {Movie: appapi.MovieDetail{ID: movieID, Number: number}},
		otherID: {Movie: appapi.MovieDetail{ID: otherID, Number: "MIDV-999"}},
	}}
	adder := &fakeAdder{}
	looker := &fakeLooker{found: true, status: qbittorrent.TorrentStatus{SizeBytes: 100 << 20}}
	outbox := sidefx.New(database.Pool())

	r := manual.New(manual.Options{
		Store:    store,
		Outbox:   outbox,
		Settings: settingsFake,
		Source:   source,
		Adder:    adder,
		Looker:   looker,
		Owner:    "test-owner",
		Logger:   logger,
	})
	return &fixture{
		database: database,
		store:    store,
		source:   source,
		adder:    adder,
		looker:   looker,
		settings: settingsFake,
		outbox:   outbox,
		r:        r,
	}
}

func (f *fixture) version(t *testing.T, infohash string) task.Version {
	t.Helper()
	version, err := f.store.Version(context.Background(), infohash)
	if err != nil {
		t.Fatalf("Version(%s): %v", infohash, err)
	}
	return version
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

// seedListTask 种一条「清单来的」任务：给番号预检与 infohash 归属测试当对照。
func (f *fixture) seedListTask(t *testing.T, movie, num, infohash string) {
	t.Helper()
	if _, err := f.database.Pool().Exec(context.Background(),
		`INSERT INTO list_registry (list_id, purpose, name) VALUES ('wanted', 'want_watch', '想看清单')`,
	); err != nil {
		t.Fatalf("种清单登记失败: %v", err)
	}
	inserted, err := f.store.SaveCandidate(context.Background(), task.Candidate{
		MovieID:  movie,
		Number:   num,
		ListID:   "wanted",
		Infohash: infohash,
		Source:   task.SourceJavDBMagnet,
		Delivery: task.DeliveryMagnet,
		Payload:  appapi.MagnetLink(infohash, num),
	})
	if err != nil {
		t.Fatalf("种清单任务失败: %v", err)
	}
	if !inserted {
		t.Fatalf("种清单任务没有插入")
	}
}

// ------------------------------------------------------------------ 链接解析

// TestMovieIDFromLink 钉住链接形状：任意主机的 /v/{id} 与裸 id 都认，其余拒绝。
func TestMovieIDFromLink(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "网页链接", input: "https://javdb.com/v/9629eV", want: "9629eV"},
		{name: "镜像域名", input: "https://javdb.rocks/v/abc123?ref=x#frag", want: "abc123"},
		{name: "裸 id", input: " 9629eV ", want: "9629eV"},
		{name: "空串", input: "", wantErr: true},
		{name: "别的路径", input: "https://javdb.com/users/1", wantErr: true},
		{name: "v 后面没有 id", input: "https://javdb.com/v/", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := manual.MovieIDFromLink(tc.input)
			if tc.wantErr {
				if err == nil || !errors.Is(err, manual.ErrBadLink) {
					t.Fatalf("err = %v, want ErrBadLink", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("MovieIDFromLink(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("MovieIDFromLink(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ------------------------------------------------------------------ 登记主流程

// TestRegisterEndToEnd 钉住完整一条：贴磁力 + 链接 → 落库（source=manual、无清单）→
// 加种 → 清 pending → 入箱「下载开始」→ 时间线两件事（手工登记 + 已加种）。
func TestRegisterEndToEnd(t *testing.T) {
	f := newFixture(t)

	result, err := f.r.Register(context.Background(), manual.Request{
		Magnet:   magnet,
		JavdbURL: "https://javdb.com/v/" + movieID,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if result.Outcome != manual.OutcomeAdded || result.MovieID != movieID || result.Number != number {
		t.Fatalf("result = %+v, want added/%s/%s", result, movieID, number)
	}

	version := f.version(t, plainHash)
	if version.Source != task.SourceManual || version.Delivery != task.DeliveryMagnet {
		t.Errorf("source/delivery = %s/%s, want manual/magnet", version.Source, version.Delivery)
	}
	if version.AddPending || version.Payload != magnet {
		t.Errorf("add_pending/payload = %v/%q, want false/贴来的磁力", version.AddPending, version.Payload)
	}
	if version.Status != task.StatusDownloading {
		t.Errorf("status = %s, want downloading", version.Status)
	}
	if version.ListRemovalPending {
		t.Errorf("list_removal_pending = true, want false（手工任务没有清单项可删）")
	}

	// 手工任务的 list_id 落的是 NULL，不是空串、不是某张清单的 id。
	var listID *string
	if err := f.database.Pool().QueryRow(context.Background(),
		`SELECT list_id FROM task WHERE movie_id = $1`, movieID,
	).Scan(&listID); err != nil {
		t.Fatalf("读 task.list_id: %v", err)
	}
	if listID != nil {
		t.Errorf("task.list_id = %q, want NULL", *listID)
	}

	if len(f.adder.calls) != 1 {
		t.Fatalf("Ensure 调了 %d 次, want 1", len(f.adder.calls))
	}
	torrent := f.adder.calls[0]
	if torrent.Hash != plainHash || torrent.Magnet != magnet || torrent.Number != number {
		t.Errorf("Ensure 的种子 = %+v, want hash/magnet/number 齐全", torrent)
	}

	if keys := f.sideEffectKeys(t, sidefx.KindNotify); len(keys) != 1 {
		t.Errorf("通知待办 = %v, want 1（下载开始）", keys)
	}

	events, err := f.store.Events(context.Background(), movieID, 10)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var registered, addedEvent bool
	for _, event := range events {
		if event.Kind == task.EventStateChange && event.Reason == "手工登记" {
			registered = true
		}
		if event.Kind == task.EventSideEffect && event.Reason == "已加种（added），下载开始通知入箱" {
			addedEvent = true
		}
	}
	if !registered || !addedEvent {
		t.Errorf("时间线缺事件: 手工登记=%v 已加种=%v（events=%+v）", registered, addedEvent, events)
	}
}

// TestRegisterOutcomePassesThrough 钉住收编/已在两种 Ensure 结局同样算登记成功。
func TestRegisterOutcomePassesThrough(t *testing.T) {
	for _, outcome := range []qbittorrent.Outcome{qbittorrent.Adopted, qbittorrent.AlreadyTracked} {
		t.Run(string(outcome), func(t *testing.T) {
			f := newFixture(t)
			f.adder.outcome = outcome

			result, err := f.r.Register(context.Background(), manual.Request{
				Magnet: magnet, JavdbURL: movieID,
			})
			if err != nil {
				t.Fatalf("Register: %v", err)
			}
			if result.Outcome != string(outcome) {
				t.Errorf("outcome = %s, want %s", result.Outcome, outcome)
			}
			if f.version(t, plainHash).AddPending {
				t.Errorf("add_pending = true, want false")
			}
		})
	}
}

// TestRegisterIsIdempotent 钉住重复登记：同 infohash 同任务 → 幂等放行、不再加种不再入箱。
func TestRegisterIsIdempotent(t *testing.T) {
	f := newFixture(t)
	first, err := f.r.Register(context.Background(), manual.Request{Magnet: magnet, JavdbURL: movieID})
	if err != nil {
		t.Fatalf("第一次 Register: %v", err)
	}

	second, err := f.r.Register(context.Background(), manual.Request{Magnet: magnet, JavdbURL: movieID})
	if err != nil {
		t.Fatalf("第二次 Register: %v", err)
	}
	if second.Outcome != manual.OutcomeDuplicate || first.Outcome != manual.OutcomeAdded {
		t.Errorf("结局 = %s 然后 %s, want added 然后 duplicate", first.Outcome, second.Outcome)
	}
	if len(f.adder.calls) != 1 || len(f.sideEffectKeys(t, sidefx.KindNotify)) != 1 {
		t.Errorf("重复登记产生了新动作: ensure=%d notify=%d, want 1/1",
			len(f.adder.calls), len(f.sideEffectKeys(t, sidefx.KindNotify)))
	}
}

// TestRegisterRejectsInfohashOfAnotherTask 钉住归属：同 infohash 已属于别的 movie_id → 拒绝。
func TestRegisterRejectsInfohashOfAnotherTask(t *testing.T) {
	f := newFixture(t)
	if _, err := f.r.Register(context.Background(), manual.Request{Magnet: magnet, JavdbURL: movieID}); err != nil {
		t.Fatalf("第一次 Register: %v", err)
	}

	// 另一部作品（不同 movie_id、不同番号），却贴同一条磁力。
	f.source.details["second"] = appapi.DetailResult{Movie: appapi.MovieDetail{ID: "second", Number: "MIDV-001"}}
	_, err := f.r.Register(context.Background(), manual.Request{Magnet: magnet, JavdbURL: "second"})
	if !errors.Is(err, manual.ErrInfohashTaken) {
		t.Fatalf("err = %v, want ErrInfohashTaken", err)
	}
}

// TestRegisterRejectsTakenNumber 钉住番号预检：同番号别的 movie_id 已有任务 → 当场拒绝、零写入。
func TestRegisterRejectsTakenNumber(t *testing.T) {
	f := newFixture(t)
	f.seedListTask(t, "owner1", number, otherHash)

	f.source.details["mine"] = appapi.DetailResult{Movie: appapi.MovieDetail{ID: "mine", Number: number}}
	_, err := f.r.Register(context.Background(), manual.Request{Magnet: magnet, JavdbURL: "mine"})
	if !errors.Is(err, manual.ErrNumberTaken) {
		t.Fatalf("err = %v, want ErrNumberTaken", err)
	}
	if _, err := f.store.Version(context.Background(), plainHash); !errors.Is(err, task.ErrVersionNotFound) {
		t.Errorf("被拒的登记留下了版本行: %v", err)
	}
	if len(f.adder.calls) != 0 {
		t.Errorf("被拒的登记加了种: %d 次", len(f.adder.calls))
	}
}

// TestRegisterGated 钉住门禁：未初始化 → 拒绝且不打 JavDB。
func TestRegisterGated(t *testing.T) {
	f := newFixture(t)
	f.settings.snapshot.Initialized = false

	_, err := f.r.Register(context.Background(), manual.Request{Magnet: magnet, JavdbURL: movieID})
	if !errors.Is(err, manual.ErrGated) {
		t.Fatalf("err = %v, want ErrGated", err)
	}
	if len(f.source.calls) != 0 {
		t.Errorf("门禁关着还打了 JavDB 详情: %v", f.source.calls)
	}
}

// TestRegisterValidatesInputs 钉住输入形状：坏磁力、坏链接、详情没有番号。
func TestRegisterValidatesInputs(t *testing.T) {
	t.Run("坏磁力", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.r.Register(context.Background(), manual.Request{
			Magnet: "http://example.com/x.torrent", JavdbURL: movieID,
		}); !errors.Is(err, manual.ErrBadMagnet) {
			t.Fatalf("err = %v, want ErrBadMagnet", err)
		}
	})
	t.Run("坏链接", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.r.Register(context.Background(), manual.Request{
			Magnet: magnet, JavdbURL: "https://javdb.com/users/1",
		}); !errors.Is(err, manual.ErrBadLink) {
			t.Fatalf("err = %v, want ErrBadLink", err)
		}
	})
	t.Run("详情没有番号", func(t *testing.T) {
		f := newFixture(t)
		f.source.details[movieID] = appapi.DetailResult{Movie: appapi.MovieDetail{ID: movieID}}
		if _, err := f.r.Register(context.Background(), manual.Request{
			Magnet: magnet, JavdbURL: movieID,
		}); err == nil {
			t.Fatalf("err = nil, want 详情没有番号的报错")
		}
	})
}

// TestRegisterDefersWhenEnsureFails 钉住 deferred：加种失败不是登记失败——
// 版本留在 add_pending，租约放回，时间线留告警，poll 会接着试。
func TestRegisterDefersWhenEnsureFails(t *testing.T) {
	f := newFixture(t)
	f.adder.err = errors.New("qbt 连不上")

	result, err := f.r.Register(context.Background(), manual.Request{Magnet: magnet, JavdbURL: movieID})
	if err != nil {
		t.Fatalf("Register: %v（加种失败不该是登记失败）", err)
	}
	if result.Outcome != manual.OutcomeDeferred || result.Reason == "" {
		t.Fatalf("result = %+v, want deferred 带原因", result)
	}

	version := f.version(t, plainHash)
	if !version.AddPending {
		t.Errorf("add_pending = false, want true（poll 接着试）")
	}
	// 租约已放回：下一次 claim 抢得到。
	if _, err := f.store.Claim(context.Background(), plainHash, "next-owner"); err != nil {
		t.Errorf("claim 放回后抢不到: %v", err)
	}
	events, _ := f.store.Events(context.Background(), movieID, 10)
	var alerted bool
	for _, event := range events {
		if event.Kind == task.EventAlert && event.Reason == "加种失败：qbt 连不上" {
			alerted = true
		}
	}
	if !alerted {
		t.Errorf("时间线没有加种失败告警（events=%+v）", events)
	}
}
