// Package poll 是清单轮询循环：JavDB 想看清单全量扫到底，把每一件待办交给共用的 ingest。
//
// 它是第一条真实流水线切片（规格 §6.2 的 poll 通道），自己只管清单这一侧的几件事：
//
//   - 单飞：一轮 poll 先抢一把 pg_advisory_lock（专用连接），抢不到就跳过；
//   - 门禁：三件套不齐或已暂停时零动作（面板那边整站只剩向导）；
//   - 清单：读出「想看」那张清单的全部条目（空页即止）；读失败也照旧推进加种，
//     那是独立于上游的活；
//   - 记账：把一轮的计数与循环健康位填好（§9 的 poll 指标从这里来）。
//
// 「一件待办作品之后怎么走」不属于本包：定源（M-Team 优先、JavDB 磁力兜底）→ 先落库
// → 加种 → 置待删都在 internal/ingest，与女优订阅（#47）共用同一段（规格 §6.2）。
// 本包对它的全部要求就是「产出一批待办作品」——一件 item 一个 ingest.Work。
//
// 真正删清单项、刷 Jellyfin、发 Telegram 由旁路引擎执行（#26）：本包只入箱。
package poll

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/ingest"
	"github.com/2017fighting/oh-my-mdc/internal/loops"
	"github.com/2017fighting/oh-my-mdc/internal/material"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// LockIDPoll 是 poll 的 advisory lock key。三条循环各持一把（规格 §4.4）。
const LockIDPoll int64 = 4230001

// DefaultInterval 是 app_config 读不到 poll_interval 时的兜底（与表默认值一致）。
const DefaultInterval = 15 * time.Minute

// GateCheckInterval 是「门禁还没开」时的探路节奏：配完向导不必等满一个 15 分钟周期。
const GateCheckInterval = 30 * time.Second

// Settings 是本循环对设置域的全部要求。
type Settings interface {
	Load(ctx context.Context) (settings.Snapshot, error)
	Input(ctx context.Context, section string) (settings.TestInput, error)
}

// Source 是清单与磁力候选的上游（真实实现是 javdb.Integration）。
//
// AllListItems 是**全量**扫到底（空页即止），与 appapi.Client.ListItems 只读第一页不同。
// Magnets 是每件作品的磁力候选：它属于 ingest 那一段（定源），本包只把上游原样递过去
// ——读清单与查磁力用的是同一张 JavDB 票。
type Source interface {
	AllListItems(ctx context.Context, token, listID string) ([]appapi.ListItem, error)
	Magnets(ctx context.Context, token, movieID string) ([]appapi.Magnet, error)
}

// Options 装配一个 poll 循环。
type Options struct {
	Store    *task.Store
	Outbox   *sidefx.Outbox
	Settings Settings
	Source   Source
	// Adder / Torrents / Material 是 ingest 那一段的依赖（定源、加种、交付材料）：
	// 本包自己不用它们，只递过去，见 ingest.Options。
	Adder    ingest.Adder
	Torrents ingest.Torrents
	Material *material.Store
	// Pool 只用于 advisory lock 的专用连接；nil 时不做单飞（测试或单进程场景）。
	Pool *pgxpool.Pool
	// Loops 记循环健康位；nil 时只跑不记。
	Loops *loops.Registry
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
	// Owner 是 claim 的持有者标识；空则用 hostname-pid。
	Owner string
	// Now 可注入测试时钟；空则 time.Now。
	Now func() time.Time
}

// Runner 是 poll 循环。
type Runner struct {
	ingest   *ingest.Ingester
	settings Settings
	source   Source
	pool     *pgxpool.Pool
	loops    *loops.Registry
	logger   *slog.Logger
	now      func() time.Time
}

// New 构造循环。
func New(opts Options) *Runner {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	return &Runner{
		ingest: ingest.New(ingest.Options{
			Store:    opts.Store,
			Outbox:   opts.Outbox,
			Settings: opts.Settings,
			Magnets:  opts.Source,
			Adder:    opts.Adder,
			Torrents: opts.Torrents,
			Material: opts.Material,
			Logger:   logger,
			Owner:    opts.Owner,
		}),
		settings: opts.Settings,
		source:   opts.Source,
		pool:     opts.Pool,
		loops:    opts.Loops,
		logger:   logger,
		now:      now,
	}
}

// Result 是一轮 poll 的可观察结果（日志、测试与面板健康位共用）。
type Result struct {
	// Gated 表示门禁没开（未初始化/已暂停），这一轮没有做任何事。
	Gated bool
	// Locked 表示另一实例正跑着 poll，这一轮让给了它。
	Locked bool
	// Reason 是跳过时给人看的一句话。
	Reason string
	// Scanned 是本轮清单项数；Queried 是真正查了磁力的项数。
	Scanned int
	Queried int
	// Candidates 是新建的版本行数；Duplicates 是已存在（零写入）的候选数。
	Candidates int
	Duplicates int
	// Torrents 是本轮 M-Team 取种成功（落成 mteam_torrent 版本）的条数。
	Torrents int
	// Fallbacks 是本轮「M-Team 启用但取不到」而降级磁力的次数（禁用不算，那根本没有尝试）。
	Fallbacks int
	// Added 是这一轮加种成功（含收编/已存在）的版本数。
	Added int
}

