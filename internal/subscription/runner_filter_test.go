package subscription_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/metrics"
	"github.com/2017fighting/oh-my-mdc/internal/subscription"
)

// 测试缝：`subscription.Runner.RunOnce` 的筛子（规格 §6.4，ADR-0017，GLOSSARY「合集/
// 跳过/收下」）。
//
// 上游两处是假的（详情 + 标签词表），其余全是真的：真 Postgres、真跳过表、真 ingest。
// 断言的是循环的可观察结果——哪些作品落了跳过行、哪些进了任务表、哪条判据没生效。

// ------------------------------------------------------------------ fakes

// fakeDetails 是 `/api/v4/movies/{id}` 的替身：按 movie_id 给详情或给一个错。
type fakeDetails struct {
	byMovie map[string]appapi.MovieDetail
	errs    map[string]error
	calls   []string
}

func (f *fakeDetails) Detail(_ context.Context, _ string, movieID string) (appapi.DetailResult, error) {
	f.calls = append(f.calls, movieID)
	if err, ok := f.errs[movieID]; ok {
		return appapi.DetailResult{}, err
	}
	return appapi.DetailResult{Movie: f.byMovie[movieID]}, nil
}

// fakeVocabulary 是 `/api/v2/tags?type=0` 的替身：一份词表，或一次读失败。
type fakeVocabulary struct {
	tags  []appapi.Tag
	err   error
	calls int
}

func (f *fakeVocabulary) Tags(context.Context, string) ([]appapi.Tag, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.tags, nil
}

// fakeSkips 是 mdc_subscription_skipped_total 的替身：只记 reason 的序列。
type fakeSkips struct {
	reasons []string
}

func (f *fakeSkips) RecordSubscriptionSkipped(reason string) {
	f.reasons = append(f.reasons, reason)
}

// captureLogs 收这一轮打的每一条日志（筛子那两条「只记一次」靠它断言）。
type captureLogs struct {
	mu      sync.Mutex
	records []string
}

func (c *captureLogs) Enabled(context.Context, slog.Level) bool { return true }

func (c *captureLogs) Handle(_ context.Context, record slog.Record) error {
	var line strings.Builder
	line.WriteString(record.Message)
	record.Attrs(func(attr slog.Attr) bool {
		line.WriteString(" ")
		line.WriteString(attr.Key)
		line.WriteString("=")
		line.WriteString(attr.Value.String())
		return true
	})
	c.mu.Lock()
	c.records = append(c.records, line.String())
	c.mu.Unlock()
	return nil
}

func (c *captureLogs) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *captureLogs) WithGroup(string) slog.Handler      { return c }

// count 数有几条日志含有 needle。
func (c *captureLogs) count(needle string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, record := range c.records {
		if strings.Contains(record, needle) {
			total++
		}
	}
	return total
}

// filterVocabulary 是 ADR-0017 里那三个类別名的词表。
//
// id 刻意**不是**上游文档里的 8/10/11：判据必须按名字从词表查 id，写死一个数字就得在
// 这里翻车。名字带不带空格也刻意混着写——抓录里是「4小時以上作品」，规格里写
// 「4 小時以上作品」，两种写法是同一个标签。
func filterVocabulary() []appapi.Tag {
	return []appapi.Tag{
		{ID: "7777", Name: "精選綜合"},
		{ID: "8888", Name: "4小時以上作品"},
		{ID: "9999", Name: "16 小時以上作品"},
	}
}

// skipRow 是跳过表的一行（断言用），日期摊成字符串免得比时区。
type skipRow struct {
	MovieID     string
	Number      string
	Title       string
	Reason      string
	ReleaseDate string
	Accepted    bool
}

