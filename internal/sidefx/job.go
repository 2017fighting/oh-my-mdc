package sidefx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrNotFound 说明这条待办不存在，或者已经完成了（规格 §8.4 人工重发的 404）。
var ErrNotFound = errors.New("sidefx: 没有这条待办")

// State 是 GET /api/side-effects 的两个筛选项（规格 §8.4）。
type State string

const (
	// StatePending 是排队中：done_at IS NULL AND next_attempt_at IS NOT NULL。
	StatePending State = "pending"
	// StateStuck 是驻留（需人工）：done_at IS NULL AND next_attempt_at IS NULL。
	StateStuck State = "stuck"
)

// ValidState 报告一个 ?state= 的值是否合法（空串合法，表示不筛）。
func ValidState(state string) bool {
	return state == "" || state == string(StatePending) || state == string(StateStuck)
}

// Job 是一条待办的快照：执行器看它、面板看它、人工重发也认它的 id。
type Job struct {
	ID        int64
	Kind      Kind
	TargetKey string
	// Payload 是这一条待办的上下文原文（通知模板的番号/标签，删除的清单 id 与 movie_id）。
	Payload json.RawMessage
	// Attempts 是已经尝试过的次数（执行器每次落库 +1）。
	Attempts  int
	LastError string
	// NextAttemptAt 为 nil 就是驻留：需人工（规格 §4.2）。
	NextAttemptAt *time.Time
	DoneAt        *time.Time
	// CreatedAt 是入箱时间（#面板「驻留副作用」的年龄）。2026-11 之前的行没有这一列，
	// 读出来是 nil：不知道就不说，不拿迁移时刻冒充。
	CreatedAt *time.Time
}

// Stuck 报告这条待办是否驻留（需人工）。
func (j Job) Stuck() bool {
	return j.DoneAt == nil && j.NextAttemptAt == nil
}

// inserted 是入箱共用的那一句（Enqueue 与 DrainPendingRemovals 都走它）。
//
// ON CONFLICT DO NOTHING 落在部分唯一索引上：同 (kind,target_key) 已有一条未完成
// 待办时静默并掉——同一次删除/刷新/通知不该排队两条。
const inserted = `INSERT INTO side_effect (kind, target_key, payload, next_attempt_at)
	VALUES ($1, $2, $3, now())
	ON CONFLICT DO NOTHING`

// execer 是连接池与事务共有的那一小片：入箱只用到 Exec。
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// insertJob 写一条待办（入箱的唯一实现：排序、清理 target_key、编码 payload 都在这里）。
func insertJob(ctx context.Context, exec execer, kind Kind, targetKey string, payload any) error {
	targetKey = strings.TrimSpace(targetKey)
	if targetKey == "" {
		return errors.New("sidefx: 待办缺 target_key")
	}

	encoded, err := marshalPayload(payload)
	if err != nil {
		return err
	}
	if _, err := exec.Exec(ctx, inserted, string(kind), targetKey, encoded); err != nil {
		return fmt.Errorf("sidefx: 入箱 %s %s: %w", kind, targetKey, err)
	}
	return nil
}

// jobColumns 是 List/Due/Get 共用的列序（与 scanJob 一一对应）。
const jobColumns = `id, kind, target_key, payload, attempts, last_error, next_attempt_at, done_at, created_at`

// orderUnfinished 让驻留排在前面（面板先看到要动手的事），其余按下次尝试时间。
const orderUnfinished = `ORDER BY (next_attempt_at IS NOT NULL), next_attempt_at, id`

// Due 列出到点的待办（done_at IS NULL AND next_attempt_at <= now()）。
//
// 顺序按 next_attempt_at、id：先来先做，同一秒内按入箱顺序。
func (o *Outbox) Due(ctx context.Context, limit int) ([]Job, error) {
	return o.query(ctx,
		`SELECT `+jobColumns+`
		 FROM side_effect
		 WHERE done_at IS NULL AND next_attempt_at IS NOT NULL AND next_attempt_at <= now()
		 ORDER BY next_attempt_at, id
		 LIMIT $1`, limitOf(limit))
}

