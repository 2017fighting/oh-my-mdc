package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 是任务域的读写门面。它只认一个连接池：落库（任务 + 版本）要在一个事务里，
// 假装成几次独立查询会让「先落库再加种」的判据在崩溃边缘失效。
type Store struct {
	pool  *pgxpool.Pool
	locks *keyedLocks
}

// NewStore 用连接池装配任务域。
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, locks: newKeyedLocks()}
}

// Lock 拿进程内 per-version 锁（规格 §4.4）。返回的函数负责释放，可重复调用。
//
// 它挡的是同一进程里两条 goroutine（poll / webhook / 对账）同时处理同一版本；
// 跨进程的互斥仍归数据库里的 claim 租约。
func (s *Store) Lock(infohash string) func() {
	return s.locks.lock(infohash)
}

// SaveCandidate 在一个事务里保证任务行存在，并在 infohash 尚未出现时写入版本行。
//
// 返回 inserted=true 才表示「这次真的新建了一条版本」：调用方据此决定是否加种。
// 同 infohash 已有版本行时不做任何写入（规格 §6.1 去重行）。
func (s *Store) SaveCandidate(ctx context.Context, candidate Candidate) (bool, error) {
	if err := validateCandidate(candidate); err != nil {
		return false, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("task: 开事务: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// 清单 id 是「首次发现来源」：任务已存在时不改它（规格 §4.1）。
	if _, err := tx.Exec(ctx,
		`INSERT INTO task (movie_id, number, list_id) VALUES ($1, $2, $3)
		 ON CONFLICT (movie_id) DO UPDATE
		 SET number = EXCLUDED.number, updated_at = now()
		 WHERE task.number IS DISTINCT FROM EXCLUDED.number`,
		candidate.MovieID, candidate.Number, candidate.ListID,
	); err != nil {
		return false, fmt.Errorf("task: 落任务 %s: %w", candidate.MovieID, err)
	}

	tag, err := tx.Exec(ctx,
		`INSERT INTO task_version (infohash, movie_id, source, delivery, label, payload, add_pending)
		 VALUES ($1, $2, $3, $4, $5, $6, true)
		 ON CONFLICT (infohash) DO NOTHING`,
		candidate.Infohash, candidate.MovieID, string(candidate.Source),
		string(candidate.Delivery), candidate.Label, candidate.Payload,
	)
	if err != nil {
		return false, fmt.Errorf("task: 落版本 %s: %w", candidate.Infohash, err)
	}
	inserted := tag.RowsAffected() == 1
	if !inserted {
		// 同 infohash 已属于别的作品/已有版本行：连任务行一起回滚。
		// 否则会留下一个没有版本的任务（task_state 为 NULL，且每轮重查磁力）。
		return false, nil
	}

	// 事件的 reason 说的是「这条版本从哪来」：定源走了哪条路，物证在这条时间线上。
	if _, err := tx.Exec(ctx,
		`INSERT INTO task_event (movie_id, infohash, kind, to_state, phase, reason)
		 VALUES ($1, $2, 'state_change', 'downloading', 'downloading', $3)`,
		candidate.MovieID, candidate.Infohash, discoveredReason(candidate.Source),
	); err != nil {
		return false, fmt.Errorf("task: 记版本创建事件: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("task: 提交落库: %w", err)
	}
	return true, nil
}

// TaskHasVersion 报告这个任务是否已经有版本（P7-6：有就不再查源）。
func (s *Store) TaskHasVersion(ctx context.Context, movieID string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM task_version WHERE movie_id = $1)`, movieID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("task: 查任务 %s 是否已有版本: %w", movieID, err)
	}
	return exists, nil
}

// Number 读一个任务的番号。
//
// 旁路引擎用它给待办补番号：番号是媒体库目录名（规格 §7.2），而 side_effect 的
// payload 未必带它（早先入箱的待办、或由别的票入箱的待办）。
func (s *Store) Number(ctx context.Context, movieID string) (string, error) {
	var number string
	if err := s.pool.QueryRow(ctx,
		`SELECT number FROM task WHERE movie_id = $1`, movieID,
	).Scan(&number); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrTaskNotFound
		}
		return "", fmt.Errorf("task: 读任务 %s 的番号: %w", movieID, err)
	}
	return number, nil
}

// Claim 给一条指定版本上租约。抢不到（别人拿着未过期的租约）返回 ErrClaimLost。
func (s *Store) Claim(ctx context.Context, infohash, owner string) (Version, error) {
	return s.claim(ctx, infohash, owner)
}

// ClaimPending 给下一条待加种的版本上租约（add_pending=true 且租约空闲）。
//
// 没有活时返回 ErrNoPendingTask —— 这是循环的正常出口，不是失败。
func (s *Store) ClaimPending(ctx context.Context, owner string) (Version, error) {
	row := s.pool.QueryRow(ctx,
		`WITH candidate AS (
		     SELECT infohash, movie_id
		     FROM task_version
		     WHERE add_pending
		       AND (claimed_at IS NULL OR claimed_at < now() - make_interval(secs => $1::double precision))
		     ORDER BY created_at, infohash
		     LIMIT 1
		     FOR UPDATE SKIP LOCKED
		 )
		 UPDATE task_version v
		 SET claimed_at = now(), claimed_by = $2, updated_at = now()
		 FROM candidate c
		 JOIN task t ON t.movie_id = c.movie_id
		 WHERE v.infohash = c.infohash
		 RETURNING `+versionColumns,
		leaseSeconds(), owner,
	)

	version, err := scanVersion(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Version{}, ErrNoPendingTask
		}
		return Version{}, err
	}
	return version, nil
}

// PendingAdds 列出本轮要加种的版本（add_pending=true 且租约空闲）。
//
// 与逐条 ClaimPending 的区别：先把名单定下来，再逐条 Claim。这样某一条加种失败、
// 租约被放回之后，同一轮不会反复捡到它，挡住下一批。
func (s *Store) PendingAdds(ctx context.Context, limit int) ([]Version, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}

	rows, err := s.pool.Query(ctx,
		`SELECT `+versionColumns+`
		 FROM task_version v
		 JOIN task t ON t.movie_id = v.movie_id
		 WHERE v.add_pending
		   AND (v.claimed_at IS NULL OR v.claimed_at < now() - make_interval(secs => $1::double precision))
		 ORDER BY v.created_at, v.infohash
		 LIMIT $2`,
		leaseSeconds(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("task: 列待加种版本: %w", err)
	}
	defer rows.Close()

	pending := []Version{}
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("task: 扫待加种版本: %w", err)
		}
		pending = append(pending, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历待加种版本: %w", err)
	}
	return pending, nil
}

// Claim 的单点实现：指定 infohash 的条件 UPDATE。
func (s *Store) claim(ctx context.Context, infohash, owner string) (Version, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE task_version v
		 SET claimed_at = now(), claimed_by = $2, updated_at = now()
		 FROM task t
		 WHERE v.infohash = $1
		   AND t.movie_id = v.movie_id
		   AND (v.claimed_at IS NULL OR v.claimed_at < now() - make_interval(secs => $3::double precision))
		 RETURNING `+versionColumns,
		infohash, owner, leaseSeconds(),
	)

	version, err := scanVersion(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Version{}, ErrClaimLost
		}
		return Version{}, err
	}
	return version, nil
}