// skipRows 列这条订阅的跳过行。
func (f *runFixture) skipRows(t *testing.T) []skipRow {
	t.Helper()
	rows, err := f.database.Pool().Query(context.Background(),
		`SELECT movie_id, number, title, reason,
		        COALESCE(to_char(release_date, 'YYYY-MM-DD'), ''), accepted_at IS NOT NULL
		 FROM subscription_skip WHERE subscription_id = $1 ORDER BY movie_id`, f.sub.ID)
	if err != nil {
		t.Fatalf("列跳过行: %v", err)
	}
	defer rows.Close()

	out := []skipRow{}
	for rows.Next() {
		var row skipRow
		if err := rows.Scan(&row.MovieID, &row.Number, &row.Title, &row.Reason,
			&row.ReleaseDate, &row.Accepted); err != nil {
			t.Fatalf("扫跳过行: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历跳过行: %v", err)
	}
	return out
}

// ------------------------------------------------------------------ tests

// TestScreeningLandsOneRowPerJudgement 是这一竖切的主链：三条判据各自命中、各自落对
// reason，被排掉的一件都不进任务表、也不查磁力（「先筛后取源」）。
//
// 详情里的标签只带 id，名字与词表里的名字故意不同：判据按**词表查出来的 id** 认，
// 不认详情里那个名字（上游改名时判据还在）。
func TestScreeningLandsOneRowPerJudgement(t *testing.T) {
	f := newRunFixture(t)
	f.backdated(t, 30, 30)
	f.works.pages[1] = []appapi.ActressWork{
		f.work(t, "single", "SSIS-001", 1),
		f.work(t, "multi", "REBD-725", 2),
		f.work(t, "comp", "OFJE-186", 3),
		f.work(t, "long", "OFJE-416", 4),
	}
	f.details.byMovie = map[string]appapi.MovieDetail{
		"single": {Title: "单体", Actors: []appapi.Actor{{ID: "a1", Name: "河北彩花"}}},
		"multi": {Title: "两位女优共演", Actors: []appapi.Actor{
			{ID: "a1", Name: "京香"}, {ID: "a2", Name: "桃园怜奈"},
		}},
		"comp": {Title: "初ベスト 6 タイトル 8 時間", Actors: []appapi.Actor{{ID: "a1"}},
			Tags: []appapi.Tag{{ID: "7777", Name: "改名了也是同一个标签"}}},
		"long": {Title: "全 50SEX 16 時間", Actors: []appapi.Actor{{ID: "a1"}},
			Tags: []appapi.Tag{{ID: "9999", Name: "標籤"}}},
	}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Seen != 4 || result.Skipped != 3 {
		t.Errorf("seen/skipped = %d/%d, want 4/3", result.Seen, result.Skipped)
	}
	if result.Unexamined != 0 {
		t.Errorf("unexamined = %d, want 0", result.Unexamined)
	}

	// 只有没被排掉的那一件进了任务表，也只为它查了磁力。
	if got := f.taskMovies(t); len(got) != 1 || got[0] != "single" {
		t.Errorf("任务表 = %v, want [single]", got)
	}
	if len(f.magnets.calls) != 1 || f.magnets.calls[0] != "single" {
		t.Errorf("查了磁力的作品 = %v, want [single]（被排掉的不取源）", f.magnets.calls)
	}

	// 每件作品都读了一次详情（筛子的判据全在那里）。
	if len(f.details.calls) != 4 {
		t.Errorf("详情读取 = %v, want 4 次", f.details.calls)
	}

	want := []skipRow{
		{MovieID: "comp", Number: "OFJE-186", Title: "初ベスト 6 タイトル 8 時間",
			Reason: metrics.ReasonCompilation, ReleaseDate: f.dbDate(t, 3)},
		{MovieID: "long", Number: "OFJE-416", Title: "全 50SEX 16 時間",
			Reason: metrics.ReasonLongForm, ReleaseDate: f.dbDate(t, 4)},
		{MovieID: "multi", Number: "REBD-725", Title: "两位女优共演",
			Reason: metrics.ReasonNotSingle, ReleaseDate: f.dbDate(t, 2)},
	}
	got := f.skipRows(t)
	if len(got) != len(want) {
		t.Fatalf("跳过行 = %+v, want %d 行", got, len(want))
	}
	for i, row := range want {
		if got[i] != row {
			t.Errorf("跳过行[%d] = %+v, want %+v", i, got[i], row)
		}
	}

	// 指标三路各记一次（按 work 的顺序：multi / comp / long）。
	wantReasons := []string{metrics.ReasonNotSingle, metrics.ReasonCompilation, metrics.ReasonLongForm}
	if len(f.skips.reasons) != len(wantReasons) {
		t.Fatalf("指标记录 = %v, want %v", f.skips.reasons, wantReasons)
	}
	for i, reason := range wantReasons {
		if f.skips.reasons[i] != reason {
			t.Errorf("指标[%d] = %s, want %s", i, f.skips.reasons[i], reason)
		}
	}
}

// TestScreeningIsIdempotentAcrossRounds 钉住回看窗口的幂等：同一部作品每轮都会再现，
// 跳过行只落一条，指标也只记「第一次」——一条记录才算一次跳过。
func TestScreeningIsIdempotentAcrossRounds(t *testing.T) {
	f := newRunFixture(t)
	f.backdated(t, 30, 30)
	f.works.pages[1] = []appapi.ActressWork{f.work(t, "comp", "OFJE-186", 3)}
	f.details.byMovie["comp"] = appapi.MovieDetail{
		Title: "初ベスト", Actors: []appapi.Actor{{ID: "a1"}},
		Tags: []appapi.Tag{{ID: "7777", Name: "精選綜合"}},
	}

	for round := 1; round <= 2; round++ {
		result, err := f.runner.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("第 %d 轮 RunOnce: %v", round, err)
		}
		if result.Skipped != 1 && round == 1 {
			t.Errorf("第 1 轮 skipped = %d, want 1", result.Skipped)
		}
		if round == 2 && result.Skipped != 0 {
			t.Errorf("第 2 轮 skipped = %d, want 0（同一部作品不重复落行）", result.Skipped)
		}
	}

	if got := f.skipRows(t); len(got) != 1 {
		t.Fatalf("跳过行 = %+v, want 1 行", got)
	}
	if len(f.skips.reasons) != 1 {
		t.Errorf("指标记录 = %v, want 1 次（只有新落的行才算一次跳过）", f.skips.reasons)
	}
	if len(f.magnets.calls) != 0 {
		t.Errorf("被排掉的作品查了磁力: %v", f.magnets.calls)
	}
}

