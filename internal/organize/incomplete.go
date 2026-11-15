// 待补齐的四面判据（#42 / GLOSSARY「待补齐」）：入库后关键面仍有缺口且上游
// 重试耗尽的任务标注。评估的主时刻在入库那一刻——盘上的图、库里的演员、简介与
// 译文都在手上，这是唯一的完整事实时刻；写库归整理段（SetMissingFaces），
// 强制重刮补齐四面后重新入库，同一次评估负责摘标。渲染对账另做兜底的扫场代评
// （#44：0.3.0 前入库的存量任务标注全空，对旧伤失明）：调同一份 MissingFaces
// 纯函数，算出的定态与库中不符才改写——入库时评是主时刻，扫场代评是兜底回流。
package organize

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// MissingFaces 按四面判据评一份入库后的任务：无任何图 / 无演员 / 无简介 /
// 翻译启用却无中文标题，任一命中即算；**个别 tag 缺不算**（裁决原文）。
//
// dir 是入库目录（判「无任何图」看盘上事实，不看 provenance 里有没有 URL——
// URL 在而文件下不来的条目，对 Jellyfin 一样是烂条目）；meta 是任务的库值
// （不是排版用的 clean 值）；actorCount 是 task_actor 的行数；translationExpected
// 表示 AI 翻译已配置且启用（禁用时中文标题缺席不算缺，译文本该有才判）。
// 返回值按固定顺序（images → actors → outline → title_zh），全齐时为空。
func MissingFaces(dir string, meta task.Metadata, actorCount int, translationExpected bool) []string {
	faces := []string{}
	if !hasAnyImage(dir) {
		faces = append(faces, task.FaceImages)
	}
	if actorCount == 0 {
		faces = append(faces, task.FaceActors)
	}
	if strings.TrimSpace(meta.Outline) == "" {
		faces = append(faces, task.FaceOutline)
	}
	if translationExpected && strings.TrimSpace(meta.TitleZH) == "" {
		faces = append(faces, task.FaceTitleZH)
	}
	return faces
}

// hasAnyImage 报告入库目录里有没有任何一张图：poster / fanart 任一槽有文件，
// 或 extrafanart 目录非空。三处全空才算「无任何图」——那是 Jellyfin 的烂条目。
func hasAnyImage(dir string) bool {
	for _, base := range []string{PosterName, FanartName} {
		if matches, _ := filepath.Glob(filepath.Join(dir, base+".*")); len(matches) > 0 {
			return true
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, ExtraFanartDir))
	if err != nil {
		return false
	}
	return len(entries) > 0
}
