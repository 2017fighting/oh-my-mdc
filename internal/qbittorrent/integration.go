// Package qbittorrent 把 qBittorrent WebUI API v2 接到设置域、向导与循环上（规格 §5.4）。
//
// 它拥有四件事：
//
//   - 「测试连接」：登录 + 版本读 + autorun 现值读（供向导展示 diff）；
//   - autorun 共存改写：把既有命令（moviepilot）与本项目回调拼成一条 `sh -c`，
//     改写前出完整前后对比，人确认后才落（ADR-0007）；
//   - 加种与收编：先查 properties，不在就加种（磁力 urls= / 私种 multipart），
//     在而无项目 tag 就收编（补 tag + category，savepath 不动）；
//   - tag 换装：入库成功时 mdc:in-progress → mdc:finished。
//
// 做种、删种、限速一概不碰：生死由 qbt 自己的全局策略裁决（规格 §5.4）。
package qbittorrent

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// Options 装配一个 qBittorrent 集成门面所需的依赖。
type Options struct {
	// HTTP 允许注入测试用的 transport；空则用带超时的默认客户端。
	HTTP *http.Client
}

// Integration 是 qBittorrent 集成门面：一个 settings.Tester + 加种/收编/tag 操作。
type Integration struct {
	http *http.Client
}

// New 构造 qBittorrent 集成门面。
func New(opts Options) *Integration {
	return &Integration{http: opts.HTTP}
}

// Outcome 是一次加种的结局。三种都是「这个版本可以继续走流程」。
type Outcome string

const (
	// Added 是新加进 qbt 的种子。
	Added Outcome = "added"
	// Adopted 是本来就在 qbt 里、但没有项目 tag 的种子：补 tag + category 收编。
	Adopted Outcome = "adopted"
	// AlreadyTracked 是本来就带着项目 tag 的种子：什么都不写，直接推进。
	AlreadyTracked Outcome = "already_tracked"
)

// Torrent 是一条要交给 qbt 的下载候选。
//
// 交付形态二选一：磁力（Magnet）或私有种子（Body）。Hash 可以不给，
// 磁力形态会从 `xt=urn:btih:` 里取（私种必须由调用方从 bencode 算出后给出）。
type Torrent struct {
	Hash string
	// Magnet 是磁力串（javdb_magnet 来源）。
	Magnet string
	// Body 是 .torrent 字节（mteam_torrent 来源）。
	Body []byte
	// Filename 是私种在 multipart 里的文件名，空则用 `<infohash>.torrent`。
	Filename string
	// Number 是番号，作为项目 tag（规格 P7-5：番号从 tag 取，不解析文件名）。
	Number string
	// SavePath 覆盖 cfg.DownloadRoot（默认就用设置里的下载根目录）。
	SavePath string
}

// Test 实现 settings.Tester：「测试连接」＝登录 + 版本读 + autorun 现值读（规格 §8.4）。
//
// 三项都过才绿：登录证明凭据，版本证明这是 qbt，autorun 现值是向导做前后对比的输入。
func (i *Integration) Test(ctx context.Context, _ settings.Section, input settings.TestInput) settings.TestResult {
	c, err := i.login(ctx, ConfigFrom(input))
	if err != nil {
		return testResult(err)
	}

	version, err := c.version(ctx)
	if err != nil {
		return testResult(err)
	}

	prefs, err := c.preferences(ctx)
	if err != nil {
		return testResult(err)
	}

	// 现值只报「读到了、多长」：autorun 里通常带着 moviepilot 的 token，不往 chip 里抄
	// 一遍（完整前后对比在向导的 autorun 面板里，那里本来就是给人看的）。
	detail := fmt.Sprintf("登录成功 · qBittorrent %s", version)
	if program := strings.TrimSpace(prefs.AutorunProgram); program == "" {
		detail += " · autorun 现值：空"
	} else {
		detail += fmt.Sprintf(" · autorun 现值 %d 字符", utf8.RuneCountInString(program))
	}
	if !prefs.AutorunEnabled {
		detail += " · autorun 是关闭的，回调不会触发"
	}
	return settings.TestResult{OK: true, Detail: detail}
}

