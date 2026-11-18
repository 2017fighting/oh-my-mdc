package subscription_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/subscription"
)

// 测试缝：subscription.Service —— 面板「添加/启停/回灌」与 GET 列表走的那个门面。
// 上游（女优详情）是假的，其余全是真的：真 Postgres、真 subscription/actor 行。
//
// 这里钉的是 §6.4 的名单与身份规则：贴 id 或女优页链接都能建订阅、同一位女优重复
// 添加幂等、建订阅时先 upsert 一行 actor（name 取上游，gender/avatar 留着等刮削补）。

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

// fakeActresses 是 `/api/v1/actors/{id}` 的替身：只回答「这个 id 是谁」。
type fakeActresses struct {
	byID map[string]string
	// err 让上游读失败（如 id 不存在 / 网络）。
	err   error
	calls []string
}

func (f *fakeActresses) Actress(_ context.Context, _ string, id string) (appapi.ActressInfo, error) {
	f.calls = append(f.calls, id)
	if f.err != nil {
		return appapi.ActressInfo{}, f.err
	}
	name, ok := f.byID[id]
	if !ok {
		return appapi.ActressInfo{}, errors.New("上游没有这位女优")
	}
	return appapi.ActressInfo{ID: id, Name: name}, nil
}

// ------------------------------------------------------------------ fixture

type fixture struct {
	database *db.DB
	service  *subscription.Service
	upstream *fakeActresses
}

