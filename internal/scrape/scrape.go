// Package scrape 是加工阶段的第一段：取齐元数据三面（规格 §5.3/§7.1，验收 P9-1、P15-1）。
//
// 它接在 reconcile 的「→ processing」之后、整理票（#29）之前：
//
//  1. **加工中发现面**：processing 而租约空闲/过期、退避到期的版本；
//  2. **复用判据先于网络**：任务元数据已存在 → 0 次上游调用，只把版本留在 processing
//     等整理（GLOSSARY「复用元数据」：判据是元数据存在，与标签/名称无关）；
//  3. **不存在才取数**：JavDB 详情（演员/标签/分值…）+ DMM（番号 → 发现 → 内容），
//     按 §7.1 的字段图归并，落强类型列 + 逐字段 provenance + 逐源 raw_snapshot；
//  4. **取数完成后版本驻 processing**：NFO/图片/硬链归 #29，这一段不碰文件系统。
//
// 上游失败（地区阻断、schema 错、连不上）走退避重试并留一条告警事件：
// 「DMM 地区阻断」不是「DMM 没收录」，所以绝不在失败时降级成 JavDB 元数据。
// 「失败三次落 failed」的裁决归聚合的整理票（#29），这里只记 attempts 与下一次时间。
package scrape

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/dmm"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

const (
	// DefaultRetryBase 是第一次取数失败的退避基数。
	DefaultRetryBase = time.Minute
	// DefaultRetryCap 是退避上限：再久也只是「下个对账周期再看」，别把重试排到几小时后。
	DefaultRetryCap = 30 * time.Minute
	// maxScrapesPerRound 挡住异常积压把一轮对账拖成无尽循环。
	maxScrapesPerRound = 100
)

// Settings 是这一段对设置域的全部要求（与其它循环同一形状）。
type Settings interface {
	Load(ctx context.Context) (settings.Snapshot, error)
	Input(ctx context.Context, section string) (settings.TestInput, error)
}

// DMM 是 DMM 取数接缝（真实实现是 dmm.Client）。
type DMM interface {
	Lookup(ctx context.Context, number string) (dmm.Result, error)
}

// JavDB 是 JavDB 详情接缝（真实实现是 javdb.Integration）。
type JavDB interface {
	Detail(ctx context.Context, token, movieID string) (appapi.DetailResult, error)
}

// Options 装配一个取数引擎。
type Options struct {
	Store    *task.Store
	Settings Settings
	DMM      DMM
	JavDB    JavDB
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
	// Owner 是 claim 的持有者标识；空则用 hostname-pid。
	Owner string
	// Now 可注入测试时钟；空则 time.Now。
	Now func() time.Time
	// RetryBase/RetryCap 是失败退避的基数与上限；零值用默认。
	RetryBase time.Duration
	RetryCap  time.Duration
}

// Runner 是取数引擎。
type Runner struct {
	store     *task.Store
	settings  Settings
	dmm       DMM
	javdb     JavDB
	logger    *slog.Logger
	owner     string
	now       func() time.Time
	retryBase time.Duration
	retryCap  time.Duration
}

