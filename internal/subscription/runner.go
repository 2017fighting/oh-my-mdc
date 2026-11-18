package subscription

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/ingest"
	"github.com/2017fighting/oh-my-mdc/internal/loops"
	"github.com/2017fighting/oh-my-mdc/internal/material"
	"github.com/2017fighting/oh-my-mdc/internal/metrics"
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

// DefaultMaxAdds 是每轮限量的兜底（与 app_config.subscription_max_adds 的表默认值一致）。
const DefaultMaxAdds = 10

// 每轮限量的语义（§6.4「节流与去重」，本票落定）：
//
//   - **范围：一轮的总预算**，不是每条订阅各一份。规格的字面是「每轮最多建 N 个任务」，
//     而这一票要挡的是「一个 15 分钟周期把 qBittorrent、M-Team 搜索配额与上游接口一起打
//     满」——那个冲击不随订阅条数翻倍，按条给预算等于没限量。
//   - **口径：数建出来的任务**（新落的版本行），不是「这一轮递进 ingest 的作品」。窗口里
//     本来就看得到上一轮建过的作品（回看窗口 30 天），它们零写入、零上游调用；要是它们
//     也吃预算，历史作品会把预算吃光，新作品永远轮不到。更要紧的是「磁力为空不下行、
//     下轮重查」的那些（§6.1）：它们每轮都会再查一遍，如果也吃预算，排在最老那头的
//     「没有磁力」的作品会把每一轮的预算吃干净，窗口永远排不空——限量把窗口锁死了。
//   - **顺序：发行日升序**（同一天按 movie_id），见 sortQueue。
//   - **先筛后排队**（#48 × #49 的交点）：筛子在入队之**前**跑，被排掉的作品根本不
//     进队列——既不建任务、也不占预算。详情读不到的那些也不占：它们没被看过，下一轮
//     原样重取（Unexamined，不进 Deferred）。
//   - **收下不排在预算后面**：人按下的「收下」是显式裁决，与 API 上那颗按钮走同一条
//     ingest，所以限量的 spent 不数它、也不挡它（它仍算进 Candidates 的实账）。
//   - **余量不落状态**：被留在门外的作品什么都不写，下一轮的窗口原样把它们再取一遍 ——
//     这就是「余量留到下一轮」的实现（窗口每轮重算，没必要多一张下界表）。配套的硬约束：
//     一条订阅的窗口里还有余量时**不许清 backfill_from**（见 run 里的 record 调用），
//     否则下界会跳回水位线 − 回看窗口，被留在门外的老作品当场掉出窗口、永远进不来。

// maxWorkPages 是一次取数的翻页上限：上游「空页即止」，但一个异常的上游
// （永远返回同一页）不该把一轮拖成无尽循环。
const maxWorkPages = 200

// WorksSource 是女优页列表的上游（真实实现是 javdb.Integration.ActressWorks）。
//
// 只读一页：翻页与「见过下界即停」是订阅循环自己的判断（水位线是它的账）。
type WorksSource interface {
	ActressWorks(ctx context.Context, token, id string, page int) ([]appapi.ActressWork, error)
}

// PendingGauge 是 mdc_subscription_pending 的接缝（真实实现是 metrics.Registry）。
//
// 一行方法：一轮末尾写一个数（不是计数器，也不是增量）。订阅循环与指标之间只该有这一个数。
type PendingGauge interface {
	SetSubscriptionPending(total int)
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
	// Details 读作品详情（筛子的三条判据都从它来，ADR-0017）。nil 时筛子**整条**跳过：
	// 连演员数那条也不判（它同样住在详情里），这一轮看见的作品全部收下，日志记一次。
	Details DetailSource
	// Tags 读标签词表（按名字查 id；进程内缓存、重启重取）；nil 时标签判据跳过。
	Tags     VocabularySource
	Magnets  ingest.Magnets
	Adder    ingest.Adder
	Torrents ingest.Torrents
	Material *material.Store
	// Metrics 记 mdc_subscription_skipped_total{reason}；nil 时什么都不记。
	Metrics metrics.SubscriptionSkipRecorder
	// Pool 只用于 advisory lock 的专用连接；nil 时不做单飞（测试或单进程场景）。
	Pool *pgxpool.Pool
	// Loops 记循环健康位；nil 时只跑不记。
	Loops *loops.Registry
	// Pending 是「被每轮限量留在门外」的现数（面板「还有 N 部待入队」）；nil 时不算。
	Pending *Pending
	// Gauge 记 mdc_subscription_pending（§9）；nil 时不记。
	Gauge PendingGauge
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
	// Owner 是 claim 的持有者标识；空则用 hostname-pid。
	Owner string
	// Now 可注入测试时钟；空则 time.Now。
	Now func() time.Time
}

