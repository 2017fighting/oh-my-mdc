package ingest

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/material"
	"github.com/2017fighting/oh-my-mdc/internal/mteam"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// stashedTorrent 是取种 + 落盘之后的产物：字节已经有一份在盘上，payload 存它的路径。
type stashedTorrent struct {
	torrent mteam.Torrent
	path    string
}

// mteamAccess 是「这一轮要不要问 M-Team」的惰性判据。
//
// 门禁用设置快照判定（P5-1：未配置/禁用 → 零调用），api key 只在真的要用时才读：
// 大多数轮里清单项都已有版本，没必要每轮把凭据从设置域读出来；一轮里也只读一次，
// 不随作品数线性增长。
type mteamAccess struct {
	ingester *Ingester
	enabled  bool

	loaded bool
	key    string
	err    error
}

// newMTeamAccess 按设置快照构造这一轮的 M-Team 判据。
func newMTeamAccess(i *Ingester, snapshot settings.Snapshot) *mteamAccess {
	section, ok := snapshot.Sections[material.SectionMTeam]
	enabled := i.torrents != nil && ok && section.Configured && section.Enabled != nil && *section.Enabled
	return &mteamAccess{ingester: i, enabled: enabled}
}

// prepare 取一条种子并把它落到备份路径（规格 §5.2）。
//
// 配置读不出来（已启用却没 key 或设置域出问题）与取种失败同样处理：都降级磁力，
// 但原因会写进任务时间线 —— 否则「为什么这部片走的是磁力」在面板上无从追溯。
func (a *mteamAccess) prepare(ctx context.Context, number string) (stashedTorrent, error) {
	key, err := a.apiKey(ctx)
	if err != nil {
		return stashedTorrent{}, err
	}
	torrent, err := a.ingester.torrents.Resolve(ctx, key, number)
	if err != nil {
		return stashedTorrent{}, err
	}
	path, err := a.ingester.material.Stash(torrent.Infohash, torrent.Bytes)
	if err != nil {
		return stashedTorrent{}, err
	}
	return stashedTorrent{torrent: torrent, path: path}, nil
}

// apiKey 读一次 api key，并把这一轮的结果缓存下来。
func (a *mteamAccess) apiKey(ctx context.Context) (string, error) {
	if a.loaded {
		return a.key, a.err
	}
	a.loaded = true

	input, err := a.ingester.settings.Input(ctx, material.SectionMTeam)
	if err != nil {
		a.err = fmt.Errorf("读 M-Team 设置失败: %w", err)
		return a.key, a.err
	}
	a.key = strings.TrimSpace(input.Text("api_key"))
	if a.key == "" {
		a.err = errors.New("M-Team 已启用但 api_key 为空")
	}
	return a.key, a.err
}

// candidateFromTorrent 把取种结果装成一条版本候选。
//
// payload 存的是**备份路径**：交付形态是 torrent，加种时要读字节（multipart）。
// 落盘与「读不到就重取」都归 material 包（自动加种与人工重试共用那一处）。
func candidateFromTorrent(work Work, stashed stashedTorrent) task.Candidate {
	torrent := stashed.torrent
	return task.Candidate{
		MovieID:  work.MovieID,
		Number:   work.Number,
		ListID:   work.ListID,
		Infohash: torrent.Infohash,
		Source:   task.SourceMTeamTorrent,
		Delivery: task.DeliveryTorrent,
		Label:    torrent.Label,
		Payload:  stashed.path,
	}
}

// bytesToMiB 把搜索给出的字节体积折成 MiB（版本行与通知里的展示单位）。
func bytesToMiB(size int64) int64 {
	if size <= 0 {
		return 0
	}
	return size / (1 << 20)
}

// labelFor 是版本 label 的 best-effort 取值：磁力只带 cnsub，判得出「中文」。
//
// 词表只有一份定义（mteam.LabelChinese）：两条定源路径的 label 必须落在同一个词上。
func labelFor(magnet appapi.Magnet) string {
	if magnet.Cnsub {
		return mteam.LabelChinese
	}
	return ""
}

// recordMTeamFallback 把 M-Team 的失败记成一条告警事件（面板时间线看得到）。
//
// 它不把一轮判成失败：降级是设计好的路径，M-Team 挂了不该让循环健康位变红。
// 但它必须留下痕迹——否则「为什么这部片走的是磁力」在面板上无从追溯。
func (i *Ingester) recordMTeamFallback(ctx context.Context, candidate task.Candidate, reason string) {
	if err := i.store.AppendEvent(ctx, task.Event{
		MovieID:  candidate.MovieID,
		Infohash: candidate.Infohash,
		Kind:     task.EventAlert,
		Phase:    string(task.PhaseDownloading),
		Reason:   "M-Team 定源失败，已降级 JavDB 磁力：" + reason,
	}); err != nil {
		i.logger.Error("ingest: 记 M-Team 降级事件失败", "movie_id", candidate.MovieID, "err", err)
	}
}
