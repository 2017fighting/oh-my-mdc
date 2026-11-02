package qbittorrent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// TorrentStatus 是 qBittorrent 对一条种子的权威读数（只含完成判定要用的四样）。
//
// 回调载荷只有 infohash（规格 §5.4）：进度、状态、落盘路径全要回 qbt 读，
// 本项目自己不算、不猜。progress/state 在 torrents/info，content_path 在
// torrents/properties（两处都有，优先 properties，见 Lookup）。
//
// tag/category 不在这里：它们是「这条种子归谁」的问题，判据是 DB 行与 Ensure，
// 不是完成条件（规格 §6.1 三条件里没有 tag）。
type TorrentStatus struct {
	Hash        string  `json:"hash"`
	Progress    float64 `json:"progress"`
	State       string  `json:"state"`
	ContentPath string  `json:"content_path"`
}

// Lookup 回读一条种子的权威状态；第二个返回值是「在不在 qbt 里」。
//
// 不在 qbt 里是**答案**而不是错误：对账据此判「种子已移除」（规格 §6.1）。
func (i *Integration) Lookup(ctx context.Context, cfg Config, hash string) (TorrentStatus, bool, error) {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if hash == "" {
		return TorrentStatus{}, false, ValidationError{Message: "回读种子状态需要 infohash"}
	}

	c, err := i.login(ctx, cfg)
	if err != nil {
		return TorrentStatus{}, false, err
	}

	props, found, err := c.properties(ctx, hash)
	if err != nil {
		return TorrentStatus{}, false, err
	}
	if !found {
		return TorrentStatus{}, false, nil
	}

	status := TorrentStatus{Hash: props.Hash, ContentPath: props.ContentPath}
	if status.Hash == "" {
		status.Hash = hash
	}

	info, foundInfo, err := c.torrentInfo(ctx, hash)
	if err != nil {
		return TorrentStatus{}, false, err
	}
	if foundInfo {
		status.Progress = info.Progress
		status.State = info.State
		if status.ContentPath == "" {
			status.ContentPath = info.ContentPath
		}
	}
	return status, true, nil
}

// Tagged 列出带某个 tag 的种子（对账按 mdc:in-progress 扫描的发现面）。
func (i *Integration) Tagged(ctx context.Context, cfg Config, tag string) ([]TorrentStatus, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil, ValidationError{Message: "按 tag 扫描需要 tag"}
	}

	c, err := i.login(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return c.torrentsInfo(ctx, url.Values{"tag": {tag}})
}

// torrentInfo 读一条种子（hashes=）；不在时 qbt 回空数组，不是错误。
func (c *client) torrentInfo(ctx context.Context, hash string) (TorrentStatus, bool, error) {
	statuses, err := c.torrentsInfo(ctx, url.Values{"hashes": {hash}})
	if err != nil {
		return TorrentStatus{}, false, err
	}
	for _, status := range statuses {
		if strings.EqualFold(status.Hash, hash) {
			return status, true, nil
		}
	}
	return TorrentStatus{}, false, nil
}

// torrentsInfo 是 torrents/info 的公共形状：调用方给筛选条件（hashes / tag）。
func (c *client) torrentsInfo(ctx context.Context, query url.Values) ([]TorrentStatus, error) {
	status, body, _, err := c.get(ctx, "/api/v2/torrents/info", query)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, c.statusError("torrents/info", status, body)
	}

	var statuses []TorrentStatus
	if err := json.Unmarshal(body, &statuses); err != nil {
		return nil, protocolError("torrents/info", "info 回执不是 JSON 数组: "+truncate(string(body), 120), err)
	}
	return statuses, nil
}
