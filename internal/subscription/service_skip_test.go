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
	"github.com/2017fighting/oh-my-mdc/internal/material"
	"github.com/2017fighting/oh-my-mdc/internal/metrics"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/subscription"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：`subscription.Service` 的跳过面（规格 §8.4 的两个端点，GLOSSARY「跳过/收下」）。
//
// 上游（磁力、M-Team、qBittorrent）是假的，其余全是真的：真 Postgres、真跳过表、
// 真 ingest（收下走的是与循环同一条段——ADR-0014）。

type skipFixture struct {
	database *db.DB
	store    *subscription.Store
	service  *subscription.Service
	magnets  *fakeMagnets
	adder    *fakeAdder
	sub      subscription.Row
}

func newSkipFixture(t *testing.T) *skipFixture {
	t.Helper()

	database := dbtest.Migrated(t)
	store := subscription.NewStore(database.Pool())
	sub, _, err := store.Create(context.Background(), subscription.Actor{ID: "EvkJ", Name: "河北彩花"})
	if err != nil {
		t.Fatalf("建订阅: %v", err)
	}

	magnets := &fakeMagnets{byMovie: map[string][]appapi.Magnet{}}
	adder := &fakeAdder{}
	torrents := &fakeTorrents{}
	settingsFake := runnerSettings()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tasks := task.NewStore(database.Pool())
	service := subscription.NewService(subscription.ServiceOptions{
		Store:    store,
		Settings: settingsFake,
		Source:   &fakeActresses{byID: map[string]string{"EvkJ": "河北彩花"}},
		Tasks:    tasks,
		Outbox:   sidefx.New(database.Pool()),
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
	})
	return &skipFixture{database: database, store: store, service: service,
		magnets: magnets, adder: adder, sub: sub}
}

// seed 造一行跳过，daysAgo 控制它的年龄（30 天窗口与清理都要看它）。
func (f *skipFixture) seed(t *testing.T, movieID, number, title, reason string, daysAgo int, accepted bool) int64 {
	t.Helper()
	var acceptedAt any
	if accepted {
		acceptedAt = time.Now()
	}
	var id int64
	if err := f.database.Pool().QueryRow(context.Background(),
		`INSERT INTO subscription_skip
		     (subscription_id, movie_id, number, title, release_date, reason, skipped_at, accepted_at)
		 VALUES ($1, $2, $3, $4, CURRENT_DATE - 2, $5, CURRENT_DATE - $6::int, $7)
		 RETURNING id`,
		f.sub.ID, movieID, number, title, reason, daysAgo, acceptedAt,
	).Scan(&id); err != nil {
		t.Fatalf("造跳过行 %s: %v", movieID, err)
	}
	return id
}

// acceptState 读一行跳过的 accepted_at（nil＝还没被收下）。
func (f *skipFixture) acceptState(t *testing.T, id int64) *time.Time {
	t.Helper()
	var at *time.Time
	if err := f.database.Pool().QueryRow(context.Background(),
		`SELECT accepted_at FROM subscription_skip WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatalf("读跳过行 %d: %v", id, err)
	}
	return at
}

// versions 数这一部作品的版本行。
func (f *skipFixture) versions(t *testing.T, movieID string) int {
	t.Helper()
	var count int
	if err := f.database.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM task_version WHERE movie_id = $1`, movieID).Scan(&count); err != nil {
		t.Fatalf("数版本: %v", err)
	}
	return count
}

// TestSkipsListsRecentUnacceptedRows 钉住 GET /api/subscriptions/{id}/skips 的窗口与口径：
// 最近 30 天、没被收下的那些，新的在前；别的订阅的 id 是 404。
func TestSkipsListsRecentUnacceptedRows(t *testing.T) {
	f := newSkipFixture(t)
	f.seed(t, "m-old", "ABP-001", "两个月前被排掉的", metrics.ReasonCompilation, 40, false)
	f.seed(t, "m-taken", "ABP-002", "已经收下的", metrics.ReasonLongForm, 3, true)
	f.seed(t, "m-newer", "ABP-003", "昨天被排掉的", metrics.ReasonNotSingle, 1, false)
	f.seed(t, "m-older", "ABP-004", "一周前被排掉的", metrics.ReasonLongForm, 7, false)

	skips, err := f.service.Skips(context.Background(), f.sub.ID)
	if err != nil {
		t.Fatalf("Skips: %v", err)
	}

	if len(skips) != 2 {
		t.Fatalf("skips = %+v, want 2 行（30 天内、没被收下的）", skips)
	}
	if skips[0].MovieID != "m-newer" || skips[1].MovieID != "m-older" {
		t.Errorf("顺序 = %s, %s, want 新的在前（m-newer, m-older）", skips[0].MovieID, skips[1].MovieID)
	}
	first := skips[0]
	if first.Number != "ABP-003" || first.Title != "昨天被排掉的" || first.Reason != metrics.ReasonNotSingle {
		t.Errorf("第一行 = %+v", first)
	}
	if first.ReleaseDate == nil {
		t.Error("release_date = nil, want 详情那一天（面板要显示它）")
	}
	if first.SubscriptionID != f.sub.ID || first.ID == 0 {
		t.Errorf("第一行身份 = %+v", first)
	}

	if _, err := f.service.Skips(context.Background(), f.sub.ID+999); !errors.Is(err, subscription.ErrNotFound) {
		t.Errorf("未知订阅的 Skips err = %v, want ErrNotFound", err)
	}
}

