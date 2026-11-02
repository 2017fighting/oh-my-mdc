// Package processing 拥有加工阶段（取数 #27 + 整理 #29）共用的失败语义。
//
// 两段在同一个版本状态（processing）上接续，所以「失败怎么记账」只能有一份实现：
//
//   - 退避：每次都更长（1m 起，翻倍，封顶 30m），到期的判据落在 task_version.next_attempt_at；
//   - 耗尽：app_config.max_attempts（默认 3）次之后落 failed(phase, reason)，版本驻留等人工；
//   - 永久错：碰撞/跨设备这类「再试也一样」的失败不烧预算，一次就落 failed；
//   - 通知：「加工失败」只发一条（Telegram 待办按 (event, infohash) 去重），执行归旁路引擎。
//
// 它不碰文件、不碰上游：只把一次失败翻成库里的状态与一条待办。这样取数段与整理段
// 不必各写一遍同一套仪式，也就不会像先前那样一处记告警事件、另一处不记。
package processing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 失败退避与自动重试上限（规格 §4.3 max_attempts / §6.1 失败行）。
const (
	// RetryBase 是第一次失败的退避时长。
	RetryBase = time.Minute
	// RetryCap 是单次退避的上限：再久也只是「下个对账周期再看」。
	RetryCap = 30 * time.Minute
	// DefaultMaxAttempts 与 app_config 的表默认值同一个数（读不到时才用它）。
	DefaultMaxAttempts = sidefx.DefaultMaxAttempts
)

// Backoff 算第 attempts 次失败（从 1 起）之后要等多久：基数翻倍，封顶 RetryCap。
func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := RetryBase
	for i := 1; i < attempts && delay < RetryCap; i++ {
		delay *= 2
	}
	if delay > RetryCap {
		delay = RetryCap
	}
	return delay
}

// Recorder 把一次加工失败翻成库里的状态、一条时间线、一条通知待办。
type Recorder struct {
	store  *task.Store
	outbox *sidefx.Outbox
	logger *slog.Logger
	now    func() time.Time
	owner  string
}

// Options 装配一个失败记账器。
type Options struct {
	Store *task.Store
	// Outbox 是通知入箱口；nil 时只记账不发通知（测试或极端降级）。
	Outbox *sidefx.Outbox
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
	// Now 可注入测试时钟；空则 time.Now。
	Now func() time.Time
	// Owner 是 claim 的持有者标识（与阶段自己的 claim 必须是同一个）。
	Owner string
}

// NewRecorder 装配失败记账器。
func NewRecorder(opts Options) *Recorder {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Recorder{
		store:  opts.Store,
		outbox: opts.Outbox,
		logger: logger,
		now:    now,
		owner:  opts.Owner,
	}
}

// Failure 是一次失败记账的结论（调用方据此写日志/决定要不要把错误传给循环）。
type Failure struct {
	// Attempts 是累计失败次数（含这一次；永久错不烧预算，为 0）。
	Attempts int
	// Exhausted 表示版本已落 failed(phase, reason)，从此只等人工。
	Exhausted bool
}

// Fail 记一次加工失败。
//
// permanent 由阶段自己判（碰撞、跨设备这类只有整理段知道的事），为 true 时直接落
// failed 并发通知；否则按退避重试，耗尽那一次才落 failed + 通知。
//
// 返回的错误只描述**记账本身**失败（数据库不可用、抢输租约）；版本为什么失败在
// cause 里，由调用方决定要不要传出去（取数段会，整理段不会：那已经落在状态机里）。
func (r *Recorder) Fail(ctx context.Context, version task.Version, phase task.Phase, cause error, permanent bool, maxAttempts int) (Failure, error) {
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	reason := strings.TrimSpace(cause.Error())

	if permanent {
		if err := r.store.MarkProcessingFailed(ctx, version.Infohash, r.owner, phase, reason); err != nil {
			return Failure{}, r.swallowClaimLost(err)
		}
		return r.exhausted(ctx, version, phase, Failure{Exhausted: true}, reason, cause)
	}

	delay := Backoff(version.Attempts + 1)
	recorded, err := r.store.RecordProcessingFailure(ctx, version.Infohash, r.owner,
		phase, reason, r.now(), delay, maxAttempts)
	if err != nil {
		return Failure{}, r.swallowClaimLost(err)
	}
	failure := Failure{Attempts: recorded.Attempts, Exhausted: recorded.Exhausted}

	r.logger.Warn("加工失败",
		"movie_id", version.MovieID, "infohash", version.Infohash, "phase", phase,
		"attempts", failure.Attempts, "exhausted", failure.Exhausted, "retry_in", delay, "err", cause)
	if !failure.Exhausted {
		return failure, nil
	}
	return r.exhausted(ctx, version, phase, failure, reason, cause)
}

// exhausted 是「耗尽/永久错」的共同收尾：时间线一条 + 通知待办一条。
//
// 通知的 target_key 是 "<event>:<infohash>"，部分唯一索引保证同一条版本只入箱一次；
// 时间线那一条带上「第几次」，人工在面板上才说得清它为什么不自动再跑了。
func (r *Recorder) exhausted(ctx context.Context, version task.Version, phase task.Phase, failure Failure, reason string, cause error) (Failure, error) {
	message := fmt.Sprintf("加工失败（%s）：%s", phase.Label(), reason)
	if failure.Attempts > 0 {
		message = fmt.Sprintf("加工失败（%s，第 %d 次）：%s", phase.Label(), failure.Attempts, reason)
	}
	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:  version.MovieID,
		Infohash: version.Infohash,
		Kind:     task.EventAlert,
		Phase:    string(phase),
		Reason:   message,
	}); err != nil {
		return failure, fmt.Errorf("processing: 记加工失败事件: %w", err)
	}

	if r.outbox != nil {
		payload := map[string]any{
			"movie_id": version.MovieID,
			"infohash": version.Infohash,
			"number":   version.Number,
			"label":    version.Label,
			"source":   string(version.Source),
			"phase":    phase.Label(),
			"reason":   reason,
		}
		if err := r.outbox.EnqueueNotify(ctx, sidefx.EventProcessingFailed, version.Infohash, payload); err != nil {
			// 通知只是待办：入箱失败不该让「已经落 failed」这件事变成记账失败。
			r.logger.Error("processing: 加工失败通知入箱失败", "infohash", version.Infohash, "err", err)
		}
	}

	r.logger.Error("加工失败，版本驻留等人工",
		"movie_id", version.MovieID, "infohash", version.Infohash, "phase", phase,
		"attempts", failure.Attempts, "err", cause)
	return failure, nil
}

// swallowClaimLost 把「抢输租约」从记账错误里抹掉：别人接手了这条版本，
// 它的账不归我改（规格 §4.4：抢输不是失败）。
func (r *Recorder) swallowClaimLost(err error) error {
	if errors.Is(err, task.ErrClaimLost) {
		return nil
	}
	return err
}
