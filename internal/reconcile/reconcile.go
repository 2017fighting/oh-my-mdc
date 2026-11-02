// Package reconcile 是下载完成的第二条通道：qbt 回调 + 对账循环（规格 §5.4/§6.1/§6.2）。
//
// 两个入口共用一个推进函数：
//
//   - **webhook**：qbt autorun 打 `GET /api/hooks/qbt?ih=%I`，当场回读 qbt 的权威值并判定；
//   - **对账**：每 5 分钟按 `mdc:in-progress` 的 tag 扫一遍，补 webhook 漏掉的通知。
//
// 三条不变式在这里守：
//
//   - **qbt 是事实源**：回调载荷只有 infohash，progress/state/content_path 一律回读
//     `torrents/info` + `torrents/properties`，本项目自己不算进度、不猜状态。
//   - **DB 是状态真相**：tag 只是发现线索。库里没有的 infohash 静默 no-op（零写入）；
//     已 failed 的版本即使还带着 tag 也不许被改回来。
//   - **占用互斥**：两个入口都先抢版本行租约（§4.4）+ 进程内 per-version 锁，
//     抢输不是失败（ErrClaimLost）。
//
// 停滞（progress 60 分钟不动）分两半：**「从什么时候起没动过」**是进程内的进度表
// （重启后重新观察一轮），**「这条版本告过警没有」**查 task_event——判据落在库里，
// 所以重启、多实例都不会把同一条停滞重复告警。
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/2017fighting/oh-my-mdc/internal/loops"
	"github.com/2017fighting/oh-my-mdc/internal/media"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// LockIDReconcile 是 reconcile 的 advisory lock key（三条循环各持一把，规格 §4.4）。
const LockIDReconcile int64 = 4230002

// DefaultInterval 是 app_config 读不到 reconcile_interval 时的兜底（与表默认值一致）。
const DefaultInterval = 5 * time.Minute

// GateCheckInterval 是「门禁还没开」时的探路节奏：配完向导不必等满一个 5 分钟周期。
const GateCheckInterval = 30 * time.Second

// StallAfter 是 progress 一动不动多久算下载停滞。
//
// 它是常量而不是设置项（规格 §6.1：常量 60 分钟，非设置项）。
const StallAfter = 60 * time.Minute

// 下载阶段的失败原因（规格 §6.1 原文）。面板直译 phase + reason，所以这里就是用户看到的那句话。
const (
	ReasonSeedGone     = "种子已从 qBittorrent 移除"
	ReasonFilesMissing = "文件丢失"
)

// completedStates 是「下载已完成」的 qbt 状态集（规格 §6.1 三条件之一）。
//
// checking/moving/allocating 都不在此列：它们还会变，等下轮再看。
var completedStates = map[string]bool{
	"uploading": true,
	"stalledUP": true,
	"pausedUP":  true,
}

// Settings 是本循环对设置域的全部要求（与 poll 同一形状）。
type Settings interface {
	Load(ctx context.Context) (settings.Snapshot, error)
	Input(ctx context.Context, section string) (settings.TestInput, error)
}

// Qbt 是 qBittorrent 的权威读数接缝（真实实现是 qbittorrent.Integration）。
//
// 只有两件事：回读一条种子的现状，以及按项目 tag 列种子（对账的发现面）。
type Qbt interface {
	Lookup(ctx context.Context, cfg qbittorrent.Config, hash string) (qbittorrent.TorrentStatus, bool, error)
	Tagged(ctx context.Context, cfg qbittorrent.Config, tag string) ([]qbittorrent.TorrentStatus, error)
}

// Options 装配一个推进引擎 / 对账循环。
type Options struct {
	Store    *task.Store
	Outbox   *sidefx.Outbox
	Settings Settings
	Qbt      Qbt
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

// Runner 是推进引擎与对账循环。
type Runner struct {
	store    *task.Store
	outbox   *sidefx.Outbox
	settings Settings
	qbt      Qbt
	pool     *pgxpool.Pool
	loops    *loops.Registry
	logger   *slog.Logger
	owner    string
	now      func() time.Time
	progress *progressTracker
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
		store:    opts.Store,
		outbox:   opts.Outbox,
		settings: opts.Settings,
		qbt:      opts.Qbt,
		pool:     opts.Pool,
		loops:    opts.Loops,
		logger:   logger,
		owner:    owner,
		now:      now,
		progress: newProgressTracker(),
	}
}

