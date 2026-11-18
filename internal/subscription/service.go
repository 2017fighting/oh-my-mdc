package subscription

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/ingest"
	"github.com/2017fighting/oh-my-mdc/internal/material"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
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

// maxBackfillDays 是回灌天数的上限：十年足够把任何一位女优的历史扫一遍，
// 再往上只会把上游翻页翻成无尽循环（不设上限时 days 就是个放大器）。要更早的
// 历史有 `all`——它是「没有下界」，不必拿一个巨大的天数和上游对赌。
const maxBackfillDays = 3650

// Request 是一次「添加订阅」的输入：贴 id 或贴女优页链接，二选一。
type Request struct {
	ActressID string
	JavdbURL  string
}

// BackfillRequest 是一次「回灌」的输入（§8.4）：{days} 或 {all: true}，二选一。
//
// days 是「往回拨多少天」，all 是「拨到她最早的作品」（起追日 = 1970-01-01）。
// 两个都给时 all 胜出——「全部」本来就盖住了任何天数。
type BackfillRequest struct {
	Days int
	All  bool
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
//
// 除「名单与身份」那几项外，它还要一份 ingest 的依赖：「收下」把一条跳过转成正常待办，
// 走的是与循环**同一条**段（ADR-0014）。那些字段为空时只有 AcceptSkip 不能用，
// 其余四个动作（列表、添加、启停、回灌）不受影响。
type ServiceOptions struct {
	Store    *Store
	Settings Settings
	Source   Source
	// Tasks/Outbox/Magnets/Adder/Torrents/Material 是 ingest 的依赖（收下用）。
	Tasks    *task.Store
	Outbox   *sidefx.Outbox
	Magnets  ingest.Magnets
	Adder    ingest.Adder
	Torrents ingest.Torrents
	Material *material.Store
	// Owner 是 claim 的持有者标识；空则用 hostname-pid。
	Owner string
	// Pending 是订阅循环每轮末尾置下的「待入队」现数（面板「还有 N 部待入队」）。
	// 它与 Runner 共用同一个账；nil 时一律读 0（不装配也能用）。
	Pending *Pending
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
}

// Service 是面板订阅区的门面：添加、启停、回灌、列表，加跳过表的读与「收下」。
type Service struct {
	store    *Store
	settings Settings
	source   Source
	ingest   *ingest.Ingester
	tasks    *task.Store
	pending  *Pending
	logger   *slog.Logger
}

// NewService 构造订阅门面。
func NewService(opts ServiceOptions) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:    opts.Store,
		settings: opts.Settings,
		source:   opts.Source,
		tasks:    opts.Tasks,
		pending:  opts.Pending,
		// 这一段 ingest 只在「收下」时用：与循环同一套依赖、同一个构造函数（ADR-0014）。
		ingest: ingest.New(ingest.Options{
			Store:    opts.Tasks,
			Outbox:   opts.Outbox,
			Settings: opts.Settings,
			Magnets:  opts.Magnets,
			Adder:    opts.Adder,
			Torrents: opts.Torrents,
			Material: opts.Material,
			Logger:   logger,
			Owner:    opts.Owner,
		}),
		logger: logger,
	}
}

// List 读全部订阅（GET /api/subscriptions）。
//
// deferred_count 不来自库：它是循环上一轮末尾置下的现数，这里只负责把它贴到行上，
// 与面板「还有 N 部待入队」、指标 mdc_subscription_pending 同一份值。
func (s *Service) List(ctx context.Context) ([]Row, error) {
	rows, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].DeferredCount = s.pending.Of(rows[i].ID)
	}
	return rows, nil
}

// withPending 给单行结果贴上同一个现数（添加/启停/回灌的回执也是面板那一行）。
func (s *Service) withPending(row Row) Row {
	row.DeferredCount = s.pending.Of(row.ID)
	return row
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
	return s.withPending(row), outcome, nil
}

// SetEnabled 启停一条订阅。停用只停追新，已建任务照走完（规格 §6.4 生命周期）。
func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) (Row, error) {
	row, err := s.store.SetEnabled(ctx, id, enabled)
	if err != nil {
		return Row{}, err
	}
	return s.withPending(row), nil
}

// Backfill 把取数下界往回拨（「回灌 N 天」或「全部」）。
//
// 只拒**真的什么都不做**的那一档：回灌的落点是 水位线 − days（见 backfillTarget），
// 它不比起追日更早时，LEAST 过后什么都不会变，那才是空操作（§8.4）。所以「回灌 30 天」
// 对一条刚建的订阅是**有效**的——起追日就是建订阅当天，往回 30 天正是把窗口拨回三十天前
// 的那一下（回看窗口只在起追日之后游走，它盖不住这一段）；反过来，已经在更早处回灌过的
// 订阅再点 30 天，落点被起追日盖住，那是空操作。
//
// days ≥ 1、上限 maxBackfillDays；{all:true} 不看起追日（没有下界）。
func (s *Service) Backfill(ctx context.Context, id int64, request BackfillRequest) (Row, error) {
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return Row{}, err
	}
	if request.All {
		row, err := s.store.SetBackfill(ctx, id, BackfillAllFloor())
		if err != nil {
			return Row{}, err
		}
		return s.withPending(row), nil
	}

	days := request.Days
	if days < 1 || days > maxBackfillDays {
		return Row{}, fmt.Errorf("%w：回灌天数要在 1–%d 之间，收到 %d", ErrBadInput, maxBackfillDays, days)
	}
	target := backfillTarget(row, days)
	if !target.Before(row.FloorDate) {
		return Row{}, fmt.Errorf(
			"%w：回灌 %d 天只到 %s，起追日已经在 %s——这一段已经覆盖了（要更早请给更大的天数，或用全部）",
			ErrBadInput, days, target.Format(dateLayout), row.FloorDate.Format(dateLayout))
	}
	updated, err := s.store.SetBackfill(ctx, id, target)
	if err != nil {
		return Row{}, err
	}
	return s.withPending(updated), nil
}

// dateLayout 是日期列的线形状：回灌落点（SQL 参数与 message 共用）、水位线、面板日期串。
const dateLayout = "2006-01-02"

// backfillTarget 是「回灌 days 天」会把取数下界拨到的那一天：水位线 − days。
//
// 水位线为 NULL 时按今天算，与 runner 的 watermarkDate 同一口径（NULL 只可能是
// 「还没跑过」）。天数 → 日期只有这一处算法，把 SQL 留给「只往前挪」那一条（LEAST）。
func backfillTarget(row Row, days int) time.Time {
	return watermarkDate(row, time.Now()).AddDate(0, 0, -days)
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
