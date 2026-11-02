package db_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/dbtest"
)

func TestMigrateAppliesEmbeddedMigrationsOnce(t *testing.T) {
	d, _ := dbtest.Open(t)
	ctx := context.Background()

	applied, err := d.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("Migrate() 没应用任何迁移：内嵌的 migrations/*.sql 没被编进来？")
	}
	if applied[0] != 1 {
		t.Errorf("applied = %v, want 从版本 1 开始", applied)
	}

	again, err := d.Migrate(ctx)
	if err != nil {
		t.Fatalf("第二次 Migrate() error = %v", err)
	}
	if len(again) != 0 {
		t.Errorf("第二次 Migrate() 又应用了 %v，want 空（迁移幂等，只前进）", again)
	}
}

func TestMigrateWaitsForTheAdvisoryLock(t *testing.T) {
	d, dsn := dbtest.Open(t)
	ctx := context.Background()

	holder, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("连接（占锁方）失败: %v", err)
	}
	t.Cleanup(func() { _ = holder.Close(context.Background()) })

	var locked bool
	if err := holder.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", db.MigrationLockID).Scan(&locked); err != nil {
		t.Fatalf("pg_try_advisory_lock: %v", err)
	}
	if !locked {
		t.Fatal("拿不到 advisory lock，测试前提不成立")
	}

	done := make(chan error, 1)
	go func() {
		_, err := d.Migrate(ctx)
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("Migrate() 在别人占着 advisory lock 时仍然跑完了 (err=%v)：双实例会同时迁移", err)
	case <-time.After(500 * time.Millisecond):
	}

	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", db.MigrationLockID); err != nil {
		t.Fatalf("pg_advisory_unlock: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("放锁后 Migrate() error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("放锁后 Migrate() 没有继续：锁没有被它等回来")
	}
}

// TestMigrateIsForwardOnly 守住「只前进」：迁移文件按 000NN_name.sql 命名，且没有 Down 段。
func TestMigrateIsForwardOnly(t *testing.T) {
	files, err := db.MigrationFiles()
	if err != nil {
		t.Fatalf("MigrationFiles() error = %v", err)
	}
	if len(files) == 0 {
		t.Fatal("内嵌的迁移是空的")
	}

	namePattern := regexp.MustCompile(`^\d{5}_[a-z0-9_]+\.sql$`)
	for name, content := range files {
		if !namePattern.MatchString(name) {
			t.Errorf("迁移 %q 不符合 000NN_name.sql 命名", name)
		}
		if strings.Contains(string(content), "+goose Down") {
			t.Errorf("迁移 %q 带 Down 段：v1 只前进，不写回滚", name)
		}
	}
}