// Result 是一轮对账的可观察结果（日志、测试与面板健康位共用）。
type Result struct {
	// Gated 表示门禁没开（未初始化/已暂停/没配 qbt），这一轮没有做任何事。
	Gated bool
	// Locked 表示另一实例正跑着对账，这一轮让给了它。
	Locked bool
	// Reason 是跳过时给人看的一句话。
	Reason string
	// Scanned 是本轮判定过的版本数；Advanced 推进到 processing；Failed 落失败；
	// Stalled 发了一次停滞告警；Waiting 是条件没齐（checking/moving/进度未满）；
	// Skipped 是抢输或这条版本已经不是下载中。
	Scanned  int
	Advanced int
	Failed   int
	Stalled  int
	Waiting  int
	Skipped  int
}

// Run 跑对账循环直到 ctx 结束。第一轮立即跑，之后按 app_config 的 reconcile_interval 等。
func (r *Runner) Run(ctx context.Context) {
	for {
		result, err := r.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			r.logger.Error("reconcile 循环失败", "err", err)
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

// RunOnce 跑一轮对账。它自己抢 advisory lock、自己记健康位。
func (r *Runner) RunOnce(ctx context.Context) (Result, error) {
	release, locked, err := loops.Guard(ctx, r.pool, LockIDReconcile, "reconcile", r.logger)
	if err != nil {
		return Result{}, err
	}
	if !locked {
		return Result{Locked: true, Reason: "另一实例正在跑对账"}, nil
	}
	defer release()

	started := r.now()
	result, err := r.run(ctx)
	if !result.Gated && !result.Locked {
		if r.loops != nil {
			r.loops.Record(loops.Reconcile, started, err == nil)
		}
		r.logger.Debug("reconcile 循环结束",
			"scanned", result.Scanned, "advanced", result.Advanced,
			"failed", result.Failed, "stalled", result.Stalled,
			"waiting", result.Waiting, "skipped", result.Skipped, "err", err)
	}
	return result, err
}

// HandleWebhook 处理一次 qbt autorun 回调（规格 §5.4）。
//
// 它永远不该让 HTTP 端回非 200：未知/非本项目 infohash 静默 no-op；抢输不是失败；
// 只有真正的运行故障才返回错误（服务器只记日志，仍回 200）。
func (r *Runner) HandleWebhook(ctx context.Context, infohash string) error {
	hash := normalizeHash(infohash)
	if hash == "" {
		return nil
	}

	// 未知/非本项目 infohash：零写入的静默 no-op（P8-2）。这一条必须在读设置之前。
	version, err := r.store.Version(ctx, hash)
	if errors.Is(err, task.ErrVersionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	cfg, ok, _, err := r.config(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	_, err = r.advance(ctx, version, cfg, false)
	return err
}

// ResetOnStartup 是进程启动时的重启复位（规格 §6.1 重启行）。
//
// 只碰 processing（阶段级复位重跑）；downloading 不动、finished/failed 不动。
// 返回被复位的版本数；每条留一句时间线，面板才说得清它为什么又跑了一遍。
func (r *Runner) ResetOnStartup(ctx context.Context) (int, error) {
	reset, err := r.store.ResetProcessing(ctx)
	if err != nil {
		return 0, err
	}
	for _, version := range reset {
		if err := r.store.AppendEvent(ctx, task.Event{
			MovieID:  version.MovieID,
			Infohash: version.Infohash,
			Kind:     task.EventStateChange,
			ToState:  string(task.StatusProcessing),
			Reason:   "重启：加工阶段复位重跑",
		}); err != nil {
			r.logger.Error("reconcile: 记重启复位事件失败", "infohash", version.Infohash, "err", err)
		}
	}
	if len(reset) > 0 {
		r.logger.Info("加工阶段已复位", "count", len(reset))
	}
	return len(reset), nil
}

// run 是 RunOnce 去掉锁与健康位后的那一半。
func (r *Runner) run(ctx context.Context) (Result, error) {
	cfg, ok, reason, err := r.config(ctx)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{Gated: true, Reason: reason}, nil
	}

	// 发现面的错误不当场返回：tag 扫描挂了也要把库里已知的下载中版本判完。
	candidates, discoveryErr := r.candidates(ctx, cfg)

	result := Result{}
	var errs []error
	if discoveryErr != nil {
		errs = append(errs, discoveryErr)
	}
	for _, version := range candidates {
		result.Scanned++
		action, err := r.advance(ctx, version, cfg, true)
		if err != nil {
			errs = append(errs, err)
		}
		switch action {
		case actionAdvanced:
			result.Advanced++
		case actionFailed:
			result.Failed++
		case actionStalled:
			result.Stalled++
		case actionWaiting:
			result.Waiting++
		case actionSkipped:
			result.Skipped++
		}
	}
	return result, errors.Join(errs...)
}

// candidates 是本轮要判定的版本：库里所有下载中的版本 ∪ qbt 里带 mdc:in-progress 的种子。
//
// 两边都是必须的：
//
//   - 库里那条发现「种子消失」——种子被移除后它连 tag 扫描都不会出现；
//   - tag 那条补「webhook 漏了、库里也没被对账扫到」的漏（P8-3）。
//
// tag 只是线索：库里没有 / 已不是 downloading / 还没加种，一律跳过（DB 是真相）。
func (r *Runner) candidates(ctx context.Context, cfg qbittorrent.Config) ([]task.Version, error) {
	versions, err := r.store.DownloadingVersions(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(versions))
	ordered := make([]task.Version, 0, len(versions))
	for _, version := range versions {
		seen[version.Infohash] = true
		ordered = append(ordered, version)
	}

	tagged, tagErr := r.qbt.Tagged(ctx, cfg, qbittorrent.TagInProgress)
	if tagErr != nil {
		// tag 扫描失败不该让库内那条发现面停工：继续判已知的下载中版本。
		r.logger.Warn("reconcile: 按 tag 扫描失败，本轮只看库内下载中的版本", "err", tagErr)
	}
	for _, status := range tagged {
		hash := normalizeHash(status.Hash)
		if hash == "" || seen[hash] {
			continue
		}

		version, err := r.store.Version(ctx, hash)
		if errors.Is(err, task.ErrVersionNotFound) {
			// 非本项目 / 不是本项目加的种子：静默跳过（P8-2 的对称面）。
			continue
		}
		if err != nil {
			tagErr = errors.Join(tagErr, err)
			continue
		}
		if version.Status != task.StatusDownloading || version.AddPending {
			// 已 failed/processing/finished 的版本不许被 tag 改回来；没加种的还没进 qbt。
			continue
		}
		seen[hash] = true
		ordered = append(ordered, version)
	}
	return ordered, tagErr
}

// action 是一次推进尝试的结局。
type action int

const (
	// actionSkipped 是「不该由我处理」：状态已不是下载中、还没加种、或抢输租约。
	actionSkipped action = iota
	// actionWaiting 是条件还没齐（checking/moving/progress<1）：保持 downloading 等下轮。
	actionWaiting
	// actionAdvanced 是推进到 processing。
	actionAdvanced
	// actionFailed 是落 failed(下载阶段, reason)。
	actionFailed
	// actionStalled 是发了一次停滞告警，状态仍是 downloading。
	actionStalled
)

// advance 是两条入口共用的推进函数：先抢租约，再回读 qbt，再按三条件判定。
func (r *Runner) advance(ctx context.Context, version task.Version, cfg qbittorrent.Config, checkStall bool) (action, error) {
	if version.Status != task.StatusDownloading || version.AddPending {
		return actionSkipped, nil
	}

	// 进程内 per-version 锁 + 数据库租约：两者都过才动手（规格 §4.4）。
	unlock := r.store.Lock(version.Infohash)
	defer unlock()

	claimed, err := r.store.Claim(ctx, version.Infohash, r.owner)
	if errors.Is(err, task.ErrClaimLost) {
		// 抢输不是失败：别人（webhook 或对账）正在处理这条版本。
		return actionSkipped, nil
	}
	if err != nil {
		return actionSkipped, err
	}
	if claimed.Status != task.StatusDownloading || claimed.AddPending {
		// claim 之后状态变了：把租约还回去，不当成失败。
		return actionSkipped, r.release(ctx, claimed.Infohash)
	}

	status, found, err := r.qbt.Lookup(ctx, cfg, claimed.Infohash)
	if err != nil {
		// 回读失败只是这一次不成事：放回租约，下轮/下个回调再来。
		if releaseErr := r.release(ctx, claimed.Infohash); releaseErr != nil {
			r.logger.Error("reconcile: 回读失败后释放租约失败", "infohash", claimed.Infohash, "err", releaseErr)
		}
		return actionSkipped, fmt.Errorf("reconcile: 回读 %s: %w", claimed.Infohash, err)
	}

	switch judge(status, found) {
	case verdictAdvance:
		return r.markProcessing(ctx, claimed, status)
	case verdictSeedGone:
		return r.fail(ctx, claimed, ReasonSeedGone)
	case verdictFilesMissing:
		return r.fail(ctx, claimed, ReasonFilesMissing)
	}

	// verdictWaiting：条件还没齐。停滞判定只在库里做（webhook 是即时通道，没有历史）。
	if checkStall && found {
		if stalled, elapsed := r.progress.observe(claimed.Infohash, status.Progress, r.now()); stalled {
			alerted, err := r.alertStall(ctx, claimed, status.Progress, elapsed)
			if err != nil {
				if releaseErr := r.release(ctx, claimed.Infohash); releaseErr != nil {
					r.logger.Error("reconcile: 记停滞告警失败后释放租约失败", "infohash", claimed.Infohash, "err", releaseErr)
				}
				return actionWaiting, err
			}
			if releaseErr := r.release(ctx, claimed.Infohash); releaseErr != nil {
				return actionWaiting, releaseErr
			}
			if alerted {
				return actionStalled, nil
			}
		}
	}
	return actionWaiting, r.release(ctx, claimed.Infohash)
}

// verdict 是三条件的判定结果（纯函数，不碰 DB 不碰 qbt）。
type verdict int

const (
	verdictWaiting      verdict = iota // 还在下/还在校验/还在挪：下轮再看
	verdictAdvance                     // → processing
	verdictSeedGone                    // → failed(种子已移除)
	verdictFilesMissing                // → failed(文件丢失)
)

// judge 把 qbt 的权威读数翻成结论（规格 §6.1「→ processing」行）。
//
// 三条件必须同时成立：progress==1.0、state ∈ {uploading, stalledUP, pausedUP}、
// content_path 可读且含视频文件。
func judge(status qbittorrent.TorrentStatus, found bool) verdict {
	if !found {
		return verdictSeedGone
	}
	if status.Progress < 1 {
		return verdictWaiting
	}
	// qbt 自己已经认定文件缺失（progress 已满）：这就是「完成但文件缺」。
	if status.State == "missingFiles" {
		return verdictFilesMissing
	}
	if !completedStates[status.State] {
		return verdictWaiting
	}
	// content_path 缺省不是「文件丢失」：qbt 还没把它报出来，下轮再看。
	if strings.TrimSpace(status.ContentPath) == "" {
		return verdictWaiting
	}

	hasVideo, err := media.HasVideo(status.ContentPath)
	if err != nil || !hasVideo {
		return verdictFilesMissing
	}
	return verdictAdvance
}

// markProcessing 落 downloading→processing，并留一条时间线。
func (r *Runner) markProcessing(ctx context.Context, version task.Version, status qbittorrent.TorrentStatus) (action, error) {
	if err := r.store.MarkProcessing(ctx, version.Infohash, r.owner); err != nil {
		if errors.Is(err, task.ErrClaimLost) {
			return actionSkipped, nil
		}
		return actionSkipped, err
	}
	r.progress.forget(version.Infohash)

	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:   version.MovieID,
		Infohash:  version.Infohash,
		Kind:      task.EventStateChange,
		FromState: string(task.StatusDownloading),
		ToState:   string(task.StatusProcessing),
		Reason:    fmt.Sprintf("下载完成（qBittorrent 状态 %s）", status.State),
	}); err != nil {
		// 事件只是时间线：写不进去不该让一次已经成功的推进变成失败。
		r.logger.Error("reconcile: 记推进事件失败", "infohash", version.Infohash, "err", err)
	}
	return actionAdvanced, nil
}

// fail 落 failed(下载阶段, reason) 并留一条时间线。
func (r *Runner) fail(ctx context.Context, version task.Version, reason string) (action, error) {
	if err := r.store.MarkDownloadFailed(ctx, version.Infohash, r.owner, reason); err != nil {
		if errors.Is(err, task.ErrClaimLost) {
			return actionSkipped, nil
		}
		return actionSkipped, err
	}
	r.progress.forget(version.Infohash)

	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:   version.MovieID,
		Infohash:  version.Infohash,
		Kind:      task.EventStateChange,
		FromState: string(task.StatusDownloading),
		ToState:   string(task.StatusFailed),
		Phase:     string(task.PhaseDownloading),
		Reason:    reason,
	}); err != nil {
		r.logger.Error("reconcile: 记下载失败事件失败", "infohash", version.Infohash, "err", err)
	}
	return actionFailed, nil
}

