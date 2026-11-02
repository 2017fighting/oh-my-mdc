// Package poll 是清单轮询循环：JavDB 想看清单 → 定源（M-Team 优先）→ 先落库 → 加种 → 置待删。
//
// 它是第一条真实流水线切片（规格 §6.1 的「建任务/版本」「定源」「加下载」「去重」
// 「置待删」五行与 §6.2 的 poll 通道）：
//
//   - 单飞：一轮 poll 先抢一把 pg_advisory_lock（专用连接），抢不到就跳过；
//   - 门禁：三件套不齐或已暂停时零动作（面板那边整站只剩向导）；
//   - 去重：任务已有版本就不再查源；同 infohash 已有版本行就零写入；
//   - 定源：JavDB 有磁力才查 M-Team（限速意识）；M-Team 命中就取私有种子，
//     取不到（未配置/禁用/搜索无果/取种失败）就**同轮降级** JavDB 磁力；
//   - 顺序：版本行先以 add_pending=true 落库，qBittorrent 成功才清（ADR-0004）。
//
// 真正删清单项、刷 Jellyfin、发 Telegram 由旁路引擎执行（#26）：本包只入箱。
package poll

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/loops"
	"github.com/2017fighting/oh-my-mdc/internal/mteam"
	"github.com/2017fighting/oh-my-mdc/internal/pin"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// LockIDPoll 是 poll 的 advisory lock key。三条循环各持一把（规格 §4.4）。
const LockIDPoll int64 = 4230001

// DefaultInterval 是 app_config 读不到 poll_interval 时的兜底（与表默认值一致）。
const DefaultInterval = 15 * time.Minute

// GateCheckInterval 是「门禁还没开」时的探路节奏：配完向导不必等满一个 15 分钟周期。
const GateCheckInterval = 30 * time.Second

// Settings 是本循环对设置域的全部要求。
type Settings interface {
	Load(ctx context.Context) (settings.Snapshot, error)
	Input(ctx context.Context, section string) (settings.TestInput, error)
}

// Source 是清单与磁力候选的上游（真实实现是 javdb.Integration）。
//
// AllListItems 是**全量**扫到底（空页即止），与 appapi.Client.ListItems 只读第一页不同。
type Source interface {
	AllListItems(ctx context.Context, token, listID string) ([]appapi.ListItem, error)
	Magnets(ctx context.Context, token, movieID string) ([]appapi.Magnet, error)
}

// Adder 是加种接缝（真实实现是 qbittorrent.Integration）。
type Adder interface {
	Ensure(ctx context.Context, cfg qbittorrent.Config, torrent qbittorrent.Torrent) (qbittorrent.Outcome, error)
}

// Torrents 是 M-Team 取种接缝（真实实现是 mteam.Integration）。
//
// 只暴露「番号 → 一份私有种子」这一件事：搜索、选种、换链接、下载、算 infohash
// 都是 M-Team 自己的事，poll 只决定**什么时候**问它（JavDB 有磁力且任务还没有版本）。
type Torrents interface {
	Resolve(ctx context.Context, apiKey, number string) (mteam.Torrent, error)
}

// Options 装配一个 poll 循环。
type Options struct {
	Store    *task.Store
	Outbox   *sidefx.Outbox
	Settings Settings
	Source   Source
	Adder    Adder
	// Torrents 是可选来源（M-Team）；nil 时定源只走 JavDB 磁力。
	Torrents Torrents
	// TorrentsDir 是 .torrent 备份目录；空则用 os.TempDir()/oh-my-mdc/torrents。
	TorrentsDir string
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

// Runner 是 poll 循环。
type Runner struct {
	store       *task.Store
	outbox      *sidefx.Outbox
	settings    Settings
	source      Source
	adder       Adder
	torrents    Torrents
	torrentsDir string
	pool        *pgxpool.Pool
	loops       *loops.Registry
	logger      *slog.Logger
	owner       string
	now         func() time.Time
}

// New 构造循环。
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

	torrentsDir := strings.TrimSpace(opts.TorrentsDir)
	if torrentsDir == "" {
		torrentsDir = defaultTorrentsDir()
	}

	return &Runner{
		store:       opts.Store,
		outbox:      opts.Outbox,
		settings:    opts.Settings,
		source:      opts.Source,
		adder:       opts.Adder,
		torrents:    opts.Torrents,
		torrentsDir: torrentsDir,
		pool:        opts.Pool,
		loops:       opts.Loops,
		logger:      logger,
		owner:       owner,
		now:         now,
	}
}

