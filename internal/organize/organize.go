package organize

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/processing"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 约定死的文件名（规格 §7.1/§7.2）：movie.nfo 固定名，图片三个固定槽。
const (
	// NFOName 是 NFO 的固定文件名（多片目录里番号命名不会被 Jellyfin 读）。
	NFOName = "movie.nfo"
	// PosterName / FanartName 是两类图片的文件名主干（扩展名随源 URL）。
	PosterName = "poster"
	FanartName = "fanart"
	// ExtraFanartDir 是预览图目录，里面是 fanart{N}.{ext}。
	ExtraFanartDir = "extrafanart"
)

// 两类「部分成功」的告警前缀：它们进时间线（kind=alert），不进状态机。
const (
	// ImageFailurePrefix 是图片落盘部分失败的告警开头（裁剪失败不失败任务）。
	ImageFailurePrefix = "图片落盘部分失败"
)

// maxOrganizesPerRound 挡住异常积压把一轮对账拖成无尽循环。
const maxOrganizesPerRound = 50

// Settings 是本段对设置域的全部要求（与其它循环同一形状）。
type Settings interface {
	Load(ctx context.Context) (settings.Snapshot, error)
	Input(ctx context.Context, section string) (settings.TestInput, error)
}

// Qbt 是回读下载落盘路径的接缝（真实实现是 qbittorrent.Integration）。
//
// 只有 content_path 是整理的事：文件在哪由 qBittorrent 说了算，本项目不猜。
type Qbt interface {
	Lookup(ctx context.Context, cfg qbittorrent.Config, hash string) (qbittorrent.TorrentStatus, bool, error)
}

// TagSwapper 是 tag 换装的接缝（真实实现是 qbittorrent.Integration）。
type TagSwapper interface {
	TransitionTag(ctx context.Context, cfg qbittorrent.Config, hash, from, to string) error
}

// Options 装配一个整理引擎。
type Options struct {
	Store    *task.Store
	Outbox   *sidefx.Outbox
	Settings Settings
	Qbt      Qbt
	Tags     TagSwapper
	// Images 是图片下载接缝；nil 时跳过图片（只落 NFO 与视频，测试/极端降级用）。
	Images ImageFetcher
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
	// Owner 是 claim 的持有者标识；空则用 hostname-pid。
	Owner string
	// Now 可注入测试时钟；空则 time.Now。
	Now func() time.Time
}

// Runner 是整理引擎。
type Runner struct {
	store    *task.Store
	outbox   *sidefx.Outbox
	settings Settings
	qbt      Qbt
	tags     TagSwapper
	images   ImageFetcher
	logger   *slog.Logger
	owner    string
	now      func() time.Time
	// recorder 是加工失败记账的单点（与取数段共用，见 internal/processing）。
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
		store:    opts.Store,
		outbox:   opts.Outbox,
		settings: opts.Settings,
		qbt:      opts.Qbt,
		tags:     opts.Tags,
		images:   opts.Images,
		logger:   logger,
		owner:    owner,
		now:      now,
		recorder: processing.NewRecorder(processing.Options{
			Store:  opts.Store,
			Outbox: opts.Outbox,
			Logger: logger,
			Now:    now,
			Owner:  owner,
		}),
	}
}

// Progress 是一轮整理的可观察结果（日志、测试与 reconcile 的 Result 共用）。
type Progress struct {
	// Gated 表示门禁没开（未初始化/已暂停/没配媒体库目录），这一轮没有做任何事。
	Gated bool
	// Reason 是跳过时给人看的一句话。
	Reason string
	// Scanned 是本轮看见的加工版本数。
	Scanned int
	// Filed 是这一轮入库完成（processing → finished）的版本数。
	Filed int
	// Failed 是这一轮记了一次加工失败的版本数（含退避重试与耗尽）。
	Failed int
	// Skipped 是等取数、抢输租约、状态已变的版本数。
	Skipped int
}

// environment 是一轮整理共用的设置与时钟（读一次，别每条版本各读一遍）。
type environment struct {
	qbt       qbittorrent.Config
	mediaRoot string
	// maxAttempts 是加工失败的自动重试上限（app_config.max_attempts）。
	maxAttempts int
	// translationExpected 表示 AI 翻译是配置且启用的：译文缺席才值得告警。
	translationExpected bool
}

