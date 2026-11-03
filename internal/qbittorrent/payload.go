package qbittorrent

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrPayloadMissing 是「私种的备份路径读不到」。
//
// 它值得一个哨兵错误，因为字节是**瞬态**的：生产上备份目录是 /tmp 里的 emptyDir，
// 而 reloader 每次改配置都会滚动重启 pod（规格 §5.2）。读不到不代表版本坏了，
// 代表材料要补——调用方据此回上游重取一次，而不是把它当成一条普通的读盘失败。
var ErrPayloadMissing = errors.New("qbittorrent: 私种备份读不到")

// FromPayload 把版本行存的交付材料翻成一次加种请求（规格 §4.1 payload / §6.1 人工重试行）。
//
// payload 是两种形态之一，与 delivery 一一对应：
//
//   - `magnet`：payload 就是磁力串，原样交给 qBittorrent；
//   - `torrent`：payload 是 `.torrent` 的**备份路径**，字节从盘上读回来（multipart 那一支）。
//
// 它是「重新加种」的唯一实现：清单轮询与人工重试拿到的是同一列 payload，
// 就该走同一段代码——否则两条路对同一条版本会有两种理解。
//
// 私种那一支读不到字节时返回包着 ErrPayloadMissing 的错误：那是材料缺失，不是加种失败。
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
		return Torrent{}, fmt.Errorf("%w %s（瞬态目录可能在重启后清空了）: %w", ErrPayloadMissing, payload, err)
	}
	torrent.Body = data
	torrent.Filename = torrent.Hash + ".torrent"
	return torrent, nil
}
