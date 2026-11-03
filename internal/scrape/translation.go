package scrape

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/task"
	"github.com/2017fighting/oh-my-mdc/internal/translate"
)

// AI 翻译是可选集成（规格 §5.7）：启用时把日文 title/outline 译成简体中文，
// 写进 task 的 title_zh/outline_zh；译文由整理段的 NFO 槽位消费（#29）。
//
// 三条边界，别的一概不做：
//
//   - **翻译不是取数**：它失败绝不重试取数、绝不计 attempts、绝不改状态机；
//     降级＝日文原文照常入库 + 一条告警（P6-2）。
//   - **未配置/禁用＝零调用**：判据在设置快照上（P6-3），api key 只在真要翻译时读。
//   - **复用路径也补译**：元数据已在库、0 次取数时，仅当译文缺失才补一次请求 ——
//     用户常常是任务刮完以后才启用翻译。

// translationAccess 是「这一轮要不要问 AI 翻译」的惰性判据。
type translationAccess struct {
	runner  *Runner
	ctx     context.Context
	enabled bool

	loaded bool
	cfg    translate.Config
	err    error
}

// newTranslationAccess 按设置快照构造这一轮的翻译判据。
//
// 与 M-Team 的同名判据一致：未配置、没启用或这一跑没接翻译，都等于零调用。
func (r *Runner) newTranslationAccess(ctx context.Context, snapshot settings.Snapshot) *translationAccess {
	enabled := r.translator != nil && snapshot.OptionalEnabled("ai_translation")
	return &translationAccess{runner: r, ctx: ctx, enabled: enabled}
}

// config 读一次设置原文，并把这一轮的结果缓存下来（一轮里多部片共用一个 key）。
func (a *translationAccess) config() (translate.Config, error) {
	if a.loaded {
		return a.cfg, a.err
	}
	a.loaded = true

	input, err := a.runner.settings.Input(a.ctx, "ai_translation")
	if err != nil {
		a.err = fmt.Errorf("读 AI 翻译设置失败: %w", err)
		return a.cfg, a.err
	}
	a.cfg = translate.ConfigFrom(input)
	return a.cfg, nil
}

// translate 是取数之后的一步可选加工：启用时译 title/outline。
//
// 失败**不返回错误**：调用方拿到的是原样的日文取数结果，入库照常（P6-2）。
func (r *Runner) translate(ctx context.Context, version task.Version, access *translationAccess, fetched Fetched) Fetched {
	if access == nil || !access.enabled {
		return fetched
	}
	fetched.Translation, _ = r.runTranslation(ctx, version, access, translate.Text{
		Title:   fetched.Values.TitleJA,
		Outline: fetched.Values.Outline,
	})
	return fetched
}

// runTranslation 是「读配置 → 调上游 → 失败记告警」的唯一实现。
//
// 两个入口（新取数、复用补译）共用它，所以「失败怎么处置」只有一处可读：
// 记一条告警、返回 ok=false，错误不往上抛（翻译失败绝不阻塞入库）。
func (r *Runner) runTranslation(ctx context.Context, version task.Version, access *translationAccess, in translate.Text) (translate.Text, bool) {
	cfg, err := access.config()
	if err != nil {
		r.recordTranslationFailure(ctx, version, err)
		return translate.Text{}, false
	}
	translated, err := r.translator.Translate(ctx, cfg, in)
	if err != nil {
		r.recordTranslationFailure(ctx, version, err)
		return translate.Text{}, false
	}
	return translated, true
}

