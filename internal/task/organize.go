package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// 加工段（取数 + 整理）的落库动作：失败记账、终态、以及整理要读的两个判据。
//
// 与 download.go 同一形状：都要求调用方先持有租约（`WHERE claimed_by = $owner`），
// 抢输返回 ErrClaimLost（规格 §4.4：别当失败）。

// ProcessingFailure 是记一次加工失败的结果。
//
// Exhausted=true 表示这一次用完自动重试预算、版本已落 failed(phase, reason)；
// 调用方据此发「加工失败」通知（规格 §5.6 四事件之一）并让版本驻留等人工。
type ProcessingFailure struct {
	// Attempts 是累计失败次数（含这一次）。
	Attempts int
	// Exhausted 表示版本已落 failed。
	Exhausted bool
}

// RecordProcessingFailure 记一次加工失败，并在错误/本地判据上做归一。
//
// 三条语义固定在这一句 SQL 里（规格 §6.1 失败行）：
//
//   - 未耗尽：attempts+1、next_attempt_at 推到 at+delay，状态仍是 processing；
//   - 耗尽（attempts 达到 maxAttempts）：落 failed(phase, reason)、清掉退避时间，
//     版本从此不再自动被捞起（需人工）；
//   - 无论哪一种都放回租约：失败不是占用，下轮/人工立刻可以接手。
//
// 状态不是 processing 或租约不在自己手里 → 零写入 + ErrClaimLost。
func (s *Store) RecordProcessingFailure(ctx context.Context, infohash, owner string, phase Phase, reason string, at time.Time, delay time.Duration, maxAttempts int) (ProcessingFailure, error) {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var (
		attempts int
		status   Status
	)
	err := s.pool.QueryRow(ctx,
		`UPDATE task_version
		 SET attempts = attempts + 1,
		     status = CASE WHEN attempts + 1 >= $5 THEN 'failed' ELSE status END,
		     error_phase = CASE WHEN attempts + 1 >= $5 THEN $3 ELSE error_phase END,
		     error_reason = CASE WHEN attempts + 1 >= $5 THEN $4 ELSE error_reason END,
		     next_attempt_at = CASE WHEN attempts + 1 >= $5 THEN NULL::timestamptz ELSE $6 END,
		     claimed_at = NULL, claimed_by = '', updated_at = now()
		 WHERE infohash = $1 AND claimed_by = $2 AND status = 'processing'
		 RETURNING attempts, status`,
		infohash, owner, string(phase), reason, maxAttempts, at.Add(delay),
	).Scan(&attempts, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProcessingFailure{}, ErrClaimLost
		}
		return ProcessingFailure{}, fmt.Errorf("task: 记版本 %s 的加工失败: %w", infohash, err)
	}
	return ProcessingFailure{Attempts: attempts, Exhausted: status == StatusFailed}, nil
}

// MarkProcessingFailed 直接落 failed(phase, reason)，不烧自动重试预算。
//
// 给永久错用（目录碰撞、跨设备硬链）：再试三次也不会让挂载变成同一块盘，
// 让它立刻进需人工区比排三次退避更诚实（规格 §7.2 碰撞/EXDEV 两行）。
func (s *Store) MarkProcessingFailed(ctx context.Context, infohash, owner string, phase Phase, reason string) error {
	return s.leaveProcessing(ctx, infohash, owner,
		`status = 'failed', error_phase = $3, error_reason = $4, next_attempt_at = NULL`,
		string(phase), reason)
}

// MarkFiled 是入库成功：processing → finished，错误列清空（它们描述的是过去）。
//
// 分片记录（version_file）由 SaveVersionFiles 单独写：入库幂等靠文件本身
// （同 inode 即已落盘），version_file 是给人查的永久账。
func (s *Store) MarkFiled(ctx context.Context, infohash, owner string) error {
	return s.leaveProcessing(ctx, infohash, owner,
		`status = 'finished', error_phase = NULL, error_reason = '',
		 attempts = 0, next_attempt_at = NULL`)
}

