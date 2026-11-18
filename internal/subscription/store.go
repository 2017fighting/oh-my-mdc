// Package subscription 是女优订阅（规格 §6.4，ADR-0014/0015）：第二个待办来源。
//
// 它只负责「产出一批待办作品」——取数窗口（水位线 + 回看窗口 + 回灌下界）与名单（哪些
// 女优被订了、停用了没有），之后每一件作品往下一律走 internal/ingest（定源 → 先落库 →
// 加种），与清单轮询同一段。所以本包不含磁力、M-Team、qBittorrent 的任何决定：
// 那些写两遍必然漂移（ADR-0014 的后果一节）。
//
// 三个部分：
//
//   - Store：subscription / actor 两张表，加上「带来 N 部」要的计数；
//   - Service：面板上的四个动作（添加、启停、回灌、列表）——贴 id 或女优页链接 →
//     问上游「这个 id 是谁」→ upsert 一行 actor → 建订阅。门禁与手工登记同一口径；
//   - Runner：第四个循环（独立 advisory lock 4230004、独立节拍）与它的取数窗口。
//
// 行永不删除（面板只有停用/恢复）：task.subscription_id 的外键与历史因此永远可解。
package subscription

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 订阅的两种结局：新建与幂等放行（同一位女优第二次添加）。
const (
	OutcomeCreated   = "created"
	OutcomeDuplicate = "duplicate"
)

// KindActress 是当下唯一的订阅种类（§4.5 的扩展位：将来到番号/全站订阅加在这里）。
const KindActress = "actress"

// ErrNotFound 说明请求里的订阅 id 不存在（HTTP 404）。
var ErrNotFound = errors.New("subscription: 订阅不存在")

// Actor 是一位女优在上游的身份与名字快照。
//
// 身份是 id，不是名字（名字只是快照，改名不该断订阅）。Gender/AvatarURL 建订阅时是空的
// ——它们等刮削随作品数据补上（规格 §4.5「actor 词义扩宽」）。
type Actor struct {
	ID        string
	Name      string
	Gender    string
	AvatarURL string
}

// Row 是一条订阅（面板一行）。
type Row struct {
	ID    int64
	Kind  string
	Actor Actor
	// WatermarkDate 是上一轮见过的最晚发行日；NULL 时读方按「建订阅当天」兜底。
	WatermarkDate *time.Time
	LookbackDays  int
	// BackfillFrom 是回灌设置的取数下界；窗口扫完清 NULL。
	BackfillFrom *time.Time
	Enabled      bool
	// TasksCount 数 task.subscription_id：「带来 N 部」。
	TasksCount int
	// PendingCount 数这条订阅建的任务里还有版本在等加种（add_pending）的条数。
	//
	// 它是「这批还卡在门口」的现数，不是「本轮被限量留在门外」的数——每轮限量归 #49，
	// 那一票会把后者加进来（§9 的 mdc_subscription_pending）。
	PendingCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Store 是订阅域的读写门面：两件事，一条订阅一行、一位女优一名演员。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用连接池装配订阅域。
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// rowColumns 是 Row 的选取清单，与 scanRow 逐一对应。
//
// 演员的名字/性别/头像用 COALESCE：它们是 NULL，不该让整行读不出来。
const rowColumns = `s.id, s.kind, s.actress_actor_id, s.watermark_date, s.lookback_days,
	s.backfill_from, s.enabled, s.created_at, s.updated_at,
	COALESCE(a.name, ''), COALESCE(a.gender, ''), COALESCE(a.avatar_url, ''),
	(SELECT count(*) FROM task t WHERE t.subscription_id = s.id),
	(SELECT count(*) FROM task t JOIN task_version v ON v.movie_id = t.movie_id
	 WHERE t.subscription_id = s.id AND v.add_pending)`

// List 读全部订阅（面板订阅区与循环共用；循环自己按 enabled 过滤）。
func (s *Store) List(ctx context.Context) ([]Row, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+rowColumns+`
		 FROM subscription s
		 JOIN actor a ON a.javdb_actor_id = s.actress_actor_id
		 ORDER BY s.id`)
	if err != nil {
		return nil, fmt.Errorf("subscription: 列订阅: %w", err)
	}
	defer rows.Close()

	out := []Row{}
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("subscription: 扫订阅: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscription: 遍历订阅: %w", err)
	}
	return out, nil
}

// Get 读一条订阅。
func (s *Store) Get(ctx context.Context, id int64) (Row, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+rowColumns+`
		 FROM subscription s
		 JOIN actor a ON a.javdb_actor_id = s.actress_actor_id
		 WHERE s.id = $1`, id)
	out, err := scanRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Row{}, ErrNotFound
		}
		return Row{}, fmt.Errorf("subscription: 读订阅 %d: %w", id, err)
	}
	return out, nil
}

