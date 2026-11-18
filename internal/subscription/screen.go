package subscription

import (
	"context"
	"strings"
	"sync"

	"github.com/2017fighting/oh-my-mdc/internal/appapi"
	"github.com/2017fighting/oh-my-mdc/internal/metrics"
)

// 筛子（规格 §6.4，GLOSSARY「合集」，ADR-0017）：一部作品拉一次详情，三条判据各自
// 落一行跳过表。取数在那之前已经是不带 `s` 的全集——`s` 在上游静默地排，被排掉的
// 作品连跳过表都进不去，也就没有「收下」这个出口。
//
// 三条判据（ADR-0017 的表，顺序就是这里判的顺序）：
//
//	len(actors) > 1                      → not_single（多人一起的）
//	类別带「精選綜合」                    → compilation（她自己的合集，类別名就是「精選綜合」）
//	类別带「4 小時以上作品」/「16 小時以上作品」 → long_form（她自己的长合集）
//
// 标签 id **不硬编码**：按名字去 `GET /api/v2/tags?type=0` 的词表里查（ADR-0017），
// 词表在进程内缓存、重启重取。判据可以错——被排掉的都在面板上看得见、能「收下」。

// DetailSource 是作品详情的接缝（真实实现是 javdb.Integration.Detail）。
//
// 筛子要的只有详情里的两样：演员数与类別标签。其余字段（标题以外的那些）是刮削的事，
// 这里不假装自己会用。
type DetailSource interface {
	Detail(ctx context.Context, token, movieID string) (appapi.DetailResult, error)
}

// VocabularySource 是标签词表的接缝（真实实现是 javdb.Integration.TagVocabulary）。
//
// 签名与那一层逐位相同（ctx + token）：片库号是实现的细节（钉死 0），这一层不该知道。
type VocabularySource interface {
	TagVocabulary(ctx context.Context, token string) ([]appapi.Tag, error)
}

// 两条标签判据要找的类別名（ADR-0017）。名字是**翻译文本**，不是 id：
// 中文站与日文站的类別名不同、上游改 id 也都不会让这两行失效——失效的是名字本身，
// 那时判据跳过并记一条日志（见 tagIDs）。
var (
	compilationTagName = "精選綜合"
	longFormTagNames   = []string{"4 小時以上作品", "16 小時以上作品"}
)

// tagIDs 是三条判据要用的标签 id（按名字从词表查出来的那一份）。
//
// 零值＝一条都没查到：此时两条标签判据整条跳过，「演员数 > 1」照常判——词表读不到不该
// 让整个筛子瘫掉，更不该让整轮失败。
type tagIDs struct {
	compilation string
	longForm    []string
}

// screenResult 是一次筛子的结论。
type screenResult struct {
	// Reason 是命中的判据（not_single/compilation/long_form）；空串＝这一部可以收下。
	Reason string
	// Title 来自详情：跳过的作品不走刮削，面板上认人就靠它。
	Title string
}

// screenTags 返回本项目要用的标签 id（进程内缓存、重启重取）。
//
// 两处「只记一次日志」都在这里：
//
//   - 词表读不到（网络/token/协议）：这一轮只按演员数排，**不**让整轮失败——一位女优
//     的详情接口抖动不该把一个循环的健康位染红（ADR-0011 的口径）；
//   - 名字不在词表里（上游改名/换词）：那一条判据跳过，日志每个名字只记一次——每轮
//     都刷一行同样的 warn 只会把日志刷成噪音，而它要人改的是代码里的名字。
//
// 缓存只在成功取到词表时置位：失败下一次还要再试（token 刚补上也该立刻生效）。
func (r *Runner) screenTags(ctx context.Context, token string) tagIDs {
	r.vocab.mu.Lock()
	defer r.vocab.mu.Unlock()

	if r.vocab.loaded {
		return r.vocab.ids
	}
	if r.tags == nil {
		return tagIDs{}
	}
	vocabulary, err := r.tags.TagVocabulary(ctx, token)
	if err != nil {
		if !r.vocab.failed {
			r.vocab.failed = true
			r.logger.Warn("订阅筛子：标签词表读不到，这一轮只按演员数排",
				"code", appapi.Classify(err), "err", err)
		}
		return tagIDs{}
	}

	r.vocab.ids, r.vocab.missing = resolveTagIDs(vocabulary)
	r.vocab.loaded = true
	for _, name := range r.vocab.missing {
		r.logger.Warn("订阅筛子：词表里没有这个类別名，这条判据跳过", "tag", name)
	}
	return r.vocab.ids
}

