// Package metrics 是规格 §9 的四族指标：数据、/metrics 的文本输出，以及各上游的计量接缝。
//
// 数据来源分三类，各有唯一的出产处：
//
//   - 循环：loops.Registry 的 last_success 与迭代计数（poll / reconcile / sidefx 三条都读它）；
//   - 上游：各客户端在自己的请求出口喂 Recorder（七个上游一个不少）；
//   - 驻留副作用：StuckSource，真实实现是 sidefx.Outbox——抓取时按 kind 数库里的行，
//     所以它与面板「需人工」区的数字同源。
//
// 实现不引 Prometheus 客户端库：v1 的最小集只有四族，手写 exposition 文本比拖一整个
// 注册表省事，也避免「哪条指标存在」散落在库的初始化代码里。文本由测试里的官方
// Prometheus 解析器校验（只有测试依赖它），形状不是靠人眼保证的。
package metrics

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/loops"
)

// 上游名字（规格 §9 的 upstream 标签取值）。
const (
	UpstreamJavDB       = "javdb"
	UpstreamMTeam       = "mteam"
	UpstreamDMM         = "dmm"
	UpstreamQbittorrent = "qbittorrent"
	UpstreamJellyfin    = "jellyfin"
	UpstreamTelegram    = "telegram"
	UpstreamOpenAI      = "openai"
)

// Upstreams 是七个上游的固定顺序：文本输出恒列满，没打过请求的也是 0。
var Upstreams = []string{
	UpstreamJavDB,
	UpstreamMTeam,
	UpstreamDMM,
	UpstreamQbittorrent,
	UpstreamJellyfin,
	UpstreamTelegram,
	UpstreamOpenAI,
}

// Result 是一次上游请求的结局（规格 §9 的 result 标签取值）。
type Result string

const (
	// ResultOK 是请求走通（2xx、或上游用成功信封回的「答案是空」）。
	ResultOK Result = "ok"
	// ResultTransient 是值得重试的失败：网络、超时、429/5xx。
	ResultTransient Result = "transient"
	// ResultPermanent 是重试变不出来的失败：凭据失效（401/403 类、JWTVerificationError、
	// telegram 400/401/403、mteam 鉴权 code）。UpstreamCredentialsInvalid 告警就数它。
	ResultPermanent Result = "permanent"
)

// Results 是三种结局的固定顺序。
var Results = []Result{ResultOK, ResultTransient, ResultPermanent}

// Recorder 记一次上游请求的结局。
//
// 它是各客户端与指标包之间的唯一接缝：*Registry 是正式实现；nil 是合法的 no-op，
// 所以测试里不传就什么都不记。
type Recorder interface {
	RecordUpstream(upstream string, result Result)
}

// ObserveUpstream 记一次上游调用：err 为 nil 即 ok，否则按 permanent 判据分永久/瞬时。
//
// permanent 是各客户端自己的分类钩子（IsPermanent 一类）：分类只在包内单点定义，
// 这里只负责把它落到标签上。
func ObserveUpstream(recorder Recorder, upstream string, err error, permanent func(error) bool) {
	if recorder == nil {
		return
	}
	result := ResultOK
	if err != nil {
		result = ResultTransient
		if permanent != nil && permanent(err) {
			result = ResultPermanent
		}
	}
	recorder.RecordUpstream(upstream, result)
}

// StuckSource 按 kind 数驻留副作用（真实实现是 sidefx.Outbox.StuckByKind）。
//
// 指标包不直接依赖 sidefx：sidefx 依赖 telegram，而 telegram 依赖本包的 Recorder，
// 反过来依赖就成环了。
type StuckSource interface {
	StuckByKind(ctx context.Context) (map[string]int, error)
}

// scrapeTimeout 是一次 /metrics 抓取最多等多久（只为那条要读库的驻留指标）。
const scrapeTimeout = 3 * time.Second

// contentType 是 Prometheus 文本格式 0.0.4 的正式 Content-Type。
const contentType = "text/plain; version=0.0.4; charset=utf-8"

// 循环迭代的两种结局（规格 §9 的 result 取值）：与上游的 Result 词表不同，
// 循环只有成败两态。
const (
	loopResultOK    = "ok"
	loopResultError = "error"
)

type counterKey struct {
	upstream string
	result   Result
}

// Registry 是四族指标的进程内数据 + 文本输出。
//
// 循环与驻留的那两族现读现算（循环是进程内状态，驻留必须与库一致）；
// 上游那族是自增计数器，重启归零——prometheus 的 counter 语义本该如此。
type Registry struct {
	loopStatus *loops.Registry
	stuck      StuckSource
	// startedAt 是「从没成功过」的基线：启动 40 分钟仍没成功就报停滞，
	// 而不是一上来就把 time()-0 算成 55 年的停滞。
	startedAt time.Time
	logger    *slog.Logger

	mu       sync.RWMutex
	upstream map[counterKey]uint64
}

// New 装配登记处。loopStatus 与 stuck 都允许为 nil（测试场景）：前者按「还没跑过」渲染，
// 后者让驻留那族不出数。
func New(loopStatus *loops.Registry, stuck StuckSource, logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		loopStatus: loopStatus,
		stuck:      stuck,
		startedAt:  time.Now(),
		logger:     logger,
		upstream:   make(map[counterKey]uint64),
	}
}

// RecordUpstream 实现 Recorder。
func (r *Registry) RecordUpstream(upstream string, result Result) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.upstream[counterKey{upstream: upstream, result: result}]++
	r.mu.Unlock()
}

// Count 读一条上游计数（面板与测试用；指标本身走文本输出）。
func (r *Registry) Count(upstream string, result Result) uint64 {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.upstream[counterKey{upstream: upstream, result: result}]
}