// Autorun 读 qbt 的 autorun 现值并算出共存改写计划（**只读**上游）。
func (i *Integration) Autorun(ctx context.Context, cfg Config) (AutorunPlan, error) {
	_, prefs, err := i.readAutorun(ctx, cfg)
	if err != nil {
		return AutorunPlan{}, err
	}
	return PlanAutorun(prefs.Autorun(), cfg.WebhookBaseURL)
}

// ApplyAutorun 把计划落到 qbt：跳过/没变化就不写，否则只写 autorun_program。
//
// 写入面只有这一个键（qbt 的 setPreferences 是部分更新）：`autorun_on_torrent_added_*`
// 与 autorun 开关都不在本项目手里（ADR-0007）。
func (i *Integration) ApplyAutorun(ctx context.Context, cfg Config) (AutorunPlan, error) {
	c, prefs, err := i.readAutorun(ctx, cfg)
	if err != nil {
		return AutorunPlan{}, err
	}

	plan, err := PlanAutorun(prefs.Autorun(), cfg.WebhookBaseURL)
	if err != nil {
		return AutorunPlan{}, err
	}
	if plan.Skip || !plan.Changed {
		return plan, nil
	}
	if err := c.setPreferences(ctx, map[string]any{"autorun_program": plan.After}); err != nil {
		return plan, err
	}
	plan.Applied = true
	return plan, nil
}

// readAutorun 是两条 autorun 路径共用的那一半：登录 + 读现值（同一次会话）。
func (i *Integration) readAutorun(ctx context.Context, cfg Config) (*client, Preferences, error) {
	c, err := i.login(ctx, cfg)
	if err != nil {
		return nil, Preferences{}, err
	}
	prefs, err := c.preferences(ctx)
	if err != nil {
		return nil, Preferences{}, err
	}
	return c, prefs, nil
}

// Ensure 把一条候选交给 qbt，并保证它带着项目的 tag 与 category（规格 §5.4）。
//
// 顺序是「先查再加」：
//
//  1. 已在且有项目 tag → AlreadyTracked，零写入（直接推进）；
//  2. 已在但没有项目 tag → 收编：补 tag + category，savepath 不动；
//  3. 不在 → 加种（显式 savepath/category/createCategories）；
//     加种报错但再查已存在（竞态）→ 按收编处理，视为成功。
//
// 「先落库再加种」在调用方（ADR-0004）：本方法只负责 qbt 这一侧。
func (i *Integration) Ensure(ctx context.Context, cfg Config, torrent Torrent) (Outcome, error) {
	prepared, err := prepare(cfg, torrent)
	if err != nil {
		return "", err
	}

	c, err := i.login(ctx, cfg)
	if err != nil {
		return "", err
	}

	props, found, err := c.properties(ctx, prepared.Hash)
	if err != nil {
		return "", err
	}
	if found {
		return settle(ctx, c, cfg, prepared, props)
	}

	// 分类由加种的 createCategories=true 显式建；这里不再先建一次（同一件事两个机制）。
	request := addRequest{
		Magnet:   prepared.Magnet,
		Torrent:  prepared.Body,
		Filename: prepared.Filename,
		SavePath: prepared.SavePath,
		Category: cfg.Category,
		Tags:     projectTags(prepared.Number),
	}
	if err := c.addTorrent(ctx, request); err != nil {
		// 竞态：另一个进程（或上一次超时重试）已经把种子加进去了。
		// 加种回执说失败，但种子在，就是成功——真正的失败是「报错且仍然不在」。
		props, found, lookupErr := c.properties(ctx, prepared.Hash)
		if lookupErr == nil && found {
			return settle(ctx, c, cfg, prepared, props)
		}
		return "", err
	}
	return Added, nil
}

