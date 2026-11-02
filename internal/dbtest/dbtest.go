// Package dbtest 给需要真 Postgres 的测试提供一次性数据库。
//
// 它不是业务代码：只被 _test.go 引用。MDC_TEST_DATABASE_URL 没设就跳过 —— CI 与
// `make test-local` 会设，普通 `go test` 不会。
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/2017fighting/oh-my-mdc/internal/db"
)

// Env 指向一个可建库的 Postgres DSN。
const Env = "MDC_TEST_DATABASE_URL"

// dsn 在测试用 Postgres 上建一个一次性数据库并返回它的 DSN；测试结束自动删库。
// 未配置 Env 时跳过当前测试。
func dsn(t *testing.T) string {
	t.Helper()

	adminDSN := os.Getenv(Env)
	if adminDSN == "" {
		t.Skipf("%s 未设置：跳过需要 Postgres 的集成测试（make test 会设它）", Env)
	}

	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("连接 %s 失败: %v", Env, err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })

	name := "mdc_test_" + randHex(t)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE %s: %v", name, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name)); err != nil {
			t.Logf("DROP DATABASE %s: %v", name, err)
		}
	})

	return dsnFor(t, adminDSN, name)
}

// Open 建一个一次性库并连上它（不迁移），返回连接与它的 DSN。
func Open(t *testing.T) (*db.DB, string) {
	t.Helper()

	databaseDSN := dsn(t)
	d, err := db.Open(context.Background(), databaseDSN)
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(d.Close)
	return d, databaseDSN
}

// Migrated 返回一个连上一次性库、且已跑完全部 goose 迁移的 *db.DB。
func Migrated(t *testing.T) *db.DB {
	t.Helper()

	d, _ := Open(t)
	if _, err := d.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return d
}

func dsnFor(t *testing.T, adminDSN, database string) string {
	t.Helper()

	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", Env, err)
	}
	u.Path = "/" + database
	return u.String()
}

func randHex(t *testing.T) string {
	t.Helper()

	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(buf[:])
}