// List 按状态列出待办（不传状态＝全部未完成的，驻留在前）。
func (o *Outbox) List(ctx context.Context, state State, limit int) ([]Job, error) {
	return o.query(ctx,
		`SELECT `+jobColumns+`
		 FROM side_effect
		 WHERE done_at IS NULL
		   AND ($2 = ''
		        OR ($2 = 'pending' AND next_attempt_at IS NOT NULL)
		        OR ($2 = 'stuck' AND next_attempt_at IS NULL))
		 `+orderUnfinished+`
		 LIMIT $1`,
		limitOf(limit), string(state))
}

// Get 读一条待办（重发之后回给面板的那一行）。
func (o *Outbox) Get(ctx context.Context, id int64) (Job, error) {
	job, err := scanJob(o.pool.QueryRow(ctx,
		`SELECT `+jobColumns+` FROM side_effect WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, ErrNotFound
		}
		return Job{}, fmt.Errorf("sidefx: 读待办 %d: %w", id, err)
	}
	return job, nil
}

// MarkDone 记一次成功执行：done_at 落定、last_error 清空。
//
// 部分唯一索引只在 done_at IS NULL 上生效，所以完成之后同 target_key 可以再入箱
// （同片再进清单可再删、同事件再来可再发，规格 §4.2）。
func (o *Outbox) MarkDone(ctx context.Context, id int64) error {
	tag, err := o.pool.Exec(ctx,
		`UPDATE side_effect
		 SET done_at = now(), attempts = attempts + 1, last_error = ''
		 WHERE id = $1 AND done_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("sidefx: 标记待办 %d 完成: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Reschedule 记一次失败并排下一次尝试：attempts+1、last_error 落原因、按 delay 退避。
func (o *Outbox) Reschedule(ctx context.Context, id int64, lastError string, delay time.Duration) error {
	if delay < 0 {
		delay = 0
	}
	tag, err := o.pool.Exec(ctx,
		`UPDATE side_effect
		 SET attempts = attempts + 1, last_error = $2,
		     next_attempt_at = now() + make_interval(secs => $3::double precision)
		 WHERE id = $1 AND done_at IS NULL`, id, lastError, delay.Seconds())
	if err != nil {
		return fmt.Errorf("sidefx: 重排待办 %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Stick 让一条待办驻留等人工：next_attempt_at = NULL（规格 §4.2 的驻留定义）。
//
// 它意味着「自动重试到此为止」，不等于放弃：面板「需要人工」区会出现这一行，
// 逐条「重发」会把它重新排上（Retry）。绝不自动禁用集成（P12-3）。
func (o *Outbox) Stick(ctx context.Context, id int64, lastError string) error {
	tag, err := o.pool.Exec(ctx,
		`UPDATE side_effect
		 SET attempts = attempts + 1, last_error = $2, next_attempt_at = NULL
		 WHERE id = $1 AND done_at IS NULL`, id, lastError)
	if err != nil {
		return fmt.Errorf("sidefx: 驻留待办 %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Retry 是人工重发（规格 §8.4 POST /api/side-effects/{id}/retry）：清驻留、重排。
//
// 同时把 attempts 归零：自动重试的预算属于「没人管的时候」，人已经介入了，
// 就该按一份新预算再走一遍。last_error 保留到下一次成功/失败再覆盖——
// 面板在重排之后还该说得清「上次为什么没成」。
func (o *Outbox) Retry(ctx context.Context, id int64) error {
	tag, err := o.pool.Exec(ctx,
		`UPDATE side_effect
		 SET next_attempt_at = now(), attempts = 0
		 WHERE id = $1 AND done_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("sidefx: 人工重发待办 %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DrainPendingRemovals 把「清单项待删」的版本翻成待办（规格 §6.1 置待删 / §4.2）。
//
// 顺序不变式：版本上的 list_removal_pending 只在**加下载成功之后**才被置起
// （task.Store.MarkAdded），所以这里入箱的每一条都恒在加种之后（P7-1/P7-4）。
//
// 入箱与清旗在同一个事务里：崩在两步之间不会留下「旗还在但活没了」，也不会反过来
// 把一条还没入箱的删除漏掉。同 target_key 已有未完成待办时 insertJob 会把它并掉、
// 旗照清：同一个清单项的删除本就幂等，不该排两条。
func (o *Outbox) DrainPendingRemovals(ctx context.Context, limit int) (int, error) {
	limit = limitOf(limit)

	tx, err := o.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("sidefx: 开事务: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	rows, err := tx.Query(ctx,
		`SELECT v.infohash, v.movie_id, t.number, t.list_id
		 FROM task_version v
		 JOIN task t ON t.movie_id = v.movie_id
		 WHERE v.list_removal_pending
		 ORDER BY v.created_at, v.infohash
		 LIMIT $1
		 FOR UPDATE OF v SKIP LOCKED`, limit)
	if err != nil {
		return 0, fmt.Errorf("sidefx: 列待删清单项的版本: %w", err)
	}

	type pendingRemoval struct {
		infohash string
		movieID  string
		number   string
		listID   string
	}
	due := []pendingRemoval{}
	for rows.Next() {
		var item pendingRemoval
		if err := rows.Scan(&item.infohash, &item.movieID, &item.number, &item.listID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("sidefx: 扫待删清单项的版本: %w", err)
		}
		due = append(due, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("sidefx: 遍历待删清单项的版本: %w", err)
	}
	if len(due) == 0 {
		return 0, nil
	}

	hashes := make([]string, 0, len(due))
	for _, item := range due {
		payload := map[string]any{
			"movie_id": item.movieID,
			"list_id":  item.listID,
			"number":   item.number,
		}
		if err := insertJob(ctx, tx, KindListRemoval, item.listID+":"+item.movieID, payload); err != nil {
			return 0, err
		}
		hashes = append(hashes, item.infohash)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE task_version SET list_removal_pending = false, updated_at = now()
		 WHERE infohash = ANY($1::text[])`, hashes,
	); err != nil {
		return 0, fmt.Errorf("sidefx: 清待删标记: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("sidefx: 提交待删入箱: %w", err)
	}
	return len(due), nil
}

// query 跑一条按 jobColumns 选取的语句。
func (o *Outbox) query(ctx context.Context, sql string, args ...any) ([]Job, error) {
	rows, err := o.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("sidefx: 列待办: %w", err)
	}
	defer rows.Close()

	jobs := []Job{}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("sidefx: 扫待办: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sidefx: 遍历待办: %w", err)
	}
	return jobs, nil
}

// scanner 是 pgx.Row 与 pgx.Rows 共有的那一半。
type scanner interface {
	Scan(dest ...any) error
}

// scanJob 扫一行 side_effect；可空时间列用 pgtype 接，再翻成 *time.Time。
func scanJob(row scanner) (Job, error) {
	var (
		job       Job
		kind      string
		payload   []byte
		nextAt    pgtype.Timestamptz
		doneAt    pgtype.Timestamptz
		createdAt pgtype.Timestamptz
	)
	if err := row.Scan(&job.ID, &kind, &job.TargetKey, &payload,
		&job.Attempts, &job.LastError, &nextAt, &doneAt, &createdAt); err != nil {
		return Job{}, err
	}
	job.Kind = Kind(strings.TrimSpace(kind))
	job.Payload = payload
	job.NextAttemptAt = timeOf(nextAt)
	job.DoneAt = timeOf(doneAt)
	job.CreatedAt = timeOf(createdAt)
	return job, nil
}

func timeOf(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time.UTC()
	return &at
}

// limitOf 给列表查询一个统一上限：0/负数当默认值，过大按上限截。
func limitOf(limit int) int {
	const (
		defaultLimit = 200
		maxLimit     = 500
	)
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}
