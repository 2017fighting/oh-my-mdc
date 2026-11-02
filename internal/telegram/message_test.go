package telegram

import (
	"strings"
	"testing"
)

// 模板照规格 §5.6 原文：四事件各一条，用户内容 HTML 转义，无图。
// 断言到整串字符：模板是给人看的文案，改动应当是一个明确的决定。

func TestRenderDownloadStarted(t *testing.T) {
	text, err := Render(EventDownloadStarted, map[string]any{
		"number":   "SSIS-001",
		"label":    "中文",
		"source":   "javdb_magnet",
		"size_mib": 1536.0,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "🎬 <b>SSIS-001</b> 开始下载\n中文 ｜ 磁力 ｜ 1.5 GiB"
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

func TestRenderFiled(t *testing.T) {
	text, err := Render(EventFiled, map[string]any{
		"number":   "SSIS-001",
		"title":    "SSIS-001 中文版",
		"label":    "中文",
		"source":   "mteam_torrent",
		"size_mib": 800.0,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "✅ <b>SSIS-001</b> 已入库\nSSIS-001 中文版\n中文 ｜ M-Team ｜ 800 MiB"
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

// TestRenderFiledNewVersionBranch 钉住「任务重开再入库＝新版本入库」的文案分支（P12-2）：
// 同一部片再来一条版本时，标题行换成「新版本已入库」，其余槽位与首次入库一致。
func TestRenderFiledNewVersionBranch(t *testing.T) {
	text, err := Render(EventFiled, map[string]any{
		"number":      "SSIS-001",
		"title":       "SSIS-001 中文字幕版",
		"label":       "中文",
		"source":      "mteam_torrent",
		"size_mib":    2048.0,
		"new_version": true,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// 文案照规格 §5.6 的措辞：「任务重开再入库 = 新版本入库」。
	want := "✅ <b>SSIS-001</b> 新版本入库\nSSIS-001 中文字幕版\n中文 ｜ M-Team ｜ 2.0 GiB"
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

func TestRenderProcessingFailed(t *testing.T) {
	text, err := Render(EventProcessingFailed, map[string]any{
		"number": "SSIS-001",
		"phase":  "organize",
		"reason": "硬链接跨盘（EXDEV）",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "⚠️ <b>SSIS-001</b> 加工失败\n阶段：organize ｜ 原因：硬链接跨盘（EXDEV）\n请到面板重试"
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

func TestRenderDownloadStalled(t *testing.T) {
	text, err := Render(EventDownloadStalled, map[string]any{
		"number":          "SSIS-001",
		"progress":        0.42,
		"stalled_minutes": 65,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "⏳ <b>SSIS-001</b> 下载停滞\n最后进度 42%，已停滞 1 小时 5 分钟"
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

// TestRenderEscapesUserContent 钉住 P12-2：番号/标题/阶段/原因都是用户或上游内容，
// 进 HTML 模板前一律转义——否则标题里的 <b> 会把整条消息排成另一种样子。
func TestRenderEscapesUserContent(t *testing.T) {
	text, err := Render(EventFiled, map[string]any{
		"number": "A&B-001<script>",
		"title":  `标题里有 <b>粗体</b> 与 "引号"`,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(text, "<script>") || strings.Contains(text, "<b>粗体</b>") {
		t.Errorf("text = %q, want 用户内容被转义", text)
	}
	if !strings.Contains(text, "A&amp;B-001&lt;script&gt;") {
		t.Errorf("text = %q, want 番号里的 & < > 转义", text)
	}
	if !strings.Contains(text, "&lt;b&gt;粗体&lt;/b&gt;") {
		t.Errorf("text = %q, want 标题里的标签转义", text)
	}

	tag, err := Render(EventProcessingFailed, map[string]any{
		"number": "SSIS-001",
		"phase":  "scrape",
		"reason": `<i>坏</i>`,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(tag, "&lt;i&gt;坏&lt;/i&gt;") {
		t.Errorf("text = %q, want 原因里的标签转义", tag)
	}
}

// TestRenderFallsBackForMissingDecoration：缺标签/来源/大小时槽位仍在，给出能读的兜底，
// 而不是把「 ｜  ｜ 」留给用户。
func TestRenderFallsBackForMissingDecoration(t *testing.T) {
	text, err := Render(EventDownloadStarted, map[string]any{"number": "SSIS-001"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "🎬 <b>SSIS-001</b> 开始下载\n普通 ｜ 未知来源 ｜ 大小未知"
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

// TestRenderRejectsUnknownShape：模板缺番号或事件名不认识时是本地校验错（business），
// 让待办驻留等人工——发一条没有番号的通知比不发更糟。
func TestRenderRejectsUnknownShape(t *testing.T) {
	if _, err := Render(EventDownloadStarted, map[string]any{}); err == nil {
		t.Error("缺番号应当报错")
	}
	if _, err := Render("unknown_event", map[string]any{"number": "SSIS-001"}); err == nil {
		t.Error("未知事件应当报错")
	}
}

// TestKnownEventCoversAllFour：引擎靠它区分「开关关着」与「事件名写错了」。
func TestKnownEventCoversAllFour(t *testing.T) {
	for _, event := range []string{
		EventDownloadStarted, EventFiled, EventProcessingFailed, EventDownloadStalled,
	} {
		if !KnownEvent(event) {
			t.Errorf("KnownEvent(%q) = false", event)
		}
	}
	if KnownEvent("download_finished") {
		t.Error("KnownEvent(download_finished) = true, want false")
	}
}
