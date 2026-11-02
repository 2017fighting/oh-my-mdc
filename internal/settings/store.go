package settings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUnknownSection 说明 URL 里的 {section} 不是设置域的一节（HTTP 404）。
var ErrUnknownSection = errors.New("settings: 未知的 section")

// ErrNotDeletable 说明这一节不能「解除配置」：app_config 恒有一行。
var ErrNotDeletable = errors.New("settings: 这一节不可解除配置")

// Store 是设置域的读写门面。它只认一个连接池：写路径要用事务里的行锁，
// 假装成几个零散查询会把「读-校验-写」拆成能互相踩的几步。
type Store struct {
	pool        *pgxpool.Pool
	testers     map[string]Tester
	testTimeout time.Duration
}

// NewStore 用连接池装配设置域，并给每张集成表挂上占位测试实现。
//
// 真实测试逻辑由集成票用 WithTester 逐节接上；在它们落地前，测试按钮回
// not_implemented 而不是假装成功。
func NewStore(pool *pgxpool.Pool, options ...Option) *Store {
	testers := make(map[string]Tester, len(sections))
	for _, section := range sections {
		if section.Kind == KindIntegration {
			testers[section.Name] = TesterFunc(placeholderTester)
		}
	}

	store := &Store{pool: pool, testers: testers, testTimeout: TestTimeout}
	for _, option := range options {
		option(store)
	}
	return store
}

// Snapshot 是全部设置的一次读。
type Snapshot struct {
	// Sections 按 name 索引，包含全部八节。
	Sections map[string]SectionState
	// Initialized 是「主线三件套齐了」的判据（规格 §4.4）。
	Initialized bool
	// Paused 是 app_config.paused：循环暂停开关。
	Paused bool
}

