package organize

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/media"
)

// 测试缝：文件级计划（分片、字幕、自然排序）是纯函数，规格 §7.2 的每一条规则
// 都能在表里读出来；discover 只做「下载目录 → 白名单文件」这一件事。

func src(relPath string, size int64) sourceFile {
	stem := relPath
	if ext := filepath.Ext(relPath); ext != "" {
		stem = relPath[:len(relPath)-len(ext)]
	}
	return sourceFile{
		Path:    "/downloads/" + relPath,
		RelPath: relPath,
		Ext:     filepath.Ext(relPath),
		Stem:    stem,
		Size:    size,
	}
}

func videosOf(plan Plan) []string {
	names := []string{}
	for _, file := range plan.Videos() {
		names = append(names, file.TargetName)
	}
	return names
}

// TestSingleVideoUsesTheBareName 钉住「单片版本的裸名」（P14-2）。
func TestSingleVideoUsesTheBareName(t *testing.T) {
	plan, err := planFiles("ABP-123", "", []sourceFile{src("ABP-123.mkv", 1024)}, nil)
	if err != nil {
		t.Fatalf("planFiles: %v", err)
	}
	videos := plan.Videos()
	if len(videos) != 1 || videos[0].TargetName != "ABP-123.mkv" {
		t.Fatalf("videos = %+v, want ABP-123.mkv", videos)
	}
	if !videos[0].IsPrimary || videos[0].PartIndex != 1 {
		t.Errorf("is_primary = %v, part_index = %d, want true / 1", videos[0].IsPrimary, videos[0].PartIndex)
	}
	if plan.VideoBytes != 1024 {
		t.Errorf("VideoBytes = %d, want 1024", plan.VideoBytes)
	}
}

// TestLabelSuffixComesFromTheVocabulary 钉住词表：「中文/破解」两个词，别的都不加后缀。
func TestLabelSuffixComesFromTheVocabulary(t *testing.T) {
	cases := []struct{ label, want string }{
		{"中文", "-中文"},
		{"破解", "-破解"},
		{"", ""},
		{"无码", ""},
	}
	for _, c := range cases {
		plan, err := planFiles("ABP-123", c.label, []sourceFile{src("ABP-123.mkv", 1)}, nil)
		if err != nil {
			t.Fatalf("planFiles(%q): %v", c.label, err)
		}
		if got := plan.Videos()[0].TargetName; got != "ABP-123"+c.want+".mkv" {
			t.Errorf("label %q → %q, want %q", c.label, got, "ABP-123"+c.want+".mkv")
		}
	}
}

// TestMultiPartKeepsExplicitShardNumbers 钉住「多片全显式含 cd1」与 is_primary=cd1（P14-1）。
func TestMultiPartKeepsExplicitShardNumbers(t *testing.T) {
	videos := []sourceFile{
		src("ABP-123-cd2.mkv", 2048),
		src("ABP-123-cd1.mkv", 1024),
	}
	plan, err := planFiles("ABP-123", "中文", videos, nil)
	if err != nil {
		t.Fatalf("planFiles: %v", err)
	}

	got := map[string]PlannedFile{}
	for _, file := range plan.Videos() {
		got[file.TargetName] = file
	}
	first, ok := got["ABP-123-中文-cd1.mkv"]
	if !ok {
		t.Fatalf("videos = %v, want ABP-123-中文-cd1.mkv", videosOf(plan))
	}
	if !first.IsPrimary || first.PartIndex != 1 {
		t.Errorf("cd1: is_primary = %v, part_index = %d, want true / 1", first.IsPrimary, first.PartIndex)
	}
	if second := got["ABP-123-中文-cd2.mkv"]; second.IsPrimary || second.PartIndex != 2 {
		t.Errorf("cd2: is_primary = %v, part_index = %d, want false / 2", second.IsPrimary, second.PartIndex)
	}
}

