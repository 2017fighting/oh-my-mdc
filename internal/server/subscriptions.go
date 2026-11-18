package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/subscription"
)

// 订阅区的 HTTP 面（规格 §8.4）：一个读端点 + 三个动作端点。
//
//   - GET  /api/subscriptions：面板订阅区那一屏（只列已订阅的女优——上游没有搜名字的
//     端点，§13 实测，所以这里不提供搜索）；
//   - POST /api/subscriptions：{actress_id} 或 {javdb_url}，解析 id → upsert actor → 建订阅；
//     同一位女优重复添加是幂等放行（outcome=duplicate）也是 200；
//   - POST /api/subscriptions/{id}/enable|disable：启停（停用只停追新）；
//   - POST /api/subscriptions/{id}/backfill {days} | {all:true}：把取数下界往回拨
//     （连起追日一起挪），回执给 backfill_from。
//
// 错误信封沿用 §8.4：形状 400 validation_error、门禁 409 gate_closed、id 读不到
// 502 upstream_error、行号不存在 404 not_found、装配缺失或库抖 503 db_unavailable。

// maxSubscriptionBody 挡住过大的请求体：这里只需要一个 id 或一条链接。
const maxSubscriptionBody = 4 << 10

// Subscriber 是订阅域的接缝（真实实现是 subscription.Service）。
//
// 四个方法就是面板上能做的四件事；循环（Runner）不在这条接缝上——它跑在进程里，
// 面板不该有能力替它跑一轮。
type Subscriber interface {
	List(ctx context.Context) ([]subscription.Row, error)
	Add(ctx context.Context, request subscription.Request) (subscription.Row, string, error)
	SetEnabled(ctx context.Context, id int64, enabled bool) (subscription.Row, error)
	Backfill(ctx context.Context, id int64, request subscription.BackfillRequest) (subscription.Row, error)
}

// subscriptionActorBody 是面板要的演员快照（名字来自上游，性别/头像等刮削补）。
type subscriptionActorBody struct {
	JavdbActorID string `json:"javdb_actor_id"`
	Name         string `json:"name"`
	AvatarURL    string `json:"avatar_url"`
	Gender       string `json:"gender"`
}

// subscriptionBody 是订阅区的一行。
type subscriptionBody struct {
	ID    int64                 `json:"id"`
	Actor subscriptionActorBody `json:"actor"`
	// WatermarkDate 是上一轮见过的最晚发行日（日期串，不是时间戳）。
	WatermarkDate string `json:"watermark_date"`
	LookbackDays  int    `json:"lookback_days"`
	// BackfillFrom 是回灌设置的取数下界；没在回灌时是 null。
	BackfillFrom *string `json:"backfill_from"`
	Enabled      bool    `json:"enabled"`
	// TasksCount 是「带来 N 部」；PendingCount 是这批里还有版本在等加种的条数。
	TasksCount   int `json:"tasks_count"`
	PendingCount int `json:"pending_count"`
}

type subscriptionsBody struct {
	OK        bool               `json:"ok"`
	Actresses []subscriptionBody `json:"actresses"`
}

type subscriptionCreateRequest struct {
	ActressID string `json:"actress_id"`
	JavdbURL  string `json:"javdb_url"`
}

type subscriptionCreateBody struct {
	OK      bool             `json:"ok"`
	Outcome string           `json:"outcome"`
	Actress subscriptionBody `json:"actress"`
}

// subscriptionBackfillRequest 是回灌的两种请求体（§8.4）：{days} 或 {all: true}。
//
// Days 是 *int：缺了（nil）与给了 0 是两件事——前者是「既没给 days 也没给 all」，
// 后者是天数不合法；两者的 400 文案不同，而空值本身必须能分辨出来。
type subscriptionBackfillRequest struct {
	Days *int `json:"days"`
	All  bool `json:"all"`
}

type subscriptionBackfillBody struct {
	OK           bool    `json:"ok"`
	BackfillFrom *string `json:"backfill_from"`
}

func (s *server) subscriptionsList(w http.ResponseWriter, r *http.Request) {
	if s.subscriptions == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "订阅未装配（数据库不可用）")
		return
	}
	rows, err := s.subscriptions.List(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())
		return
	}
	body := subscriptionsBody{OK: true, Actresses: make([]subscriptionBody, 0, len(rows))}
	for _, row := range rows {
		body.Actresses = append(body.Actresses, subscriptionBodyOf(row))
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *server) subscriptionCreate(w http.ResponseWriter, r *http.Request) {
	if s.subscriptions == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "订阅未装配（数据库不可用）")
		return
	}

	request, ok := decodeSubscriptionCreate(w, r)
	if !ok {
		return
	}
	row, outcome, err := s.subscriptions.Add(r.Context(), request)
	if err != nil {
		s.writeSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, subscriptionCreateBody{
		OK:      true,
		Outcome: outcome,
		Actress: subscriptionBodyOf(row),
	})
}

