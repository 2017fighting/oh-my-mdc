package telegram

import (
	"fmt"
	"html"
	"strconv"
	"strings"
)

// 模板照规格 §5.6 原文（zh-CN）。四件事在这里固定：
//
//   - 槽位顺序与分隔符（｜ / 换行）不随调用方变；
//   - 用户/上游内容（番号、标题、标签、阶段、原因）一律 HTML 转义；
//   - 缺省槽位给出能读的兜底（普通 / 未知来源 / 大小未知），不留空槽；
//   - 「新版本入库」是入库模板的文案分支，不是第五个事件。
const (
	downloadStartedTemplate  = "🎬 <b>%s</b> 开始下载\n%s ｜ %s ｜ %s"
	filedTemplate            = "✅ <b>%s</b> 已入库\n%s\n%s ｜ %s ｜ %s"
	filedNewVersionTemplate  = "✅ <b>%s</b> 新版本入库\n%s\n%s ｜ %s ｜ %s"
	processingFailedTemplate = "⚠️ <b>%s</b> 加工失败\n阶段：%s ｜ 原因：%s\n请到面板重试"
	downloadStalledTemplate  = "⏳ <b>%s</b> 下载停滞\n最后进度 %s，已停滞 %s"
)

// eventSpec 是一个通知事件的全部差异：开关列 + 模板。
//
// 三个消费者（KnownEvent / Config.EnabledFor / Render）都从这一张表取词，
// 所以「加第五个事件」是一处改动，不是三处 switch 各加一条分支。
type eventSpec struct {
	// switchName 是 telegram 表上那个布尔列（规格 §4.3），也是向导上的勾选框。
	switchName string
	// flag 读同名列的那个开关。
	flag func(Config) bool
	// render 用已转义的番号渲染正文。
	render func(payload map[string]any, escapedNumber string) string
}

var events = map[string]eventSpec{
	EventDownloadStarted: {
		switchName: "notify_download_started",
		flag:       func(c Config) bool { return c.NotifyDownloadStarted },
		render: func(payload map[string]any, number string) string {
			return fmt.Sprintf(downloadStartedTemplate,
				append([]any{number}, decoration(payload)...)...)
		},
	},
	EventFiled: {
		switchName: "notify_filed",
		flag:       func(c Config) bool { return c.NotifyFiled },
		render: func(payload map[string]any, number string) string {
			title := html.EscapeString(textOf(payload, "title"))
			if title == "" {
				title = "（暂无标题）"
			}
			template := filedTemplate
			if boolOf(payload, "new_version") {
				template = filedNewVersionTemplate
			}
			return fmt.Sprintf(template, append([]any{number, title}, decoration(payload)...)...)
		},
	},
	EventProcessingFailed: {
		switchName: "notify_processing_failed",
		flag:       func(c Config) bool { return c.NotifyProcessingFailed },
		render: func(payload map[string]any, number string) string {
			return fmt.Sprintf(processingFailedTemplate,
				number,
				html.EscapeString(textOf(payload, "phase")),
				html.EscapeString(textOf(payload, "reason")),
			)
		},
	},
	EventDownloadStalled: {
		switchName: "notify_download_stalled",
		flag:       func(c Config) bool { return c.NotifyDownloadStalled },
		render: func(payload map[string]any, number string) string {
			return fmt.Sprintf(downloadStalledTemplate,
				number, progressOf(payload), durationOf(payload))
		},
	},
}

// Render 把一条通知待办的 payload 渲染成 sendMessage 的 HTML 正文。
//
// payload 的键就是入箱处写下的那些（见 sidefx 的入箱点与规格 §5.6 的 payload 契约）：
//
//   - 公共：number、label、source、size_mib；
//   - filed：title、new_version（true = 任务重开后再入库的文案分支）；
//   - processing_failed：phase、reason；
//   - download_stalled：progress（0..1）、stalled_minutes。
//
// 未知事件或空番号是本地校验错：模板缺了身份就不是一条能读的通知，
// 让它驻留等人工比发一条「🎬 <b></b> 开始下载」好。
func Render(event string, payload map[string]any) (string, error) {
	spec, ok := events[event]
	if !ok {
		return "", ValidationError{Message: "未知的通知事件: " + event}
	}

	number := textOf(payload, "number")
	if number == "" {
		return "", ValidationError{Message: "通知缺少番号（number），无法套用模板"}
	}
	return spec.render(payload, html.EscapeString(number)), nil
}

// switchOf 取一个事件对应的开关列名（未知事件回空串）。
func switchOf(event string) string {
	return events[event].switchName
}

// decoration 是「标签 ｜ 来源 ｜ 大小」三个槽位（下载开始与入库共用）。
//
// 返回值顺序即模板槽位顺序；缺省时给兜底而不是空串——一条中间空着两格的
// 通知比没有更难看懂。
func decoration(payload map[string]any) []any {
	label := textOf(payload, "label")
	if label == "" {
		label = "普通"
	}
	return []any{
		html.EscapeString(label),
		html.EscapeString(sourceLabel(textOf(payload, "source"))),
		html.EscapeString(sizeLabel(numberOf(payload, "size_mib"))),
	}
}

// sourceLabel 把 version.source 的枚举值翻成给人看的来源（规格 §5.6 的 {M-Team/磁力}）。
func sourceLabel(source string) string {
	switch source {
	case "mteam_torrent":
		return "M-Team"
	case "javdb_magnet":
		return "磁力"
	default:
		return "未知来源"
	}
}

// sizeLabel 把 MiB 翻成一条通知里好读的大小；缺省是「大小未知」。
func sizeLabel(mib float64) string {
	if mib <= 0 {
		return "大小未知"
	}
	if mib >= 1024 {
		return strconv.FormatFloat(mib/1024, 'f', 1, 64) + " GiB"
	}
	if mib == float64(int64(mib)) {
		return strconv.FormatInt(int64(mib), 10) + " MiB"
	}
	return strconv.FormatFloat(mib, 'f', 1, 64) + " MiB"
}

// progressOf 把 0..1 的进度翻成百分比（模板槽位 {progress}）。
func progressOf(payload map[string]any) string {
	progress := numberOf(payload, "progress")
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	return strconv.Itoa(int(progress*100+0.5)) + "%"
}

// durationOf 是停滞时长（模板槽位 {时长}）：分钟起步，超过一小时换成「N 小时 M 分钟」。
func durationOf(payload map[string]any) string {
	minutes := int(numberOf(payload, "stalled_minutes"))
	if minutes < 0 {
		minutes = 0
	}
	if minutes < 60 {
		return strconv.Itoa(minutes) + " 分钟"
	}
	hours := minutes / 60
	rest := minutes % 60
	if rest == 0 {
		return strconv.Itoa(hours) + " 小时"
	}
	return fmt.Sprintf("%d 小时 %d 分钟", hours, rest)
}

// textOf 取一个字符串字段（缺字段/类型不对都回空串，首尾空白去掉）。
func textOf(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

// numberOf 取一个数值字段。payload 从 JSON 解出来时数字是 float64，
// 但同一份结构也可能是进程内直接构造的（int/int64）——两种都认。
func numberOf(payload map[string]any, key string) float64 {
	switch value := payload[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case int32:
		return float64(value)
	default:
		return 0
	}
}

// boolOf 取一个布尔字段（JSON 与进程内构造都认）。
func boolOf(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}