// TestAcceptedSkipLetsTheWorkThrough 钉住「收下」那个出口的另一半：已经收下的作品下一轮
// 不再被重新排一遍，而是照常走 ingest（判据可以错，出口必须留）。
func TestAcceptedSkipLetsTheWorkThrough(t *testing.T) {
	f := newRunFixture(t)
	f.backdated(t, 30, 30)
	f.works.pages[1] = []appapi.ActressWork{f.work(t, "comp", "OFJE-186", 3)}
	f.details.byMovie["comp"] = appapi.MovieDetail{
		Title: "初ベスト", Actors: []appapi.Actor{{ID: "a1"}},
		Tags: []appapi.Tag{{ID: "7777", Name: "精選綜合"}},
	}

	outcome, err := f.store.RecordSkip(context.Background(), subscription.Skip{
		SubscriptionID: f.sub.ID, MovieID: "comp", Number: "OFJE-186",
		Title: "初ベスト", Reason: metrics.ReasonCompilation, SkippedAt: time.Now(),
	})
	if err != nil || outcome != subscription.SkipRecorded {
		t.Fatalf("先落一行跳过: outcome=%v err=%v", outcome, err)
	}
	rows := f.skipRows(t)
	if err := f.store.AcceptSkip(context.Background(), f.sub.ID, skipID(t, f, "comp")); err != nil {
		t.Fatalf("收下: %v", err)
	}
	if rows[0].MovieID != "comp" {
		t.Fatalf("跳过行 = %+v", rows)
	}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Skipped != 0 {
		t.Errorf("skipped = %d, want 0（已收下的不再被重新排）", result.Skipped)
	}

	got := f.skipRows(t)
	if len(got) != 1 || !got[0].Accepted {
		t.Fatalf("跳过行 = %+v, want 一行且已收下（不新落一张卡片）", got)
	}
	if movies := f.taskMovies(t); len(movies) != 1 || movies[0] != "comp" {
		t.Errorf("任务表 = %v, want [comp]（收下的那一部照常走 ingest）", movies)
	}
}

