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
	"github.com/2017fighting/oh-my-mdc/internal/dmm"
	"github.com/2017fighting/oh-my-mdc/internal/javdb"
	"github.com/2017fighting/oh-my-mdc/internal/jellyfin"
	"github.com/2017fighting/oh-my-mdc/internal/loops"
	"github.com/2017fighting/oh-my-mdc/internal/mteam"
	"github.com/2017fighting/oh-my-mdc/internal/organize"
	"github.com/2017fighting/oh-my-mdc/internal/poll"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/reconcile"
	"github.com/2017fighting/oh-my-mdc/internal/scrape"
	"github.com/2017fighting/oh-my-mdc/internal/server"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
	"github.com/2017fighting/oh-my-mdc/internal/telegram"
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
	var mteamIntegration *mteam.Integration
	var qbtIntegration *qbittorrent.Integration
	var jellyfinIntegration *jellyfin.Integration
	var telegramIntegration *telegram.Integration
	var taskStore *task.Store
	var sidefxOutbox *sidefx.Outbox
	var loopsRegistry *loops.Registry
	var reconcileRunner *reconcile.Runner
	// loopDone 是循环退出的信号：关连接池之前要先等它们，别把在途查询踩掉。
	var loopDone []chan struct{}
	if database != nil {
		pinger = database

		// JavDB 集成门面：既是设置域的「测试读取」实现，也是向导探测/代建清单的客户端。
		javdbIntegration = javdb.New(javdb.Options{})
		// M-Team 集成门面：设置域的「测试检索」实现 + poll 的取种来源（可选集成）。
		mteamIntegration = mteam.New(mteam.Options{})
		// qBittorrent 集成门面：设置域的「测试连接」实现 + 向导的 autorun 对比/落地。
		qbtIntegration = qbittorrent.New(qbittorrent.Options{})
		// Jellyfin 集成门面：设置域的「测试可写」实现 + 目录级刷新（由 sidefx 引擎消费）。
		jellyfinIntegration = jellyfin.New(jellyfin.Options{})
		// Telegram 集成门面：设置域的「两步测试」实现 + sidefx 的通知执行者。
		telegramIntegration = telegram.New(telegram.Options{})
		// 任务域读模型与副作用出箱：面板与 poll 循环共用。
		taskStore = task.NewStore(database.Pool())
		sidefxOutbox = sidefx.New(database.Pool())
		loopsRegistry = loops.NewRegistry()
		settingsStore = settings.NewStore(database.Pool(),
			settings.WithTester("javdb", javdbIntegration),
			settings.WithTester("qbittorrent", qbtIntegration),
			settings.WithTester("jellyfin", jellyfinIntegration),
			settings.WithTester("mteam", mteamIntegration),
			settings.WithTester("telegram", telegramIntegration))

		// 读设置是启动顺序的一环（规格 §4.4）：这一次读失败说明设置域不可用，
		// 与迁移失败同等对待——照常监听，但 /readyz 回 503 带原因。
		if unready == nil {
			snapshot, err := settingsStore.Load(ctx)
			if err != nil {
				logger.Error("读取设置失败", "err", err)
				unready = fmt.Errorf("读取设置失败: %w", err)
			} else {
				logger.Info("设置已读取", "initialized", snapshot.Initialized, "paused", snapshot.Paused)

				// 刮削取数（加工第一段）：版本进 processing 后取齐元数据三面，
				// 取完仍驻 processing 等整理票（#29）。DMM 的日本出口由集群侧保证（§13）。
				scrapeRunner := scrape.New(scrape.Options{
					Store:    taskStore,
					Outbox:   sidefxOutbox,
					Settings: settingsStore,
					DMM:      dmm.New(dmm.Options{}),
					JavDB:    javdbIntegration,
					Logger:   logger,
				})

				// 整理入库（加工第二段）：硬链进媒体库 + NFO/图片 + 换 tag + 置刷库/通知待办。
				// 图片走 HTTP（DMM/JavDB 的绝对 URL），硬链的跨设备判据归文件系统（EXDEV 即失败）。
				organizeRunner := organize.New(organize.Options{
					Store:    taskStore,
					Outbox:   sidefxOutbox,
					Settings: settingsStore,
					Qbt:      qbtIntegration,
					Tags:     qbtIntegration,
					Images:   organize.NewHTTPImages(nil),
					Logger:   logger,
				})

				// 下载完成的第二条通道：qbt 回调（即时）+ 对账循环（5 分钟）。
				// 两者共用一个推进引擎，互斥靠版本行租约（规格 §4.4）。
				// 对账同时推进 processing：取数段交给 scrape（§6.2）。
				reconcileRunner = reconcile.New(reconcile.Options{
					Store:     taskStore,
					Outbox:    sidefxOutbox,
					Settings:  settingsStore,
					Qbt:       qbtIntegration,
					Pool:      database.Pool(),
					Loops:     loopsRegistry,
					Logger:    logger,
					Processor: scrapeRunner,
					Organizer: organizeRunner,
				})
				// 重启复位（规格 §6.1 重启行）：只复位 processing，其它状态一概不动。
				if reset, err := reconcileRunner.ResetOnStartup(ctx); err != nil {
					logger.Error("重启复位加工阶段失败", "err", err)
				} else if reset > 0 {
					logger.Info("重启复位加工阶段", "count", reset)
				}

				// 循环随进程起，每轮自己看门禁（未初始化/已暂停就零动作）：
				// 向导配完三件套后不必重启，下一个探路周期就会开始轮询。
				pollRunner := poll.New(poll.Options{
					Store:    taskStore,
					Outbox:   sidefxOutbox,
					Settings: settingsStore,
					Source:   javdbIntegration,
					Adder:    qbtIntegration,
					// M-Team 是可选来源：未配置/禁用时 poll 自己不会问它（P5-1）。
					Torrents: mteamIntegration,
					Pool:     database.Pool(),
					Loops:    loopsRegistry,
					Logger:   logger,
				})
				pollDone := make(chan struct{})
				go func() {
					defer close(pollDone)
					pollRunner.Run(ctx)
				}()
				loopDone = append(loopDone, pollDone)
				logger.Info("poll 循环已启动")

				reconcileDone := make(chan struct{})
				go func() {
					defer close(reconcileDone)
					reconcileRunner.Run(ctx)
				}()
				loopDone = append(loopDone, reconcileDone)
				logger.Info("reconcile 循环已启动")

				// 旁路副作用引擎：删清单项 / 刷 Jellyfin / 发通知（规格 §6.2 的 sidefx 通道）。
				// 它自己入箱（把 list_removal_pending 翻成待办）、自己重试，失败不阻塞任务。
				sidefxEngine := sidefx.NewEngine(sidefx.Options{
					Outbox:    sidefxOutbox,
					Settings:  settingsStore,
					Remover:   javdbIntegration,
					Refresher: jellyfinIntegration,
					Notifier:  telegramIntegration,
					Tasks:     taskStore,
					Pool:      database.Pool(),
					Loops:     loopsRegistry,
					Logger:    logger,
				})
				sidefxDone := make(chan struct{})
				go func() {
					defer close(sidefxDone)
					sidefxEngine.Run(ctx)
				}()
				loopDone = append(loopDone, sidefxDone)
				logger.Info("sidefx 循环已启动")

				// defer 顺序：这里注册的最先执行——先取消 ctx，等循环退出，
				// 之后才是 database.Close（它在更早处注册）。
				defer func() {
					stop()
					deadline := time.After(shutdownGrace)
					for _, done := range loopDone {
						select {
						case <-done:
						case <-deadline:
							logger.Warn("循环未在宽限内退出")
							return
						}
					}
				}()
			}
		}
	}

	// nil 指针装进接口会变成非 nil 接口：引擎没起来时明确传 nil。
	var hooks server.Hooks
	if reconcileRunner != nil {
		hooks = reconcileRunner
	}

	handler, err := server.New(server.Options{
		Version:  version,
		DB:       pinger,
		Assets:   web.Assets(),
		Unready:  unready,
		Settings: settingsStore,
		JavDB:    javdbIntegration,
		Qbt:      qbtIntegration,
		Tasks:    taskStore,
		Sidefx:   sidefxOutbox,
		Loops:    loopsRegistry,
		Hooks:    hooks,
		Logger:   logger,
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
