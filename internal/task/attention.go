package task

import (
	"context"
	"fmt"
	"time"
)

// StallReasonPrefix 是「下载停滞」告警的固定开头（规格 §6.1 下载异常行）。
//
// 它有两个读者：reconcile 写这条告警（task_event.kind=alert）时用它开头，
// 面板的「需要人工」区按它认「这条下载停住了但状态仍是 downloading」。
// 两处必须同一个字面量，所以放在这里（reconcile 反向引用本包）。
const StallReasonPrefix = "下载停滞"

// AttentionItem 是「需要人工」区的一行（规格 §8.2）。
//
// 两半共用一个形状：失败的版本（phase + reason）与停滞的下载（reason 是那次告警）。
type AttentionItem struct {
	MovieID  string
	Number   string
	Infohash string
	// Source / Label 是版本的判别词（JavDB 磁力、M-Team 种子 / 中文、破解）：
	// 一部作品的两个版本会同时失败，面板要指出是哪一条，而不是让人记 infohash。
	Source string
	Label  string
	// Phase 是失败发生在哪一步（仅失败行有值）。
	Phase string
	// Reason 是给人看的那句话：失败原因，或停滞告警的原文。
	Reason string
	At     time.Time
}

// Attention 是「需要人工」区的任务侧两半。
//
// 第三半（驻留副作用）属于副作用域，由面板把 side_effect 的 stuck 行拼进来。
type Attention struct {
	// Failed 是落 failed(phase, reason) 的版本，最近更新的在前。
	Failed []AttentionItem
	// Stalled 是仍在下载、但已经发过停滞告警的版本（P12-4 的告警兜底面）。
	Stalled []AttentionItem
}

// Attention 读「需要人工」区（规格 §8.2）。
//
// 停滞的判据不是「进度多久没动」（那是进程内历史，重启就没了），而是**库里那条
// 告警**：reconcile 只在真正判出停滞时写一条 kind=alert 的事件，且同一条版本只写一次
// （HasAlert）。所以这里读到的是「已经停住并且告过警」的版本，不会把正常慢速下载算进来。
func (s *Store) Attention(ctx context.Context, limit int) (Attention, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}

	attention := Attention{Failed: []AttentionItem{}, Stalled: []AttentionItem{}}

	failed, err := s.failedItems(ctx, limit)
	if err != nil {
		return Attention{}, err
	}
	attention.Failed = failed

	stalled, err := s.stalledItems(ctx, limit)
	if err != nil {
		return Attention{}, err
	}
	attention.Stalled = stalled
	return attention, nil
}

func (s *Store) failedItems(ctx context.Context, limit int) ([]AttentionItem, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT v.movie_id, t.number, v.infohash, v.source, v.label,
		        COALESCE(v.error_phase, ''), v.error_reason, v.updated_at
		 FROM task_version v
		 JOIN task t ON t.movie_id = v.movie_id
		 WHERE v.status = 'failed'
		 ORDER BY v.updated_at DESC, v.infohash
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("task: 列失败版本: %w", err)
	}
	defer rows.Close()

	items := []AttentionItem{}
	for rows.Next() {
		var item AttentionItem
		if err := rows.Scan(&item.MovieID, &item.Number, &item.Infohash, &item.Source, &item.Label,
			&item.Phase, &item.Reason, &item.At); err != nil {
			return nil, fmt.Errorf("task: 扫失败版本: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历失败版本: %w", err)
	}
	return items, nil
}

// stalledItems 列「已告过警、仍在下载」的版本。
//
// 人工重试的重新加种是这条告警的终点：种子被重新加进 qBittorrent、进度从头来，
// 之前那次停滞就不再说当前事实。判据是事件序（不是时间戳）：最近一条揭示
// 「重新加种过」的 manual_retry 事件之后还没再告过警。
func (s *Store) stalledItems(ctx context.Context, limit int) ([]AttentionItem, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT v.movie_id, t.number, v.infohash, v.source, v.label, e.reason, e.at
		 FROM task_version v
		 JOIN task t ON t.movie_id = v.movie_id
		 JOIN LATERAL (
		     SELECT id, reason, at
		     FROM task_event
		     WHERE infohash = v.infohash AND kind = 'alert' AND reason LIKE $2 ESCAPE '\'
		     ORDER BY id DESC
		     LIMIT 1
		 ) e ON true
		 WHERE v.status = 'downloading'
		   AND e.id > COALESCE((
		       SELECT max(id) FROM task_event
		       WHERE infohash = v.infohash AND kind = 'manual_retry'
		         AND detail->>'action' = $3
		   ), 0)
		 ORDER BY e.at DESC, v.infohash
		 LIMIT $1`,
		limit, escapeLike(StallReasonPrefix)+"%", RetryActionReadded,
	)
	if err != nil {
		return nil, fmt.Errorf("task: 列停滞下载: %w", err)
	}
	defer rows.Close()

	items := []AttentionItem{}
	for rows.Next() {
		var item AttentionItem
		if err := rows.Scan(&item.MovieID, &item.Number, &item.Infohash, &item.Source, &item.Label,
			&item.Reason, &item.At); err != nil {
			return nil, fmt.Errorf("task: 扫停滞下载: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历停滞下载: %w", err)
	}
	return items, nil
}