// Handler 是 GET /metrics 的处理函数。
//
// 它只管输出：进程不活着就没人回这个端点，所以这里不折叠数据库——库不可用只让
// 驻留那一族缺席（并记一条 warn），循环与上游照常出数。
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), scrapeTimeout)
		defer cancel()

		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		r.Write(ctx, w)
	})
}

// Write 把四族指标按 Prometheus 文本格式写出去。
//
// 写得出的错误只有网络那一侧（ResponseWriter），这里不回报：抓取端看到的短读
// 不是我们能补救的事。
func (r *Registry) Write(ctx context.Context, w io.Writer) {
	if r == nil {
		return
	}

	writeLoops(w, r.loopStatus, r.startedAt)
	writeUpstreams(w, r)
	writeStuck(ctx, w, r.stuck, r.logger)
}

// writeLoops 写两条循环指标：最近一次成功的时间与迭代计数。
//
// 三条循环恒列满：从没成功过的用启动时间当基线（见 Registry.startedAt）。
func writeLoops(w io.Writer, status *loops.Registry, startedAt time.Time) {
	fmt.Fprintln(w, "# HELP mdc_loop_last_success_unix_seconds 三条循环最近一次成功的时间（Unix 秒）；从没成功过时是进程启动时间。")
	fmt.Fprintln(w, "# TYPE mdc_loop_last_success_unix_seconds gauge")
	for _, name := range loops.All {
		lastSuccess := startedAt
		if health := status.Health(name); health.LastSuccessAt != nil {
			lastSuccess = *health.LastSuccessAt
		}
		fmt.Fprintf(w, "mdc_loop_last_success_unix_seconds{loop=\"%s\"} %d\n", escapeLabel(name), lastSuccess.Unix())
	}

	fmt.Fprintln(w, "# HELP mdc_loop_iterations_total 三条循环的迭代计数；门禁没开或抢锁抢输的轮次不算。")
	fmt.Fprintln(w, "# TYPE mdc_loop_iterations_total counter")
	for _, name := range loops.All {
		iterations := status.Health(name).Iterations
		fmt.Fprintf(w, "mdc_loop_iterations_total{loop=\"%s\",result=\"%s\"} %d\n",
			escapeLabel(name), loopResultOK, iterations.OK)
		fmt.Fprintf(w, "mdc_loop_iterations_total{loop=\"%s\",result=\"%s\"} %d\n",
			escapeLabel(name), loopResultError, iterations.Error)
	}
}

// writeUpstreams 写七个上游的请求计数。
//
// 七上游 × 三结局恒列满（没打过也是 0）：UpstreamCredentialsInvalid 的
// `increase(...{result="permanent"}[15m]) > 0` 只有在系列存在时才有意义。
func writeUpstreams(w io.Writer, r *Registry) {
	fmt.Fprintln(w, "# HELP mdc_upstream_requests_total 上游请求数，按结局分；permanent=凭据失效类（401/403、JWTVerificationError 等）。")
	fmt.Fprintln(w, "# TYPE mdc_upstream_requests_total counter")

	r.mu.RLock()
	counts := make(map[counterKey]uint64, len(r.upstream))
	for key, value := range r.upstream {
		counts[key] = value
	}
	r.mu.RUnlock()

	for _, upstream := range Upstreams {
		for _, result := range Results {
			fmt.Fprintf(w, "mdc_upstream_requests_total{upstream=\"%s\",result=\"%s\"} %d\n",
				escapeLabel(upstream), escapeLabel(string(result)), counts[counterKey{upstream: upstream, result: result}])
		}
	}

	// 不认识的标签组合也要出数：漏掉一条生产上的调用比多一行难看得多。
	var extras []counterKey
	for key := range counts {
		if !knownUpstream(key.upstream) || !knownResult(key.result) {
			extras = append(extras, key)
		}
	}
	sort.Slice(extras, func(i, j int) bool {
		if extras[i].upstream != extras[j].upstream {
			return extras[i].upstream < extras[j].upstream
		}
		return extras[i].result < extras[j].result
	})
	for _, key := range extras {
		fmt.Fprintf(w, "mdc_upstream_requests_total{upstream=\"%s\",result=\"%s\"} %d\n",
			escapeLabel(key.upstream), escapeLabel(string(key.result)), counts[key])
	}
}

// writeStuck 写驻留副作用数。读库失败只让这一族缺席：抓取不该因为库抖了就整个失败。
func writeStuck(ctx context.Context, w io.Writer, stuck StuckSource, logger *slog.Logger) {
	if stuck == nil {
		return
	}
	counts, err := stuck.StuckByKind(ctx)
	if err != nil {
		logger.Warn("数驻留副作用失败，本次不输出 mdc_side_effect_stuck", "err", err)
		return
	}

	fmt.Fprintln(w, "# HELP mdc_side_effect_stuck 驻留（需人工）的副作用数，按 kind；抓取时数库里的行。")
	fmt.Fprintln(w, "# TYPE mdc_side_effect_stuck gauge")

	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		fmt.Fprintf(w, "mdc_side_effect_stuck{kind=\"%s\"} %d\n", escapeLabel(kind), counts[kind])
	}
}

func knownUpstream(upstream string) bool {
	for _, candidate := range Upstreams {
		if candidate == upstream {
			return true
		}
	}
	return false
}

func knownResult(result Result) bool {
	for _, candidate := range Results {
		if candidate == result {
			return true
		}
	}
	return false
}

// escapeLabel 按 Prometheus 文本格式转义标签值里的反斜杠、双引号与换行。
func escapeLabel(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return replacer.Replace(value)
}