// alertStall 发一次「下载停滞」告警：task_event + Telegram 待办（规格 §5.6）。
//
// 「只发一次」的判据在 task_event（GLOSSARY「任务事件」）：重启后同一条版本不会再发。
// 返回本次是否真的发了（已发过 → false，调用方不要把这一轮算成停滞）。
func (r *Runner) alertStall(ctx context.Context, version task.Version, progress float64, elapsed time.Duration) (bool, error) {
	alerted, err := r.store.HasAlert(ctx, version.Infohash, task.StallReasonPrefix)
	if err != nil {
		return false, err
	}
	if alerted {
		return false, nil
	}

	reason := fmt.Sprintf("%s：进度 %d%%，已 %d 分钟未动", task.StallReasonPrefix, int(progress*100), int(elapsed.Minutes()))
	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:  version.MovieID,
		Infohash: version.Infohash,
		Kind:     task.EventAlert,
		Phase:    string(task.PhaseDownloading),
		Reason:   reason,
	}); err != nil {
		return false, err
	}

	// 通知只是待办：执行（含四个开关与重试）归旁路引擎（#26/#28）。
	if r.outbox != nil {
		payload := map[string]any{
			"number":          version.Number,
			"label":           version.Label,
			"source":          string(version.Source),
			"progress":        progress,
			"stalled_minutes": int(elapsed.Minutes()),
		}
		if err := r.outbox.EnqueueNotify(ctx, sidefx.EventDownloadStalled, version.Infohash, payload); err != nil {
			r.logger.Error("reconcile: 停滞通知入箱失败", "infohash", version.Infohash, "err", err)
		}
	}

	r.logger.Warn("下载停滞", "infohash", version.Infohash, "progress", progress, "elapsed", elapsed)
	return true, nil
}

