// Package material 拥有版本行存的**交付材料**（规格 §4.1 payload / §5.2）：
// 磁力串原样交出，私种的 `.torrent` 字节从备份路径读回来。
//
// 它存在的理由是那份备份会消失：`.torrent` 字节是瞬态的（生产上备份目录是 /tmp 里的
// emptyDir，pod 重启即清空，而 reloader 每次改设置都会滚动重启）。读不到字节时材料的
// 唯一补法是回 M-Team 重取一次——**只认 infohash 相符的那一条**，因为 Resolve 按今天的
// 选种策略挑种，站点上换了种子时它给的是另一条版本的材料。
//
// 两条加种路径（poll 的自动加种、人工重试的重新加种）共用这一处：它们读的是同一列
// payload，就该对同一条版本有同一个理解。
package material

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/mteam"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// SectionMTeam 是取种凭据所在的设置节。
const SectionMTeam = "mteam"

// Torrents 是 M-Team 取种接缝（真实实现是 mteam.Integration）。
//
// 只暴露「番号 → 一份种子」：搜索、选种、换链接、下载、算 infohash 都是 M-Team 自己的事。
type Torrents interface {
	Resolve(ctx context.Context, apiKey, number string) (mteam.Torrent, error)
}

// Settings 是重取时要问的设置域：凭据（Input）与可选集成的启停（Load）。
//
// 启停是一个独立的事实：绕过它去搜种会让「禁用 M-Team」变成一句空话，
// 而搜索与下载正是 M-Team 唯一有配额的两种调用（规格 §5.2）。
type Settings interface {
	Input(ctx context.Context, section string) (settings.TestInput, error)
	Load(ctx context.Context) (settings.Snapshot, error)
}

// Options 装配一份交付材料仓库。
type Options struct {
	// Dir 是 .torrent 备份目录；空则 os.TempDir()/oh-my-mdc/torrents。
	Dir string
	// Torrents 是取种接缝（重取用）。
	Torrents Torrents
	// Settings 用于重取时读 api key 与 M-Team 的启停。
	Settings Settings
	// Logger 空则用 slog.Default()。
	Logger *slog.Logger
}

// Store 是交付材料仓库：落盘、读回、以及「读不回来就重取一次」。
type Store struct {
	dir      string
	torrents Torrents
	settings Settings
	logger   *slog.Logger
}

// New 构造仓库。
func New(opts Options) *Store {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		dir = DefaultDir()
	}
	return &Store{dir: dir, torrents: opts.Torrents, settings: opts.Settings, logger: logger}
}

// DefaultDir 是备份目录的兜底。
//
// 生产上 /tmp 是 emptyDir：进程重启后内容会没（规格 §5.2 的「字节瞬态走 /tmp」），
// 所以这个目录**不承诺**里面还有东西——读不到就重取，见 Deliver。
func DefaultDir() string {
	return filepath.Join(os.TempDir(), "oh-my-mdc", "torrents")
}

// Delivery 是一条版本这次加种要用的材料。
type Delivery struct {
	Torrent qbittorrent.Torrent
	// Material 是这次实际用到的交付材料（磁力串或备份路径）。
	// 与版本行的 payload 列不同就是「重取落到了新路径」，调用方要把它写回库：
	// 人工重试与下一轮 poll 读的都是那一列。
	Material string
	// Refetched 表示这份材料是回 M-Team 重取回来的（日志与时间线据此说话）。
	Refetched bool
}

// Deliver 把一条版本翻成加种请求；私种备份读不到时回 M-Team 重取一次。
//
// 返回的错误分两种，调用方要分开处置（Permanent 是判据）：
//
//   - 材料永久没了（站点上已没有这一条种子、凭据不对）：重试变不出来，该进「需要人工」；
//   - 瞬时失败（连不上 M-Team、设置域读不出来）：保住 pending，下一轮再来。
func (s *Store) Deliver(ctx context.Context, version task.Version, cfg qbittorrent.Config) (Delivery, error) {
	torrent, err := qbittorrent.FromPayload(cfg, version.Infohash, version.Number,
		string(version.Delivery), version.Payload)
	if err == nil {
		return Delivery{Torrent: torrent, Material: version.Payload}, nil
	}
	if !errors.Is(err, qbittorrent.ErrPayloadMissing) {
		return Delivery{}, err
	}

	path, refetchErr := s.refetch(ctx, version)
	if refetchErr != nil {
		return Delivery{}, RefetchError{Backup: err, Refetch: refetchErr}
	}

	torrent, err = qbittorrent.FromPayload(cfg, version.Infohash, version.Number,
		string(version.Delivery), path)
	if err != nil {
		return Delivery{}, err
	}
	return Delivery{Torrent: torrent, Material: path, Refetched: true}, nil
}

// Stash 把 .torrent 字节落到备份路径，返回 payload 要存的那条路径。
//
// 落盘而不是把字节塞进 payload 列：payload 是 text（磁力串或备份路径），
// 而加种与人工重试（#31）都要拿到原文才下得动。
//
// 原子写（temp + rename）：加种可能在任何时刻读到这个路径，读到半份种子
// 只会得到「infohash 对不上的红种」，而那种故障在日志里没有痕迹。
func (s *Store) Stash(infohash string, data []byte) (string, error) {
	if infohash == "" {
		return "", fmt.Errorf("material: 存种子需要 infohash")
	}
	if len(data) == 0 {
		return "", fmt.Errorf("material: 存种子需要字节")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", fmt.Errorf("material: 建种子备份目录 %s: %w", s.dir, err)
	}

	path := filepath.Join(s.dir, infohash+".torrent")
	tmp, err := os.CreateTemp(s.dir, infohash+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("material: 建种子临时文件: %w", err)
	}
	tmpPath := tmp.Name()
	// 失败路径上一定删掉临时文件：留着就是 /tmp 里永不回收的垃圾。
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("material: 写种子临时文件: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("material: 收尾种子临时文件: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("material: 落种子到 %s: %w", path, err)
	}
	return path, nil
}

