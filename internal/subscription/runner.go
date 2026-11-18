package subscription

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
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// LockIDSubscription 是女优订阅循环的 advisory lock key（规格 §6.2：独立一把）。
//
// 与 poll（4230001）分开是刻意的：一位女优的接口挂掉不该拖死清单轮询，
// 两条循环各有各的节拍与单飞。
const LockIDSubscription int64 = 4230004

// DefaultInterval 是 app_config 读不到 subscription_interval 时的兜底（与表默认值一致）。
const DefaultInterval = 15 * time.Minute

// GateCheckInterval 是「门禁还没开」时的探路节奏：配完向导不必等满一个 15 分钟周期。
const GateCheckInterval = 30 * time.Second

// DefaultLookbackDays 是回看窗口的兜底（与 subscription.lookback_days 的表默认值一致）。
const DefaultLookbackDays = 30

// maxWorkPages 是一次取数的翻页上限：上游「空页即止」，但一个异常的上游
// （永远返回同一页）不该把一轮拖成无尽循环。
const maxWorkPages = 200

// WorksSource 是女优页列表的上游（真实实现是 javdb.Integration.ActressWorks）。
//
// 只读一页：翻页与「见过下界即停」是订阅循环自己的判断（水位线是它的账）。
type WorksSource interface {
	ActressWorks(ctx context.Context, token, id string, page int) ([]appapi.ActressWork, error)
}

