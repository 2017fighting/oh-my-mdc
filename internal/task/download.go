package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// 下载阶段的三个落库动作（规格 §6.1「→ processing」「失败」「重启」）。
//
// 它们都要求调用方先持有租约：`WHERE claimed_by = $owner` 让「抢输」变成一次零写入，
// 而不是把别人的账覆盖掉。抢输返回 ErrClaimLost（规格 §4.4：别当失败）。

// Version 读一条版本；不在本项目手里时返回 ErrVersionNotFound。
//
// webhook 进门第一件事就是它：未知/非本项目 infohash 要静默 no-op（零写入，P8-2）。
func (s *Store) Version(ctx context.Context, infohash string) (Version, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+versionColumns+`
		 FROM task_version v
		 JOIN task t ON t.movie_id = v.movie_id
		 WHERE v.infohash = $1`,
		infohash,
	)

	version, err := scanVersion(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Version{}, ErrVersionNotFound
		}
		return Version{}, fmt.Errorf("task: 读版本 %s: %w", infohash, err)
	}
	return version, nil
}

// DownloadingVersions 列「已经加过种、还在下载」的版本（对账的发现面）。
//
// add_pending=true 的不算：它们还没进 qBittorrent，把它们判成「种子已移除」是致命的。
func (s *Store) DownloadingVersions(ctx context.Context) ([]Version, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+versionColumns+`
		 FROM task_version v
		 JOIN task t ON t.movie_id = v.movie_id
		 WHERE v.status = 'downloading' AND NOT v.add_pending
		 ORDER BY v.created_at, v.infohash`,
	)
	if err != nil {
		return nil, fmt.Errorf("task: 列下载中的版本: %w", err)
	}
	defer rows.Close()

	versions := []Version{}
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("task: 扫下载中的版本: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历下载中的版本: %w", err)
	}
	return versions, nil
}

// MarkProcessing 把版本推进到 processing 并清掉租约（加工在 #27 接手）。
//
// 上一次下载期留下的 error_phase/error_reason 一并清空：它们描述的是过去。
func (s *Store) MarkProcessing(ctx context.Context, infohash, owner string) error {
	return s.leaveDownloading(ctx, infohash, owner, StatusProcessing, "", "")
}

// MarkDownloadFailed 落 failed(下载阶段, reason) 并清掉租约。
func (s *Store) MarkDownloadFailed(ctx context.Context, infohash, owner, reason string) error {
	return s.leaveDownloading(ctx, infohash, owner, StatusFailed, PhaseDownloading, reason)
}

// leaveDownloading 是两条「离开下载阶段」写法的共同形状：改状态、清错误列、清租约。
//
// 租约不属于自己或状态已不是下载中 → 零写入 + ErrClaimLost（抢输不是失败）。
func (s *Store) leaveDownloading(ctx context.Context, infohash, owner string, status Status, phase Phase, reason string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE task_version
		 SET status = $3, error_phase = $4, error_reason = $5,
		     claimed_at = NULL, claimed_by = '', updated_at = now()
		 WHERE infohash = $1 AND claimed_by = $2 AND status = 'downloading'`,
		infohash, owner, string(status), nullIfEmpty(string(phase)), reason,
	)
	if err != nil {
		return fmt.Errorf("task: 版本 %s 离开下载阶段（%s）: %w", infohash, status, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrClaimLost
	}
	return nil
}

// ResetVersion 是重启复位的一条产物：引擎据此在时间线上留一句「为什么它会重跑」。
type ResetVersion struct {
	Infohash string
	MovieID  string
}

// ResetProcessing 是进程启动时的阶段级复位（规格 §6.1 重启行）。
//
// 只碰 processing：加工没有步骤游标，复位＝清租约、清重试计数、下一位从头跑（幂等）。
// downloading 不动（真实进度在 qBittorrent），finished/failed 不动（终态不再自动碰）。
//
// 租约只清过期的那一种（§4.4 的同一个 10 分钟判据）：滚动更新时旧 pod 还活着，
// 它的租约不该被新进程抢走——版本级互斥仍归 claim，不归复位。
func (s *Store) ResetProcessing(ctx context.Context) ([]ResetVersion, error) {
	rows, err := s.pool.Query(ctx,
		`UPDATE task_version
		 SET claimed_at = NULL, claimed_by = '', attempts = 0,
		     next_attempt_at = NULL, updated_at = now()
		 WHERE status = 'processing'
		   AND (claimed_at IS NULL
		        OR claimed_at < now() - make_interval(secs => $1::double precision))
		 RETURNING infohash, movie_id`,
		leaseSeconds(),
	)
	if err != nil {
		return nil, fmt.Errorf("task: 复位加工阶段: %w", err)
	}
	defer rows.Close()

	reset := []ResetVersion{}
	for rows.Next() {
		var version ResetVersion
		if err := rows.Scan(&version.Infohash, &version.MovieID); err != nil {
			return nil, fmt.Errorf("task: 扫复位结果: %w", err)
		}
		reset = append(reset, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历复位结果: %w", err)
	}
	return reset, nil
}

// HasAlert 报告这条版本是否已经记过某类告警（按 reason 前缀区分）。
//
// 「告警只发一次」的判据在 task_event（GLOSSARY「任务事件」/规格 §5.6），
// 不靠进程内记忆：重启后同一条版本不该再告警一次。
func (s *Store) HasAlert(ctx context.Context, infohash, reasonPrefix string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (
		     SELECT 1 FROM task_event
		     WHERE infohash = $1 AND kind = 'alert' AND reason LIKE $2 ESCAPE '\'
		 )`,
		infohash, escapeLike(reasonPrefix)+"%",
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("task: 查版本 %s 的告警记录: %w", infohash, err)
	}
	return exists, nil
}
