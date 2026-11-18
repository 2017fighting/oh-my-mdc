package subscription_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/loops"
	"github.com/2017fighting/oh-my-mdc/internal/material"
	"github.com/2017fighting/oh-my-mdc/internal/mteam"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/subscription"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：subscription.Runner.RunOnce —— 第四个循环（规格 §6.2/§6.4）。
//
// 真 Postgres + 假上游：女优页列表、磁力候选、qBittorrent 都是假的，订阅窗口
// （水位线 / 回看 / 回灌）、来源记录、「加种不带清单待删」全是真的走一遍。

// ------------------------------------------------------------------ fakes

type worksSource struct {
	// pages 按页号给答案（1 起）；超出范围的页回空（「空页即止」）。
	pages map[int][]appapi.ActressWork
	// errAt 让某一页读失败（窗口没扫完的那条路）。
	errAt map[int]error
	calls []int
}

func (f *worksSource) ActressWorks(_ context.Context, _ string, _ string, page int) ([]appapi.ActressWork, error) {
	f.calls = append(f.calls, page)
	if err, ok := f.errAt[page]; ok {
		return nil, err
	}
	return f.pages[page], nil
}

type fakeMagnets struct {
	byMovie map[string][]appapi.Magnet
	calls   []string
}

func (f *fakeMagnets) Magnets(_ context.Context, _ string, movieID string) ([]appapi.Magnet, error) {
	f.calls = append(f.calls, movieID)
	return f.byMovie[movieID], nil
}

type fakeAdder struct {
	calls   []qbittorrent.Torrent
	configs []qbittorrent.Config
}

func (f *fakeAdder) Ensure(_ context.Context, cfg qbittorrent.Config, torrent qbittorrent.Torrent) (qbittorrent.Outcome, error) {
	f.calls = append(f.calls, torrent)
	f.configs = append(f.configs, cfg)
	return qbittorrent.Added, nil
}

// ------------------------------------------------------------------ fixture

type runFixture struct {
	database *db.DB
	store    *subscription.Store
	runner   *subscription.Runner
	works    *worksSource
	magnets  *fakeMagnets
	adder    *fakeAdder
	settings *fakeSettings
	loops    *loops.Registry
	sub      subscription.Row
}

func newRunFixture(t *testing.T) *runFixture {
	t.Helper()
	return newRunFixtureWith(t, func(*fakeSettings) {})
}

func newRunFixtureWith(t *testing.T, tweak func(*fakeSettings)) *runFixture {
	t.Helper()

	database := dbtest.Migrated(t)
	store := subscription.NewStore(database.Pool())
	sub, _, err := store.Create(context.Background(), subscription.Actor{ID: "EvkJ", Name: "河北彩花"})
	if err != nil {
		t.Fatalf("建订阅: %v", err)
	}

	works := &worksSource{pages: map[int][]appapi.ActressWork{}, errAt: map[int]error{}}
	magnets := &fakeMagnets{byMovie: map[string][]appapi.Magnet{}}
	adder := &fakeAdder{}
	torrents := &fakeTorrents{}
	settingsFake := runnerSettings()
	tweak(settingsFake)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tasks := task.NewStore(database.Pool())
	registry := loops.NewRegistry()
	runner := subscription.NewRunner(subscription.RunnerOptions{
		Store:    store,
		Tasks:    tasks,
		Outbox:   sidefx.New(database.Pool()),
		Settings: settingsFake,
		Source:   works,
		Magnets:  magnets,
		Adder:    adder,
		Torrents: torrents,
		Material: material.New(material.Options{
			Dir:      t.TempDir(),
			Torrents: torrents,
			Settings: settingsFake,
			Logger:   logger,
		}),
		Pool:   database.Pool(),
		Loops:  registry,
		Owner:  "test-owner",
		Logger: logger,
	})
	return &runFixture{
		database: database,
		store:    store,
		runner:   runner,
		works:    works,
		magnets:  magnets,
		adder:    adder,
		settings: settingsFake,
		loops:    registry,
		sub:      sub,
	}
}

// runnerSettings 是循环用例的设置快照：门禁已开、qbt 三件套（category/download_root）
// 与订阅自己的节拍都在里面。
func runnerSettings() *fakeSettings {
	fake := readySettings()
	fake.snapshot.Sections["app"] = settings.SectionState{Configured: true, Data: map[string]any{
		"poll_interval":         "15m0s",
		"subscription_interval": "15m0s",
	}}
	fake.inputs["qbittorrent"] = settings.TestInput{Values: map[string]any{
		"url": "http://qbt:8080", "category": "mdc", "download_root": "/media/downloads",
	}}
	return fake
}