// TestShardPatternCoversDiscAndPartSpellings 覆盖正则的几种写法（大小写、分隔符）。
func TestShardPatternCoversDiscAndPartSpellings(t *testing.T) {
	cases := []struct {
		stem string
		want int
		ok   bool
	}{
		{"ABP-123-cd1", 1, true},
		{"ABP-123_CD02", 2, true},
		{"ABP-123.disc3", 3, true},
		{"ABP-123 disk4", 4, true},
		{"ABP-123part5", 5, true},
		{"ABP-123", 0, false},
		{"ABP-123-cd99", 99, true},
	}
	for _, c := range cases {
		got, ok := shardIndexOf(c.stem)
		if ok != c.ok || got != c.want {
			t.Errorf("shardIndexOf(%q) = (%d, %v), want (%d, %v)", c.stem, got, ok, c.want, c.ok)
		}
	}
}

// TestUnnumberedMultiPartGetsNaturalOrder 钉住「多片但无序号 → 自然排序赋顺序号」（P14-2）。
func TestUnnumberedMultiPartGetsNaturalOrder(t *testing.T) {
	videos := []sourceFile{
		src("ABP-123 part2.mp4", 2),
		src("ABP-123 part10.mp4", 10),
		src("ABP-123 part1.mp4", 1),
	}
	sortNatural(videos)
	t.Logf("natural order = %v", []string{videos[0].RelPath, videos[1].RelPath, videos[2].RelPath})

	// 分片序号已能解析（partN），所以这里显式验证「解析不全」的那一档：
	// 文件名里完全没有序号时才按自然排序赋 1..n。
	unnumbered := []sourceFile{
		src("ABP-123-a.mkv", 1),
		src("ABP-123-b.mkv", 2),
		src("ABP-123-c.mkv", 3),
	}
	plan, err := planFiles("ABP-123", "", unnumbered, nil)
	if err != nil {
		t.Fatalf("planFiles: %v", err)
	}
	want := []string{"ABP-123-cd1.mkv", "ABP-123-cd2.mkv", "ABP-123-cd3.mkv"}
	if got := videosOf(plan); !equal(got, want) {
		t.Errorf("videos = %v, want %v", got, want)
	}
	for i, file := range plan.Videos() {
		if file.PartIndex != i+1 || file.IsPrimary != (i == 0) {
			t.Errorf("%s: part_index = %d, is_primary = %v", file.TargetName, file.PartIndex, file.IsPrimary)
		}
	}
}

// TestPartiallyNumberedMultiPartFallsBackToSequence 钉住「解析不全」这一档：
// 只要有一片没有序号，就整条版本按自然顺序重排（不混用两种编号）。
func TestPartiallyNumberedMultiPartFallsBackToSequence(t *testing.T) {
	videos := []sourceFile{
		src("ABP-123-cd2.mkv", 2),
		src("ABP-123-extra.mkv", 1),
	}
	plan, err := planFiles("ABP-123", "", videos, nil)
	if err != nil {
		t.Fatalf("planFiles: %v", err)
	}
	want := []string{"ABP-123-cd1.mkv", "ABP-123-cd2.mkv"}
	if got := videosOf(plan); !equal(got, want) {
		t.Errorf("videos = %v, want %v", got, want)
	}
}

// TestSubtitlesFollowTheirVideo 钉住字幕同名硬链与语言后缀。
func TestSubtitlesFollowTheirVideo(t *testing.T) {
	videos := []sourceFile{src("ABP-123.mkv", 100)}
	subtitles := []sourceFile{
		src("ABP-123.srt", 1),
		src("ABP-123.chs.srt", 1),
		src("ABP-123-cd9.ass", 1), // 没有对应视频：不落盘
	}
	plan, err := planFiles("ABP-123", "", videos, subtitles)
	if err != nil {
		t.Fatalf("planFiles: %v", err)
	}

	got := map[string]bool{}
	for _, file := range plan.Files {
		got[file.TargetName] = file.Subtitle
	}
	if !got["ABP-123.srt"] || !got["ABP-123.chs.srt"] {
		t.Fatalf("files = %+v, want 两条同名/带语言后缀的字幕", plan.Files)
	}
	if _, ok := got["ABP-123-cd9.ass"]; ok {
		t.Error("没有对应视频的字幕不该落盘")
	}
	if len(plan.Videos()) != 1 {
		t.Errorf("Videos() 把字幕也算成视频了: %+v", plan.Videos())
	}
}

