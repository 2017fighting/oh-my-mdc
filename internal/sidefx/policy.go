package sidefx

import (
	"errors"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/jellyfin"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// 重试节奏的默认值（规格 §4.2 退避 / §5.6 五个上限 / §4.3 max_attempts）。
const (
	// DefaultMaxAttempts 是 app_config.max_attempts 读不到时的兜底（与表默认值一致）。
	DefaultMaxAttempts = 3
	// NotifyMaxAttempts 是 notify 的专用上限：规格 §5.6「瞬时退避最多 5 次后驻留」。
	NotifyMaxAttempts = 5
	// DefaultBackoff 是第一次退避的时长；之后逐次翻倍。
	DefaultBackoff = time.Minute
	// MaxBackoff 是单次退避的上限：不能退到「比人工还慢」。
	MaxBackoff = 30 * time.Minute
)

// kindSpec 是一类待办的 kind 差异：面板/时间线的称呼、执行成功的说法、重试上限覆盖。
//
// 这些差异只有这一份定义（词汇以 GLOSSARY.md 为准：删清单项 / 刷新 Jellyfin /
// 发 Telegram）：执行器、时间线与前端都从这里取词，免得同一个概念三处三种写法。
type kindSpec struct {
	// label 是人话名字：面板的队列行与「驻留等人工」的时间线都用它。
	label string
	// done 是执行成功那一句。
	done string
	// maxAttempts 覆盖 app_config.max_attempts；0 表示跟随设置。
	maxAttempts int
}

var kindSpecs = map[Kind]kindSpec{
	KindListRemoval:     {label: "删清单项", done: "已从清单删除"},
	KindJellyfinRefresh: {label: "刷新 Jellyfin", done: "Jellyfin 已受理刷新"},
	KindNotify:          {label: "发 Telegram", maxAttempts: NotifyMaxAttempts},
}

// specFor 取一类待办的 kind 差异。未知 kind 给一份中性的：库里可能有更新的 kind，
// 引擎不该因此 panic，面板也还能把 target_key 读出来。
func specFor(kind Kind) kindSpec {
	if spec, ok := kindSpecs[kind]; ok {
		return spec
	}
	return kindSpec{label: string(kind)}
}

// ValidationError 是引擎自己的本地校验错（待办缺字段、任务行读不到）。
//
// 它归 business：配置与数据不会因为重试而长出来，驻留等人工才有意义。
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string { return e.Message }

// Policy 是一类待办的重试节奏。
//
// 上限由两部分驱动（规格 §4.2/§5.6）：**kind**（notify 固定 5 次，其余走
// app_config.max_attempts）与**错误类**（永久错立刻驻留，不烧预算）。
type Policy struct {
	// MaxAttempts 是自动尝试的次数上限（含第一次）。
	MaxAttempts int
	// Backoff 是第一次退避；之后翻倍到 Cap。
	Backoff time.Duration
	// Cap 是单次退避的上限。
	Cap time.Duration
}

// policyFor 决定一类待办的重试节奏。
//
// 上限由 kind 覆盖设置（规格 §5.6：Telegram 的 5 次预算是它自己定的，不跟
// app_config 走——通知比删清单项更「可丢」，但丢之前值得多试两次）。
func policyFor(kind Kind, maxAttempts int) Policy {
	if spec := specFor(kind); spec.maxAttempts > 0 {
		maxAttempts = spec.maxAttempts
	}
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	return Policy{MaxAttempts: maxAttempts, Backoff: DefaultBackoff, Cap: MaxBackoff}
}

// backoff 算第 attempts 次失败（attempts 从 1 起）之后要等多久：Backoff·2^(attempts-1)，封顶 Cap。
func (p Policy) backoff(attempts int) time.Duration {
	if attempts <= 0 {
		attempts = 1
	}
	delay := p.Backoff
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= p.Cap {
			return p.Cap
		}
	}
	if delay > p.Cap {
		return p.Cap
	}
	return delay
}

// exhausted 报告第 attempts 次失败是否已经用完自动尝试的预算。
func (p Policy) exhausted(attempts int) bool {
	return attempts >= p.MaxAttempts
}

// permanent 报告一次失败是不是「再试也一样」：永久错立刻驻留，不烧重试预算。
//
// 判据由**错误类**驱动，不按 kind 猜：
//
//   - auth：凭据要人换（JavDB token 被踢、Jellyfin 拒 key）——重试只会刷无效请求；
//   - business：缺字段、任务行没了、目录不在盘、服务端规则不认这次请求——重试变不出配置；
//   - network / timeout / protocol：瞬时，按指数退避重试到上限（规格 §5.6 分流）。
//
// 分类本身不在本包重写：JavDB 用 appapi.Classify，Jellyfin 用 jellyfin.Classify，
// 两边都产出规格 §8.4 的同一套机器码。
func permanent(err error) bool {
	switch errorClass(err) {
	case settings.CodeAuth, settings.CodeBusiness:
		return true
	default:
		return false
	}
}

// errorClass 把一次失败归成规格 §8.4 的分类码（统一成 settings.TestCode 一种词汇）。
func errorClass(err error) settings.TestCode {
	var invalid ValidationError
	if errors.As(err, &invalid) {
		return settings.CodeBusiness
	}
	var jellyfinErr *jellyfin.Error
	var jellyfinInvalid jellyfin.ValidationError
	if errors.As(err, &jellyfinErr) || errors.As(err, &jellyfinInvalid) {
		return jellyfin.Classify(err)
	}
	return settings.TestCode(appapi.Classify(err))
}
