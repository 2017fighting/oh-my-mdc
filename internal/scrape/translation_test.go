package scrape_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/scrape"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
	"github.com/2017fighting/oh-my-mdc/internal/translate"
)

// AI 翻译是可选集成（规格 §5.7）：启用了就把日文 title/outline 译成中文再落库，
// 失败只降级日文原文 + 一条告警，**绝不**阻塞入库（P6-1/P6-2/P6-3）。
//
// 断言只看四件事：译文列、上游被问了几次、任务有没有被卡住、告警有没有留痕。

// fakeTranslator 是翻译接缝的假实现：记下每一次调用（含输入）并按需失败。
type fakeTranslator struct {
	result translate.Text
	err    error
	calls  []translate.Text
	config []translate.Config
}

func (f *fakeTranslator) Translate(_ context.Context, cfg translate.Config, in translate.Text) (translate.Text, error) {
	f.calls = append(f.calls, in)
	f.config = append(f.config, cfg)
	if f.err != nil {
		return translate.Text{}, f.err
	}
	return f.result, nil
}

func (f *fakeTranslator) count() int { return len(f.calls) }

// enableTranslation 把 AI 翻译配成「已配置且启用」（门禁判据在设置快照上）。
func (f *fixture) enableTranslation(enabled bool) {
	f.settings.snapshot.Sections["ai_translation"] = settings.SectionState{
		Configured: true,
		Enabled:    &enabled,
		Data:       map[string]any{"model": "gpt-4o-mini"},
	}
	f.settings.inputs["ai_translation"] = settings.TestInput{
		Values: map[string]any{
			"base_url": "https://api.example.com/v1",
			"api_key":  "sk-test",
			"model":    "gpt-4o-mini",
			"enabled":  enabled,
		},
		Enabled: enabled,
	}
}

// setTranslation 让假翻译器回一份固定译文。
func (f *fixture) setTranslation() translate.Text {
	f.translator.result = translate.Text{
		Title:   "禁欲一月的尽头",
		Outline: "S1 苗条女优的豪华共演",
	}
	return f.translator.result
}

// TestTranslationIsWrittenWhenEnabled 是 P6-1 的主链：启用 → 一次翻译请求
// （输入是日文原文）→ title_zh/outline_zh 落列、provenance 记来源，日文原文不动。
func TestTranslationIsWrittenWhenEnabled(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)
	f.enableTranslation(true)
	want := f.setTranslation()

	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}

	if f.translator.count() != 1 {
		t.Fatalf("翻译调用 = %d 次, want 1（一次请求出两个译文）", f.translator.count())
	}
	if in := f.translator.calls[0]; !strings.Contains(in.Title, "一ヶ月間の禁欲") || in.Outline == "" {
		t.Errorf("翻译输入 = %+v, want 日文原文（标题与简介）", in)
	}
	if cfg := f.translator.config[0]; cfg.Model != "gpt-4o-mini" || cfg.APIKey != "sk-test" {
		t.Errorf("翻译配置 = %+v, want 设置域里的 model/api_key", cfg)
	}

	metadata, _, err := f.store.Metadata(context.Background(), firstMovie)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if metadata.TitleZH != want.Title || metadata.OutlineZH != want.Outline {
		t.Errorf("title_zh/outline_zh = %q/%q, want %q/%q",
			metadata.TitleZH, metadata.OutlineZH, want.Title, want.Outline)
	}
	if metadata.TitleJA == "" || metadata.Outline == "" {
		t.Errorf("日文原文被译文挤掉了: %+v", metadata)
	}

	var provenance map[string]struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(metadata.Provenance, &provenance); err != nil {
		t.Fatalf("provenance 不是 JSON: %v", err)
	}
	if provenance[task.FieldTitleZH].Source != task.TranslationSource {
		t.Errorf("provenance.title_zh = %+v, want 来源 %q", provenance[task.FieldTitleZH], task.TranslationSource)
	}

	if version := f.version(t, firstHash); version.Status != task.StatusProcessing {
		t.Errorf("status = %q, want processing（翻译照旧不改变状态机）", version.Status)
	}
}

// TestDisabledTranslationMakesNoUpstreamCall 是 P6-3：没配或禁用了就一次都不问。
func TestDisabledTranslationMakesNoUpstreamCall(t *testing.T) {
	cases := map[string]func(*fixture){
		"未配置": func(f *fixture) {},
		"已配置但禁用": func(f *fixture) {
			f.enableTranslation(false)
		},
	}

	for name, setup := range cases {
		f := newFixture(t)
		f.seedProcessing(t, firstMovie, firstNum, firstHash)
		setup(f)

		if _, err := f.runner.ProcessPending(context.Background()); err != nil {
			t.Fatalf("%s: ProcessPending: %v", name, err)
		}
		if f.translator.count() != 0 {
			t.Errorf("%s: 翻译调用 = %d 次, want 0", name, f.translator.count())
		}
		metadata, _, err := f.store.Metadata(context.Background(), firstMovie)
		if err != nil {
			t.Fatalf("%s: Metadata: %v", name, err)
		}
		if metadata.TitleZH != "" || metadata.OutlineZH != "" {
			t.Errorf("%s: title_zh/outline_zh = %q/%q, want 空（没启用翻译）",
				name, metadata.TitleZH, metadata.OutlineZH)
		}
	}
}

