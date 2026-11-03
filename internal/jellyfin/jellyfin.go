// Package jellyfin 把 Jellyfin 接到设置域、向导与旁路副作用上（规格 §5.5）。
//
// 它拥有三件事：
//
//   - 「测试可写」：GET /System/Info 鉴权（规格把鉴权通过定为可写，向导用管理员 key）
//   - media_root 本机可见；
//   - 目录级刷新：POST /Library/Media/Updated（目录在盘才调），被拒（4xx）降级全库；
//   - 「已受理」语义：2xx 只说明上游收到了，**不是**「目录真的被扫了」的证据
//     （Jellyfin 对未知路径也回 204，实测陷阱）。
//
// 刷新不是任务状态：它是旁路副作用（ADR-0005），失败不阻塞入库与通知，
// 由 side_effect 表按自己的节奏重试。
package jellyfin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/metrics"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// defaultHTTPTimeout 是单次调用的上限。测试连接另有 10s 的总预算（settings.TestTimeout）。
const defaultHTTPTimeout = 30 * time.Second

// Config 是一次 Jellyfin 调用要用的设置原文（含凭据，只许在进程内流转）。
type Config struct {
	URL    string
	APIKey string
	// MediaRoot 是媒体库根目录（{media_root}/{番号} 的上一级）。
	// 它随本票搬进 jellyfin 表：刷新与硬链接用的是同一个「三容器同路径」约定。
	MediaRoot string
}

// ConfigFrom 把设置域的原文翻成调用配置（单点：向导、测试、副作用循环用同一份）。
func ConfigFrom(input settingsInput) Config {
	return Config{
		URL:       strings.TrimSpace(input.Text("url")),
		APIKey:    strings.TrimSpace(input.Text("api_key")),
		MediaRoot: strings.TrimSpace(input.Text("media_root")),
	}
}

// settingsInput 是 settings.TestInput 里本包用到的那一小片。
type settingsInput interface {
	Text(name string) string
}

// Options 装配一个 Jellyfin 集成门面所需的依赖。
type Options struct {
	// HTTP 允许注入测试用的 transport；空则用带超时的默认客户端。
	HTTP *http.Client
	// Metrics 记每次上游请求的结局（规格 §9 的 jellyfin 指标）；nil 时什么都不记。
	Metrics metrics.Recorder
}

// Integration 是 Jellyfin 集成门面：一个 settings.Tester + 目录级刷新。
type Integration struct {
	http    *http.Client
	metrics metrics.Recorder
}

// New 构造 Jellyfin 集成门面。
func New(opts Options) *Integration {
	return &Integration{http: opts.HTTP, metrics: opts.Metrics}
}

// SystemInfo 是 GET /System/Info 里本项目要用到的两项（chip 文案用）。
type SystemInfo struct {
	ServerName string `json:"ServerName"`
	Version    string `json:"Version"`
}

// Test 实现 settings.Tester：「测试可写」＝鉴权 + media_root 可见（规格 §5.5 / P4-1）。
//
// 两关的判据：
//
//  1. GET /System/Info 带 X-Emby-Token 过鉴权。规格把「鉴权通过」定为可写判定：
//     读到服务器信息就说明这把 key 被接受，刷新请求会带同一把 key。它证明的是
//     「能进这个 Jellyfin」，不额外区分用户 token 与管理员 key（权限不足会由第一次
//     真实刷新以 auth 错暴露，走 §9 的 permanent 指标）；
//  2. media_root 在本机可见：三个容器把同一 host 目录挂到同一路径，看不见就说明
//     挂载没对齐——这时刷新与硬链接都会失败，红得越早越好（P4-2）。
//
// 本地校验（地址/key/media_root 缺项）先于任何请求：缺项时连不上，也不该打扰上游。
func (i *Integration) Test(ctx context.Context, _ settings.Section, input settings.TestInput) settings.TestResult {
	cfg := ConfigFrom(input)
	if err := validate(cfg); err != nil {
		return testResult(err)
	}

	c, err := newClient(cfg, i.http, i.metrics)
	if err != nil {
		return testResult(err)
	}

	info, err := c.systemInfo(ctx)
	if err != nil {
		return testResult(err)
	}

	if !settings.DirectoryVisible(cfg.MediaRoot) {
		return testResult(ValidationError{Message: invisibleMessage(cfg.MediaRoot)})
	}

	detail := fmt.Sprintf("鉴权通过 · Jellyfin %s", info.Version)
	if name := strings.TrimSpace(info.ServerName); name != "" {
		detail += "（" + name + "）"
	}
	detail += " · 媒体库目录 " + cfg.MediaRoot + " 可见"
	return settings.TestResult{OK: true, Detail: detail}
}

// validate 检查一次调用缺哪些设置（本地校验，不打上游）。
//
// Test 与 Refresh 共用它：同一份缺项在哪条路径上都该是 business，而不是一条报
// business、另一条把空 key 发出去换回 auth。
func validate(cfg Config) error {
	switch {
	case cfg.URL == "":
		return ValidationError{Message: "Jellyfin 节还没设置地址"}
	case cfg.APIKey == "":
		return ValidationError{Message: "Jellyfin 节还没设置 API key"}
	}
	return validateMediaRoot(cfg.MediaRoot)
}

// validateMediaRoot 是 media_root 的空值判据（单点：validate 与 MediaDirectory 共用）。
func validateMediaRoot(mediaRoot string) error {
	if strings.TrimSpace(mediaRoot) == "" {
		return ValidationError{Message: "Jellyfin 节还没设置媒体库目录（media_root）"}
	}
	return nil
}

// invisibleMessage 是 media_root 不可见时给人看的那句话：说清楚「哪条路径」与「去查什么」。
func invisibleMessage(root string) string {
	return "媒体库目录 " + root + " 在这台机器上看不见：qBittorrent / oh-my-mdc / Jellyfin 三个容器" +
		"要把同一个 host 目录挂到同一路径（规格 §5.5），否则刷新与硬链接都会失败"
}

func testResult(err error) settings.TestResult {
	return settings.TestResult{Code: Classify(err), Detail: err.Error()}
}

// 编译期确认门面满足测试接缝。
var _ settings.Tester = (*Integration)(nil)