// fakeTorrents 是 M-Team 取种接缝：本票的所有用例都不该走到它（M-Team 未配置）。
type fakeTorrents struct {
	calls int
}

func (f *fakeTorrents) Resolve(context.Context, string, string) (mteam.Torrent, error) {
	f.calls++
	return mteam.Torrent{}, errors.New("本票的用例不该问 M-Team")
}

func (f *runFixture) current(t *testing.T) subscription.Row {
	t.Helper()
	row, err := f.store.Get(context.Background(), f.sub.ID)
	if err != nil {
		t.Fatalf("读订阅: %v", err)
	}
	return row
}

// daysAgo 造一个「N 天前发行」的作品（窗口断言全靠它）。
func (f *runFixture) daysAgo(t *testing.T, days int) time.Time {
	t.Helper()
	var value time.Time
	if err := f.database.Pool().QueryRow(context.Background(),
		`SELECT CURRENT_DATE - $1::int`, days).Scan(&value); err != nil {
		t.Fatalf("读 CURRENT_DATE: %v", err)
	}
	return value
}

// dbDate 读库里的 CURRENT_DATE − daysAgo（断言日期以库的时钟为准）。
func (f *runFixture) dbDate(t *testing.T, daysAgo int) string {
	t.Helper()
	return dateOf(f.daysAgo(t, daysAgo))
}

// backdated 把这条订阅的起追日与水位线一并挪到 N 天前，模拟「已经在追的一位女优」。
//
// 刚建好的订阅 floor_date 就是今天（起追日，永不回看历史），所以凡是要看见今天之前
// 那些作品的用例，都得先把它挪成一条真实存在了一阵的订阅。
func (f *runFixture) backdated(t *testing.T, floorDaysAgo, watermarkDaysAgo int) {
	t.Helper()
	if _, err := f.database.Pool().Exec(context.Background(),
		`UPDATE subscription
		 SET floor_date = CURRENT_DATE - $2::int, watermark_date = CURRENT_DATE - $3::int
		 WHERE id = $1`, f.sub.ID, floorDaysAgo, watermarkDaysAgo,
	); err != nil {
		t.Fatalf("把订阅挪回 %d/%d 天前: %v", floorDaysAgo, watermarkDaysAgo, err)
	}
}

