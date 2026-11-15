// Package manual 拥有「手工登记」（GLOSSARY）：不经清单轮询，把一部作品直接交给流水线。
//
// 用户在面板上贴两样东西：磁力串与 JavDB 网页链接。登记做的事只有四步——
//
//   - 解析：磁力里取 infohash，链接里取 movie_id（`/v/{id}` 形状，也认裸 id）；
//   - 校验：调 JavDB 详情确认链接真实存在并取番号（贴错链接当场报错），
//     番号已被别的任务占用也当场拒绝（一番号一目录，ADR-0008，晚拦注定驻留）；
//   - 落库：先落库再加种（ADR-0004），source=manual、交付材料就是贴来的磁力串；
//   - 加种：与 poll 同一条 Ensure（新加/收编/已在都算成功），清 add_pending、
//     入箱「下载开始」。
//
// 此后下载→刮削→入库→tag 换装全是现成机器，与清单任务无差别。加种一步失败时
// 版本留在 add_pending，poll 的补加种通道按存的磁力重试——登记本身已经成功。
package manual

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 登记的结局。前三种是「这次登记把种子交给了 qbt」；deferred 是「登记成功、
// 加种这一步没做完，poll 会接着试」；duplicate 是「之前已经登记过，零写入」。
const (
	OutcomeAdded          = "added"
	OutcomeAdopted        = "adopted"
	OutcomeAlreadyTracked = "already_tracked"
	OutcomeDeferred       = "deferred"
	OutcomeDuplicate      = "duplicate"
)

// 四个哨兵错误：HTTP 面按它们分状态码，面板按 message 直译。
var (
	// ErrBadMagnet / ErrBadLink 是输入形状不对（400）。
	ErrBadMagnet = errors.New("磁力链接读不出 infohash")
	ErrBadLink   = errors.New("JavDB 链接不是 /v/{id} 形状")
	// ErrNumberTaken 是番号已被别的任务占用（409）：晚拦注定在整理阶段驻留。
	ErrNumberTaken = errors.New("番号已属于另一任务")
	// ErrInfohashTaken 是这条种子已登记在别的作品名下（409）。
	ErrInfohashTaken = errors.New("这条种子已登记在另一任务")
	// ErrGated 是门禁没开（409）：未初始化/已暂停时登记不该开始。
	ErrGated = errors.New("门禁没开")
)

// Request 是一次手工登记的输入：两串粘贴。
type Request struct {
	Magnet   string
	JavdbURL string
}

// Result 是一次登记的回执。
type Result struct {
	MovieID  string
	Infohash string
	Number   string
	Outcome  string
	// Reason 是 deferred / duplicate 时给人看的一句话。
	Reason string
}

// Source 是 JavDB 详情的接缝（真实实现是 javdb.Integration.Detail）。
type Source interface {
	Detail(ctx context.Context, token, movieID string) (appapi.DetailResult, error)
}

// Adder 是加种接缝（真实实现是 qbittorrent.Integration.Ensure）。
type Adder interface {
	Ensure(ctx context.Context, cfg qbittorrent.Config, torrent qbittorrent.Torrent) (qbittorrent.Outcome, error)
}

// Looker 是加种后的回读接缝：通知要的 size_mib 只能从 qbt 的权威读数来。
type Looker interface {
	Lookup(ctx context.Context, cfg qbittorrent.Config, hash string) (qbittorrent.TorrentStatus, bool, error)
}

// Settings 是设置域接缝：门禁（Load）与凭据（Input）。
type Settings interface {
	Load(ctx context.Context) (settings.Snapshot, error)
	Input(ctx context.Context, section string) (settings.TestInput, error)
}

// Options 装配一个登记器。
type Options struct {
	Store    *task.Store
	Outbox   *sidefx.Outbox
	Settings Settings
	Source   Source
	Adder    Adder
	Looker   Looker
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
	// Owner 是 claim 的持有者标识；空则用 hostname-pid。
	Owner string
}

// Registrar 是手工登记器：一个方法，同步做完一次登记。
type Registrar struct {
	store    *task.Store
	outbox   *sidefx.Outbox
	settings Settings
	source   Source
	adder    Adder
	looker   Looker
	logger   *slog.Logger
	owner    string
}

// New 构造登记器。
func New(opts Options) *Registrar {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	owner := strings.TrimSpace(opts.Owner)
	if owner == "" {
		owner = task.DefaultOwner()
	}
	return &Registrar{
		store:    opts.Store,
		outbox:   opts.Outbox,
		settings: opts.Settings,
		source:   opts.Source,
		adder:    opts.Adder,
		looker:   opts.Looker,
		logger:   logger,
		owner:    owner,
	}
}

