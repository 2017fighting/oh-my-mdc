// 待补齐标注（#42 / GLOSSARY「待补齐」）：入库后关键面仍有缺口且上游重试耗尽的
// 任务标注。它**不是任务状态**（ADR-0006 的任务状态只由版本聚合，不落列）：
// 面板聚合成区、/metrics 出计数、修复动作是强制重刮，四面补齐自动摘标。
package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Face 是待补齐四面判据的机器名（task.missing_faces 数组的合法成员）。
// 取值就是缺的那一面：panel 文案（web/app/lib/copy.ts）按同一套词翻译。
const (
	// FaceImages 是「无任何图」：入库目录里 poster/fanart/extrafanart 三处全空。
	FaceImages = "images"
	// FaceActors 是「无演员」：task_actor 一行都没有（JavDB actors=null 也算）。
	// 唯一例外是补录任务（规格 §6.3）：取数只问 DMM、演员只来自 JavDB，演员面
	// 对补录是结构性永缺，不计入待补齐——豁免在整理段入库标注处（按 source 分支）。
	FaceActors = "actors"
	// FaceOutline 是「无简介」：outline 列为空。
	FaceOutline = "outline"
	// FaceTitleZH 是「翻译启用却无中文标题」：ai_translation 启用而 title_zh 为空。
	FaceTitleZH = "title_zh"
)

// validFace 报告一个面名是不是四面之一（写入口的单点校验）。
func validFace(face string) bool {
	switch face {
	case FaceImages, FaceActors, FaceOutline, FaceTitleZH:
		return true
	}
	return false
}

// SetMissingFaces 整体覆写一个任务的待补齐标注：faces 为空即摘标。
//
// 写入方只有整理段（入库那一刻评估四面，唯一掌握全部事实的时刻）；
// 强制重刮补齐四面后重新入库，同一条语句负责摘标。重复面去重、未知面报错：
// 判据词汇是跨包契约（Go 侧写、Web 侧读），悄悄落进一个拼错的词不如当场失败。
func (s *Store) SetMissingFaces(ctx context.Context, movieID string, faces []string) error {
	if strings.TrimSpace(movieID) == "" {
		return errors.New("task: 标待补齐缺 movie_id")
	}
	ordered := make([]string, 0, len(faces))
	seen := make(map[string]bool, len(faces))
	for _, face := range faces {
		if !validFace(face) {
			return fmt.Errorf("task: 未知待补齐面 %q", face)
		}
		if !seen[face] {
			seen[face] = true
			ordered = append(ordered, face)
		}
	}
	if ordered == nil {
		ordered = []string{}
	}

	tag, err := s.pool.Exec(ctx,
		`UPDATE task SET missing_faces = $2, updated_at = now() WHERE movie_id = $1`,
		movieID, ordered,
	)
	if err != nil {
		return fmt.Errorf("task: 标任务 %s 的待补齐面: %w", movieID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// ClearMissingFace 摘掉一个已经补齐的面（#42「四面补齐自动摘标」的补译路径）。
//
// 取数段的补译在复用路径上只填 title_zh、不触发整理段重入库，所以这一面的
// 摘标也由它做——其它三面的现状没变，不动。面本来就不在标注里时是零写入。
func (s *Store) ClearMissingFace(ctx context.Context, movieID, face string) error {
	if strings.TrimSpace(movieID) == "" {
		return errors.New("task: 摘待补齐面缺 movie_id")
	}
	if !validFace(face) {
		return fmt.Errorf("task: 未知待补齐面 %q", face)
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE task SET missing_faces = array_remove(missing_faces, $2), updated_at = now()
		 WHERE movie_id = $1`,
		movieID, face,
	)
	if err != nil {
		return fmt.Errorf("task: 摘任务 %s 的待补齐面 %s: %w", movieID, face, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// IncompleteTask 是「待补齐」聚合区的一行（#42 / 规格 §8.2）。
//
// 行的单位是**任务**不是版本：缺口是任务级的（四面都长在 task 上），
// 修复动作（强制重刮）也是任务级的。
type IncompleteTask struct {
	MovieID string
	Number  string
	// MissingFaces 是缺的那几面（faces.go 的机器名），面板按成员渲染 chip。
	MissingFaces []string
	UpdatedAt    time.Time
}

// Incomplete 列待补齐的任务，最近有动静的在前。
//
// 行数超过 limit 时截断（与 Attention 同一口径：调用方取 limit+1 才知道后面还有）。
func (s *Store) Incomplete(ctx context.Context, limit int) ([]IncompleteTask, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	rows, err := s.pool.Query(ctx,
		`SELECT movie_id, number, missing_faces, updated_at
		 FROM task
		 WHERE missing_faces <> '{}'
		 ORDER BY updated_at DESC, movie_id
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("task: 列待补齐任务: %w", err)
	}
	defer rows.Close()

	tasks := []IncompleteTask{}
	for rows.Next() {
		var row IncompleteTask
		if err := rows.Scan(&row.MovieID, &row.Number, &row.MissingFaces, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("task: 扫待补齐任务: %w", err)
		}
		tasks = append(tasks, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: 遍历待补齐任务: %w", err)
	}
	return tasks, nil
}

// IncompleteCount 数待补齐的任务（/metrics 的 mdc_task_incomplete 用）。
// 与面板聚合区同一个 WHERE，两个数字不会互相打架。
func (s *Store) IncompleteCount(ctx context.Context) (int, error) {
	var count int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM task WHERE missing_faces <> '{}'`,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("task: 数待补齐任务: %w", err)
	}
	return count, nil
}