// taskMovies 列任务表里由这条订阅带来的 movie_id（断言「谁进来了」用）。
func (f *runFixture) taskMovies(t *testing.T) []string {
	t.Helper()
	rows, err := f.database.Pool().Query(context.Background(),
		`SELECT movie_id FROM task WHERE subscription_id = $1 ORDER BY movie_id`, f.sub.ID)
	if err != nil {
		t.Fatalf("列任务: %v", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var movie string
		if err := rows.Scan(&movie); err != nil {
			t.Fatalf("扫任务: %v", err)
		}
		out = append(out, movie)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历任务: %v", err)
	}
	return out
}

// work 造一件作品，并把它的磁力配好（hash 由 movie_id 决定，便于断言）。
func (f *runFixture) work(t *testing.T, movieID, number string, daysAgo int) appapi.ActressWork {
	t.Helper()
	f.magnets.byMovie[movieID] = []appapi.Magnet{{
		Hash:      movieID + "-hash",
		SizeMiB:   2048,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}}
	return appapi.ActressWork{MovieID: movieID, Number: number, ReleaseDate: f.daysAgo(t, daysAgo)}
}

// ------------------------------------------------------------------ tests

// TestRunOnceBringsWindowWorksIntoTheTaskTable 是这条竖切的主链，也是验收原文：
// 窗口内的作品进任务表、走同一条 ingest（加种带番号/category/download_root），
// 但**来源记录**是订阅（list_id NULL、subscription_id 设了），因此不置清单项待删。
func TestRunOnceBringsWindowWorksIntoTheTaskTable(t *testing.T) {
	f := newRunFixture(t)
	f.backdated(t, 30, 30) // 订阅建于 30 天前：回看窗口那一段里的作品都算新
	f.works.pages[1] = []appapi.ActressWork{
		f.work(t, "m1", "ABP-001", 1),
		f.work(t, "m2", "ABP-002", 3),
	}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Gated || result.Locked {
		t.Fatalf("result = %+v, want 真跑了一轮", result)
	}
	if result.Subscriptions != 1 || result.Seen != 2 {
		t.Errorf("subscriptions/seen = %d/%d, want 1/2", result.Subscriptions, result.Seen)
	}
	if result.Queried != 2 || result.Candidates != 2 || result.Added != 2 {
		t.Errorf("queried/candidates/added = %d/%d/%d, want 2/2/2",
			result.Queried, result.Candidates, result.Added)
	}

	for _, movieID := range []string{"m1", "m2"} {
		var (
			number         string
			listID         *string
			subscriptionID *int64
		)
		if err := f.database.Pool().QueryRow(context.Background(),
			`SELECT number, list_id, subscription_id FROM task WHERE movie_id = $1`, movieID,
		).Scan(&number, &listID, &subscriptionID); err != nil {
			t.Fatalf("读任务 %s: %v", movieID, err)
		}
		if listID != nil {
			t.Errorf("%s 的 list_id = %v, want NULL（订阅作品不在任何清单上）", movieID, *listID)
		}
		if subscriptionID == nil || *subscriptionID != f.sub.ID {
			t.Errorf("%s 的 subscription_id = %v, want %d", movieID, subscriptionID, f.sub.ID)
		}
	}

	var addPending, removalPending bool
	if err := f.database.Pool().QueryRow(context.Background(),
		`SELECT add_pending, list_removal_pending FROM task_version WHERE movie_id = 'm1'`,
	).Scan(&addPending, &removalPending); err != nil {
		t.Fatalf("读版本: %v", err)
	}
	if addPending {
		t.Error("add_pending = true, want false（种已经加进去了）")
	}
	if removalPending {
		t.Error("list_removal_pending = true, want false（订阅作品没有清单项可删）")
	}

	// 加种走的是与清单轮询同一条 Ensure：番号（tag 三件套）与 qbt 配置都来自本项目。
	if len(f.adder.calls) != 2 {
		t.Fatalf("加种调用 = %d, want 2", len(f.adder.calls))
	}
	for i, torrent := range f.adder.calls {
		if torrent.Number == "" {
			t.Errorf("第 %d 次加种缺番号（qbt 的番号 tag 靠它）", i)
		}
		if cfg := f.adder.configs[i]; cfg.Category != "mdc" || cfg.DownloadRoot != "/media/downloads" {
			t.Errorf("第 %d 次加种的 qbt 配置 = %+v, want category=mdc / download_root=/media/downloads", i, cfg)
		}
	}
}

// TestNewSubscriptionNeverIngestsPreSubscriptionWorks 是起追日（floor_date）那条裁决：
// 今天订的订阅**永不看她今天之前的作品**——昨天那部虽然已经出水、有磁力，也不进来。
//
// 机制是取数下界的下界：下界 = GREATEST(floor_date, …)，而 floor_date 是建订阅当天。
// 没有它时回看窗口每轮都会往回带 30 天，「新订阅不追历史」只是一句好听话。
func TestNewSubscriptionNeverIngestsPreSubscriptionWorks(t *testing.T) {
	f := newRunFixture(t) // 订阅建于今天：floor_date = 今天
	f.works.pages[1] = []appapi.ActressWork{
		f.work(t, "today", "ABP-002", 0),
		f.work(t, "yesterday", "ABP-001", 1),
	}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Seen != 1 {
		t.Errorf("seen = %d, want 1（只有建订阅当天及以后的作品）", result.Seen)
	}
	if len(f.magnets.calls) != 1 || f.magnets.calls[0] != "today" {
		t.Errorf("查了磁力的作品 = %v, want [today]（昨天的作品连查都不该查）", f.magnets.calls)
	}
	if got := f.taskMovies(t); len(got) != 1 || got[0] != "today" {
		t.Errorf("任务表 = %v, want [today]", got)
	}
}

// TestNewSubscriptionReseesWorksAfterTheFloor 是起追日的另一半：它挡的是**今天之前**，
// 不是「一切」——建订阅之后出现、出水时还没有磁力的作品，下一轮仍在窗口里被重查。
func TestNewSubscriptionReseesWorksAfterTheFloor(t *testing.T) {
	f := newRunFixture(t) // 订阅建于今天
	work := appapi.ActressWork{MovieID: "nomagnet", Number: "ABP-404", ReleaseDate: f.daysAgo(t, 0)}
	delete(f.magnets.byMovie, "nomagnet")
	f.works.pages[1] = []appapi.ActressWork{work}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Seen != 1 || result.Candidates != 0 || result.Added != 0 {
		t.Errorf("seen/candidates/added = %d/%d/%d, want 1/0/0（出水时还没磁力）",
			result.Seen, result.Candidates, result.Added)
	}

	// 下一轮：磁力出来了，同一件作品仍在窗口里（起追日只挡今天之前）。
	f.work(t, "nomagnet", "ABP-404", 0)
	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("第二轮 RunOnce: %v", err)
	}
	if got := f.taskMovies(t); len(got) != 1 || got[0] != "nomagnet" {
		t.Errorf("任务表 = %v, want [nomagnet]（无磁力的作品下一轮仍在窗口内）", got)
	}
}