// Result 是一轮 poll 的可观察结果（日志、测试与面板健康位共用）。
type Result struct {
	// Gated 表示门禁没开（未初始化/已暂停），这一轮没有做任何事。
	Gated bool
	// Locked 表示另一实例正跑着 poll，这一轮让给了它。
	Locked bool
	// Reason 是跳过时给人看的一句话。
	Reason string
	// Scanned 是本轮清单项数；Queried 是真正查了磁力的项数。
	Scanned int
	Queried int
	// Candidates 是新建的版本行数；Duplicates 是已存在（零写入）的候选数。
	Candidates int
	Duplicates int
	// Torrents 是本轮 M-Team 取种成功（落成 mteam_torrent 版本）的条数。
	Torrents int
	// Fallbacks 是本轮「M-Team 启用但取不到」而降级磁力的次数（禁用不算，那根本没有尝试）。
	Fallbacks int
	// Added 是这一轮加种成功（含收编/已存在）的版本数。
	Added int
}

// Run 跑循环直到 ctx 结束。第一轮立即跑，之后按 app_config 的 poll_interval 等。
//
// 门禁没开时用 GateCheckInterval 探路：配完向导后不必等满一个轮询周期才看见任务。
func (r *Runner) Run(ctx context.Context) {
	for {
		result, err := r.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			r.logger.Error("poll 循环失败", "err", err)
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

// RunOnce 跑一轮。它自己抢 advisory lock、自己记健康位。
func (r *Runner) RunOnce(ctx context.Context) (Result, error) {
	release, locked, err := loops.Guard(ctx, r.pool, LockIDPoll, "poll", r.logger)
	if err != nil {
		return Result{}, err
	}
	if !locked {
		return Result{Locked: true, Reason: "另一实例正在跑 poll"}, nil
	}
	defer release()

	started := r.now()
	result, err := r.run(ctx)
	if !result.Gated && !result.Locked {
		if r.loops != nil {
			r.loops.Record(loops.Poll, started, err == nil)
		}
		r.logger.Debug("poll 循环结束",
			"scanned", result.Scanned, "queried", result.Queried,
			"candidates", result.Candidates, "torrents", result.Torrents,
			"fallbacks", result.Fallbacks, "added", result.Added, "err", err)
	}
	return result, err
}

// run 是 RunOnce 去掉锁与健康位后的那一半。
func (r *Runner) run(ctx context.Context) (Result, error) {
	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("poll: 读设置: %w", err)
	}
	if reason := snapshot.GateReason(); reason != "" {
		return Result{Gated: true, Reason: reason}, nil
	}

	list, ok := wantWatchList(snapshot)
	if !ok {
		return Result{Gated: true, Reason: "想看清单还没登记"}, nil
	}

	javdbInput, err := r.settings.Input(ctx, "javdb")
	if err != nil {
		return Result{}, fmt.Errorf("poll: 读 JavDB 设置: %w", err)
	}
	qbtInput, err := r.settings.Input(ctx, "qbittorrent")
	if err != nil {
		return Result{}, fmt.Errorf("poll: 读 qBittorrent 设置: %w", err)
	}
	token := javdbInput.Text("token")
	qbtConfig := qbittorrent.ConfigFrom(qbtInput)
	access := r.newMTeamAccess(ctx, snapshot)

	var result Result
	var errs []error

	items, err := r.source.AllListItems(ctx, token, list.ListID)
	if err != nil {
		// 清单读失败也要把已有的待加种版本推一步：那是独立于上游的活。
		errs = append(errs, fmt.Errorf("poll: 扫清单 %s: %w", list.ListID, err))
	}

	sizes := make(map[string]int64)
	for _, item := range items {
		result.Scanned++
		if item.MovieID == "" || strings.TrimSpace(item.Number) == "" {
			continue
		}

		hasVersion, err := r.store.TaskHasVersion(ctx, item.MovieID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if hasVersion {
			// P7-6：任务已有版本就不再查源（新版本场景由 v2 的重开语义负责）。
			continue
		}

		result.Queried++
		magnets, err := r.source.Magnets(ctx, token, item.MovieID)
		if err != nil {
			errs = append(errs, fmt.Errorf("poll: 读 %s 的磁力: %w", item.MovieID, err))
			continue
		}

		best, ok := pin.Select(magnets)
		if !ok {
			// P7-3：磁力为空 → 零行落库，下轮重查。
			// 顺带一提，M-Team 也是到这里才考虑查的（限速意识：JavDB 没有就不问 M-Team）。
			continue
		}

		// 先摆好磁力兜底的形状（规格 §6.1 定源行）：M-Team 任何一步失败都落它。
		candidate := task.Candidate{
			MovieID:  item.MovieID,
			Number:   item.Number,
			ListID:   list.ListID,
			Infohash: best.Hash,
			Source:   task.SourceJavDBMagnet,
			Delivery: task.DeliveryMagnet,
			Label:    labelFor(best),
			Payload:  appapi.MagnetLink(best.Hash, item.Number),
		}
		size := best.SizeMiB

		var fallbackReason string
		if access.enabled {
			// M-Team 优先（P5-2）：命中就只走种子这一条，不再落磁力版本。
			torrent, sourceErr := access.prepare(ctx, item.Number)
			if sourceErr != nil {
				// 降级（P5-3）：同轮落磁力版本。
				result.Fallbacks++
				if mteam.IsNotFound(sourceErr) {
					// M-Team 上没有这部片是答案，不是故障：降级但不留告警红字。
					r.logger.Debug("M-Team 没有这部片，走磁力", "movie_id", item.MovieID, "number", item.Number)
				} else {
					fallbackReason = sourceErr.Error()
					r.logger.Warn("M-Team 取种失败，降级 JavDB 磁力",
						"movie_id", item.MovieID, "number", item.Number, "err", sourceErr)
				}
			} else {
				candidate = candidateFromTorrent(item, list.ListID, torrent)
				size = bytesToMiB(torrent.torrent.SizeBytes)
			}
		}

		inserted, err := r.store.SaveCandidate(ctx, candidate)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !inserted {
			// 同 infohash 已有版本行：不重复加种、不重复置待删（规格 §6.1 去重行）。
			result.Duplicates++
			continue
		}

		result.Candidates++
		sizes[candidate.Infohash] = size
		if candidate.Delivery == task.DeliveryTorrent {
			result.Torrents++
			continue
		}
		if fallbackReason != "" {
			r.recordMTeamFallback(ctx, candidate, fallbackReason)
		}
	}

	// 先落库再加种：到这里本轮所有候选都已落库，接下来统一加种（含上轮遗留的 pending）。
	added, addErrs := r.addPending(ctx, qbtConfig, sizes)
	result.Added = added
	errs = append(errs, addErrs...)

	return result, errors.Join(errs...)
}

// addPending 把 add_pending=true 的版本逐条交给 qBittorrent。
//
// 先列名单再逐条 claim：某一条加种失败、租约放回之后，同一轮不会反复捡到它。
// 每条都先 claim 再干活：claim 抢输不是失败（ErrClaimLost），只是这次轮不到本进程。
func (r *Runner) addPending(ctx context.Context, cfg qbittorrent.Config, sizes map[string]int64) (int, []error) {
	pending, err := r.store.PendingAdds(ctx, maxAddsPerRound)
	if err != nil {
		return 0, []error{err}
	}

	added := 0
	var errs []error
	for _, candidate := range pending {
		version, err := r.store.Claim(ctx, candidate.Infohash, r.owner)
		if errors.Is(err, task.ErrClaimLost) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}

		ok, err := r.addOne(ctx, version, cfg, sizes)
		if err != nil {
			errs = append(errs, err)
		}
		if ok {
			added++
		}
	}
	return added, errs
}

// maxAddsPerRound 是一轮最多加多少条：挡住异常积压把一轮 poll 拖成无尽循环。
const maxAddsPerRound = 500

// addOne 加一条版本：进程内锁 → Ensure → 成功清 pending/置待删/入箱通知。
func (r *Runner) addOne(ctx context.Context, version task.Version, cfg qbittorrent.Config, sizes map[string]int64) (bool, error) {
	unlock := r.store.Lock(version.Infohash)
	defer unlock()

	delivery, err := deliveryFor(version, cfg)
	if err != nil {
		// 交付形态的材料没了（私种的备份路径读不到）：保留 pending、下轮再试，
		// 并把原因写进时间线——静默重试会让面板上只剩一个永远不动的「下载中」。
		r.abandonAdd(ctx, version, err.Error())
		return false, fmt.Errorf("poll: 准备加种材料 %s: %w", version.Infohash, err)
	}

	outcome, err := r.adder.Ensure(ctx, cfg, delivery)
	if err != nil {
		// 加种失败：保留 add_pending，放掉租约，记一条告警，下轮重试。
		r.abandonAdd(ctx, version, fmt.Sprintf("加种失败：%v", err))
		return false, fmt.Errorf("poll: 加种 %s: %w", version.Infohash, err)
	}

	// 通知入箱（收编同发，规格 §5.6）。执行归旁路引擎（#26）。
	//
	// 顺序是「先入箱、后清 pending」：入箱靠 (kind,target_key) 的部分唯一索引幂等，
	// 崩在这两步之间时下一轮会重新 Ensure（此时是 AlreadyTracked）并重试一次入箱
	// （no-op），所以通知不会丢；反过来先清 pending 就会永久丢掉这条通知。
	payload := map[string]any{
		"number": version.Number,
		"label":  version.Label,
		"source": string(version.Source),
	}
	if size, ok := sizes[version.Infohash]; ok {
		payload["size_mib"] = size
	}
	if err := r.outbox.EnqueueNotify(ctx, sidefx.EventDownloadStarted, version.Infohash, payload); err != nil {
		if releaseErr := r.store.Release(ctx, version.Infohash, r.owner); releaseErr != nil {
			r.logger.Error("poll: 入箱失败后释放租约失败", "infohash", version.Infohash, "err", releaseErr)
		}
		return false, err
	}

	if err := r.store.MarkAdded(ctx, version.Infohash, r.owner); err != nil {
		if errors.Is(err, task.ErrClaimLost) {
			// 租约已经过期并被别人接手：这次的加种结果不该覆盖当前占用者的账。
			return false, nil
		}
		return false, err
	}

	// 事件只是时间线：写不进去不该让一次已经成功的加种变成失败。
	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:  version.MovieID,
		Infohash: version.Infohash,
		Kind:     task.EventSideEffect,
		Reason:   fmt.Sprintf("已加种（%s），下载开始通知入箱", outcome),
	}); err != nil {
		r.logger.Error("poll: 记加种事件失败", "infohash", version.Infohash, "err", err)
	}
	return true, nil
}