// TestAcceptSkipBuildsTheTaskAndIsIdempotent 是「收下」的主链：建任务并入队（走同一
// ingest）、行被标记已收下，重复收下幂等——不重复加种、不重复落版本、回同样的 outcome。
func TestAcceptSkipBuildsTheTaskAndIsIdempotent(t *testing.T) {
	f := newSkipFixture(t)
	skipID := f.seed(t, "m1", "ABP-001", "真正的四小时单体作品", metrics.ReasonLongForm, 2, false)
	f.magnets.byMovie["m1"] = []appapi.Magnet{{
		Hash: "m1-hash", SizeMiB: 4096,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}}

	result, err := f.service.AcceptSkip(context.Background(), f.sub.ID, skipID)
	if err != nil {
		t.Fatalf("AcceptSkip: %v", err)
	}
	if result.MovieID != "m1" || result.Outcome != subscription.OutcomeAccepted {
		t.Errorf("回执 = %+v, want {m1 accepted}", result)
	}

	// 任务与版本都落了库，并且已经进 qBittorrent（入队，不是只排个队等人点下一颗按钮）。
	if got := f.versions(t, "m1"); got != 1 {
		t.Errorf("版本数 = %d, want 1", got)
	}
	var number string
	var subscriptionID *int64
	if err := f.database.Pool().QueryRow(context.Background(),
		`SELECT number, subscription_id FROM task WHERE movie_id = 'm1'`,
	).Scan(&number, &subscriptionID); err != nil {
		t.Fatalf("读任务: %v", err)
	}
	if number != "ABP-001" || subscriptionID == nil || *subscriptionID != f.sub.ID {
		t.Errorf("任务 = %s / 订阅 %v, want ABP-001 / %d", number, subscriptionID, f.sub.ID)
	}
	var addPending bool
	if err := f.database.Pool().QueryRow(context.Background(),
		`SELECT add_pending FROM task_version WHERE movie_id = 'm1'`).Scan(&addPending); err != nil {
		t.Fatalf("读版本: %v", err)
	}
	if addPending {
		t.Error("add_pending = true, want false（收下就该加种，不是留个待办）")
	}
	if len(f.adder.calls) != 1 || f.adder.calls[0].Number != "ABP-001" {
		t.Errorf("加种调用 = %+v, want 一次 ABP-001", f.adder.calls)
	}
	if at := f.acceptState(t, skipID); at == nil {
		t.Error("accepted_at = NULL, want 收下的时间")
	}
	// 收下的那一行不再出现在「最近被排掉的」里。
	if skips, err := f.service.Skips(context.Background(), f.sub.ID); err != nil || len(skips) != 0 {
		t.Errorf("收下之后 skips = %+v err = %v, want 空", skips, err)
	}

	// 再点一次：同一个 outcome，不重复加种、不重复落版本。
	again, err := f.service.AcceptSkip(context.Background(), f.sub.ID, skipID)
	if err != nil {
		t.Fatalf("第二次 AcceptSkip: %v", err)
	}
	if again != result {
		t.Errorf("第二次回执 = %+v, want %+v（重复收下幂等）", again, result)
	}
	if got := f.versions(t, "m1"); got != 1 {
		t.Errorf("版本数 = %d, want 1（不重复落行）", got)
	}
	if len(f.adder.calls) != 1 {
		t.Errorf("加种调用 = %d, want 1（不重复加种）", len(f.adder.calls))
	}
	if len(f.magnets.calls) != 1 {
		t.Errorf("磁力查询 = %v, want 1 次（任务已有版本就不再查源）", f.magnets.calls)
	}
}

