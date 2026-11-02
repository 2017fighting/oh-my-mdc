package scrape_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
	"github.com/2017fighting/oh-my-mdc/internal/dmm"
	"github.com/2017fighting/oh-my-mdc/internal/scrape"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：scrape.Runner.ProcessPending —— 真 Postgres + 夹具驱动的 DMM/JavDB 假源。
//
// 断言只看「上游被问了几次」「元数据三面在不在」「版本留在哪个状态」：
// 复用判据（0 次网络）是这一票的验收核心，计数断言是它唯一诚实的证据。

// ------------------------------------------------------------------ fakes

type fakeSettings struct {
	snapshot settings.Snapshot
	inputs   map[string]settings.TestInput
	loadErr  error
}

func (f *fakeSettings) Load(context.Context) (settings.Snapshot, error) {
	return f.snapshot, f.loadErr
}

func (f *fakeSettings) Input(_ context.Context, section string) (settings.TestInput, error) {
	input, ok := f.inputs[section]
	if !ok {
		return settings.TestInput{}, settings.ErrNotConfigured
	}
	return input, nil
}

type fakeDMM struct {
	result dmm.Result
	err    error
	calls  []string
}

func (f *fakeDMM) Lookup(_ context.Context, number string) (dmm.Result, error) {
	f.calls = append(f.calls, number)
	if f.err != nil {
		return dmm.Result{}, f.err
	}
	return f.result, nil
}

type fakeJavDB struct {
	result appapi.DetailResult
	err    error
	calls  []string
}

func (f *fakeJavDB) Detail(_ context.Context, token, movieID string) (appapi.DetailResult, error) {
	f.calls = append(f.calls, movieID)
	if f.err != nil {
		return appapi.DetailResult{}, f.err
	}
	return f.result, nil
}

func (f *fakeDMM) hit() int   { return len(f.calls) }
func (f *fakeJavDB) hit() int { return len(f.calls) }

// ------------------------------------------------------------------ fixture