// abandonAdd 放弃这一次加种：放回租约、记一条告警，保留 add_pending 等下轮重试。
func (r *Runner) abandonAdd(ctx context.Context, version task.Version, reason string) {
	if err := r.store.Release(ctx, version.Infohash, r.owner); err != nil {
		r.logger.Error("poll: 释放失败版本的租约失败", "infohash", version.Infohash, "err", err)
	}
	r.recordAlert(ctx, version, reason)
}

// recordAlert 记一条失败事件（时间线 + 后续告警判据都靠它）。
func (r *Runner) recordAlert(ctx context.Context, version task.Version, reason string) {
	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:  version.MovieID,
		Infohash: version.Infohash,
		Kind:     task.EventAlert,
		Phase:    string(task.PhaseDownloading),
		Reason:   reason,
	}); err != nil {
		r.logger.Error("poll: 记加种失败事件失败", "infohash", version.Infohash, "err", err)
	}
}

// stashedTorrent 是取种 + 落盘之后的产物：字节已经有一份在盘上，payload 存它的路径。
type stashedTorrent struct {
	torrent mteam.Torrent
	path    string
}

// mteamAccess 是「这一轮要不要问 M-Team」的惰性判据。
//
// 门禁用设置快照判定（P5-1：未配置/禁用 → 零调用），api key 只在真的要用时才读：
// 大多数轮里清单项都已有版本，没必要每轮把凭据从设置域读出来。
type mteamAccess struct {
	runner  *Runner
	ctx     context.Context
	enabled bool

	loaded bool
	key    string
	err    error
}

