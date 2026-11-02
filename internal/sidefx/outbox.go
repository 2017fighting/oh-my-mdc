// Package sidefx 拥有旁路副作用：出箱表与执行引擎（ADR-0005，规格 §4.2/§5.5/§6.2）。
//
// 分工是固定的：
//
//   - **入箱**（outbox.go）：任务流程结束时把待办写进 side_effect，失败不阻塞任务本身。
//     新待办必须带 next_attempt_at = now()：NULL 只由执行器在放弃重试时写。
//   - **执行**（engine.go）：sidefx 循环按 kind 消费待办，瞬时错指数退避、永久错立即驻留
//     （需人工），绝不自动禁用集成。
//
// 两条语义在这里固定：
//   - 同一目标只许一条未完成的待办（部分唯一索引），所以刷新可以天然合并，完成之后
//     同一目标还可以再入箱（同片再进清单可再删、同事件再来可再发）；
//   - 驻留（需人工）＝ done_at IS NULL AND next_attempt_at IS NULL，驱动面板需人工区。
package sidefx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Kind 是 side_effect.kind 的取值（规格 §4.2）。
type Kind string

const (
	KindListRemoval     Kind = "list_removal"
	KindJellyfinRefresh Kind = "jellyfin_refresh"
	KindNotify          Kind = "notify"
)

// 通知事件名（规格 §5.6）。target_key 是 "<event>:<infohash>"，靠它做「同事件只发一次」。
//
// 它们同时是 Telegram 模板的选择键，四个事件各有独立开关；模板本身由 #28 落地。
const (
	EventDownloadStarted  = "download_started"
	EventFiled            = "filed"
	EventProcessingFailed = "processing_failed"
	EventDownloadStalled  = "download_stalled"
)

// Outbox 是副作用出箱的入箱口。
type Outbox struct {
	pool *pgxpool.Pool
}

// New 装配出箱口。
func New(pool *pgxpool.Pool) *Outbox {
	return &Outbox{pool: pool}
}

// Enqueue 写入一条待办。已有同 (kind, target_key) 的未完成待办时静默跳过
// （部分唯一索引 + ON CONFLICT DO NOTHING）：新事件不该让一条已在排队的活变成两条。
//
// payload 是这一条待办的上下文（通知模板的番号/标签/来源/大小）；nil 记作 {}。
func (o *Outbox) Enqueue(ctx context.Context, kind Kind, targetKey string, payload any) error {
	targetKey = strings.TrimSpace(targetKey)
	if targetKey == "" {
		return errors.New("sidefx: 待办缺 target_key")
	}

	if err := insertJob(ctx, o.pool, kind, targetKey, payload); err != nil {
		return err
	}
	return nil
}

// EnqueueNotify 入箱一条通知事件（target_key = "<event>:<infohash>"）。
func (o *Outbox) EnqueueNotify(ctx context.Context, event, infohash string, payload any) error {
	return o.Enqueue(ctx, KindNotify, event+":"+infohash, payload)
}

// StuckCount 数驻留待办（done_at IS NULL AND next_attempt_at IS NULL）。
//
// 它是「需人工」统计卡的一分子（规格 §8.2）：失败任务由 task_state 数，驻留副作用数这里。
func (o *Outbox) StuckCount(ctx context.Context) (int, error) {
	var count int
	if err := o.pool.QueryRow(ctx,
		`SELECT count(*) FROM side_effect WHERE done_at IS NULL AND next_attempt_at IS NULL`,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("sidefx: 数驻留待办: %w", err)
	}
	return count, nil
}

func marshalPayload(payload any) ([]byte, error) {
	if payload == nil {
		return []byte("{}"), nil
	}
	if raw, ok := payload.([]byte); ok {
		if !json.Valid(raw) {
			return nil, errors.New("sidefx: payload 不是合法 JSON")
		}
		return raw, nil
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("sidefx: 编码 payload: %w", err)
	}
	return encoded, nil
}