// ProcessPending 推进本轮的整理：元数据就绪的版本落盘、换 tag、置副作用待办。
//
// 它不抢循环级的 advisory lock：作为 reconcile 的一环跑，单飞归 reconcile 那把锁
// （规格 §4.4）；版本级互斥仍归 claim。
func (r *Runner) ProcessPending(ctx context.Context) (Progress, error) {
	env, reason, err := r.environment(ctx)
	if err != nil {
		return Progress{}, err
	}
	if reason != "" {
		return Progress{Gated: true, Reason: reason}, nil
	}

	versions, err := r.store.ProcessingVersions(ctx)
	if err != nil {
		return Progress{}, err
	}

	progress := Progress{}
	var errs []error
	for _, version := range versions {
		if progress.Filed+progress.Failed >= maxOrganizesPerRound {
			break
		}
		progress.Scanned++

		// 元数据还没取齐：归取数段（#27），这里零动作。
		_, exists, err := r.store.Metadata(ctx, version.MovieID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !exists {
			progress.Skipped++
			continue
		}

		outcome, err := r.processVersion(ctx, version, env)
		if err != nil {
			errs = append(errs, err)
		}
		switch outcome {
		case outcomeFiled:
			progress.Filed++
		case outcomeFailed:
			progress.Failed++
		case outcomeSkipped:
			progress.Skipped++
		}
	}

	if progress.Scanned > 0 {
		r.logger.Debug("organize 整理结束",
			"scanned", progress.Scanned, "filed", progress.Filed,
			"failed", progress.Failed, "skipped", progress.Skipped)
	}
	return progress, errors.Join(errs...)
}

// outcome 是一条版本这一轮的结局。
type outcome int

const (
	// outcomeSkipped 是「不该由我处理」：抢输租约、状态已变、元数据在抢租约期间没了。
	outcomeSkipped outcome = iota
	// outcomeFiled 是入库完成（processing → finished）。
	outcomeFiled
	// outcomeFailed 是记了一次加工失败（已按退避/耗尽落库）。
	outcomeFailed
)

// processVersion 处理一条加工版本：抢租约 → 落盘 → 换 tag → 置待办 → 落 finished。
//
// 抢输租约与状态已变都算 skipped 而不是 failed：它们不是这条版本的失败，
// 也不该占掉本轮的处理预算（规格 §4.4：抢输不是失败）。
func (r *Runner) processVersion(ctx context.Context, version task.Version, env environment) (outcome, error) {
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

	// 租约下的第二次读：抢租约期间别人可能刚写完元数据。
	meta, exists, err := r.store.Metadata(ctx, claimed.MovieID)
	if err != nil {
		return outcomeSkipped, errors.Join(err, r.store.Release(ctx, claimed.Infohash, r.owner))
	}
	if !exists {
		return outcomeSkipped, r.store.Release(ctx, claimed.Infohash, r.owner)
	}

	if err := r.file(ctx, claimed, meta, env); err != nil {
		// 记账成功就返回 nil：版本失败已经落在状态机里，不是循环故障。
		return outcomeFailed, r.recordFailure(ctx, claimed, env, err)
	}
	return outcomeFiled, nil
}

// file 是入库一条版本的完整动作。任何一步失败都会让整条版本重来（幂等）：
// 硬链同 inode、NFO 内容同、通知入箱唯一，重跑只会补上没做完的那一半。
func (r *Runner) file(ctx context.Context, version task.Version, meta task.Metadata, env environment) error {
	clean := readMetadata(meta, version.Number)
	if !env.translationExpected {
		// P6-3：翻译禁用时 NFO/通知一律用日文原文。库里已存的译文不删（数据是数据），
		// 只是不参与排版 —— 重新启用翻译后它们照旧可用，不必重译。
		clean.TitleZH, clean.OutlineZH = "", ""
	}

	// 同番号异 movie_id：目录是「一番号一目录」，先到者（或已入库者）拥有它（规格 §7.2 碰撞行）。
	owner, err := r.store.TaskOwningNumber(ctx, version.Number, version.MovieID)
	if err != nil {
		return err
	}
	if owner != "" {
		return permanently(fmt.Errorf("番号 %s 的目录已属于另一个任务（movie_id=%s）：后到者驻留等人工",
			version.Number, owner))
	}

	status, found, err := r.qbt.Lookup(ctx, env.qbt, version.Infohash)
	if err != nil {
		return fmt.Errorf("回读下载落盘路径: %w", err)
	}
	if !found {
		return materialGone{fmt.Errorf("种子已从 qBittorrent 移除，无法定位下载文件（%s）", version.Infohash)}
	}

	videos, subtitles, err := discover(status.ContentPath)
	if err != nil {
		return err
	}
	plan, err := planFiles(version.Number, version.Label, videos, subtitles)
	if err != nil {
		if errors.Is(err, ErrNoVideoFiles) {
			return materialGone{err}
		}
		return err // 计划里的 ErrCollision 是永久错（由 isPermanent() 认出来）
	}

	dir := filepath.Join(env.mediaRoot, version.Number)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("建目录 %s: %w", dir, err)
	}

	// 视频与字幕：只硬链，EXDEV 直接失败（绝不降级 copy）。
	for _, file := range plan.Files {
		if _, err := linkFile(file.Source.Path, filepath.Join(dir, file.TargetName)); err != nil {
			return err
		}
	}

	// NFO：原子写、内容同不重写。
	nfo, err := Render(clean.Movie)
	if err != nil {
		return fmt.Errorf("生成 NFO: %w", err)
	}
	if _, err := writeIfChanged(filepath.Join(dir, NFOName), nfo); err != nil {
		return err
	}

	// 图片：部分成功不失败任务（规格 §5.3 / mdc-lite ADR-0015）。
	warnings := r.writeImages(ctx, dir, clean)

	// 分片账（version_file）：视频才有分片，字幕不算。
	partials := make([]task.VersionFile, 0, len(plan.Videos()))
	for _, file := range plan.Videos() {
		partials = append(partials, task.VersionFile{
			RelPath:   file.Source.RelPath,
			SizeBytes: file.Source.Size,
			PartIndex: file.PartIndex,
			IsPrimary: file.IsPrimary,
		})
	}
	if err := r.store.SaveVersionFiles(ctx, version.Infohash, partials); err != nil {
		return err
	}

	// 入库成功才换 tag：mdc:in-progress → mdc:finished（失败不新增第三个 tag）。
	if err := r.tags.TransitionTag(ctx, env.qbt, version.Infohash, qbittorrent.TagInProgress, qbittorrent.TagFinished); err != nil {
		return fmt.Errorf("换 tag（%s → %s）: %w", qbittorrent.TagInProgress, qbittorrent.TagFinished, err)
	}

	// 「新版本入库」文案的判据要在落 finished 之前取：落完就变成它自己了。
	newVersion, err := r.store.HasFinishedVersion(ctx, version.MovieID, version.Infohash)
	if err != nil {
		return err
	}
	if err := r.enqueueFiled(ctx, version, clean, plan, newVersion); err != nil {
		return err
	}

	if err := r.store.MarkFiled(ctx, version.Infohash, r.owner); err != nil {
		if errors.Is(err, task.ErrClaimLost) {
			// 租约已被别人接手：这次的入库结果不该覆盖当前占用者的账。
			return nil
		}
		return err
	}

	r.record(ctx, version, task.EventStateChange, "processing", "finished",
		fmt.Sprintf("入库完成：%s（%d 个视频文件）", dir, len(plan.Videos())))
	r.recordWarnings(ctx, version, clean, env.translationExpected, warnings)

	r.logger.Info("入库完成",
		"movie_id", version.MovieID, "number", version.Number,
		"infohash", version.Infohash, "dir", dir, "videos", len(plan.Videos()))
	return nil
}

