package dmm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/dmm"
)

// 测试缝：Client.Lookup —— 一次「番号 → 内容」的完整取数（搜索腿 + 内容腿）。
//
// 全部用 **库里的录制响应** 驱动（internal/dmm/testdata，mdc-lite 2026-10-29 的真实抓录，
// sha256 记在 fixtures.json 里）：DMM 全站对非日本出口 TLS 阻断（规格 §13），
// 所以没有别的办法在这里见到真实字节。

// fixture 读一份录制响应。
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读夹具 %s: %v", name, err)
	}
	return body
}

// recorded 是一次被 fake 看见的 GraphQL 请求。
type recorded struct {
	header http.Header
	query  string
}

// fakeDMM 是按请求体分派的 GraphQL fake：queryWord 决定搜索腿的答案，
// ppvContent(id:) 决定内容腿的答案。
type fakeDMM struct {
	mu       sync.Mutex
	requests []recorded
	// search 把 queryWord 映射到夹具名；缺省回一份空搜索结果。
	search map[string]string
	// content 是内容腿的夹具名；空则回 ppvContent:null。
	content string
	// raw 非空时无论请求什么原样回它（错误/漂移用例）。
	raw []byte
	// searchRaw 非空时只回给搜索腿（内容腿照常回 ppvContent:null）。
	searchRaw []byte
	// status 是 HTTP 状态码，0 当作 200。
	status int
}

func (f *fakeDMM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var payload struct {
		Query string `json:"query"`
	}
	_ = json.Unmarshal(body, &payload)

	f.mu.Lock()
	f.requests = append(f.requests, recorded{header: r.Header.Clone(), query: payload.Query})
	f.mu.Unlock()

	if f.status != 0 {
		w.WriteHeader(f.status)
	}

	switch {
	case f.raw != nil:
		_, _ = w.Write(f.raw)
	case f.searchRaw != nil && !strings.Contains(payload.Query, "ppvContent"):
		_, _ = w.Write(f.searchRaw)
	case strings.Contains(payload.Query, "ppvContent"):
		if f.content == "" {
			_, _ = w.Write([]byte(`{"data":{"ppvContent":null,"reviewSummary":null}}`))
			return
		}
		_, _ = w.Write(fixtureBytes(f.content))
	default: // 搜索腿
		saw := ""
		for term, name := range f.search {
			if strings.Contains(payload.Query, `queryWord: "`+term+`"`) {
				saw = name
				break
			}
		}
		if saw == "" {
			_, _ = w.Write([]byte(`{"data":{"legacySearchPPV":{"result":{"contents":[],"pageInfo":{"totalCount":0}}}}}`))
			return
		}
		_, _ = w.Write(fixtureBytes(saw))
	}
}

func (f *fakeDMM) seen() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

// fixtureBytes 在读不到时 panic：fake 的夹具名是测试常量，写错就该当场炸。
func fixtureBytes(name string) []byte {
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		panic("夹具不存在: " + name + ": " + err.Error())
	}
	return body
}

func newFake(t *testing.T, fake *fakeDMM) (*dmm.Client, *fakeDMM) {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return dmm.New(dmm.Options{Host: srv.URL}), fake
}

// liveSearch 是真实的 SSIS-001 搜索结果：95 条里没有第一条作品（ssis00001），
// 全是 ssis001NN 的邻居 —— 正是「两种零填充拼写都要试」的现场。
func liveSearch() *fakeDMM {
	return &fakeDMM{
		search: map[string]string{
			"ssis001":   "dmm-SSIS-001-search-ssis001.json",
			"ssis00001": "dmm-SSIS-001-search-ssis00001.json",
		},
		content: "dmm-SSIS-001-content.json",
	}
}

