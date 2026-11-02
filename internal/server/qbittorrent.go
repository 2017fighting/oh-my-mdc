package server

import (
	"net/http"

	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// autorunBody 是 autorun 前后对比的回执（GET 预览与 POST 落地的形状一致）。
//
// Before/After 是**完整**命令串：向导要原样展示给人对比，不做任何截断或摘要。
// Applied 只在 POST 且真的写了 qbt 时为 true；Skip 表示现值已含本项目 URL（幂等）。
type autorunBody struct {
	OK      bool   `json:"ok"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Skip    bool   `json:"skip"`
	Changed bool   `json:"changed"`
	Enabled bool   `json:"enabled"`
	Warning string `json:"warning,omitempty"`
	Applied bool   `json:"applied"`
}

// qbtAutorunPlan 读 qbt 的 autorun 现值并算出共存改写（**不改上游**）。
//
// 这是向导「保存前展示前后完整对比」的那一半：人来确认，才轮到下面的 apply。
func (s *server) qbtAutorunPlan(w http.ResponseWriter, r *http.Request) {
	integration, store, ok := s.qbtReady(w)
	if !ok {
		return
	}

	input, err := store.Input(r.Context(), "qbittorrent")
	if err != nil {
		s.writeSettingsError(w, err)
		return
	}

	plan, err := integration.Autorun(r.Context(), qbittorrent.ConfigFrom(input))
	if err != nil {
		s.writeQbtError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, autorunOf(plan))
}

// qbtAutorunApply 把确认过的计划落到 qbt（只写 autorun_program，ADR-0007）。
func (s *server) qbtAutorunApply(w http.ResponseWriter, r *http.Request) {
	integration, store, ok := s.qbtReady(w)
	if !ok {
		return
	}

	input, err := store.Input(r.Context(), "qbittorrent")
	if err != nil {
		s.writeSettingsError(w, err)
		return
	}

	plan, err := integration.ApplyAutorun(r.Context(), qbittorrent.ConfigFrom(input))
	if err != nil {
		s.writeQbtError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, autorunOf(plan))
}

func autorunOf(plan qbittorrent.AutorunPlan) autorunBody {
	return autorunBody{
		OK:      true,
		Before:  plan.Before,
		After:   plan.After,
		Skip:    plan.Skip,
		Changed: plan.Changed,
		Enabled: plan.Enabled,
		Warning: plan.Warning,
		Applied: plan.Applied,
	}
}

// qbtReady 把「集成没装配 / 设置域不可用」收敛成 503 db_unavailable。
func (s *server) qbtReady(w http.ResponseWriter) (*qbittorrent.Integration, *settings.Store, bool) {
	if s.qbt == nil || s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "qBittorrent 集成未装配（数据库不可用）")
		return nil, nil, false
	}
	return s.qbt, s.settings, true
}

// writeQbtError 把 qBittorrent 上游错翻成 §8.4 的稳定机器码与状态码。
//
// 分类由 qbittorrent.Classify 单点决定（与测试 chip 共用）：
//
//	auth / business → 400（要用户改凭据/输入，重试无用）
//	network / timeout / protocol → 502（等一会儿重试，或要查）
func (s *server) writeQbtError(w http.ResponseWriter, err error) {
	code := qbittorrent.Classify(err)
	status := http.StatusBadGateway
	if code == settings.CodeAuth || code == settings.CodeBusiness {
		status = http.StatusBadRequest
	}
	writeError(w, status, string(code), err.Error())
}
