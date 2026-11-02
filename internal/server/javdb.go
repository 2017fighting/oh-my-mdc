package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/javdb"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// maxJavDBBody 是一次建清单请求的上限：name/description 都是小字段。
const maxJavDBBody = 64 << 10

// javdbListBody 是探测结果里的一张清单（只出判定与展示要用的字段）。
//
// ID 是身份，Name 只是快照（规格 §5.1）：前端按 id 选中登记。
type javdbListBody struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	IsDefault   bool   `json:"is_default"`
	MoviesCount int    `json:"movies_count"`
}

// javdbListsBody 是 GET /api/javdb/lists 的回执。
type javdbListsBody struct {
	OK    bool            `json:"ok"`
	Lists []javdbListBody `json:"lists"`
}

// javdbCreatedBody 是 POST /api/javdb/lists 的回执：新清单 id 就在 data.id。
type javdbCreatedBody struct {
	OK   bool   `json:"ok"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// createListRequest 是 POST /api/javdb/lists 的请求体。
type createListRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// javdbLists 探测用户自建的清单（向导「读取已有清单」）。
//
// 需要已保存 token（javdb 节）。没保存 → 400；上游错按 §8.4 的错误码归类。
func (s *server) javdbLists(w http.ResponseWriter, r *http.Request) {
	integration, store, ok := s.javdbReady(w)
	if !ok {
		return
	}

	input, err := store.Input(r.Context(), "javdb")
	if err != nil {
		s.writeSettingsError(w, err)
		return
	}

	lists, err := integration.Lists(r.Context(), input.Text("token"))
	if err != nil {
		s.writeJavDBError(w, err)
		return
	}

	body := javdbListsBody{OK: true, Lists: make([]javdbListBody, 0, len(lists))}
	for _, list := range lists {
		body.Lists = append(body.Lists, javdbListBody{
			ID:          list.ID,
			Name:        list.Name,
			IsDefault:   list.IsDefault,
			MoviesCount: list.MoviesCount,
		})
	}
	writeJSON(w, http.StatusOK, body)
}

// javdbCreateList 代建一张清单（向导「一键创建」），把 data.id 交回登记。
//
// 同名拒/名字过短等业务错原样透出（规格 P2-2：用户要能看见「同名清单」）。
func (s *server) javdbCreateList(w http.ResponseWriter, r *http.Request) {
	integration, store, ok := s.javdbReady(w)
	if !ok {
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxJavDBBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "读请求体失败: "+err.Error())
		return
	}
	var request createListRequest
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &request); err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "请求体不是 JSON 对象: "+err.Error())
			return
		}
	}
	name := strings.TrimSpace(request.Name)

	input, err := store.Input(r.Context(), "javdb")
	if err != nil {
		s.writeSettingsError(w, err)
		return
	}

	// 空 name/description 的默认值由集成层兜（单点），这里只传原值。
	id, err := integration.CreateList(r.Context(), input.Text("token"), name, request.Description)
	if err != nil {
		s.writeJavDBError(w, err)
		return
	}

	// name 是请求值（集成层已兜默认）；清单名字的快照以它为准。
	if name == "" {
		name = javdb.DefaultWantWatchListName
	}
	writeJSON(w, http.StatusOK, javdbCreatedBody{OK: true, ID: id, Name: name})
}

// javdbReady 把「集成没装配 / 设置域不可用」收敛成 503 db_unavailable。
func (s *server) javdbReady(w http.ResponseWriter) (*javdb.Integration, *settings.Store, bool) {
	if s.javdb == nil || s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "JavDB 集成未装配（数据库不可用）")
		return nil, nil, false
	}
	return s.javdb, s.settings, true
}

// writeJavDBError 把 JavDB 上游错翻成 §8.4 的稳定机器码与状态码。
//
// 分类由 appapi.Classify 单点决定（与测试 chip 共用）；这里只负责 code→status：
//
//	auth/business → 400（要用户改凭据/输入，重试无用）
//	protocol      → 502（服务端行为与预期不一致，要查）
//	network       → 502（等一会儿重试）
func (s *server) writeJavDBError(w http.ResponseWriter, err error) {
	code := appapi.Classify(err)
	status := http.StatusBadGateway
	if code == appapi.CodeAuth || code == appapi.CodeBusiness {
		status = http.StatusBadRequest
	}
	writeError(w, status, code, javdbErrorText(err))
}

// javdbErrorText 给业务错留上游原话（同名清单、名字过短…）：那些文案是写给用户的。
func javdbErrorText(err error) string {
	var apiErr *appapi.APIError
	if errors.As(err, &apiErr) && apiErr.IsBusiness() && apiErr.Message != "" {
		return apiErr.Message
	}
	return err.Error()
}
