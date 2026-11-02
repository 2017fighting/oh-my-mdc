package appapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// clientFor 起一个 HTTP fake，返回一个指向它的 Client。
//
// 测试只按**请求形状**说话（方法、路径、query、头、body），不碰内部函数 ——
// 与 javdb_rss 的先例一致：HTTP fake 是锁「我们发给上游的到底是什么」的唯一接缝。
func clientFor(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{
		Host:     srv.URL,
		Identity: DefaultIdentity(),
		Signer:   NewSigner(),
	}, srv
}

// envBody 把负载包进上游的响应信封。服务发的每个请求都会先解信封再把 data 交给调用方。
func envBody(data string) string {
	return `{"success":1,"action":null,"message":null,"data":` + data + `}`
}

// TestRequestCarriesSignatureAndPublicParams 锁一次 GET 的完整请求形状：
// jdsignature 头、8 个公共 query 参数、Bearer 头、accept-language。
func TestRequestCarriesSignatureAndPublicParams(t *testing.T) {
	var got *http.Request
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		_, _ = w.Write([]byte(envBody(`{"lists":[]}`)))
	})
	c.Token = "tk"
	c.Lang = "zh-CN"

	if _, err := c.Lists(context.Background()); err != nil {
		t.Fatalf("Lists: %v", err)
	}

	if got.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", got.Method)
	}
	if got.URL.Path != "/api/v1/lists/simple" {
		t.Errorf("path = %q, want /api/v1/lists/simple", got.URL.Path)
	}
	if sig := got.Header.Get("jdsignature"); sig == "" {
		t.Error("缺 jdsignature 头 —— 服务端会返回 InvalidSignature/ParameterInvalid")
	}
	if auth := got.Header.Get("authorization"); auth != "Bearer tk" {
		t.Errorf("authorization = %q, want Bearer tk", auth)
	}
	if lang := got.Header.Get("accept-language"); lang != "zh-CN" {
		t.Errorf("accept-language = %q, want zh-CN", lang)
	}

	// 8 个公共参数一个都不能少。
	q := got.URL.Query()
	for _, k := range []string{
		"app_channel", "app_version", "app_version_number", "platform",
		"system_version", "device_model", "device_name", "device_uuid",
	} {
		if q.Get(k) == "" {
			t.Errorf("公共 query 参数 %q 缺失", k)
		}
	}
}

// TestBusinessErrorIsSuccessZeroWithNullAction 钉住 #16 的分层：
// 业务校验错 = success:0 + action:null，归类为业务错（不重试、不是凭据问题）。
func TestBusinessErrorIsSuccessZeroWithNullAction(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		// 名字过短的实测原文。
		_, _ = w.Write([]byte(`{"success":0,"action":null,"message":"名稱 過短（最短爲 4 個字符）","data":null}`))
	})
	c.Token = "tk"

	// 名字长度合规（过 4–30 本地校验），让请求真的走到上游，验证业务错分类。
	_, err := c.CreateList(context.Background(), "想看清单", "")
	if err == nil {
		t.Fatal("业务校验错应当报错")
	}
	if !IsBusinessError(err) {
		t.Errorf("err = %v, want 业务错（action 为空）", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err 应当是 *APIError，得到 %T", err)
	}
	if apiErr.Action != "" {
		t.Errorf("action = %q, want 空（业务错）", apiErr.Action)
	}
	if !strings.Contains(apiErr.Message, "過短") {
		t.Errorf("message = %q, want 原样透出上游文案", apiErr.Message)
	}
}

// TestAuthErrorOnJWTVerification 钉住：action=JWTVerificationError → AuthError。
// 这与业务错、网络错都必须可区分（规格 P1-2）。
func TestAuthErrorOnJWTVerification(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":0,"action":"JWTVerificationError","message":"token 无效","data":null}`))
	})
	c.Token = "bad"

	_, err := c.Lists(context.Background())
	if err == nil {
		t.Fatal("坏 token 应当报错")
	}
	if !IsAuthError(err) {
		t.Errorf("err = %v, want AuthError", err)
	}
	if IsBusinessError(err) {
		t.Error("AuthError 不该被当成业务错")
	}
}

