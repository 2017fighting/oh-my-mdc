package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/backfill"
)

// 补录（#43，GLOSSARY「补录」）的 HTTP 面：一个读端点 + 一个动作端点。
//
//   - GET /api/backfill/scan：「未补录目录」列表（面板常驻区），实时扫盘比对任务表；
//   - POST /api/backfill：一键补录。幂等放行（duplicate）也是 200——重复点击
//     零写入不是错误，回执里的 outcome 与 message 把「为什么没新建」说清。
//
// 错误信封沿用 §8.4：输入形状 400 validation_error、门禁/目录状态 409
// （gate_closed / dir_missing / no_videos）、装配缺失或库抖 503 db_unavailable。

// maxBackfillBody 挡住过大的请求体：一键补录只需要一个番号。
const maxBackfillBody = 2 << 10

// backfillRunRequest 是 POST /api/backfill 的请求体。
type backfillRunRequest struct {
	Number string `json:"number"`
}

// backfillScanBody 是 GET /api/backfill/scan 的回执。
type backfillScanBody struct {
	OK   bool                 `json:"ok"`
	Dirs []backfill.Candidate `json:"dirs"`
}

// backfillRunBody 是 POST /api/backfill 的回执。
type backfillRunBody struct {
	OK       bool   `json:"ok"`
	MovieID  string `json:"movie_id"`
	Number   string `json:"number"`
	Outcome  string `json:"outcome"`
	Versions int    `json:"versions"`
	Message  string `json:"message,omitempty"`
}

func (s *server) backfillScan(w http.ResponseWriter, r *http.Request) {
	if s.backfill == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "补录未装配（数据库不可用）")
		return
	}
	dirs, err := s.backfill.Scan(r.Context())
	if err != nil {
		s.writeBackfillError(w, err)
		return
	}
	if dirs == nil {
		dirs = []backfill.Candidate{}
	}
	writeJSON(w, http.StatusOK, backfillScanBody{OK: true, Dirs: dirs})
}

func (s *server) backfillRun(w http.ResponseWriter, r *http.Request) {
	if s.backfill == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "补录未装配（数据库不可用）")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBackfillBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "读请求体失败: "+err.Error())
		return
	}
	request := backfillRunRequest{}
	if err := json.Unmarshal(body, &request); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "请求体不是 JSON 对象: "+err.Error())
		return
	}
	if strings.TrimSpace(request.Number) == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "缺 number：要补录的目录名（番号）")
		return
	}

	result, err := s.backfill.Backfill(r.Context(), request.Number)
	if err != nil {
		s.writeBackfillError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, backfillRunBody{
		OK:       true,
		MovieID:  result.MovieID,
		Number:   result.Number,
		Outcome:  result.Outcome,
		Versions: result.Versions,
		Message:  result.Reason,
	})
}

// writeBackfillError 把补录的错误翻成 §8.4 的状态码与稳定机器码。
func (s *server) writeBackfillError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, backfill.ErrBadNumber):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, backfill.ErrGated):
		writeError(w, http.StatusConflict, "gate_closed", err.Error())
	case errors.Is(err, backfill.ErrNoVideos):
		writeError(w, http.StatusConflict, "no_videos", err.Error())
	case errors.Is(err, backfill.ErrDirMissing):
		// 列表是快照、世界在变：点下去的时候目录没了是状态冲突，不是输入错。
		writeError(w, http.StatusConflict, "dir_missing", err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())
	}
}
