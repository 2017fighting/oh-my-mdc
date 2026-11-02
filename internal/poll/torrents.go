package poll

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// defaultTorrentsDir 是 .torrent 备份目录的兜底：生产上 /tmp 是 emptyDir，
// 进程重启后内容会没（规格 §5.2 的「字节瞬态走 /tmp」）。
func defaultTorrentsDir() string {
	return filepath.Join(os.TempDir(), "oh-my-mdc", "torrents")
}

// stashTorrent 把取回的 .torrent 字节落到备份路径，返回 payload 要存的那条路径。
//
// 为什么落盘而不是把字节塞进 payload 列：payload 是 text（磁力串或备份路径），
// 而加种与人工重试（#31）都要拿到原文才下得动。
//
// 原子写（temp + rename）：加种可能在任何时刻读到这个路径，读到半份种子
// 只会得到「infohash 对不上的红种」，而那种故障在日志里没有痕迹。
func stashTorrent(dir, infohash string, data []byte) (string, error) {
	if infohash == "" {
		return "", fmt.Errorf("poll: 存种子需要 infohash")
	}
	if len(data) == 0 {
		return "", fmt.Errorf("poll: 存种子需要字节")
	}
	if dir == "" {
		return "", fmt.Errorf("poll: 存种子需要备份目录（Runner 装配时一定给了）")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("poll: 建种子备份目录 %s: %w", dir, err)
	}

	path := filepath.Join(dir, infohash+".torrent")
	tmp, err := os.CreateTemp(dir, infohash+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("poll: 建种子临时文件: %w", err)
	}
	tmpPath := tmp.Name()
	// 失败路径上一定删掉临时文件：留着就是 /tmp 里永不回收的垃圾。
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("poll: 写种子临时文件: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("poll: 收尾种子临时文件: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("poll: 落种子到 %s: %w", path, err)
	}
	return path, nil
}

// candidateFromTorrent 把取种结果装成一条版本候选。
//
// payload 存的是**备份路径**：交付形态是 torrent，加种时要读字节（multipart）。
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

// deliveryFor 把一条版本翻成 qBittorrent 要的交付形态（磁力串 / 私有种子字节）。
//
// 两种形态共用一条版本行，但材料来源不同：磁力在 payload 里就是串，而私种
// **只有一份备份路径**（payload 存路径，规格 §4.1）—— 字节要从盘上读回来。
func deliveryFor(version task.Version, cfg qbittorrent.Config) (qbittorrent.Torrent, error) {
	delivery := qbittorrent.Torrent{
		Hash:     version.Infohash,
		Number:   version.Number,
		SavePath: cfg.DownloadRoot, // 显式 savepath（P7-2）；不设 autoTMM
	}

	if version.Delivery != task.DeliveryTorrent {
		delivery.Magnet = version.Payload
		return delivery, nil
	}

	data, err := os.ReadFile(version.Payload)
	if err != nil {
		return qbittorrent.Torrent{}, fmt.Errorf("读不到私种备份 %s（瞬态目录可能在重启后清空了）: %w", version.Payload, err)
	}
	delivery.Body = data
	delivery.Filename = version.Infohash + ".torrent"
	return delivery, nil
}
