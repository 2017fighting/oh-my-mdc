// Package retry 拥有「人工重试」这一件事（规格 §6.1 人工重试行 / §8.2 / 验收 P13-1/2/3）。
//
// 面板上的两个按钮（「重试」与「强制重刮」）落到同一段判断上，逐条版本三分支：
//
//   - **回加工开头**：失败在加工阶段（scrape/organize）或本来就停在 processing 的版本，
//     清错误、清尝试计数与退避、放回租约，下轮取数与整理各自从头跑（幂等）。
//   - **重新加种**：下载阶段的版本按版本行存的 payload（磁力串或 .torrent 备份路径）重加，
//     成功才回 downloading——**允许重下**；重加失败保持 failed，可以再试。
//     私种备份没了（重启清空了 /tmp）时与 poll 走同一处重取（material 包，ADR-0011）：
//     重新加种不该是一条死路。
//   - **强制重刮**：只有 force=true 才碰已 finished 的版本。旧 raw_snapshot 在覆盖前
//     先落进 manual_retry 事件的 detail（新旧对比可查），版本回加工开头并置起
//     force_rescrape，取数段见它就不再复用元数据。
//
// 一条硬边界：**qBittorrent 里仍在下载的版本永不重下**。判断只认 qbt 的权威读数
// （progress < 1），跳过时留一条说明，不静默。
//
// 互斥与其它三条通道一致：进程内 per-version 锁 + 版本行租约（规格 §4.4），
// 抢输不是失败——重试只是「这次没轮到」，面板回一句「稍后再试」。
package retry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/material"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// Action 是一次重试对一条版本的结局（面板按它说话，也是接口的机器可读部分）。
//
// 取值来自 task 包：同一条字符串还被「需要人工」的停滞判据读（detail.action），
// 两边各写一遍字面量就会一改就坏。
type Action string

const (
	// ActionResetProcessing 是「回加工开头重跑」：failed(scrape/organize)、processing
	// 与强制重刮的 finished 都落这里。
	ActionResetProcessing Action = task.RetryActionResetProcessing
	// ActionReadded 是「重新加种成功，版本回下载中」。
	ActionReadded Action = task.RetryActionReadded
	// ActionSkipped 是零动作：已入库（没 force）、qbt 里仍在下载、或租约被别人拿着。
	ActionSkipped Action = task.RetryActionSkipped
	// ActionFailed 是这次重试没做成（加种失败/回读失败/没配 qbt）：状态保持原样，可以再试。
	ActionFailed Action = task.RetryActionFailed
)

// Outcome 是一条版本的重试结局。
type Outcome struct {
	Infohash string
	Action   Action
	// FromState 是重试前那条版本的状态；ToState 是重试后的状态（零动作时两者相同）。
	FromState task.Status
	ToState   task.Status
	// Reason 是给人看的一句话：面板行内显示，也进时间线。
	Reason string
	// ForceRescrape 表示这条版本下一次取数不复用元数据。
	ForceRescrape bool
}

// Result 是一次重试的逐版结果。
//
// 一条任务可以有多条版本（M-Team 降级、中字版本），所以重试不是「成功/失败」一个布尔：
// 一个在下载中被拒、另一条重新加种成功，是正常的一天。
type Result struct {
	MovieID  string
	Versions []Outcome
}

// Settings 是本包对设置域的唯一要求：加种要 qBittorrent 那一节的凭据。
//
// 人工重试是人的动作，不看循环门禁（暂停时点了也只是把状态摆好，循环恢复后自然跑）。
// Load 只为交付材料的重取而存在：补私种备份要问 M-Team 还在不在启用状态（#36）。
type Settings interface {
	Input(ctx context.Context, section string) (settings.TestInput, error)
	Load(ctx context.Context) (settings.Snapshot, error)
}

// Qbt 是 qBittorrent 的接缝：回读现状（判「仍在下载」）与加种（幂等，含收编）。
type Qbt interface {
	Lookup(ctx context.Context, cfg qbittorrent.Config, hash string) (qbittorrent.TorrentStatus, bool, error)
	Ensure(ctx context.Context, cfg qbittorrent.Config, torrent qbittorrent.Torrent) (qbittorrent.Outcome, error)
}

