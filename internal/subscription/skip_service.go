package subscription

import (
	"context"
	"errors"
	"fmt"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/ingest"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
)

// 「跳过 / 收下」的门面那一半（规格 §8.4 的两个端点，GLOSSARY「跳过 / 收下」）。
//
// 收下之所以在这里而不是在循环里：它是**人**按的一颗按钮，走的是同一个 ingest
// （ADR-0014：两条订阅共用一段流水线），所以门面也要拿着那一套依赖。

// 收下的两种回执（§8.4 的 `outcome`）。
const (
	// OutcomeAccepted 是「收下了」：任务已在（这一次建的，或本来就有的），版本进了队列。
	OutcomeAccepted = "accepted"
	// OutcomeNoMagnet 是「还没有磁力」：跳过的行留着、等出水了再点一次。
	//
	// 它不落 accepted_at：置上就等于把这一部从面板上抹掉，而它又永远进不了任务表——
	// 那是「磁力为空不落行」的反面，等于把用户的裁决执行成一次丢失。
	OutcomeNoMagnet = "no_magnet"
)

// AcceptResult 是一次「收下」的回执（§8.4：`{movie_id, outcome}`）。
type AcceptResult struct {
	MovieID string
	Outcome string
}

// Skips 读一条订阅最近 30 天被排掉的作品（GET /api/subscriptions/{id}/skips）。
//
// 订阅不存在是 ErrNotFound（404）：空列表与「没有这条订阅」在面板上是两件事。
func (s *Service) Skips(ctx context.Context, id int64) ([]Skip, error) {
	if _, err := s.store.Get(ctx, id); err != nil {
		return nil, err
	}
	return s.store.ListSkips(ctx, id)
}

// AcceptSkip 是「收下」：把一条跳过转成正常待办——建任务并入队（走同一 ingest）。
//
// 幂等靠**下游**，不靠一张已收下的表：重复收下时 ingest 看见任务已有版本就零写入，
// 加种那一步的 Ensure 也是幂等的（qBittorrent 的「已存在」）。所以第二次按下与第一次
// 回同一个 outcome，且不重复加种、不重复落版本。
func (s *Service) AcceptSkip(ctx context.Context, subscriptionID, skipID int64) (AcceptResult, error) {
	skip, err := s.store.GetSkip(ctx, subscriptionID, skipID)
	if err != nil {
		return AcceptResult{}, err
	}

	// 门禁不看（与添加/启停/回灌同一口径）：跳过行只可能由循环产出，而有跳过行就意味着
	// 系统跑过；暂停时收下只是把待办摆进队列，循环恢复后照常走完。

	snapshot, err := s.settings.Load(ctx)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("subscription: 读设置: %w", err)
	}
	javdbInput, err := s.settings.Input(ctx, "javdb")
	if err != nil {
		return AcceptResult{}, fmt.Errorf("subscription: 读 JavDB 设置: %w", err)
	}
	qbtInput, err := s.settings.Input(ctx, "qbittorrent")
	if err != nil {
		return AcceptResult{}, fmt.Errorf("subscription: 读 qBittorrent 设置: %w", err)
	}
	round := s.ingest.Round(snapshot, javdbInput.Text("token"), qbittorrent.ConfigFrom(qbtInput))

	// 走**同一条** ingest（ADR-0014）：与循环看到的每一件作品一模一样，不另写一条
	// 「从跳过表建任务」的路——那第二条路迟早与第一条漂移。
	if _, err := round.Ingest(ctx, ingest.Work{
		MovieID:        skip.MovieID,
		Number:         skip.Number,
		SubscriptionID: skip.SubscriptionID,
	}); err != nil {
		// 上游/库抖了：跳过行原样不动，用户再点一次（这也是它留在面板上的理由）。
		if isUpstream(err) {
			return AcceptResult{}, fmt.Errorf("%w：%s（%v）", ErrUpstream, skip.MovieID, err)
		}
		return AcceptResult{}, err
	}

	// 任务有没有版本才是「收下成不成立」的判据：Ingested 的零值既可能是「本来就有
	// 版本」，也可能是「磁力为空」，那两件事的处置正好相反。
	tracked, err := s.tasks.TaskHasVersion(ctx, skip.MovieID)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("subscription: 查 %s 的任务: %w", skip.MovieID, err)
	}
	if !tracked {
		return AcceptResult{MovieID: skip.MovieID, Outcome: OutcomeNoMagnet}, nil
	}

	// 「建任务并入队」的另一半：加种那一步与循环共用（先落库再加种，ADR-0004）。
	// 它捡的是**所有** add_pending 的版本（含上轮崩在落库与加种之间的那些），不只是
	// 这一条——那正是 ADR-0004 的自愈，不因为触发者是 HTTP 还是循环而变。
	// 加种失败不算收下失败：版本留着 add_pending，下一次循环接着试。
	if added, addErrs := round.AddPending(ctx); addErrs != nil {
		s.logger.Warn("收下：任务已建，加种还没成，下一轮循环接着试",
			"movie_id", skip.MovieID, "added", added, "errs", len(addErrs))
	}

	if err := s.store.AcceptSkip(ctx, subscriptionID, skipID); err != nil {
		return AcceptResult{}, err
	}
	return AcceptResult{MovieID: skip.MovieID, Outcome: OutcomeAccepted}, nil
}

// isUpstream 报告一个 ingest 错误是不是上游那一路（JavDB 磁力/详情）。
//
// ingest 的这条路上只有两种错误来源：库与上游。库抖了归 503（装配/库不可用），
// 上游读不到归 502（upstream_error）——面板上这两种病的处置不一样。
func isUpstream(err error) bool {
	var (
		transport *appapi.TransportError
		protocol  *appapi.ProtocolError
		auth      *appapi.AuthError
		api       *appapi.APIError
	)
	return errors.As(err, &transport) || errors.As(err, &protocol) ||
		errors.As(err, &auth) || errors.As(err, &api)
}
