// 本文件是 sidefx 的执行半段（入箱见 outbox.go，包级说明也在那里）：
// sidefx 循环按 kind 消费待办，瞬时错指数退避、永久错立即驻留（需人工），绝不自动禁用集成。
//
// 一条不变式在这里守：**副作用不是任务状态**——这里不动 task_version.status，
// 刷新/通知失败不阻塞入库。
package sidefx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/2017fighting/oh-my-mdc/internal/jellyfin"
	"github.com/2017fighting/oh-my-mdc/internal/loops"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// LockIDSidefx 是 sidefx 的 advisory lock key。三条循环各持一把（规格 §4.4）。
const LockIDSidefx int64 = 4230003

// DefaultInterval 是 app_config 读不到 sidefx_interval 时的兜底（与表默认值一致）。
const DefaultInterval = time.Minute

// GateCheckInterval 是「门禁还没开」时的探路节奏：配完向导不必等满一个周期。
const GateCheckInterval = 30 * time.Second

// maxJobsPerRound 是一轮最多执行多少条待办：挡住异常积压把一轮拖成无尽循环。
const maxJobsPerRound = 50

// Settings 是引擎对设置域的全部要求（与 poll/reconcile 同一形状）。
type Settings interface {
	Load(ctx context.Context) (settings.Snapshot, error)
	Input(ctx context.Context, section string) (settings.TestInput, error)
}

// ListRemover 是删清单项的接缝（真实实现是 javdb.Integration）。
type ListRemover interface {
	RemoveFromList(ctx context.Context, token, listID, movieID string) error
}

// Refresher 是 Jellyfin 目录级刷新的接缝（真实实现是 jellyfin.Integration）。
type Refresher interface {
	Refresh(ctx context.Context, cfg jellyfin.Config, number string) (jellyfin.RefreshResult, error)
}

// Tasks 是引擎对任务域的读写面：番号兜底与时间线。
//
// 时间线是「副作用发生过」的唯一留痕：面板的最近事件与告警追问都读它（规格 §4.1）。
type Tasks interface {
	Number(ctx context.Context, movieID string) (string, error)
	AppendEvent(ctx context.Context, event task.Event) error
}

// Options 装配一个旁路引擎。
type Options struct {
	Outbox    *Outbox
	Settings  Settings
	Remover   ListRemover
	Refresher Refresher
	Tasks     Tasks
	// Pool 只用于 advisory lock 的专用连接；nil 时不做单飞（测试或单进程场景）。
	Pool *pgxpool.Pool
	// Loops 记循环健康位；nil 时只跑不记。
	Loops *loops.Registry
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
	// Now 可注入测试时钟；空则 time.Now。
	Now func() time.Time
}

// Engine 是旁路副作用引擎。
type Engine struct {
	outbox    *Outbox
	settings  Settings
	remover   ListRemover
	refresher Refresher
	tasks     Tasks
	pool      *pgxpool.Pool
	loops     *loops.Registry
	logger    *slog.Logger
	now       func() time.Time
}

// NewEngine 构造引擎。
//
// 名字带 Engine 是因为同包已有 Outbox 的 New：两个都叫 New 会让 sidefx.New 有两种
// 含义，调用点读起来就得靠参数猜。
func NewEngine(opts Options) *Engine {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Engine{
		outbox:    opts.Outbox,
		settings:  opts.Settings,
		remover:   opts.Remover,
		refresher: opts.Refresher,
		tasks:     opts.Tasks,
		pool:      opts.Pool,
		loops:     opts.Loops,
		logger:    logger,
		now:       now,
	}
}

// Result 是一轮的可观察结果（日志、测试与面板健康位共用）。
type Result struct {
	// Gated 表示门禁没开（未初始化/已暂停），这一轮没有做任何事。
	Gated bool
	// Locked 表示另一实例正跑着 sidefx，这一轮让给了它。
	Locked bool
	// Reason 是跳过时给人看的一句话。
	Reason string
	// Swept 是本轮从「清单项待删」翻成待办的条数。
	Swept int
	// Due 是本轮到点的待办数；Done/Retried/Stuck/Skipped 是它们的去向。
	Due     int
	Done    int
	Retried int
	Stuck   int
	Skipped int
}