// enqueueFiled 把入库的两个旁路副作用入箱：刷 Jellyfin 与「已入库」通知（规格 §5.5/§5.6）。
//
// 入箱在落 finished 之前：反过来的话入箱失败就再也没有下一轮来补，通知会永久丢掉。
func (r *Runner) enqueueFiled(ctx context.Context, version task.Version, meta cleanMetadata, plan Plan, newVersion bool) error {
	if r.outbox == nil {
		return nil
	}

	// jellyfin_refresh 的 target_key 是 movie_id：同任务多版本天然 coalesce 成一条（P11-1）。
	if err := r.outbox.Enqueue(ctx, sidefx.KindJellyfinRefresh, version.MovieID, map[string]any{
		"movie_id": version.MovieID,
		"infohash": version.Infohash,
		"number":   version.Number,
	}); err != nil {
		return err
	}

	payload := map[string]any{
		"movie_id": version.MovieID,
		"infohash": version.Infohash,
		"number":   version.Number,
		"label":    version.Label,
		"source":   string(version.Source),
		"size_mib": float64(plan.VideoBytes) / (1024 * 1024),
		"title":    meta.title(),
	}
	if newVersion {
		payload["new_version"] = true
	}
	return r.outbox.EnqueueNotify(ctx, sidefx.EventFiled, version.Infohash, payload)
}

