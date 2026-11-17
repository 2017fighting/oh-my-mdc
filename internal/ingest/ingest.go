// Package ingest 拥有「一件待办作品 → 一条版本 → 加进 qBittorrent」这一段（规格 §6.1）：
// 定源（M-Team 优先、JavDB 磁力兜底）→ 先落库 → 加种 → 置待删。
//
// 它有两个调用方：清单轮询（poll：想看清单全量扫出来的每一件）与女优订阅（#47：按水位线
// 追出来的每一件）。两个来源只负责「产出一批待办作品」，往下一律走这里——规格 §6.2 的
// 「与 poll 共用同一段 ingest」就是这一包：同一件事写两遍，两份必然漂移。
//
// 段内的规矩都是既有的，搬过来一行没改：
//
//   - 去重：任务已有版本就不再查源；同 infohash 已有版本行就零写入（规格 §6.1 去重行）；
//   - 定源：JavDB 有磁力才查 M-Team（限速意识）；M-Team 命中就取私有种子，取不到
//     （未配置/禁用/搜索无果/取种失败）就**同轮降级** JavDB 磁力；
//   - 顺序：版本行先以 add_pending=true 落库，qBittorrent 成功才清（ADR-0004）；
//   - 材料：私种备份读不到时回 M-Team 重取一次，重取不回来才进需人工（ADR-0011）；
//   - 置待删：加种成功即置 list_removal_pending，真正删清单项归旁路引擎（#26）。
//
// 一轮的形状是 Round：来源每轮造一个（磁力凭据、qbt 加种配置、M-Team 的启停判据都是
// 每轮读一次的东西），把每一件待办喂给它的 Ingest，最后用 AddPending 收口——那一步捡的
// 是**所有** add_pending 的版本，不只是这一轮新落的（上轮崩在落库与加种之间的那些）。
//
// 今天 AddPending 恒置「清单项待删」（清单轮询口径）。订阅来源的作品不在任何清单上，
// 要不要置这一位是 #47 的账——MarkAdded 的 listRemoval 参数已经是那个开关的入口。
package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/material"
	"github.com/2017fighting/oh-my-mdc/internal/mteam"
	"github.com/2017fighting/oh-my-mdc/internal/pin"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// Work 是一件待办作品：两个来源说得出的话就这么多。
type Work struct {
	// MovieID 是任务主键（上游的作品 id）。
	MovieID string
	// Number 是番号：M-Team 的搜索词、qBittorrent 的 tag、媒体库的目录名都靠它。
	Number string
	// ListID 是首次发现它的清单；订阅发现的作品不在任何清单上（规格 §6.4）。
	//
	// 今天它还必须有值：任务行的「首次发现来源」只有清单这一半（task.Candidate），
	// store 的校验也只放行手工登记的空值。订阅来源要落「清单空、订阅非空」的候选，
	// 那是 #47 把 subscription_id 接进候选形状的事，不是这一段的事。
	ListID string
}

// Ingested 是一件作品走完「定源 → 先落库」之后的事实。
//
// 来源据此记自己的账（日志、健康位、每轮限量），不据此改流程：怎么走由这一段自己定。
// 出错时它仍然说清走到了哪一步——已经查过的 Queried 照旧有效。
type Ingested struct {
	// Queried 表示真的查了这件作品的磁力候选（任务已有版本时为 false，规格 §6.1 去重行）。
	Queried bool
	// Inserted 表示这次真的新建了一条版本行。
	Inserted bool
	// Duplicate 表示定出了版本、但同 infohash 已有版本行：零写入（规格 §6.1 去重行）。
	Duplicate bool
	// Candidate 是这条版本（Inserted 为 true 时可用）。
	Candidate task.Candidate
	// SizeMiB 是这条版本的体积（通知里的展示单位；0 表示这个来源没给）。
	SizeMiB int64
	// Fallback 表示 M-Team 启用却取不到、这一条降级成了 JavDB 磁力（禁用不算，那根本没试）。
	Fallback bool
}

// Magnets 是磁力候选的上游（真实实现是 javdb.Integration 的 Magnets）。
//
// 凭据（JavDB token）由来源每轮读一次递进来：读清单项与查磁力用的是同一张票。
type Magnets interface {
	Magnets(ctx context.Context, token, movieID string) ([]appapi.Magnet, error)
}

// Adder 是加种接缝（真实实现是 qbittorrent.Integration）。
type Adder interface {
	Ensure(ctx context.Context, cfg qbittorrent.Config, torrent qbittorrent.Torrent) (qbittorrent.Outcome, error)
}

// Torrents 是 M-Team 取种接缝（真实实现是 mteam.Integration）。
//
// 只暴露「番号 → 一份私有种子」这一件事：搜索、选种、换链接、下载、算 infohash
// 都是 M-Team 自己的事，这里只决定**什么时候**问它（JavDB 有磁力且任务还没有版本）。
type Torrents interface {
	Resolve(ctx context.Context, apiKey, number string) (mteam.Torrent, error)
}

// Settings 是本段对设置域的全部要求：M-Team 的 api key。
//
// 启停（要不要问 M-Team）由来源每轮读好的设置快照给出，见 Round。
type Settings interface {
	Input(ctx context.Context, section string) (settings.TestInput, error)
}

// Options 装配一段 ingest。它是缝（两个来源共用的一套依赖），不是一轮：
// 每轮的东西走 Round。
type Options struct {
	Store  *task.Store
	Outbox *sidefx.Outbox
	// Settings 用于读 M-Team 凭据（只在真的要取种时才读）。
	Settings Settings
	// Magnets 是磁力候选的上游（JavDB）。
	Magnets Magnets
	Adder   Adder
	// Torrents 是可选来源（M-Team）；nil 时定源只走 JavDB 磁力。
	Torrents Torrents
	// Material 是交付材料仓库（私种备份的落盘与重取）。必填：每一件待办都要
	// 落盘与读回私种备份，装配漏了它就不该跑起来。
	Material *material.Store
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
	// Owner 是 claim 的持有者标识；空则用 hostname-pid。
	Owner string
}

