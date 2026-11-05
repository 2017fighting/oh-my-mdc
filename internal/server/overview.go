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
	Downloading int `json:"downloading"`
	Processing  int `json:"processing"`
	Finished    int `json:"finished"`
	// Failed 是全部版本都失败的任务数（与面板的「需人工」筛选同一个口径）。
	Failed int `json:"failed"`
}

// loopHealth 是一条循环的健康位：最近一次跑的时间与成败。
type loopHealth struct {
	LastRunAt *time.Time `json:"last_run_at"`
	LastOK    *bool      `json:"last_ok"`
	// LastSuccessAt / RunningSince / Iterations 是登记处里一直存着的三样：
	// 「上次成功」（连败判据）、「正在跑」（本轮起跑时间）与迭代计数。
	// 面板的循环健康与 §9 指标同源，都是进程内健康位。
	LastSuccessAt *time.Time    `json:"last_success_at"`
	RunningSince  *time.Time    `json:"running_since"`
	Iterations    iterationsBody `json:"iterations"`
}

type iterationsBody struct {
	OK    uint64 `json:"ok"`
	Error uint64 `json:"error"`
}

type loopsBody struct {
	Poll      loopHealth `json:"poll"`
	Reconcile loopHealth `json:"reconcile"`
	Sidefx    loopHealth `json:"sidefx"`
}

// overview 汇总门禁与面板顶部要用的计数。
//
// counts 来自 task_state 视图（任务级状态不落列，ADR-0006）；它只回答「多少任务在某状态」，
// 不回答「多少件事要动手」——后者是 GET /api/attention 的行数，两个口径分开报，
// 免得同一个屏幕上出现两个互相矛盾的「需人工」。loops 来自进程内健康位：
// 循环没起来/还没跑过时是空位。
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
			Downloading: counts.Downloading,
			Processing:  counts.Processing,
			Finished:    counts.Finished,
			Failed:      counts.Failed,
		}
	}
	if s.sidefx != nil {
		// 只校验副作用域可用（读不到就 503），不把 stuck 数拼进 counts：
		// 那会把「任务数」与「要动手的事」混成一个数。
		if _, err := s.sidefx.StuckCount(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "db_unavailable", err.Error())
			return
		}
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
