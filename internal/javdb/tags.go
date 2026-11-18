package javdb

import (
	"context"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
)

// tagTypeZone 是标签词表的片库号：0 是本站主库（筛子要的三个类別都在那里）。
const tagTypeZone = 0

// Tags 读标签词表（`GET /api/v2/tags?type=0`，规格 §6.4 筛子）。
//
// 订阅筛子按**名字**查 id，不硬编码（ADR-0017）：上游改 id、加别名时，判据不该跟着
// 静默失效。这个端点 2026-11 起要 token（§13）——生产有这个 token，匿名调用会拿到
// JWTVerificationError（调用方按「这一轮不由标签判定」处置，不让整轮失败）。
func (i *Integration) Tags(ctx context.Context, token string) ([]appapi.Tag, error) {
	return i.client(token).Tags(ctx, tagTypeZone)
}
