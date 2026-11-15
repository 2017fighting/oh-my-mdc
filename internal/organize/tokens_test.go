package organize_test

import (
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/organize"
)

// 测试缝：§7.2 文件名分词的反方向。planFiles 把 (番号, label) 拼成文件名；
// 补录（#43）要从 mdcng 时代的现存文件名里把 (label, 分片序号) 读回来。
// 两个方向必须出自同一份词表（中文/破解、cd/disc/disk/part），否则同名异判。

func TestParseFileNameTokens(t *testing.T) {
	cases := []struct {
		number    string
		stem      string
		wantLabel string
		wantPart  int
	}{
		{"SONE-682", "SONE-682", "", 1},
		{"SONE-682", "SONE-682-中文", "中文", 1},
		{"SONE-682", "SONE-682-破解", "破解", 1},
		{"SONE-682", "SONE-682-cd2", "", 2},
		{"SONE-682", "SONE-682-中文-cd2", "中文", 2},
		{"SONE-682", "SONE-682-Disc1", "", 1},
		{"SONE-682", "SONE-682-part10", "", 10},
		{"SONE-682", "SONE-682-C", "", 1},  // mdcng 时代的词表外后缀：不猜标签
		{"SONE-682", "SONE-682-4k", "", 1}, // 同上
		{"SONE-682", "SONE-682-CD1-cd2", "", 2},
	}
	for _, c := range cases {
		label, part := organize.ParseFileNameTokens(c.number, c.stem)
		if label != c.wantLabel || part != c.wantPart {
			t.Errorf("ParseFileNameTokens(%q, %q) = (%q, %d), want (%q, %d)",
				c.number, c.stem, label, part, c.wantLabel, c.wantPart)
		}
	}
}