// TestMissingTagNameSkipsThatJudgementOnce 是「名字不在词表里」那条：那条判据跳过、
// 其余判据照常、整轮不失败，而且日志每个名字只记一次。
func TestMissingTagNameSkipsThatJudgementOnce(t *testing.T) {
	f := newRunFixture(t)
	f.backdated(t, 30, 30)
	// 词表里只有精選綜合：两个长片类別都不在。
	f.vocabulary.tags = []appapi.Tag{{ID: "7777", Name: "精選綜合"}}
	f.works.pages[1] = []appapi.ActressWork{
		f.work(t, "long1", "OFJE-416", 3),
		f.work(t, "long2", "IDBD-887", 4),
		f.work(t, "comp", "OFJE-186", 5),
	}
	f.details.byMovie["long1"] = appapi.MovieDetail{
		Title: "12 時間", Actors: []appapi.Actor{{ID: "a1"}},
		Tags: []appapi.Tag{{ID: "1234", Name: "4小時以上作品"}},
	}
	f.details.byMovie["long2"] = appapi.MovieDetail{
		Title: "48 時間 BOX", Actors: []appapi.Actor{{ID: "a1"}},
		Tags: []appapi.Tag{{ID: "5678", Name: "16小時以上作品"}},
	}
	f.details.byMovie["comp"] = appapi.MovieDetail{
		Title: "初ベスト", Actors: []appapi.Actor{{ID: "a1"}},
		Tags: []appapi.Tag{{ID: "7777", Name: "精選綜合"}},
	}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v（词表缺名字不该让整轮失败）", err)
	}
	if result.Skipped != 1 {
		t.Errorf("skipped = %d, want 1（只有精选合集那条命中）", result.Skipped)
	}
	if got := f.taskMovies(t); len(got) != 2 || got[0] != "long1" || got[1] != "long2" {
		t.Errorf("任务表 = %v, want [long1 long2]（判据跳过的作品照常收下）", got)
	}

	// 两个缺名字的类別各记一次，与作品数无关。
	if got := f.logs.count("词表里没有这个类別名"); got != 2 {
		t.Errorf("缺名字日志 = %d 条, want 2（每个名字一条，不按作品数刷）", got)
	}

	// 下一轮：词表已缓存，日志不再重复。
	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("第二轮 RunOnce: %v", err)
	}
	if got := f.logs.count("词表里没有这个类別名"); got != 2 {
		t.Errorf("第二轮之后缺名字日志 = %d 条, want 2（同一个名字只记一次）", got)
	}
	if f.vocabulary.calls != 1 {
		t.Errorf("词表读取 = %d 次, want 1（进程内缓存）", f.vocabulary.calls)
	}
}

