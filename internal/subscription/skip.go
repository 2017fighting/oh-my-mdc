package subscription

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// 跳过表的读写（规格 §6.4/§8.4，GLOSSARY「跳过 / 收下」，迁移 00016）。
//
// 生命周期只有三件事：落一行（幂等）、「收下」（只置 accepted_at）、30 天后清掉没被
// 收下的那些。三件事分在四个方法里，每个都只做一件事。

// ErrSkipNotFound 说明请求里的跳过 id 不存在（或不属于这条订阅）——HTTP 404。
var ErrSkipNotFound = errors.New("subscription: 跳过记录不存在")

// RecordSkip 落一行跳过，幂等（回看窗口每轮都会重见同一部作品）。
//
// 三种结局分别是「新落一行」「已经在表里（每轮再现）」与「这一部已经被收下过」——
// 最后一种是判据给用户的裁决让路的意思：同一部作品不该每轮都被重新排一遍，更不能在
// 被收下之后又冒出来一张新卡片。
func (s *Store) RecordSkip(ctx context.Context, skip Skip) (SkipOutcome, error) {
	// 先看有没有「已收下」的记录：部分唯一索引只管没被收下的那些，所以这件事必须先问，
	// 不能指望 ON CONFLICT 挡住它。
	var accepted bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (
		     SELECT 1 FROM subscription_skip
		     WHERE subscription_id = $1 AND movie_id = $2 AND accepted_at IS NOT NULL)`,
		skip.SubscriptionID, skip.MovieID,
	).Scan(&accepted); err != nil {
		return SkipPresent, fmt.Errorf("subscription: 查 %s 的收下记录: %w", skip.MovieID, err)
	}
	if accepted {
		return SkipAccepted, nil
	}

	// ON CONFLICT 要重复写部分索引的谓词，否则 PG 不知道该拿哪个索引去判冲突。
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO subscription_skip
		     (subscription_id, movie_id, number, title, release_date, reason, skipped_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (subscription_id, movie_id) WHERE accepted_at IS NULL DO NOTHING`,
		skip.SubscriptionID, skip.MovieID, skip.Number, skip.Title,
		dateParam(skip.ReleaseDate), skip.Reason, skip.SkippedAt,
	)
	if err != nil {
		return SkipPresent, fmt.Errorf("subscription: 落 %s 的跳过行: %w", skip.MovieID, err)
	}
	if tag.RowsAffected() == 0 {
		return SkipPresent, nil
	}
	return SkipRecorded, nil
}

// ListSkips 读一条订阅最近 30 天**没被收下**的跳过（§8.4 的 GET skips）。
//
// 收下的那些不进这个列表：它们已经不是「被排掉的」，而是待办（面板上要认它们的地方
// 是任务表）。新的在前——「最近被排掉的」本来就该第一眼看最新的。
func (s *Store) ListSkips(ctx context.Context, subscriptionID int64) ([]Skip, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, subscription_id, movie_id, number, title, release_date, reason,
		        skipped_at, accepted_at
		 FROM subscription_skip
		 WHERE subscription_id = $1 AND accepted_at IS NULL
		   AND skipped_at >= now() - make_interval(days => $2)
		 ORDER BY skipped_at DESC, id DESC`,
		subscriptionID, SkipRetentionDays,
	)
	if err != nil {
		return nil, fmt.Errorf("subscription: 列订阅 %d 的跳过: %w", subscriptionID, err)
	}
	defer rows.Close()

	out := []Skip{}
	for rows.Next() {
		skip, err := scanSkip(rows)
		if err != nil {
			return nil, fmt.Errorf("subscription: 扫跳过: %w", err)
		}
		out = append(out, skip)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscription: 遍历跳过: %w", err)
	}
	return out, nil
}

// GetSkip 读一条跳过。按订阅限定：别的订阅的 id 不算（它不是全局编号）。
func (s *Store) GetSkip(ctx context.Context, subscriptionID, skipID int64) (Skip, error) {
	skip, err := scanSkip(s.pool.QueryRow(ctx,
		`SELECT id, subscription_id, movie_id, number, title, release_date, reason,
		        skipped_at, accepted_at
		 FROM subscription_skip
		 WHERE subscription_id = $1 AND id = $2`,
		subscriptionID, skipID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Skip{}, ErrSkipNotFound
		}
		return Skip{}, fmt.Errorf("subscription: 读跳过 %d: %w", skipID, err)
	}
	return skip, nil
}

// AcceptSkip 记下「收下」：只置 accepted_at，行留着。
//
// 行不删是刻意的：它是「用户裁决过这一部」的唯一依据——删掉之后同一部作品下一轮又会被
// 判据重新排一遍，那个出口就等于没处可用。已经收下过的再置一次是幂等的（时间戳不刷新：
// 第一次收下那一刻才是这件事发生的时间）。
func (s *Store) AcceptSkip(ctx context.Context, subscriptionID, skipID int64) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE subscription_skip
		 SET accepted_at = COALESCE(accepted_at, now())
		 WHERE subscription_id = $1 AND id = $2`,
		subscriptionID, skipID,
	)
	if err != nil {
		return fmt.Errorf("subscription: 收下跳过 %d: %w", skipID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSkipNotFound
	}
	return nil
}

// PurgeSkips 清掉 before 之前的跳过行，返回清掉几条。
//
// 只清**没被收下**的：一句「最近被排掉的」的 30 天窗口到了就该退场，而被收下的那条是
// 用户裁决过的记录，也是同一部作品不再被重新排一遍的依据（见 AcceptSkip）。
func (s *Store) PurgeSkips(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM subscription_skip WHERE accepted_at IS NULL AND skipped_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("subscription: 清理过期的跳过行: %w", err)
	}
	return tag.RowsAffected(), nil
}

// scanSkip 扫一行跳过；调用方负责把它包成业务错误。
func scanSkip(row scanner) (Skip, error) {
	var (
		out       Skip
		release   *time.Time
		accepted  *time.Time
		skippedAt time.Time
	)
	if err := row.Scan(&out.ID, &out.SubscriptionID, &out.MovieID, &out.Number,
		&out.Title, &release, &out.Reason, &skippedAt, &accepted); err != nil {
		return Skip{}, err
	}
	out.ReleaseDate = dateOnly(release)
	out.SkippedAt = skippedAt
	if accepted != nil {
		at := *accepted
		out.AcceptedAt = &at
	}
	return out, nil
}

// dateParam 把可空日期递给 pgx：nil 走 SQL NULL，否则只取到日。
func dateParam(at *time.Time) any {
	if at == nil {
		return nil
	}
	return at.Format(dateLayout)
}