// refetch 回 M-Team 重取这一条版本的 `.torrent`，落盘后返回新的备份路径。
//
// 重取仍然受 M-Team 的启停约束：搜索与下载是 M-Team 唯一有配额的两种调用，
// 绕过开关去重取会让「禁用 M-Team」变成一句空话。禁用时这里给的是**永久**失败
// （「启用它，再点重试」是一条真能走通的路），不是每轮去撞一次。
//
// 选种仍是 Resolve 的「今天的最优」，所以只能**下完核对 infohash**：
// 相符才用，不符就是站点上已经没有这一条（GoneError）——不拿另一条种子的字节凑合。
func (s *Store) refetch(ctx context.Context, version task.Version) (string, error) {
	key, err := s.apiKey(ctx)
	if err != nil {
		return "", err
	}

	torrent, err := s.torrents.Resolve(ctx, key, version.Number)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(strings.TrimSpace(torrent.Infohash), version.Infohash) {
		return "", GoneError{Infohash: version.Infohash, Number: version.Number, Got: torrent.Infohash}
	}

	path, err := s.Stash(torrent.Infohash, torrent.Bytes)
	if err != nil {
		return "", err
	}
	s.logger.Info("私种备份缺失，已回 M-Team 重取",
		"movie_id", version.MovieID, "infohash", version.Infohash, "path", path)
	return path, nil
}

// apiKey 读取种凭据。
//
// 「这份部署没有 M-Team」与「M-Team 节没有 key」都是**永久**失败（要人配），
// 所以包成 mteam.ValidationError：它与「搜不到」「key 无效」走同一个分类。
func (s *Store) apiKey(ctx context.Context) (string, error) {
	if s.torrents == nil || s.settings == nil {
		return "", mteam.ValidationError{Message: "M-Team 没配置，重取不了私种备份"}
	}
	snapshot, err := s.settings.Load(ctx)
	if err != nil {
		return "", fmt.Errorf("读设置失败: %w", err)
	}
	if !snapshot.OptionalEnabled(SectionMTeam) {
		return "", mteam.ValidationError{Message: "M-Team 已禁用，补不回私种备份（启用后再重试）"}
	}
	input, err := s.settings.Input(ctx, SectionMTeam)
	if errors.Is(err, settings.ErrNotConfigured) {
		return "", mteam.ValidationError{Message: "M-Team 节没配置，重取不了私种备份"}
	}
	if err != nil {
		return "", fmt.Errorf("读 M-Team 设置失败: %w", err)
	}
	key := strings.TrimSpace(input.Text("api_key"))
	if key == "" {
		return "", mteam.ValidationError{Message: "M-Team 节没有 api_key，重取不了私种备份"}
	}
	return key, nil
}

// RefetchError 是「备份读不到，回 M-Team 重取也没成」：两半原因都留着。
//
// 人要看的是「缺的是什么、补为什么没补上」——只说「重取失败」等于把现场的
// 备份路径藏起来（那一句话正是排查的第一条线索）。
type RefetchError struct {
	// Backup 是读备份时的原始错误（包着 qbittorrent.ErrPayloadMissing）。
	Backup error
	// Refetch 是重取失败的原因（M-Team 分类错误、GoneError 或读设置失败）。
	Refetch error
}

func (e RefetchError) Error() string {
	return fmt.Sprintf("%v；回 M-Team 重取失败：%v", e.Backup, e.Refetch)
}

// Unwrap 让 errors.Is/As 同时看到两半。
func (e RefetchError) Unwrap() []error { return []error{e.Backup, e.Refetch} }

// GoneError 表示站点上已经没有这一条 infohash 的种子：重取到的是另一条。
//
// 它是一句**答案**，不是故障：这条版本永远加不上种了，只能由人处置
// （换一条版本、或删掉它）。
type GoneError struct {
	// Infohash 是版本行的身份。
	Infohash string
	// Number 是版本次那部片的番号（重取用的搜索词）。
	Number string
	// Got 是这次重取到的那条种子的 infohash（不同就是它）。
	Got string
}

func (e GoneError) Error() string {
	return fmt.Sprintf("M-Team 上 %s 已没有这条种子（版本 infohash %s，重取到的是 %s）",
		e.Number, e.Infohash, e.Got)
}

// Permanent 报告这次「材料没了」是否重试无用：站点上已没有这条种子、凭据不对，
// 或这份部署根本没有 M-Team。
//
// 口径与规格 §9 的 permanent 分类同源（mteam.IsPermanent）：连不上/超时/协议不一致
// 都**不**算永久——那种时候保住 pending 等下一轮才是对的。
func Permanent(err error) bool {
	var refetch RefetchError
	if !errors.As(err, &refetch) {
		return false
	}
	var gone GoneError
	if errors.As(refetch.Refetch, &gone) {
		return true
	}
	return mteam.IsPermanent(refetch.Refetch)
}
