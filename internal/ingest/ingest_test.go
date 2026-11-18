package ingest_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/ingest"
	"github.com/2017fighting/oh-my-mdc/internal/material"
	"github.com/2017fighting/oh-my-mdc/internal/mteam"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：ingest.Ingester.Round(...) 的 Ingest / AddPending —— 真 Postgres + 假磁力源/qbt。
//
// 定源、去重、降级、材料重取的每条分支都已经被 poll 的既有测试压住了（走的是
// poll.RunOnce 那个组装缝，那里才是清单轮询的对外行为）。这里只补它们照不到的那件事：
// **一轮的形状**——来源把每一件待办喂进来，这一段负责先落库、把上轮遗留的一并加种，
// 且一轮只读一次 M-Team 凭据（不随作品数线性增长）。

// ------------------------------------------------------------------ fakes

type fakeSettings struct {
	snapshot settings.Snapshot
	inputs   map[string]settings.TestInput
	// inputCalls 数每一节被读了几次。
	inputCalls map[string]int
}

func (f *fakeSettings) Load(context.Context) (settings.Snapshot, error) {
	return f.snapshot, nil
}

func (f *fakeSettings) Input(_ context.Context, section string) (settings.TestInput, error) {
	if f.inputCalls == nil {
		f.inputCalls = map[string]int{}
	}
	f.inputCalls[section]++
	return f.inputs[section], nil
}

type fakeMagnets struct {
	byMovie map[string][]appapi.Magnet
}

func (f *fakeMagnets) Magnets(_ context.Context, _ string, movieID string) ([]appapi.Magnet, error) {
	return f.byMovie[movieID], nil
}

type fakeAdder struct {
	calls []qbittorrent.Torrent
	err   error
}

func (f *fakeAdder) Ensure(_ context.Context, _ qbittorrent.Config, torrent qbittorrent.Torrent) (qbittorrent.Outcome, error) {
	f.calls = append(f.calls, torrent)
	if f.err != nil {
		return "", f.err
	}
	return qbittorrent.Added, nil
}

type fakeTorrents struct {
	byNumber map[string]mteam.Torrent
	calls    []string
}

func (f *fakeTorrents) Resolve(_ context.Context, _ string, number string) (mteam.Torrent, error) {
	f.calls = append(f.calls, number)
	return f.byNumber[number], nil
}

// ------------------------------------------------------------------ fixture