// Run 跑副作用循环直到 ctx 结束。第一轮立即跑，之后按 app_config 的 sidefx_interval 等。
func (e *Engine) Run(ctx context.Context) {
	for {
		result, err := e.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			e.logger.Error("sidefx 循环失败", "err", err)
		}

		wait := GateCheckInterval
		if !result.Gated {
			wait = e.interval(ctx)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// RunOnce 跑一轮。它自己抢 advisory lock、自己记健康位。
func (e *Engine) RunOnce(ctx context.Context) (Result, error) {
	release, locked, err := loops.Guard(ctx, e.pool, LockIDSidefx, "sidefx", e.logger)
	if err != nil {
		return Result{}, err
	}
	if !locked {
		return Result{Locked: true, Reason: "另一实例正在跑副作用循环"}, nil
	}
	defer release()

	started := e.now()
	result, err := e.run(ctx)
	if !result.Gated && !result.Locked {
		if e.loops != nil {
			e.loops.Record(loops.Sidefx, started, err == nil)
		}
		e.logger.Debug("sidefx 循环结束",
			"swept", result.Swept, "due", result.Due, "done", result.Done,
			"retried", result.Retried, "stuck", result.Stuck, "skipped", result.Skipped, "err", err)
	}
	return result, err
}

// run 是 RunOnce 去掉锁与健康位后的那一半。
func (e *Engine) run(ctx context.Context) (Result, error) {
	snapshot, err := e.settings.Load(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("sidefx: 读设置: %w", err)
	}
	if reason := snapshot.GateReason(); reason != "" {
		return Result{Gated: true, Reason: reason}, nil
	}

	result := Result{}
	var errs []error

	// 先把「加种成功后置起的待删」翻成待办：删清单项恒在加下载之后（P7-1/P7-4）。
	swept, err := e.outbox.DrainPendingRemovals(ctx, maxJobsPerRound)
	if err != nil {
		errs = append(errs, err)
	}
	result.Swept = swept

	jobs, err := e.outbox.Due(ctx, maxJobsPerRound)
	if err != nil {
		return result, errors.Join(append(errs, err)...)
	}
	result.Due = len(jobs)

	maxAttempts := snapshot.MaxAttempts(DefaultMaxAttempts)
	for _, job := range jobs {
		action, err := e.execute(ctx, job, maxAttempts)
		if err != nil {
			errs = append(errs, err)
		}
		switch action {
		case actionDone:
			result.Done++
		case actionRetried:
			result.Retried++
		case actionStuck:
			result.Stuck++
		case actionSkipped:
			result.Skipped++
		}
	}

	return result, errors.Join(errs...)
}

// action 是一条待办执行之后的去向。
type action int

const (
	// actionSkipped 是「这一类还没有执行者」：零写入，下一轮再看。
	actionSkipped action = iota
	// actionDone 是执行成功。
	actionDone
	// actionRetried 是这次没成、已按退避排下一次。
	actionRetried
	// actionStuck 是这次没成、已驻留等人工。
	actionStuck
)

// execute 执行一条待办，并按错误类决定「退避重试」还是「驻留」。
func (e *Engine) execute(ctx context.Context, job Job, maxAttempts int) (action, error) {
	consumer := e.consumer(job.Kind)
	if consumer == nil {
		// 这一类还没有执行者（notify 归 Telegram 票）：不动它、不烧预算。
		e.logger.Debug("sidefx: 这一类还没有执行者，本轮跳过", "kind", job.Kind, "target_key", job.TargetKey)
		return actionSkipped, nil
	}

	handleErr := consumer(ctx, job)
	if handleErr == nil {
		if markErr := e.outbox.MarkDone(ctx, job.ID); markErr != nil {
			// 活干成了但没记上：不谎报成功——下轮会再执行一次（两类消费者都幂等）。
			return actionSkipped, markErr
		}
		e.record(ctx, job, task.EventSideEffect, successReason(job))
		return actionDone, nil
	}

	policy := policyFor(job.Kind, maxAttempts)
	attempts := job.Attempts + 1
	if permanent(handleErr) || policy.exhausted(attempts) {
		if stickErr := e.outbox.Stick(ctx, job.ID, handleErr.Error()); stickErr != nil {
			return actionStuck, errors.Join(stickErr, handleErr)
		}
		e.logger.Warn("sidefx: 待办驻留等人工",
			"kind", job.Kind, "target_key", job.TargetKey, "attempts", attempts, "err", handleErr)
		e.record(ctx, job, task.EventAlert, stuckReason(job, attempts, handleErr))
		return actionStuck, nil
	}

	delay := policy.backoff(attempts)
	if retryErr := e.outbox.Reschedule(ctx, job.ID, handleErr.Error(), delay); retryErr != nil {
		return actionRetried, errors.Join(retryErr, handleErr)
	}
	e.logger.Warn("sidefx: 待办退避重试",
		"kind", job.Kind, "target_key", job.TargetKey, "attempts", attempts, "delay", delay, "err", handleErr)
	e.warn(ctx, job, attempts, handleErr)
	return actionRetried, nil
}

// consumer 返回某类待办的执行函数；nil 表示这一类还没有执行者。
func (e *Engine) consumer(kind Kind) func(context.Context, Job) error {
	switch kind {
	case KindListRemoval:
		if e.remover == nil {
			return nil
		}
		return e.removeFromList
	case KindJellyfinRefresh:
		if e.refresher == nil {
			return nil
		}
		return e.refresh
	default:
		// notify 的执行者是 Telegram 集成票（#28）。在它就位前这里没有执行者：
		// 待办留在箱里（不烧预算），面板的副作用队列看得见它。
		return nil
	}
}

// removeFromList 执行一条 list_removal：movie_actions 的 remove（规格 §5.1/§6.2）。
//
// 幂等靠上游：重复 remove 回 success:1（#16），所以「删成功但没落库」的下一轮
// 会直接成功，不需要额外的去重记录。
func (e *Engine) removeFromList(ctx context.Context, job Job) error {
	movieID := payloadString(job, "movie_id")
	listID := payloadString(job, "list_id")
	if movieID == "" || listID == "" {
		// 缺字段是本地校验错（永久的）：再试也变不出这两个 id，驻留等人工。
		return ValidationError{Message: fmt.Sprintf(
			"list_removal 待办缺 movie_id 或 list_id（payload=%s）", string(job.Payload))}
	}

	input, err := e.settings.Input(ctx, "javdb")
	if err != nil {
		return fmt.Errorf("读 JavDB 设置: %w", err)
	}
	if err := e.remover.RemoveFromList(ctx, input.Text("token"), listID, movieID); err != nil {
		return err
	}
	return nil
}

// refresh 执行一条 jellyfin_refresh：目录级 rescan，被拒降级全库（规格 §5.5）。
//
// 目录不在盘（ScopeSkipped）算成功：重试变不出目录，把一条待办钉在需人工区只会
// 让真问题（挂载没对齐）看起来像副作用失败。
func (e *Engine) refresh(ctx context.Context, job Job) error {
	movieID := payloadString(job, "movie_id")
	if movieID == "" {
		return ValidationError{Message: fmt.Sprintf(
			"jellyfin_refresh 待办缺 movie_id（payload=%s）", string(job.Payload))}
	}

	number := payloadString(job, "number")
	if number == "" && e.tasks != nil {
		resolved, err := e.tasks.Number(ctx, movieID)
		if errors.Is(err, task.ErrTaskNotFound) {
			// 任务行没了：重试不会让它回来，驻留等人工。
			return ValidationError{Message: "待办指向的任务不存在: " + movieID}
		}
		if err != nil {
			return fmt.Errorf("读任务 %s 的番号: %w", movieID, err)
		}
		number = resolved
	}

	input, err := e.settings.Input(ctx, "jellyfin")
	if err != nil {
		return fmt.Errorf("读 Jellyfin 设置: %w", err)
	}

	result, err := e.refresher.Refresh(ctx, jellyfin.ConfigFrom(input), number)
	if err != nil {
		return err
	}
	e.logger.Debug("sidefx: Jellyfin 刷新已受理",
		"movie_id", movieID, "scope", result.Scope, "directory", result.Directory)
	return nil
}

// record 往任务时间线追加一条事件；payload 里没有 movie_id 时跳过（时间线要挂任务）。
func (e *Engine) record(ctx context.Context, job Job, kind task.EventKind, reason string) {
	if e.tasks == nil {
		return
	}
	movieID := payloadString(job, "movie_id")
	if movieID == "" {
		return
	}
	if err := e.tasks.AppendEvent(ctx, task.Event{
		MovieID:  movieID,
		Infohash: payloadString(job, "infohash"),
		Kind:     kind,
		Reason:   reason,
	}); err != nil {
		// 事件只是时间线：写不进去不该让一次已经成功的执行变成失败。
		e.logger.Error("sidefx: 记事件失败", "kind", job.Kind, "target_key", job.TargetKey, "err", err)
	}
}

// warn 是「两次失败 = warning」的判据（规格 §5.5 / P11-2）：刷新的第 2 次失败起留痕。
//
// 它只对 jellyfin_refresh 生效：别的 kind 的失败在驻留时才值得进时间线，
// 每一次退避都写一条只会把面板的事件流刷成噪音。
func (e *Engine) warn(ctx context.Context, job Job, attempts int, cause error) {
	if job.Kind != KindJellyfinRefresh || !jellyfin.ShouldWarn(attempts) {
		return
	}
	e.record(ctx, job, task.EventAlert,
		fmt.Sprintf("Jellyfin 刷新连续失败 %d 次，仍在重试：%v", attempts, cause))
}

// successReason 是执行成功那一句（面板的最近事件）。
func successReason(job Job) string {
	done := specFor(job.Kind).done
	if done == "" {
		return specFor(job.Kind).label + "已执行"
	}
	if job.Kind == KindListRemoval {
		// 删除的成功说法要带上是哪一条（同一个 movie 可能出现在多张清单里）。
		return fmt.Sprintf("%s（%s）", done, job.TargetKey)
	}
	return done
}

// stuckReason 是驻留那一句：说清「什么没成」与「第几次」，人工重发才有的放矢。
func stuckReason(job Job, attempts int, cause error) string {
	return fmt.Sprintf("%s 驻留等人工（已尝试 %d 次）：%v", specFor(job.Kind).label, attempts, cause)
}

// payloadString 从 payload 里取一个字符串字段（缺字段/类型不对都回空串）。
func payloadString(job Job, key string) string {
	if len(job.Payload) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return ""
	}
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

// interval 读 app_config 的 sidefx_interval；读不到就用默认值。
func (e *Engine) interval(ctx context.Context) time.Duration {
	snapshot, err := e.settings.Load(ctx)
	if err != nil {
		return DefaultInterval
	}
	return snapshot.Interval("sidefx_interval", DefaultInterval)
}