// Create 落一位女优的演员行并建订阅，返回这一行与结局。
//
// 顺序是「先 actor、后 subscription」：subscription.actress_actor_id 的外键要求演员行
// 已经在（§4.5：外键立得起来的前提是建订阅时 upsert 一行 actor）。
//
// 幂等靠 UNIQUE (kind, actress_actor_id) 的 DO NOTHING：第二次添加既不建第二行，
// 也不重置水位线（重复添加不是「重新开始」）。
func (s *Store) Create(ctx context.Context, actor Actor) (Row, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Row{}, "", fmt.Errorf("subscription: 开事务: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// 名字用上游的快照覆盖，但**只在上游给了名字时**：空名字不该把已知的名字抹掉。
	// 性别与头像一概不动——那是刮削的产出，订阅这条路径不知道也不该猜（§4.5）。
	if _, err := tx.Exec(ctx,
		`INSERT INTO actor (javdb_actor_id, name, updated_at) VALUES ($1, $2, now())
		 ON CONFLICT (javdb_actor_id) DO UPDATE
		 SET name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE actor.name END,
		     updated_at = now()`,
		actor.ID, actor.Name,
	); err != nil {
		return Row{}, "", fmt.Errorf("subscription: 落演员 %s: %w", actor.ID, err)
	}

	// 水位线 = 建订阅当天：新订阅不回灌历史，否则「通没通」要等上游真出片才知道。
	inserted, err := tx.Exec(ctx,
		`INSERT INTO subscription (kind, actress_actor_id, watermark_date) VALUES ($1, $2, CURRENT_DATE)
		 ON CONFLICT (kind, actress_actor_id) DO NOTHING`,
		KindActress, actor.ID,
	)
	if err != nil {
		return Row{}, "", fmt.Errorf("subscription: 建订阅 %s: %w", actor.ID, err)
	}
	outcome := OutcomeCreated
	if inserted.RowsAffected() == 0 {
		outcome = OutcomeDuplicate
	}

	// 在同一个事务里读回来：面板要的那一行（含计数）与刚写进去的必须是同一份事实。
	row, err := scanRow(tx.QueryRow(ctx,
		`SELECT `+rowColumns+`
		 FROM subscription s
		 JOIN actor a ON a.javdb_actor_id = s.actress_actor_id
		 WHERE s.kind = $1 AND s.actress_actor_id = $2`,
		KindActress, actor.ID))
	if err != nil {
		return Row{}, "", fmt.Errorf("subscription: 读回订阅 %s: %w", actor.ID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Row{}, "", fmt.Errorf("subscription: 提交: %w", err)
	}
	return row, outcome, nil
}

// SetEnabled 启停一条订阅。停用只停追新：任务、水位线、跳过行一概不碰（§6.4 生命周期）。
func (s *Store) SetEnabled(ctx context.Context, id int64, enabled bool) (Row, error) {
	if _, err := s.pool.Exec(ctx,
		`UPDATE subscription SET enabled = $2, updated_at = now() WHERE id = $1`, id, enabled,
	); err != nil {
		return Row{}, fmt.Errorf("subscription: 改订阅 %d 的启停: %w", id, err)
	}
	return s.Get(ctx, id)
}

// SetBackfill 把取数下界往回拨 days 天（§6.4 回灌）。
//
// 下界 = watermark_date − days，水位线本身不动：窗口扫完由循环清 NULL，
// 所以「回灌 30 天」不会被一次水位线推进立刻吃掉。
func (s *Store) SetBackfill(ctx context.Context, id int64, days int) (Row, error) {
	if _, err := s.pool.Exec(ctx,
		`UPDATE subscription
		 SET backfill_from = COALESCE(watermark_date, CURRENT_DATE) - $2::int, updated_at = now()
		 WHERE id = $1`, id, days,
	); err != nil {
		return Row{}, fmt.Errorf("subscription: 设订阅 %d 的回灌窗口: %w", id, err)
	}
	return s.Get(ctx, id)
}

// Seen 记一轮的结果：水位线推成「这轮见过的最晚发行日」，扫完才清回灌下界。
//
// 水位线只由「见过」推进（不是「入队成功」）：出水即无磁力的作品靠回看窗口下轮重见，
// 与 §6.1「磁力为空不落行、下轮重查」是同一条承诺。
//
// GREATEST 是「只增不减」：新订阅的水位线是建订阅当天，而窗口里见到的作品总比今天老，
// 因此头几轮它推不动——正是这一点让「不点回灌就不会回灌历史」成立。会往回退的水位线
// 等于把一位退休女优三年前的作品当场回灌进来（那正是「回灌」要人按一下的原因）。
//
// clearBackfill 只在回灌窗口**真的扫完**时为 true：中途读上游失败就留着，
// 下一轮接着从同一个下界开始（规格 §6.4「余量留到下一轮且 backfill_from 不被清」）。
func (s *Store) Seen(ctx context.Context, id int64, seen time.Time, clearBackfill bool) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE subscription
		 SET watermark_date = GREATEST(COALESCE(watermark_date, $2::date), $2::date),
		     backfill_from = CASE WHEN $3 THEN NULL ELSE backfill_from END,
		     updated_at = now()
		 WHERE id = $1`,
		id, seen.Format("2006-01-02"), clearBackfill,
	); err != nil {
		return fmt.Errorf("subscription: 推进订阅 %d 的水位线: %w", id, err)
	}
	return nil
}

// scanner 是 pgx.Row 与 pgx.Rows 共有的那一半。
type scanner interface {
	Scan(dest ...any) error
}

// scanRow 扫一行订阅；调用方负责把它包成业务错误。
func scanRow(row scanner) (Row, error) {
	var (
		out       Row
		watermark *time.Time
		backfill  *time.Time
	)
	err := row.Scan(
		&out.ID, &out.Kind, &out.Actor.ID, &watermark, &out.LookbackDays,
		&backfill, &out.Enabled, &out.CreatedAt, &out.UpdatedAt,
		&out.Actor.Name, &out.Actor.Gender, &out.Actor.AvatarURL,
		&out.TasksCount, &out.PendingCount,
	)
	if err != nil {
		return Row{}, err
	}
	out.WatermarkDate = dateOnly(watermark)
	out.BackfillFrom = dateOnly(backfill)
	return out, nil
}

// dateOnly 把 date 列的时间戳截到日（库给的是当地零点的 timestamptz，日才是语义）。
func dateOnly(at *time.Time) *time.Time {
	if at == nil {
		return nil
	}
	truncated := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	return &truncated
}