// Ingester 是一段 ingest：缝在这里，一轮的账在 Round。
type Ingester struct {
	store    *task.Store
	outbox   *sidefx.Outbox
	settings Settings
	magnets  Magnets
	adder    Adder
	torrents Torrents
	material *material.Store
	logger   *slog.Logger
	owner    string
}

// New 构造一段 ingest。
func New(opts Options) *Ingester {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	owner := strings.TrimSpace(opts.Owner)
	if owner == "" {
		owner = task.DefaultOwner()
	}

	return &Ingester{
		store:    opts.Store,
		outbox:   opts.Outbox,
		settings: opts.Settings,
		magnets:  opts.Magnets,
		adder:    opts.Adder,
		torrents: opts.Torrents,
		material: opts.Material,
		logger:   logger,
		owner:    owner,
	}
}

// Round 是一轮的共用上下文：来源每轮读一次的东西（磁力凭据、qbt 加种配置、M-Team 的
// 启停判据）与这一轮新落版本的体积账。
//
// 它存在是为了两件事：M-Team 凭据一轮只读一次（不随作品数线性增长），以及加种那一步
// 要看得见「这一轮新落的版本各自多大」——通知里的 size_mib 只在定源那一刻拿得到。
type Round struct {
	ingester *Ingester
	token    string
	cfg      qbittorrent.Config
	access   *mteamAccess
	sizes    map[string]int64
}

// Round 开一轮。
//
// snapshot 是来源这一轮读好的设置快照：M-Team 的启停判据只从它来（未配置/禁用 → 零调用）。
// token 是磁力候选的凭据；cfg 是本轮加种用的 qBittorrent 配置。
func (i *Ingester) Round(snapshot settings.Snapshot, token string, cfg qbittorrent.Config) *Round {
	return &Round{
		ingester: i,
		token:    token,
		cfg:      cfg,
		access:   newMTeamAccess(i, snapshot),
		sizes:    make(map[string]int64),
	}
}

// Ingest 把一件待办作品走完「定源 → 先落库」。
//
// 返回的错误交给来源记进它自己的错误账：同一条清单/订阅的其余作品照样往下走。
// 加种不在这一步——先落库再加种是段级顺序（ADR-0004），见 AddPending。
func (r *Round) Ingest(ctx context.Context, work Work) (Ingested, error) {
	store := r.ingester.store

	hasVersion, err := store.TaskHasVersion(ctx, work.MovieID)
	if err != nil {
		return Ingested{}, err
	}
	if hasVersion {
		// P7-6：任务已有版本就不再查源（新版本场景由 v2 的重开语义负责）。
		return Ingested{}, nil
	}

	ingested := Ingested{Queried: true}
	magnets, err := r.ingester.magnets.Magnets(ctx, r.token, work.MovieID)
	if err != nil {
		return ingested, fmt.Errorf("ingest: 读 %s 的磁力: %w", work.MovieID, err)
	}

	best, ok := pin.Select(magnets)
	if !ok {
		// P7-3：磁力为空 → 零行落库，下轮重查。
		// 顺带一提，M-Team 也是到这里才考虑查的（限速意识：JavDB 没有就不问 M-Team）。
		return ingested, nil
	}

	// 先摆好磁力兜底的形状（规格 §6.1 定源行）：M-Team 任何一步失败都落它。
	candidate := task.Candidate{
		MovieID:  work.MovieID,
		Number:   work.Number,
		ListID:   work.ListID,
		Infohash: best.Hash,
		Source:   task.SourceJavDBMagnet,
		Delivery: task.DeliveryMagnet,
		Label:    labelFor(best),
		Payload:  appapi.MagnetLink(best.Hash, work.Number),
	}
	size := best.SizeMiB

	var fallbackReason string
	if r.access.enabled {
		// M-Team 优先（P5-2）：命中就只走种子这一条，不再落磁力版本。
		torrent, sourceErr := r.access.prepare(ctx, work.Number)
		if sourceErr != nil {
			// 降级（P5-3）：同轮落磁力版本。
			ingested.Fallback = true
			if mteam.IsNotFound(sourceErr) {
				// M-Team 上没有这部片是答案，不是故障：降级但不留告警红字。
				r.ingester.logger.Debug("M-Team 没有这部片，走磁力", "movie_id", work.MovieID, "number", work.Number)
			} else {
				fallbackReason = sourceErr.Error()
				r.ingester.logger.Warn("M-Team 取种失败，降级 JavDB 磁力",
					"movie_id", work.MovieID, "number", work.Number, "err", sourceErr)
			}
		} else {
			candidate = candidateFromTorrent(work, torrent)
			size = bytesToMiB(torrent.torrent.SizeBytes)
		}
	}

	inserted, err := store.SaveCandidate(ctx, candidate)
	if err != nil {
		return ingested, err
	}
	if !inserted {
		// 同 infohash 已有版本行：不重复加种、不重复置待删（规格 §6.1 去重行）。
		ingested.Duplicate = true
		return ingested, nil
	}

	ingested.Inserted = true
	ingested.Candidate = candidate
	ingested.SizeMiB = size
	r.sizes[candidate.Infohash] = size

	if candidate.Delivery == task.DeliveryMagnet && fallbackReason != "" {
		r.ingester.recordMTeamFallback(ctx, candidate, fallbackReason)
	}
	return ingested, nil
}
