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