// TestLookupTriesBothZeroPaddingSpellings 钉住发现腿的两条拼写：
// ssis001 只找得到后来的作品，第一条要 ssis00001 才命中（规格 §5.3）。
func TestLookupTriesBothZeroPaddingSpellings(t *testing.T) {
	client, fake := newFake(t, liveSearch())

	result, err := client.Lookup(context.Background(), "SSIS-001")
	if err != nil {
		t.Fatalf("Lookup(SSIS-001): %v", err)
	}
	if !result.Found {
		t.Fatal("没找到 SSIS-001：两条拼写都试过才该下这个结论")
	}
	if result.Content.ID != "ssis00001" {
		t.Errorf("id = %q, want ssis00001", result.Content.ID)
	}

	requests := fake.seen()
	if len(requests) != 3 {
		t.Fatalf("请求数 = %d, want 3（两条搜索 + 一条内容）", len(requests))
	}
	if !strings.Contains(requests[0].query, `queryWord: "ssis001"`) {
		t.Errorf("第一腿 = %q, want queryWord ssis001（先试省零拼写）", requests[0].query)
	}
	if !strings.Contains(requests[1].query, `queryWord: "ssis00001"`) {
		t.Errorf("第二腿 = %q, want queryWord ssis00001", requests[1].query)
	}
	// 发现腿的固定参数（规格 §5.3）：窗口 30、AV floor、销量排序。
	for _, part := range []string{"limit: 30", "floor: AV", "sort: SALES_RANK_SCORE"} {
		if !strings.Contains(requests[0].query, part) {
			t.Errorf("搜索查询少了 %q: %s", part, requests[0].query)
		}
	}
	if !strings.Contains(requests[2].query, `ppvContent(id: "ssis00001")`) {
		t.Errorf("内容腿没按命中 id 读 ppvContent: %s", requests[2].query)
	}
	if !strings.Contains(requests[2].query, `reviewSummary(contentId: "ssis00001")`) {
		t.Errorf("内容腿没顺带读 reviewSummary: %s", requests[2].query)
	}
}

// TestLookupMatchesIdentityNotPosition 是 SSIS-0011 邻居陷阱：
// 搜索窗口里全是数值不等的前缀相同 id，按位置取第一条就会给错片。
func TestLookupMatchesIdentityNotPosition(t *testing.T) {
	fake := &fakeDMM{
		raw: []byte(`{"data":{"legacySearchPPV":{"result":{"contents":[
			{"id":"ssis00125"},{"id":"ssis00011"},{"id":"ssis0011"},{"id":"ssis001001"}
		],"pageInfo":{"totalCount":4}}}}}`),
	}
	client, replay := newFake(t, fake)

	result, err := client.Lookup(context.Background(), "SSIS-001")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if result.Found {
		t.Fatalf("把邻居当成命中：id = %q（身份 = 前缀 + 数值，不是位置）", result.Content.ID)
	}
	for _, request := range replay.seen() {
		if strings.Contains(request.query, "ppvContent") {
			t.Fatal("没命中还花了内容腿的请求")
		}
	}
}

// TestLookupMatchesNumericIdentity 确认数值相等即同一条：ssis00001 ≡ SSIS-001，
// 以及已经五位的 ssis00123 ≡ SSIS-123（同一条理由的另一面）。
func TestLookupMatchesNumericIdentity(t *testing.T) {
	cases := []struct {
		number string
		id     string
	}{
		{number: "SSIS-001", id: "ssis00001"},
		{number: "SSIS-123", id: "ssis00123"},
		{number: "IPX-001", id: "ipx00001"},
	}
	for _, tc := range cases {
		t.Run(tc.number, func(t *testing.T) {
			client, replay := newFake(t, &fakeDMM{
				searchRaw: []byte(`{"data":{"legacySearchPPV":{"result":{"contents":[{"id":"` + tc.id + `"}],"pageInfo":{"totalCount":1}}}}}`),
			})

			if _, err := client.Lookup(context.Background(), tc.number); err != nil {
				t.Fatalf("Lookup(%s): %v", tc.number, err)
			}
			// 命中的证据是「内容腿按这个 id 去了」。
			want := `ppvContent(id: "` + tc.id + `")`
			found := false
			for _, request := range replay.seen() {
				found = found || strings.Contains(request.query, want)
			}
			if !found {
				t.Fatalf("内容腿没按 %s 读（%d 个请求）", want, len(replay.seen()))
			}
		})
	}
}

// TestLookupSendsTheFiveHardHeaders 钉住「不是 HTML 抓取」的传输事实：
// 站点会拒掉不像自家播放器的请求（规格 §5.3）。
func TestLookupSendsTheFiveHardHeaders(t *testing.T) {
	client, fake := newFake(t, liveSearch())

	if _, err := client.Lookup(context.Background(), "SSIS-001"); err != nil {
		t.Fatalf("Lookup: %v", err)
	}

	want := map[string]string{
		"Referer":       "https://video.dmm.co.jp/",
		"Content-Type":  "application/json",
		"Accept":        "application/json",
		"Cache-Control": "no-cache",
		"Fanza-Device":  "BROWSER",
	}
	for index, request := range fake.seen() {
		for name, value := range want {
			if got := request.header.Get(name); got != value {
				t.Errorf("第 %d 个请求的 %s = %q, want %q", index, name, got, value)
			}
		}
		// 研究文档：Origin 从不发（是否接受未知，所以别试）。
		if got := request.header.Get("Origin"); got != "" {
			t.Errorf("第 %d 个请求带了 Origin = %q，want 空", index, got)
		}
	}
}