type fixture struct {
	database *db.DB
	store    *task.Store
	settings *fakeSettings
	dmm      *fakeDMM
	javdb    *fakeJavDB
	runner   *scrape.Runner
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	database := dbtest.Migrated(t)
	if _, err := database.Pool().Exec(context.Background(),
		`INSERT INTO list_registry (list_id, purpose, name) VALUES ('wanted', 'want_watch', '想看清单')`,
	); err != nil {
		t.Fatalf("种清单登记失败: %v", err)
	}

	f := &fixture{
		database: database,
		store:    task.NewStore(database.Pool()),
		settings: readySettings(),
		dmm:      &fakeDMM{result: dmmFixture(t)},
		javdb:    &fakeJavDB{result: javdbFixture(t)},
	}
	f.runner = scrape.New(scrape.Options{
		Store:    f.store,
		Settings: f.settings,
		DMM:      f.dmm,
		JavDB:    f.javdb,
		Owner:    "test-scrape",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return f
}

func readySettings() *fakeSettings {
	return &fakeSettings{
		snapshot: settings.Snapshot{
			Initialized: true,
			Sections: map[string]settings.SectionState{
				"app": {Configured: true, Data: map[string]any{"reconcile_interval": "5m0s"}},
			},
		},
		inputs: map[string]settings.TestInput{
			"javdb": {Values: map[string]any{"token": "tk"}},
		},
	}
}

// seedProcessing 种一条已被推进到 processing 的版本（租约空闲，等取数）。
func (f *fixture) seedProcessing(t *testing.T, movieID, number, infohash string) {
	t.Helper()
	ctx := context.Background()
	inserted, err := f.store.SaveCandidate(ctx, task.Candidate{
		MovieID: movieID, Number: number, ListID: "wanted",
		Infohash: infohash, Source: task.SourceJavDBMagnet, Delivery: task.DeliveryMagnet,
		Payload: "magnet:?xt=urn:btih:" + infohash,
	})
	if err != nil || !inserted {
		t.Fatalf("种版本 %s: inserted=%v err=%v", infohash, inserted, err)
	}
	if _, err := f.store.Claim(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := f.store.MarkAdded(ctx, infohash, "owner-add"); err != nil {
		t.Fatalf("MarkAdded: %v", err)
	}
	if _, err := f.store.Claim(ctx, infohash, "owner-scrape"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := f.store.MarkProcessing(ctx, infohash, "owner-scrape"); err != nil {
		t.Fatalf("MarkProcessing: %v", err)
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

const (
	firstMovie = "ZY5eq"
	firstHash  = "1111111111111111111111111111111111111111"
	secondHash = "2222222222222222222222222222222222222222"
	firstNum   = "SSIS-001"
)

// ------------------------------------------------------------------ tests

// TestProcessPendingFetchesMetadataAndKeepsProcessing 是这一票的主链：
// 版本进 processing → 取齐两个源 → 字段图落列 + provenance + raw_snapshot + 演员落行，
// 取完仍驻 processing（整理归 #29）。
func TestProcessPendingFetchesMetadataAndKeepsProcessing(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)

	progress, err := f.runner.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if progress.Fetched != 1 || progress.Scanned != 1 {
		t.Fatalf("progress = %+v, want Scanned=1 Fetched=1", progress)
	}
	if f.dmm.hit() != 1 || f.javdb.hit() != 1 {
		t.Errorf("上游调用 = dmm %d / javdb %d, want 各 1 次", f.dmm.hit(), f.javdb.hit())
	}
	if calls := f.dmm.calls; len(calls) != 1 || calls[0] != firstNum {
		t.Errorf("DMM 查询 = %v, want 按番号 %s", calls, firstNum)
	}

	metadata, exists, err := f.store.Metadata(context.Background(), firstMovie)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if !exists {
		t.Fatal("取数完成后复用判据不成立")
	}
	if !strings.HasPrefix(metadata.TitleJA, "一ヶ月間の禁欲") {
		t.Errorf("title_ja = %q, want DMM 日文标题", metadata.TitleJA)
	}
	if metadata.Rating == nil || *metadata.Rating != 8.86 {
		t.Errorf("rating = %v, want 8.86", metadata.Rating)
	}
	if metadata.ReleaseDate == nil || metadata.ReleaseDate.Format("2006-01-02") != "2021-02-19" {
		t.Errorf("release_date = %v, want 2021-02-19", metadata.ReleaseDate)
	}
	if len(metadata.Tags) != 6 {
		t.Errorf("tags = %v, want JavDB 的 6 个", metadata.Tags)
	}

	var provenance map[string]scrape.Provenance
	if err := json.Unmarshal(metadata.Provenance, &provenance); err != nil {
		t.Fatalf("provenance 不是 JSON: %v", err)
	}
	if provenance[scrape.FieldTitleJA].Source != scrape.SourceDMM {
		t.Errorf("title_ja 来源 = %q, want dmm", provenance[scrape.FieldTitleJA].Source)
	}
	if provenance[scrape.FieldRating].Source != scrape.SourceJavDB {
		t.Errorf("rating 来源 = %q, want javdb", provenance[scrape.FieldRating].Source)
	}
	if provenance[scrape.FieldTitleJA].FetchedAt.IsZero() || provenance[scrape.FieldTitleJA].RawValue == nil {
		t.Errorf("provenance 条目缺 fetched_at/raw_value: %+v", provenance[scrape.FieldTitleJA])
	}

	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(metadata.RawSnapshot, &snapshot); err != nil {
		t.Fatalf("raw_snapshot 不是 JSON: %v", err)
	}
	for _, source := range []string{"dmm_search", "dmm_content", "javdb"} {
		if len(snapshot[source]) == 0 {
			t.Errorf("raw_snapshot 缺 %q", source)
		}
	}

	actors, err := f.store.Actors(context.Background(), firstMovie)
	if err != nil {
		t.Fatalf("Actors: %v", err)
	}
	if len(actors) != 3 {
		t.Fatalf("演员 = %d, want 3（JavDB 全量落行）", len(actors))
	}
	genders := map[string]string{}
	for _, actor := range actors {
		genders[actor.JavDBID] = actor.Gender
	}
	if genders["A5yq"] != "female" || genders["8VNb9"] != "male" {
		t.Errorf("性别落行 = %v", genders)
	}

	// 版本留在 processing：取数完成 ≠ 入库。
	version := f.version(t, firstHash)
	if version.Status != task.StatusProcessing {
		t.Errorf("status = %q, want processing（整理归 #29）", version.Status)
	}
}

// TestProcessPendingReusesMetadataWithZeroNetwork 是 P9-2 的计数断言：
// 元数据已存在的任务来新版本 → 0 次上游调用，只留在 processing 等硬链。
func TestProcessPendingReusesMetadataWithZeroNetwork(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)

	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("第一轮 ProcessPending: %v", err)
	}

	// 新版本（中字场景）到来：同一任务，第二条 infohash。
	f.seedProcessing(t, firstMovie, firstNum, secondHash)
	f.dmm.calls = nil
	f.javdb.calls = nil

	progress, err := f.runner.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("第二轮 ProcessPending: %v", err)
	}
	// 两条版本都在 processing（第一条取完数后留在那儿），两条都命中复用。
	if progress.Reused != 2 || progress.Fetched != 0 {
		t.Fatalf("progress = %+v, want Reused=2 Fetched=0", progress)
	}
	if f.dmm.hit() != 0 || f.javdb.hit() != 0 {
		t.Errorf("复用仍有网络调用：dmm %d / javdb %d, want 0/0", f.dmm.hit(), f.javdb.hit())
	}
	if version := f.version(t, secondHash); version.Status != task.StatusProcessing {
		t.Errorf("新版本状态 = %q, want processing", version.Status)
	}
}

// TestProcessPendingReusesEvenWhenOnlyProvenanceExists 钉住复用判据的口径：
// 「元数据存在」＝刮过（provenance 非空），与这份元数据好不好看无关。
func TestProcessPendingReusesEvenWhenOnlyProvenanceExists(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)

	// 一份「上游什么都没给」的元数据：只有 provenance 留了记录。
	if err := f.store.SaveMetadata(context.Background(), firstMovie, task.Lease{},
		task.Metadata{Provenance: json.RawMessage(`{"title_ja":{"source":"javdb","raw_value":""}}`), RawSnapshot: json.RawMessage(`{}`)},
		nil,
	); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}
	f.dmm.calls = nil
	f.javdb.calls = nil

	progress, err := f.runner.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if progress.Reused != 1 || f.dmm.hit()+f.javdb.hit() != 0 {
		t.Errorf("progress = %+v 调用 = %d/%d, want 复用且 0 次网络", progress, f.dmm.hit(), f.javdb.hit())
	}
}