// TestTranslationFailureDegradesWithOneAlert 是 P6-2：翻译失败 → 日文原文照常入库、
// 版本不进 failed、时间线上只留一条带原因的告警。
func TestTranslationFailureDegradesWithOneAlert(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)
	f.enableTranslation(true)
	f.translator.err = &translate.Error{
		Code: settings.CodeProtocol, Op: "chat", Detail: "回包没有 choices",
	}

	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("翻译失败不该让取数这一轮报错: %v", err)
	}

	metadata, exists, err := f.store.Metadata(context.Background(), firstMovie)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if !exists || metadata.TitleJA == "" || metadata.Outline == "" {
		t.Fatalf("取数成果被翻译失败挡回去了: exists=%v %+v", exists, metadata)
	}
	if metadata.TitleZH != "" || metadata.OutlineZH != "" {
		t.Errorf("失败却写了译文: %q/%q", metadata.TitleZH, metadata.OutlineZH)
	}

	version := f.version(t, firstHash)
	if version.Status != task.StatusProcessing || version.Attempts != 0 {
		t.Errorf("version = status %q attempts %d, want processing/0（翻译失败不烧取数预算）",
			version.Status, version.Attempts)
	}

	events, err := f.store.Events(context.Background(), firstMovie, 20)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var alerts []string
	for _, event := range events {
		if event.Kind == task.EventAlert && strings.HasPrefix(event.Reason, task.TranslationFailurePrefix) {
			alerts = append(alerts, event.Reason)
		}
	}
	if len(alerts) != 1 {
		t.Fatalf("翻译失败告警 = %v, want 恰好一条", alerts)
	}
	if !strings.Contains(alerts[0], "回包没有 choices") {
		t.Errorf("告警没带上失败原因: %q", alerts[0])
	}
}

// TestReusePathBackfillsTheTranslation 是「启用晚了一步」的场景：
// 元数据已在库（复用判据命中、0 次取数），译文缺失时补译一次，不重刮。
func TestReusePathBackfillsTheTranslation(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)
	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("第一轮 ProcessPending: %v", err)
	}
	want := f.setTranslation()

	// 用户中途启用翻译：下一轮命中复用，只补一次翻译请求。
	f.enableTranslation(true)
	f.dmm.calls = nil
	f.javdb.calls = nil

	progress, err := f.runner.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("第二轮 ProcessPending: %v", err)
	}
	if progress.Reused != 1 {
		t.Errorf("progress = %+v, want Reused=1", progress)
	}
	if f.dmm.hit()+f.javdb.hit() != 0 {
		t.Errorf("补译把取数重跑了一遍: dmm %d / javdb %d", f.dmm.hit(), f.javdb.hit())
	}
	if f.translator.count() != 1 {
		t.Fatalf("补译调用 = %d 次, want 1", f.translator.count())
	}

	metadata, _, err := f.store.Metadata(context.Background(), firstMovie)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if metadata.TitleZH != want.Title || metadata.OutlineZH != want.Outline {
		t.Errorf("补译没落库: %q/%q", metadata.TitleZH, metadata.OutlineZH)
	}

	// 第三轮：译文已在，复用路径不再问上游。
	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("第三轮 ProcessPending: %v", err)
	}
	if f.translator.count() != 1 {
		t.Errorf("译文已在仍然补译: %d 次", f.translator.count())
	}
}

// TestReusePathKeepsTheOldTranslationOnFailure 钉住补译失败的降级：
// 已有译文不是「失败就清空」，原文与旧译文都留着，只留一条告警。
func TestReusePathKeepsTheOldTranslationOnFailure(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)
	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("第一轮 ProcessPending: %v", err)
	}
	// 一次成功的补译。
	f.enableTranslation(true)
	f.setTranslation()
	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("第二轮 ProcessPending: %v", err)
	}

	// 把译文抹掉，再让补译失败：库里保持原样，不写空串。
	if _, err := f.database.Pool().Exec(context.Background(),
		`UPDATE task SET title_zh = '', outline_zh = '' WHERE movie_id = $1`, firstMovie); err != nil {
		t.Fatalf("清译文: %v", err)
	}
	f.translator.err = errors.New("upstream 挂了")
	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("第三轮 ProcessPending: %v", err)
	}

	metadata, _, err := f.store.Metadata(context.Background(), firstMovie)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if metadata.TitleZH != "" {
		t.Errorf("title_zh = %q, want 空（失败不写伪译文）", metadata.TitleZH)
	}
	if metadata.TitleJA == "" || metadata.Outline == "" {
		t.Errorf("日文原文丢了: %+v", metadata)
	}
}

