package appapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Magnet 是一条磁力候选（GET /api/v1/movies/{movie_id}/magnets，规格 §5.1）。
//
// 身份是 infohash；`size` 上游给的是 MiB。cnsub/hd 是定选策略的两个排序键，
// created_at 是最后的稳定性键（规格 §6.1 定源行）。
type Magnet struct {
	Hash      string
	Cnsub     bool
	HD        bool
	SizeMiB   int64
	CreatedAt time.Time
	Name      string
}

// magnetPayload 是磁力接口的常规形态：data.magnets。
//
// 实测形态来自 javdb_rss 的移植；同时接受 data 直接是数组的形态（见 Magnets），
// 因为上游没有把这一层的形状写成契约。
type magnetPayload struct {
	Magnets []magnetRow `json:"magnets"`
}

type magnetRow struct {
	ID        string `json:"id"`
	MovieID   string `json:"movie_id"`
	Hash      string `json:"hash"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	HD        bool   `json:"hd"`
	Cnsub     bool   `json:"cnsub"`
	CreatedAt string `json:"created_at"`
}

// Magnets 读一部作品的磁力候选。匿名可读；token 在场则携带（规格 §5.1）。
//
// 空数组是合法答案（清单项没有磁力）：poll 对它的处置是零行落库、下轮重查，
// 所以这里不把空当成错误。
func (c *Client) Magnets(ctx context.Context, movieID string) ([]Magnet, error) {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return nil, &ValidationError{Message: "磁力候选需要 movie_id"}
	}

	path := "/api/v1/movies/" + url.PathEscape(movieID) + "/magnets"

	// 先接住原始 data：既能是 {"magnets":[…]}，也能直接是 […]。两种都认，
	// 免得上游换了一层包装就把整条磁力路径打死。
	var raw json.RawMessage
	if err := c.GetJSON(ctx, path, nil, &raw); err != nil {
		return nil, err
	}

	rows, err := decodeMagnetRows(raw)
	if err != nil {
		return nil, &ProtocolError{Path: path, Message: err.Error()}
	}

	magnets := make([]Magnet, 0, len(rows))
	for _, row := range rows {
		hash := strings.ToLower(strings.TrimSpace(row.Hash))
		if hash == "" {
			// 没有 infohash 的候选不是候选：它是身份，缺了就没法去重、没法加种。
			continue
		}
		magnets = append(magnets, Magnet{
			Hash:      hash,
			Cnsub:     row.Cnsub,
			HD:        row.HD,
			SizeMiB:   row.Size,
			CreatedAt: parseMagnetTime(row.CreatedAt),
			Name:      strings.TrimSpace(row.Name),
		})
	}
	return magnets, nil
}

func decodeMagnetRows(raw json.RawMessage) ([]magnetRow, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var payload magnetPayload
	if err := json.Unmarshal(raw, &payload); err == nil {
		return payload.Magnets, nil
	}

	var rows []magnetRow
	if err := json.Unmarshal(raw, &rows); err == nil {
		return rows, nil
	}
	return nil, fmt.Errorf("磁力负载既不是 {magnets:[…]} 也不是 […]: %s", truncate(string(raw), 200))
}

// parseMagnetTime 尽量把 created_at 解成时间；解不出来就返回零值。
//
// 定选策略只在「体积相同」时用它做最后的排序，解不出来不该让整轮 poll 失败。
// 常见写法是 RFC3339 与纯日期两种，另留一种带空格的日期时间。
func parseMagnetTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

// MagnetLink 把一条候选拼成 qBittorrent 能收的磁力串。
//
// 磁力接口只给 hash（规格 §5.1）；交付形态是磁力时由本项目补上 xt 与 dn。
// 不塞 tracker：候选的 tracker 集合不在 API 里，DHT 是 qbt 自己的事。
func MagnetLink(hash, number string) string {
	link := "magnet:?xt=urn:btih:" + strings.ToLower(strings.TrimSpace(hash))
	if number = strings.TrimSpace(number); number != "" {
		link += "&dn=" + url.QueryEscape(number)
	}
	return link
}
