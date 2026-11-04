package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// taskVersionBody 是面板版本 chip 与抽屉版本列表的形状（规格 §8.4）。
//
// infohash 只出全串（抽屉要能复制去 qbt 查），不另给前缀——前缀是前端的展示选择。
type taskVersionBody struct {
	Infohash           string    `json:"infohash"`
	Source             string    `json:"source"`
	Delivery           string    `json:"delivery"`
	Label              string    `json:"label"`
	Status             string    `json:"status"`
	ErrorPhase         string    `json:"error_phase,omitempty"`
	ErrorReason        string    `json:"error_reason,omitempty"`
	AddPending         bool      `json:"add_pending"`
	ListRemovalPending bool      `json:"list_removal_pending"`
	ForceRescrape      bool      `json:"force_rescrape"`
	Attempts           int       `json:"attempts"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// taskBody 是面板任务表的一行：聚合状态 + 版本摘要。
type taskBody struct {
	MovieID   string            `json:"movie_id"`
	Number    string            `json:"number"`
	ListID    string            `json:"list_id"`
	State     string            `json:"state"`
	Versions  []taskVersionBody `json:"versions"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// taskEventBody 是抽屉事件时间线的一行。
//
// Detail 是结构化附注（人工重试留下旧 raw_snapshot 的对比基准）：v1 抽屉不渲染刮削字段，
// 但接口把它原样回出去——「重刮前 raw_snapshot 对比可查」需要读得到（规格 §8.2 / P13-3）。
type taskEventBody struct {
	ID        int64           `json:"id"`
	MovieID   string          `json:"movie_id"`
	Infohash  string          `json:"infohash,omitempty"`
	Kind      string          `json:"kind"`
	FromState string          `json:"from_state,omitempty"`
	ToState   string          `json:"to_state,omitempty"`
	Phase     string          `json:"phase,omitempty"`
	Reason    string          `json:"reason"`
	Detail    json.RawMessage `json:"detail,omitempty"`
	At        time.Time       `json:"at"`
}

// tasksBody 是 GET /api/tasks 的回执。
type tasksBody struct {
	OK    bool       `json:"ok"`
	Tasks []taskBody `json:"tasks"`
	Page  int        `json:"page"`
	// Total 是**同筛选条件下**的总条数：面板用它说「共 N 条」并把最后一页的
	// 「下一页」关掉，而不是靠「这一页满了」猜（满页的最后一页会一直可点）。
	Total    int `json:"total"`
	PageSize int `json:"page_size"`
}

// taskDetailBody 是 GET /api/tasks/{movie_id} 的回执（抽屉数据）。
type taskDetailBody struct {
	OK     bool            `json:"ok"`
	Task   taskBody        `json:"task"`
	Events []taskEventBody `json:"events"`
}

// tasksList 返回面板任务表的一页（?state=&q=&page=，规格 §8.4）。
func (s *server) tasksList(w http.ResponseWriter, r *http.Request) {
	store, ok := s.tasksStore(w)
	if !ok {
		return
	}

	query := r.URL.Query()
	state := strings.TrimSpace(query.Get("state"))
	if !task.ValidState(state) {
		writeError(w, http.StatusBadRequest, "validation_error",
			"state 只能是 downloading / processing / finished / failed")
		return
	}

	page := 1
	if raw := strings.TrimSpace(query.Get("page")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "validation_error", "page 必须是正整数")
			return
		}
		page = parsed
	}

	filter := task.ListFilter{
		State: state,
		Query: query.Get("q"),
		Page:  page,
	}

	rows, err := store.List(r.Context(), filter)
	if err != nil {
		s.writeTaskError(w, err)
		return
	}

	total, err := store.Count(r.Context(), filter)
	if err != nil {
		s.writeTaskError(w, err)
		return
	}

	body := tasksBody{
		OK:       true,
		Tasks:    make([]taskBody, 0, len(rows)),
		Page:     page,
		Total:    total,
		PageSize: task.DefaultPageSize,
	}
	for _, row := range rows {
		body.Tasks = append(body.Tasks, toTaskBody(row))
	}
	writeJSON(w, http.StatusOK, body)
}

