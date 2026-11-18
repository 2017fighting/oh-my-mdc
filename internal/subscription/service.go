package subscription

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"unicode"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// 三个哨兵错误：HTTP 面按它们分状态码（400 / 409 / 502），面板按 message 直译。
var (
	// ErrBadInput 是输入形状不对：两种输入都没给、链接不是女优页、id 不像 id、回灌天数越界。
	ErrBadInput = errors.New("订阅输入不合法")
	// ErrGated 是门禁没开（未初始化/已暂停）：与手工登记、补录同一口径（409 gate_closed）。
	ErrGated = errors.New("门禁没开")
	// ErrUpstream 是「这个 id 在上游读不到」：id 打错、网络不通、上游改版（502 upstream_error）。
	ErrUpstream = errors.New("上游读不到这位女优")
)

// maxBackfillDays 是回灌天数的上限：一年足够把任何一位女优的历史扫一遍，
// 再往上只会把上游翻页翻成无尽循环（不设上限时 days 就是个放大器）。
const maxBackfillDays = 3650

// Request 是一次「添加订阅」的输入：贴 id 或贴女优页链接，二选一。
type Request struct {
	ActressID string
	JavdbURL  string
}

// Settings 是订阅门面对设置域的全部要求：门禁（Load）与上游凭据（Input）。
type Settings interface {
	Load(ctx context.Context) (settings.Snapshot, error)
	Input(ctx context.Context, section string) (settings.TestInput, error)
}

// Source 是女优详情的接缝（真实实现是 javdb.Integration.Actress）。
//
// 只问一件事：这个 id 是谁。上游没有按名字搜女优的端点（§13 实测），所以面板的入口
// 只能是「贴 id / 女优页链接」，而这一个读取顺手把名字取回来当快照。
type Source interface {
	Actress(ctx context.Context, token, id string) (appapi.ActressInfo, error)
}

// ServiceOptions 装配订阅门面。
type ServiceOptions struct {
	Store    *Store
	Settings Settings
	Source   Source
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
}

// Service 是面板订阅区的门面：添加、启停、回灌、列表。
type Service struct {
	store    *Store
	settings Settings
	source   Source
	logger   *slog.Logger
}

// NewService 构造订阅门面。
func NewService(opts ServiceOptions) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: opts.Store, settings: opts.Settings, source: opts.Source, logger: logger}
}

// List 读全部订阅（GET /api/subscriptions）。
func (s *Service) List(ctx context.Context) ([]Row, error) {
	return s.store.List(ctx)
}

// Add 建一条订阅：解析 id → 问上游「她是谁」→ upsert actor → 建订阅。
//
// 门禁同 poll（三件套齐 + 未暂停）：没开时零写入（规格 §6.4 门禁行）。
// 同一位女优第二次添加是幂等放行（outcome=duplicate），不建第二行、不重置水位线。
func (s *Service) Add(ctx context.Context, request Request) (Row, string, error) {
	actressID, err := resolveActressID(request)
	if err != nil {
		return Row{}, "", err
	}

	snapshot, err := s.settings.Load(ctx)
	if err != nil {
		return Row{}, "", fmt.Errorf("subscription: 读设置: %w", err)
	}
	if reason := snapshot.GateReason(); reason != "" {
		return Row{}, "", fmt.Errorf("%w：%s", ErrGated, reason)
	}

	// 名字取上游。id 打错与网络故障在这里是同一档（502）：都意味着「现在建不了」，
	// 而区别（改输入 or 重试）由 message 说清。
	javdbInput, err := s.settings.Input(ctx, "javdb")
	if err != nil {
		return Row{}, "", fmt.Errorf("subscription: 读 JavDB 设置: %w", err)
	}
	info, err := s.source.Actress(ctx, javdbInput.Text("token"), actressID)
	if err != nil {
		return Row{}, "", fmt.Errorf("%w：%s（%v）", ErrUpstream, actressID, err)
	}
	name := strings.TrimSpace(info.Name)
	if name == "" {
		return Row{}, "", fmt.Errorf("%w：%s 在上游没有名字", ErrUpstream, actressID)
	}

	row, outcome, err := s.store.Create(ctx, Actor{ID: actressID, Name: name})
	if err != nil {
		return Row{}, "", err
	}
	s.logger.Info("女优订阅", "actress", actressID, "name", name, "outcome", outcome)
	return row, outcome, nil
}

// SetEnabled 启停一条订阅。停用只停追新，已建任务照走完（规格 §6.4 生命周期）。
func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) (Row, error) {
	return s.store.SetEnabled(ctx, id, enabled)
}

// Backfill 把取数下界往回拨 days 天（「回灌 N 天」）。
func (s *Service) Backfill(ctx context.Context, id int64, days int) (Row, error) {
	if days < 1 || days > maxBackfillDays {
		return Row{}, fmt.Errorf("%w：回灌天数要在 1–%d 之间，收到 %d", ErrBadInput, maxBackfillDays, days)
	}
	return s.store.SetBackfill(ctx, id, days)
}

// resolveActressID 从两种输入里取出女优 id。
//
// 认两种形状：裸 id（`EvkJ`）与女优页链接（`https://javdb.com/actors/EvkJ?…`，镜像域名
// 一堆，只认路径段）。其余一律 ErrBadInput——宁可让用户改一串输入，也不猜。
func resolveActressID(request Request) (string, error) {
	if id := strings.TrimSpace(request.ActressID); id != "" {
		if !isActressID(id) {
			return "", fmt.Errorf("%w：%q 不像女优 id（上游的 id 是字母数字短串，如 EvkJ）", ErrBadInput, id)
		}
		return id, nil
	}

	raw := strings.TrimSpace(request.JavdbURL)
	if raw == "" {
		return "", fmt.Errorf("%w：要给 actress_id 或 javdb_url 之一", ErrBadInput)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w：%v", ErrBadInput, err)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i, segment := range segments {
		if segment == "actors" || segment == "actor" {
			if i+1 >= len(segments) || strings.TrimSpace(segments[i+1]) == "" {
				break
			}
			id := strings.TrimSpace(segments[i+1])
			if !isActressID(id) {
				break
			}
			return id, nil
		}
	}
	return "", fmt.Errorf("%w：%s 不是女优页链接（形状是 /actors/{id}）", ErrBadInput, raw)
}

// isActressID 判定一个 id 像不像上游的演员 id。
//
// 只做形状判定（字母数字、长度上限）：真实 id 是 4–6 位混合大小写短串（EvkJ / Av2e /
// 1KBW），而这里**不**把「看起来像名字」当合法输入——上游没有按名字搜女优的端点（§13），
// 把中文名发给它只会换回一个 404 类错误，不如当场说清「这里要 id」。
func isActressID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for _, r := range id {
		if r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
