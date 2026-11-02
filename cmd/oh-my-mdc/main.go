// Command oh-my-mdc 是单二进制服务：内嵌前端 + Postgres。
//
// 启动顺序照规格 §4.4：迁移（goose Up，advisory lock）→ 读设置 → 起监听。
// 迁移或读设置失败都不 abort：页面照开，/readyz 回 503 db_unavailable 并带原因。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/config"
	"github.com/2017fighting/oh-my-mdc/internal/db"
	"github.com/2017fighting/oh-my-mdc/internal/javdb"
	"github.com/2017fighting/oh-my-mdc/internal/server"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/web"
)

// version 由 -ldflags -X main.version=v1.2.3 注入，未注入时为 dev。
var version = "dev"

// shutdownGrace 是收到终止信号后留给在途请求的时间。
const shutdownGrace = 10 * time.Second

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("进程退出", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 启动日志一行：版本 + 监听地址，发布核对用。
	logger.Info("oh-my-mdc 启动", "version", version, "listen", cfg.Listen)

	database, unready := connectAndMigrate(ctx, cfg, logger)
	if database != nil {
		defer database.Close()
	}

	var pinger server.Pinger
	var settingsStore *settings.Store
	var javdbIntegration *javdb.Integration
	if database != nil {
		pinger = database

		// JavDB 集成门面：既是设置域的「测试读取」实现，也是向导探测/代建清单的客户端。
		javdbIntegration = javdb.New(javdb.Options{})
		settingsStore = settings.NewStore(database.Pool(),
			settings.WithTester("javdb", javdbIntegration))

		// 读设置是启动顺序的一环（规格 §4.4）：这一次读失败说明设置域不可用，
		// 与迁移失败同等对待——照常监听，但 /readyz 回 503 带原因。
		if unready == nil {
			snapshot, err := settingsStore.Load(ctx)
			if err != nil {
				logger.Error("读取设置失败", "err", err)
				unready = fmt.Errorf("读取设置失败: %w", err)
			} else {
				logger.Info("设置已读取", "initialized", snapshot.Initialized, "paused", snapshot.Paused)
			}
		}
	}

	handler, err := server.New(server.Options{
		Version:  version,
		DB:       pinger,
		Assets:   web.Assets(),
		Unready:  unready,
		Settings: settingsStore,
		JavDB:    javdbIntegration,
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- fmt.Errorf("监听 %s: %w", cfg.Listen, err)
			return
		}
		errc <- nil
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	logger.Info("收到终止信号，停止接收请求")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("优雅停止: %w", err)
	}
	return nil
}

// connectAndMigrate 建池并跑迁移。任何失败都只降级为「未就绪」：
// 返回的 error 交给 /readyz 原样解释原因，进程照常监听。
func connectAndMigrate(ctx context.Context, cfg config.Config, logger *slog.Logger) (*db.DB, error) {
	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("数据库连接池创建失败", "err", err)
		return nil, fmt.Errorf("数据库连接池创建失败: %w", err)
	}

	applied, err := database.Migrate(ctx)
	if err != nil {
		logger.Error("数据库迁移失败", "err", err)
		return database, fmt.Errorf("数据库迁移失败: %w", err)
	}
	logger.Info("数据库迁移完成", "applied", applied)

	return database, nil
}
