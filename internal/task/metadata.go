package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// 元数据三面与加工阶段的落库（规格 §3/§7.1/§6.1）。
//
// 三条不变式：
//
//   - **值在强类型列**：字段图（§7.1）定下来的每一列一个列，图片不是列
//     （它的「值」就落在 provenance 的 raw_value 上，见 scrape 的字段图）。
//   - **复用判据 = provenance 非空**：刮过一次（哪怕上游什么都没给）就算有了。
//     判据与标签、名称无关（GLOSSARY「复用元数据」）。
//   - **加工租约**：取数与整理都在同一条版本租约下跑（§4.4）；成功后放回租约、
//     清掉重试计数，让整理票接着在同一条版本上干活。

// ScrapeFailurePrefix 是刮削取数失败事件的 reason 前缀。
//
// 每失败一次留一条（时间线要看得见尝试史）；聚合整理票（#29）在三次耗尽时
// 用 HasAlert 按这个前缀判定「这条版本告过警没有」，所以它必须是常量。
const ScrapeFailurePrefix = "刮削取数失败"

// Lease 是加工段持有的一份版本租约：成功后由 SaveMetadata 放回。
//
// 零值表示「不放回」（fixture / 后台修数据）。**按 infohash 而非 movie_id**：
// 一条任务可以有两条版本（M-Team 降级场景），放回别人的租约会连带清掉它的重试计数。
type Lease struct {
	Infohash string
	Owner    string
}

// Metadata 是 §7.1 的强类型列值 + 两个 JSON 面。
type Metadata struct {
	TitleJA        string
	TitleZH        string
	Outline        string
	OutlineZH      string
	ReleaseDate    *time.Time
	RuntimeMinutes *int
	Studio         string
	Publisher      string
	Series         string
	Director       string
	TrailerURL     string
	Rating         *float64
	Tags           []string
	// Provenance 是逐字段 {source, fetched_at, raw_value}，原样落 jsonb。
	Provenance json.RawMessage
	// RawSnapshot 是逐源原始返回，原样落 jsonb。
	RawSnapshot json.RawMessage
}

// Actor 是演员表的一行（actor）加它与任务的关联（task_actor，role 固定 'actor'）。
//
// Gender 是空串表示**不知道**：落 NULL，不替上游猜一个最常见的（规格 §4.1）。
type Actor struct {
	JavDBID   string
	Name      string
	Gender    string
	AvatarURL string
}

// Metadata 读一个任务的元数据三面；exists 是复用判据（provenance 非空）。
func (s *Store) Metadata(ctx context.Context, movieID string) (Metadata, bool, error) {
	var (
		metadata        Metadata
		provenance, raw []byte
		exists          bool
	)
	err := s.pool.QueryRow(ctx,
		`SELECT title_ja, title_zh, outline, outline_zh, release_date, runtime_minutes,
		        studio, publisher, series, director, trailer_url, rating, tags,
		        provenance, raw_snapshot,
		        (provenance <> '{}'::jsonb) AS has_metadata
		 FROM task WHERE movie_id = $1`,
		movieID,
	).Scan(
		&metadata.TitleJA, &metadata.TitleZH, &metadata.Outline, &metadata.OutlineZH,
		&metadata.ReleaseDate, &metadata.RuntimeMinutes, &metadata.Studio, &metadata.Publisher,
		&metadata.Series, &metadata.Director, &metadata.TrailerURL, &metadata.Rating, &metadata.Tags,
		&provenance, &raw, &exists,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Metadata{}, false, ErrTaskNotFound
		}
		return Metadata{}, false, fmt.Errorf("task: 读任务 %s 的元数据: %w", movieID, err)
	}
	metadata.Provenance = json.RawMessage(provenance)
	metadata.RawSnapshot = json.RawMessage(raw)
	return metadata, exists, nil
}

