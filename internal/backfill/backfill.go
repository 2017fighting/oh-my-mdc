// Package backfill 拥有「补录」（GLOSSARY，#43）：把媒体库里已落盘、不归本项目的
// 旧目录登记成任务。两个动作，都由面板驱动：
//
//   - **Scan**：扫 media_root 下的 `{番号}/` 目录 ∧ 任务表无此番号 →「未补录目录」列表。
//     列表与任务表实时一致——补录成功的那一刻，目录就从列表里消失。
//   - **Backfill**（一键补录）：删目录里 mdcng 时代的 `{番号}*.nfo`（movie.nfo 除外），
//     然后一个事务把任务与版本落库：目录里每个现有视频记为一条已就位的版本
//     （source=backfill、delivery=none、processing、force_rescrape、无 add_pending）。
//
// 「立即强制重刮」不是这里另起的动作：登记出来的版本天生 processing + force_rescrape，
// 下一轮对账循环的取数段会带着「不复用」标记去取数，整理段重写 NFO 与图片后照常
// 入库——Jellyfin 刷新与「入库完成」通知由入库路径照发，「下载开始」不发（无下载）。
// 补录完仍缺的关键面（演员等）由整理段按四面判据标「待补齐」（#42 的机器接着管）。
//
// 碰撞沿用现有规则：番号已有任务（轮询/手工来的，另一个 movie_id）→ 幂等放行
// （GLOSSARY「补录」：不抢别人的目录）；目录里没有视频 → 拒绝（没有可登记的版本，
// 硬造一个空任务只会喂面板一个永不变化的状态 NULL 行）。
//
// 幂等：重复补录以「任务表已有此番号」为判据整体跳过——零写入、零删文件。
package backfill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/media"
	"github.com/2017fighting/oh-my-mdc/internal/organize"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 补录的两种结局：backfilled 是这次真的登记了；duplicate 是番号已有任务，
// 幂等放行、零写入（与手工登记的 duplicate 同一形状）。
const (
	OutcomeBackfilled = "backfilled"
	OutcomeDuplicate  = "duplicate"
)

// 四个哨兵错误：HTTP 面按它们分状态码。
var (
	// ErrBadNumber 是输入形状不对（400）：空串或带路径分隔符。
	ErrBadNumber = errors.New("目录名不是合法番号")
	// ErrGated 是门禁没开（409）：未初始化/已暂停时补录不该开始。
	ErrGated = errors.New("门禁没开")
	// ErrDirMissing 是目录已不在盘上（409）：列表是快照，点下去之前世界会变。
	ErrDirMissing = errors.New("目录已不在媒体库")
	// ErrNoVideos 是目录里没有视频（400）：没有可登记的版本。
	ErrNoVideos = errors.New("目录里没有视频文件")
)

// Settings 是设置域接缝：门禁（Load）与媒体库根目录（Input("jellyfin")）。
type Settings interface {
	Load(ctx context.Context) (settings.Snapshot, error)
	Input(ctx context.Context, section string) (settings.TestInput, error)
}

// Options 装配补录器。
type Options struct {
	Store    *task.Store
	Settings Settings
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
}

// Runner 是补录器：扫描与一键补录。
type Runner struct {
	store    *task.Store
	settings Settings
	logger   *slog.Logger
}

// New 构造补录器。
func New(opts Options) *Runner {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{store: opts.Store, settings: opts.Settings, logger: logger}
}

// Candidate 是「未补录目录」的一行：番号（=目录名）与目录里的视频概要。
type Candidate struct {
	Number string
	Videos int
	Bytes  int64
}

// Scan 扫出「未补录目录」：media_root 下的一级目录 ∧ 任务表无此番号。
//
// 判据是**实时**的（每次面板读取都现扫现比），不是快照：扫描不落任何行。
// 隐藏目录（. 开头）不是作品目录，跳过；目录按番号排序，列表稳定。
func (r *Runner) Scan(ctx context.Context) ([]Candidate, error) {
	mediaRoot, err := r.mediaRoot(ctx)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(mediaRoot)
	if err != nil {
		return nil, fmt.Errorf("backfill: 读媒体库 %s: %w", mediaRoot, err)
	}

	claimed, err := r.store.TaskNumbers(ctx)
	if err != nil {
		return nil, err
	}

	dirs := []Candidate{}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") || claimed[name] {
			continue
		}
		videos, bytes, err := dirVideos(filepath.Join(mediaRoot, name))
		if err != nil {
			return nil, err
		}
		// 没有视频的目录不是可登记的作品目录：列表里也不放（免得点进去才挨拒绝）。
		if len(videos) == 0 {
			continue
		}
		dirs = append(dirs, Candidate{Number: name, Videos: len(videos), Bytes: bytes})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Number < dirs[j].Number })
	return dirs, nil
}

// Result 是一次补录的回执。
type Result struct {
	Number  string
	MovieID string
	Outcome string
	// Versions 是这次登记（或已登记）的版本数。
	Versions int
	// Reason 是 duplicate 时给人看的一句话。
	Reason string
}

