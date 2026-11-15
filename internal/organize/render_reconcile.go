package organize

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 渲染对账（GLOSSARY，#41）：定时用库内刮削元数据重新渲染 movie.nfo，与盘上文件
// 逐槽位比对，漂移才重写。它是补译与字段修订的兜底回流；入库与强制重刮的落盘路径
// 不在这里（那两处照旧，规格 §7.1 NFO 约定）。周期挂靠对账循环（reconcile）。
//
// 三条不变式：
//
//   - **逐槽位比对，不是整文件比对**：Jellyfin 的 NFO writer 会改写同一文件
//     （lockdata/dateadded/playcount/watched/resume/fileinfo、演员头像换本地路径），
//     槽位值一个没变——整文件比对会两边互覆永动（#41 背景的生产实证）。
//   - **只看 omd 拥有的槽位集**：title/originaltitle/sorttitle/plot/premiered/
//     releasedate/runtime/rating/studio/director/set/trailer/tag/actor；
//     Jellyfin 私有段不模拟、不删除。演员只比 name 序列：thumb 会被 Jellyfin
//     本地化成缓存路径，那以后就是它的领域。
//   - **空转零写盘**：槽位值没变就不写（writeIfChanged 再兜一层底），
//     不触发 Jellyfin 重新扫描。
//
// 期望值不是手抄一份槽位推导，而是把库内元数据过一遍 Render 再解析回来：
// 渲染槽位的唯一真源是 nfo.go 的 Render，对账永远对着「整理一段今天会写出的样子」比。

// ReconcileProgress 是一轮渲染对账的可观察结果（日志、测试与 reconcile 的 Result 共用）。
type ReconcileProgress struct {
	// Gated 表示门禁没开（未初始化/已暂停/没配媒体库目录），这一轮没有做任何事。
	Gated bool
	// Reason 是跳过时给人看的一句话。
	Reason string
	// Scanned 是本轮看见的已入库任务数。
	Scanned int
	// Rewritten 是这一轮因漂移而重写 movie.nfo 的任务数。
	Rewritten int
	// Unchanged 是槽位值没变、零写盘的任务数（空转面）。
	Unchanged int
	// Skipped 是没法判或判不了的任务数：目录归别的任务、盘上没文件、盘上解不开。
	Skipped int
	// Failed 是判出漂移但重写失败的任务数。
	Failed int
}

// ReconcileRenders 跑一轮渲染对账：对每部已入库作品重新 Render，与盘上 movie.nfo
// 逐槽位比对，漂移才重写。
//
// 它不抢版本租约：对账不碰版本状态机，只读任务元数据、写一个文件；
// 文件写入是原子的（temp+rename），与整理段并发也只会看到完整的新旧其一。
func (r *Runner) ReconcileRenders(ctx context.Context) (ReconcileProgress, error) {
	// 门禁与整理段同一条路（同一句判词、同一个 media_root）：
	// 渲染对账是 reconcile 循环的一环，不该另起一套门禁。
	env, reason, err := r.environment(ctx)
	if err != nil {
		return ReconcileProgress{}, err
	}
	if reason != "" {
		return ReconcileProgress{Gated: true, Reason: reason}, nil
	}

	targets, err := r.store.FinishedTasks(ctx)
	if err != nil {
		return ReconcileProgress{}, err
	}

	progress := ReconcileProgress{}
	var errs []error
	for _, target := range targets {
		progress.Scanned++
		rewritten, err := r.reconcileRender(ctx, target, env)
		if err != nil {
			progress.Failed++
			errs = append(errs, err)
			continue
		}
		switch rewritten {
		case renderRewritten:
			progress.Rewritten++
		case renderUnchanged:
			progress.Unchanged++
		default:
			progress.Skipped++
		}
	}

	if progress.Scanned > 0 {
		r.logger.Debug("渲染对账结束",
			"scanned", progress.Scanned, "rewritten", progress.Rewritten,
			"unchanged", progress.Unchanged, "skipped", progress.Skipped, "failed", progress.Failed)
	}
	return progress, errors.Join(errs...)
}

// renderOutcome 是一部作品这一轮的结局。
type renderOutcome int

const (
	renderSkipped   renderOutcome = iota // 没法判（目录归别人/盘上没文件/解不开）：零动作
	renderUnchanged                      // 槽位值没变：零写盘
	renderRewritten                      // 有漂移：重写 movie.nfo
)

