// Package task 拥有任务/版本域：两级聚合、占用租约与面板读模型（规格 §3/§4.1/§4.4）。
//
// 两条不变式由这里守：
//
//   - **两级聚合**：任务以 movie_id 为主键，版本以 infohash 为主键（全局唯一）；
//     任务级状态由 task_state 视图实时聚合，**不落列**（ADR-0006）。
//   - **先落库再加种**：版本行先以 add_pending=true 落库，qBittorrent 侧成功才清
//     （ADR-0004）。任意重入以版本行存在为去重判据。
//
// 占用（claim）是版本行上一份带超时的租约：清单轮询、webhook、对账三条触发路径
// 都先抢租约再动手，抢输不是失败（规格 §4.4）。进程内另有 per-version 锁。
package task

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// 版本来源与交付形态（规格 §4.1 的 CHECK 面）。
type Source string

const (
	SourceJavDBMagnet  Source = "javdb_magnet"
	SourceMTeamTorrent Source = "mteam_torrent"
)

type Delivery string

const (
	DeliveryMagnet  Delivery = "magnet"
	DeliveryTorrent Delivery = "torrent"
)

// Status 是版本状态（规格 §3）。
type Status string

const (
	StatusDownloading Status = "downloading"
	StatusProcessing  Status = "processing"
	StatusFinished    Status = "finished"
	StatusFailed      Status = "failed"
)

// Phase 是失败发生的那一步（规格 §3）。
type Phase string

const (
	PhaseDownloading Phase = "downloading"
	PhaseScrape      Phase = "scrape"
	PhaseOrganize    Phase = "organize"
)

// EventKind 是 task_event.kind 的取值（规格 §4.1）。
type EventKind string

const (
	EventStateChange EventKind = "state_change"
	EventSideEffect  EventKind = "side_effect"
	EventAlert       EventKind = "alert"
	EventManualRetry EventKind = "manual_retry"
)

// 占用租约：10 分钟（规格 §4.4）。Claim 与 ClaimPending 用同一个值。
const LeaseDuration = 10 * time.Minute

// 两个哨兵错误（规格 §4.4）：
//
//   - ErrNoPendingTask 是本轮没活（ClaimPending 找不到待加种的版本）；
//   - ErrClaimLost 是抢输（别人拿着租约），调用方不该把它当失败。
var (
	ErrNoPendingTask   = errors.New("task: 没有待处理的版本")
	ErrClaimLost       = errors.New("task: 没抢到版本租约（别人正处理）")
	ErrTaskNotFound    = errors.New("task: 任务不存在")
	ErrVersionNotFound = errors.New("task: 版本不存在")
)

// Candidate 是 poll 定源后要落库的一条候选：任务种子 + 版本种子。
//
// 一条 Candidate 同时保证任务行存在与版本行唯一：同 infohash 已有版本行时不写任何东西。
type Candidate struct {
	MovieID  string
	Number   string
	ListID   string
	Infohash string
	Source   Source
	Delivery Delivery
	// Label 是 best-effort 展示标签（如「中文」），空串表示没有可展示的标签。
	Label string
	// Payload 是磁力串或 .torrent 备份路径（规格 §4.1）。
	Payload string
}

// Version 是面板与循环都要读的一条版本。
type Version struct {
	Infohash           string
	MovieID            string
	Number             string
	Source             Source
	Delivery           Delivery
	Label              string
	Status             Status
	ErrorPhase         Phase
	ErrorReason        string
	AddPending         bool
	ListRemovalPending bool
	Attempts           int
	Payload            string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// TaskRow 是面板的一行：任务 + 聚合状态 + 版本。
//
// State 为空表示这个任务没有任何版本（view 对空集合给 NULL）；正常流程里不会出现。
type TaskRow struct {
	MovieID   string
	Number    string
	ListID    string
	State     string
	Versions  []Version
	Events    []Event
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Event 是一条只追加的任务事件（面板时间线）。
type Event struct {
	ID        int64
	MovieID   string
	Infohash  string
	Kind      EventKind
	FromState string
	ToState   string
	Phase     string
	Reason    string
	At        time.Time
}

// Counts 是面板统计卡的四个数（规格 §8.2）。
//
// NeedsAttention 由调用方加上驻留副作用数：那属于副作用域，不在任务表里。
type Counts struct {
	Downloading    int
	Processing     int
	Finished       int
	Failed         int
	NeedsAttention int
}

// ListFilter 是 GET /api/tasks 的筛选与翻页（规格 §8.4）。
type ListFilter struct {
	// State 为空表示不按状态筛。
	State string
	// Query 匹配 movie_id 或番号（大小写不敏感的子串）。
	Query string
	// Page 从 1 开始；0 当作 1。
	Page int
	// PageSize 为 0 时用 DefaultPageSize。
	PageSize int
}

const (
	// DefaultPageSize 是面板任务表的默认页大小。
	DefaultPageSize = 50
	// MaxPageSize 挡住一个过大的 page 参数把整库读进内存。
	MaxPageSize = 200
)

// DefaultOwner 是 claim 持有者的默认标识（hostname-pid）。
//
// 三条触发通道（poll / webhook / 对账）都用它：租约上记的是谁拿的，
// 排查「谁在跑这条版本」时一眼能对上进程。
func DefaultOwner() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "oh-my-mdc"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

// ValidState 报告一个 ?state= 是不是聚合状态名（空串合法，表示不筛）。
func ValidState(state string) bool {
	switch state {
	case "", string(StatusDownloading), string(StatusProcessing), string(StatusFinished), string(StatusFailed):
		return true
	}
	return false
}