// Register 登记一部作品：解析 → 校验 → 落库 → 加种。
//
// 返回的 error 只装「这次登记没成」的事实（输入形状、撞车、门禁、上游故障）；
// 加种一步的失败不算登记失败——版本行已在库里、材料已存好，poll 会接着试，
// 这种结局走 OutcomeDeferred 而不是 error。
func (r *Registrar) Register(ctx context.Context, request Request) (Result, error) {
	magnet := strings.TrimSpace(request.Magnet)
	infohash, err := qbittorrent.HashFromMagnet(magnet)
	if err != nil {
		return Result{}, fmt.Errorf("%w：%v", ErrBadMagnet, err)
	}
	movieID, err := MovieIDFromLink(request.JavdbURL)
	if err != nil {
		return Result{}, err
	}

	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("manual: 读设置: %w", err)
	}
	if reason := snapshot.GateReason(); reason != "" {
		return Result{}, fmt.Errorf("%w：%s", ErrGated, reason)
	}
	javdbInput, err := r.settings.Input(ctx, "javdb")
	if err != nil {
		return Result{}, fmt.Errorf("manual: 读 JavDB 设置: %w", err)
	}
	qbtInput, err := r.settings.Input(ctx, "qbittorrent")
	if err != nil {
		return Result{}, fmt.Errorf("manual: 读 qBittorrent 设置: %w", err)
	}
	cfg := qbittorrent.ConfigFrom(qbtInput)

	// 链接真实性只认详情读数：读不到就是不存在的作品，不该进流水线。
	detail, err := r.source.Detail(ctx, javdbInput.Text("token"), movieID)
	if err != nil {
		return Result{}, fmt.Errorf("读 JavDB 详情 %s: %w", movieID, err)
	}
	number := strings.TrimSpace(detail.Movie.Number)
	if number == "" {
		return Result{}, fmt.Errorf("JavDB 详情里没有番号，没法登记 %s", movieID)
	}

	// 番号撞车预检：同番号别的 movie_id 已有任务 → 当场拒绝（后到者注定驻留）。
	if owner, taken, err := r.store.TaskIDByNumber(ctx, number, movieID); err != nil {
		return Result{}, err
	} else if taken {
		return Result{}, fmt.Errorf("%w：番号 %s 已属于 %s", ErrNumberTaken, number, owner)
	}

	inserted, err := r.store.SaveCandidate(ctx, task.Candidate{
		MovieID:  movieID,
		Number:   number,
		Infohash: infohash,
		Source:   task.SourceManual,
		Delivery: task.DeliveryMagnet,
		Payload:  magnet,
	})
	if err != nil {
		return Result{}, err
	}
	if !inserted {
		return r.duplicate(ctx, infohash, movieID, number)
	}

	r.logger.Info("手工登记", "movie_id", movieID, "number", number, "infohash", infohash)
	return r.add(ctx, cfg, infohash, movieID, number, magnet)
}

// duplicate 处置「同 infohash 已有版本行」：同任务 → 幂等放行；别的任务 → 拒绝。
//
// 幂等放行时不再加种、不再入箱：那条版本可能早已在跑，重复动作只会制造
// 第二条「下载开始」待办之后的 no-op 轮次。还在 add_pending 的由 poll 补完。
func (r *Registrar) duplicate(ctx context.Context, infohash, movieID, number string) (Result, error) {
	version, err := r.store.Version(ctx, infohash)
	if err != nil {
		return Result{}, err
	}
	if version.MovieID != movieID {
		return Result{}, fmt.Errorf("%w：infohash %s 已属于 %s", ErrInfohashTaken, infohash, version.MovieID)
	}
	reason := "这条种子已经登记过了"
	if version.AddPending {
		reason += "，加种将由清单轮询补上"
	}
	return Result{MovieID: movieID, Infohash: infohash, Number: number, Outcome: OutcomeDuplicate, Reason: reason}, nil
}