type fixture struct {
	database *db.DB
	store    *task.Store
	settings *fakeSettings
	magnets  *fakeMagnets
	adder    *fakeAdder
	torrents *fakeTorrents
	ingester *ingest.Ingester
	cfg      qbittorrent.Config
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	database := dbtest.Migrated(t)
	if _, err := database.Pool().Exec(context.Background(),
		`INSERT INTO list_registry (list_id, purpose, name) VALUES ('wanted', 'want_watch', '想看清单')`,
	); err != nil {
		t.Fatalf("种清单登记失败: %v", err)
	}

	settingsFake := &fakeSettings{
		inputs: map[string]settings.TestInput{
			"mteam": {Values: map[string]any{"api_key": "mteam-key"}, Enabled: true},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adder := &fakeAdder{}
	torrents := &fakeTorrents{byNumber: map[string]mteam.Torrent{}}
	magnets := &fakeMagnets{byMovie: map[string][]appapi.Magnet{}}
	store := task.NewStore(database.Pool())

	return &fixture{
		database: database,
		store:    store,
		settings: settingsFake,
		magnets:  magnets,
		adder:    adder,
		torrents: torrents,
		ingester: ingest.New(ingest.Options{
			Store:    store,
			Outbox:   sidefx.New(database.Pool()),
			Settings: settingsFake,
			Magnets:  magnets,
			Adder:    adder,
			Torrents: torrents,
			Material: material.New(material.Options{
				Dir:      t.TempDir(),
				Torrents: torrents,
				Settings: settingsFake,
				Logger:   logger,
			}),
			Owner:  "test-owner",
			Logger: logger,
		}),
		cfg: qbittorrent.ConfigFrom(settings.TestInput{Values: map[string]any{
			"url": "http://qbt:8080", "category": "mdc", "download_root": "/media/downloads",
		}}),
	}
}

// enableMTeam 把 M-Team 节配好并启用（与向导里配了 api key 并打了勾等价）。
func (f *fixture) enableMTeam(t *testing.T) {
	t.Helper()
	enabled := true
	f.settings.snapshot.Sections = map[string]settings.SectionState{
		"mteam": {Configured: true, Enabled: &enabled},
	}
}

const (
	movieA  = "YwrO3B"
	numberA = "ABP-123"
	hashA   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	movieB  = "9kNP2X"
	numberB = "SSIS-001"
	hashB   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// 磁力候选只是「有磁力」的证据：定源优先走 M-Team，它们的 hash 不会落成版本。
	magnetHashA = "1111111111111111111111111111111111111111"
	magnetHashB = "2222222222222222222222222222222222222222"
)

// TestRoundIngestsEveryWorkThenAddsThem 是这一段一轮的形状：
// 来源喂两件待办 → 两件都先落库（add_pending 还在，加种还没发生）→ 一次 AddPending 收口，
// 加种各自带自己的种子字节，通知带上定源那一刻的体积。
func TestRoundIngestsEveryWorkThenAddsThem(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.enableMTeam(t)
	f.torrents.byNumber[numberA] = mteam.Torrent{Infohash: hashA, Bytes: []byte("torrent-a"), SizeBytes: 2 << 30}
	f.torrents.byNumber[numberB] = mteam.Torrent{Infohash: hashB, Bytes: []byte("torrent-b"), SizeBytes: 3 << 30}
	// JavDB 有磁力才查 M-Team（限速意识）：两件都得先有磁力候选。
	f.magnets.byMovie[movieA] = []appapi.Magnet{{Hash: magnetHashA, SizeMiB: 2048}}
	f.magnets.byMovie[movieB] = []appapi.Magnet{{Hash: magnetHashB, SizeMiB: 3072}}

	round := f.ingester.Round(f.settings.snapshot, "tk", f.cfg)

	for _, work := range []ingest.Work{
		{MovieID: movieA, Number: numberA, ListID: "wanted"},
		{MovieID: movieB, Number: numberB, ListID: "wanted"},
	} {
		ingested, err := round.Ingest(ctx, work)
		if err != nil {
			t.Fatalf("Ingest(%s): %v", work.MovieID, err)
		}
		if !ingested.Queried || !ingested.Inserted {
			t.Fatalf("ingested(%s) = %+v, want 已查源、已落库", work.MovieID, ingested)
		}
		if ingested.Candidate.Source != task.SourceMTeamTorrent || ingested.Candidate.Delivery != task.DeliveryTorrent {
			t.Errorf("%s 的 source/delivery = %s/%s, want mteam_torrent/torrent",
				work.MovieID, ingested.Candidate.Source, ingested.Candidate.Delivery)
		}
	}

	// M-Team 凭据一轮只读一次：它不该随作品数线性增长（设置域不是热路径）。
	if calls := f.settings.inputCalls[material.SectionMTeam]; calls != 1 {
		t.Errorf("M-Team 凭据读取 = %d 次, want 1（一轮一次）", calls)
	}
	if len(f.torrents.calls) != 2 {
		t.Errorf("M-Team 取种 = %v, want 每件一次", f.torrents.calls)
	}

	// 先落库再加种：两件都在库里等着，qBittorrent 还没被碰过。
	for _, movieID := range []string{movieA, movieB} {
		row := detail(t, f, movieID)
		if len(row.Versions) != 1 || !row.Versions[0].AddPending {
			t.Fatalf("%s 的版本 = %+v, want 一条 add_pending=true 的版本", movieID, row.Versions)
		}
	}
	if len(f.adder.calls) != 0 {
		t.Errorf("加种调用 = %d, want 0（AddPending 之前不碰 qBittorrent）", len(f.adder.calls))
	}

	added, addErrs := round.AddPending(ctx)
	if added != 2 || len(addErrs) != 0 {
		t.Fatalf("AddPending = %d, %v, want 2 条、无错误", added, addErrs)
	}
	bodies := map[string]string{}
	for _, call := range f.adder.calls {
		bodies[call.Hash] = string(call.Body)
	}
	if bodies[hashA] != "torrent-a" || bodies[hashB] != "torrent-b" {
		t.Errorf("加种的种子字节 = %v, want 各自那一份", bodies)
	}
	for _, movieID := range []string{movieA, movieB} {
		if version := detail(t, f, movieID).Versions[0]; version.AddPending {
			t.Errorf("%s 的 add_pending = true, want false（加种成功后清）", movieID)
		}
	}

	// 通知里的 size_mib 只在定源那一刻拿得到，所以要由这一段带下来。
	sizes := notifySizes(t, f)
	if sizes[hashA] != "2048" || sizes[hashB] != "3072" {
		t.Errorf("通知体积 = %v, want %s=2048、%s=3072", sizes, hashA, hashB)
	}
}

// ------------------------------------------------------------------ helpers

func detail(t *testing.T, f *fixture, movieID string) task.TaskRow {
	t.Helper()
	row, err := f.store.Detail(context.Background(), movieID, 50)
	if err != nil {
		t.Fatalf("Detail(%s): %v", movieID, err)
	}
	return row
}

// notifySizes 读出「下载开始」通知里的 infohash → size_mib。
func notifySizes(t *testing.T, f *fixture) map[string]string {
	t.Helper()
	rows, err := f.database.Pool().Query(context.Background(),
		`SELECT payload->>'infohash', payload->>'size_mib' FROM side_effect WHERE kind = $1`,
		string(sidefx.KindNotify))
	if err != nil {
		t.Fatalf("读 side_effect: %v", err)
	}
	defer rows.Close()

	sizes := map[string]string{}
	for rows.Next() {
		var hash, size *string
		if err := rows.Scan(&hash, &size); err != nil {
			t.Fatalf("扫 side_effect: %v", err)
		}
		if hash != nil && size != nil {
			sizes[*hash] = *size
		}
	}
	return sizes
}

// TestSubscriptionWorkIsPersistedWithoutListRemoval 是 #47 与这一段的两处接口：
//
//   - 订阅形状的候选（清单空、订阅非空）落得了库：validateCandidate 的放行清单从
//     「只有手工登记」扩到「有订阅 id 也行」（#47），同一段 ingest 才真的是两个来源共用；
//   - 加种之后**不置清单项待删**：订阅作品不在任何清单上，置起只会让旁路引擎入箱一条
//     list_id 为空的删除待办（它重试变不出来，只会驻留进需人工）。
//
// 判据是「这条版本所属的任务有没有清单 id」，所以它同时覆盖这条用例里最刁的那条路：
// 订阅上一轮落库的版本，这一轮被清单轮询的 AddPending 捡走加种——它照样不该置待删。
func TestSubscriptionWorkIsPersistedWithoutListRemoval(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// 订阅行：task.subscription_id 的外键要求它先存在。
	var subscriptionID int64
	if _, err := f.database.Pool().Exec(ctx,
		`INSERT INTO actor (javdb_actor_id, name) VALUES ('EvkJ', '河北彩花')
		 ON CONFLICT (javdb_actor_id) DO NOTHING`); err != nil {
		t.Fatalf("种演员: %v", err)
	}
	if err := f.database.Pool().QueryRow(ctx,
		`INSERT INTO subscription (actress_actor_id, watermark_date) VALUES ('EvkJ', CURRENT_DATE)
		 RETURNING id`).Scan(&subscriptionID); err != nil {
		t.Fatalf("种订阅: %v", err)
	}

	f.magnets.byMovie[movieA] = []appapi.Magnet{{Hash: magnetHashA, SizeMiB: 2048}}
	round := f.ingester.Round(f.settings.snapshot, "tk", f.cfg)

	ingested, err := round.Ingest(ctx, ingest.Work{
		MovieID:        movieA,
		Number:         numberA,
		SubscriptionID: subscriptionID,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !ingested.Inserted {
		t.Fatalf("ingested = %+v, want 订阅形状的候选落成一条版本", ingested)
	}
	if ingested.Candidate.SubscriptionID != subscriptionID {
		t.Errorf("候选的 subscription_id = %d, want %d", ingested.Candidate.SubscriptionID, subscriptionID)
	}

	var stored *int64
	if err := f.database.Pool().QueryRow(ctx,
		`SELECT subscription_id FROM task WHERE movie_id = $1`, movieA).Scan(&stored); err != nil {
		t.Fatalf("读任务的首次发现来源: %v", err)
	}
	if stored == nil || *stored != subscriptionID {
		t.Errorf("task.subscription_id = %v, want %d", stored, subscriptionID)
	}

	if added, errs := round.AddPending(ctx); added != 1 || len(errs) != 0 {
		t.Fatalf("AddPending = %d, %v, want 1 条、无错误", added, errs)
	}
	version := detail(t, f, movieA).Versions[0]
	if version.AddPending {
		t.Error("add_pending = true, want false")
	}
	if version.ListRemovalPending {
		t.Error("list_removal_pending = true, want false（订阅作品没有清单项可删）")
	}
	if removed, err := sidefx.New(f.database.Pool()).DrainPendingRemovals(ctx, 10); err != nil {
		t.Fatalf("DrainPendingRemovals: %v", err)
	} else if removed != 0 {
		t.Errorf("入箱的删清单待办 = %d 条, want 0（list_id 是空的删除永远做不成）", removed)
	}
}

// TestCandidateWithoutAnySourceIsRejected 是上面那条放行的反面：
// 清单 id 与订阅 id 都没有、又不是手工登记的候选，仍然落不了库——它填不上
// 「首次发现来源」这一列（规格 §4.1）。
func TestCandidateWithoutAnySourceIsRejected(t *testing.T) {
	f := newFixture(t)

	_, err := f.store.SaveCandidate(context.Background(), task.Candidate{
		MovieID:  movieA,
		Number:   numberA,
		Infohash: magnetHashA,
		Source:   task.SourceJavDBMagnet,
		Delivery: task.DeliveryMagnet,
		Payload:  "magnet:?xt=urn:btih:" + magnetHashA,
	})
	if err == nil {
		t.Fatal("没有首次发现来源的候选落库成功了，want 拒绝")
	}
}