// Options 装配一个人工重试引擎。
type Options struct {
	Store    *task.Store
	Outbox   *sidefx.Outbox
	Settings Settings
	Qbt      Qbt
	// Material 是交付材料仓库（私种备份的读取与重取）。必填：重新加种读的就是它。
	Material *material.Store
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
	// Owner 是 claim 的持有者标识；空则用 hostname-pid。
	Owner string
}

// Runner 是人工重试引擎。
type Runner struct {
	store    *task.Store
	outbox   *sidefx.Outbox
	settings Settings
	qbt      Qbt
	material *material.Store
	logger   *slog.Logger
	owner    string
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
	return &Runner{
		store:    opts.Store,
		outbox:   opts.Outbox,
		settings: opts.Settings,
		qbt:      opts.Qbt,
		material: opts.Material,
		logger:   logger,
		owner:    owner,
	}
}

// Retry 对一条任务的人工重试跑一次。
//
// infohashes 为空就动这条任务的**全部**版本（抽屉里的「强制重刮」要这个语义）；
// 给了就只动这几条——面板的行是**版本组**，按钮上写「重试 2 条版本」就必须只动那 2 条。
// 尤其是组里混着宿主机病因（权限/只读/满盘）的版本时：那种重试毫无意义，
// 混在全任务重试里就会跟着一起被再放一遍（remedy.ts 刚立了不许给假动作的规矩）。
//
// 逐条处理、逐条留结论：某一条被拒（租约被别人拿着、qbt 仍在下载）不影响其它版本。
// 返回的 error 只描述**引擎本身**的故障（数据库不可用），版本侧的结论都在 Result 里。
func (r *Runner) Retry(ctx context.Context, movieID string, infohashes []string, force bool) (Result, error) {
	versions, err := r.store.VersionsOf(ctx, movieID)
	if err != nil {
		return Result{}, err
	}
	if len(infohashes) > 0 {
		want := make(map[string]struct{}, len(infohashes))
		for _, hash := range infohashes {
			want[hash] = struct{}{}
		}
		selected := make([]task.Version, 0, len(versions))
		for _, version := range versions {
			if _, ok := want[version.Infohash]; ok {
				selected = append(selected, version)
			}
		}
		// 点名了却一条也对不上（旧的页面、手改的请求）：回空结果，
		// 面板会说「这条任务还没有版本」，而不是偷偷重试整个任务。
		versions = selected
	}

	result := Result{MovieID: movieID, Versions: make([]Outcome, 0, len(versions))}
	var errs []error
	for _, version := range versions {
		outcome, err := r.retryVersion(ctx, version, force)
		if err != nil {
			errs = append(errs, err)
		}
		result.Versions = append(result.Versions, outcome)
	}
	return result, errors.Join(errs...)
}

// retryVersion 是一条版本的分支判断（规格 §6.1 人工重试行）。
func (r *Runner) retryVersion(ctx context.Context, version task.Version, force bool) (Outcome, error) {
	// finished 是终态：只有 force 才允许把它拉回加工开头（P13-3）。
	if version.Status == task.StatusFinished && !force {
		return Outcome{
			Infohash: version.Infohash, Action: ActionSkipped,
			FromState: version.Status, ToState: version.Status,
			Reason: "已入库：只有「强制重刮」才会让它重跑",
		}, nil
	}

	// 进程内 per-version 锁 + 数据库租约：两条通道同时点也不会一起动手（规格 §4.4）。
	unlock := r.store.Lock(version.Infohash)
	defer unlock()

	claimed, err := r.store.Claim(ctx, version.Infohash, r.owner)
	if errors.Is(err, task.ErrClaimLost) {
		return skipped(version, LeaseHeldReason), nil
	}
	if err != nil {
		return Outcome{}, err
	}

	// 拿到租约后按最新状态分支：两次读之间它可能已经变了。
	switch {
	case claimed.Status == task.StatusDownloading:
		return r.readd(ctx, claimed, force)
	case claimed.Status == task.StatusFailed && claimed.ErrorPhase == task.PhaseDownloading:
		return r.readd(ctx, claimed, force)
	case claimed.Status == task.StatusFinished:
		return r.forceRescrape(ctx, claimed)
	default:
		return r.resetProcessing(ctx, claimed, force)
	}
}