// Release 清掉自己的租约（这次没干成，下轮/别人可以再来）。不是自己拿的租约不碰。
func (s *Store) Release(ctx context.Context, infohash, owner string) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE task_version
		 SET claimed_at = NULL, claimed_by = '', updated_at = now()
		 WHERE infohash = $1 AND claimed_by = $2`,
		infohash, owner,
	); err != nil {
		return fmt.Errorf("task: 释放版本 %s 的租约: %w", infohash, err)
	}
	return nil
}

// MarkAdded 是「加种成功或已存在」的落库：清 add_pending、置清单项待删、清租约。
//
// 租约已被别人接手时返回 ErrClaimLost（说明这次的结果不能代表当前占用者）。
func (s *Store) MarkAdded(ctx context.Context, infohash, owner string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE task_version
		 SET add_pending = false, list_removal_pending = true,
		     claimed_at = NULL, claimed_by = '', updated_at = now()
		 WHERE infohash = $1 AND claimed_by = $2`,
		infohash, owner,
	)
	if err != nil {
		return fmt.Errorf("task: 标记版本 %s 已加种: %w", infohash, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrClaimLost
	}
	return nil
}

// AppendEvent 追加一条任务事件（append-only）。
func (s *Store) AppendEvent(ctx context.Context, event Event) error {
	if event.MovieID == "" {
		return errors.New("task: 事件缺 movie_id")
	}
	at := event.At
	if at.IsZero() {
		at = time.Now().UTC()
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO task_event (movie_id, infohash, kind, from_state, to_state, phase, reason, detail, at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		event.MovieID, nullIfEmpty(event.Infohash), string(event.Kind),
		nullIfEmpty(event.FromState), nullIfEmpty(event.ToState),
		nullIfEmpty(event.Phase), event.Reason, detailJSON(event.Detail), at,
	)
	if err != nil {
		return fmt.Errorf("task: 记事件: %w", err)
	}
	return nil
}

// List 返回面板任务表的一页：聚合状态 + 版本摘要（规格 §8.4）。
func (s *Store) List(ctx context.Context, filter ListFilter) ([]TaskRow, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	rows, err := s.pool.Query(ctx,
		`SELECT t.movie_id, t.number, t.list_id, COALESCE(ts.state, ''), t.created_at, t.updated_at
		 FROM task t
		 LEFT JOIN task_state ts ON ts.movie_id = t.movie_id
		 WHERE ($1 = '' OR ts.state = $1)
		   AND ($2 = '' OR t.movie_id ILIKE '%' || $2 || '%' ESCAPE '\' OR t.number ILIKE '%' || $2 || '%' ESCAPE '\')
		 ORDER BY t.created_at DESC, t.movie_id
		 LIMIT $3 OFFSET $4`,
		filter.State, escapeLike(strings.TrimSpace(filter.Query)), pageSize, (page-1)*pageSize,
	)
	if err != nil {
		return nil, fmt.Errorf("task: 列任务: %w", err)
	}
	defer rows.Close()

	tasks := []TaskRow{}
	for rows.Next() {
		var row TaskRow
		if err := rows.Scan(&row.MovieID, &row.Number, &row.ListID, &row.State, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("task: 扫任务行: %w", err)
		}
		tasks = append(tasks, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历任务行: %w", err)
	}

	return s.attachVersions(ctx, tasks)
}

// Count 返回与 List 同一筛选条件下的总条数。
//
// 面板靠它说「共 N 条」并关掉最后一页的「下一页」：只看「这一页是否满」的话，
// 恰好整页的最后一页会一直可点，点进去是空页。
func (s *Store) Count(ctx context.Context, filter ListFilter) (int, error) {
	var total int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*)
		 FROM task t
		 LEFT JOIN task_state ts ON ts.movie_id = t.movie_id
		 WHERE ($1 = '' OR ts.state = $1)
		   AND ($2 = '' OR t.movie_id ILIKE '%' || $2 || '%' ESCAPE '\' OR t.number ILIKE '%' || $2 || '%' ESCAPE '\')`,
		filter.State, escapeLike(strings.TrimSpace(filter.Query)),
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("task: 数任务: %w", err)
	}
	return total, nil
}

// Detail 返回一个任务的抽屉数据：任务 + 版本列表 + 最近事件（规格 §8.4）。
func (s *Store) Detail(ctx context.Context, movieID string, eventLimit int) (TaskRow, error) {
	var row TaskRow
	err := s.pool.QueryRow(ctx,
		`SELECT t.movie_id, t.number, t.list_id, COALESCE(ts.state, ''), t.created_at, t.updated_at
		 FROM task t
		 LEFT JOIN task_state ts ON ts.movie_id = t.movie_id
		 WHERE t.movie_id = $1`,
		movieID,
	).Scan(&row.MovieID, &row.Number, &row.ListID, &row.State, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TaskRow{}, ErrTaskNotFound
		}
		return TaskRow{}, fmt.Errorf("task: 读任务 %s: %w", movieID, err)
	}

	withVersions, err := s.attachVersions(ctx, []TaskRow{row})
	if err != nil {
		return TaskRow{}, err
	}
	withVersions[0].Events, err = s.Events(ctx, movieID, eventLimit)
	if err != nil {
		return TaskRow{}, err
	}
	return withVersions[0], nil
}