// add 把刚落库的版本交给 qbt：claim → Ensure → 入箱 → 清 pending。
//
// 与 poll 的 addOne 同一条顺序（先入箱后清 pending：入箱靠部分唯一索引幂等，
// 崩在两步之间下一轮重试是 no-op，通知不会丢；反过来就会永久丢）。
// 任一步不顺都返回 deferred——版本行已在库里，poll 的补加种通道拿着存的磁力会接着试。
func (r *Registrar) add(ctx context.Context, cfg qbittorrent.Config, infohash, movieID, number, magnet string) (Result, error) {
	result := Result{MovieID: movieID, Infohash: infohash, Number: number, Outcome: OutcomeDeferred}

	unlock := r.store.Lock(infohash)
	defer unlock()

	version, err := r.store.Claim(ctx, infohash, r.owner)
	if errors.Is(err, task.ErrClaimLost) {
		result.Reason = "这条版本正在被别的通道处理（轮询或对账），加种由它完成"
		return result, nil
	}
	if err != nil {
		return result, err
	}

	// 材料就是刚存进 payload 的磁力串；FromPayload 与 poll/重试是同一处实现。
	torrent, err := qbittorrent.FromPayload(cfg, infohash, number, string(task.DeliveryMagnet), magnet)
	if err != nil {
		r.abandon(ctx, version, "备交付材料失败："+err.Error())
		return result, nil
	}

	outcome, err := r.adder.Ensure(ctx, cfg, torrent)
	if err != nil {
		r.abandon(ctx, version, fmt.Sprintf("加种失败：%v", err))
		result.Reason = "已登记，加种暂未完成（qBittorrent 侧失败），清单轮询会重试"
		return result, nil
	}

	payload := map[string]any{
		"movie_id": movieID,
		"infohash": infohash,
		"number":   number,
		"label":    "",
		"source":   string(task.SourceManual),
	}
	if status, found, lookupErr := r.looker.Lookup(ctx, cfg, infohash); lookupErr == nil && found && status.SizeBytes > 0 {
		payload["size_mib"] = float64(status.SizeBytes) / (1 << 20)
	}
	if err := r.outbox.EnqueueNotify(ctx, sidefx.EventDownloadStarted, infohash, payload); err != nil {
		r.abandon(ctx, version, fmt.Sprintf("下载开始通知入箱失败：%v", err))
		result.Reason = "已登记并加种，通知待办稍后由轮询补上"
		return result, nil
	}

	if err := r.store.MarkAdded(ctx, infohash, r.owner, false); err != nil {
		if errors.Is(err, task.ErrClaimLost) {
			result.Reason = "已登记并加种，收尾（清加种标记）由占用者完成"
			return result, nil
		}
		return result, err
	}

	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:  movieID,
		Infohash: infohash,
		Kind:     task.EventSideEffect,
		Reason:   fmt.Sprintf("已加种（%s），下载开始通知入箱", outcome),
	}); err != nil {
		// 事件只是时间线：写不进去不该让一次已经成功的登记变成失败。
		r.logger.Error("manual: 记加种事件失败", "infohash", infohash, "err", err)
	}

	result.Outcome = string(outcome)
	return result, nil
}

// abandon 放弃这一次收尾：放回租约、留一条告警，add_pending 保持 true 等 poll 重试。
func (r *Registrar) abandon(ctx context.Context, version task.Version, reason string) {
	if err := r.store.Release(ctx, version.Infohash, r.owner); err != nil {
		r.logger.Error("manual: 释放失败版本的租约失败", "infohash", version.Infohash, "err", err)
	}
	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:  version.MovieID,
		Infohash: version.Infohash,
		Kind:     task.EventAlert,
		Phase:    string(task.PhaseDownloading),
		Reason:   reason,
	}); err != nil {
		r.logger.Error("manual: 记加种告警失败", "infohash", version.Infohash, "err", err)
	}
}

// MovieIDFromLink 从用户贴的 JavDB 链接里取 movie_id。
//
// 认两种形状：`https://javdb.com/v/9629eV?...`（任意主机都行，镜像域名一堆）与
// 裸 id（`9629eV`）。其余一律 ErrBadLink——宁可让用户改一串输入，也不猜。
func MovieIDFromLink(raw string) (string, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return "", fmt.Errorf("%w：链接是空的", ErrBadLink)
	}
	if !strings.Contains(input, "/") {
		return input, nil
	}
	parsed, err := url.Parse(input)
	if err != nil {
		return "", fmt.Errorf("%w：%v", ErrBadLink, err)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i, segment := range segments {
		if segment == "v" && i+1 < len(segments) && strings.TrimSpace(segments[i+1]) != "" {
			return segments[i+1], nil
		}
	}
	return "", fmt.Errorf("%w：%s", ErrBadLink, input)
}
