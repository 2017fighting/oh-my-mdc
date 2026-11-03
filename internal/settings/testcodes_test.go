package settings

import "testing"

// TestIsPermanentCodeIsTheSpec9Classification 钉住规格 §9 的 permanent 分类边界：
// 凭据与「重试变不出来」的错立刻驻留；网络、超时与协议不一致按退避重试。
//
// 各集成的 IsPermanent（appapi / telegram）与 sidefx 的驻留判据都走这一处，
// 所以这张表就是「哪些错不重试」的唯一答案。
func TestIsPermanentCodeIsTheSpec9Classification(t *testing.T) {
	cases := map[TestCode]bool{
		CodeAuth:            true,
		CodeBusiness:        true,
		CodeTokenInvalid:    true,
		CodeChatUnavailable: true,
		CodeNetwork:         false,
		CodeTimeout:         false,
		CodeProtocol:        false,
		// 占位码不是真实失败，更不是永久的：真接线的集成本就不该产出它。
		CodeNotImplemented: false,
	}

	for code, want := range cases {
		if got := IsPermanentCode(code); got != want {
			t.Errorf("IsPermanentCode(%q) = %v, want %v", code, got, want)
		}
	}
}

// TestOptionalEnabledIsTheSingleEnableRule 钉住可选集成的启用判据：
// 未配置、没启用、enabled 还没写过（nil）都算关着；只有「已配置 + true」才开着。
//
// 它是 mteam / ai_translation / telegram 三个执行半段共用的唯一判据。
func TestOptionalEnabledIsTheSingleEnableRule(t *testing.T) {
	yes, no := true, false
	cases := map[string]struct {
		snapshot Snapshot
		want     bool
	}{
		"节不存在":       {Snapshot{Sections: map[string]SectionState{}}, false},
		"未配置":        {Snapshot{Sections: map[string]SectionState{"ai_translation": {Enabled: &yes}}}, false},
		"enabled 为空": {Snapshot{Sections: map[string]SectionState{"ai_translation": {Configured: true}}}, false},
		"已配置但禁用":     {Snapshot{Sections: map[string]SectionState{"ai_translation": {Configured: true, Enabled: &no}}}, false},
		"已配置且启用":     {Snapshot{Sections: map[string]SectionState{"ai_translation": {Configured: true, Enabled: &yes}}}, true},
	}
	for name, c := range cases {
		if got := c.snapshot.OptionalEnabled("ai_translation"); got != c.want {
			t.Errorf("%s: OptionalEnabled = %v, want %v", name, got, c.want)
		}
	}
}