// Events 读一个任务的事件时间线，新的在前。
func (s *Store) Events(ctx context.Context, movieID string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, movie_id, COALESCE(infohash, ''), kind,
		        COALESCE(from_state, ''), COALESCE(to_state, ''), COALESCE(phase, ''), reason, detail, at
		 FROM task_event
		 WHERE movie_id = $1
		 ORDER BY id DESC
		 LIMIT $2`,
		movieID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("task: 读任务 %s 的事件: %w", movieID, err)
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		var (
			event  Event
			detail []byte
		)
		if err := rows.Scan(&event.ID, &event.MovieID, &event.Infohash, &event.Kind,
			&event.FromState, &event.ToState, &event.Phase, &event.Reason, &detail, &event.At); err != nil {
			return nil, fmt.Errorf("task: 扫事件: %w", err)
		}
		event.Detail = json.RawMessage(detail)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历事件: %w", err)
	}
	return events, nil
}

// Counts 按聚合状态数任务（面板统计行）。
//
// Failed 只数「全部版本都失败」的任务；一任务里部分版本失败属于「需要人工」区的
// 行，由 Attention 报，两者不在这儿相加。
func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var counts Counts
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE state = 'downloading'),
		        count(*) FILTER (WHERE state = 'processing'),
		        count(*) FILTER (WHERE state = 'finished'),
		        count(*) FILTER (WHERE state = 'failed')
		 FROM task_state`,
	).Scan(&counts.Downloading, &counts.Processing, &counts.Finished, &counts.Failed)
	if err != nil {
		return Counts{}, fmt.Errorf("task: 数任务状态: %w", err)
	}
	return counts, nil
}

