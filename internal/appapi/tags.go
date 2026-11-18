package appapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// tagVocabPath 是标签词表端点。筛子要的两个类別名（「精選綜合」/「4 小時以上作品」）都在
// 这份词表里，而它们的 id 不写进代码（ADR-0017）：上游改 id、加别名时，判据不该跟着失效。
//
// 2026-11 起匿名一律 JWTVerificationError（§13）：生产带 token 不受影响，离线探针与测试
// 只能靠夹具。
const tagVocabPath = "/api/v2/tags"

// Tags 读标签词表（`GET /api/v2/tags?type=0`）。
//
// tagType 是片库号：0 是本站的主库，三条判据的类別都在那里。零值就是 0，所以调用方
// 通常只写 Tags(ctx)。
//
// 形态宽容到两种：`{"tags":[…]}`（与女优详情里的词表同形）与裸数组。宽容的理由是
// **这个端点本仓没有实测记录**——猜错形状时宁可多认一种，也不要在生产上让两条判据
// 静默失效；两种都解不出来则归 protocol（上游改版要人查，不是「词表是空的」）。
func (c *Client) Tags(ctx context.Context, tagType int) ([]Tag, error) {
	var raw json.RawMessage
	if err := c.GetJSON(ctx, tagVocabPath,
		url.Values{"type": {strconv.Itoa(tagType)}}, &raw); err != nil {
		return nil, fmt.Errorf("取标签词表: %w", err)
	}
	if len(raw) == 0 {
		return nil, &ProtocolError{Path: tagVocabPath, Message: "词表没有 data"}
	}

	tags, err := decodeTags(raw)
	if err != nil {
		return nil, &ProtocolError{Path: tagVocabPath, Message: err.Error()}
	}
	return tags, nil
}

// tagWire 是词表里一条的线格式（id/name 之外的字段一概不读）。
type tagWire struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// decodeTags 解两种形态里的任意一种，摊成 Tag 并丢掉认不出的条目。
func decodeTags(raw json.RawMessage) ([]Tag, error) {
	var envelope struct {
		Tags []tagWire `json:"tags"`
	}
	items := []tagWire(nil)
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Tags != nil {
		// 认出了对象形态（`{"tags":[]}` 也算：空词表是个合法的空，不是上游改版）。
		items = envelope.Tags
	} else {
		var bare []tagWire
		if bareErr := json.Unmarshal(raw, &bare); bareErr != nil {
			return nil, fmt.Errorf("词表既不是 {tags:[…]} 也不是数组: %v", bareErr)
		}
		items = bare
	}

	out := make([]Tag, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		name := strings.TrimSpace(item.Name)
		// 缺 id 或空名字的条目一概不认：拿空 id 去比对详情会把整批作品判成合集，
		// 拿空名字去查则永远查不到——两者都只会静默做错事。
		if id == "" || name == "" {
			continue
		}
		out = append(out, Tag{ID: id, Name: name})
	}
	return out, nil
}