// TestLookupExtractsContentFields 是内容单的字段提取（验收第二条）：
// title/description/RFC3339 日期/duration 秒/绝对图片 URL，一项都不许凑。
func TestLookupExtractsContentFields(t *testing.T) {
	client, _ := newFake(t, liveSearch())

	result, err := client.Lookup(context.Background(), "SSIS-001")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	content := result.Content

	if !strings.HasPrefix(content.Title, "一ヶ月間の禁欲の果てに") {
		t.Errorf("title = %q, want 日文原题", content.Title)
	}
	if !strings.HasPrefix(content.Description, "S1スリム美女優の豪華共演") {
		t.Errorf("description = %q, want 日文简介", content.Description)
	}
	if content.ReleaseDate != "2021-02-18" {
		t.Errorf("release_date = %q, want 2021-02-18（deliveryStartDate 优先）", content.ReleaseDate)
	}
	if content.DurationSeconds != 8826 {
		t.Errorf("duration = %d, want 8826（API 的单位是秒，不是分钟）", content.DurationSeconds)
	}
	if content.PackageLargeURL != "https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/ssis00001/ssis00001pl.jpg" {
		t.Errorf("package large = %q", content.PackageLargeURL)
	}
	if content.PackageMediumURL != "https://awsimgsrc.dmm.co.jp/pics_dig/digital/video/ssis00001/ssis00001ps.jpg" {
		t.Errorf("package medium = %q", content.PackageMediumURL)
	}
	if len(content.SampleURLs) != 10 {
		t.Fatalf("预览图 = %d 张, want 10", len(content.SampleURLs))
	}
	for _, rawURL := range append([]string{content.PackageLargeURL, content.PackageMediumURL}, content.SampleURLs...) {
		if !strings.HasPrefix(rawURL, "https://") {
			t.Errorf("图片不是绝对 URL: %q", rawURL)
		}
	}
	if len(content.Actresses) != 2 || content.Actresses[0].Name != "葵つかさ" {
		t.Errorf("actresses = %+v, want 葵つかさ 起头两位", content.Actresses)
	}
	if len(content.Directors) != 1 || content.Directors[0] != "苺原" {
		t.Errorf("directors = %v, want [苺原]", content.Directors)
	}
	if content.Maker != "エスワン ナンバーワンスタイル" || content.Label != "S1 NO.1 STYLE" {
		t.Errorf("maker/label = %q/%q", content.Maker, content.Label)
	}
	if len(content.Genres) != 8 || content.Genres[0] != "3P・4P" {
		t.Errorf("genres = %v", content.Genres)
	}
	if content.ReviewAverage != 4.22 {
		t.Errorf("review average = %v, want 4.22（5 分制）", content.ReviewAverage)
	}
	if content.Series != "" {
		t.Errorf("series = %q, want 空（录制响应里是 null）", content.Series)
	}
}

// TestLookupReturnsNotFoundWhenSearchIsEmpty 确认搜不到不是故障：
// 空结果 = Found:false + 没有内容腿，调用方据此按字段回退 JavDB。
func TestLookupReturnsNotFoundWhenSearchIsEmpty(t *testing.T) {
	client, fake := newFake(t, &fakeDMM{
		search: map[string]string{
			"zzzz9999":  "dmm-ZZZZ-9999-search-zzzz9999.json",
			"zzzz09999": "dmm-ZZZZ-9999-search-zzzz09999.json",
		},
	})

	result, err := client.Lookup(context.Background(), "ZZZZ-9999")
	if err != nil {
		t.Fatalf("Lookup(ZZZZ-9999): %v", err)
	}
	if result.Found {
		t.Fatal("空搜索结果不该算命中")
	}
	if len(result.SearchRaw) == 0 {
		t.Error("跑过的搜索腿应当留一份原文（raw_snapshot 要用）")
	}
	var legs []json.RawMessage
	if err := json.Unmarshal(result.SearchRaw, &legs); err != nil || len(legs) != 2 {
		t.Errorf("搜索腿原文 = %s (err=%v), want 两条拼写各一份（证据缺一半就说不清为什么回退）",
			result.SearchRaw, err)
	}
	for _, request := range fake.seen() {
		if strings.Contains(request.query, "ppvContent") {
			t.Fatal("没有命中还花了内容腿的请求")
		}
	}
}