// TestReuseBackfillRunsAtMostOncePerVersion 确认补译不会变成「每轮花一次 token」：
// 失败过的版本只补一次，告警也只留一条，之后每轮命中复用但零调用。
func TestReuseBackfillRunsAtMostOncePerVersion(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)
	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("第一轮 ProcessPending: %v", err)
	}
	f.enableTranslation(true)
	f.translator.err = &translate.Error{Code: settings.CodeNetwork, Op: "chat", Detail: "上游 503"}

	for round := 2; round <= 4; round++ {
		if _, err := f.runner.ProcessPending(context.Background()); err != nil {
			t.Fatalf("第 %d 轮 ProcessPending: %v", round, err)
		}
	}
	if f.translator.count() != 1 {
		t.Errorf("补译调用 = %d 次, want 1（失败后不再每轮重试）", f.translator.count())
	}

	events, err := f.store.Events(context.Background(), firstMovie, 20)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	alerts := 0
	for _, event := range events {
		if event.Kind == task.EventAlert && strings.HasPrefix(event.Reason, task.TranslationFailurePrefix) {
			alerts++
		}
	}
	if alerts != 1 {
		t.Errorf("翻译失败告警 = %d 条, want 1", alerts)
	}
}

// TestReuseBackfillStopsWhenTheUpstreamGivesNothing 钉住「调用成功却没译文」：
// 缺的那一半仍是空就当成一次失败（留一条告警），而不是每轮再花一次请求。
func TestReuseBackfillStopsWhenTheUpstreamGivesNothing(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)
	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("第一轮 ProcessPending: %v", err)
	}
	f.enableTranslation(true)
	f.translator.result = translate.Text{} // 上游成功但没有译文

	for round := 2; round <= 3; round++ {
		if _, err := f.runner.ProcessPending(context.Background()); err != nil {
			t.Fatalf("第 %d 轮 ProcessPending: %v", round, err)
		}
	}
	if f.translator.count() != 1 {
		t.Errorf("补译调用 = %d 次, want 1", f.translator.count())
	}
	metadata, _, err := f.store.Metadata(context.Background(), firstMovie)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if metadata.TitleZH != "" || metadata.OutlineZH != "" {
		t.Errorf("空译文被落库: %q/%q", metadata.TitleZH, metadata.OutlineZH)
	}
}

// TestTranslationDegradesAgainstARealFailingEndpoint 是 AC5 的降级面：
// 用**真的**翻译客户端打一个会 500 的 fake 端点（而不是假翻译器），证明
// 请求确实打到 {base_url}/v1/chat/completions、失败后降级日文原文、只留一条带分类的告警。
func TestTranslationDegradesAgainstARealFailingEndpoint(t *testing.T) {
	f := newFixture(t)
	f.seedProcessing(t, firstMovie, firstNum, firstHash)

	var requests []string
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
	}))
	t.Cleanup(endpoint.Close)

	f.enableTranslation(true)
	f.settings.inputs["ai_translation"] = settings.TestInput{
		Values: map[string]any{
			"base_url": endpoint.URL + "/v1", "api_key": "sk-test", "model": "gpt-4o-mini", "enabled": true,
		},
		Enabled: true,
	}
	// 换掉假翻译器：这一轮走真的 OpenAI 兼容客户端。
	f.runner = scrape.New(scrape.Options{
		Store:      f.store,
		Outbox:     sidefx.New(f.database.Pool()),
		Settings:   f.settings,
		DMM:        f.dmm,
		JavDB:      f.javdb,
		Translator: translate.New(translate.Options{}),
		Owner:      "test-scrape",
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	if _, err := f.runner.ProcessPending(context.Background()); err != nil {
		t.Fatalf("翻译失败不该让取数这一轮报错: %v", err)
	}
	if len(requests) != 1 || requests[0] != "POST /v1/chat/completions" {
		t.Errorf("请求 = %v, want 一次 POST /v1/chat/completions", requests)
	}

	metadata, exists, err := f.store.Metadata(context.Background(), firstMovie)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if !exists || metadata.TitleJA == "" {
		t.Fatalf("翻译失败把取数成果挡回去了: exists=%v %+v", exists, metadata)
	}
	if metadata.TitleZH != "" || metadata.OutlineZH != "" {
		t.Errorf("失败却写了译文: %q/%q", metadata.TitleZH, metadata.OutlineZH)
	}

	events, err := f.store.Events(context.Background(), firstMovie, 20)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	alerts := 0
	for _, event := range events {
		if event.Kind == task.EventAlert && strings.HasPrefix(event.Reason, task.TranslationFailurePrefix) {
			alerts++
			if !strings.Contains(event.Reason, string(settings.CodeNetwork)) {
				t.Errorf("告警没带上分类 %q: %q", settings.CodeNetwork, event.Reason)
			}
		}
	}
	if alerts != 1 {
		t.Errorf("翻译失败告警 = %d 条, want 1", alerts)
	}
}