// TestSignatureErrorIsNotAuth 钉住：InvalidSignature 是传输/代码错，不是凭据错。
func TestSignatureErrorIsNotAuth(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"success":0,"action":"InvalidSignature","message":"無效的簽名","data":null}`))
	})

	_, err := c.Lists(context.Background())
	if err == nil {
		t.Fatal("签名无效应当报错")
	}
	if IsAuthError(err) {
		t.Error("InvalidSignature 不是凭据错（要改代码，不是换 token）")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Action != "InvalidSignature" {
		t.Fatalf("err = %v, want *APIError action=InvalidSignature", err)
	}
	// 即使 HTTP 是 400，也仍然按信封分类，而不是变成字符串错误。
	if apiErr.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", apiErr.Status)
	}
}

// TestNonEnvelopeHTTPErrorIsPlainError 钉住：不是信封的响应（裸 500）不冒充 APIError。
func TestNonEnvelopeHTTPErrorIsPlainError(t *testing.T) {
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":500,"error":"Internal Server Error"}`))
	})

	_, err := c.Lists(context.Background())
	if err == nil {
		t.Fatal("裸 500 应当报错")
	}
	if IsAuthError(err) || IsBusinessError(err) {
		t.Errorf("err = %v, 裸 HTTP 错不该被归成 auth/business", err)
	}
}

// TestCreateListPostsFormEncoded 锁建清单的请求形状：
// POST + application/x-www-form-urlencoded（JSON 被拒）+ name/description 字段 + 取 data.id。
func TestCreateListPostsFormEncoded(t *testing.T) {
	var got *http.Request
	var body string
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(envBody(`{"id":"e8r1OM","name":"想看清单","privacy":"open"}`)))
	})
	c.Token = "tk"

	id, err := c.CreateList(context.Background(), "想看清单", "oh-my-mdc 自动创建")
	if err != nil {
		t.Fatalf("CreateList: %v", err)
	}
	if id != "e8r1OM" {
		t.Errorf("id = %q, want e8r1OM（data.id）", id)
	}

	if got.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", got.Method)
	}
	if got.URL.Path != "/api/v1/lists" {
		t.Errorf("path = %q, want /api/v1/lists", got.URL.Path)
	}
	if ct := got.Header.Get("content-type"); ct != "application/x-www-form-urlencoded" {
		t.Errorf("content-type = %q, want form 编码", ct)
	}
	form, err := url.ParseQuery(body)
	if err != nil {
		t.Fatalf("body 不是 form: %v (%q)", err, body)
	}
	if form.Get("name") != "想看清单" {
		t.Errorf("name = %q", form.Get("name"))
	}
	if form.Get("description") != "oh-my-mdc 自动创建" {
		t.Errorf("description = %q", form.Get("description"))
	}
}

// TestCreateListRejectsEmptyNameLocally 确认空名在本地就被拦下，不白打一次上游。
func TestCreateListRejectsEmptyNameLocally(t *testing.T) {
	called := false
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte(envBody(`{"id":"x"}`)))
	})

	if _, err := c.CreateList(context.Background(), "  ", ""); err == nil {
		t.Fatal("空名应当在本地报错")
	}
	if called {
		t.Error("空名不该发请求")
	}
}

// TestCreateListEnforcesNameLengthLocally 钉住服务端的 4–30 字符规则：本地先拦，
// 不白打一次上游（同时证明「默认名合不合规」是一条可测的事实）。
func TestCreateListEnforcesNameLengthLocally(t *testing.T) {
	called := false
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte(envBody(`{"id":"x"}`)))
	})

	for _, name := range []string{"看", "想看", "想看清"} {
		if _, err := c.CreateList(context.Background(), name, ""); err == nil {
			t.Errorf("名字 %q（%d 字）应当在本地报错（短于 4）", name, len([]rune(name)))
		}
	}
	tooLong := strings.Repeat("长", MaxListNameLen+1)
	if _, err := c.CreateList(context.Background(), tooLong, ""); err == nil {
		t.Errorf("名字 %d 字应当在本地报错（长于 %d）", len([]rune(tooLong)), MaxListNameLen)
	}
	if called {
		t.Error("长度不合规不该发请求")
	}
}