// HasMetadata 报告任务有没有刮过：复用判据（P9-2），判据是 provenance 非空。
func (s *Store) HasMetadata(ctx context.Context, movieID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT (provenance <> '{}'::jsonb) FROM task WHERE movie_id = $1`, movieID,
	).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrTaskNotFound
		}
		return false, fmt.Errorf("task: 查任务 %s 是否已刮过: %w", movieID, err)
	}
	return exists, nil
}

// SaveMetadata 在一个事务里写元数据三面 + 演员，并放回加工租约。
//
// title_zh/outline_zh 不写：那是 AI 翻译的产物（#30），取数重刮不该抹掉它。
// lease 非零值时顺手清掉**这一条版本**的租约与重试计数（整理票接着在一条干净的
// 版本上跑）；租约已经不在自己手里不算错——元数据是任务级的、幂等的，
// 别人的租约也不该被我清。
func (s *Store) SaveMetadata(ctx context.Context, movieID string, lease Lease, metadata Metadata, actors []Actor) error {
	if strings.TrimSpace(movieID) == "" {
		return errors.New("task: 落元数据缺 movie_id")
	}
	if len(metadata.Provenance) == 0 {
		metadata.Provenance = json.RawMessage(`{}`)
	}
	if len(metadata.RawSnapshot) == 0 {
		metadata.RawSnapshot = json.RawMessage(`{}`)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("task: 开事务: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	tag, err := tx.Exec(ctx,
		`UPDATE task
		 SET title_ja = $2, outline = $3, release_date = $4, runtime_minutes = $5,
		     studio = $6, publisher = $7, series = $8, director = $9, trailer_url = $10,
		     rating = $11, tags = $12, provenance = $13, raw_snapshot = $14, updated_at = now()
		 WHERE movie_id = $1`,
		movieID, metadata.TitleJA, metadata.Outline, metadata.ReleaseDate, metadata.RuntimeMinutes,
		metadata.Studio, metadata.Publisher, metadata.Series, metadata.Director, metadata.TrailerURL,
		metadata.Rating, nonNilStrings(metadata.Tags), metadata.Provenance, metadata.RawSnapshot,
	)
	if err != nil {
		return fmt.Errorf("task: 落任务 %s 的元数据: %w", movieID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTaskNotFound
	}

	if err := saveActors(ctx, tx, movieID, actors); err != nil {
		return err
	}

	// 租约与重试计数：只清这一条版本、且只在租约还在自己手里时（条件写），清不到不是错。
	if lease.Infohash != "" && lease.Owner != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE task_version
			 SET claimed_at = NULL, claimed_by = '', attempts = 0, next_attempt_at = NULL, updated_at = now()
			 WHERE infohash = $1 AND claimed_by = $2`,
			lease.Infohash, lease.Owner,
		); err != nil {
			return fmt.Errorf("task: 放回版本 %s 的加工租约: %w", lease.Infohash, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("task: 提交元数据: %w", err)
	}
	return nil
}

// saveActors 是演员落行的单点：actor 按 javdb_actor_id upsert，
// task_actor 全量换一份（同一任务重刮后消失的演员必须真的消失）。
func saveActors(ctx context.Context, tx pgx.Tx, movieID string, actors []Actor) error {
	// 排序去重：「组内保 JavDB 顺序」靠插入序，而 task_actor 的读法按 role+插入序，
	// 所以重复 id 只保留第一次出现的那一行（PK 会撞，撞了会整次回滚）。
	seen := make(map[string]bool, len(actors))
	ordered := make([]Actor, 0, len(actors))
	for _, actor := range actors {
		id := strings.TrimSpace(actor.JavDBID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		actor.JavDBID = id
		ordered = append(ordered, actor)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM task_actor WHERE movie_id = $1 AND role = 'actor'`, movieID); err != nil {
		return fmt.Errorf("task: 清任务 %s 的演员关联: %w", movieID, err)
	}

	for _, actor := range ordered {
		// 性别/头像用 COALESCE：这次没给不该把上次知道的抹掉。
		if _, err := tx.Exec(ctx,
			`INSERT INTO actor (javdb_actor_id, name, gender, avatar_url, updated_at)
			 VALUES ($1, $2, $3, $4, now())
			 ON CONFLICT (javdb_actor_id) DO UPDATE
			 SET name = EXCLUDED.name,
			     gender = COALESCE(EXCLUDED.gender, actor.gender),
			     avatar_url = COALESCE(EXCLUDED.avatar_url, actor.avatar_url),
			     updated_at = now()`,
			actor.JavDBID, actor.Name, nullIfEmpty(actor.Gender), nullIfEmpty(actor.AvatarURL),
		); err != nil {
			return fmt.Errorf("task: 落演员 %s: %w", actor.JavDBID, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO task_actor (movie_id, javdb_actor_id, role) VALUES ($1, $2, 'actor')
			 ON CONFLICT DO NOTHING`,
			movieID, actor.JavDBID,
		); err != nil {
			return fmt.Errorf("task: 关联任务 %s 与演员 %s: %w", movieID, actor.JavDBID, err)
		}
	}
	return nil
}