// TestAcceptSkipWithoutMagnetKeepsTheRow 是「磁力为空」那条：收下时还没出水，就**不**
// 标记已收下——否则这一部从面板上消失、又永远进不了任务表，等于把它弄丢了。
func TestAcceptSkipWithoutMagnetKeepsTheRow(t *testing.T) {
	f := newSkipFixture(t)
	skipID := f.seed(t, "m1", "ABP-001", "还没出水的作品", metrics.ReasonCompilation, 2, false)

	result, err := f.service.AcceptSkip(context.Background(), f.sub.ID, skipID)
	if err != nil {
		t.Fatalf("AcceptSkip: %v", err)
	}
	if result.Outcome != subscription.OutcomeNoMagnet {
		t.Errorf("outcome = %s, want %s（还没有磁力）", result.Outcome, subscription.OutcomeNoMagnet)
	}
	if at := f.acceptState(t, skipID); at != nil {
		t.Error("accepted_at 被置上了：还没出水就收下等于把它弄丢")
	}
	if skips, err := f.service.Skips(context.Background(), f.sub.ID); err != nil || len(skips) != 1 {
		t.Errorf("skips = %+v err = %v, want 那一行还留着", skips, err)
	}

	// 磁力出来了，再点一次就收下。
	f.magnets.byMovie["m1"] = []appapi.Magnet{{
		Hash: "m1-hash", SizeMiB: 4096,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}}
	if result, err := f.service.AcceptSkip(context.Background(), f.sub.ID, skipID); err != nil ||
		result.Outcome != subscription.OutcomeAccepted {
		t.Fatalf("出水后再收下 = %+v err = %v, want accepted", result, err)
	}
	if got := f.versions(t, "m1"); got != 1 {
		t.Errorf("版本数 = %d, want 1", got)
	}
}

// TestAcceptSkipRejectsAnotherSubscriptionsRow 钉住作用域：跳过 id 不是全局编号，
// 拿别的订阅的 id 来收下必须是 404，而不是「碰巧改了别人那一行」。
func TestAcceptSkipRejectsAnotherSubscriptionsRow(t *testing.T) {
	f := newSkipFixture(t)
	skipID := f.seed(t, "m1", "ABP-001", "被排掉的", metrics.ReasonCompilation, 2, false)
	other, _, err := f.store.Create(context.Background(), subscription.Actor{ID: "Av2e", Name: "另一位"})
	if err != nil {
		t.Fatalf("建第二条订阅: %v", err)
	}

	if _, err := f.service.AcceptSkip(context.Background(), other.ID, skipID); !errors.Is(err, subscription.ErrSkipNotFound) {
		t.Errorf("err = %v, want ErrSkipNotFound", err)
	}
	if at := f.acceptState(t, skipID); at != nil {
		t.Error("别的订阅收下竟然改了这条行")
	}
}

// TestAcceptSkipLeavesTheRowWhenUpstreamFails 钉住「上游抖了不算收下」：
// 磁力读失败时既不能假装收下（那会让作品消失），也不该悄悄吞掉错误。
func TestAcceptSkipLeavesTheRowWhenUpstreamFails(t *testing.T) {
	f := newSkipFixture(t)
	skipID := f.seed(t, "m1", "ABP-001", "被排掉的", metrics.ReasonCompilation, 2, false)
	f.magnets.err = &appapi.TransportError{Path: "/api/v1/movies/m1/magnets", Err: errors.New("连接被断")}

	if _, err := f.service.AcceptSkip(context.Background(), f.sub.ID, skipID); err == nil {
		t.Fatal("AcceptSkip = nil error, want 上游错误")
	} else if !errors.Is(err, subscription.ErrUpstream) {
		t.Errorf("err = %v, want 包着 ErrUpstream（HTTP 502）", err)
	}
	if at := f.acceptState(t, skipID); at != nil {
		t.Error("上游失败也把行标记成已收下了")
	}
	if _, err := f.service.AcceptSkip(context.Background(), f.sub.ID+999, skipID); !errors.Is(err, subscription.ErrSkipNotFound) {
		// 收下是按「这条订阅下的这条跳过」限定的：订阅不对就是这条跳过不存在（都是 404）。
		t.Errorf("未知订阅的 AcceptSkip err = %v, want ErrSkipNotFound", err)
	}
}