// TestListItemsUsesEntityLetterL 钉住清单项读的 filter_by 形状（0:l:{id}）。
//
// 实体字母写错或 zone 写成非 0，上游都不报错、只返回别的作品 —— 这是最容易静默错的一处。
func TestListItemsUsesEntityLetterL(t *testing.T) {
	var gotFilter string
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		gotFilter = r.URL.Query().Get("filter_by")
		_, _ = w.Write([]byte(envBody(`{"movies":[
			{"id":"YwrO3B","number":"ATID-637"},
			{"id":"m2","number":"SSIS-001"}
		]}`)))
	})
	c.Token = "tk"

	items, err := c.ListItems(context.Background(), "k4EVE4")
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if gotFilter != "0:l:k4EVE4" {
		t.Errorf("filter_by = %q, want 0:l:k4EVE4", gotFilter)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].MovieID != "YwrO3B" || items[0].Number != "ATID-637" {
		t.Errorf("第一项 = %+v", items[0])
	}
}

// TestListItemsRejectsEmptyIDLocally 确认空清单 id 在本地就被拦下。
func TestListItemsRejectsEmptyIDLocally(t *testing.T) {
	called := false
	c, _ := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte(envBody(`{"movies":[]}`)))
	})
	if _, err := c.ListItems(context.Background(), ""); err == nil {
		t.Fatal("空清单 id 应当在本地报错")
	}
	if called {
		t.Error("空清单 id 不该发请求")
	}
}

// TestListsReadsEveryPageUntilEmpty 钉住清单列表翻页：读到空页即止，按 id 去重。
func TestListsReadsEveryPageUntilEmpty(t *testing.T) {
	pages := map[string]string{
		"1": `{"lists":[{"id":"k4EVE4","name":"遥控跳弹","privacy":"open","is_default":false,"movies_count":1},
		                 {"id":"R9r77","name":"default","privacy":"own","is_default":true,"movies_count":6}]}`,
		"2": `{"lists":[]}`,
	}
	var requested []string
	c, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		requested = append(requested, page)
		_, _ = w.Write([]byte(envBody(pages[page])))
	})
	c.Token = "tk"

	lists, err := c.Lists(context.Background())
	if err != nil {
		t.Fatalf("Lists: %v", err)
	}
	if len(lists) != 2 {
		t.Fatalf("lists = %d, want 2", len(lists))
	}
	if lists[0].ID != "k4EVE4" || lists[0].Name != "遥控跳弹" || lists[0].MoviesCount != 1 {
		t.Errorf("第一份清单 = %+v", lists[0])
	}
	if !lists[1].IsDefault {
		t.Errorf("is_default 没透出: %+v", lists[1])
	}
	if len(requested) != 2 || requested[1] != "2" {
		t.Errorf("翻页序列 = %v, want [1 2]（第 2 页空即止）", requested)
	}
}

// TestClassifyAndPermanentLockTheMetricHook 钉住 §9 上游指标的分类钩子：
// auth/protocol 是 permanent（重试无用），business 归 400，network 是 transient。
func TestClassifyAndPermanentLockTheMetricHook(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		code      string
		permanent bool
	}{
		{"凭据失效", &AuthError{Action: "JWTVerificationError"}, CodeAuth, true},
		{"签名不兼容", &APIError{Action: "InvalidSignature"}, CodeProtocol, true},
		{"业务错", &APIError{Message: "同名"}, CodeBusiness, false},
		{"本地校验", &ValidationError{Message: "短"}, CodeBusiness, false},
		{"裸 5xx", &ProtocolError{Path: "/x", Status: 500}, CodeProtocol, true},
		{"网络", &TransportError{Path: "/x", Err: context.DeadlineExceeded}, CodeNetwork, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.err); got != tc.code {
				t.Errorf("Classify = %q, want %q", got, tc.code)
			}
			if got := IsPermanent(tc.err); got != tc.permanent {
				t.Errorf("IsPermanent = %v, want %v", got, tc.permanent)
			}
		})
	}
}