// leaveProcessing 是「离开加工阶段」写法的共同形状：条件写、清租约、抢输报 ErrClaimLost。
func (s *Store) leaveProcessing(ctx context.Context, infohash, owner string, set string, args ...any) error {
	query := `UPDATE task_version
	 SET ` + set + `,
	     claimed_at = NULL, claimed_by = '', updated_at = now()
	 WHERE infohash = $1 AND claimed_by = $2 AND status = 'processing'`

	params := append([]any{infohash, owner}, args...)
	tag, err := s.pool.Exec(ctx, query, params...)
	if err != nil {
		return fmt.Errorf("task: 版本 %s 离开加工阶段: %w", infohash, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrClaimLost
	}
	return nil
}

// HasFinishedVersion 报告任务是否已有别的已入库版本。
//
// 它是「新版本入库」文案的唯一判据（规格 §5.6 filed 的 new_version）：任务重开
// 再入库（v2 中字场景）用另一句模板，而这一句只取决于历史，不取决于本次结果。
func (s *Store) HasFinishedVersion(ctx context.Context, movieID, exceptInfohash string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (
		     SELECT 1 FROM task_version
		     WHERE movie_id = $1 AND infohash <> $2 AND status = 'finished'
		 )`,
		movieID, exceptInfohash,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("task: 查任务 %s 是否已有入库版本: %w", movieID, err)
	}
	return exists, nil
}

// TaskOwningNumber 报告是否另有任务占着同一个番号；返回那个任务的 movie_id。
//
// 目录是「一番号一目录」（ADR-0008），所以两个任务撞上同一个番号时谁进这个目录
// 必须有唯一答案：先到者（created_at 早，同刻按 movie_id 字典序）进，后到者
// 失败驻留等人工——绝不合并、绝不 -v2（规格 §7.2 碰撞行）。
//
// 第二个判据是「另有任务已经入库」：先到者若晚于那个已入库的任务动手，它写下的
// movie.nfo 会把别人的元数据盖掉。这个判据是防御性的（正常时序下后到者先就失败了），
// 但它让「谁拥有目录」这个问题在两个方向上都有答案。
func (s *Store) TaskOwningNumber(ctx context.Context, number, movieID string) (string, error) {
	var owner string
	err := s.pool.QueryRow(ctx,
		`SELECT t.movie_id
		 FROM task t
		 JOIN task mine ON mine.movie_id = $2
		 WHERE t.number = $1 AND t.movie_id <> $2
		   AND (
		       (t.created_at, t.movie_id) < (mine.created_at, mine.movie_id)
		       OR EXISTS (
		           SELECT 1 FROM task_version v
		           WHERE v.movie_id = t.movie_id AND v.status = 'finished'
		       )
		   )
		 ORDER BY t.created_at, t.movie_id
		 LIMIT 1`,
		number, movieID,
	).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("task: 查番号 %s 的归属任务: %w", number, err)
	}
	return owner, nil
}

// VersionFile 是 version_file 的一行：一条版本里的一个分片视频。
type VersionFile struct {
	// RelPath 是分片相对下载根（content_path）的位置；单文件种子就是文件名。
	RelPath   string
	SizeBytes int64
	// PartIndex 从 1 起：单片版本是 1，多片按分片序号/自然顺序。
	PartIndex int
	// IsPrimary 标记主分片（cd1 / 最前那一片）。
	IsPrimary bool
}

// SaveVersionFiles 全量写一条版本的分片记录（重跑时覆盖，不累积幽灵行）。
//
// 它不碰租约：写入发生在文件已经落盘之后，与「入库成功」是同一件事的两半。
func (s *Store) SaveVersionFiles(ctx context.Context, infohash string, files []VersionFile) error {
	if strings.TrimSpace(infohash) == "" {
		return errors.New("task: 落分片缺 infohash")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("task: 开事务: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, `DELETE FROM version_file WHERE infohash = $1`, infohash); err != nil {
		return fmt.Errorf("task: 清版本 %s 的分片: %w", infohash, err)
	}
	for _, file := range files {
		if _, err := tx.Exec(ctx,
			`INSERT INTO version_file (infohash, rel_path, size_bytes, part_index, is_primary)
			 VALUES ($1, $2, $3, $4, $5)`,
			infohash, file.RelPath, file.SizeBytes, file.PartIndex, file.IsPrimary,
		); err != nil {
			return fmt.Errorf("task: 落版本 %s 的分片 %s: %w", infohash, file.RelPath, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("task: 提交分片记录: %w", err)
	}
	return nil
}

// VersionFiles 读一条版本的分片记录（按 part_index 排序）。
func (s *Store) VersionFiles(ctx context.Context, infohash string) ([]VersionFile, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT rel_path, size_bytes, part_index, is_primary
		 FROM version_file
		 WHERE infohash = $1
		 ORDER BY part_index, rel_path`,
		infohash,
	)
	if err != nil {
		return nil, fmt.Errorf("task: 读版本 %s 的分片: %w", infohash, err)
	}
	defer rows.Close()

	files := []VersionFile{}
	for rows.Next() {
		var file VersionFile
		if err := rows.Scan(&file.RelPath, &file.SizeBytes, &file.PartIndex, &file.IsPrimary); err != nil {
			return nil, fmt.Errorf("task: 扫分片: %w", err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历分片: %w", err)
	}
	return files, nil
}