func readySettings() *fakeSettings {
	return &fakeSettings{
		snapshot: settings.Snapshot{
			Initialized: true,
			Sections: map[string]settings.SectionState{
				"app": {Configured: true, Data: map[string]any{}},
			},
		},
		inputs: map[string]settings.TestInput{
			"javdb": {Values: map[string]any{"token": "tk"}},
		},
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	database := dbtest.Migrated(t)
	upstream := &fakeActresses{byID: map[string]string{"EvkJ": "河北彩花"}}
	service := subscription.NewService(subscription.ServiceOptions{
		Store:    subscription.NewStore(database.Pool()),
		Settings: readySettings(),
		Source:   upstream,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return &fixture{database: database, service: service, upstream: upstream}
}

func (f *fixture) actor(t *testing.T, id string) (name string, gender *string, avatar *string) {
	t.Helper()
	err := f.database.Pool().QueryRow(context.Background(),
		`SELECT name, gender, avatar_url FROM actor WHERE javdb_actor_id = $1`, id,
	).Scan(&name, &gender, &avatar)
	if err != nil {
		t.Fatalf("读 actor %s: %v", id, err)
	}
	return name, gender, avatar
}

// dbDate 读库里的 CURRENT_DATE：断言日期时以库的时钟为准，不拿进程的本地时区赌。
func (f *fixture) dbDate(t *testing.T, daysAgo int) string {
	t.Helper()
	var value time.Time
	if err := f.database.Pool().QueryRow(context.Background(),
		`SELECT CURRENT_DATE - $1::int`, daysAgo).Scan(&value); err != nil {
		t.Fatalf("读 CURRENT_DATE: %v", err)
	}
	return dateOf(value)
}

func (f *fixture) rows(t *testing.T, sql string) int {
	t.Helper()
	var count int
	if err := f.database.Pool().QueryRow(context.Background(), sql).Scan(&count); err != nil {
		t.Fatalf("数行 %q: %v", sql, err)
	}
	return count
}

// ------------------------------------------------------------------ tests

// TestAddByActressIDCreatesSubscriptionAndActor 是面板「贴 id」那条路的主链：
// 建订阅 + 先 upsert 一行 actor（name 取上游，gender/avatar 留空等刮削补）。
func TestAddByActressIDCreatesSubscriptionAndActor(t *testing.T) {
	f := newFixture(t)

	row, outcome, err := f.service.Add(context.Background(), subscription.Request{ActressID: "EvkJ"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if outcome != subscription.OutcomeCreated {
		t.Errorf("outcome = %q, want %q", outcome, subscription.OutcomeCreated)
	}
	if row.ID == 0 || row.Actor.ID != "EvkJ" || row.Actor.Name != "河北彩花" {
		t.Fatalf("row = %+v, want 一条 EvkJ/河北彩花 的订阅", row)
	}
	if !row.Enabled {
		t.Error("enabled = false, want true（新订阅默认启用）")
	}
	if row.LookbackDays != 30 {
		t.Errorf("lookback_days = %d, want 30（表默认值）", row.LookbackDays)
	}
	if row.WatermarkDate == nil {
		t.Fatal("watermark_date = NULL, want 建订阅当天（从今天往后追）")
	}
	if got, want := dateOf(*row.WatermarkDate), f.dbDate(t, 0); got != want {
		t.Errorf("watermark_date = %s, want %s（建订阅当天）", got, want)
	}
	// 起追日同样是建订阅当天：她今天之前的作品永不自动捡起（GLOSSARY「起追日」）。
	if got, want := dateOf(row.FloorDate), f.dbDate(t, 0); got != want {
		t.Errorf("floor_date = %s, want %s（起追日 = 建订阅当天）", got, want)
	}

	name, gender, avatar := f.actor(t, "EvkJ")
	if name != "河北彩花" {
		t.Errorf("actor.name = %q, want 上游的名字", name)
	}
	if gender != nil || avatar != nil {
		t.Errorf("actor.gender/avatar = %v/%v, want NULL/NULL（等刮削补）", gender, avatar)
	}
}

// TestAddByJavdbURLAcceptsActressPageLink 钉住「贴女优页链接」也能建：
// 身份从 URL 的 /actors/{id} 段里取，镜像域名一堆也认。
func TestAddByJavdbURLAcceptsActressPageLink(t *testing.T) {
	f := newFixture(t)

	row, outcome, err := f.service.Add(context.Background(), subscription.Request{
		JavdbURL: "https://javdb.com/actors/EvkJ?locale=zh",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if outcome != subscription.OutcomeCreated || row.Actor.ID != "EvkJ" {
		t.Fatalf("row/outcome = %+v/%q, want EvkJ/created", row, outcome)
	}
	if len(f.upstream.calls) != 1 || f.upstream.calls[0] != "EvkJ" {
		t.Errorf("上游调用 = %v, want 只问一次 EvkJ", f.upstream.calls)
	}
}

// TestAddIsIdempotentForTheSameActress 是验收原文：同一位女优重复添加仍是 200，
// 不产生第二行（水位线也不被重置——重复添加不是「重新开始」）。
func TestAddIsIdempotentForTheSameActress(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	first, _, err := f.service.Add(ctx, subscription.Request{ActressID: "EvkJ"})
	if err != nil {
		t.Fatalf("第一次 Add: %v", err)
	}
	// 把水位线往回拨一天：重复添加若把它重置成今天，这条断言就会红。
	if _, err := f.database.Pool().Exec(ctx,
		`UPDATE subscription SET watermark_date = CURRENT_DATE - 1 WHERE id = $1`, first.ID); err != nil {
		t.Fatalf("改水位线: %v", err)
	}

	second, outcome, err := f.service.Add(ctx, subscription.Request{ActressID: "EvkJ"})
	if err != nil {
		t.Fatalf("第二次 Add: %v", err)
	}
	if outcome != subscription.OutcomeDuplicate {
		t.Errorf("outcome = %q, want duplicate", outcome)
	}
	if second.ID != first.ID {
		t.Errorf("第二次 id = %d, want %d（同一行）", second.ID, first.ID)
	}
	if second.WatermarkDate == nil || dateOf(*second.WatermarkDate) != f.dbDate(t, 1) {
		t.Errorf("watermark_date = %v, want 原样（重复添加不重置水位线）", second.WatermarkDate)
	}
	if got := f.rows(t, `SELECT count(*) FROM subscription`); got != 1 {
		t.Errorf("subscription 行数 = %d, want 1", got)
	}
	if got := f.rows(t, `SELECT count(*) FROM actor`); got != 1 {
		t.Errorf("actor 行数 = %d, want 1（upsert 不是插入第二行）", got)
	}
}

// TestAddKeepsScrapedGenderAndAvatar 守住「等刮削补」的反面：重扫过之后
// 再添加一次（幂等路径）不许把已补上的性别与头像抹掉。
func TestAddKeepsScrapedGenderAndAvatar(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.service.Add(ctx, subscription.Request{ActressID: "EvkJ"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := f.database.Pool().Exec(ctx,
		`UPDATE actor SET gender = 'female', avatar_url = 'https://example.invalid/e.jpg' WHERE javdb_actor_id = 'EvkJ'`); err != nil {
		t.Fatalf("补上性别头像: %v", err)
	}

	if _, _, err := f.service.Add(ctx, subscription.Request{ActressID: "EvkJ"}); err != nil {
		t.Fatalf("第二次 Add: %v", err)
	}
	name, gender, avatar := f.actor(t, "EvkJ")
	if name != "河北彩花" {
		t.Errorf("actor.name = %q", name)
	}
	if gender == nil || *gender != "female" || avatar == nil {
		t.Errorf("gender/avatar = %v/%v, want 保留刮削补上的值", gender, avatar)
	}
}

// TestAddRejectsBadShapes 钉住 400 那一档：两种输入都没给、链接不是女优页。
func TestAddRejectsBadShapes(t *testing.T) {
	f := newFixture(t)

	cases := map[string]subscription.Request{
		"两种都没给":    {},
		"链接不是女优页":  {JavdbURL: "https://javdb.com/v/ZY5eq"},
		"裸名字不是 id": {ActressID: "河北彩花"},
	}
	for name, request := range cases {
		if _, _, err := f.service.Add(context.Background(), request); !errors.Is(err, subscription.ErrBadInput) {
			t.Errorf("%s: err = %v, want ErrBadInput", name, err)
		}
	}
	if len(f.upstream.calls) != 0 {
		t.Errorf("坏输入也打了上游: %v", f.upstream.calls)
	}
}

// TestAddNeedsTheGate 与手工登记同一口径：未初始化/已暂停时添加被拒（409 gate_closed）。
func TestAddNeedsTheGate(t *testing.T) {
	f := newFixture(t)
	gated := readySettings()
	gated.snapshot.Initialized = false
	f.service = subscription.NewService(subscription.ServiceOptions{
		Store:    subscription.NewStore(f.database.Pool()),
		Settings: gated,
		Source:   f.upstream,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	_, _, err := f.service.Add(context.Background(), subscription.Request{ActressID: "EvkJ"})
	if !errors.Is(err, subscription.ErrGated) {
		t.Fatalf("err = %v, want ErrGated", err)
	}
	if got := f.rows(t, `SELECT count(*) FROM subscription`); got != 0 {
		t.Errorf("门禁没开却落了 %d 行订阅", got)
	}
}

// TestAddSurfacesUpstreamFailure 把「上游读不到这位 id」留给 HTTP 层当 502：
// 服务层只负责包成 ErrUpstream，不假装订阅建成了。
func TestAddSurfacesUpstreamFailure(t *testing.T) {
	f := newFixture(t)
	f.upstream.err = errors.New("connection reset")

	_, _, err := f.service.Add(context.Background(), subscription.Request{ActressID: "EvkJ"})
	if !errors.Is(err, subscription.ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
	if got := f.rows(t, `SELECT count(*) FROM subscription`); got != 0 {
		t.Errorf("上游失败却落了 %d 行订阅", got)
	}
}

// TestSetEnabledOnlyStopsChasingNewOnes 是「生命周期只有停用/恢复」那一行：
// 停用不碰已建的订阅行，也不动水位线。
func TestSetEnabledOnlyStopsChasingNewOnes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	created, _, err := f.service.Add(ctx, subscription.Request{ActressID: "EvkJ"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	disabled, err := f.service.SetEnabled(ctx, created.ID, false)
	if err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if disabled.Enabled {
		t.Error("enabled = true, want false")
	}
	if disabled.WatermarkDate == nil || dateOf(*disabled.WatermarkDate) != f.dbDate(t, 0) {
		t.Errorf("停用动了水位线: %v", disabled.WatermarkDate)
	}
	if got := f.rows(t, `SELECT count(*) FROM subscription`); got != 1 {
		t.Errorf("停用删了行（行数 %d）—— 行永不删除", got)
	}

	enabled, err := f.service.SetEnabled(ctx, created.ID, true)
	if err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	if !enabled.Enabled {
		t.Error("enabled = false, want true")
	}

	if _, err := f.service.SetEnabled(ctx, 999, true); !errors.Is(err, subscription.ErrNotFound) {
		t.Errorf("未知 id 的 err = %v, want ErrNotFound", err)
	}
}

// TestBackfillMovesTheLowerBoundBackwards 钉住 §8.4 的回执：
// backfill_from = watermark_date − days（水位线本身不动），起追日一并往前挪。
func TestBackfillMovesTheLowerBoundBackwards(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	created, _, err := f.service.Add(ctx, subscription.Request{ActressID: "EvkJ"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	row, err := f.service.Backfill(ctx, created.ID, subscription.BackfillRequest{Days: 90})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if row.BackfillFrom == nil {
		t.Fatal("backfill_from = NULL, want 水位线 − 90 天")
	}
	if got, want := dateOf(*row.BackfillFrom), f.dbDate(t, 90); got != want {
		t.Errorf("backfill_from = %s, want %s", got, want)
	}
	// 起追日跟着走：它才是「这条订阅从此敢看多早」的那一列，回灌是唯一能挪它的手段。
	if got, want := dateOf(row.FloorDate), f.dbDate(t, 90); got != want {
		t.Errorf("floor_date = %s, want %s（回灌把起追日往前挪）", got, want)
	}
	if row.WatermarkDate == nil || dateOf(*row.WatermarkDate) != f.dbDate(t, 0) {
		t.Errorf("回灌动了水位线: %v", row.WatermarkDate)
	}

	if _, err := f.service.Backfill(ctx, created.ID, subscription.BackfillRequest{Days: 0}); !errors.Is(err, subscription.ErrBadInput) {
		t.Errorf("days=0 的 err = %v, want ErrBadInput", err)
	}
	if _, err := f.service.Backfill(ctx, 999, subscription.BackfillRequest{Days: 90}); !errors.Is(err, subscription.ErrNotFound) {
		t.Errorf("未知 id 的 err = %v, want ErrNotFound", err)
	}
}

// TestBackfillRejectsDaysWithinTheLookback 是 §8.4 那条 400：days ≤ 回看窗口是**空操作**
// ——水位线 − days 落在回看窗口里面，下一轮本来就会看那么远。与其发一个什么都不做的
// 按钮，不如当场说清「回看窗口已经覆盖」。
func TestBackfillRejectsDaysWithinTheLookback(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	created, _, err := f.service.Add(ctx, subscription.Request{ActressID: "EvkJ"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	for _, days := range []int{1, created.LookbackDays} {
		_, err := f.service.Backfill(ctx, created.ID, subscription.BackfillRequest{Days: days})
		if !errors.Is(err, subscription.ErrBadInput) {
			t.Fatalf("days=%d 的 err = %v, want ErrBadInput", days, err)
		}
		// 这句话要能告诉用户「为什么不行」：回看窗口已经覆盖到那里了。
		if !strings.Contains(err.Error(), "回看窗口") {
			t.Errorf("days=%d 的 message = %q, want 说到回看窗口", days, err)
		}
	}

	// 被拒就是零写入：起追日与游标都原样。
	row, err := f.service.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(row) != 1 || row[0].BackfillFrom != nil || dateOf(row[0].FloorDate) != f.dbDate(t, 0) {
		t.Fatalf("被拒的回灌写了东西: %+v", row)
	}

	// 越过回看窗口一天就成立。
	if _, err := f.service.Backfill(ctx, created.ID, subscription.BackfillRequest{Days: created.LookbackDays + 1}); err != nil {
		t.Errorf("days=lookback+1 的 err = %v, want nil（这才是能推进的那一档）", err)
	}
}

// TestBackfillAllReaches1970 是「全部」那颗按钮：起追日与游标一并挪到 1970-01-01，
// 也就是「没有下界」。
func TestBackfillAllReaches1970(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	created, _, err := f.service.Add(ctx, subscription.Request{ActressID: "EvkJ"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	row, err := f.service.Backfill(ctx, created.ID, subscription.BackfillRequest{All: true})
	if err != nil {
		t.Fatalf("Backfill(all): %v", err)
	}
	if row.BackfillFrom == nil || dateOf(*row.BackfillFrom) != "1970-01-01" {
		t.Errorf("backfill_from = %v, want 1970-01-01", row.BackfillFrom)
	}
	if got := dateOf(row.FloorDate); got != "1970-01-01" {
		t.Errorf("floor_date = %s, want 1970-01-01（全部＝没有下界）", got)
	}
}

// TestListReportsTasksBroughtByEachSubscription 钉住面板一行要的全部字段：
// actor 快照、水位线、回看窗口、回灌下界、启停与「带来 N 部」。
func TestListReportsTasksBroughtByEachSubscription(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	created, _, err := f.service.Add(ctx, subscription.Request{ActressID: "EvkJ"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// 三部任务由这条订阅带来，其中一条的版本还在等加种。
	for i, movie := range []string{"m1", "m2", "m3"} {
		if _, err := f.database.Pool().Exec(ctx,
			`INSERT INTO task (movie_id, number, subscription_id) VALUES ($1, $2, $3)`,
			movie, "ABP-00"+movie, created.ID); err != nil {
			t.Fatalf("种任务 %s: %v", movie, err)
		}
		if i == 0 {
			if _, err := f.database.Pool().Exec(ctx,
				`INSERT INTO task_version (infohash, movie_id, source, delivery, payload)
				 VALUES ('aaa', $1, 'javdb_magnet', 'magnet', 'magnet:?xt=urn:btih:aaa')`, movie,
			); err != nil {
				t.Fatalf("种版本: %v", err)
			}
		}
	}

	rows, err := f.service.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List = %d 行, want 1", len(rows))
	}
	row := rows[0]
	if row.Actor.ID != "EvkJ" || row.Actor.Name != "河北彩花" {
		t.Errorf("actor = %+v, want EvkJ/河北彩花", row.Actor)
	}
	if row.TasksCount != 3 {
		t.Errorf("tasks_count = %d, want 3", row.TasksCount)
	}
	if row.PendingCount != 1 {
		t.Errorf("pending_count = %d, want 1（还有版本在等加种）", row.PendingCount)
	}
	if row.LookbackDays != 30 || !row.Enabled {
		t.Errorf("lookback/enabled = %d/%v", row.LookbackDays, row.Enabled)
	}
}

// ------------------------------------------------------------------ helpers

func dateOf(at time.Time) string { return at.Format("2006-01-02") }
