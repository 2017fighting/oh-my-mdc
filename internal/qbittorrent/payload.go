package qbittorrent

import (
	"fmt"
	"os"
	"strings"
)

// FromPayload 把版本行存的交付材料翻成一次加种请求（规格 §4.1 payload / §6.1 人工重试行）。
//
// payload 是两种形态之一，与 delivery 一一对应：
//
//   - `magnet`：payload 就是磁力串，原样交给 qBittorrent；
//   - `torrent`：payload 是 `.torrent` 的**备份路径**，字节从盘上读回来（multipart 那一支）。
//
// 它是「重新加种」的唯一实现：清单轮询与人工重试拿到的是同一列 payload，
// 就该走同一段代码——否则两条路对同一条版本会有两种理解。
func FromPayload(cfg Config, infohash, number, delivery, payload string) (Torrent, error) {
	torrent := Torrent{
		Hash:     strings.ToLower(strings.TrimSpace(infohash)),
		Number:   strings.TrimSpace(number),
		SavePath: cfg.DownloadRoot, // 显式 savepath（规格 §5.4）；不设 autoTMM
	}

	if delivery != "torrent" {
		torrent.Magnet = strings.TrimSpace(payload)
		return torrent, nil
	}

	data, err := os.ReadFile(payload)
	if err != nil {
		return Torrent{}, fmt.Errorf("读不到私种备份 %s（瞬态目录可能在重启后清空了）: %w", payload, err)
	}
	torrent.Body = data
	torrent.Filename = torrent.Hash + ".torrent"
	return torrent, nil
}
