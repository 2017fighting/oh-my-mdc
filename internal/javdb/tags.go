package javdb

import (
	"context"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
)

// tagTypeZone 是标签词表的片库号：0 是本站主库（筛子要的三个类別都在那里）。
const tagTypeZone = 0

// TagVocabulary 读标签词表（`GET /api/v2/tags?type=0`，规格 §6.4 筛子）。
//
// 订阅筛子按**名字**查 id，不硬编码（ADR-0017）：上游改 id、加别名时，判据不该跟着
// 静默失效。这个端点 2026-11 起要 token（§13）——生产有这个 token，匿名调用会拿到
// JWTVerificationError（调用方按「这一轮不由标签判定」处置，不让整轮失败）。
//
// 签名与 `subscription.VocabularySource` 逐位相同（ctx + token）：筛子那一层拿的就是这一
// 份，片库号钉在这里（0 = 本站主库）。传输层的同名方法是 `appapi.Client.TagVocabulary`，
// 它要的是片库号、不要 token——两者一眼就能分开。
func (i *Integration) TagVocabulary(ctx context.Context, token string) ([]appapi.Tag, error) {
	return i.client(token).TagVocabulary(ctx, tagTypeZone)
}
