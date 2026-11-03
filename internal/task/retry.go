package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// 人工重试的两条复位（规格 §6.1 人工重试行 / #31）。
//
// 与 download.go / organize.go 同一形状：都要求调用方先持有租约（`WHERE claimed_by = $owner`），
// 抢输返回 ErrClaimLost。重试的**判断**归 retry 包（它要看 qbt 里那件事），
// 这里只负责「复位成哪个起点」这一句 SQL。

// ResetForRetry 把一条版本拨回 processing 开头：清错误列、清尝试计数与退避、放回租约。
//
// 这是 P13-1 的落库一半：「非 finished 版本回 processing 开头重跑（幂等）」。
// 加工没有步骤游标，所以复位就是这几列——取数段与整理段下轮各自从头跑，
// 已经落盘的产物按各自的幂等判据（同 inode / NFO 内容 hash）跳过。
//
// forceRescrape=true 顺手置起 force_rescrape：取数段见它就跳过复用判据（GLOSSARY「复用元数据」），
// 取数成功后自己清掉。标记只增不减（`OR`），因为一次普通重试不该撤销上一次强制重刮的意图。
//
// 状态不做前置判据：调用方在拿到租约后已经看过最新的 status（finished 只有 force 才允许进来），
// 这里再加一条只会把「状态在两次读之间变了」变成一次静默失败。
func (s *Store) ResetForRetry(ctx context.Context, infohash, owner string, forceRescrape bool) (Version, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE task_version v
		 SET status = 'processing',
		     error_phase = NULL,
		     error_reason = '',
		     attempts = 0,
		     next_attempt_at = NULL,
		     add_pending = false,
		     force_rescrape = v.force_rescrape OR $3,
		     claimed_at = NULL, claimed_by = '', updated_at = now()
		 FROM task t
		 WHERE v.infohash = $1 AND v.claimed_by = $2 AND t.movie_id = v.movie_id
		 RETURNING `+versionColumns,
		infohash, owner, forceRescrape,
	)

	version, err := scanVersion(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Version{}, ErrClaimLost
		}
		return Version{}, fmt.Errorf("task: 复位版本 %s 到加工开头: %w", infohash, err)
	}
	return version, nil
}

// MarkRetryDownloading 把重新加种成功的版本放回 downloading（P13-2 的落库一半）。
//
// 语义与 MarkAdded 对齐：add_pending 清掉（种已经进了 qBittorrent）、清单项置待删
// （加下载之后才删，规格 §6.1）。另外清掉上一次失败留下的错误列与尝试计数：
// 它们描述的是过去，而这条版本现在是一个新的下载。
//
// forceRescrape=true 顺手置起 force_rescrape（对人点了「强制重刮」的下载中任务：
// 下载完之后那一次取数也不复用元数据）。标记只增不减，与 ResetForRetry 同一口径。
func (s *Store) MarkRetryDownloading(ctx context.Context, infohash, owner string, forceRescrape bool) (Version, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE task_version v
		 SET status = 'downloading',
		     error_phase = NULL,
		     error_reason = '',
		     attempts = 0,
		     next_attempt_at = NULL,
		     add_pending = false,
		     list_removal_pending = true,
		     force_rescrape = v.force_rescrape OR $3,
		     claimed_at = NULL, claimed_by = '', updated_at = now()
		 FROM task t
		 WHERE v.infohash = $1 AND v.claimed_by = $2 AND t.movie_id = v.movie_id
		 RETURNING `+versionColumns,
		infohash, owner, forceRescrape,
	)

	version, err := scanVersion(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Version{}, ErrClaimLost
		}
		return Version{}, fmt.Errorf("task: 把版本 %s 放回下载中: %w", infohash, err)
	}
	return version, nil
}

// VersionsOf 读一个任务的全部版本（按创建序），是人工重试的发现面。
//
// 任务不存在与「任务存在但还没有版本」是两件事：前者 ErrTaskNotFound，
// 后者返回空切片（面板抽屉能画出来）。
func (s *Store) VersionsOf(ctx context.Context, movieID string) ([]Version, error) {
	if _, err := s.Number(ctx, movieID); err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT `+versionColumns+`
		 FROM task_version v
		 JOIN task t ON t.movie_id = v.movie_id
		 WHERE v.movie_id = $1
		 ORDER BY v.created_at, v.infohash`,
		movieID,
	)
	if err != nil {
		return nil, fmt.Errorf("task: 读任务 %s 的版本: %w", movieID, err)
	}
	defer rows.Close()

	versions := []Version{}
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("task: 扫版本: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历任务 %s 的版本: %w", movieID, err)
	}
	return versions, nil
}

// RawSnapshot 读任务的逐源原始返回（强制重刮前的对比基准，规格 §3）。
//
// 它必须在重取之前读：取数成功那一刻 raw_snapshot 被整体覆盖，旧的那份就没了。
func (s *Store) RawSnapshot(ctx context.Context, movieID string) (json.RawMessage, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx,
		`SELECT raw_snapshot FROM task WHERE movie_id = $1`, movieID,
	).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTaskNotFound
		}
		return nil, fmt.Errorf("task: 读任务 %s 的 raw_snapshot: %w", movieID, err)
	}
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(raw), nil
}