// TestVocabularyFailureStillScreensActors 钉住降级：词表读不到时标签判据整条跳过、
// 演员数那条照常，整轮不失败——token 过期不该让订阅循环变红（那是上游指标的事）。
func TestVocabularyFailureStillScreensActors(t *testing.T) {
	f := newRunFixture(t)
	f.backdated(t, 30, 30)
	f.vocabulary.err = errors.New("JWTVerificationError")
	f.works.pages[1] = []appapi.ActressWork{
		f.work(t, "multi", "REBD-725", 2),
		f.work(t, "comp", "OFJE-186", 3),
	}
	f.details.byMovie["multi"] = appapi.MovieDetail{
		Actors: []appapi.Actor{{ID: "a1"}, {ID: "a2"}},
	}
	f.details.byMovie["comp"] = appapi.MovieDetail{
		Actors: []appapi.Actor{{ID: "a1"}},
		Tags:   []appapi.Tag{{ID: "7777", Name: "精選綜合"}},
	}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v（词表读不到不该让整轮失败）", err)
	}
	if result.Skipped != 1 {
		t.Errorf("skipped = %d, want 1（只按演员数排）", result.Skipped)
	}
	got := f.skipRows(t)
	if len(got) != 1 || got[0].MovieID != "multi" || got[0].Reason != metrics.ReasonNotSingle {
		t.Fatalf("跳过行 = %+v, want 只有 multi/not_single", got)
	}
	if movies := f.taskMovies(t); len(movies) != 1 || movies[0] != "comp" {
		t.Errorf("任务表 = %v, want [comp]（标签判据没生效时它照常收下）", movies)
	}
	if f.logs.count("标签词表读不到") != 1 {
		t.Errorf("词表读不到日志 = %d 条, want 1", f.logs.count("标签词表读不到"))
	}
}

// TestDetailFailureLeavesTheWorkForTheNextRound 是「详情取数失败不落行」那条验收：
// 该作品既不建任务也不落跳过行，下一轮还在窗口里（与「磁力为空不落行」同一处置），
// 整轮不算失败。
func TestDetailFailureLeavesTheWorkForTheNextRound(t *testing.T) {
	f := newRunFixture(t)
	f.backdated(t, 30, 30)
	f.works.pages[1] = []appapi.ActressWork{
		f.work(t, "flaky", "SSIS-001", 1),
		f.work(t, "fine", "SSIS-002", 2),
	}
	f.details.byMovie["fine"] = appapi.MovieDetail{Actors: []appapi.Actor{{ID: "a1"}}}
	f.details.errs["flaky"] = &appapi.TransportError{Path: "/api/v4/movies/flaky", Err: errors.New("连接被断")}

	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v（详情读失败不该让整轮失败）", err)
	}
	if result.Unexamined != 1 || result.Skipped != 0 {
		t.Errorf("unexamined/skipped = %d/%d, want 1/0", result.Unexamined, result.Skipped)
	}
	if got := f.taskMovies(t); len(got) != 1 || got[0] != "fine" {
		t.Errorf("任务表 = %v, want [fine]（读不到详情的那件不落任务）", got)
	}
	if got := f.skipRows(t); len(got) != 0 {
		t.Errorf("跳过行 = %+v, want 0 行（读不到详情不落行）", got)
	}
	if health := f.loops.Health("subscription"); health.LastOK == nil || !*health.LastOK {
		t.Errorf("循环健康位 = %+v, want 成功（上游抖一下不算整轮失败）", health)
	}

	// 下一轮详情回来了：它在窗口里，被正常判过并收下。
	delete(f.details.errs, "flaky")
	f.details.byMovie["flaky"] = appapi.MovieDetail{Actors: []appapi.Actor{{ID: "a1"}}}
	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("第二轮 RunOnce: %v", err)
	}
	if got := f.taskMovies(t); len(got) != 2 || got[0] != "fine" || got[1] != "flaky" {
		t.Errorf("任务表 = %v, want [fine flaky]（下一轮重新判过）", got)
	}
}