// RunnerOptions 装配订阅循环。
//
// 磁力、加种、M-Team 取种与交付材料都是 **ingest 那一段**的依赖：本包自己不用它们，
// 只递过去（ADR-0014：两条订阅共用 ingest，不共用调度）。
type RunnerOptions struct {
	Store *Store
	// Tasks 是任务域（ingest 落库用）；与 Store 是同一个池上的两张门面。
	Tasks    *task.Store
	Outbox   *sidefx.Outbox
	Settings Settings
	Source   WorksSource
	Magnets  ingest.Magnets
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

// Runner 是女优订阅循环（规格 §6.2 的 subscription 通道）。
type Runner struct {
	store    *Store
	ingest   *ingest.Ingester
	settings Settings
	source   WorksSource
	pool     *pgxpool.Pool
	loops    *loops.Registry
	logger   *slog.Logger
	now      func() time.Time
}

// NewRunner 构造订阅循环。
func NewRunner(opts RunnerOptions) *Runner {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	return &Runner{
		store: opts.Store,
		ingest: ingest.New(ingest.Options{
			Store:    opts.Tasks,
			Outbox:   opts.Outbox,
			Settings: opts.Settings,
			Magnets:  opts.Magnets,
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

// Result 是一轮订阅循环的可观察结果（日志、测试与面板健康位共用）。
type Result struct {
	// Gated 表示门禁没开（未初始化/已暂停），这一轮没有做任何事。
	Gated bool
	// Locked 表示另一实例正跑着订阅循环，这一轮让给了它。
	Locked bool
	// Reason 是跳过时给人看的一句话。
	Reason string
	// Subscriptions 是本轮真的去追了几条订阅（停用的不算）。
	Subscriptions int
	// Seen 是回看窗口里看到的作品数（含没有磁力的那些——它们也推进水位线）。
	Seen int
	// Queried 是真正查了磁力候选的作品数。
	Queried int
	// Candidates 是新建的版本行数；Duplicates 是已存在（零写入）的候选数。
	Candidates int
	Duplicates int
	// Fallbacks 是本轮「M-Team 启用但取不到」而降级磁力的次数。
	Fallbacks int
	// Added 是这一轮回看窗口 + 待加种通道加种成功（含收编/已存在）的版本数。
	Added int
}

// Run 跑循环直到 ctx 结束。第一轮立即跑，之后按 app_config 的 subscription_interval 等。
//
// 门禁没开时用 GateCheckInterval 探路（与 poll 同一口径）。一条订阅都没有时循环空转
// 即返回：订阅**不进** initialized 判据（规格 §6.4 门禁行）。
func (r *Runner) Run(ctx context.Context) {
	for {
		result, err := r.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			r.logger.Error("订阅循环失败", "err", err)
		}

		wait := GateCheckInterval
		if !result.Gated {
			wait = r.Interval(ctx)
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
	release, locked, err := loops.Guard(ctx, r.pool, LockIDSubscription, loops.Subscription, r.logger)
	if err != nil {
		return Result{}, err
	}
	if !locked {
		return Result{Locked: true, Reason: "另一实例正在跑女优订阅"}, nil
	}
	defer release()

	started := r.now()
	if r.loops != nil {
		r.loops.Start(loops.Subscription, started)
	}
	result, err := r.run(ctx)
	if !result.Gated && !result.Locked {
		if r.loops != nil {
			r.loops.Record(loops.Subscription, started, err == nil)
		}
		r.logger.Debug("订阅循环结束",
			"subscriptions", result.Subscriptions, "seen", result.Seen,
			"candidates", result.Candidates, "added", result.Added, "err", err)
	} else if r.loops != nil {
		// 门禁拦下的轮次不算迭代，也不能一直挂着「正在跑」。
		r.loops.Stop(loops.Subscription)
	}
	return result, err
}

// run 是 RunOnce 去掉锁与健康位后的那一半。
func (r *Runner) run(ctx context.Context) (Result, error) {
	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("subscription: 读设置: %w", err)
	}
	if reason := snapshot.GateReason(); reason != "" {
		return Result{Gated: true, Reason: reason}, nil
	}

	rows, err := r.store.List(ctx)
	if err != nil {
		return Result{}, err
	}

	javdbInput, err := r.settings.Input(ctx, "javdb")
	if err != nil {
		return Result{}, fmt.Errorf("subscription: 读 JavDB 设置: %w", err)
	}
	qbtInput, err := r.settings.Input(ctx, "qbittorrent")
	if err != nil {
		return Result{}, fmt.Errorf("subscription: 读 qBittorrent 设置: %w", err)
	}

	// 一轮的共用上下文：磁力凭据、加种配置、M-Team 的启停判据都是每轮取一次的东西。
	round := r.ingest.Round(snapshot, javdbInput.Text("token"), qbittorrent.ConfigFrom(qbtInput))

	var result Result
	var errs []error
	now := r.now()

	for _, sub := range rows {
		if !sub.Enabled {
			// 停用只停追新：任务照走完，水位线与回灌下界一概不动（§6.4 生命周期）。
			continue
		}
		result.Subscriptions++

		works, complete, err := r.window(ctx, sub, javdbInput.Text("token"), now)
		if err != nil {
			// 读上游失败：这一条订阅的窗口没扫完，回灌下界留着，其余订阅照常往下走。
			errs = append(errs, fmt.Errorf("subscription: 取女优 %s 的作品: %w", sub.Actor.ID, err))
		}

		for _, work := range works {
			result.Seen++
			ingested, err := round.Ingest(ctx, ingest.Work{
				MovieID:        work.MovieID,
				Number:         work.Number,
				SubscriptionID: sub.ID,
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
			}
			if ingested.Fallback {
				result.Fallbacks++
			}
		}

		// 水位线只由「见过」推进；回灌下界只在窗口扫完时清。
		if err := r.record(ctx, sub, works, complete, now); err != nil {
			errs = append(errs, err)
		}
	}

	// 先落库再加种：到这里本轮所有候选都已落库，接下来统一加种（含上轮遗留的 pending）。
	added, addErrs := round.AddPending(ctx)
	result.Added = added
	errs = append(errs, addErrs...)

	return result, errors.Join(errs...)
}

// window 取一条订阅在回看窗口里的作品。
//
// 返回的 complete 表示**窗口扫完了**（空页、或翻到了下界之前）：只有扫完才允许清
// backfill_from，否则下一轮从同一个下界接着扫（§6.4「余量留到下一轮」）。
//
// 列表按 release/desc 排序（取数时写明），因此「这一页已经全是下界之前的作品」就是
// 到底的证据——不必把一位女优的全部历史都翻一遍。
func (r *Runner) window(ctx context.Context, sub Row, token string, now time.Time) ([]appapi.ActressWork, bool, error) {
	lower := lowerBound(sub, now)
	var works []appapi.ActressWork
	// 翻页期间上游可能给出重叠的页（清单扫的同一条护栏）：按 movie_id 去重，
	// 同 id 以第一次出现的为准。
	seen := make(map[string]bool)

	for page := 1; page <= maxWorkPages; page++ {
		items, err := r.source.ActressWorks(ctx, token, sub.Actor.ID, page)
		if err != nil {
			return works, false, err
		}
		if len(items) == 0 {
			return works, true, nil
		}

		crossed := false
		for _, item := range items {
			if item.MovieID == "" || strings.TrimSpace(item.Number) == "" {
				continue
			}
			if item.ReleaseDate.IsZero() {
				// 发行日不知道：不进窗口、也不推进水位线（拿它推等于把窗口甩到它那一天）。
				continue
			}
			if item.ReleaseDate.Before(lower) {
				crossed = true
				continue
			}
			if seen[item.MovieID] {
				continue
			}
			seen[item.MovieID] = true
			works = append(works, item)
		}
		if crossed {
			return works, true, nil
		}
	}
	// 翻到上限还没到底：窗口没扫完，不清回灌下界。
	return works, false, nil
}

// record 把这一轮的结果写回订阅：水位线推成「见过的最晚发行日」，扫完清回灌下界。
//
// 一件都没见着不算「没见过就推」——只有窗口真的扫完时才写一次（那时下界已经是
// 空跑的证明，清掉它）；没扫完就什么都不写，下一轮从同一个地方接着扫。
func (r *Runner) record(ctx context.Context, sub Row, works []appapi.ActressWork, complete bool, now time.Time) error {
	var newest time.Time
	for _, work := range works {
		if work.ReleaseDate.After(newest) {
			newest = work.ReleaseDate
		}
	}
	if newest.IsZero() {
		if !complete {
			return nil
		}
		// 窗口里一件都没有（她最近没新作，或者回灌的那段确实没有作品）：
		// 水位线原样，只把回灌下界清掉。
		newest = watermarkDate(sub, now)
	}
	return r.store.Seen(ctx, sub.ID, newest, complete)
}

// Interval 读 app_config 的 subscription_interval；读不到就用默认值。
//
// 它是**独立节拍**（§6.2）：与 poll_interval 各走各的，一位女优的接口挂掉不该
// 让清单轮询跟着慢下来。
func (r *Runner) Interval(ctx context.Context) time.Duration {
	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return DefaultInterval
	}
	return snapshot.Interval("subscription_interval", DefaultInterval)
}

// lowerBound 是取数下界：LEAST(watermark_date − lookback_days, COALESCE(backfill_from, +∞))。
//
// 两条规则合在这里：
//   - 回看窗口（lookback_days，默认 30）让「出水时还没磁力」的作品下一轮仍被看见；
//   - 回灌（backfill_from）把下界一次性往回拨，窗口没扫完之前不会被水位线推回去。
//
// 取两者更早的那个：回灌**放宽**窗口，从不收窄。
func lowerBound(sub Row, now time.Time) time.Time {
	lookback := sub.LookbackDays
	if lookback <= 0 {
		lookback = DefaultLookbackDays
	}
	lower := watermarkDate(sub, now).AddDate(0, 0, -lookback)
	if sub.BackfillFrom != nil && sub.BackfillFrom.Before(lower) {
		return *sub.BackfillFrom
	}
	return lower
}

// watermarkDate 读水位线；NULL 按「今天」兜底（新订阅的语义：不回灌历史）。
func watermarkDate(sub Row, now time.Time) time.Time {
	if sub.WatermarkDate != nil {
		return *sub.WatermarkDate
	}
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}
