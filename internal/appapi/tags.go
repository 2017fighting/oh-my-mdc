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

// TagVocabulary 读标签词表（`GET /api/v2/tags?type=N`）。
//
// 这一层是**传输**：它不要 token（Client 自己带着），只要片库号——`tagType` 就是
// 上游的 `type` 参数，0 是本站主库，筛子要的三个类別都在那里（调用方今天只传 0，
// 但把它写成参数是因为它是端点形状的一部分，不是常量）。
//
// 名字与 token 那一层刻意分开：拿 token 的那一层是 `javdb.Integration.TagVocabulary`
// （它自己钉死片库号），与 `subscription.VocabularySource` 签名逐位相同，于是两处不会
// 被写混成「谁该传 token」。
//
// 形态宽容到两种：`{"tags":[…]}`（与女优详情里的词表同形）与裸数组。宽容的理由是
// **这个端点本仓没有实测记录**——猜错形状时宁可多认一种，也不要在生产上让两条判据
// 静默失效；两种都解不出来则归 protocol（上游改版要人查，不是「词表是空的」）。
func (c *Client) TagVocabulary(ctx context.Context, tagType int) ([]Tag, error) {
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
//
// **两个形态的关键在这里**：真上游（javdb_rss 的 2026-10-04 捕获，`tags_type0.json`）
// 顶层是「分组」而不是标签——`{"tags":[{"category":"基本","category_id":"main",
// "tags":[{"id":"p","name":"可播放"},…]}]}`。按扁平形态去解只会把每个分组当成
// 缺 id 的条目丢掉，得到一张**空词表且不报错**的假象（2026-11-18 真 token 实测：n=0, err=nil），
// 于是 `compilation`/`long_form` 两条判据静默失效。因此 `Tags` 字段是必须的。
type tagWire struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Tags 是分组形态里嵌的那一层；扁平形态没有它。
	Tags []tagWire `json:"tags"`
}

// flatten 把两种形态摊平：分组先展开子项，扁平条目自己留着。
//
// 分组自身若带 id/name（上游将来两种混写）也算一条，不浪费。
func (w tagWire) flatten() []tagWire {
	if len(w.Tags) == 0 {
		return []tagWire{w}
	}
	out := make([]tagWire, 0, len(w.Tags)+1)
	if w.ID != "" && w.Name != "" {
		out = append(out, tagWire{ID: w.ID, Name: w.Name})
	}
	return append(out, w.Tags...)
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
	for _, group := range items {
		for _, item := range group.flatten() {
			id := strings.TrimSpace(item.ID)
			name := strings.TrimSpace(item.Name)
			// 缺 id 或空名字的条目一概不认：拿空 id 去比对详情会把整批作品判成合集，
			// 拿空名字去查则永远查不到——两者都只会静默做错事。
			if id == "" || name == "" {
				continue
			}
			out = append(out, Tag{ID: id, Name: name})
		}
	}
	return out, nil
}