// recordFailure 记一次整理失败（退避/耗尽/永久错/通知都归 processing.Recorder）。
//
// 记账成功就返回 nil：版本失败已经落在状态机里，不是循环故障。
func (r *Runner) recordFailure(ctx context.Context, version task.Version, env environment, cause error) error {
	_, err := r.recorder.Fail(ctx, version, phaseFor(cause), cause, isPermanent(cause), env.maxAttempts)
	return err
}

// phaseFor 决定这次失败记在哪个阶段。
//
// 下载材料不见了（种子被移除、content_path 里没有视频）的账要记在下载阶段：规格
// §6.1 就是这么要求的，而且人工重试的语义也不同——failed(下载阶段) 会按存的 payload
// 重新加种，而 failed(整理) 只会重跑整理（那时材料还是没有）。
func phaseFor(cause error) task.Phase {
	var gone materialGone
	if errors.As(cause, &gone) {
		return task.PhaseDownloading
	}
	return task.PhaseOrganize
}

// materialGone 标记「下载材料不见了」：失败阶段归下载（规格 §6.1「种子消失/文件丢失」）。
type materialGone struct{ err error }

func (e materialGone) Error() string { return e.err.Error() }
func (e materialGone) Unwrap() error { return e.err }

// writeImages 落三类图片，返回给人看的告警（部分成功不失败任务）。
//
// 三个不变的判断：
//
//   - 复用：目标图比 provenance 的抓取时刻新 → 这份元数据的图已经落过盘，零下载零改写；
//     三组图各有自己的抓取时刻（sleeve 与预览图可能来自不同源），各判各的；
//   - poster 由横版 sleeve 右裁 2:3；fanart 是同一张**原横版**（源不横就不写 fanart）；
//   - 任何一张失败/裁不了都只记一条告警，接着做下一张。
func (r *Runner) writeImages(ctx context.Context, dir string, meta cleanMetadata) []string {
	if r.images == nil {
		return nil
	}

	warnings := []string{}
	if len(meta.sleeve) > 0 && !imagesCurrent(dir, meta.sleeveFetchTime()) {
		warnings = append(warnings, r.writeSleeveImages(ctx, dir, meta.sleeve)...)
	}
	warnings = append(warnings, r.writeExtraFanart(ctx, dir, meta.extraFanart, meta.extraFetchTime())...)
	return warnings
}

// writeSleeveImages 落 poster 与 fanart：先挑横版 sleeve，再右裁出一张 2:3。
func (r *Runner) writeSleeveImages(ctx context.Context, dir string, sleeves []sourcedImage) []string {
	warnings := []string{}

	var (
		chosen     *sourcedImage
		chosenBody []byte
		first      *sourcedImage
		firstBody  []byte
	)
	for _, sleeve := range sleeves {
		body, err := r.images.Get(ctx, sleeve.URL)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("下载 sleeve 失败：%v", err))
			continue
		}
		candidate := sleeve
		if first == nil {
			first, firstBody = &candidate, body
		}
		if isLandscape(body) {
			chosen, chosenBody = &candidate, body
			break
		}
	}
	if chosen == nil {
		// pl/ps 哪张横版未实测（规格 §13）：都不横时用第一张，几何兜底交给 cropRect。
		chosen, chosenBody = first, firstBody
	}
	if chosen == nil {
		warnings = append(warnings, "sleeve 图片全部下载失败")
		return warnings
	}

	// fanart 是**原横版**：源不横（JavDB 竖版封面兜底）就没有横版可原样落，宁缺勿歪。
	ext := imageExtension(chosen.URL)
	if isLandscape(chosenBody) {
		fanartPath := filepath.Join(dir, FanartName+ext)
		if _, err := writeIfChanged(fanartPath, chosenBody); err != nil {
			warnings = append(warnings, fmt.Sprintf("落 fanart 失败：%v", err))
		}
		removeStaleExtension(dir, FanartName, ext)
	} else {
		removeStaleBase(dir, FanartName)
	}

	poster, err := cropPoster(chosenBody)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("右裁 2:3 失败（poster 跳过）：%v", err))
		return warnings
	}
	posterPath := filepath.Join(dir, PosterName+ext)
	if _, err := writeIfChanged(posterPath, poster); err != nil {
		warnings = append(warnings, fmt.Sprintf("落 poster 失败：%v", err))
	}
	removeStaleExtension(dir, PosterName, ext)
	return warnings
}

