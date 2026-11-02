package server

import (
	"net/http"
	"time"
)

// overviewBody 是 GET /api/overview 的形状（规格 §8.4）。
//
// 这是骨架：counts 要等任务域落地（#23）才有真数据，loops 要等三条循环起来
// （#26）才有 last_run_at。形状先定，前端照它接。
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

	writeJSON(w, http.StatusOK, overviewBody{
		OK:          true,
		Initialized: snapshot.Initialized,
		Paused:      snapshot.Paused,
		// 任务域表在 #23 落地；在那之前计数恒为 0，而不是假装查成功。
		Counts: countsBody{},
		Loops:  loopsBody{},
	})
}
