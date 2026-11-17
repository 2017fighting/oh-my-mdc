package ingest

import (
	"context"
	"errors"
	"fmt"

	"github.com/2017fighting/oh-my-mdc/internal/material"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// maxAddsPerRound 是一轮最多加多少条：挡住异常积压把一轮拖成无尽循环。
const maxAddsPerRound = 500

// materialAlertPrefix 是「加种材料缺失」告警的固定开头。
//
// 它有两个读者：这里写（kind=alert），以及 HasAlert 判据（同一版本只写一次）。
// 瞬时失败会每轮重来（上游迟早回来），但时间线不该每 15 分钟多一条同样的红字。
const materialAlertPrefix = "加种材料缺失"

// AddPending 是「加种 → 置待删」那一步：把 add_pending 的版本逐条交给 qBittorrent。
//
// 捡的是**所有**待加种的版本，不只是这一轮新落的：上一轮崩在落库与加种之间的那些
// 也在这里补上（ADR-0004 的自愈）。来源读上游失败时照样该调它——那是独立于上游的活。
//
// 先列名单再逐条 claim：某一条加种失败、租约放回之后，同一轮不会反复捡到它。
// 每条都先 claim 再干活：claim 抢输不是失败（ErrClaimLost），只是这次轮不到本进程。
func (r *Round) AddPending(ctx context.Context) (int, []error) {
	pending, err := r.ingester.store.PendingAdds(ctx, maxAddsPerRound)
	if err != nil {
		return 0, []error{err}
	}

	added := 0
	var errs []error
	for _, candidate := range pending {
		version, err := r.ingester.store.Claim(ctx, candidate.Infohash, r.ingester.owner)
		if errors.Is(err, task.ErrClaimLost) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}

		ok, err := r.addOne(ctx, version)
		if err != nil {
			errs = append(errs, err)
		}
		if ok {
			added++
		}
	}
	return added, errs
}

// addOne 加一条版本：进程内锁 → 备好交付材料 → Ensure → 成功清 pending/置待删/入箱通知。
//
// 它是 Round 的方法而不是 Ingester 的：加种要的 qbt 配置与「这一轮新落的版本体积」
// （通知里带的 size_mib）都是这一轮的账。
func (r *Round) addOne(ctx context.Context, version task.Version) (bool, error) {
	i := r.ingester
	cfg := r.cfg
	unlock := i.store.Lock(version.Infohash)
	defer unlock()

	delivery, err := i.material.Deliver(ctx, version, cfg)
	if err != nil {
		// 材料拿不到：永久缺失进需人工，瞬时失败保 pending 下轮再试（两条路都不让
		// 这一轮变红——那正是 #36 里「版本永久卡住 + LoopStalled 常红」的现场）。
		i.handleMaterialError(ctx, version, err)
		return false, nil
	}
	if delivery.Material != version.Payload {
		// 重取落到了新路径：payload 是下次读材料的唯一线索，必须写回库。
		if err := i.store.SetPayload(ctx, version.Infohash, i.owner, delivery.Material); err != nil {
			if errors.Is(err, task.ErrClaimLost) {
				return false, nil
			}
			return false, err
		}
	}

	outcome, err := i.adder.Ensure(ctx, cfg, delivery.Torrent)
	if err != nil {
		// 加种失败：保留 add_pending，放掉租约，记一条告警，下轮重试。
		i.abandonAdd(ctx, version, fmt.Sprintf("加种失败：%v", err))
		return false, fmt.Errorf("ingest: 加种 %s: %w", version.Infohash, err)
	}

	// 通知入箱（收编同发，规格 §5.6）。执行归旁路引擎（#26）。
	//
	// 顺序是「先入箱、后清 pending」：入箱靠 (kind,target_key) 的部分唯一索引幂等，
	// 崩在这两步之间时下一轮会重新 Ensure（此时是 AlreadyTracked）并重试一次入箱
	// （no-op），所以通知不会丢；反过来先清 pending 就会永久丢掉这条通知。
	payload := map[string]any{
		"movie_id": version.MovieID,
		"infohash": version.Infohash,
		"number":   version.Number,
		"label":    version.Label,
		"source":   string(version.Source),
	}
	if size, ok := r.sizes[version.Infohash]; ok {
		payload["size_mib"] = size
	}
	if err := i.outbox.EnqueueNotify(ctx, sidefx.EventDownloadStarted, version.Infohash, payload); err != nil {
		if releaseErr := i.store.Release(ctx, version.Infohash, i.owner); releaseErr != nil {
			i.logger.Error("ingest: 入箱失败后释放租约失败", "infohash", version.Infohash, "err", releaseErr)
		}
		return false, err
	}

	if err := i.store.MarkAdded(ctx, version.Infohash, i.owner, true); err != nil {
		if errors.Is(err, task.ErrClaimLost) {
			// 租约已经过期并被别人接手：这次的加种结果不该覆盖当前占用者的账。
			return false, nil
		}
		return false, err
	}

	// 事件只是时间线：写不进去不该让一次已经成功的加种变成失败。
	if err := i.store.AppendEvent(ctx, task.Event{
		MovieID:  version.MovieID,
		Infohash: version.Infohash,
		Kind:     task.EventSideEffect,
		Reason:   fmt.Sprintf("已加种（%s），下载开始通知入箱", outcome),
	}); err != nil {
		i.logger.Error("ingest: 记加种事件失败", "infohash", version.Infohash, "err", err)
	}
	return true, nil
}