// Actors 读一个任务的演员，按 javdb_actor_id 排序（**不是**展示顺序）。
//
// NFO 要的是「女优排前、组内保 JavDB 顺序」（规格 §7.1 演员行），而那是源数据的顺序：
// 它原样留在 provenance 的 actors 条目（raw_value 是 JavDB 的 actors[] 数组顺序）与
// raw_snapshot 里，写 NFO 的那一层按它排；这里只保证「谁在这部片里」这一集合。
func (s *Store) Actors(ctx context.Context, movieID string) ([]Actor, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT a.javdb_actor_id, a.name, COALESCE(a.gender, ''), COALESCE(a.avatar_url, '')
		 FROM task_actor ta
		 JOIN actor a ON a.javdb_actor_id = ta.javdb_actor_id
		 WHERE ta.movie_id = $1 AND ta.role = 'actor'
		 ORDER BY ta.javdb_actor_id`,
		movieID,
	)
	if err != nil {
		return nil, fmt.Errorf("task: 读任务 %s 的演员: %w", movieID, err)
	}
	defer rows.Close()

	actors := []Actor{}
	for rows.Next() {
		var actor Actor
		if err := rows.Scan(&actor.JavDBID, &actor.Name, &actor.Gender, &actor.AvatarURL); err != nil {
			return nil, fmt.Errorf("task: 扫演员: %w", err)
		}
		actors = append(actors, actor)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历演员: %w", err)
	}
	return actors, nil
}

// ProcessingVersions 列本轮该推进的加工版本：processing、租约空闲或过期、退避到期。
//
// 退避到期由 next_attempt_at 表示（失败一次推一次，见 ScheduleProcessingRetry）；
// 时间用库的 now()，与租约判据同一个时钟。
func (s *Store) ProcessingVersions(ctx context.Context) ([]Version, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+versionColumns+`
		 FROM task_version v
		 JOIN task t ON t.movie_id = v.movie_id
		 WHERE v.status = 'processing'
		   AND NOT v.add_pending
		   AND (v.claimed_at IS NULL OR v.claimed_at < now() - make_interval(secs => $1::double precision))
		   AND (v.next_attempt_at IS NULL OR v.next_attempt_at <= now())
		 ORDER BY v.created_at, v.infohash`,
		leaseSeconds(),
	)
	if err != nil {
		return nil, fmt.Errorf("task: 列加工中的版本: %w", err)
	}
	defer rows.Close()

	versions := []Version{}
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("task: 扫加工中的版本: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历加工中的版本: %w", err)
	}
	return versions, nil
}

// ScheduleProcessingRetry 记一次加工失败：attempts+1、next_attempt_at 推到 at+delay、
// 放回租约。状态不变（失败三次落 failed 的裁决归聚合的整理票，#29）。
//
// 租约不在自己手里 → ErrClaimLost（零写入，别把别人的账改了）。
func (s *Store) ScheduleProcessingRetry(ctx context.Context, infohash, owner string, at time.Time, delay time.Duration) (int, error) {
	var attempts int
	err := s.pool.QueryRow(ctx,
		`UPDATE task_version
		 SET attempts = attempts + 1,
		     next_attempt_at = $3,
		     claimed_at = NULL, claimed_by = '', updated_at = now()
		 WHERE infohash = $1 AND claimed_by = $2 AND status = 'processing'
		 RETURNING attempts`,
		infohash, owner, at.Add(delay),
	).Scan(&attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrClaimLost
		}
		return 0, fmt.Errorf("task: 记版本 %s 的加工重试: %w", infohash, err)
	}
	return attempts, nil
}

// nonNilStrings 把 nil 切片写成空数组而不是 NULL（tags 列是 NOT NULL）。
func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
