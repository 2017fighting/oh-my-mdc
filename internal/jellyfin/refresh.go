package jellyfin

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// RefreshScope 是一次刷新实际走到的范围。
//
// 三个值对应三种结局：目录级（正常）、全库（目录级被拒后的降级）、跳过（目录不在盘，
// 一个请求都没发）。它只说「打到哪」，不说「扫到了什么」——见 RefreshResult.Accepted。
type RefreshScope string

const (
	// ScopeDirectory 是目录级 rescan（POST /Library/Media/Updated）。
	ScopeDirectory RefreshScope = "directory"
	// ScopeLibrary 是全库刷新（POST /Library/Refresh），目录级被拒时的降级。
	ScopeLibrary RefreshScope = "library"
	// ScopeSkipped 是「目录不在盘，没调上游」：不是失败，重试也变不出目录。
	ScopeSkipped RefreshScope = "skipped"
)

// WarnAfter 是「两次失败 = warning」的阈值（规格 §5.5 / P11-2）。
//
// 它只是判据：warning 怎么冒泡（面板、事件、Telegram）与重试节奏都归 side_effect
// （ADR-0005），消费者票接线时调 ShouldWarn 就够了，不必再各写一份比较。
const WarnAfter = 2

// ShouldWarn 报告同一目标连续失败到第几次该冒一个 warning。
func ShouldWarn(attempts int) bool {
	return attempts >= WarnAfter
}

// RefreshResult 是一次刷新的结论。
//
// Accepted 只说明上游回了 2xx，也就是「已受理」：Jellyfin 对未知路径也回 204
// （实测陷阱），所以它**不是**「这个目录真的被扫了」的证据。消费者可以拿它决定
// 「不必再重试」，但不许把它翻成成功事件或写进「已入库」的叙述（P11-2）。
type RefreshResult struct {
	Scope     RefreshScope
	Directory string
	Accepted  bool
}

// Refresh 让 Jellyfin 重扫一部作品的目录（规格 §5.5，P11-1/2）。
//
// 顺序与判据：
//
//  1. 目录 = {media_root}/{番号}，番号必须是单个路径分量（不许带分隔符或 ..）；
//  2. 目录不在盘 → ScopeSkipped，一个请求都不发（「目录在盘才调」）；
//  3. 目录级 rescan：被**拒**（4xx，鉴权错除外）→ 降级全库刷新；2xx → 已受理；
//  4. 鉴权错不降级：同一个 key 换端点也一样被拒，多打一次没意义；
//     5xx/429、网络与超时也不降级：那是瞬时问题，交给 side_effect 用自己的节奏重试。
//
// 多次失败＝warning 的判据是 ShouldWarn；刷新失败不阻塞入库与通知（ADR-0005）。
func (i *Integration) Refresh(ctx context.Context, cfg Config, number string) (RefreshResult, error) {
	if err := validate(cfg); err != nil {
		return RefreshResult{}, err
	}
	directory, err := MediaDirectory(cfg.MediaRoot, number)
	if err != nil {
		return RefreshResult{}, err
	}
	c, err := newClient(cfg, i.http)
	if err != nil {
		return RefreshResult{}, err
	}

	if !settings.DirectoryVisible(directory) {
		return RefreshResult{Scope: ScopeSkipped, Directory: directory}, nil
	}

	if err := c.updatedMedia(ctx, directory); err != nil {
		// 只有「被拒」才降级：4xx 说明这个端点/路径服务端不收，全库刷新还有机会。
		if !isRejection(err) {
			return RefreshResult{Scope: ScopeDirectory, Directory: directory}, err
		}
		if fallbackErr := c.refreshLibrary(ctx); fallbackErr != nil {
			return RefreshResult{Scope: ScopeLibrary, Directory: directory},
				fmt.Errorf("目录级刷新被拒，降级全库刷新也没成: %w", fallbackErr)
		}
		return RefreshResult{Scope: ScopeLibrary, Directory: directory, Accepted: true}, nil
	}
	return RefreshResult{Scope: ScopeDirectory, Directory: directory, Accepted: true}, nil
}

// MediaDirectory 算出一次刷新要报给 Jellyfin 的目录：{media_root}/{番号}。
//
// 番号被当成目录名用，所以先验形状：空值、分隔符、`..` 都不许拼进路径
// （番号来自清单，但「拿用户数据拼路径」不该靠上游保证）。
func MediaDirectory(mediaRoot, number string) (string, error) {
	if err := validateMediaRoot(mediaRoot); err != nil {
		return "", err
	}
	number = strings.TrimSpace(number)
	switch {
	case number == "":
		return "", ValidationError{Message: "刷新需要番号：目录名就是番号"}
	case strings.ContainsAny(number, `/\`) || !filepath.IsLocal(number):
		return "", ValidationError{Message: "番号不能带路径分隔符或 ..: " + number}
	}
	return filepath.Join(strings.TrimSpace(mediaRoot), number), nil
}
