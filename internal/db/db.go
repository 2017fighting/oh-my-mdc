// Package db 拥有 Postgres 连接与内嵌的 goose 迁移。
//
// 迁移只前进（ADR-0001）：启动时自动 Up，Up 前先抢一把 advisory lock 防双实例同迁。
// 任何失败都只回报给调用方——启动不因此 abort，页面照开、/readyz 回 503（规格 §4.4）。
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// MigrationLockID 是迁移用的 advisory lock key，沿用 goose 的默认值。
//
// 它是对外契约的一部分：运维与测试要能拿同一把锁判断「谁在迁移」。
const MigrationLockID int64 = lock.DefaultLockID

// 迁移锁的等锁节奏：每秒探一次，最多等一分钟。滚动更新时新旧 pod 的重叠通常只要几秒。
const (
	lockRetryPeriod  uint64 = 1
	lockRetryCount   uint64 = 60
	unlockRetryCount uint64 = 30
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB 持有连接池，并向下游提供 ping 与迁移两件事。
type DB struct {
	pool *pgxpool.Pool
}

// connectTimeout 是一次建连最多等多久：数据库不可达时应快速失败成「未就绪」，
// 而不是把启动挂上几分钟。
const connectTimeout = 10 * time.Second

// Open 建好连接池。它不 ping：启动期连不上数据库不是致命错误，
// 真实状态由 /readyz 与错误日志表达。
func Open(ctx context.Context, dsn string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 Postgres DSN: %w", err)
	}
	cfg.ConnConfig.ConnectTimeout = connectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("建立 Postgres 连接池: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Ping 用一次往返确认数据库可用。
func (d *DB) Ping(ctx context.Context) error {
	return d.pool.Ping(ctx)
}

// Close 释放连接池。
func (d *DB) Close() {
	d.pool.Close()
}

// Pool 把连接池交给需要事务的上层（设置域的读-校验-写）。
// 它不复制、不包装：拿到的就是同一个池，别再往下传。
func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}

// Migrate 跑完全部待应用的迁移，返回本轮应用的版本号（没有待应用的则返回空）。
//
// 迁移失败只返回错误，不重试也不 panic：调用方记下原因继续起服务。
func (d *DB) Migrate(ctx context.Context) ([]int64, error) {
	migrations, err := migrationFS()
	if err != nil {
		return nil, err
	}

	sqlDB := stdlib.OpenDBFromPool(d.pool)
	defer func() { _ = sqlDB.Close() }()

	locker, err := lock.NewPostgresSessionLocker(
		lock.WithLockID(MigrationLockID),
		lock.WithLockTimeout(lockRetryPeriod, lockRetryCount),
		lock.WithUnlockTimeout(lockRetryPeriod, unlockRetryCount),
	)
	if err != nil {
		return nil, fmt.Errorf("建迁移锁: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("装配迁移: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("执行迁移: %w", err)
	}

	applied := make([]int64, 0, len(results))
	for _, result := range results {
		applied = append(applied, result.Source.Version)
	}
	sort.Slice(applied, func(i, j int) bool { return applied[i] < applied[j] })
	return applied, nil
}

// MigrationFiles 返回内嵌的迁移文件（文件名 → 内容），供守序测试与运维检查使用。
func MigrationFiles() (map[string][]byte, error) {
	migrations, err := migrationFS()
	if err != nil {
		return nil, err
	}

	entries, err := fs.ReadDir(migrations, ".")
	if err != nil {
		return nil, fmt.Errorf("读迁移目录: %w", err)
	}

	files := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".sql" {
			continue
		}
		content, err := fs.ReadFile(migrations, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("读迁移 %s: %w", entry.Name(), err)
		}
		files[entry.Name()] = content
	}
	return files, nil
}

func migrationFS() (fs.FS, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("定位内嵌迁移目录: %w", err)
	}
	return sub, nil
}
