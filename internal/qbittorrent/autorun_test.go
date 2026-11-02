package qbittorrent

import (
	"errors"
	"strings"
	"testing"
)

const testBaseURL = "http://oh-my-mdc.oh-my-mdc.svc:8080"

// TestPlanAutorunWrapsExistingProgramAndKeepsIt 钉住 P3-2/P3-4：既有 moviepilot 命令
// 原样保留（双引号归一成单引号）并包进 sh -c，本项目回调追加在后。
func TestPlanAutorunWrapsExistingProgramAndKeepsIt(t *testing.T) {
	plan, err := PlanAutorun(Autorun{
		Enabled: true,
		Program: `curl "http://moviepilot.media.svc:3000/api/v1/transfer/now?token=abc123"`,
	}, testBaseURL)
	if err != nil {
		t.Fatalf("PlanAutorun: %v", err)
	}

	want := `/bin/sh -c "curl 'http://moviepilot.media.svc:3000/api/v1/transfer/now?token=abc123' ; ` +
		`curl -fsS -m 10 'http://oh-my-mdc.oh-my-mdc.svc:8080/api/hooks/qbt?ih=%I'"`
	if plan.After != want {
		t.Errorf("After = %s\nwant %s", plan.After, want)
	}
	if plan.Skip || !plan.Changed {
		t.Errorf("skip/changed = %v/%v, want false/true", plan.Skip, plan.Changed)
	}
	if !strings.Contains(plan.After, "transfer/now?token=abc123") {
		t.Error("moviepilot 的原命令必须原样保留")
	}
}

// TestPlanAutorunNormalisesBareArgvQuotes 钉住 P3-4 的字面：旧命令的双引号一律
// 归一成单引号（哪怕这个参数不带 shell 特殊字符），参数语义不变。
func TestPlanAutorunNormalisesBareArgvQuotes(t *testing.T) {
	plan, err := PlanAutorun(Autorun{
		Enabled: true,
		Program: `curl "http://moviepilot.media.svc:3000/transfer/now"`,
	}, testBaseURL)
	if err != nil {
		t.Fatalf("PlanAutorun: %v", err)
	}

	prefix := "/bin/sh -c \"curl 'http://moviepilot.media.svc:3000/transfer/now' ; "
	if !strings.HasPrefix(plan.After, prefix) {
		t.Errorf("After = %s\nwant 前缀 %s（双引号归一成单引号）", plan.After, prefix)
	}
}

// TestPlanAutorunIsIdempotent 钉住 P3-3：现值已含本项目 URL → 跳过不改。
func TestPlanAutorunIsIdempotent(t *testing.T) {
	already := `/bin/sh -c "curl 'http://moviepilot.media.svc:3000/transfer/now?token=abc' ; ` +
		`curl -fsS -m 10 'http://oh-my-mdc.oh-my-mdc.svc:8080/api/hooks/qbt?ih=%I'"`

	plan, err := PlanAutorun(Autorun{Enabled: true, Program: already}, testBaseURL)
	if err != nil {
		t.Fatalf("PlanAutorun: %v", err)
	}
	if !plan.Skip {
		t.Fatal("现值已含本项目 URL，应当跳过")
	}
	if plan.Changed {
		t.Error("跳过的 plan 不该报告 changed")
	}
	if plan.After != already {
		t.Errorf("After = %s, want 现值原样", plan.After)
	}
}

// TestPlanAutorunPutsHookAloneWhenNothingIsConfigured 确认空现值只落一条本项目回调。
func TestPlanAutorunPutsHookAloneWhenNothingIsConfigured(t *testing.T) {
	plan, err := PlanAutorun(Autorun{Enabled: true}, testBaseURL)
	if err != nil {
		t.Fatalf("PlanAutorun: %v", err)
	}

	want := `/bin/sh -c "curl -fsS -m 10 'http://oh-my-mdc.oh-my-mdc.svc:8080/api/hooks/qbt?ih=%I'"`
	if plan.After != want {
		t.Errorf("After = %s\nwant %s", plan.After, want)
	}
}

// TestPlanAutorunTrimsTrailingSlashOnBaseURL 确认 base URL 末尾斜杠不会拼出双斜杠。
func TestPlanAutorunTrimsTrailingSlashOnBaseURL(t *testing.T) {
	hook, err := HookURL(testBaseURL + "/")
	if err != nil {
		t.Fatalf("HookURL: %v", err)
	}
	if hook != testBaseURL+HookPath {
		t.Errorf("hook = %q, want %q", hook, testBaseURL+HookPath)
	}
}

// TestPlanAutorunWarnsWhenAutorunIsDisabled 确认 autorun 未启用时只警告、照旧给计划：
// ADR-0007 只碰 autorun_program，不改用户的开关。
func TestPlanAutorunWarnsWhenAutorunIsDisabled(t *testing.T) {
	plan, err := PlanAutorun(Autorun{Enabled: false, Program: `curl "http://moviepilot"`}, testBaseURL)
	if err != nil {
		t.Fatalf("PlanAutorun: %v", err)
	}
	if plan.Warning == "" {
		t.Fatal("autorun 未启用时应当带入警告，让向导明说回调不会触发")
	}
	if plan.Skip {
		t.Error("未启用不等于跳过：program 仍然要给出来")
	}
}

// TestPlanAutorunRejectsUnsafeProgram 确认无法安全归一（单引号落单）的现值不动、
// 报出来让人自己改：宁可不改，也不能拼出一条会跑歪的 shell 命令。
func TestPlanAutorunRejectsUnsafeProgram(t *testing.T) {
	_, err := PlanAutorun(Autorun{Program: `curl "http://x/?a='b"`}, testBaseURL)
	if err == nil {
		t.Fatal("单引号落单的现值应当被拒绝")
	}
	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
}

// TestPlanAutorunRequiresBaseURL 确认没有回调 base URL 时不猜地址。
func TestPlanAutorunRequiresBaseURL(t *testing.T) {
	if _, err := PlanAutorun(Autorun{Program: "curl http://x"}, "  "); err == nil {
		t.Fatal("没有 webhook base URL 应当报错")
	}
	if _, err := HookURL("not a url"); err == nil {
		t.Fatal("不是 http(s) 地址应当报错")
	}
}