// TestScreeningPurgesSkipsOlderThan30Days 是 30 天清理：没被收下的过期行清掉，
// 被收下的那条留着（它是「用户裁决过」的记录，也是同一部作品不再被重排的依据）。
func TestScreeningPurgesSkipsOlderThan30Days(t *testing.T) {
	f := newRunFixture(t)
	f.works.pages[1] = []appapi.ActressWork{f.work(t, "fresh", "SSIS-002", 1)}
	f.details.byMovie["fresh"] = appapi.MovieDetail{Actors: []appapi.Actor{{ID: "a1"}}}

	seed := func(movieID string, daysAgo int, accepted bool) {
		t.Helper()
		var acceptedAt any
		if accepted {
			acceptedAt = f.daysAgo(t, daysAgo)
		}
		if _, err := f.database.Pool().Exec(context.Background(),
			`INSERT INTO subscription_skip
			     (subscription_id, movie_id, number, title, reason, skipped_at, accepted_at)
			 VALUES ($1, $2, $3, '', 'compilation', CURRENT_DATE - $4::int, $5)`,
			f.sub.ID, movieID, "ABP-"+movieID, daysAgo, acceptedAt,
		); err != nil {
			t.Fatalf("造跳过行 %s: %v", movieID, err)
		}
	}
	seed("old", 31, false)
	seed("kept", 40, true)
	seed("recent", 5, false)

	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := map[string]bool{}
	for _, row := range f.skipRows(t) {
		got[row.MovieID] = row.Accepted
	}
	if _, ok := got["old"]; ok {
		t.Error("31 天前那条没被收下的跳过行还在（30 天清理没跑）")
	}
	if accepted, ok := got["kept"]; !ok || !accepted {
		t.Error("已收下的行被清掉了（它是同一部作品不再被重排的依据）")
	}
	if _, ok := got["recent"]; !ok {
		t.Error("5 天前那条被清掉了（30 天窗口内该留着）")
	}
}

// skipID 读某一部作品在这条订阅下的跳过行 id。
func skipID(t *testing.T, f *runFixture, movieID string) int64 {
	t.Helper()
	var id int64
	if err := f.database.Pool().QueryRow(context.Background(),
		`SELECT id FROM subscription_skip WHERE subscription_id = $1 AND movie_id = $2`,
		f.sub.ID, movieID,
	).Scan(&id); err != nil {
		t.Fatalf("读 %s 的跳过行 id: %v", movieID, err)
	}
	return id
}

// TestTrackedWorkIsNotScreened 钉住去重行（§6.1）在筛子这一层的形状：已经有任务的
// 作品既不再筛、也不再取源——同一部作品可能既在清单上又被订阅看见，那时它已经在下载了，
// 给它落一条「跳过」只会把正在跑的东西摆到「最近被排掉的」里。
func TestTrackedWorkIsNotScreened(t *testing.T) {
	f := newRunFixture(t)
	f.backdated(t, 30, 30)
	f.works.pages[1] = []appapi.ActressWork{f.work(t, "m1", "SSIS-001", 1)}
	f.details.byMovie["m1"] = appapi.MovieDetail{Actors: []appapi.Actor{{ID: "a1"}}}

	if _, err := f.runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("第一轮 RunOnce: %v", err)
	}
	if got := f.taskMovies(t); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("任务表 = %v, want [m1]", got)
	}

	// 第二轮：同一部作品还在回看窗口里。上游此后把它标成了共演（详情变了），但这已经
	// 不重要了——它有任务，就不该再被筛子重判一遍。
	f.details.byMovie["m1"] = appapi.MovieDetail{Actors: []appapi.Actor{{ID: "a1"}, {ID: "a2"}}}
	result, err := f.runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("第二轮 RunOnce: %v", err)
	}
	if result.Skipped != 0 {
		t.Errorf("skipped = %d, want 0（已有任务的作品不再被筛）", result.Skipped)
	}
	if got := f.skipRows(t); len(got) != 0 {
		t.Errorf("跳过行 = %+v, want 0 行", got)
	}
	if len(f.details.calls) != 1 {
		t.Errorf("详情读取 = %d 次, want 1（已有任务的不再取详情）", len(f.details.calls))
	}
}
