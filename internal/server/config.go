package server

import (
	"errors"
	"io"
	"net/http"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// maxConfigBody 是一次 PATCH 的请求体上限：设置项都是小字段，1 MiB 足够宽松。
const maxConfigBody = 1 << 20

// configBody 是 GET /api/config：八节设置 + 门禁判据。
//
// media_root_visible 是门禁的第二半（P4-2）：向导的解释卡要靠它说清「Jellyfin
// 已配置、但目录不在盘」这一种未初始化，不然三个绿勾配一句「还没初始化」没法读。
type configBody struct {
	OK               bool                             `json:"ok"`
	Initialized      bool                             `json:"initialized"`
	MediaRootVisible bool                             `json:"media_root_visible"`
	Sections         map[string]settings.SectionState `json:"sections"`
}

// sectionBody 是单节读写与解除配置的回执。门禁状态从 GET /api/config 读，
// 不在这里重复一份（保存完向导本来就要重读整份快照来点亮步骤条）。
type sectionBody struct {
	OK      bool   `json:"ok"`
	Section string `json:"section"`
	settings.SectionState
}

// testBody 是 POST /api/config/{section}/test 的回执（规格 §8.4）。
//
// 这里的 ok 是**测试结论**，不是信封：请求本身永远 200（除非节不存在或没保存过），
// 失败用 code 说明是哪一类。这样前端只读一个 ok 就能决定 chip 颜色。
type testBody struct {
	OK         bool              `json:"ok"`
	Code       settings.TestCode `json:"code,omitempty"`
	Detail     string            `json:"detail,omitempty"`
	DurationMS int64             `json:"duration_ms"`
	// javdb 的测试实际读到的登记清单 id（没读清单缺省）：前端用它给绿胶囊标深度。
	ListID string `json:"list_id,omitempty"`
}

// configAll 返回全部八节 + initialized（未初始化时整站只剩向导）。
func (s *server) configAll(w http.ResponseWriter, r *http.Request) {
	store, ok := s.settingsStore(w)
	if !ok {
		return
	}

	snapshot, err := store.Load(r.Context())
	if err != nil {
		s.writeSettingsError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, configBody{
		OK:               true,
		Initialized:      snapshot.Initialized,
		MediaRootVisible: snapshot.MediaRootVisible,
		Sections:         snapshot.Sections,
	})
}

// configSection 返回单节（带 revision 指纹）。
func (s *server) configSection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.settingsStore(w)
	if !ok {
		return
	}

	section, err := store.Section(r.Context(), r.PathValue("section"))
	if err != nil {
		s.writeSettingsError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, sectionBody{OK: true, Section: r.PathValue("section"), SectionState: section})
}

// patchSection 保存一节的部分字段：省略＝不改，敏感字段空串＝不改，
// revision 冲突 409，字段校验错 400（规格 §8.4）。
func (s *server) patchSection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.settingsStore(w)
	if !ok {
		return
	}

	name := r.PathValue("section")
	section, found := settings.Lookup(name)
	if !found {
		s.writeSettingsError(w, settings.ErrUnknownSection)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxConfigBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "读请求体失败: "+err.Error())
		return
	}

	patch, err := settings.DecodePatch(section, body)
	if err != nil {
		s.writeSettingsError(w, err)
		return
	}

	updated, err := store.Patch(r.Context(), name, patch)
	if err != nil {
		s.writeSettingsError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, sectionBody{OK: true, Section: name, SectionState: updated})
}

// deleteSection 删掉这一节的行＝解除配置；主线三件套被删掉后 initialized 变 false。
func (s *server) deleteSection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.settingsStore(w)
	if !ok {
		return
	}

	name := r.PathValue("section")
	removed, err := store.Delete(r.Context(), name)
	if err != nil {
		s.writeSettingsError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, sectionBody{OK: true, Section: name, SectionState: removed})
}

// testSection 跑一节的测试连接并写 last_test_*。
func (s *server) testSection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.settingsStore(w)
	if !ok {
		return
	}

	result, err := store.Test(r.Context(), r.PathValue("section"))
	if err != nil {
		s.writeSettingsError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, testBody{
		OK:         result.OK,
		Code:       result.Code,
		Detail:     result.Detail,
		DurationMS: result.Duration.Milliseconds(),
		ListID:     result.ListID,
	})
}

// settingsStore 把「数据库不可用」收敛成一处：没装配或查询失败都回 503 db_unavailable，
// 与 /readyz 的语义一致（设置全在库里，库不通就没有设置可谈）。
func (s *server) settingsStore(w http.ResponseWriter) (*settings.Store, bool) {
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "数据库未配置：设置只在 Postgres 里")
		return nil, false
	}
	return s.settings, true
}

// writeSettingsError 把设置域的错误翻成规格 §8.4 的状态码与稳定机器码。
func (s *server) writeSettingsError(w http.ResponseWriter, err error) {
	var (
		validation settings.ValidationError
		conflict   settings.RevisionConflictError
	)

	switch {
	case errors.Is(err, settings.ErrUnknownSection):
		writeError(w, http.StatusNotFound, "unknown_section", "没有这一节设置: "+err.Error())
	case errors.Is(err, settings.ErrNotDeletable):
		writeError(w, http.StatusBadRequest, "validation_error", "app 节是单行配置，只能改不能删")
	case errors.Is(err, settings.ErrNotTestable):
		writeError(w, http.StatusBadRequest, "validation_error", "这一节没有测试连接")
	case errors.Is(err, settings.ErrNotConfigured):
		writeError(w, http.StatusBadRequest, "validation_error", "先保存本步再测试连接")
	case errors.As(err, &conflict):
		writeError(w, http.StatusConflict, "revision_conflict",
			"这一节已被别处改过，刷新后再保存；当前 revision 是 "+conflict.Current)
	case errors.As(err, &validation):
		writeError(w, http.StatusBadRequest, "validation_error", validation.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())
	}
}