// TestProcessPendingSchedulesRetryOnUpstreamFailure 是失败面：
// 地区阻断（blocked）**不许**降级成 JavDB 元数据 —— 版本留在 processing、
// attempts+1、退避期内不再重试、时间线上有一条告警。
func TestProcessPendingSchedulesRetryOnUpstreamFailure(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)
	f.dmm.err = &dmm.Error{
		Code:   settings.CodeProtocol,
		Op:     "graphql",
		Detail: "DMM 拒绝了这次请求：地区阻断（not-available-in-your-region）",
		Region: true,
	}

	progress, err := f.runner.ProcessPending(context.Background())
	if err == nil {
		t.Fatal("取数失败应当报出来（循环日志要看得见）")
	}
	if progress.Failed != 1 || progress.Fetched != 0 {
		t.Fatalf("progress = %+v, want Failed=1", progress)
	}

	_, exists, metaErr := f.store.Metadata(context.Background(), firstMovie)
	if metaErr != nil {
		t.Fatalf("Metadata: %v", metaErr)
	}
	if exists {
		t.Fatal("失败的一轮落了元数据：地区阻断被当成了「DMM 没收录」")
	}

	version := f.version(t, firstHash)
	if version.Status != task.StatusProcessing || version.Attempts != 1 {
		t.Errorf("version = status %q attempts %d, want processing/1", version.Status, version.Attempts)
	}

	events, err := f.store.Events(context.Background(), firstMovie, 10)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Kind == task.EventAlert && event.Phase == string(task.PhaseScrape) &&
			strings.HasPrefix(event.Reason, task.ScrapeFailurePrefix) {
			found = true
		}
	}
	if !found {
		t.Errorf("时间线没有取数失败告警: %+v", events)
	}

	// 退避期内不再重试。
	f.dmm.err = nil
	progress, err = f.runner.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("第二轮 ProcessPending: %v", err)
	}
	if progress.Scanned != 0 {
		t.Errorf("退避期内仍扫到 %d 条，want 0", progress.Scanned)
	}
}

// TestProcessPendingFailsWhenJavDBIsUnavailable 确认 JavDB 是必成的源：
// 它挂了就不该先花 DMM 的请求，版本留在 processing 等重试。
func TestProcessPendingFailsWhenJavDBIsUnavailable(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)
	f.javdb.err = errors.New("javdb 502")

	if progress, err := f.runner.ProcessPending(context.Background()); err == nil || progress.Failed != 1 {
		t.Fatalf("progress = %+v err = %v, want Failed=1 且有错误", progress, err)
	}
	if f.dmm.hit() != 0 {
		t.Errorf("JavDB 先失败却问了 DMM %d 次", f.dmm.hit())
	}
}

// TestProcessPendingIsGated 确认门禁没开时零动作（不进上游、不写库）。
func TestProcessPendingIsGated(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)
	f.settings.snapshot = settings.Snapshot{
		Paused:   true,
		Sections: map[string]settings.SectionState{},
	}

	progress, err := f.runner.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if !progress.Gated || progress.Scanned != 0 {
		t.Errorf("progress = %+v, want Gated（门禁关着）", progress)
	}
	if f.dmm.hit()+f.javdb.hit() != 0 {
		t.Errorf("门禁关着仍有上游调用: dmm %d / javdb %d", f.dmm.hit(), f.javdb.hit())
	}
}

// TestProcessPendingLeavesVersionsClaimedElsewhereAlone 确认租约互斥：
// 别的进程正处理的版本不归我，连发现面都不该出现（零写入、零网络）。
func TestProcessPendingSkipsVersionsClaimedElsewhere(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)
	if _, err := f.store.Claim(context.Background(), firstHash, "another-pod"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	progress, err := f.runner.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if progress.Scanned != 0 || progress.Fetched != 0 {
		t.Errorf("progress = %+v, want 扫到 0 条（租约在别人手里）", progress)
	}
	if f.dmm.hit()+f.javdb.hit() != 0 {
		t.Errorf("抢输的版本仍然上网了: dmm %d / javdb %d", f.dmm.hit(), f.javdb.hit())
	}
}
