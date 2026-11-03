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
//
// AI 翻译（#30）是取数之后的一步**可选**加工：启用时把日文 title/outline 译成中文，
// 写进 task 的 title_zh/outline_zh；译文由整理段（#29）的 NFO 槽位消费。
// 翻译失败只降级日文原文 + 一条告警事件，绝不阻塞入库，也绝不烧取数的重试预算。
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
	"github.com/2017fighting/oh-my-mdc/internal/processing"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
	"github.com/2017fighting/oh-my-mdc/internal/translate"
)

// maxScrapesPerRound 挡住异常积压把一轮对账拖成无尽循环。
const maxScrapesPerRound = 100

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

// Translator 是 AI 翻译接缝（真实实现是 translate.Integration）。
//
// 它是可选接缝：nil、未配置或 disabled 时刮削一次都不问上游（P6-3）。
type Translator interface {
	Translate(ctx context.Context, cfg translate.Config, in translate.Text) (translate.Text, error)
}

// Options 装配一个取数引擎。
type Options struct {
	Store    *task.Store
	Settings Settings
	// Outbox 是通知入箱口（可选）：取数三次耗尽时发「加工失败」一条（规格 §5.6）。
	Outbox *sidefx.Outbox
	DMM    DMM
	JavDB  JavDB
	// Translator 是 AI 翻译（可选集成）；nil 表示这一跑没接翻译。
	Translator Translator
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
	// Owner 是 claim 的持有者标识；空则用 hostname-pid。
	Owner string
	// Now 可注入测试时钟；空则 time.Now。
	Now func() time.Time
}

// Runner 是取数引擎。
type Runner struct {
	store      *task.Store
	settings   Settings
	dmm        DMM
	javdb      JavDB
	translator Translator
	logger     *slog.Logger
	owner      string
	now        func() time.Time
	// recorder 是加工失败记账的单点（与整理段共用，见 internal/processing）。
	recorder *processing.Recorder
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
	return &Runner{
		store:      opts.Store,
		settings:   opts.Settings,
		dmm:        opts.DMM,
		javdb:      opts.JavDB,
		translator: opts.Translator,
		logger:     logger,
		owner:      owner,
		now:        now,
		recorder: processing.NewRecorder(processing.Options{
			Store:  opts.Store,
			Outbox: opts.Outbox,
			Logger: logger,
			Now:    now,
			Owner:  owner,
		}),
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
	// 加工失败的自动重试上限与旁路引擎同一个设置（规格 §4.3 max_attempts）。
	maxAttempts := snapshot.MaxAttempts(processing.DefaultMaxAttempts)
	translation := r.newTranslationAccess(ctx, snapshot)

	progress := Progress{}
	var errs []error
	for _, version := range versions {
		if progress.Fetched+progress.Failed >= maxScrapesPerRound {
			break
		}
		progress.Scanned++
		outcome, err := r.processVersion(ctx, version, token, maxAttempts, translation)
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

// reuseCriterion 回答「这一轮要不要复用元数据」：元数据在、且没被人工要求强制重刮。
//
// force_rescrape 只由人工重试置起（#31 / 规格 §6.1 人工重试行），取数成功后由
// SaveMetadata 清掉。它是唯一能打破复用判据的东西：源改版、字段映射修了要重取时，
// 人需要一个确定性的开关，而不是把 provenance 清掉再看运气。
func reuseCriterion(reused bool, forceRescrape bool) bool {
	return reused && !forceRescrape
}

// processVersion 处理一条加工版本：先看复用判据，再抢租约，最后才上网。
//
// 复用判据刻意放在**抢租约之前**：命中复用的版本不需要取数，也就不该占着租约
// 让整理票（#29）等它。抢到租约后再查一次——两次查询之间别的进程可能已经刮完了。
func (r *Runner) processVersion(ctx context.Context, version task.Version, token string, maxAttempts int, translation *translationAccess) (outcome, error) {
	reused, err := r.store.HasMetadata(ctx, version.MovieID)
	if err != nil {
		return outcomeSkipped, err
	}
	if reuseCriterion(reused, version.ForceRescrape) {
		// P9-2：元数据存在 → 0 次取数。翻译不是取数：启用而译文缺失时补一次翻译请求，
		// 不碰 JavDB/DMM（用户可能在任务刮完以后才启用翻译）。
		r.backfillTranslation(ctx, version, translation)
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
	if reuseCriterion(reused, claimed.ForceRescrape) {
		r.backfillTranslation(ctx, claimed, translation)
		return outcomeReused, r.store.Release(ctx, claimed.Infohash, r.owner)
	}

	fetched, err := r.fetch(ctx, claimed, token)
	if err != nil {
		return outcomeFailed, r.fail(ctx, claimed, maxAttempts, err)
	}
	// 翻译是取数之后的一步可选加工：失败只降级日文原文 + 一条告警（P6-2）。
	fetched = r.translate(ctx, claimed, translation, fetched)
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
	// 译文单独写：重刮不抹掉它，翻译失败也不挡取数的成果（规格 §7.2）。
	if err := r.store.SaveTranslation(ctx, claimed.MovieID, fetched.TaskTranslation()); err != nil {
		return outcomeFailed, errors.Join(err, r.store.Release(ctx, claimed.Infohash, r.owner))
	}
	r.logger.Info("元数据已取齐", "movie_id", claimed.MovieID, "number", claimed.Number,
		"translated", !fetched.TaskTranslation().Empty())
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

// fail 记一次取数失败（退避/耗尽/通知都归 processing.Recorder）。
//
// 取数失败仍然把原因传出去：循环日志要看得见上游怎么了。耗尽时版本已落
// failed(scrape, reason) 并入了「加工失败」通知（规格 §5.6 四事件之一）。
func (r *Runner) fail(ctx context.Context, version task.Version, maxAttempts int, cause error) error {
	failure, err := r.recorder.Fail(ctx, version, task.PhaseScrape, cause, false, maxAttempts)
	if err != nil {
		return errors.Join(cause, err)
	}
	if !failure.Exhausted {
		// 每次失败都留一条尝试史（耗尽的最后一次由记账器统一写「加工失败」）。
		if err := r.store.AppendEvent(ctx, task.Event{
			MovieID:  version.MovieID,
			Infohash: version.Infohash,
			Kind:     task.EventAlert,
			Phase:    string(task.PhaseScrape),
			Reason:   fmt.Sprintf("%s：%v", task.ScrapeFailurePrefix, cause),
		}); err != nil {
			r.logger.Error("scrape: 记取数失败事件失败", "infohash", version.Infohash, "err", err)
		}
	}
	return cause
}
