package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
)

// 副作用队列的 HTTP 面（规格 §8.4）：GET 列队、POST 人工重发。
//
// 面板用它做两件事：
//   - 「需要人工」区读 ?state=stuck（驻留 = 需人工）；
//   - 底部副作用队列读不带 state 的全量未完成。

// sideEffectBody 是一条待办的对外形状。
//
// payload 原样透出：通知模板的番号/标签、删除的清单 id 都在里面，面板要用它说清
// 「这一条到底在做什么」。它不含凭据，所以不需要脱敏。
type sideEffectBody struct {
	ID            int64          `json:"id"`
	Kind          string         `json:"kind"`
	TargetKey     string         `json:"target_key"`
	Payload       map[string]any `json:"payload"`
	Attempts      int            `json:"attempts"`
	LastError     string         `json:"last_error,omitempty"`
	NextAttemptAt *time.Time     `json:"next_attempt_at"`
	Stuck         bool           `json:"stuck"`
}

// sideEffectsBody 是 GET /api/side-effects 的回执。
type sideEffectsBody struct {
	OK          bool             `json:"ok"`
	SideEffects []sideEffectBody `json:"side_effects"`
}

// sideEffectRetryBody 是 POST /api/side-effects/{id}/retry 的回执（重排之后的那一行）。
type sideEffectRetryBody struct {
	OK         bool           `json:"ok"`
	SideEffect sideEffectBody `json:"side_effect"`
}

// sideEffectsList 返回副作用队列（?state=pending|stuck，省略＝全部未完成）。
func (s *server) sideEffectsList(w http.ResponseWriter, r *http.Request) {
	outbox, ok := s.sidefxOutbox(w)
	if !ok {
		return
	}

	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if !sidefx.ValidState(state) {
		writeError(w, http.StatusBadRequest, "validation_error", "state 只能是 pending 或 stuck")
		return
	}

	jobs, err := outbox.List(r.Context(), sidefx.State(state), 0)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())
		return
	}

	body := sideEffectsBody{OK: true, SideEffects: make([]sideEffectBody, 0, len(jobs))}
	for _, job := range jobs {
		body.SideEffects = append(body.SideEffects, toSideEffectBody(job))
	}
	writeJSON(w, http.StatusOK, body)
}

// sideEffectRetry 是人工重发：清驻留、重排 next_attempt_at（规格 §8.4）。
//
// 它不立即执行：执行归 sidefx 循环（下一个周期），这里只把这一条交回队列——
// HTTP 请求不该替循环去调上游，那样超时与重试就绕开了单飞与退避。
func (s *server) sideEffectRetry(w http.ResponseWriter, r *http.Request) {
	outbox, ok := s.sidefxOutbox(w)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "validation_error", "副作用 id 必须是正整数")
		return
	}

	if err := outbox.Retry(r.Context(), id); err != nil {
		if errors.Is(err, sidefx.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "没有这条未完成的副作用（可能已经完成）")
			return
		}
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())
		return
	}

	job, err := outbox.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sideEffectRetryBody{OK: true, SideEffect: toSideEffectBody(job)})
}

// sidefxOutbox 把「副作用域没装配」收敛成 503 db_unavailable。
func (s *server) sidefxOutbox(w http.ResponseWriter) (*sidefx.Outbox, bool) {
	if s.sidefx == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "副作用域未装配（数据库不可用）")
		return nil, false
	}
	return s.sidefx, true
}

func toSideEffectBody(job sidefx.Job) sideEffectBody {
	payload := map[string]any{}
	if len(job.Payload) > 0 {
		// payload 是入箱时自己写的 JSON；读不出来说明这一条坏了，空对象比 500 有用
		// （面板还能显示 kind/target_key/last_error，人工重发也不会因此被挡住）。
		if decoded, ok := decodeJSONObject(job.Payload); ok {
			payload = decoded
		}
	}
	return sideEffectBody{
		ID:            job.ID,
		Kind:          string(job.Kind),
		TargetKey:     job.TargetKey,
		Payload:       payload,
		Attempts:      job.Attempts,
		LastError:     job.LastError,
		NextAttemptAt: job.NextAttemptAt,
		Stuck:         job.Stuck(),
	}
}