// New 构造引擎。
func New(opts Options) *Runner {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	owner := strings.TrimSpace(opts.Owner)
	if owner == "" {
		owner = task.DefaultOwner()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	retryBase := opts.RetryBase
	if retryBase <= 0 {
		retryBase = DefaultRetryBase
	}
	retryCap := opts.RetryCap
	if retryCap <= 0 {
		retryCap = DefaultRetryCap
	}
	return &Runner{
		store:     opts.Store,
		settings:  opts.Settings,
		dmm:       opts.DMM,
		javdb:     opts.JavDB,
		logger:    logger,
		owner:     owner,
		now:       now,
		retryBase: retryBase,
		retryCap:  retryCap,
	}
}

// Progress 是一轮取数的可观察结果（日志、测试与 reconcile 的 Result 共用）。
type Progress struct {
	// Gated 表示门禁没开（未初始化/已暂停），这一轮没有做任何事。
	Gated bool
	// Reason 是跳过时给人看的一句话。
	Reason string
	// Scanned 是本轮看见的加工版本数。
	Scanned int
	// Fetched 是这一轮真的取了数的版本数。
	Fetched int
	// Reused 是命中复用判据（0 次网络）的版本数。
	Reused int
	// Failed 是取数失败、已排下一次重试的版本数。
	Failed int
	// Skipped 是抢输租约/状态已变的版本数。
	Skipped int
}

// ProcessPending 推进本轮的加工版本：复用判据 → 取数 → 落库，版本留在 processing。
//
// 它不抢循环级的 advisory lock：作为 reconcile 的一环跑，单飞由 reconcile 那把锁保证
// （规格 §4.4）；版本级互斥仍归 claim。
func (r *Runner) ProcessPending(ctx context.Context) (Progress, error) {
	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return Progress{}, fmt.Errorf("scrape: 读设置: %w", err)
	}
	if reason := snapshot.GateReason(); reason != "" {
		return Progress{Gated: true, Reason: reason}, nil
	}

	javdbInput, err := r.settings.Input(ctx, "javdb")
	if err != nil {
		return Progress{}, fmt.Errorf("scrape: 读 JavDB 设置: %w", err)
	}
	token := javdbInput.Text("token")

	versions, err := r.store.ProcessingVersions(ctx)
	if err != nil {
		return Progress{}, err
	}

	progress := Progress{}
	var errs []error
	for _, version := range versions {
		if progress.Fetched+progress.Failed >= maxScrapesPerRound {
			break
		}
		progress.Scanned++
		outcome, err := r.processVersion(ctx, version, token)
		if err != nil {
			errs = append(errs, err)
		}
		switch outcome {
		case outcomeFetched:
			progress.Fetched++
		case outcomeReused:
			progress.Reused++
		case outcomeFailed:
			progress.Failed++
		case outcomeSkipped:
			progress.Skipped++
		}
	}

	if progress.Scanned > 0 {
		r.logger.Debug("scrape 取数结束",
			"scanned", progress.Scanned, "fetched", progress.Fetched,
			"reused", progress.Reused, "failed", progress.Failed, "skipped", progress.Skipped)
	}
	return progress, errors.Join(errs...)
}

// outcome 是一条版本的结局。
type outcome int

const (
	// outcomeSkipped 是「不该由我处理」：抢输租约、状态已变、或读元数据出错前就退了。
	outcomeSkipped outcome = iota
	// outcomeFetched 是这一轮取了数并落库。
	outcomeFetched
	// outcomeReused 是命中复用判据（0 次网络）。
	outcomeReused
	// outcomeFailed 是取数失败、已排下一次重试（版本仍是 processing）。
	outcomeFailed
)