// TestLookupToleratesSchemaDrift 是已知风险 §13「DMM GraphQL schema 漂移」：
// 字段整块消失时按字段留空，**不失败任务**——回退交给字段映射，不在这里报错。
func TestLookupToleratesSchemaDrift(t *testing.T) {
	// 内容腿回一份缺字段的响应：只有 id/title，packageImage/genres/reviewSummary 全没了。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(readQuery(t, r), "ppvContent") {
			_, _ = w.Write([]byte(`{"data":{"ppvContent":{"id":"ssis00001","title":"只剩下的标题"}}}`))
			return
		}
		_, _ = w.Write(fixture(t, "dmm-SSIS-001-search-ssis00001.json"))
	}))
	t.Cleanup(srv.Close)
	client := dmm.New(dmm.Options{Host: srv.URL})

	result, err := client.Lookup(context.Background(), "SSIS-001")
	if err != nil {
		t.Fatalf("漂移不该失败任务: %v", err)
	}
	if !result.Found || result.Content.Title != "只剩下的标题" {
		t.Fatalf("result = %+v, want 命中且只留有的字段", result)
	}
	if result.Content.PackageLargeURL != "" || len(result.Content.SampleURLs) != 0 || len(result.Content.Genres) != 0 {
		t.Errorf("缺失字段应当留空: %+v", result.Content)
	}
	if result.Content.ReviewAverage != 0 {
		t.Errorf("review average = %v, want 0（没给）", result.Content.ReviewAverage)
	}
}

// TestLookupReportsRegionRefusal 钉住研究文档的判据：地区拒绝是 **blocked 不是 absent**，
// 必须报错让循环退避，绝不把它当成「DMM 没收录」（那会永久降级成 JavDB 元数据）。
func TestLookupReportsRegionRefusal(t *testing.T) {
	client, _ := newFake(t, &fakeDMM{
		raw: []byte(`{"errors":[{"message":"not-available-in-your-region"}]}`),
	})

	_, err := client.Lookup(context.Background(), "SSIS-001")
	if err == nil {
		t.Fatal("地区拒绝应当报错")
	}
	if !dmm.IsRegionBlocked(err) {
		t.Errorf("err = %v, want 地区阻断", err)
	}
}

// TestLookupReportsGraphQLErrors 确认 errors 键是失败（HTTP 200 也算）：
// schema 变了/查询写错了要显式报出来，而不是当成「没命中」。
func TestLookupReportsGraphQLErrors(t *testing.T) {
	client, _ := newFake(t, &fakeDMM{
		raw: []byte(`{"errors":[{"message":"Cannot query field \"makerContentId\""}]}`),
	})

	_, err := client.Lookup(context.Background(), "SSIS-001")
	if err == nil {
		t.Fatal("GraphQL errors 应当是错误")
	}
	if dmm.IsRegionBlocked(err) {
		t.Errorf("schema 错不该被当成地区阻断: %v", err)
	}
	if !strings.Contains(err.Error(), "makerContentId") {
		t.Errorf("错误信息应当带上游原文: %v", err)
	}
}

// TestLookupReportsTransportFailure 确认连不上是错误（不是「没收录」）。
func TestLookupReportsTransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	client := dmm.New(dmm.Options{Host: url})
	if _, err := client.Lookup(context.Background(), "SSIS-001"); err == nil {
		t.Fatal("连不上应当报错")
	}
}

// TestLookupRequiresANumber 确认缺番号在本地就报错，不空打上游。
func TestLookupRequiresANumber(t *testing.T) {
	client, replay := newFake(t, liveSearch())

	if _, err := client.Lookup(context.Background(), "  "); err == nil {
		t.Fatal("空番号应当报错")
	}
	if len(replay.seen()) != 0 {
		t.Errorf("空番号发了 %d 个请求，want 0", len(replay.seen()))
	}
}

func readQuery(t *testing.T, r *http.Request) string {
	t.Helper()
	var payload struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Fatalf("读请求体: %v", err)
	}
	return payload.Query
}

// TestFixturesMatchTheirRecordedHashes 钉住夹具的出处：
// 每份录制响应的 sha256 与 fixtures.json 记录的一致。
//
// 夹具是「DMM 今天到底长什么样」的唯一证据（开发机与 CI 都到不了 DMM，规格 §13）。
// 允许悄悄改夹具，就等于允许把测试改成迎合实现 —— 这条测试挡住那件事。
func TestFixturesMatchTheirRecordedHashes(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join("testdata", "fixtures.json"))
	if err != nil {
		t.Fatalf("读 fixtures.json: %v", err)
	}
	var entries []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(manifest, &entries); err != nil {
		t.Fatalf("fixtures.json 不是 JSON: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("fixtures.json 是空的")
	}
	for _, entry := range entries {
		sum := sha256.Sum256(fixture(t, entry.Path))
		if got := hex.EncodeToString(sum[:]); got != entry.SHA256 {
			t.Errorf("%s 的 sha256 = %s, want %s（录制响应被改过）", entry.Path, got, entry.SHA256)
		}
	}
}