// tagCache 是词表的进程内缓存（一个 Runner 一份）。
type tagCache struct {
	mu      sync.Mutex
	loaded  bool
	failed  bool
	ids     tagIDs
	missing []string
}

// resolveTagIDs 按名字查 id，返回查到的 id 与没查到的名字。
//
// 名字比对**忽略空白**：上游的类別名在抓录里写作「4小時以上作品」，而规格与 ADR 里
// 写作「4 小時以上作品」——两种写法是同一个标签，判据不该被一个空格打掉。
func resolveTagIDs(vocabulary []appapi.Tag) (tagIDs, []string) {
	byName := make(map[string]string, len(vocabulary))
	for _, tag := range vocabulary {
		if key := normalizeTagName(tag.Name); key != "" {
			byName[key] = tag.ID
		}
	}

	var (
		ids     tagIDs
		missing []string
	)
	if id, ok := byName[normalizeTagName(compilationTagName)]; ok {
		ids.compilation = id
	} else {
		missing = append(missing, compilationTagName)
	}
	for _, name := range longFormTagNames {
		if id, ok := byName[normalizeTagName(name)]; ok {
			ids.longForm = append(ids.longForm, id)
			continue
		}
		missing = append(missing, name)
	}
	return ids, missing
}

// normalizeTagName 去掉类別名里的空白（空格、全角空格、制表），其余原样。
func normalizeTagName(name string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(name, "\u3000", " ")), "")
}

// screen 是筛子的唯一实现：一次详情 + 三条判据。
//
// 详情读不到时返回错误：调用方据此把这件作品**留到下一轮**（不落任务、不落跳过行），
// 与「磁力为空不落行」同一处置——上游抖一下不该变成一张永久记录。
//
// 详情源根本没装配（nil）时整条筛子跳过：三条判据的输入全在详情里（演员数与类別），
// 没有详情就没有能判的东西，于是这一部照常收下——宁可多收几部，也不要因为少装一个
// 依赖就把一个循环打挂。日志只记一次（与「词表读不到」那条同一口径：这是部署的事，
// 不该每轮刷一行）。
func (r *Runner) screen(ctx context.Context, movieID, token string, ids tagIDs) (screenResult, error) {
	if r.details == nil {
		r.noDetails.Do(func() {
			r.logger.Warn("订阅筛子：详情源没装配，三条判据整条跳过——这一轮看见的作品全部收下")
		})
		return screenResult{}, nil
	}
	result, err := r.details.Detail(ctx, token, movieID)
	if err != nil {
		return screenResult{}, err
	}
	return judgeDetail(result.Movie, ids), nil
}

// judgeDetail 是三条判据（ADR-0017），纯函数：详情 + 词表 id → 命中的判据。
//
// 同时命中时取表里靠前的那一条（演员数 → 精選綜合 → 长合集）：都是「跳过」，
// reason 只是面板上「为什么被排掉」的解释，不必为多重命中另立一种。
func judgeDetail(movie appapi.MovieDetail, ids tagIDs) screenResult {
	out := screenResult{Title: movie.Title}
	if len(movie.Actors) > 1 {
		out.Reason = metrics.ReasonNotSingle
		return out
	}
	for _, tag := range movie.Tags {
		if ids.compilation != "" && tag.ID == ids.compilation {
			out.Reason = metrics.ReasonCompilation
			return out
		}
	}
	for _, tag := range movie.Tags {
		for _, id := range ids.longForm {
			if tag.ID == id {
				out.Reason = metrics.ReasonLongForm
				return out
			}
		}
	}
	return out
}