// TestRunStopsAtTheLookbackWindow 钉住回看窗口那一层：水位线已经追到最近时，
// 下界 = 水位线 − lookback_days（30），更老的历史不进来——哪怕它在起追日之后。
//
// 子集分得开才有信息量：40 天前那件在起追日（90 天前）之后，被回看窗口排除；
// 100 天前那件在起追日之前，被起追日排除。两层各管一段。
func TestRunStopsAtTheLookbackWindow(t *testing.T) {
	f := newRunFixture(t)
	f.backdated(t, 90, 1) // 建于 90 天前、水位线已经追到昨天
	f.works.pages[1] = []appapi.ActressWork{
		f.work(t, "fresh", "ABP-001", 1),
		f.work(t, "stale", "ABP-002", 40),
		f.work(t, "ancient", "ABP-003", 100),
	}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Seen != 1 {
		t.Errorf("seen = %d, want 1（只有回看窗口里那一件）", result.Seen)
	}
	if len(f.magnets.calls) != 1 || f.magnets.calls[0] != "fresh" {
		t.Errorf("查了磁力的作品 = %v, want [fresh]", f.magnets.calls)
	}

	// 水位线只增不减：见到的是昨天那件，于是水位线就停在昨天。
	row := f.current(t)
	if row.WatermarkDate == nil || dateOf(*row.WatermarkDate) != f.dbDate(t, 1) {
		t.Errorf("watermark_date = %v, want 昨天（水位线只增不减）", row.WatermarkDate)
	}
	if row.BackfillFrom != nil {
		t.Errorf("backfill_from = %v, want NULL（没点回灌）", row.BackfillFrom)
	}
	if got := dateOf(row.FloorDate); got != f.dbDate(t, 90) {
		t.Errorf("floor_date = %s, want %s（回灌才会挪它，追新不动）", got, f.dbDate(t, 90))
	}
}

// TestBackfillWindowReachesHistoricWorksThenClears 是「手动回灌」那条验收：
// 点回灌 90 天之后取数下界往回拨（60 天前那件进来、120 天前那件还在外面），
// 起追日一并往前挪，窗口扫完只清 backfill_from。
func TestBackfillWindowReachesHistoricWorksThenClears(t *testing.T) {
	f := newRunFixture(t)
	if _, err := f.store.SetBackfill(context.Background(), f.sub.ID, 90); err != nil {
		t.Fatalf("回灌: %v", err)
	}
	f.works.pages[1] = []appapi.ActressWork{
		f.work(t, "fresh", "ABP-001", 1),
		f.work(t, "old", "ABP-002", 60),
		f.work(t, "ancient", "ABP-003", 120),
	}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Seen != 2 {
		t.Errorf("seen = %d, want 2（90 天窗口里的两件）", result.Seen)
	}

	row := f.current(t)
	if row.BackfillFrom != nil {
		t.Errorf("backfill_from = %v, want NULL（窗口扫完要清）", row.BackfillFrom)
	}
	if row.WatermarkDate == nil || dateOf(*row.WatermarkDate) != f.dbDate(t, 0) {
		t.Errorf("watermark_date = %v, want 建订阅当天（回灌不动水位线）", row.WatermarkDate)
	}
	// 起追日被回灌往前挪到 90 天前，而且**扫完也不回退**：它是永久的「这条订阅从哪
	// 一天开始追」，不是这一轮的上限。
	if got := dateOf(row.FloorDate); got != f.dbDate(t, 90) {
		t.Errorf("floor_date = %s, want %s（回灌把它往前挪，不清）", got, f.dbDate(t, 90))
	}
}