// LeaseHeldReason 是「这次没轮到」的措辞：抢输租约不是失败（规格 §4.4）。
const LeaseHeldReason = "版本正被别的循环处理（租约未过期），稍后再试"

// StillDownloadingReason 是那条硬边界的措辞：qbt 里仍在下载就永不重下。
const StillDownloadingReason = "qBittorrent 里仍在下载：永不重下"

// skipped 是一条「零动作」结局：状态不变，原因给人看。
func skipped(version task.Version, reason string) Outcome {
	return Outcome{
		Infohash: version.Infohash, Action: ActionSkipped,
		FromState: version.Status, ToState: version.Status,
		Reason: reason,
	}
}

// resetProcessing 把版本拨回加工开头（P13-1）。
func (r *Runner) resetProcessing(ctx context.Context, version task.Version, force bool) (Outcome, error) {
	outcome := Outcome{
		Infohash: version.Infohash, Action: ActionResetProcessing,
		FromState: version.Status, ToState: task.StatusProcessing,
		Reason: "人工重试：回到加工开头重跑",
	}
	if force {
		outcome.Reason = "人工重试：回到加工开头，下一次取数不复用元数据"
	}
	return r.applyReset(ctx, version, outcome, force, nil)
}

// forceRescrape 把已入库的版本拉回加工开头，并在覆盖前留下旧的 raw_snapshot（P13-3）。
//
// 「先读快照、再复位、最后记事件」的顺序是有意的：快照必须在取数段覆盖它之前读到，
// 事件随复位一起落库；若复位抢输，就不该有一条宣称「留了痕」的事件。
func (r *Runner) forceRescrape(ctx context.Context, version task.Version) (Outcome, error) {
	before, err := r.store.RawSnapshot(ctx, version.MovieID)
	if err != nil {
		return Outcome{}, errors.Join(err, r.store.Release(ctx, version.Infohash, r.owner))
	}

	outcome := Outcome{
		Infohash: version.Infohash, Action: ActionResetProcessing,
		FromState: version.Status, ToState: task.StatusProcessing,
		Reason:        "强制重刮：回到加工开头，重取前已留下旧 raw_snapshot（对比基准）",
		ForceRescrape: true,
	}
	return r.applyReset(ctx, version, outcome, true,
		map[string]any{"raw_snapshot_before": before})
}

// applyReset 是两条「回加工开头」的共同收尾：复位、记事件、回结局。
func (r *Runner) applyReset(ctx context.Context, version task.Version, outcome Outcome, force bool, extra map[string]any) (Outcome, error) {
	updated, err := r.store.ResetForRetry(ctx, version.Infohash, r.owner, force)
	if errors.Is(err, task.ErrClaimLost) {
		return skipped(version, LeaseHeldReason), nil
	}
	if err != nil {
		return Outcome{}, err
	}
	outcome.ForceRescrape = updated.ForceRescrape

	if err := r.record(ctx, version, outcome, extra); err != nil {
		return outcome, err
	}
	return outcome, nil
}