// newMTeamAccess 按设置快照构造这一轮的 M-Team 判据。
func (r *Runner) newMTeamAccess(ctx context.Context, snapshot settings.Snapshot) *mteamAccess {
	section, ok := snapshot.Sections["mteam"]
	enabled := r.torrents != nil && ok && section.Configured && section.Enabled != nil && *section.Enabled
	return &mteamAccess{runner: r, ctx: ctx, enabled: enabled}
}

// prepare 取一条种子并把它落到备份路径（规格 §5.2）。
//
// 配置读不出来（已启用却没 key 或设置域出问题）与取种失败同样处理：都降级磁力，
// 但原因会写进任务时间线 —— 否则「为什么这部片走的是磁力」在面板上无从追溯。
func (a *mteamAccess) prepare(ctx context.Context, number string) (stashedTorrent, error) {
	key, err := a.apiKey()
	if err != nil {
		return stashedTorrent{}, err
	}
	torrent, err := a.runner.torrents.Resolve(ctx, key, number)
	if err != nil {
		return stashedTorrent{}, err
	}
	path, err := stashTorrent(a.runner.torrentsDir, torrent.Infohash, torrent.Bytes)
	if err != nil {
		return stashedTorrent{}, err
	}
	return stashedTorrent{torrent: torrent, path: path}, nil
}

