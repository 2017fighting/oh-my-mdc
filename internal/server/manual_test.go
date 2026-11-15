package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/manual"
	"github.com/2017fighting/oh-my-mdc/internal/server"
)

// 手工登记的 HTTP 契约（GLOSSARY「手工登记」）：错误信封按原因分状态码，
// 成功（含 deferred / duplicate）恒 200 带 outcome。

type fakeRegistrar struct {
	result manual.Result
	err    error
	calls  []manual.Request
}

func (f *fakeRegistrar) Register(_ context.Context, request manual.Request) (manual.Result, error) {
	f.calls = append(f.calls, request)
	return f.result, f.err
}

func newManualFixture(t *testing.T, registrar server.Registrar) (http.Handler, *fakeRegistrar) {
	t.Helper()
	fake := &fakeRegistrar{}
	if registrar == nil {
		registrar = fake
	}
	return newHandler(t, server.Options{DB: pinger{}, Registrar: registrar}), fake
}

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, h, http.MethodPost, "/api/manual-tasks", body)
}

func TestManualRegisterReturnsOutcome(t *testing.T) {
	h, fake := newManualFixture(t, nil)
	fake.result = manual.Result{
		MovieID: "YwrO3B", Infohash: "1111111111111111111111111111111111111111",
		Number: "ABP-123", Outcome: manual.OutcomeAdded,
	}

	rec := post(t, h, `{"magnet":"magnet:?xt=urn:btih:1111111111111111111111111111111111111111","javdb_url":"https://javdb.com/v/YwrO3B"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	body := jsonOf(t, rec)
	if body["ok"] != true || body["outcome"] != manual.OutcomeAdded || body["number"] != "ABP-123" {
		t.Errorf("body = %v, want ok/outcome/number", body)
	}
	if len(fake.calls) != 1 || fake.calls[0].JavdbURL != "https://javdb.com/v/YwrO3B" {
		t.Errorf("registrar 收到 %+v, want 原样的两串", fake.calls)
	}
}

func TestManualRegisterRequiresAssembler(t *testing.T) {
	h := newHandler(t, server.Options{DB: pinger{}})
	rec := post(t, h, `{}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestManualRegisterRejectsBadJSON(t *testing.T) {
	h, fake := newManualFixture(t, nil)
	rec := post(t, h, "not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(fake.calls) != 0 {
		t.Errorf("坏请求体也打了登记器")
	}
}

// TestManualRegisterErrorCodes 钉住错误信封：每个哨兵一个稳定机器码与状态码。
func TestManualRegisterErrorCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		want string
	}{
		{name: "坏磁力", err: manual.ErrBadMagnet, code: http.StatusBadRequest, want: "validation_error"},
		{name: "坏链接", err: manual.ErrBadLink, code: http.StatusBadRequest, want: "validation_error"},
		{name: "番号撞车", err: manual.ErrNumberTaken, code: http.StatusConflict, want: "number_taken"},
		{name: "种子归属", err: manual.ErrInfohashTaken, code: http.StatusConflict, want: "infohash_taken"},
		{name: "门禁", err: manual.ErrGated, code: http.StatusConflict, want: "gate_closed"},
		{name: "详情读不到", err: errors.New("读 JavDB 详情 YwrO3B: connection reset"), code: http.StatusBadGateway, want: "upstream_error"},
		{name: "库故障", err: errors.New("task: 落库失败"), code: http.StatusServiceUnavailable, want: "db_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, fake := newManualFixture(t, nil)
			fake.err = tc.err

			rec := post(t, h, `{"magnet":"magnet:?xt=urn:btih:1111111111111111111111111111111111111111","javdb_url":"YwrO3B"}`)
			if rec.Code != tc.code {
				t.Fatalf("status = %d, want %d（body=%s）", rec.Code, tc.code, rec.Body.String())
			}
			body := jsonOf(t, rec)
			envelope, _ := body["error"].(map[string]any)
			if envelope == nil || envelope["code"] != tc.want {
				t.Errorf("error.code = %v, want %q", body["error"], tc.want)
			}
			if message, _ := envelope["message"].(string); strings.TrimSpace(message) == "" {
				t.Errorf("error.message 是空的，面板没文案可直译")
			}
		})
	}
}
