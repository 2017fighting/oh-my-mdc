package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/manual"
)

// maxRegistrationBody 挡住一个过大的请求体：登记只需要两串粘贴（磁力 + 链接）。
const maxRegistrationBody = 8 << 10

// manualRegistrationRequest 是 POST /api/manual-tasks 的请求体（GLOSSARY「手工登记」）。
type manualRegistrationRequest struct {
	Magnet   string `json:"magnet"`
	JavdbURL string `json:"javdb_url"`
}

// manualRegistrationBody 是 POST /api/manual-tasks 的回执。
//
// Outcome 直译登记结局（added / adopted / already_tracked / deferred / duplicate），
// Message 是 deferred / duplicate 时给人看的那句话；面板两行文案都靠它。
type manualRegistrationBody struct {
	OK       bool   `json:"ok"`
	MovieID  string `json:"movie_id"`
	Infohash string `json:"infohash"`
	Number   string `json:"number"`
	Outcome  string `json:"outcome"`
	Message  string `json:"message,omitempty"`
}

// manualRegister 是手工登记的入口。
//
// 登记成功恒 200（含 deferred：登记本身成了，加种由轮询接着试）；错误信封只装
// 「这次没登记成」：输入形状（400）、撞车（409）、门禁（409）、上游/库（502/503）。
func (s *server) manualRegister(w http.ResponseWriter, r *http.Request) {
	if s.registrar == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "手工登记未装配（数据库不可用）")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRegistrationBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "读请求体失败: "+err.Error())
		return
	}
	request := manualRegistrationRequest{}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &request); err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "请求体不是 JSON 对象: "+err.Error())
			return
		}
	}

	result, err := s.registrar.Register(r.Context(), manual.Request{
		Magnet:   request.Magnet,
		JavdbURL: request.JavdbURL,
	})
	if err != nil {
		s.writeRegistrationError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, manualRegistrationBody{
		OK:       true,
		MovieID:  result.MovieID,
		Infohash: result.Infohash,
		Number:   result.Number,
		Outcome:  result.Outcome,
		Message:  result.Reason,
	})
}

// writeRegistrationError 把登记的错误翻成 §8.4 的状态码与稳定机器码。
func (s *server) writeRegistrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, manual.ErrBadMagnet), errors.Is(err, manual.ErrBadLink):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, manual.ErrNumberTaken):
		writeError(w, http.StatusConflict, "number_taken", err.Error())
	case errors.Is(err, manual.ErrInfohashTaken):
		writeError(w, http.StatusConflict, "infohash_taken", err.Error())
	case errors.Is(err, manual.ErrGated):
		writeError(w, http.StatusConflict, "gate_closed", err.Error())
	case strings.HasPrefix(err.Error(), "读 JavDB 详情"):
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())
	}
}