// TestWatermarkAdvancesOnSeenEvenWithoutMagnets 是「水位线只由『见过』推进」那条验收：
// 昨天出水、还没有磁力的作品这一轮不落任何行，但水位线照推。
func TestWatermarkAdvancesOnSeenEvenWithoutMagnets(t *testing.T) {
	f := newRunFixture(t)
	f.backdated(t, 30, 30)
	// 还没出水的作品：上游先给了发行日，磁力还没有。
	work := appapi.ActressWork{MovieID: "nomagnet", Number: "ABP-404", ReleaseDate: f.daysAgo(t, 1)}
	f.works.pages[1] = []appapi.ActressWork{work}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Seen != 1 || result.Queried != 1 || result.Candidates != 0 || result.Added != 0 {
		t.Errorf("seen/queried/candidates/added = %d/%d/%d/%d, want 1/1/0/0",
			result.Seen, result.Queried, result.Candidates, result.Added)
	}
	row := f.current(t)
	if row.WatermarkDate == nil || dateOf(*row.WatermarkDate) != f.dbDate(t, 1) {
		t.Errorf("watermark_date = %v, want 见过的那一天（不是「入队成功」才推）", row.WatermarkDate)
	}

	// 下一轮：同一件作品仍在窗口里（下界跟着水位线走），于是再查一次源。
	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("第二轮 RunOnce: %v", err)
	}
	if len(f.magnets.calls) != 2 {
		t.Errorf("磁力查询次数 = %d, want 2（无磁力的作品下一轮仍在窗口内重查）", len(f.magnets.calls))
	}
}

// TestFutureReleaseDateDoesNotPushTheWatermark 钉住 §13 的预告片护栏：
// 上游会把还没发行的作品先登在列表里（预告），于是「见过的最晚发行日」可能落在未来。
// 水位线一旦跳进未来，下界（水位线 − 回看窗口）就跑到她全部现存作品之后——订阅会
// 静默停追到那个日子为止。水位线因此封顶在今天。
func TestFutureReleaseDateDoesNotPushTheWatermark(t *testing.T) {
	f := newRunFixture(t) // 订阅建于今天
	future := appapi.ActressWork{MovieID: "future", Number: "ABP-900", ReleaseDate: f.daysAgo(t, -60)}
	today := appapi.ActressWork{MovieID: "today", Number: "ABP-001", ReleaseDate: f.daysAgo(t, 0)}
	delete(f.magnets.byMovie, "future")
	delete(f.magnets.byMovie, "today")
	f.works.pages[1] = []appapi.ActressWork{future, today} // 列表按 release 倒序

	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("第一轮 RunOnce: %v", err)
	}
	row := f.current(t)
	if row.WatermarkDate == nil || dateOf(*row.WatermarkDate) != f.dbDate(t, 0) {
		t.Errorf("watermark_date = %v, want 今天（预告片的发行日不许把水位线推出去）", row.WatermarkDate)
	}

	// 下一轮：那部「今天发行」的作品出水了，必须被捡进来。水位线若停在 60 天后，
	// 下界会变成 30 天后，它就被当成「比下界还老」而直接跳过。
	f.work(t, "today", "ABP-001", 0)
	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("第二轮 RunOnce: %v", err)
	}
	if got := f.taskMovies(t); len(got) != 1 || got[0] != "today" {
		t.Errorf("任务表 = %v, want [today]（水位线封顶后，今天这部仍进得来）", got)
	}
}

// TestWindowDedupesOverlappingPages 钉住翻页护栏：上游的页可以重叠（清单扫描也按
// movie_id 去重），同一部作品不该被算两次「见过」、更不该被查两次磁力。
func TestWindowDedupesOverlappingPages(t *testing.T) {
	f := newRunFixture(t)
	f.backdated(t, 30, 30)
	first := f.work(t, "m1", "ABP-001", 1)
	f.works.pages[1] = []appapi.ActressWork{first}
	f.works.pages[2] = []appapi.ActressWork{first}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Seen != 1 || result.Queried != 1 {
		t.Errorf("seen/queried = %d/%d, want 1/1（重叠的页不该算两遍）", result.Seen, result.Queried)
	}
}

// TestBackfillAllReachesAncientWorks 是「全部」那颗按钮的循环面：起追日挪到 1970 之后，
// 十几年前的作品也进得来——90 天那种按天数往回拨够不到的地方。
func TestBackfillAllReachesAncientWorks(t *testing.T) {
	f := newRunFixture(t)
	if _, err := f.store.SetBackfillAll(context.Background(), f.sub.ID); err != nil {
		t.Fatalf("全部回灌: %v", err)
	}
	f.works.pages[1] = []appapi.ActressWork{f.work(t, "ancient", "ABP-001", 4000)}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Seen != 1 || result.Added != 1 {
		t.Errorf("seen/added = %d/%d, want 1/1（四千天前的作品也在窗口里）", result.Seen, result.Added)
	}
}