// taskDetail 返回抽屉数据：任务 + 版本列表 + 最近事件。
func (s *server) taskDetail(w http.ResponseWriter, r *http.Request) {
	store, ok := s.tasksStore(w)
	if !ok {
		return
	}

	row, err := store.Detail(r.Context(), r.PathValue("movie_id"), 50)
	if err != nil {
		s.writeTaskError(w, err)
		return
	}

	body := taskDetailBody{OK: true, Task: toTaskBody(row), Events: make([]taskEventBody, 0, len(row.Events))}
	for _, event := range row.Events {
		body.Events = append(body.Events, toEventBody(event))
	}
	writeJSON(w, http.StatusOK, body)
}

// tasksStore 把「任务域没装配」收敛成 503 db_unavailable（与设置域同一套语义）。
func (s *server) tasksStore(w http.ResponseWriter) (*task.Store, bool) {
	if s.tasks == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "任务域未装配（数据库不可用）")
		return nil, false
	}
	return s.tasks, true
}

// writeTaskError 把任务域的错误翻成 §8.4 的状态码与稳定机器码。
func (s *server) writeTaskError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, task.ErrTaskNotFound):
		writeError(w, http.StatusNotFound, "not_found", "没有这个任务")
	default:
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())
	}
}

func toTaskBody(row task.TaskRow) taskBody {
	body := taskBody{
		MovieID:   row.MovieID,
		Number:    row.Number,
		ListID:    row.ListID,
		State:     row.State,
		Versions:  make([]taskVersionBody, 0, len(row.Versions)),
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	for _, version := range row.Versions {
		body.Versions = append(body.Versions, taskVersionBody{
			Infohash:           version.Infohash,
			Source:             string(version.Source),
			Delivery:           string(version.Delivery),
			Label:              version.Label,
			Status:             string(version.Status),
			ErrorPhase:         string(version.ErrorPhase),
			ErrorReason:        version.ErrorReason,
			AddPending:         version.AddPending,
			ListRemovalPending: version.ListRemovalPending,
			ForceRescrape:      version.ForceRescrape,
			Attempts:           version.Attempts,
			CreatedAt:          version.CreatedAt,
			UpdatedAt:          version.UpdatedAt,
		})
	}
	return body
}

func toEventBody(event task.Event) taskEventBody {
	return taskEventBody{
		ID:        event.ID,
		MovieID:   event.MovieID,
		Infohash:  event.Infohash,
		Kind:      string(event.Kind),
		FromState: event.FromState,
		ToState:   event.ToState,
		Phase:     event.Phase,
		Reason:    event.Reason,
		Detail:    event.Detail,
		At:        event.At,
	}
}

// attentionBody 是 GET /api/attention 的回执（面板「需要人工」区的任务侧两半）。
type attentionBody struct {
	OK      bool            `json:"ok"`
	Failed  []attentionItem `json:"failed"`
	Stalled []attentionItem `json:"stalled"`
	// FailedTruncated / StalledTruncated：这一半到了上限、后面还有。
	// 面板据此把数字写成「50+」，而不是把队列的总量说成 50。
	FailedTruncated  bool `json:"failed_truncated"`
	StalledTruncated bool `json:"stalled_truncated"`
}

// attentionItem 是「需要人工」区的一行：失败的版本或停滞的下载。
type attentionItem struct {
	MovieID  string    `json:"movie_id"`
	Number   string    `json:"number"`
	Infohash string    `json:"infohash"`
	Source   string    `json:"source"`
	Label    string    `json:"label"`
	Phase    string    `json:"phase,omitempty"`
	Reason   string    `json:"reason"`
	At       time.Time `json:"at"`
}

// attention 返回「需要人工」区的任务侧两半（规格 §8.2）。
//
// 驻留副作用不在这里：它属于副作用域，面板用 GET /api/side-effects?state=stuck
// 拼进同一个区，两边的行各带自己的操作（重发 / 看任务）。
// attentionLimit 是「需要人工」区每一半最多回多少行。
// 超过它的队列会被截断，但回执会带上 *_truncated，面板因此不会把 50 说成总量。
const attentionLimit = 50

func (s *server) attention(w http.ResponseWriter, r *http.Request) {
	store, ok := s.tasksStore(w)
	if !ok {
		return
	}

	attention, err := store.Attention(r.Context(), attentionLimit)
	if err != nil {
		s.writeTaskError(w, err)
		return
	}

	body := attentionBody{
		OK:               true,
		Failed:           []attentionItem{},
		Stalled:          []attentionItem{},
		FailedTruncated:  attention.FailedTruncated,
		StalledTruncated: attention.StalledTruncated,
	}
	for _, item := range attention.Failed {
		body.Failed = append(body.Failed, toAttentionItem(item))
	}
	for _, item := range attention.Stalled {
		body.Stalled = append(body.Stalled, toAttentionItem(item))
	}
	writeJSON(w, http.StatusOK, body)
}

func toAttentionItem(item task.AttentionItem) attentionItem {
	return attentionItem{
		MovieID:  item.MovieID,
		Number:   item.Number,
		Infohash: item.Infohash,
		Source:   item.Source,
		Label:    item.Label,
		Phase:    item.Phase,
		Reason:   item.Reason,
		At:       item.At,
	}
}

// maxRetryBody 挡住一个过大的请求体：重试只需要一个布尔。
const maxRetryBody = 4 << 10

// retryRequest 是 POST /api/tasks/{movie_id}/retry 的请求体（规格 §8.4）。
// 缺省（空请求体或只有 {}）就是 force=false。
type retryRequest struct {
	Force bool `json:"force"`
}

// taskRetryBody 是 POST /api/tasks/{movie_id}/retry 的回执。
//
// 逐版结果而不是一个布尔：一条任务可以有多条版本，一个在下载中被拒、
// 另一条重新加种成功是正常的一天（规格 §6.1 人工重试行）。
type taskRetryBody struct {
	OK      bool               `json:"ok"`
	MovieID string             `json:"movie_id"`
	Force   bool               `json:"force"`
	Results []taskRetryOutcome `json:"results"`
}

// taskRetryOutcome 是逐版结局：面板行内显示 reason，测试与排查看 from/to。
type taskRetryOutcome struct {
	Infohash      string `json:"infohash"`
	Action        string `json:"action"`
	FromState     string `json:"from_state"`
	ToState       string `json:"to_state"`
	Reason        string `json:"reason"`
	ForceRescrape bool   `json:"force_rescrape"`
}

// taskRetry 是「重试 / 强制重刮」的入口（规格 §8.4 / P13）。
//
// 永远 200 + 逐版结果：版本的结局（被拒、加种失败）不是 HTTP 错误，而是这个任务
// 此刻的真实状态；只有任务不存在（404）与库不可用（503）才用错误信封。
func (s *server) taskRetry(w http.ResponseWriter, r *http.Request) {
	if s.retrier == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "人工重试未装配（数据库不可用）")
		return
	}

	request := retryRequest{}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRetryBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "读请求体失败: "+err.Error())
		return
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &request); err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "请求体不是 JSON 对象: "+err.Error())
			return
		}
	}

	result, err := s.retrier.Retry(r.Context(), r.PathValue("movie_id"), request.Force)
	if err != nil {
		s.writeTaskError(w, err)
		return
	}

	response := taskRetryBody{OK: true, MovieID: result.MovieID, Force: request.Force,
		Results: make([]taskRetryOutcome, 0, len(result.Versions))}
	for _, outcome := range result.Versions {
		response.Results = append(response.Results, taskRetryOutcome{
			Infohash:      outcome.Infohash,
			Action:        string(outcome.Action),
			FromState:     string(outcome.FromState),
			ToState:       string(outcome.ToState),
			Reason:        outcome.Reason,
			ForceRescrape: outcome.ForceRescrape,
		})
	}
	writeJSON(w, http.StatusOK, response)
}