// Runner 是女优订阅循环（规格 §6.2 的 subscription 通道）。
type Runner struct {
	store  *Store
	ingest *ingest.Ingester
	// tasks 只用来问一句「这一部已经有任务了吗」：已有任务的既不再筛也不再取源
	// （§6.1 去重行）——同一部作品可能既在清单上又被订阅看见。
	tasks    *task.Store
	settings Settings
	source   WorksSource
	details  DetailSource
	tags     VocabularySource
	metrics  metrics.SubscriptionSkipRecorder
	vocab    tagCache
	// noDetails 让「详情源没装配」这条 warn 只记一次（与词表读不到那条同一口径）。
	noDetails sync.Once
	pool      *pgxpool.Pool
	loops     *loops.Registry
	pending   *Pending
	gauge     PendingGauge
	logger    *slog.Logger
	now       func() time.Time
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
		tasks:    opts.Tasks,
		details:  opts.Details,
		tags:     opts.Tags,
		metrics:  opts.Metrics,
		pool:     opts.Pool,
		loops:    opts.Loops,
		pending:  opts.Pending,
		gauge:    opts.Gauge,
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
	// Seen 是回看窗口里看到的作品数（含没有磁力的那些——它们也推进水位线，也含被筛子
	// 排掉的那些：筛在后面，见到就是见到）。
	Seen int
	// Deferred 是本轮「看过、也够格、只是没轮到」的作品数（§6.4/§9）：被每轮限量留在门外
	// 的那些。它**不含**另外两类——被筛子排掉的（本来就该收）与详情读不到的
	// （Unexamined，没被看过就谈不上「留到下一轮」）。
	// 它们什么状态都没落，下一轮的窗口原样再取一遍。
	Deferred int
	// Queried 是真正查了磁力候选的作品数。
	Queried int
	// Candidates 是新建的版本行数；Duplicates 是已存在（零写入）的候选数。
	Candidates int
	Duplicates int
	// Fallbacks 是本轮「M-Team 启用但取不到」而降级磁力的次数。
	Fallbacks int
	// Added 是这一轮回看窗口 + 待加种通道加种成功（含收编/已存在）的版本数。
	Added int
	// Skipped 是这一轮新落的跳过行数（回看窗口里再见到的旧行不算）。
	Skipped int
	// Unexamined 是详情没读到、留到下一轮的作品数（与「磁力为空不落行」同一处置）。
	Unexamined int
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
			"skipped", result.Skipped, "unexamined", result.Unexamined,
			"candidates", result.Candidates, "deferred", result.Deferred,
			"added", result.Added, "err", err)
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

	var result Result
	var errs []error
	now := r.now()

	// 跳过行的 30 天清理跟着循环跑（§6.4 生命周期）：一轮一次，不按订阅数线性增长。
	// 它只清没被收下的旧行，与回灌下界（backfill_from）无关——后者只由「窗口走完」
	// 决定（见本轮末尾的 record）。
	//
	// 它排在下面那条早退**之前**：这是纯本地的活（一次 DELETE，不读上游、不看名单），
	// 于是订阅全停用/全删掉之后，历史跳过行照旧按 30 天过期——不然那些行会永远留在库里，
	// 而它们只对面板有意义。
	if purged, err := r.store.PurgeSkips(ctx, now.AddDate(0, 0, -SkipRetentionDays)); err != nil {
		errs = append(errs, err)
	} else if purged > 0 {
		r.logger.Info("订阅筛子：清掉过期的跳过行", "count", purged)
	}

	// 一条**启用的**订阅都没有：循环空转即返回（§6.4 门禁行：订阅不进 initialized 判据）。
	//
	// 早退在「读设置凭据 → 建一轮共用上下文 → 读词表 → AddPending」之前，因为那四件事
	// 都要花东西：`GET /api/v2/tags` 是一次要 token 的上游调用，AddPending 是一次库查询，
	// 而这时既没有候选项、也没有窗口，它们只能是白花。上面那次本地清理照跑（见那里的注释）。
	// 现数一并清零：订阅全停用之后，上一轮留下的「还有 N 部待入队」不该停在面板上。
	if !anyEnabled(rows) {
		r.pending.Set(nil)
		if r.gauge != nil {
			r.gauge.SetSubscriptionPending(0)
		}
		return result, errors.Join(errs...)
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
	// 每轮限量（§6.4）：这个数是**一轮所有订阅共享**的预算，口径与顺序见 DefaultMaxAdds 上方的注释。
	budget := snapshot.AppInt("subscription_max_adds", DefaultMaxAdds)

	// 词表一轮取一次（进程内缓存，重启重取）：标签 id 按名字查，不硬编码（ADR-0017）。
	// 详情源没装配时连词表也不必读：两条标签判据是要拿详情里的类別去比对的，没有详情
	// 就没有比对的对象（筛子的守卫在 screen）。
	var ids tagIDs
	if r.details != nil {
		ids = r.screenTags(ctx, javdbInput.Text("token"))
	}

	// 先把每条订阅的窗口取完再统一入队：限量是一轮的总预算，排队要看得到所有订阅的作品。
	scans := make([]windowScan, 0, len(rows))
	var queue []windowWork
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

		result.Seen += len(works)
		scans = append(scans, windowScan{row: sub, works: works, complete: complete})
		for _, work := range works {
			// 任务已有版本就不再筛、也不再取源（§6.1 去重行）：同一部作品可能既在清单上
			// 又被订阅看见——那时它已经在下载了，给它落一条「跳过」只会把正在跑的东西摆到
			// 「最近被排掉的」里，而那里面每一条都是等人按「收下」的。
			//
			// 它也不吃这一轮的预算（#49 的口径）：预算数的是「这一轮建出来的任务」，而
			// 已有任务的作品零写入、零上游调用。回看窗口让三十天内的作品每轮都在窗口里，
			// 它们要是也吃预算，新作品永远轮不到。
			tracked, err := r.tasks.TaskHasVersion(ctx, work.MovieID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if tracked {
				continue
			}

			// 筛子（ADR-0017）：一次详情，三条判据各自落跳过表。
			//
			// 筛在**入队之前**——这是 #48 与 #49 的交点：被排掉的作品根本不进这一轮的
			// 队列，于是既不建任务、也不占预算。限量只该被「真能入队的作品」吃掉。
			screened, err := r.screen(ctx, work.MovieID, javdbInput.Text("token"), ids)
			if err != nil {
				// 详情读不到：这一件留到下一轮（不落任务、不落跳过行），与「磁力为空
				// 不落行」同一处置——上游抖一下不该换来一张永久记录。整轮照常往下走。
				//
				// 它**不算 deferred**：没被看过不等于「被限量留在门外」。限量数的是看过、
				// 也够格、只是这一轮没轮到的那些；这一件下一轮原样再被取一次。
				result.Unexamined++
				r.logger.Warn("订阅筛子：读详情失败，这件留到下一轮",
					"movie_id", work.MovieID, "code", appapi.Classify(err), "err", err)
				continue
			}
			// accepted 记住了「这一部是人收下的」：它照常走同一条 ingest，但不排在这一轮
			// 的预算后面（见下面那一圈）。正常路径上收下当场就建了任务，这一件会在上面的
			// 去重检查处被跳过；这里是兜底，与那张卡片是谁建的无关。
			accepted := false
			if screened.Reason != "" {
				outcome, err := r.store.RecordSkip(ctx, Skip{
					SubscriptionID: sub.ID,
					MovieID:        work.MovieID,
					Number:         work.Number,
					Title:          screened.Title,
					ReleaseDate:    releaseDateOfWork(work),
					Reason:         screened.Reason,
					SkippedAt:      now,
				})
				if err != nil {
					errs = append(errs, err)
					continue
				}
				if outcome == SkipAccepted {
					// 已经「收下」过：判据给用户的裁决让路，照常走 ingest（任务已在时是
					// 空转）。「用户说这一部可以」比判据优先级高。
					accepted = true
					r.logger.Debug("订阅筛子：这一部已收下，判据让路",
						"movie_id", work.MovieID, "subscription", sub.ID)
				} else {
					if outcome == SkipRecorded {
						// 指标数「新落的跳过」而不是「每轮的命中」：回看窗口让同一部作品每轮
						// 再现，而 §9 拿这条指标判断「判据还活着吗」——一个数一条记录才对应
						// 面板上那张卡片。
						result.Skipped++
						metrics.ObserveSubscriptionSkipped(r.metrics, screened.Reason)
					}
					continue
				}
			}

			queue = append(queue, windowWork{row: sub, work: work, accepted: accepted})
		}
	}
	sortQueue(queue)

	// 每轮限量（§6.4「节流与去重」）：预算数的是**这一轮建出来的任务**，不是递进
	// ingest 的作品；顺序按发行日从旧到新（跨订阅统一排队）。余量什么都不写，下一轮的
	// 窗口原样再取一遍，这就是「余量留到下一轮」的全部实现。
	//
	// spent 与 result.Candidates 是两个数：Candidates 是「这一轮建了几条版本」的实账，
	// spent 只数**订阅自己产的**那部分，限量比的是它。人按下的「收下」因此既不排在预算
	// 后面、也不占别人的名额——它是一次显式裁决，与 API 上那颗按钮走同一条 ingest。
	deferred := make(map[int64]int)
	spent := 0
	for _, item := range queue {
		if !item.accepted && spent >= budget {
			// 预算用完了：剩下的原样留在窗口里，下一轮接着来（§6.4「余量留到下一轮」）。
			deferred[item.row.ID]++
			result.Deferred++
			continue
		}

		ingested, err := round.Ingest(ctx, ingest.Work{
			MovieID:        item.work.MovieID,
			Number:         item.work.Number,
			SubscriptionID: item.row.ID,
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
			// Candidates 是实账（含收下的那一条）；spent 只数订阅自己产的，限量比的是它。
			result.Candidates++
			if !item.accepted {
				spent++
			}
		}
		if ingested.Fallback {
			result.Fallbacks++
		}
	}

	for _, scan := range scans {
		// 水位线只由「见过」推进；回灌下界只在**窗口真的走完**时清：
		// 扫完（complete）且这一条订阅没有被限量留在门外的余量（§6.4）。
		if err := r.record(ctx, scan.row, scan.works, scan.complete && deferred[scan.row.ID] == 0, now); err != nil {
			errs = append(errs, err)
		}
	}

	// 先落库再加种：到这里本轮所有候选都已落库，接下来统一加种（含上轮遗留的 pending）。
	// 限量只挡「订阅再往下产、入队」，不挡已建任务的推进（§6.4 验收）。
	added, addErrs := round.AddPending(ctx)
	result.Added = added
	errs = append(errs, addErrs...)

	// 每轮末尾置数（§9）：面板「还有 N 部待入队」与 mdc_subscription_pending 是同一份现数。
	// 门禁没开、抢锁抢输的轮次不置数（它们不是一轮）。
	r.pending.Set(deferred)
	if r.gauge != nil {
		r.gauge.SetSubscriptionPending(result.Deferred)
	}

	return result, errors.Join(errs...)
}

// anyEnabled 说这一轮有没有事可做：停用的订阅只停追新，它们不产生窗口。
//
// 「一条订阅都没有」与「订了但全停用」是同一个轮次（零窗口），早退对两者一样成立。
func anyEnabled(rows []Row) bool {
	for _, row := range rows {
		if row.Enabled {
			return true
		}
	}
	return false
}

// releaseDateOfWork 把列表给的发行日摊成可空日期：零值不猜一个今天，落 SQL NULL。
func releaseDateOfWork(work appapi.ActressWork) *time.Time {
	if work.ReleaseDate.IsZero() {
		return nil
	}
	date := work.ReleaseDate
	return &date
}

// windowScan 是一条订阅这一轮的取数结果：窗口里的作品 + 窗口扫完了没有。
type windowScan struct {
	row      Row
	works    []appapi.ActressWork
	complete bool
}

// windowWork 是一件排队的待办作品：作品 + 它属于哪条订阅（入队时要记下来源），
// 以及它是不是人「收下」来的（那一条不排在预算后面）。
type windowWork struct {
	row      Row
	work     appapi.ActressWork
	accepted bool
}

// sortQueue 把一轮的待办作品排成入队顺序：**发行日升序**，同一天按 movie_id。
//
// 最老的先走，因为下界只会往前挪：水位线只增不减、回灌窗口也在往前扫，所以离下界最近
// 的就是那些最先掉出窗口的作品。反过来让最新的先走，被限量留在门外的老作品会在余量轮
// 轮到它之前就滑出窗口——那等于限量把一个本可以拿到的作品默默丢掉了。
//
// 同一天的两部按 movie_id：跨订阅时页序与订阅遍历顺序都不该影响谁先走（可重现）。
func sortQueue(queue []windowWork) {
	sort.SliceStable(queue, func(i, j int) bool {
		left, right := queue[i].work, queue[j].work
		if !left.ReleaseDate.Equal(right.ReleaseDate) {
			return left.ReleaseDate.Before(right.ReleaseDate)
		}
		return left.MovieID < right.MovieID
	})
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

// record 把这一轮的结果写回订阅：水位线推成「见过的最晚发行日」，窗口走完才清回灌下界。
//
// 「走完」有两个条件（§6.4「节流与去重」）：complete（窗口扫到底，上游没断）**且**
// 这一条订阅没有被每轮限量留在门外的余量。少一个条件就清下界，下界会跳回
// 水位线 − 回看窗口，被留在门外的老作品当场掉出窗口、永远进不来（限量就成了丢件）。
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

// lowerBound 是取数下界：
//
//	GREATEST(floor_date, LEAST(watermark_date - lookback_days, COALESCE(backfill_from, +∞)))
//
// 三层各有各的活（GLOSSARY「起追日 / 回看窗口 / 回灌」）：
//
//   - 起追日（floor_date）是永久下界：建订阅当天定下，只有回灌能把它往前挪。它让
//     「新订阅不追历史」成为机制，而不是一句好听话——只靠 watermark − lookback，
//     回看窗口每轮都会往回带 30 天，昨天出的作品今天就被捡进来；
//   - 回看窗口（lookback_days，默认 30）让「出水时还没磁力」的作品下一轮仍被看见；
//   - 回灌（backfill_from）把下界一次性往回拨，窗口没扫完之前不会被水位线推回去。
//
// 内层取更早的那个：回灌**放宽**窗口，从不收窄。外层 GREATEST 再把它抬回起追日之上。
func lowerBound(sub Row, now time.Time) time.Time {
	lookback := sub.LookbackDays
	if lookback <= 0 {
		lookback = DefaultLookbackDays
	}
	lower := watermarkDate(sub, now).AddDate(0, 0, -lookback)
	if sub.BackfillFrom != nil && sub.BackfillFrom.Before(lower) {
		lower = *sub.BackfillFrom
	}
	if sub.FloorDate.After(lower) {
		return sub.FloorDate
	}
	return lower
}

// watermarkDate 读水位线；NULL 按「今天」兜底（新订阅的语义：从今天往后追，起追日兜住下界）。
func watermarkDate(sub Row, now time.Time) time.Time {
	if sub.WatermarkDate != nil {
		return *sub.WatermarkDate
	}
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}