// backfillTranslation 是复用路径上的补译：元数据已在库（0 次取数），译文缺失时补一次。
//
// 只补缺的那一半：已有译文不重译（一次调用出两个字段，省下来的是 token）。
// 失败与正式翻译同一处置：留一条告警，不动库里的任何东西。
func (r *Runner) backfillTranslation(ctx context.Context, version task.Version, access *translationAccess) {
	if access == nil || !access.enabled {
		return
	}

	metadata, _, err := r.store.Metadata(ctx, version.MovieID)
	if err != nil {
		r.logger.Error("scrape: 补译读元数据失败", "movie_id", version.MovieID, "err", err)
		return
	}
	if !needsTranslation(metadata) {
		return
	}
	// 同一版本只补一次：失败过的版本不再每轮重试（译文是可选的，用户可强制重刮）。
	// 判据复用告警去重，所以「补译一次就停」与「只留一条告警」是同一条记录。
	if r.translationFailureRecorded(ctx, version) {
		return
	}

	translated, ok := r.runTranslation(ctx, version, access, translate.Text{
		Title:   metadata.TitleJA,
		Outline: metadata.Outline,
	})
	if !ok {
		return
	}

	// 只写缺的那一半：向量的两半各有自己的「原文是不是空」判据。
	if strings.TrimSpace(metadata.TitleZH) != "" {
		translated.Title = ""
	}
	if strings.TrimSpace(metadata.OutlineZH) != "" {
		translated.Outline = ""
	}
	if translated.Empty() {
		// 调用成功却没给出缺的那一半：当成一次失败（否则每轮都会再花一次请求）。
		r.recordTranslationFailure(ctx, version,
			errors.New("上游没有给出缺的那一半译文（标题或简介）"))
		return
	}
	if err := r.store.SaveTranslation(ctx, version.MovieID, task.Translated{
		TitleZH: translated.Title, OutlineZH: translated.Outline,
	}); err != nil {
		r.logger.Error("scrape: 补译落库失败", "movie_id", version.MovieID, "err", err)
		return
	}
	r.logger.Info("scrape: 译文已补齐", "movie_id", version.MovieID, "number", version.Number)
}

// needsTranslation 报告这条任务有没有「本该有却没有」的译文。
//
// 原文为空不算缺：没有简介的片不该每轮都去问一次翻译。
func needsTranslation(metadata task.Metadata) bool {
	missingTitle := strings.TrimSpace(metadata.TitleJA) != "" && strings.TrimSpace(metadata.TitleZH) == ""
	missingOutline := strings.TrimSpace(metadata.Outline) != "" && strings.TrimSpace(metadata.OutlineZH) == ""
	return missingTitle || missingOutline
}

// translationFailureRecorded 报告这条版本上是否已经有一条翻译失败告警。
//
// 它同时是「补译不再重试」的判据（见 backfillTranslation）：译文是可选功能，
// 一次失败就停下等人看，比每 5 分钟花一次 token 更像话。
// 读失败当「没有」：宁可多一条告警，也不要因为一次读失败把失败原因整个丢掉
// （与整理段的去重策略一致）。
func (r *Runner) translationFailureRecorded(ctx context.Context, version task.Version) bool {
	alerted, err := r.store.HasAlert(ctx, version.Infohash, task.TranslationFailurePrefix)
	if err != nil {
		r.logger.Error("scrape: 查翻译告警去重失败", "infohash", version.Infohash, "err", err)
		return false
	}
	return alerted
}

// recordTranslationFailure 记一条翻译失败告警（带分类与原因）。
//
// 同一版本只留一条（HasAlert）：时间线不该被同一件事刷满。
// 它不把一轮取数判成失败 —— 降级是设计好的路径，翻译挂了不该让循环健康位变红。
func (r *Runner) recordTranslationFailure(ctx context.Context, version task.Version, cause error) {
	if r.translationFailureRecorded(ctx, version) {
		return
	}

	r.logger.Warn("AI 翻译失败，已降级日文原文",
		"movie_id", version.MovieID, "infohash", version.Infohash, "err", cause)
	if err := r.store.AppendEvent(ctx, task.Event{
		MovieID:  version.MovieID,
		Infohash: version.Infohash,
		Kind:     task.EventAlert,
		Phase:    string(task.PhaseScrape),
		Reason: fmt.Sprintf("%s（%s）：%s（title/plot 用日文原文入库）",
			task.TranslationFailurePrefix, translate.Classify(cause), cause),
	}); err != nil {
		r.logger.Error("scrape: 记翻译失败事件失败", "infohash", version.Infohash, "err", err)
	}
}