// subscriptionEnable / subscriptionDisable 是启停的两颗按钮（规格 §8.4）。
//
// 停用只停追新：已建任务照走完，水位线与跳过行一概不碰——那是订阅域的语义，
// HTTP 只是把 enabled 这个布尔递进去。
func (s *server) subscriptionEnable(w http.ResponseWriter, r *http.Request) {
	s.subscriptionSetEnabled(w, r, true)
}

func (s *server) subscriptionDisable(w http.ResponseWriter, r *http.Request) {
	s.subscriptionSetEnabled(w, r, false)
}

func (s *server) subscriptionSetEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	if s.subscriptions == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "订阅未装配（数据库不可用）")
		return
	}
	id, ok := subscriptionIDOf(w, r)
	if !ok {
		return
	}
	row, err := s.subscriptions.SetEnabled(r.Context(), id, enabled)
	if err != nil {
		s.writeSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, subscriptionCreateBody{
		OK:      true,
		Outcome: outcomeFor(enabled),
		Actress: subscriptionBodyOf(row),
	})
}

func (s *server) subscriptionBackfill(w http.ResponseWriter, r *http.Request) {
	if s.subscriptions == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "订阅未装配（数据库不可用）")
		return
	}
	id, ok := subscriptionIDOf(w, r)
	if !ok {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxSubscriptionBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "读请求体失败: "+err.Error())
		return
	}
	decoded := subscriptionBackfillRequest{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "请求体不是 JSON 对象（days 要是整数）: "+err.Error())
		return
	}
	if !decoded.All && decoded.Days == nil {
		writeError(w, http.StatusBadRequest, "validation_error", "要给 days（往回拨多少天）或 all:true（翻到她最早的作品）")
		return
	}
	request := subscription.BackfillRequest{All: decoded.All}
	if decoded.Days != nil {
		request.Days = *decoded.Days
	}

	row, err := s.subscriptions.Backfill(r.Context(), id, request)
	if err != nil {
		s.writeSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, subscriptionBackfillBody{OK: true, BackfillFrom: dateOf(row.BackfillFrom)})
}

// decodeSubscriptionCreate 解析「贴 id 或贴链接」的请求体。
func decodeSubscriptionCreate(w http.ResponseWriter, r *http.Request) (subscription.Request, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSubscriptionBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "读请求体失败: "+err.Error())
		return subscription.Request{}, false
	}
	decoded := subscriptionCreateRequest{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "请求体不是 JSON 对象: "+err.Error())
		return subscription.Request{}, false
	}
	if strings.TrimSpace(decoded.ActressID) == "" && strings.TrimSpace(decoded.JavdbURL) == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "要给 actress_id 或 javdb_url 之一")
		return subscription.Request{}, false
	}
	return subscription.Request{ActressID: decoded.ActressID, JavdbURL: decoded.JavdbURL}, true
}

// subscriptionIDOf 解析路径里的行号；不是数字就 400。
func subscriptionIDOf(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "validation_error", "订阅 id 要是正整数")
		return 0, false
	}
	return id, true
}

// writeSubscriptionError 把订阅域的错误翻成 §8.4 的状态码与稳定机器码。
func (s *server) writeSubscriptionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscription.ErrBadInput):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, subscription.ErrGated):
		writeError(w, http.StatusConflict, "gate_closed", err.Error())
	case errors.Is(err, subscription.ErrUpstream):
		// 「这个 id 在上游读不到」：要用户改输入，也可能是上游抖了——502 是这条的既有口径
		// （与向导辅助端点的 protocol/network 同档）。
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
	case errors.Is(err, subscription.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())
	}
}

// subscriptionBodyOf 把一行订阅翻成对外的形状。
func subscriptionBodyOf(row subscription.Row) subscriptionBody {
	return subscriptionBody{
		ID: row.ID,
		Actor: subscriptionActorBody{
			JavdbActorID: row.Actor.ID,
			Name:         row.Actor.Name,
			AvatarURL:    row.Actor.AvatarURL,
			Gender:       row.Actor.Gender,
		},
		WatermarkDate: dateString(row.WatermarkDate),
		LookbackDays:  row.LookbackDays,
		BackfillFrom:  dateOf(row.BackfillFrom),
		Enabled:       row.Enabled,
		TasksCount:    row.TasksCount,
		PendingCount:  row.PendingCount,
	}
}

// dateString 把水位线摊成 `YYYY-MM-DD`；NULL 给空串（面板据此说「不知道」）。
func dateString(at *time.Time) string {
	if at == nil {
		return ""
	}
	return at.Format("2006-01-02")
}

// dateOf 把可空日期摊成 JSON 的 string|null（回灌下界：NULL 就是「没在回灌」）。
func dateOf(at *time.Time) *string {
	if at == nil {
		return nil
	}
	formatted := at.Format("2006-01-02")
	return &formatted
}

// outcomeFor 是启停回执里的 outcome：它复用「这次调用做成了什么」的同一套词。
func outcomeFor(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}