// writeExtraFanart 落 extrafanart/fanart{N}.{ext}，并把多出来的旧图清掉（收缩清理）。
//
// 一组预览图整组判复用：目录里最新的那张比这份元数据新，就说明这一组已经落过盘。
// 元数据里一张预览都没有时整组清掉（重刮后预览可能变少）。
func (r *Runner) writeExtraFanart(ctx context.Context, dir string, extras []sourcedImage, fetchedAt time.Time) []string {
	extraDir := filepath.Join(dir, ExtraFanartDir)
	if len(extras) == 0 {
		if _, err := cleanExtraFanart(extraDir, 0); err != nil {
			return []string{fmt.Sprintf("清旧预览图失败：%v", err)}
		}
		return nil
	}
	if extraFanartCurrent(extraDir, fetchedAt) {
		return nil
	}
	if err := os.MkdirAll(extraDir, 0o755); err != nil {
		return []string{fmt.Sprintf("建 %s 失败：%v", ExtraFanartDir, err)}
	}

	warnings := []string{}
	written := 0
	for _, extra := range extras {
		body, err := r.images.Get(ctx, extra.URL)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("下载预览图失败：%v", err))
			continue
		}
		written++
		name := fmt.Sprintf("%s%d%s", FanartName, written, imageExtension(extra.URL))
		if _, err := writeIfChanged(filepath.Join(extraDir, name), body); err != nil {
			warnings = append(warnings, fmt.Sprintf("落 %s 失败：%v", name, err))
		}
	}

	// 收缩清理：预览图比上次少了，旧图不能留在盘上。
	if _, err := cleanExtraFanart(extraDir, written); err != nil {
		warnings = append(warnings, fmt.Sprintf("清旧预览图失败：%v", err))
	}
	return warnings
}

// recordWarnings 把两类「部分成功」写进时间线：同一类只写一次（HasAlert），
// 免得一条版本重跑几轮就把面板刷满。
func (r *Runner) recordWarnings(ctx context.Context, version task.Version, meta cleanMetadata, translationExpected bool, imageWarnings []string) {
	if len(imageWarnings) > 0 {
		r.recordOnce(ctx, version, ImageFailurePrefix,
			fmt.Sprintf("%s：%s（任务照常入库）", ImageFailurePrefix, strings.Join(imageWarnings, "；")))
	}
	// AI 翻译（#30）：译文本该有却没有时才告警，缺席不阻塞入库。
	// 取数段已经记过失败原因（TranslationFailurePrefix）的不再重复——两段共用 task 的词表。
	if translationExpected && !meta.translated() && !r.hasAlert(ctx, version, task.TranslationFailurePrefix) {
		r.recordOnce(ctx, version, task.TranslationMissingPrefix,
			fmt.Sprintf("%s：title/plot 用日文原文入库", task.TranslationMissingPrefix))
	}
}

// recordOnce 只在还没有同类告警时写一条（判据在 task_event）。
func (r *Runner) recordOnce(ctx context.Context, version task.Version, prefix, reason string) {
	if r.hasAlert(ctx, version, prefix) {
		return
	}
	r.record(ctx, version, task.EventAlert, "", "", reason)
}

// hasAlert 报告这条版本的时间线上有没有某一类告警（查不到一律当「没有」：
// 宁可多一条告警，也不要因为一次读失败把告警整个丢掉）。
func (r *Runner) hasAlert(ctx context.Context, version task.Version, prefix string) bool {
	alerted, err := r.store.HasAlert(ctx, version.Infohash, prefix)
	if err != nil {
		r.logger.Error("organize: 查告警去重失败", "infohash", version.Infohash, "err", err)
		return false
	}
	return alerted
}

// record 往任务时间线追加一条事件（写不进去不该让一次已经成功的入库变成失败）。
func (r *Runner) record(ctx context.Context, version task.Version, kind task.EventKind, from, to, reason string) {
	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:   version.MovieID,
		Infohash:  version.Infohash,
		Kind:      kind,
		FromState: from,
		ToState:   to,
		Phase:     string(task.PhaseOrganize),
		Reason:    reason,
	}); err != nil {
		r.logger.Error("organize: 记事件失败", "infohash", version.Infohash, "err", err)
	}
}