// reconcileRender 对账一部作品：渲染期望 → 读盘 → 逐槽位比 → 漂移才写。
func (r *Runner) reconcileRender(ctx context.Context, target task.FinishedTask, env environment) (renderOutcome, error) {
	// 目录归属：同番号撞了别的任务（规格 §7.2 碰撞行，驻留等人工）时，
	// 两边元数据不同——对账谁的重写都是给对方捣乱，跳过。
	owner, err := r.store.TaskOwningNumber(ctx, target.Number, target.MovieID)
	if err != nil {
		return renderSkipped, err
	}
	if owner != "" {
		r.logger.Debug("渲染对账：番号归别的任务，跳过",
			"movie_id", target.MovieID, "number", target.Number, "owner", owner)
		return renderSkipped, nil
	}

	meta, exists, err := r.store.Metadata(ctx, target.MovieID)
	if err != nil {
		return renderSkipped, err
	}
	if !exists {
		return renderSkipped, nil
	}

	want, body, err := expectedRender(meta, target.Number, env.translationExpected)
	if err != nil {
		return renderSkipped, fmt.Errorf("渲染对账 %s: %w", target.Number, err)
	}

	path := filepath.Join(env.mediaRoot, target.Number, NFOName)
	onDisk, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// 缺文件不重建：媒体库挂载抖动时空目录会让对账批量「复活」假条目，
		// 喂给 Jellyfin 一堆空壳。漂移兜底管「内容旧了」，不管「文件没了」（补录归 #43）。
		r.logger.Debug("渲染对账：盘上没有 movie.nfo，跳过", "number", target.Number, "path", path)
		return renderSkipped, nil
	}
	if err != nil {
		return renderSkipped, fmt.Errorf("渲染对账 %s: 读 %s: %w", target.Number, path, err)
	}

	var have nfoMovie
	if err := xml.Unmarshal(onDisk, &have); err != nil {
		// 解不开的文件不覆盖：里面可能有 Jellyfin 的私有段或人工修改，宁跳过勿损。
		r.logger.Warn("渲染对账：盘上 movie.nfo 解不开，跳过", "number", target.Number, "path", path, "err", err)
		return renderSkipped, nil
	}

	drifted := driftedSlots(want, have)
	if len(drifted) == 0 {
		return renderUnchanged, nil
	}

	written, err := writeIfChanged(path, body)
	if err != nil {
		return renderSkipped, fmt.Errorf("渲染对账 %s: 重写 %s: %w", target.Number, path, err)
	}
	if !written {
		// 槽位漂移但字节相同（理论上到不了这里）：writeIfChanged 的内容同不重写兜住。
		return renderUnchanged, nil
	}
	r.logger.Info("渲染对账：movie.nfo 落后于库内元数据，已重写",
		"number", target.Number, "slots", drifted)
	return renderRewritten, nil
}

// expectedRender 用库内元数据渲染一份：返回 omd 拥有的槽位（比对用）与
// 渲染字节（重写用）。
//
// 走 Render 而不是手抄推导：槽位怎么从三面翻成 NFO（回退、降级、排版）只有一处定义，
// 对账与整理段永远比着同一个标准、写着同一份字节。
func expectedRender(meta task.Metadata, number string, translationExpected bool) (nfoMovie, []byte, error) {
	clean := readMetadata(meta, number)
	if !translationExpected {
		// 与整理段同一条 P6-3 规则：翻译禁用时译文不参与排版（库里留着，只是不用）。
		// 不然对账写中文、整理写日文，一个开关来回两边就打架。
		clean.TitleZH, clean.OutlineZH = "", ""
	}
	body, err := Render(clean.Movie)
	if err != nil {
		return nfoMovie{}, nil, err
	}
	var slots nfoMovie
	if err := xml.Unmarshal(body, &slots); err != nil {
		return nfoMovie{}, nil, err
	}
	return slots, body, nil
}

// driftedSlots 逐槽位比对，返回漂移的槽位名（给人看的：日志与测试用）。
//
// 只比 omd 拥有的槽位；演员只比 name 序列（thumb 归 Jellyfin 本地化）。
func driftedSlots(want, have nfoMovie) []string {
	var drifted []string
	slot := func(name string, same bool) {
		if !same {
			drifted = append(drifted, name)
		}
	}

	slot("title", want.Title == have.Title)
	slot("originaltitle", want.OriginalTitle == have.OriginalTitle)
	slot("sorttitle", want.SortTitle == have.SortTitle)
	slot("plot", want.Plot == have.Plot)
	slot("runtime", want.Runtime == have.Runtime)
	slot("rating", want.Rating == have.Rating)
	slot("premiered", want.Premiered == have.Premiered)
	slot("releasedate", want.ReleasedDate == have.ReleasedDate)
	slot("studio", want.Studio == have.Studio)
	slot("director", want.Director == have.Director)
	slot("set", want.Set == have.Set)
	slot("trailer", want.Trailer == have.Trailer)
	slot("tag", slices.Equal(want.Tags, have.Tags))

	if len(want.Actors) != len(have.Actors) {
		slot("actor", false)
	} else {
		sameActors := true
		for i := range want.Actors {
			// thumb 不比：Jellyfin 会把头像 URL 换成本地缓存路径（生产实证）。
			if want.Actors[i].Name != have.Actors[i].Name {
				sameActors = false
				break
			}
		}
		slot("actor", sameActors)
	}
	return drifted
}
