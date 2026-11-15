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
// 上游失败（地区阻断、schema 错、连不上）是**瞬时**错：退避跨循环重试 5 轮
// （与副作用通知同一节奏，#42）；期间版本留 processing，循环健康位不变红
// （ADR-0011 口径：上游不可达不算循环失败）。五轮耗尽后照常入库不阻塞——
// 按手头已有的源落元数据，缺哪几面由整理段按四面判据标「待补齐」；
// 只有凭据失效/协议不兼容这类重试变不出来的永久错才直接落 failed(scrape) 等人工。
// 「DMM 地区阻断」不是「DMM 没收录」，绝不在失败时降级成不存在的字段。
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
	// Degraded 是重试耗尽、按手头数据降级入库的版本数（#42）。
	Degraded int
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
	translation := r.newTranslationAccess(ctx, snapshot)

	progress := Progress{}
	var errs []error
	for _, version := range versions {
		if progress.Fetched+progress.Degraded+progress.Failed >= maxScrapesPerRound {
			break
		}
		progress.Scanned++
		outcome, err := r.processVersion(ctx, version, token, translation)
		if err != nil {
			errs = append(errs, err)
		}
		switch outcome {
		case outcomeFetched:
			progress.Fetched++
		case outcomeDegraded:
			progress.Degraded++
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
			"reused", progress.Reused, "degraded", progress.Degraded,
			"failed", progress.Failed, "skipped", progress.Skipped)
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
	// outcomeDegraded 是重试耗尽、按手头数据降级入库（版本仍是 processing 等整理，#42）。
	outcomeDegraded
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
func (r *Runner) processVersion(ctx context.Context, version task.Version, token string, translation *translationAccess) (outcome, error) {
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

	outcome := r.fetch(ctx, claimed, token)
	switch {
	case outcome.permanentErr() != nil:
		// 永久错：凭据失效/协议不兼容重试变不出来，一次就落 failed(scrape) 进需人工，
		// 不烧 5 轮预算（internal/processing 的永久错口径）。错误不往循环上抛。
		return outcomeFailed, r.fail(ctx, claimed, outcome.permanentErr())
	case outcome.transientErr() != nil && claimed.Attempts+1 < processing.TransientMaxAttempts:
		// 预算内：退避跨循环重试（#42，与副作用通知同一节奏）。上游不可达不算
		// 循环失败（ADR-0011 口径）：错误只进时间线与日志，不往循环上抛。
		if err := r.recorder.Retry(ctx, claimed, task.PhaseScrape, outcome.transientErr()); err != nil {
			return outcomeFailed, err
		}
		r.recordAlert(ctx, claimed, fmt.Sprintf("%s：%v", task.ScrapeFailurePrefix, outcome.transientErr()))
		return outcomeFailed, nil
	case outcome.transientErr() != nil:
		// 五轮耗尽：照常入库不阻塞（#42）。按手头已有的源落元数据——哪怕两源
		// 全没取到（provenance 全空值也证明「刮过一次」），缺哪几面由整理段
		// 按四面判据标注「待补齐」，修复动作是面板上的强制重刮。
		fetched := r.translate(ctx, claimed, translation, outcome.fetched)
		if err := r.saveFetched(ctx, claimed, fetched); err != nil {
			return outcomeFailed, err
		}
		r.recordExhausted(ctx, claimed, outcome)
		r.logger.Warn("取数重试耗尽，降级入库", "movie_id", claimed.MovieID,
			"infohash", claimed.Infohash, "missing", outcome.missing(),
			"attempts", processing.TransientMaxAttempts)
		return outcomeDegraded, nil
	}
	// 两源都取到了：翻译是取数之后的一步可选加工（失败只降级日文原文，P6-2）。
	fetched := r.translate(ctx, claimed, translation, outcome.fetched)
	if err := r.saveFetched(ctx, claimed, fetched); err != nil {
		return outcomeFailed, err
	}
	r.logger.Info("元数据已取齐", "movie_id", claimed.MovieID, "number", claimed.Number,
		"translated", !fetched.TaskTranslation().Empty())
	return outcomeFetched, nil
}

// saveFetched 是取数成功的落库段：一个事务写元数据三面 + 演员，译文单独写。
func (r *Runner) saveFetched(ctx context.Context, version task.Version, fetched Fetched) error {
	metadata, err := fetched.TaskMetadata()
	if err != nil {
		return errors.Join(fmt.Errorf("scrape: 序列化元数据 %s: %w", version.MovieID, err),
			r.store.Release(ctx, version.Infohash, r.owner))
	}

	// 一个事务：元数据三面 + 演员 + 放回这条版本的租约。版本留在 processing 等整理票。
	lease := task.Lease{Infohash: version.Infohash, Owner: r.owner}
	if err := r.store.SaveMetadata(ctx, version.MovieID, lease, metadata, fetched.Values.Actors); err != nil {
		return errors.Join(err, r.store.Release(ctx, version.Infohash, r.owner))
	}
	// 译文单独写：重刮不抹掉它，翻译失败也不挡取数的成果（规格 §7.2）。
	if err := r.store.SaveTranslation(ctx, version.MovieID, fetched.TaskTranslation()); err != nil {
		return errors.Join(err, r.store.Release(ctx, version.Infohash, r.owner))
	}
	return nil
}

// fetchOutcome 是一轮取数的原始结果：合成的字段 + 哪一边没取到。
//
// 永久错与瞬时错的分界只有一处定义：JavDB 用 appapi.IsPermanent（凭据失效/协议
// 不兼容），DMM 用 dmm.IsPermanent（匿名 API，恒为瞬时）——与 §9 上游指标的
// 分类钩子同一套。
type fetchOutcome struct {
	fetched  Fetched
	javdbErr error
	dmmErr   error
}

// permanentErr 返回这一轮的永久错（重试变不出来的）；两源都错时永久错优先。
func (o fetchOutcome) permanentErr() error {
	if o.javdbErr != nil && appapi.IsPermanent(o.javdbErr) {
		return o.javdbErr
	}
	if o.dmmErr != nil && dmm.IsPermanent(o.dmmErr) {
		return o.dmmErr
	}
	return nil
}

// transientErr 返回这一轮的瞬时错（网络/超时/5xx——等一会儿重试）。
func (o fetchOutcome) transientErr() error {
	if o.javdbErr != nil && !appapi.IsPermanent(o.javdbErr) {
		return o.javdbErr
	}
	if o.dmmErr != nil && !dmm.IsPermanent(o.dmmErr) {
		return o.dmmErr
	}
	return nil
}

// missing 把这一轮没取到的源名连起来（人话，进耗尽告警）。
func (o fetchOutcome) missing() string {
	var names []string
	if o.javdbErr != nil {
		names = append(names, "JavDB")
	}
	if o.dmmErr != nil {
		names = append(names, "DMM")
	}
	return strings.Join(names, "、")
}

// fetch 是一个任务的完整取数：JavDB 详情与 DMM 内容两源各取各的（#42）。
//
// 一个源挂了不再挡住另一个源：哪边活着哪边先取到，退避重试的每一轮两源都问，
// 耗尽降级时才能按「手头有多少算多少」入库。字段级回退仍是 §7.1 的单向
// DMM→JavDB（Merge 的事，裁决不扩双向兑底）。没取到的源不进 raw_snapshot；
// 它的字段在 provenance 里是空值——「上游没给出」与「字段本来没有」在这一面
// 上不可分，但耗尽告警与待补齐标注会把「谁没取到」说清。
//
// 唯一的例外是补录源（#43）：补录任务的主键是番号本身（JavDB App API 没有
// 番号→movie_id 的检索端点），拿它调详情只会得到 404 类协议错、被 IsPermanent
// 当成永久错打成 failed(scrape)。补录只问 DMM；演员等 JavDB 侧面的缺席由
// 整理段按四面判据标「待补齐」，修复口径是面板的强制重刮。
func (r *Runner) fetch(ctx context.Context, version task.Version, token string) fetchOutcome {
	outcome := fetchOutcome{}

	var detail appapi.DetailResult
	if version.Source == task.SourceBackfill {
		r.logger.Info("补录版本只问 DMM（无 JavDB movie_id 可查）",
			"movie_id", version.MovieID, "number", version.Number)
	} else {
		var err error
		detail, err = r.javdb.Detail(ctx, token, version.MovieID)
		if err != nil {
			outcome.javdbErr = fmt.Errorf("读 JavDB 详情 %s: %w", version.MovieID, err)
		}
	}
	dmmResult, err := r.dmm.Lookup(ctx, version.Number)
	if err != nil {
		outcome.dmmErr = fmt.Errorf("读 DMM %s: %w", version.Number, err)
	}
	outcome.fetched = Merge(r.now(), dmmResult, detail)
	return outcome
}

// recordExhausted 记「取数重试耗尽、降级入库」一条告警（同版本只留一条，HasAlert）。
//
// 降级入库后版本走复用判据，不会再自动打已恢复无望的上游；强制重刮是唯一
// 的重取口。面板事件流要看得见为什么标了待补齐，但不该被复发刷满。
func (r *Runner) recordExhausted(ctx context.Context, version task.Version, outcome fetchOutcome) {
	alerted, err := r.store.HasAlert(ctx, version.Infohash, task.FetchExhaustedPrefix)
	if err != nil {
		r.logger.Error("scrape: 查耗尽告警去重失败", "infohash", version.Infohash, "err", err)
	}
	if alerted {
		return
	}
	r.recordAlert(ctx, version, fmt.Sprintf("%s：%s 连续 %d 轮失败，已按手头数据入库并标「待补齐」",
		task.FetchExhaustedPrefix, outcome.missing(), processing.TransientMaxAttempts))
}

// recordAlert 往任务时间线追加一条刮削段告警（写不进去不影响这轮的结论）。
func (r *Runner) recordAlert(ctx context.Context, version task.Version, reason string) {
	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:  version.MovieID,
		Infohash: version.Infohash,
		Kind:     task.EventAlert,
		Phase:    string(task.PhaseScrape),
		Reason:   reason,
	}); err != nil {
		r.logger.Error("scrape: 记事件失败", "infohash", version.Infohash, "err", err)
	}
}

// fail 落一次永久错：failed(scrape, reason) + 一条「加工失败」通知，全部归
// processing.Recorder（permanent=true：不烧预算）。返回的错误只描述记账本身——
// 上游为什么挂已经落在状态机与时间线里，不往循环上抛（ADR-0011 口径）。
func (r *Runner) fail(ctx context.Context, version task.Version, cause error) error {
	if _, err := r.recorder.Fail(ctx, version, task.PhaseScrape, cause, true, 0); err != nil {
		return err
	}
	r.logger.Error("取数永久失败，版本驻留等人工",
		"movie_id", version.MovieID, "infohash", version.Infohash, "err", cause)
	return nil
}
