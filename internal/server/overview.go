package server

import (
	"net/http"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/loops"
)

// overviewBody 是 GET /api/overview 的形状（规格 §8.4）。
type overviewBody struct {
	OK          bool       `json:"ok"`
	Initialized bool       `json:"initialized"`
	Paused      bool       `json:"paused"`
	Counts      countsBody `json:"counts"`
	Loops       loopsBody  `json:"loops"`
}

type countsBody struct {
	Downloading    int `json:"downloading"`
	Processing     int `json:"processing"`
	Finished       int `json:"finished"`
	NeedsAttention int `json:"needs_attention"`
}

// loopHealth 是一条循环的健康位：最近一次跑的时间与成败。
type loopHealth struct {
	LastRunAt *time.Time `json:"last_run_at"`
	LastOK    *bool      `json:"last_ok"`
}

type loopsBody struct {
	Poll      loopHealth `json:"poll"`
	Reconcile loopHealth `json:"reconcile"`
	Sidefx    loopHealth `json:"sidefx"`
}

// overview 汇总门禁与面板顶部要用的计数。
//
// counts 来自 task_state 视图（任务级状态不落列，ADR-0006）；需人工 = 失败任务 +
// 驻留副作用（规格 §8.2）。loops 来自进程内健康位：循环没起来/还没跑过时是空位。
func (s *server) overview(w http.ResponseWriter, r *http.Request) {
	store, ok := s.settingsStore(w)
	if !ok {
		return
	}

	snapshot, err := store.Load(r.Context())
	if err != nil {
		s.writeSettingsError(w, err)
		return
	}

	body := overviewBody{
		OK:          true,
		Initialized: snapshot.Initialized,
		Paused:      snapshot.Paused,
	}

	if s.tasks != nil {
		counts, err := s.tasks.Counts(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())
			return
		}
		body.Counts = countsBody{
			Downloading:    counts.Downloading,
			Processing:     counts.Processing,
			Finished:       counts.Finished,
			NeedsAttention: counts.NeedsAttention,
		}
	}
	if s.sidefx != nil {
		stuck, err := s.sidefx.StuckCount(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())
			return
		}
		body.Counts.NeedsAttention += stuck
	}

	body.Loops = loopsBody{
		Poll:      s.healthOf(loops.Poll),
		Reconcile: s.healthOf(loops.Reconcile),
		Sidefx:    s.healthOf(loops.Sidefx),
	}

	writeJSON(w, http.StatusOK, body)
}

// healthOf 把登记处的一条状态翻成对外的空位/实位。
func (s *server) healthOf(name string) loopHealth {
	if s.loops == nil {
		return loopHealth{}
	}
	status := s.loops.Health(name)
	return loopHealth{LastRunAt: status.LastRunAt, LastOK: status.LastOK}
}