// TestMultiPartSubtitlesKeepTheirShard 钉住分片字幕：跟着各自的分片名走。
func TestMultiPartSubtitlesKeepTheirShard(t *testing.T) {
	videos := []sourceFile{
		src("ABP-123-cd1.mkv", 1),
		src("ABP-123-cd2.mkv", 1),
	}
	subtitles := []sourceFile{
		src("ABP-123-cd1.chs.srt", 1),
		src("ABP-123-cd2.srt", 1),
	}
	plan, err := planFiles("ABP-123", "", videos, subtitles)
	if err != nil {
		t.Fatalf("planFiles: %v", err)
	}

	names := []string{}
	for _, file := range plan.Files {
		if file.Subtitle {
			names = append(names, file.TargetName)
		}
	}
	want := []string{"ABP-123-cd1.chs.srt", "ABP-123-cd2.srt"}
	if !equal(names, want) {
		t.Errorf("subtitles = %v, want %v", names, want)
	}
}

// TestCollisionInsideOnePlanIsAnError 钉住同一条版本里的重名：那是数据问题，不覆盖。
func TestCollisionInsideOnePlanIsAnError(t *testing.T) {
	videos := []sourceFile{
		src("ABP-123-cd1.mkv", 1),
		src("other/ABP-123-cd1.mkv", 1),
	}
	if _, err := planFiles("ABP-123", "", videos, nil); !errors.Is(err, ErrCollision) {
		t.Errorf("err = %v, want ErrCollision", err)
	}
}

// TestDiscoverOnlyWhitelistsVideoAndSubtitles 钉住「视频/字幕外文件不落盘」。
func TestDiscoverOnlyWhitelistsVideoAndSubtitles(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"ABP-123-cd1.mkv":        "v1",
		"ABP-123-cd2.MP4":        "v2",
		"ABP-123-cd1.chs.srt":    "s1",
		"readme.txt":             "广告",
		"cover.jpg":              "",
		"ABP-123.nfo":            "自带 nfo 不落盘",
		"sample/ABP-123-cd3.avi": "v3",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	videos, subtitles, err := discover(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	videoNames := []string{}
	for _, video := range videos {
		if !media.IsVideo(video.Path) {
			t.Fatalf("%s 不是视频却被收进来了", video.RelPath)
		}
		videoNames = append(videoNames, video.RelPath)
	}
	want := []string{"ABP-123-cd1.mkv", "ABP-123-cd2.MP4", "sample/ABP-123-cd3.avi"}
	if !equal(videoNames, want) {
		t.Errorf("videos = %v, want %v", videoNames, want)
	}
	if len(subtitles) != 1 || subtitles[0].RelPath != "ABP-123-cd1.chs.srt" {
		t.Errorf("subtitles = %+v, want 只有 ABP-123-cd1.chs.srt", subtitles)
	}
}

// TestDiscoverSingleVideoFile 钉住「content_path 是单文件」这一档。
func TestDiscoverSingleVideoFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "ABP-123.mkv")
	if err := os.WriteFile(path, []byte("video"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	videos, subtitles, err := discover(path)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(videos) != 1 || videos[0].RelPath != "ABP-123.mkv" || videos[0].Size != 5 {
		t.Fatalf("videos = %+v", videos)
	}
	if len(subtitles) != 0 {
		t.Errorf("subtitles = %+v, want 空", subtitles)
	}
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