// readd 按存的 payload 重新加种（P13-2），成功才回 downloading。
func (r *Runner) readd(ctx context.Context, version task.Version, force bool) (Outcome, error) {
	outcome := Outcome{
		Infohash: version.Infohash, Action: ActionFailed,
		FromState: version.Status, ToState: version.Status,
	}

	cfg, reason := r.qbtConfig(ctx)
	if reason != "" {
		outcome.Reason = reason
		return r.finish(ctx, version, outcome)
	}

	// 「仍在下载永不重下」只认 qbt 的权威读数：progress 未满就是还在下。
	// 这一条对 add_pending 的版本同样成立：种子已经进去了，待加种标记归 poll 去清。
	status, found, err := r.qbt.Lookup(ctx, cfg, version.Infohash)
	if err != nil {
		outcome.Reason = fmt.Sprintf("回读 qBittorrent 失败：%v", err)
		return r.finish(ctx, version, outcome)
	}
	if found && status.Progress < 1 {
		outcome.Action = ActionSkipped
		outcome.Reason = StillDownloadingReason
		return r.finish(ctx, version, outcome)
	}

	delivery, err := r.material.Deliver(ctx, version, cfg)
	if err != nil {
		outcome.Reason = fmt.Sprintf("重新加种失败：%v", err)
		// 材料永久没了（站点上已无该种、M-Team 已禁用）：这一次就把版本摆进需人工区，
		// 而不是等下一轮 poll 再花一次 M-Team 配额得出同一个结论（进「需要人工」的判词
		// 与 poll 一致，两处不各写一套）。
		if material.Permanent(err) && version.Status == task.StatusDownloading {
			return r.failAdd(ctx, version, outcome, err.Error())
		}
		return r.finish(ctx, version, outcome)
	}
	if delivery.Material != version.Payload {
		// 重取落到了新路径：payload 是下次读材料的唯一线索，必须写回库。
		if err := r.store.SetPayload(ctx, version.Infohash, r.owner, delivery.Material); err != nil {
			if errors.Is(err, task.ErrClaimLost) {
				return skipped(version, LeaseHeldReason), nil
			}
			return Outcome{}, err
		}
	}

	ensured, err := r.qbt.Ensure(ctx, cfg, delivery.Torrent)
	if err != nil {
		outcome.Reason = fmt.Sprintf("重新加种失败：%v", err)
		return r.finish(ctx, version, outcome)
	}

	// 通知在清 pending 之前入箱：与自动加种同一顺序，崩在中间也不会丢（§5.6）。
	// 已经带着项目 tag 的种子不算「新的下载开始」，不重复喊一次。
	if ensured != qbittorrent.AlreadyTracked {
		r.enqueueDownloadStarted(ctx, version, r.sizeAfterAdd(ctx, cfg, version.Infohash, status))
	}
	outcome.Action = ActionReadded
	outcome.ToState = task.StatusDownloading
	// 不说「版本回下载中」：种子刚交给 qBittorrent，要等下一轮对账确认才算数。
	// 回执与抽屉时间线同一句话，屏幕上不预支下一秒兑现不了的结果。
	outcome.Reason = fmt.Sprintf("重新加种（%s），等下一轮对账确认", ensureLabel(ensured))
	if delivery.Refetched {
		// 时间线上要说清「备份曾经没了、这次是重取回来的」：那是一次真实的 M-Team 调用。
		outcome.Reason = "私种备份已回 M-Team 重取；重新加种，等下一轮对账确认"
	}

	updated, err := r.store.MarkRetryDownloading(ctx, version.Infohash, r.owner, force)
	if errors.Is(err, task.ErrClaimLost) {
		return skipped(version, LeaseHeldReason), nil
	}
	if err != nil {
		return outcome, err
	}
	outcome.ToState = updated.Status
	outcome.ForceRescrape = updated.ForceRescrape

	if err := r.record(ctx, version, outcome, nil); err != nil {
		return outcome, err
	}
	r.logger.Info("人工重试已重新加种",
		"movie_id", version.MovieID, "infohash", version.Infohash, "outcome", outcome.Reason)
	return outcome, nil
}

// failAdd 把一条「交付材料永久没了」的版本摆进需人工（§8.2 的 failed(phase, reason)）。
//
// 只对还在 downloading 的版本动手：已经 failed 的那条状态与原因都在，这次重试只是
// 再说一遍「补不回来」——改写它的 error_reason 没有意义，留一条事件就够。
func (r *Runner) failAdd(ctx context.Context, version task.Version, outcome Outcome, reason string) (Outcome, error) {
	if err := r.store.MarkAddFailed(ctx, version.Infohash, r.owner, reason); err != nil {
		if errors.Is(err, task.ErrClaimLost) {
			return skipped(version, LeaseHeldReason), nil
		}
		return Outcome{}, errors.Join(err, r.store.Release(ctx, version.Infohash, r.owner))
	}
	outcome.Action = ActionFailed
	outcome.ToState = task.StatusFailed
	return r.finish(ctx, version, outcome)
}

// finish 收尾一次没有改动状态的尝试：记一条事件（人要知道点过、为什么没成），放回租约。
func (r *Runner) finish(ctx context.Context, version task.Version, outcome Outcome) (Outcome, error) {
	err := r.record(ctx, version, outcome, nil)
	if releaseErr := r.store.Release(ctx, version.Infohash, r.owner); releaseErr != nil {
		err = errors.Join(err, releaseErr)
	}
	return outcome, err
}