// processVersion 处理一条加工版本：先看复用判据，再抢租约，最后才上网。
//
// 复用判据刻意放在**抢租约之前**：命中复用的版本不需要取数，也就不该占着租约
// 让整理票（#29）等它。抢到租约后再查一次——两次查询之间别的进程可能已经刮完了。
func (r *Runner) processVersion(ctx context.Context, version task.Version, token string) (outcome, error) {
	reused, err := r.store.HasMetadata(ctx, version.MovieID)
	if err != nil {
		return outcomeSkipped, err
	}
	if reused {
		// P9-2：元数据存在 → 0 次上游调用，只留在 processing 等整理。
		return outcomeReused, nil
	}

	// 进程内 per-version 锁 + 数据库租约（规格 §4.4）。
	unlock := r.store.Lock(version.Infohash)
	defer unlock()

	claimed, err := r.store.Claim(ctx, version.Infohash, r.owner)
	if errors.Is(err, task.ErrClaimLost) {
		return outcomeSkipped, nil
	}
	if err != nil {
		return outcomeSkipped, err
	}
	if claimed.Status != task.StatusProcessing || claimed.AddPending {
		// claim 之后状态变了：把租约还回去，不当成失败。
		return outcomeSkipped, r.store.Release(ctx, claimed.Infohash, r.owner)
	}

	// 租约下的第二次查询：抢租约可能等过了别人刚刚完成的那一次取数。
	reused, err = r.store.HasMetadata(ctx, claimed.MovieID)
	if err != nil {
		return outcomeSkipped, errors.Join(err, r.store.Release(ctx, claimed.Infohash, r.owner))
	}
	if reused {
		return outcomeReused, r.store.Release(ctx, claimed.Infohash, r.owner)
	}

	fetched, err := r.fetch(ctx, claimed, token)
	if err != nil {
		return outcomeFailed, r.fail(ctx, claimed, err)
	}
	metadata, err := fetched.TaskMetadata()
	if err != nil {
		return outcomeFailed, errors.Join(fmt.Errorf("scrape: 序列化元数据 %s: %w", claimed.MovieID, err),
			r.store.Release(ctx, claimed.Infohash, r.owner))
	}

	// 一个事务：元数据三面 + 演员 + 放回这条版本的租约。版本留在 processing 等整理票。
	lease := task.Lease{Infohash: claimed.Infohash, Owner: r.owner}
	if err := r.store.SaveMetadata(ctx, claimed.MovieID, lease, metadata, fetched.Values.Actors); err != nil {
		return outcomeFailed, errors.Join(err, r.store.Release(ctx, claimed.Infohash, r.owner))
	}
	r.logger.Info("元数据已取齐", "movie_id", claimed.MovieID, "number", claimed.Number)
	return outcomeFetched, nil
}

// fetch 是一个任务的完整取数：JavDB 详情（必需）+ DMM（缺 → 字段级回退）。
//
// 顺序是先 JavDB 后 DMM：JavDB 是必成的那个源，它失败就不该先花 DMM 的请求。
// DMM 的失败**不**降级成「没有」：地区阻断与 schema 错都是故障，交给退避重试。
func (r *Runner) fetch(ctx context.Context, version task.Version, token string) (Fetched, error) {
	detail, err := r.javdb.Detail(ctx, token, version.MovieID)
	if err != nil {
		return Fetched{}, fmt.Errorf("读 JavDB 详情 %s: %w", version.MovieID, err)
	}
	dmmResult, err := r.dmm.Lookup(ctx, version.Number)
	if err != nil {
		return Fetched{}, fmt.Errorf("读 DMM %s: %w", version.Number, err)
	}
	return Merge(r.now(), dmmResult, detail), nil
}

// fail 记一次取数失败：排下一次重试 + 留一条告警事件。状态不变（仍是 processing）。
func (r *Runner) fail(ctx context.Context, version task.Version, cause error) error {
	delay := r.backoff(version.Attempts + 1)
	attempts, err := r.store.ScheduleProcessingRetry(ctx, version.Infohash, r.owner, r.now(), delay)
	if err != nil {
		// 抢输不是失败（别人接手了这条版本），别把它当故障抛出去。
		if errors.Is(err, task.ErrClaimLost) {
			return nil
		}
		return errors.Join(cause, err)
	}

	reason := fmt.Sprintf("%s：%v", task.ScrapeFailurePrefix, cause)
	r.logger.Warn("刮削取数失败，已排下一次重试",
		"movie_id", version.MovieID, "number", version.Number, "attempts", attempts, "retry_in", delay, "err", cause)

	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:  version.MovieID,
		Infohash: version.Infohash,
		Kind:     task.EventAlert,
		Phase:    string(task.PhaseScrape),
		Reason:   reason,
	}); err != nil {
		r.logger.Error("scrape: 记取数失败事件失败", "infohash", version.Infohash, "err", err)
	}
	return cause
}

// backoff 是第 attempts 次失败的退避：基数翻倍，封顶 retryCap。
func (r *Runner) backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := r.retryBase
	for i := 1; i < attempts && delay < r.retryCap; i++ {
		delay *= 2
	}
	if delay > r.retryCap {
		delay = r.retryCap
	}
	return delay
}
