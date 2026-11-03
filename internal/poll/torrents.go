package poll

import (
	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// candidateFromTorrent 把取种结果装成一条版本候选。
//
// payload 存的是**备份路径**：交付形态是 torrent，加种时要读字节（multipart）。
// 落盘与「读不到就重取」都归 material 包（poll 与人工重试共用那一处）。
func candidateFromTorrent(item appapi.ListItem, listID string, stashed stashedTorrent) task.Candidate {
	torrent := stashed.torrent
	return task.Candidate{
		MovieID:  item.MovieID,
		Number:   item.Number,
		ListID:   listID,
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