// apiKey 读一次 api key，并把这一轮的结果缓存下来。
func (a *mteamAccess) apiKey() (string, error) {
	if a.loaded {
		return a.key, a.err
	}
	a.loaded = true

	input, err := a.runner.settings.Input(a.ctx, "mteam")
	if err != nil {
		a.err = fmt.Errorf("读 M-Team 设置失败: %w", err)
		return a.key, a.err
	}
	a.key = strings.TrimSpace(input.Text("api_key"))
	if a.key == "" {
		a.err = errors.New("M-Team 已启用但 api_key 为空")
	}
	return a.key, a.err
}

// recordMTeamFallback 把 M-Team 的失败记成一条告警事件（面板时间线看得到）。
//
// 它不把一轮 poll 判成失败：降级是设计好的路径，M-Team 挂了不该让循环健康位变红。
// 但它必须留下痕迹——否则「为什么这部片走的是磁力」在面板上无从追溯。
func (r *Runner) recordMTeamFallback(ctx context.Context, candidate task.Candidate, reason string) {
	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:  candidate.MovieID,
		Infohash: candidate.Infohash,
		Kind:     task.EventAlert,
		Phase:    string(task.PhaseDownloading),
		Reason:   "M-Team 定源失败，已降级 JavDB 磁力：" + reason,
	}); err != nil {
		r.logger.Error("poll: 记 M-Team 降级事件失败", "movie_id", candidate.MovieID, "err", err)
	}
}

// interval 读 app_config 的 poll_interval；读不到就用默认值。
func (r *Runner) interval(ctx context.Context) time.Duration {
	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return DefaultInterval
	}
	return snapshot.Interval("poll_interval", DefaultInterval)
}

// wantWatchList 从设置快照里取「想看」清单。
func wantWatchList(snapshot settings.Snapshot) (settings.ListEntry, bool) {
	entries, _ := snapshot.Sections["list_registry"].Data["entries"].([]settings.ListEntry)
	for _, entry := range entries {
		if entry.Purpose == "want_watch" && strings.TrimSpace(entry.ListID) != "" {
			return entry, true
		}
	}
	return settings.ListEntry{}, false
}

// labelFor 是版本 label 的 best-effort 取值：磁力只带 cnsub，判得出「中文」。
//
// 词表只有一份定义（mteam.LabelChinese）：两条定源路径的 label 必须落在同一个词上。
func labelFor(magnet appapi.Magnet) string {
	if magnet.Cnsub {
		return mteam.LabelChinese
	}
	return ""
}