// abandonAdd 放弃这一次加种：放回租约、记一条告警，保留 add_pending 等下轮重试。
func (i *Ingester) abandonAdd(ctx context.Context, version task.Version, reason string) {
	if err := i.store.Release(ctx, version.Infohash, i.owner); err != nil {
		i.logger.Error("ingest: 释放失败版本的租约失败", "infohash", version.Infohash, "err", err)
	}
	i.recordAlert(ctx, version, reason)
}

// abandonAddOnce 与 abandonAdd 同一处置，但告警**只记一次**（HasAlert 判据，重启后也认）。
//
// 用在「材料缺失、上游连不上」这条会自动重来的路上：第一次就够说明问题了，
// 之后同一句话每轮再写一遍只是把时间线填满。
func (i *Ingester) abandonAddOnce(ctx context.Context, version task.Version, reason string) {
	if err := i.store.Release(ctx, version.Infohash, i.owner); err != nil {
		i.logger.Error("ingest: 释放失败版本的租约失败", "infohash", version.Infohash, "err", err)
	}
	alerted, err := i.store.HasAlert(ctx, version.Infohash, materialAlertPrefix)
	if err != nil {
		i.logger.Error("ingest: 查材料缺失告警失败", "infohash", version.Infohash, "err", err)
		return
	}
	if alerted {
		i.logger.Warn("ingest: 加种材料仍然缺失", "movie_id", version.MovieID,
			"infohash", version.Infohash, "reason", reason)
		return
	}
	i.recordAlert(ctx, version, reason)
}

// handleMaterialError 处置「这一条版本的交付材料拿不到」。
//
//   - 永久（站点上已没有这条种子、凭据不对）→ 落 failed(下载阶段) 进需人工区：
//     这条路重试变不出来，留在 pending 里只会让每一轮都以 error 收尾；
//   - 瞬时（连不上 M-Team、设置域读不出来）→ 保留 pending 下轮再试，且**不**把这
//     一轮判成失败：M-Team 挂了不该让循环健康位变红（与降级磁力同一口径）。
func (i *Ingester) handleMaterialError(ctx context.Context, version task.Version, err error) {
	if material.Permanent(err) {
		i.failAdd(ctx, version, err.Error())
		return
	}
	i.abandonAddOnce(ctx, version, materialAlertPrefix+"，下轮再试："+err.Error())
}

// failAdd 把一条加不了种的版本落进「需要人工」（规格 §8.2 的 failed(phase, reason)）。
//
// 清 pending 是这件事的重点：不清就每轮都把同一条材料缺失重跑一遍，而它不会自己好。
func (i *Ingester) failAdd(ctx context.Context, version task.Version, reason string) {
	if err := i.store.MarkAddFailed(ctx, version.Infohash, i.owner, reason); err != nil {
		if errors.Is(err, task.ErrClaimLost) {
			return
		}
		i.logger.Error("ingest: 落加种失败状态失败", "infohash", version.Infohash, "err", err)
		return
	}

	// 事件只是时间线：写不进去不该让一次已经落到库里的失败变成两次。
	if err := i.store.AppendEvent(ctx, task.Event{
		MovieID:   version.MovieID,
		Infohash:  version.Infohash,
		Kind:      task.EventStateChange,
		FromState: string(task.StatusDownloading),
		ToState:   string(task.StatusFailed),
		Phase:     string(task.PhaseDownloading),
		Reason:    reason,
	}); err != nil {
		i.logger.Error("ingest: 记加种失败事件失败", "infohash", version.Infohash, "err", err)
	}
	i.logger.Warn("ingest: 交付材料拿不回来，版本已进需人工",
		"movie_id", version.MovieID, "infohash", version.Infohash, "reason", reason)
}

// recordAlert 记一条失败事件（时间线 + 后续告警判据都靠它）。
func (i *Ingester) recordAlert(ctx context.Context, version task.Version, reason string) {
	if err := i.store.AppendEvent(ctx, task.Event{
		MovieID:  version.MovieID,
		Infohash: version.Infohash,
		Kind:     task.EventAlert,
		Phase:    string(task.PhaseDownloading),
		Reason:   reason,
	}); err != nil {
		i.logger.Error("ingest: 记加种失败事件失败", "infohash", version.Infohash, "err", err)
	}
}
