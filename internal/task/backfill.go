package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

// 补录落库（#43，GLOSSARY「补录」）：把 media_root 下已落盘的旧目录登记成任务。
//
// 一个事务写四样东西：任务行（番号即主键）、每个视频一条版本（processing +
// force_rescrape，等强制重刮重写 NFO）、每条版本的分片账、时间线的登记事件。
// 重复补录以「任务行已存在」为判据整体跳过（幂等），不产生第二份任何东西。

// BackfillVideo 是补录目录里的一个视频文件：一条待登记的版本。
type BackfillVideo struct {
	// RelPath 是视频相对补录目录的位置（ToSlash 归一）。
	RelPath string
	// SizeBytes 是文件字节数（version_file 的永久账）。
	SizeBytes int64
	// PartIndex 从 1 起：单片是 1，cdN 按序号（§7.2 文件名分词）。
	PartIndex int
	// IsPrimary 标记主分片（序号最小那片；单片恒 true）。
	IsPrimary bool
	// Label 是 best-effort 展示标签（中文/破解，§7.2 文件名分词），空串没有。
	Label string
}

// Backfill 是一次补录登记的输入：番号（=目录名=任务主键）与目录里的视频清单。
type Backfill struct {
	Number string
	Videos []BackfillVideo
}

// BackfillInfohash 是补录版本的合成身份：sha256(番号 + rel_path)，64 位小写十六进制。
//
// 同一输入永远同一输出：重复补录、重启、换进程都靠它认出「这条视频已经登记过」。
func BackfillInfohash(number, relPath string) string {
	sum := sha256.Sum256([]byte(number + relPath))
	return hex.EncodeToString(sum[:])
}

// SaveBackfill 在一个事务里登记一次补录。
//
// 返回 inserted=true 表示「这次真的建了任务」；番号已有任务时零写入并返回
// inserted=false（调用方按幂等放行处置，GLOSSARY「补录」的碰撞行）。
func (s *Store) SaveBackfill(ctx context.Context, backfill Backfill) (bool, error) {
	if err := validateBackfill(backfill); err != nil {
		return false, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("task: 开事务: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// 番号即任务主键：补录拿不到（也不需要）JavDB 的 movie_id，目录名是唯一身份。
	// 撞主键与撞番号在这里是同一件事，都走 ON CONFLICT 的幂等出口。
	tag, err := tx.Exec(ctx,
		`INSERT INTO task (movie_id, number, list_id) VALUES ($1, $1, NULL)
		 ON CONFLICT (movie_id) DO NOTHING`,
		backfill.Number,
	)
	if err != nil {
		return false, fmt.Errorf("task: 落补录任务 %s: %w", backfill.Number, err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}

	for _, video := range backfill.Videos {
		infohash := BackfillInfohash(backfill.Number, video.RelPath)
		if _, err := tx.Exec(ctx,
			`INSERT INTO task_version
			     (infohash, movie_id, source, delivery, label, status,
			      force_rescrape, add_pending, payload)
			 VALUES ($1, $2, $3, $4, $5, $6, true, false, '')`,
			infohash, backfill.Number, string(SourceBackfill), string(DeliveryNone),
			video.Label, string(StatusProcessing),
		); err != nil {
			return false, fmt.Errorf("task: 落补录版本 %s: %w", infohash, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO version_file (infohash, rel_path, size_bytes, part_index, is_primary)
			 VALUES ($1, $2, $3, $4, $5)`,
			infohash, video.RelPath, video.SizeBytes, video.PartIndex, video.IsPrimary,
		); err != nil {
			return false, fmt.Errorf("task: 落补录分片 %s: %w", video.RelPath, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO task_event (movie_id, infohash, kind, to_state, phase, reason)
			 VALUES ($1, $2, 'state_change', 'processing', 'organize', $3)`,
			backfill.Number, infohash, backfillReason(len(backfill.Videos)),
		); err != nil {
			return false, fmt.Errorf("task: 记补录事件: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("task: 提交补录: %w", err)
	}
	return true, nil
}

// backfillReason 是补录登记事件的措辞：时间线要说清这条版本不是下载来的。
func backfillReason(videos int) string {
	return fmt.Sprintf("补录登记：%d 个现有视频原地入库，待强制重刮重写 NFO", videos)
}

// validateBackfill 挡住把目录名当路径用的输入：番号进目录名与任务主键，
// 视频相对路径不得逃出目录（version_file 的 rel_path 会被读回来定位文件）。
func validateBackfill(backfill Backfill) error {
	number := strings.TrimSpace(backfill.Number)
	switch {
	case number == "" || number == "." || number == "..":
		return fmt.Errorf("task: 番号 %q 不能当目录名", backfill.Number)
	case strings.ContainsAny(number, `/\`) || strings.ContainsRune(number, 0):
		return fmt.Errorf("task: 番号 %q 含有路径分隔符", number)
	case len(backfill.Videos) == 0:
		return fmt.Errorf("task: 补录 %s 没有视频，无法登记版本", number)
	}
	for _, video := range backfill.Videos {
		rel := strings.TrimSpace(video.RelPath)
		if rel == "" {
			return fmt.Errorf("task: 补录 %s 有视频缺 rel_path", number)
		}
		// Clean 之后再逃出目录（../）或换根（/、C:\）的，不是目录内的相对位置。
		cleaned := filepath.Clean(rel)
		if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return fmt.Errorf("task: 补录 %s 的 rel_path %q 逃出了目录", number, rel)
		}
	}
	return nil
}