// Backfill 对一个目录执行一键补录：删旧 NFO → 登记任务与版本（一个事务）。
//
// 幂等的关键顺序：先查重（番号已有任务 → 直接回 duplicate，零写入零删除），
// 再删旧 NFO（本身可重跑），最后落库。崩在删与落之间，目录仍在列表里，
// 再点一次就补上——每一步都向同一终态收敛。
func (r *Runner) Backfill(ctx context.Context, number string) (Result, error) {
	number = strings.TrimSpace(number)
	if number == "" || number == "." || number == ".." || strings.ContainsAny(number, `/\`) {
		return Result{}, fmt.Errorf("%w：%q", ErrBadNumber, number)
	}

	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("backfill: 读设置: %w", err)
	}
	if reason := snapshot.GateReason(); reason != "" {
		return Result{}, fmt.Errorf("%w：%s", ErrGated, reason)
	}

	// 碰撞预检沿用手工登记的规则（GLOSSARY「补录」）：番号已有任务（轮询/手工来的，
	// 或上次补录自己的）→ 幂等放行，不删任何文件、不写任何行。
	// 两目录同番号在「目录名即番号」的扫描口径下不存在。
	if _, taken, err := r.store.TaskIDByNumber(ctx, number, ""); err != nil {
		return Result{}, err
	} else if taken {
		return Result{
			Number: number, MovieID: number, Outcome: OutcomeDuplicate,
			Reason: "这个番号已经有任务了",
		}, nil
	}

	mediaRoot, err := r.mediaRoot(ctx)
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Join(mediaRoot, number)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Result{}, fmt.Errorf("%w：%s", ErrDirMissing, dir)
	}

	videos, _, err := dirVideos(dir)
	if err != nil {
		return Result{}, err
	}
	if len(videos) == 0 {
		return Result{}, fmt.Errorf("%w：%s", ErrNoVideos, dir)
	}

	if err := removeLegacyNfos(dir, number); err != nil {
		return Result{}, err
	}

	inserted, err := r.store.SaveBackfill(ctx, task.Backfill{Number: number, Videos: videos})
	if err != nil {
		return Result{}, err
	}
	if !inserted {
		// 预检与落库之间有人建了同番号任务：同一句幂等出口，回执说清零写入。
		return Result{
			Number: number, MovieID: number, Outcome: OutcomeDuplicate,
			Reason: "这个番号已经有任务了",
		}, nil
	}

	r.logger.Info("补录完成",
		"number", number, "dir", dir, "videos", len(videos))
	return Result{
		Number:   number,
		MovieID:  number,
		Outcome:  OutcomeBackfilled,
		Versions: len(videos),
	}, nil
}

// mediaRoot 读媒体库根目录；门禁没开或目录没配都不往下走。
func (r *Runner) mediaRoot(ctx context.Context) (string, error) {
	snapshot, err := r.settings.Load(ctx)
	if err != nil {
		return "", fmt.Errorf("backfill: 读设置: %w", err)
	}
	if reason := snapshot.GateReason(); reason != "" {
		return "", fmt.Errorf("%w：%s", ErrGated, reason)
	}
	input, err := r.settings.Input(ctx, "jellyfin")
	if errors.Is(err, settings.ErrNotConfigured) {
		return "", fmt.Errorf("%w：Jellyfin 那一节还没配置", ErrGated)
	}
	if err != nil {
		return "", fmt.Errorf("backfill: 读 Jellyfin 设置: %w", err)
	}
	root := strings.TrimSpace(input.Text("media_root"))
	if root == "" {
		return "", fmt.Errorf("%w：媒体库目录（media_root）还没配置", ErrGated)
	}
	return root, nil
}

// backfillVideo 是 dirVideos 的一行结果：task.BackfillVideo 的组装中间态。
type backfillVideo = task.BackfillVideo

// dirVideos 扫一个目录里的视频文件（递归，自然顺序），并给出每个文件的
// §7.2 文件名分词（label 与分片序号）。rel_path 相对目录本身。
//
// 主分片是序号最小的那片；同序号的多片按出现顺序取第一片打标记。
func dirVideos(dir string) ([]backfillVideo, int64, error) {
	type found struct {
		rel  string
		size int64
	}
	var founds []found
	total := int64(0)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("backfill: 遍历 %s: %w", path, walkErr)
		}
		if entry.IsDir() {
			return nil
		}
		if !media.IsVideo(entry.Name()) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("backfill: 读 %s: %w", path, err)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("backfill: 相对路径 %s: %w", path, err)
		}
		founds = append(founds, found{rel: filepath.ToSlash(rel), size: info.Size()})
		total += info.Size()
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if len(founds) == 0 {
		return nil, 0, nil
	}

	number := filepath.Base(dir)
	videos := make([]backfillVideo, 0, len(founds))
	primary := -1
	primaryPart := 0
	for _, f := range founds {
		stem := strings.TrimSuffix(filepath.Base(f.rel), filepath.Ext(f.rel))
		label, part := organize.ParseFileNameTokens(number, stem)
		if primary == -1 || part < primaryPart {
			primary, primaryPart = len(videos), part
		}
		videos = append(videos, backfillVideo{
			RelPath:   f.rel,
			SizeBytes: f.size,
			PartIndex: part,
			Label:     label,
		})
	}
	if primary >= 0 {
		videos[primary].IsPrimary = true
	}
	return videos, total, nil
}

// removeLegacyNfos 删目录里 mdcng 时代的 `{番号}*.nfo`（movie.nfo 除外）。
//
// 依据（#43 背景与用户裁决）：Jellyfin ≥10.9 读 movie.nfo 优先（jellyfin/jellyfin
// #10339），旧件是死重，直接删、不归档。movie.nfo 与词表外的 NFO（不是以番号
// 开头的）都不碰——后者不归本项目认领。文件已不在（重跑）不算错误。
func removeLegacyNfos(dir, number string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("backfill: 读 %s: %w", dir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(name), ".nfo") {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if strings.EqualFold(stem, organize.NFOName) || !strings.HasPrefix(strings.ToLower(stem), strings.ToLower(number)) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("backfill: 删旧 NFO %s: %w", name, err)
		}
	}
	return nil
}
