package media_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/media"
)

// TestVideoWhitelistIsTheSpecList 钉住规格 §7.2 的视频白名单。
//
// 它是「下载完成」判定的唯一真相（§6.1 第三条），也是入库时挑视频文件的唯一真相：
// 两处各写一份就会对同一个文件得出两个结论。
func TestVideoWhitelistIsTheSpecList(t *testing.T) {
	videos := []string{
		"ABP-123.mp4", "ABP-123.mkv", "ABP-123.avi", "ABP-123.wmv", "ABP-123.mov",
		"ABP-123.m2ts", "ABP-123.ts", "ABP-123.rmvb", "ABP-123.flv", "ABP-123.webm",
		"ABP-123.mpg", "ABP-123.mpeg",
		// 大小写不敏感：源文件名不由本项目决定。
		"ABP-123.MP4", "ABP-123.MkV",
	}
	for _, name := range videos {
		if !media.IsVideo(name) {
			t.Errorf("IsVideo(%q) = false, want true", name)
		}
	}

	notVideos := []string{"ABP-123.jpg", "movie.nfo", "ABP-123.srt", "x.torrent", "", "mp4", "x.mp4.part"}
	for _, name := range notVideos {
		if media.IsVideo(name) {
			t.Errorf("IsVideo(%q) = true, want false", name)
		}
	}
}

// TestHasVideoJudgesFilesAndDirectories 钉住「content_path 可读含视频文件」这一条：
// qbt 的 content_path 可能是单文件，也可能是种子根目录。
func TestHasVideoJudgesFilesAndDirectories(t *testing.T) {
	root := t.TempDir()

	single := filepath.Join(root, "single.mkv")
	write(t, single)

	nested := filepath.Join(root, "nested", "ABP-123")
	write(t, filepath.Join(nested, "ABP-123-cd1.mp4"))
	write(t, filepath.Join(nested, "ABP-123-cd1.srt"))

	noVideo := filepath.Join(root, "no-video")
	write(t, filepath.Join(noVideo, "readme.txt"))

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"单个视频文件", single, true},
		{"目录里嵌套视频", nested, true},
		{"目录里只有非视频", noVideo, false},
		{"空路径", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := media.HasVideo(tc.path)
			if err != nil {
				t.Fatalf("HasVideo(%q) error = %v", tc.path, err)
			}
			if got != tc.want {
				t.Errorf("HasVideo(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}

	if _, err := media.HasVideo(filepath.Join(root, "gone")); err == nil {
		t.Error("HasVideo(不存在的路径) error = nil, want 错误（读不到就不是可读）")
	}
}

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录 %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("写 %s: %v", path, err)
	}
}