// attachVersions 给一页任务补上各自的版本（一条查询取回整页，避免 N+1）。
func (s *Store) attachVersions(ctx context.Context, tasks []TaskRow) ([]TaskRow, error) {
	if len(tasks) == 0 {
		return tasks, nil
	}

	movieIDs := make([]string, 0, len(tasks))
	for _, row := range tasks {
		movieIDs = append(movieIDs, row.MovieID)
	}

	rows, err := s.pool.Query(ctx,
		`SELECT `+versionColumns+`
		 FROM task_version v
		 JOIN task t ON t.movie_id = v.movie_id
		 WHERE v.movie_id = ANY($1::text[])
		 ORDER BY v.created_at, v.infohash`,
		movieIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("task: 读版本摘要: %w", err)
	}
	defer rows.Close()

	byMovie := make(map[string][]Version, len(tasks))
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		byMovie[version.MovieID] = append(byMovie[version.MovieID], version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历版本摘要: %w", err)
	}

	for i := range tasks {
		versions := byMovie[tasks[i].MovieID]
		if versions == nil {
			versions = []Version{}
		}
		tasks[i].Versions = versions
	}
	return tasks, nil
}

// versionColumns 是 claim/version 读取共用的列序（与 scanVersion 一一对应）。
const versionColumns = `v.infohash, v.movie_id, t.number, v.source, v.delivery, v.label, v.status,
	COALESCE(v.error_phase, ''), v.error_reason, v.add_pending, v.list_removal_pending,
	v.attempts, v.force_rescrape, v.payload, v.created_at, v.updated_at`

// scanner 是 pgx.Row 与 pgx.Rows 共有的那一半。
type scanner interface {
	Scan(dest ...any) error
}

// scanVersion 扫一行版本；调用方负责把它包成业务错误。
func scanVersion(row scanner) (Version, error) {
	var version Version
	err := row.Scan(
		&version.Infohash, &version.MovieID, &version.Number, &version.Source,
		&version.Delivery, &version.Label, &version.Status, &version.ErrorPhase,
		&version.ErrorReason, &version.AddPending, &version.ListRemovalPending,
		&version.Attempts, &version.ForceRescrape, &version.Payload, &version.CreatedAt, &version.UpdatedAt,
	)
	if err != nil {
		return Version{}, err
	}
	return version, nil
}

// discoveredReason 是版本创建事件的措辞：两条定源路径分开说，时间线才读得懂。
func discoveredReason(source Source) string {
	if source == SourceMTeamTorrent {
		return "发现 M-Team 种子"
	}
	return "发现磁力候选"
}

func validateCandidate(candidate Candidate) error {
	switch {
	case strings.TrimSpace(candidate.MovieID) == "":
		return errors.New("task: 候选缺 movie_id")
	case strings.TrimSpace(candidate.Number) == "":
		return errors.New("task: 候选缺番号")
	case strings.TrimSpace(candidate.ListID) == "":
		return errors.New("task: 候选缺清单 id")
	case strings.TrimSpace(candidate.Infohash) == "":
		return errors.New("task: 候选缺 infohash")
	case candidate.Source != SourceJavDBMagnet && candidate.Source != SourceMTeamTorrent:
		return fmt.Errorf("task: 未知来源 %q", candidate.Source)
	case candidate.Delivery != DeliveryMagnet && candidate.Delivery != DeliveryTorrent:
		return fmt.Errorf("task: 未知交付形态 %q", candidate.Delivery)
	}
	return nil
}

// escapeLike 把用户搜索里的 LIKE 元字符转义成字面量：输入 "%" 应该找百分号，
// 而不是匹配全部（规格 §8.4 只说 q 是搜索词，没说它是模式）。
func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func leaseSeconds() float64 {
	return LeaseDuration.Seconds()
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// detailJSON 把事件附注落成 jsonb：空值写成 {}，读回来永远是一份合法 JSON。
func detailJSON(detail json.RawMessage) json.RawMessage {
	if len(detail) == 0 || !json.Valid(detail) {
		return json.RawMessage(`{}`)
	}
	return detail
}
