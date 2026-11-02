package sidefx

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/jellyfin"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// TestPolicyLimitsAreKindDriven 钉住「上限由 kind 驱动」（规格 §5.6 / §4.3）：
// notify 固定 5 次（Telegram 自己的预算），其余跟 app_config.max_attempts 走。
func TestPolicyLimitsAreKindDriven(t *testing.T) {
	cases := []struct {
		kind    Kind
		appMax  int
		wantMax int
		name    string
	}{
		{kind: KindNotify, appMax: 3, wantMax: NotifyMaxAttempts, name: "notify 不看 app_config"},
		{kind: KindListRemoval, appMax: 3, wantMax: 3, name: "list_removal 跟 app_config"},
		{kind: KindJellyfinRefresh, appMax: 7, wantMax: 7, name: "jellyfin_refresh 跟 app_config"},
		{kind: KindListRemoval, appMax: 0, wantMax: DefaultMaxAttempts, name: "读不到就用默认值"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := policyFor(tc.kind, tc.appMax).MaxAttempts; got != tc.wantMax {
				t.Errorf("MaxAttempts = %d, want %d", got, tc.wantMax)
			}
		})
	}
}

// TestBackoffDoublesAndCaps 钉住指数退避：1m → 2m → 4m … 封顶 30m。
func TestBackoffDoublesAndCaps(t *testing.T) {
	policy := policyFor(KindListRemoval, DefaultMaxAttempts)
	want := map[int]time.Duration{
		1: time.Minute,
		2: 2 * time.Minute,
		3: 4 * time.Minute,
		6: 30 * time.Minute, // 32m 封顶到 30m
		9: 30 * time.Minute,
	}
	for attempts, expected := range want {
		if got := policy.backoff(attempts); got != expected {
			t.Errorf("backoff(%d) = %v, want %v", attempts, got, expected)
		}
	}
	if got := policy.backoff(0); got != time.Minute {
		t.Errorf("backoff(0) = %v, want 第一次退避（参数 0 当第 1 次）", got)
	}
}

// TestExhaustedCountsAttemptsIncludingTheFirst：3 次上限＝第 3 次失败之后驻留。
func TestExhaustedCountsAttemptsIncludingTheFirst(t *testing.T) {
	policy := policyFor(KindListRemoval, 3)
	for attempts, want := range map[int]bool{1: false, 2: false, 3: true, 4: true} {
		if got := policy.exhausted(attempts); got != want {
			t.Errorf("exhausted(%d) = %v, want %v", attempts, got, want)
		}
	}
}

// TestPermanentIsDrivenByErrorClass 钉住「永久错立刻驻留」（规格 §5.6 分流）：
// 凭据与业务错不重试；网络/超时/协议不一致按退避重试到上限。
func TestPermanentIsDrivenByErrorClass(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"javdb token 被踢", &appapi.AuthError{Action: "JWTVerificationError", Message: "token 无效"}, true},
		{"javdb 业务错", &appapi.APIError{Message: "您已創建了同名清單"}, true},
		{"javdb 本地校验错", &appapi.ValidationError{Message: "清单项动作缺 movie_id"}, true},
		{"javdb 网络", &appapi.TransportError{Path: "/api/v1/lists", Err: errors.New("连接被拒")}, false},
		{"javdb 协议", &appapi.ProtocolError{Path: "/api/v1/lists", Status: 502, Message: "Cloudflare"}, false},
		{"jellyfin 鉴权", &jellyfin.Error{Code: settings.CodeAuth, Op: "System/Info"}, true},
		{"jellyfin 业务", jellyfin.ValidationError{Message: "还没设置媒体库目录"}, true},
		{"jellyfin 超时", &jellyfin.Error{Code: settings.CodeTimeout, Op: "Library/Media/Updated"}, false},
		{"jellyfin 网络", &jellyfin.Error{Code: settings.CodeNetwork, Op: "Library/Media/Updated"}, false},
		{"引擎本地校验错", ValidationError{Message: "待办缺 movie_id"}, true},
		{"普通错误", fmt.Errorf("数据库连不上"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := permanent(tc.err); got != tc.want {
				t.Errorf("permanent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestValidState 只认规格 §8.4 的两个筛选项（空串＝不筛）。
func TestValidState(t *testing.T) {
	for state, want := range map[string]bool{"": true, "pending": true, "stuck": true, "done": false, "x": false} {
		if got := ValidState(state); got != want {
			t.Errorf("ValidState(%q) = %v, want %v", state, got, want)
		}
	}
}