// environment 读一次本轮的设置：门禁、qbt 凭据、媒体库根目录、重试上限与翻译开关。
func (r *Runner) environment(ctx context.Context) (environment, string, error) {
	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return environment{}, "", fmt.Errorf("organize: 读设置: %w", err)
	}
	if reason := snapshot.GateReason(); reason != "" {
		return environment{}, reason, nil
	}

	qbtInput, err := r.settings.Input(ctx, "qbittorrent")
	if errors.Is(err, settings.ErrNotConfigured) {
		return environment{}, "qBittorrent 还没配置", nil
	}
	if err != nil {
		return environment{}, "", fmt.Errorf("organize: 读 qBittorrent 设置: %w", err)
	}

	jellyfinInput, err := r.settings.Input(ctx, "jellyfin")
	if errors.Is(err, settings.ErrNotConfigured) {
		return environment{}, "Jellyfin 还没配置", nil
	}
	if err != nil {
		return environment{}, "", fmt.Errorf("organize: 读 Jellyfin 设置: %w", err)
	}

	mediaRoot := strings.TrimSpace(jellyfinInput.Text("media_root"))
	if mediaRoot == "" {
		return environment{}, "媒体库目录（media_root）还没配置", nil
	}

	env := environment{
		qbt:         qbittorrent.ConfigFrom(qbtInput),
		mediaRoot:   mediaRoot,
		maxAttempts: snapshot.MaxAttempts(processing.DefaultMaxAttempts),
	}
	// AI 翻译是可选集成：已配置且启用才算「译文本该有」。
	env.translationExpected = snapshot.OptionalEnabled("ai_translation")
	return env, "", nil
}

// permanentError 标记「再试也一样」的失败：碰撞、跨设备、目录归属。
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// permanently 包一层永久错（再试也一样，不烧自动重试预算）。
func permanently(err error) error { return permanentError{err: err} }

// isPermanent 报告一次失败是不是永久错。
//
// 两个来源：显式包过永久错（目录归属），以及落盘那一层的两个哨兵——碰撞
// （目标被别人占着，重试不会变）与跨设备（换挂载才是解法，规格 §7.2）。
func isPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p) || errors.Is(err, ErrCollision) || errors.Is(err, ErrCrossDevice)
}

// imagesCurrent 报告目标目录里的 poster/fanart 是不是已经按这份元数据落过盘。
//
// 判据是 mtime：图片是下载后新写的文件，写它的时候这份元数据已经取回来了；
// 所以「图比元数据新」＝这张图就是按它落的。mtime 缺失/读不到一律当「不新」。
func imagesCurrent(dir string, fetchedAt time.Time) bool {
	if fetchedAt.IsZero() {
		return false
	}
	for _, name := range []string{PosterName, FanartName} {
		matches, err := filepath.Glob(filepath.Join(dir, name+".*"))
		if err != nil || len(matches) == 0 {
			return false
		}
		info, err := os.Stat(matches[0])
		if err != nil || info.ModTime().Before(fetchedAt) {
			return false
		}
	}
	return true
}

// extraFanartCurrent 报告 extrafanart 目录是不是已经按这份元数据落过盘。
//
// 判据与 poster/fanart 同一套（mtime）：目录里最新的那张比抓取时刻新，
// 就说明这一组预览图是按这份元数据落的，不必再下一遍（复用＝零下载，规格 §7.2）。
func extraFanartCurrent(dir string, fetchedAt time.Time) bool {
	if fetchedAt.IsZero() {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	newest := time.Time{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), FanartName) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return false
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return !newest.Before(fetchedAt)
}

// removeStaleExtension 删掉同一个图片槽位落过的别的扩展名（poster.jpg → poster.png）。
//
// 扩展名随源 URL：源换了格式时旧文件会变成一颗没人管的僵尸图，Jellyfin 可能读它。
func removeStaleExtension(dir, base, keepExt string) {
	matches, err := filepath.Glob(filepath.Join(dir, base+".*"))
	if err != nil {
		return
	}
	for _, match := range matches {
		if strings.EqualFold(filepath.Ext(match), keepExt) {
			continue
		}
		_ = os.Remove(match)
	}
}

// removeStaleBase 删掉一个图片槽位的全部文件（清理用：这个槽位已经没有合法的内容）。
func removeStaleBase(dir, base string) {
	matches, err := filepath.Glob(filepath.Join(dir, base+".*"))
	if err != nil {
		return
	}
	for _, match := range matches {
		_ = os.Remove(match)
	}
}