// TestUnfinishedWindowKeepsTheBackfillBound 是「回灌没扫完不许清」：
// 中途读上游失败时留着下界，下一轮从同一个地方接着扫。
func TestUnfinishedWindowKeepsTheBackfillBound(t *testing.T) {
	f := newRunFixture(t)
	if _, err := f.store.SetBackfill(context.Background(), f.sub.ID, 90); err != nil {
		t.Fatalf("回灌: %v", err)
	}
	f.works.pages[1] = []appapi.ActressWork{f.work(t, "m1", "ABP-001", 1)}
	f.works.errAt[2] = errors.New("上游断了")

	result, err := f.runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce = nil, want 上游读失败的错误（循环健康位要能看见）")
	}
	if result.Seen != 1 || result.Added != 1 {
		t.Errorf("seen/added = %d/%d, want 1/1（第一页的活照做）", result.Seen, result.Added)
	}
	if row := f.current(t); row.BackfillFrom == nil {
		t.Error("backfill_from 被清掉了 —— 窗口没扫完，下一轮还要接着扫")
	}
}

// TestDisabledSubscriptionIsNotChased 是「停用后不再追新」那一条：一条上游读取都不发。
func TestDisabledSubscriptionIsNotChased(t *testing.T) {
	f := newRunFixture(t)
	if _, err := f.store.SetEnabled(context.Background(), f.sub.ID, false); err != nil {
		t.Fatalf("停用: %v", err)
	}
	f.works.pages[1] = []appapi.ActressWork{f.work(t, "m1", "ABP-001", 1)}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Subscriptions != 0 || result.Seen != 0 {
		t.Errorf("result = %+v, want 一条订阅都没扫", result)
	}
	if len(f.works.calls) != 0 {
		t.Errorf("停用的订阅也打了上游: %v", f.works.calls)
	}
}

// TestGatedRunDoesNothing 与 poll 同一道门：未初始化/已暂停时零动作，
// 且门禁拦下的轮次不算一次迭代（面板不把它记成「跑过」）。
func TestGatedRunDoesNothing(t *testing.T) {
	f := newRunFixtureWith(t, func(s *fakeSettings) { s.snapshot.Paused = true })
	f.works.pages[1] = []appapi.ActressWork{f.work(t, "m1", "ABP-001", 1)}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !result.Gated || result.Reason == "" {
		t.Fatalf("result = %+v, want gated 且带一句原因", result)
	}
	if len(f.works.calls) != 0 {
		t.Errorf("门禁没开也打了上游: %v", f.works.calls)
	}
	if health := f.loops.Health(loops.Subscription); health.LastRunAt != nil {
		t.Error("门禁拦下的轮次记成了迭代（last_run_at 非空）")
	}
}

// TestRunRecordsItsOwnLoopHealth 是 §9 的 observability 那半：第四个循环有自己的健康位，
// 面板「循环健康」与 mdc_loop_* 指标都读它。
func TestRunRecordsItsOwnLoopHealth(t *testing.T) {
	f := newRunFixture(t)
	f.works.pages[1] = []appapi.ActressWork{f.work(t, "m1", "ABP-001", 1)}

	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	health := f.loops.Health(loops.Subscription)
	if health.LastRunAt == nil || health.LastOK == nil || !*health.LastOK {
		t.Fatalf("subscription 循环健康位 = %+v, want 跑过且成功", health)
	}
	if health.Iterations.OK != 1 {
		t.Errorf("iterations.ok = %d, want 1", health.Iterations.OK)
	}
}

// TestRunReadsItsOwnInterval 钉住「独立节拍」：间隔读 app_config.subscription_interval，
// 与清单轮询的 poll_interval 各走各的（一位女优的接口挂掉不该拖死清单轮询）。
func TestRunReadsItsOwnInterval(t *testing.T) {
	f := newRunFixtureWith(t, func(s *fakeSettings) {
		s.snapshot.Sections["app"] = settings.SectionState{Configured: true, Data: map[string]any{
			"poll_interval":         "5m0s",
			"subscription_interval": "37m0s",
		}}
	})

	if got := f.runner.Interval(context.Background()); got != 37*time.Minute {
		t.Errorf("Interval = %v, want 37m（读 subscription_interval）", got)
	}
}