// Run 跑循环直到 ctx 结束。第一轮立即跑，之后按 app_config 的 poll_interval 等。
//
// 门禁没开时用 GateCheckInterval 探路：配完向导后不必等满一个轮询周期才看见任务。
func (r *Runner) Run(ctx context.Context) {
	for {
		result, err := r.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			r.logger.Error("poll 循环失败", "err", err)
		}

		wait := GateCheckInterval
		if !result.Gated {
			wait = r.interval(ctx)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// RunOnce 跑一轮。它自己抢 advisory lock、自己记健康位。
func (r *Runner) RunOnce(ctx context.Context) (Result, error) {
	release, locked, err := loops.Guard(ctx, r.pool, LockIDPoll, "poll", r.logger)
	if err != nil {
		return Result{}, err
	}
	if !locked {
		return Result{Locked: true, Reason: "另一实例正在跑 poll"}, nil
	}
	defer release()

	started := r.now()
	if r.loops != nil {
		// 抢到锁、真的要跑了：面板能看见「正在跑」，结束由 Record/Stop 收口。
		r.loops.Start(loops.Poll, started)
	}
	result, err := r.run(ctx)
	if !result.Gated && !result.Locked {
		if r.loops != nil {
			r.loops.Record(loops.Poll, started, err == nil)
		}
		r.logger.Debug("poll 循环结束",
			"scanned", result.Scanned, "queried", result.Queried,
			"candidates", result.Candidates, "torrents", result.Torrents,
			"fallbacks", result.Fallbacks, "added", result.Added, "err", err)
	} else if r.loops != nil {
		// 门禁拦下的轮次不算迭代，也不能一直挂着「正在跑」。
		r.loops.Stop(loops.Poll)
	}
	return result, err
}

// run 是 RunOnce 去掉锁与健康位后的那一半。
func (r *Runner) run(ctx context.Context) (Result, error) {
	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("poll: 读设置: %w", err)
	}
	if reason := snapshot.GateReason(); reason != "" {
		return Result{Gated: true, Reason: reason}, nil
	}

	list, ok := wantWatchList(snapshot)
	if !ok {
		return Result{Gated: true, Reason: "想看清单还没登记"}, nil
	}

	javdbInput, err := r.settings.Input(ctx, "javdb")
	if err != nil {
		return Result{}, fmt.Errorf("poll: 读 JavDB 设置: %w", err)
	}
	qbtInput, err := r.settings.Input(ctx, "qbittorrent")
	if err != nil {
		return Result{}, fmt.Errorf("poll: 读 qBittorrent 设置: %w", err)
	}
	token := javdbInput.Text("token")

	// 一轮的共用上下文：磁力凭据、加种配置、M-Team 的启停判据都是每轮取一次的东西。
	round := r.ingest.Round(snapshot, token, qbittorrent.ConfigFrom(qbtInput))

	var result Result
	var errs []error

	items, err := r.source.AllListItems(ctx, token, list.ListID)
	if err != nil {
		// 清单读失败也要把已有的待加种版本推一步：那是独立于上游的活。
		errs = append(errs, fmt.Errorf("poll: 扫清单 %s: %w", list.ListID, err))
	}

	for _, item := range items {
		result.Scanned++
		if item.MovieID == "" || strings.TrimSpace(item.Number) == "" {
			continue
		}

		ingested, err := round.Ingest(ctx, ingest.Work{
			MovieID: item.MovieID,
			Number:  item.Number,
			ListID:  list.ListID,
		})
		// 出错时 Ingested 仍然说清走到了哪一步：已经查过的照旧计数。
		if ingested.Queried {
			result.Queried++
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ingested.Duplicate {
			result.Duplicates++
			continue
		}
		if ingested.Inserted {
			result.Candidates++
			if ingested.Candidate.Delivery == task.DeliveryTorrent {
				result.Torrents++
			}
		}
		if ingested.Fallback {
			result.Fallbacks++
		}
	}

	// 先落库再加种：到这里本轮所有候选都已落库，接下来统一加种（含上轮遗留的 pending）。
	added, addErrs := round.AddPending(ctx)
	result.Added = added
	errs = append(errs, addErrs...)

	return result, errors.Join(errs...)
}

// interval 读 app_config 的 poll_interval；读不到就用默认值。
func (r *Runner) interval(ctx context.Context) time.Duration {
	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return DefaultInterval
	}
	return snapshot.Interval("poll_interval", DefaultInterval)
}

// wantWatchList 从设置快照里取「想看」清单。
func wantWatchList(snapshot settings.Snapshot) (settings.ListEntry, bool) {
	entries, _ := snapshot.Sections["list_registry"].Data["entries"].([]settings.ListEntry)
	for _, entry := range entries {
		if entry.Purpose == "want_watch" && strings.TrimSpace(entry.ListID) != "" {
			return entry, true
		}
	}
	return settings.ListEntry{}, false
}
