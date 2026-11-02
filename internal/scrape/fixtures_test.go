package scrape_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/dmm"
)

// 夹具：DMM 与 JavDB 两边都用 mdc-lite 2026-10-29 的真实抓录
// （internal/dmm/testdata、internal/appapi/testdata，sha256 在各自的 fixtures.json 里）。
//
// 这里用**真实客户端**回放它们，而不是手搓结构体：字段映射的输入就是将来线上那两份
// 字节，测试才对「解析 + 映射」整体负责。DMM 对非日本出口 TLS 阻断（规格 §13），
// 抓录是唯一能在这里见到的真实形态。

func readFixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", dir, "testdata", name))
	if err != nil {
		t.Fatalf("读夹具 %s: %v", name, err)
	}
	return body
}

// dmmFixture 回放 SSIS-001 的两条搜索腿与内容腿。
func dmmFixture(t *testing.T) dmm.Result {
	t.Helper()

	search := readFixture(t, "dmm", "dmm-SSIS-001-search-ssis001.json")
	padded := readFixture(t, "dmm", "dmm-SSIS-001-search-ssis00001.json")
	content := readFixture(t, "dmm", "dmm-SSIS-001-content.json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(body, &payload)
		query := payload.Query
		switch {
		case strings.Contains(query, "ppvContent"):
			_, _ = w.Write(content)
		case strings.Contains(query, `queryWord: "ssis00001"`):
			_, _ = w.Write(padded)
		default:
			_, _ = w.Write(search)
		}
	}))
	t.Cleanup(srv.Close)

	client := dmm.New(dmm.Options{Host: srv.URL})
	result, err := client.Lookup(context.Background(), "SSIS-001")
	if err != nil {
		t.Fatalf("Lookup(SSIS-001): %v", err)
	}
	if !result.Found {
		t.Fatal("夹具没命中：夹具本身坏了")
	}
	return result
}

// javdbFixture 回放 SSIS-001 的详情端点。
func javdbFixture(t *testing.T) appapi.DetailResult {
	t.Helper()

	body := readFixture(t, "appapi", "javdb-SSIS-001-detail.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	client := &appapi.Client{Host: srv.URL, Identity: appapi.DefaultIdentity(), Signer: appapi.NewSigner()}
	result, err := client.Detail(context.Background(), "ZY5eq")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	return result
}