// settle 决定「种子已在 qbt 里」这一支的结局：有项目 tag 就放手，没有就收编。
//
// 收编要能重入：tag 先补、category 后补，若上次在两步之间失败，重入时种子已带项目
// tag —— 这时仍要补齐 category，否则「有 tag 就放手」会把收编永远停在半路。
func settle(ctx context.Context, c *client, cfg Config, torrent Torrent, props Properties) (Outcome, error) {
	// properties 一般带 hash；万一上游没给，就用我们查它时用的那个。
	hash := props.Hash
	if hash == "" {
		hash = torrent.Hash
	}

	if props.HasProjectTag() {
		if props.Category == cfg.Category {
			return AlreadyTracked, nil
		}
		if err := c.setCategory(ctx, hash, cfg.Category); err != nil {
			return "", err
		}
		return AlreadyTracked, nil
	}

	// 收编要求分类已存在：qbt 的 setCategory 不替人建分类。
	if err := c.createCategory(ctx, cfg.Category); err != nil {
		return "", err
	}
	if err := c.addTags(ctx, hash, []string{torrent.Number, TagInProgress}); err != nil {
		return "", err
	}
	if err := c.setCategory(ctx, hash, cfg.Category); err != nil {
		return "", err
	}
	return Adopted, nil
}

// TransitionTag 把项目 tag 从一个换成另一个（入库成功：in-progress → finished）。
//
// 先补新 tag 再摘旧 tag：中途失败时种子仍带项目 tag，对账不会把它当成「不是我们的」。
func (i *Integration) TransitionTag(ctx context.Context, cfg Config, hash, from, to string) error {
	if strings.TrimSpace(hash) == "" {
		return ValidationError{Message: "换 tag 需要 infohash"}
	}
	c, err := i.login(ctx, cfg)
	if err != nil {
		return err
	}
	if err := c.addTags(ctx, hash, []string{to}); err != nil {
		return err
	}
	return c.removeTags(ctx, hash, []string{from})
}

// login 建客户端并登录一次（每次调用一个 SID，不跨调用复用）。
func (i *Integration) login(ctx context.Context, cfg Config) (*client, error) {
	c, err := newClient(cfg, i.http)
	if err != nil {
		return nil, err
	}
	if err := c.login(ctx, cfg); err != nil {
		return nil, err
	}
	return c, nil
}

// prepare 校验候选并补齐 infohash / savepath 的默认值（本地校验，不打上游）。
func prepare(cfg Config, torrent Torrent) (Torrent, error) {
	prepared := torrent
	prepared.Hash = strings.ToLower(strings.TrimSpace(prepared.Hash))
	prepared.Number = strings.TrimSpace(prepared.Number)
	prepared.Filename = strings.TrimSpace(prepared.Filename)
	prepared.Magnet = strings.TrimSpace(prepared.Magnet)
	if prepared.SavePath == "" {
		prepared.SavePath = strings.TrimSpace(cfg.DownloadRoot)
	}

	switch {
	case prepared.Number == "":
		return Torrent{}, ValidationError{Message: "加种需要番号：它要作为项目 tag"}
	case prepared.Magnet != "" && len(prepared.Body) > 0:
		return Torrent{}, ValidationError{Message: "磁力与私有种子只能给一个（一条版本只有一种交付形态）"}
	case prepared.Magnet == "" && len(prepared.Body) == 0:
		return Torrent{}, ValidationError{Message: "加种需要磁力串或 .torrent 字节"}
	case prepared.SavePath == "":
		return Torrent{}, ValidationError{Message: "加种需要下载根目录：qBittorrent 节还没设置 download_root"}
	}

	if prepared.Hash == "" {
		hash, err := HashFromMagnet(prepared.Magnet)
		if err != nil {
			return Torrent{}, err
		}
		prepared.Hash = hash
	}
	if len(prepared.Body) > 0 && prepared.Filename == "" {
		prepared.Filename = prepared.Hash + ".torrent"
	}
	return prepared, nil
}

// projectTags 是加种一次带上的 tag：番号（对账时直接取用）+ mdc:in-progress。
func projectTags(number string) string {
	return number + "," + TagInProgress
}

func testResult(err error) settings.TestResult {
	return settings.TestResult{Code: Classify(err), Detail: err.Error()}
}

// 编译期确认门面满足测试接缝。
var _ settings.Tester = (*Integration)(nil)