// config 一次读出「门禁开没开」与 qbt 凭据（webhook 与对账共用）。
func (r *Runner) config(ctx context.Context) (qbittorrent.Config, bool, string, error) {
	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return qbittorrent.Config{}, false, "", fmt.Errorf("reconcile: 读设置: %w", err)
	}
	if reason := snapshot.GateReason(); reason != "" {
		return qbittorrent.Config{}, false, reason, nil
	}

	input, err := r.settings.Input(ctx, "qbittorrent")
	if errors.Is(err, settings.ErrNotConfigured) {
		return qbittorrent.Config{}, false, "qBittorrent 还没配置", nil
	}
	if err != nil {
		return qbittorrent.Config{}, false, "", fmt.Errorf("reconcile: 读 qBittorrent 设置: %w", err)
	}
	return qbittorrent.ConfigFrom(input), true, "", nil
}

// release 放回自己的租约（这次没干成，下轮/别人可以再来）。
//
// 它不跟着请求的取消走：qbt 的 autorun 是 `curl -m 10`，回读超时时请求 ctx 已经结束，
// 用那个 ctx 释放会让租约白占 10 分钟。与 RunOnce 还 advisory lock 同理，用独立 ctx。
func (r *Runner) release(ctx context.Context, infohash string) error {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if err := r.store.Release(releaseCtx, infohash, r.owner); err != nil {
		return fmt.Errorf("reconcile: 释放 %s 的租约: %w", infohash, err)
	}
	return nil
}

// interval 读 app_config 的 reconcile_interval；读不到就用默认值。
func (r *Runner) interval(ctx context.Context) time.Duration {
	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return DefaultInterval
	}
	return snapshot.Interval("reconcile_interval", DefaultInterval)
}

// normalizeHash 把回调里的 infohash 归一：小写、去空白；不是 40 位十六进制就返回空串。
func normalizeHash(raw string) string {
	hash := strings.ToLower(strings.TrimSpace(raw))
	if len(hash) != 40 {
		return ""
	}
	for _, char := range hash {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return ""
		}
	}
	return hash
}