// Load 在一个只读事务里读全八节，保证 initialized 与各节互相对得上。
func (s *Store) Load(ctx context.Context) (Snapshot, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Snapshot{}, fmt.Errorf("settings: 开只读事务: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	snapshot := Snapshot{Sections: make(map[string]SectionState, len(sections))}
	for _, sec := range sections {
		loaded, err := loadRow(ctx, tx, sec, false)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Sections[sec.Name] = loaded.state(sec)
		if sec.Kind == KindApp {
			snapshot.Paused, _ = loaded.values["paused"].(bool)
		}
	}
	snapshot.Initialized = isInitialized(snapshot.Sections)

	if err := tx.Commit(ctx); err != nil {
		return Snapshot{}, fmt.Errorf("settings: 提交只读事务: %w", err)
	}
	return snapshot, nil
}

// Section 读一节（GET /api/config/{section}）。
func (s *Store) Section(ctx context.Context, name string) (SectionState, error) {
	sec, ok := Lookup(name)
	if !ok {
		return SectionState{}, ErrUnknownSection
	}
	r, err := loadRow(ctx, s.pool, sec, false)
	if err != nil {
		return SectionState{}, err
	}
	return r.state(sec), nil
}

// Patch 保存一节的部分字段（PATCH /api/config/{section}）。
//
// 在一个事务里「读-校验-revision 比对-写」，回读后返回新的对外形态。
func (s *Store) Patch(ctx context.Context, name string, patch Patch) (SectionState, error) {
	sec, ok := Lookup(name)
	if !ok {
		return SectionState{}, ErrUnknownSection
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SectionState{}, fmt.Errorf("settings: 开事务: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	current, err := loadRow(ctx, tx, sec, true)
	if err != nil {
		return SectionState{}, err
	}
	// revision 是必填的并发编辑凭据（规格 §8.4）：省掉它就等于关掉两个标签页的保护。
	// 只在真的要改东西时检查——空补丁不该因为没带指纹就报冲突。
	if len(patch.Values) > 0 && patch.Revision != current.revision(sec) {
		return SectionState{}, RevisionConflictError{Current: current.revision(sec)}
	}
	if err := validateRequired(sec, current, patch); err != nil {
		return SectionState{}, err
	}

	var saveErr error
	if sec.Kind == KindLists {
		saveErr = saveList(ctx, tx, sec, patch)
	} else {
		saveErr = saveSingle(ctx, tx, sec, patch)
	}
	if saveErr != nil {
		return SectionState{}, saveErr
	}

	updated, err := loadRow(ctx, tx, sec, false)
	if err != nil {
		return SectionState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SectionState{}, fmt.Errorf("settings: 提交事务: %w", err)
	}
	return updated.state(sec), nil
}

// Delete 解除一节的配置（DELETE /api/config/{section}）。
//
// 主线三件套被删掉后 initialized 立刻变 false：下一次 Read 就能看到（规格 §8.4）。
func (s *Store) Delete(ctx context.Context, name string) (SectionState, error) {
	sec, ok := Lookup(name)
	if !ok {
		return SectionState{}, ErrUnknownSection
	}
	if sec.Kind == KindApp {
		return SectionState{}, ErrNotDeletable
	}

	var err error
	if sec.Kind == KindLists {
		_, err = s.pool.Exec(ctx, "DELETE FROM list_registry")
	} else {
		_, err = s.pool.Exec(ctx, "DELETE FROM "+sec.Table()+" WHERE id = 1")
	}
	if err != nil {
		return SectionState{}, fmt.Errorf("settings: 解除 %s 配置: %w", name, err)
	}

	return s.Section(ctx, name)
}

// RecordTest 把一次真实测试的结果写进 last_test_*。
func (s *Store) RecordTest(ctx context.Context, name string, result TestResult) error {
	sec, ok := Lookup(name)
	if !ok {
		return ErrUnknownSection
	}
	if sec.Kind != KindIntegration {
		return ErrNotTestable
	}

	errorText := ""
	if !result.OK {
		errorText = result.Detail
		if result.Code != "" {
			errorText = string(result.Code) + ": " + result.Detail
		}
	}
	if _, err := s.pool.Exec(ctx,
		"UPDATE "+sec.Table()+" SET last_test_at = now(), last_test_ok = $1, last_test_error = $2 WHERE id = 1",
		result.OK, nullIfEmpty(errorText),
	); err != nil {
		return fmt.Errorf("settings: 记录 %s 测试结果: %w", name, err)
	}
	return nil
}

// isInitialized 实现规格 §4.4 的门禁判据：主线三件套都配置了就位。
//
// 只看「行在不在」，不看 last_test_*：测试是诊断而非门禁——上游某次抖动不该把
// 已经跑起来的系统踢回向导。凭据是否还有效由集成自己的告警指标说话（规格 §9）。
func isInitialized(states map[string]SectionState) bool {
	for _, name := range []string{"javdb", "qbittorrent", "jellyfin"} {
		if !states[name].Configured {
			return false
		}
	}
	entries, _ := states["list_registry"].Data["entries"].([]ListEntry)
	for _, entry := range entries {
		if entry.Purpose == "want_watch" {
			return true
		}
	}
	return false
}

// querier 是连接池与事务共有的那一小片面：本包只用得上这三句。
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// loadRow 读一节。lock 为真时给该节的行上锁（写路径的读-校验-写靠它串行化）。
func loadRow(ctx context.Context, q querier, sec Section, lock bool) (row, error) {
	if sec.Kind == KindLists {
		return loadLists(ctx, q, lock)
	}
	return loadSingle(ctx, q, sec, lock)
}

func loadSingle(ctx context.Context, q querier, sec Section, lock bool) (row, error) {
	values := make(map[string]any, len(sec.Fields))
	columns := make([]string, 0, len(sec.Fields)+3)
	holders := make([]any, 0, len(sec.Fields)+3)
	assigners := make([]func(), 0, len(sec.Fields))

	for _, field := range sec.Fields {
		codec := codecOf(field)
		columns = append(columns, codec.selectExpr(field.Name))
		target, assign := codec.scan()
		holders = append(holders, target)
		assigners = append(assigners, func() { assign(values, field.Name) })
	}

	// 只有集成表带 last_test_*；app_config 的时间戳是程序自己写的 updated_at，不读。
	var (
		testedAt  pgtype.Timestamptz
		testedOK  pgtype.Bool
		testError pgtype.Text
	)
	if sec.Kind == KindIntegration {
		columns = append(columns, "last_test_at", "last_test_ok", "last_test_error")
		holders = append(holders, &testedAt, &testedOK, &testError)
	}

	sql := "SELECT " + strings.Join(columns, ", ") + " FROM " + sec.Table() + " WHERE id = 1"
	if lock {
		sql += " FOR UPDATE"
	}

	if err := q.QueryRow(ctx, sql).Scan(holders...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return row{values: map[string]any{}}, nil
		}
		return row{}, fmt.Errorf("settings: 读 %s: %w", sec.Name, err)
	}
	for _, assign := range assigners {
		assign()
	}

	loaded := row{configured: true, values: values}
	if sec.Kind == KindIntegration {
		loaded.lastTest = lastTestOf(testedAt, testedOK, testError)
	}
	return loaded, nil
}

func loadLists(ctx context.Context, q querier, lock bool) (row, error) {
	sql := "SELECT list_id, purpose, name FROM list_registry ORDER BY purpose"
	if lock {
		sql += " FOR UPDATE"
	}
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return row{}, fmt.Errorf("settings: 读 list_registry: %w", err)
	}
	defer rows.Close()

	entries := []ListEntry{}
	for rows.Next() {
		var entry ListEntry
		if err := rows.Scan(&entry.ListID, &entry.Purpose, &entry.Name); err != nil {
			return row{}, fmt.Errorf("settings: 扫 list_registry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return row{}, fmt.Errorf("settings: 遍历 list_registry: %w", err)
	}

	return row{configured: len(entries) > 0, values: map[string]any{}, lists: entries}, nil
}

func saveSingle(ctx context.Context, q querier, sec Section, patch Patch) error {
	if len(patch.Values) == 0 {
		// 没有任何要改的字段：不写库，也不把「行存在」当成用户意图。
		return nil
	}

	columns := []string{"id"}
	values := []any{1}
	bound := make([]boundColumn, 0, len(patch.Values))
	updates := make([]string, 0, len(patch.Values))

	for _, field := range sec.Fields {
		value, ok := patch.Values[field.Name]
		if !ok {
			continue
		}
		codec := codecOf(field)
		position := len(values) + 1
		columns = append(columns, field.Name)
		values = append(values, codec.bind(value))
		bound = append(bound, boundColumn{field: field, position: position})
		updates = append(updates, field.Name+" = "+codec.writeExpr(position))
	}

	// VALUES 里也要走 writeExpr：interval 列要是裸 $n，Postgres 会把这个参数判成
	// interval，从而让 UPDATE 那边同一个占位符的 ::double precision 报类型错。
	placeholders := make([]string, 0, len(values))
	placeholders = append(placeholders, "$1")
	for _, column := range bound {
		placeholders = append(placeholders, codecOf(column.field).writeExpr(column.position))
	}

	sql := "INSERT INTO " + sec.Table() + " (" + strings.Join(columns, ", ") + ") VALUES (" +
		strings.Join(placeholders, ", ") + ") ON CONFLICT (id) DO UPDATE SET " + strings.Join(updates, ", ")
	if sec.Kind == KindApp {
		sql += ", updated_at = now()"
	}
	if _, err := q.Exec(ctx, sql, values...); err != nil {
		return fmt.Errorf("settings: 保存 %s: %w", sec.Name, err)
	}
	return nil
}

// boundColumn 记一个绑定参数在语句里的位置，供 VALUES 与 UPDATE 两处共用。
type boundColumn struct {
	field    Field
	position int
}

// saveList 把 {list_id, purpose, name} upsert 进 list_registry。
//
// 一个用途只许占一张清单（唯一索引）；换清单要先 DELETE 旧的，这里给的是能读懂的
// 冲突文案而不是 Postgres 的约束名。list_id 是字符串（JavDB 的 6 字符 nanoid）。
func saveList(ctx context.Context, q querier, sec Section, patch Patch) error {
	listID, _ := patch.Values["list_id"].(string)
	listID = strings.TrimSpace(listID)
	purpose, _ := patch.Values["purpose"].(string)
	if listID == "" || purpose == "" {
		// 两个都没给：和 saveSingle 一样，不写库。
		return nil
	}

	var holder string
	err := q.QueryRow(ctx,
		"SELECT list_id FROM list_registry WHERE purpose = $1 AND list_id <> $2", purpose, listID).Scan(&holder)
	switch {
	case err == nil:
		return ValidationError{
			Field:   "purpose",
			Message: fmt.Sprintf("%s 已登记清单 %s，先解除再换", purpose, holder),
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("settings: 查 %s 用途占用: %w", purpose, err)
	}

	// 同一个 id 换用途也是换清单：ON CONFLICT 直接改 purpose 会把另一用途的登记
	// 静默顶掉（丢一行）。这里先查这张 id 现在是不是挂在**别的**用途上。
	var existingPurpose string
	err = q.QueryRow(ctx, "SELECT purpose FROM list_registry WHERE list_id = $1", listID).Scan(&existingPurpose)
	switch {
	case err == nil:
		if existingPurpose != purpose {
			return ValidationError{
				Field:   "purpose",
				Message: fmt.Sprintf("清单 %s 已登记为 %s，先解除再换用途", listID, existingPurpose),
			}
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("settings: 查清单 %s 现有用途: %w", listID, err)
	}

	columns := []string{"list_id", "purpose"}
	values := []any{listID, purpose}
	set := "purpose = EXCLUDED.purpose"
	if name, ok := patch.Values["name"]; ok {
		columns = append(columns, "name")
		values = append(values, name)
		set += ", name = EXCLUDED.name"
	}

	placeholders := make([]string, len(values))
	for i := range values {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	sql := "INSERT INTO list_registry (" + strings.Join(columns, ", ") + ") VALUES (" +
		strings.Join(placeholders, ", ") + ") ON CONFLICT (list_id) DO UPDATE SET " + set

	if _, err := q.Exec(ctx, sql, values...); err != nil {
		return fmt.Errorf("settings: 登记清单: %w", err)
	}
	return nil
}

// validateRequired 检查合并后（当前值 + 本次补丁）的必填字段。
//
// list_registry 的「当前值」是若干行而不是一行的列，所以它的必填只看本次补丁：
// 登记清单永远是「哪张 id + 什么用途」，改名字也要把这两人份一起带上。
func validateRequired(sec Section, current row, patch Patch) error {
	for _, name := range sec.Required {
		value, ok := patch.Values[name]
		if !ok && sec.Kind != KindLists {
			value, ok = current.values[name]
		}
		if !ok {
			return ValidationError{Field: name, Message: "必填"}
		}
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				return ValidationError{Field: name, Message: "必填"}
			}
		case int:
			if v == 0 {
				return ValidationError{Field: name, Message: "必填"}
			}
		}
	}
	return nil
}

func lastTestOf(at pgtype.Timestamptz, ok pgtype.Bool, errText pgtype.Text) *LastTest {
	if !at.Valid {
		return nil
	}
	return &LastTest{
		At:    at.Time.UTC(),
		OK:    ok.Valid && ok.Bool,
		Error: errText.String,
	}
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// registeredLists 读全部已登记的清单（list_registry 的行）。
//
// 给「测试读取」用：JavDB 的测试要连登记清单一起验，但清单是另一节。
func (s *Store) registeredLists(ctx context.Context) ([]ListEntry, error) {
	loaded, err := loadLists(ctx, s.pool, false)
	if err != nil {
		return nil, err
	}
	return loaded.lists, nil
}