// record 写一条 manual_retry 事件：每次点按钮都留一句，成败都算事实（规格 §4.1）。
//
// detail 里固定带 action（机器可读的结局），其余的键由调用方补（强制重刮补旧 raw_snapshot）。
// 面板的「下载停滞」判据要认「这条版本重新加种过」（task.Attention），所以 action 是结构化的：
// 靠 reason 的措辞去认会一改文案就坏。
func (r *Runner) record(ctx context.Context, version task.Version, outcome Outcome, extra map[string]any) error {
	detail := make(map[string]any, len(extra)+1)
	for key, value := range extra {
		detail[key] = value
	}
	detail["action"] = string(outcome.Action)

	encoded, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("retry: 编码人工重试事件附注 %s: %w", version.Infohash, err)
	}

	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:   version.MovieID,
		Infohash:  version.Infohash,
		Kind:      task.EventManualRetry,
		FromState: string(outcome.FromState),
		ToState:   string(outcome.ToState),
		Reason:    outcome.Reason,
		Detail:    encoded,
	}); err != nil {
		return fmt.Errorf("retry: 记人工重试事件 %s: %w", version.Infohash, err)
	}
	return nil
}

// enqueueDownloadStarted 入箱「下载开始」（收编同发，规格 §5.6）。
//
// 通知只是待办：入箱失败不该让「种子已经加进去了」变成一次重试失败。
// 大小从 qbt 的权威读数补上：人工重试没有磁力候选列表可查（§5.6 payload 契约要 size_mib）。
func (r *Runner) enqueueDownloadStarted(ctx context.Context, version task.Version, status qbittorrent.TorrentStatus) {
	if r.outbox == nil {
		return
	}
	payload := map[string]any{
		"movie_id": version.MovieID,
		"infohash": version.Infohash,
		"number":   version.Number,
		"label":    version.Label,
		"source":   string(version.Source),
	}
	if status.SizeBytes > 0 {
		payload["size_mib"] = float64(status.SizeBytes) / (1 << 20)
	}
	if err := r.outbox.EnqueueNotify(ctx, sidefx.EventDownloadStarted, version.Infohash, payload); err != nil {
		r.logger.Error("retry: 下载开始通知入箱失败", "infohash", version.Infohash, "err", err)
	}
}

// sizeAfterAdd 补一次回读，拿到刚加进去那条种子的大小（§5.6 payload 契约要 size_mib）。
//
// 新加的种子在加种前的 Lookup 里还不存在，拿不到大小；加完之后 qbt 立刻报得出
// total_size。回读失败只是「大小未知」这一格降级，不该让已经成功的加种变成失败。
func (r *Runner) sizeAfterAdd(ctx context.Context, cfg qbittorrent.Config, hash string, before qbittorrent.TorrentStatus) qbittorrent.TorrentStatus {
	if before.SizeBytes > 0 {
		return before
	}
	status, found, err := r.qbt.Lookup(ctx, cfg, hash)
	if err != nil || !found {
		return before
	}
	return status
}

// ensureLabel 把加种的三种结局翻成中文（规格 §8.3：全部 copy zh-CN）。
func ensureLabel(outcome qbittorrent.Outcome) string {
	switch outcome {
	case qbittorrent.Added:
		return "新加种"
	case qbittorrent.Adopted:
		return "收编已有种子"
	case qbittorrent.AlreadyTracked:
		return "种子已在 qBittorrent"
	default:
		return "已交给 qBittorrent"
	}
}

// qbtConfig 读加种要的 qBittorrent 配置；第二个返回值非空表示「读不到，别加」。
func (r *Runner) qbtConfig(ctx context.Context) (qbittorrent.Config, string) {
	if r.qbt == nil {
		return qbittorrent.Config{}, "qBittorrent 集成未装配：无法重新加种"
	}
	if r.settings == nil {
		return qbittorrent.Config{}, "设置域未装配：无法重新加种"
	}

	input, err := r.settings.Input(ctx, "qbittorrent")
	if errors.Is(err, settings.ErrNotConfigured) {
		return qbittorrent.Config{}, "qBittorrent 还没配置：无法重新加种"
	}
	if err != nil {
		return qbittorrent.Config{}, fmt.Sprintf("读 qBittorrent 设置失败：%v", err)
	}
	return qbittorrent.ConfigFrom(input), ""
}
