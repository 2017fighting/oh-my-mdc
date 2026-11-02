// Package organize 是加工阶段的第二段：把下载完成的文件硬链进媒体库，
// 并落下 NFO 与三类图片（规格 §7.1/§7.2，验收 P9-3/4/5、P11-1、P14-1/2）。
//
// 它接在取数（#27）之后、旁路副作用（#26）之前：
//
//  1. 复用判据先于网络：任务元数据已存在 → 0 次上游调用，直接整理（只硬链新视频文件）；
//  2. 布局固定：`{media_root}/{番号}/{番号}[-中文|-破解][-cdN]{扩展名}`，多版本同目录
//     共享一份 movie.nfo + 图片（ADR-0008，v1 无模板）；
//  3. 硬链模式唯一：EXDEV 是失败并明示换挂载，绝不静默降级 copy；
//  4. 碰撞不覆盖：目标已存在且不是同一个 inode、或同番号已属于别的任务 → 后到者失败驻留；
//  5. 入库成功才换 tag（mdc:in-progress → mdc:finished）并置刷库/通知待办；
//  6. 加工失败按指数退避重试，耗尽落 failed(phase, reason) 驻留等人工 + 通知一次。
//
// 本包不碰下载与取数：content_path 从 qBittorrent 回读（它才是事实源），元数据从库里读。
package organize

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/2017fighting/oh-my-mdc/internal/media"
)

// 词表 v1 只有两个后缀词（规格 §7.2：扩词只加词不改布局）。
const (
	labelChinese = "中文"
	labelCracked = "破解"
)

// subtitleExtensions 是随视频同名硬链的字幕扩展名（规格 §7.2，小写）。
var subtitleExtensions = map[string]bool{
	".srt": true, ".ass": true, ".ssa": true, ".vtt": true,
	".sub": true, ".idx": true, ".sup": true,
}

// shardPattern 是分片序号的正则（规格 §7.2）：cd/disc/disk/part + 1~2 位数字，
// 大小写不敏感，分隔符可有可无（`ABP-1cd2` 也是分片）。
var shardPattern = regexp.MustCompile(`(?i)[ _.\-]?(?:cd|disc|disk|part)[ _.\-]?(\d{1,2})`)

// ErrCollision 是「目标已存在且不是同一个文件」：不覆盖、不 -v2，后到者失败驻留（规格 §7.2）。
var ErrCollision = errors.New("organize: 目标已存在且不是同一个文件")

// ErrCrossDevice 是硬链的 EXDEV：下载目录与媒体库不在同一挂载。绝不降级 copy（规格 §7.2）。
var ErrCrossDevice = errors.New("organize: 跨设备（EXDEV），下载目录与媒体库不在同一挂载")

// ErrNoVideoFiles 是「下载材料里没有视频」：它的失败阶段是下载而不是整理（规格 §6.1 文件丢失）。
var ErrNoVideoFiles = errors.New("organize: 下载里没有视频文件（文件丢失）")

// sourceFile 是下载目录里的一个文件（视频或字幕）。
type sourceFile struct {
	// Path 是绝对路径（硬链的源）。
	Path string
	// RelPath 是相对下载根的位置（version_file 的永久记录）。
	RelPath string
	// Ext 是小写扩展名（带点）。
	Ext string
	// Stem 是去扩展名的文件名。
	Stem string
	// Size 是文件字节数。
	Size int64
}

// PlannedFile 是一条要硬链的源文件与它落盘后的文件名。
type PlannedFile struct {
	Source sourceFile
	// TargetName 是目标目录里的文件名（不含目录）。
	TargetName string
	// PartIndex 从 1 起：单片版本是 1。
	PartIndex int
	// IsPrimary 标记主分片（cd1 / 最前那一片）。
	IsPrimary bool
	// Subtitle 区分字幕：只有视频进 version_file。
	Subtitle bool
}

// Plan 是一条版本的文件级计划。
type Plan struct {
	// Files 是全部要落盘的文件（视频在前、字幕在后，各自按自然顺序）。
	Files []PlannedFile
	// VideoBytes 是这条版本的视频总字节数（通知里的「大小」）。
	VideoBytes int64
}

// Videos 只返回视频分片。
func (p Plan) Videos() []PlannedFile {
	videos := make([]PlannedFile, 0, len(p.Files))
	for _, file := range p.Files {
		if !file.Subtitle {
			videos = append(videos, file)
		}
	}
	return videos
}

// labelSuffix 把版本的展示标签翻成目录名后缀：词表 v1 只有「中文/破解」两个词。
//
// 词表外的标签回空串（不猜）：名字加不加后缀必须由词表决定，否则「无标签版本」
// 与「奇怪标签版本」会在同一个文件名上撞车，而碰撞在那一步只能失败。
func labelSuffix(label string) string {
	switch strings.TrimSpace(label) {
	case labelChinese:
		return "-" + labelChinese
	case labelCracked:
		return "-" + labelCracked
	}
	return ""
}

// discover 扫下载落盘的位置，按白名单挑出视频与字幕（别的文件一律不落盘）。
//
// contentPath 可能是单个视频文件，也可能是种子的根目录：单个文件时 RelPath 就是
// 文件名，目录时是相对根的路径。两种都返回自然排序后的两个清单。
func discover(contentPath string) (videos, subtitles []sourceFile, err error) {
	contentPath = strings.TrimSpace(contentPath)
	if contentPath == "" {
		return nil, nil, errors.New("organize: 没有下载落盘路径（content_path 为空）")
	}

	info, err := os.Stat(contentPath)
	if err != nil {
		return nil, nil, fmt.Errorf("organize: 读下载落盘路径 %s: %w", contentPath, err)
	}

	if !info.IsDir() {
		file, err := newSourceFile(contentPath, filepath.Base(contentPath), info)
		if err != nil {
			return nil, nil, err
		}
		if media.IsVideo(file.Path) {
			return []sourceFile{file}, nil, nil
		}
		if subtitleExtensions[file.Ext] {
			return nil, []sourceFile{file}, nil
		}
		return nil, nil, nil
	}

	err = filepath.WalkDir(contentPath, func(candidate string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("organize: 遍历 %s: %w", candidate, walkErr)
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(contentPath, candidate)
		if err != nil {
			return fmt.Errorf("organize: 相对路径 %s: %w", candidate, err)
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return fmt.Errorf("organize: 读 %s: %w", candidate, err)
		}
		file, err := newSourceFile(candidate, rel, fileInfo)
		if err != nil {
			return err
		}
		switch {
		case media.IsVideo(file.Path):
			videos = append(videos, file)
		case subtitleExtensions[file.Ext]:
			subtitles = append(subtitles, file)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	sortNatural(videos)
	sortNatural(subtitles)
	return videos, subtitles, nil
}

func newSourceFile(path, rel string, info os.FileInfo) (sourceFile, error) {
	if !info.Mode().IsRegular() {
		return sourceFile{}, fmt.Errorf("organize: %s 不是普通文件", path)
	}
	return sourceFile{
		Path:    path,
		RelPath: filepath.ToSlash(rel),
		Ext:     strings.ToLower(filepath.Ext(path)),
		Stem:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Size:    info.Size(),
	}, nil
}

// planFiles 是纯函数：给定番号、标签与源文件，算出每个文件落盘后的名字。
//
// 规则全在规格 §7.2：
//   - 单片版本裸名 `{番号}[-标签]{扩展名}`；
//   - 多片全显式含 cd1：能解析出序号的用序号，解析不全的按自然顺序赋 1..n；
//   - `is_primary` = cd1 / 最前那一片；
//   - 字幕随对应视频同名，同视频多字幕保留源文件的语言后缀（`{视频}{.chs}{扩展名}`）。
func planFiles(number, label string, videos, subtitles []sourceFile) (Plan, error) {
	number = strings.TrimSpace(number)
	if number == "" {
		return Plan{}, errors.New("organize: 缺番号，无法决定目录与文件名")
	}
	if len(videos) == 0 {
		return Plan{}, ErrNoVideoFiles
	}
	if err := validateNumberForPath(number); err != nil {
		return Plan{}, err
	}

	base := number + labelSuffix(label)

	// 分片序号：单片裸名；多片能全解析就用解析结果，否则自然顺序。
	indices := make([]int, len(videos))
	multi := len(videos) > 1
	if !multi {
		// 单片版本的 part_index 也是 1：它是最前、也是唯一的一片。
		indices[0] = 1
	}
	parsedAll := multi
	if multi {
		for i, video := range videos {
			index, ok := shardIndexOf(video.Stem)
			if !ok {
				parsedAll = false
				break
			}
			indices[i] = index
		}
		if !parsedAll {
			for i := range videos {
				indices[i] = i + 1
			}
		}
	}

	plan := Plan{Files: make([]PlannedFile, 0, len(videos)+len(subtitles))}
	videoBase := make([]string, len(videos))
	used := make(map[string]bool, len(videos)+len(subtitles))

	// 主分片是序号最小的那一片（cd1 / 最前），所以要等序号全算完再打标记。
	primary := 0
	for i := range videos {
		if indices[i] < indices[primary] {
			primary = i
		}
	}

	for i, video := range videos {
		name := base
		if multi {
			name = fmt.Sprintf("%s-cd%d", base, indices[i])
		}
		target := name + video.Ext
		if used[target] {
			return Plan{}, fmt.Errorf("%w: %s（同一条版本里有两个分片撞名）", ErrCollision, target)
		}
		used[target] = true
		videoBase[i] = name
		plan.Files = append(plan.Files, PlannedFile{
			Source:     video,
			TargetName: target,
			PartIndex:  indices[i],
			IsPrimary:  i == primary,
		})
		plan.VideoBytes += video.Size
	}

	for _, subtitle := range subtitles {
		match := matchVideo(subtitle.Stem, videos)
		if match < 0 {
			// 没有对应视频的字幕不落盘（白名单之外没有归属）。
			continue
		}
		suffix := subtitle.Stem[len(videos[match].Stem):]
		target := videoBase[match] + suffix + subtitle.Ext
		if used[target] {
			return Plan{}, fmt.Errorf("%w: %s（同一条版本里有两个字幕撞名）", ErrCollision, target)
		}
		used[target] = true
		plan.Files = append(plan.Files, PlannedFile{
			Source:     subtitle,
			TargetName: target,
			PartIndex:  indices[match],
			IsPrimary:  indices[match] == indices[primary],
			Subtitle:   true,
		})
	}

	return plan, nil
}

// matchVideo 找一个字幕所属的视频：取最长的 stem 前缀（`{视频}.chs` 归那个视频）。
func matchVideo(stem string, videos []sourceFile) int {
	match, matchLen := -1, -1
	for i, video := range videos {
		if !strings.HasPrefix(stem, video.Stem) {
			continue
		}
		if len(video.Stem) == len(stem) {
			return i
		}
		rest := stem[len(video.Stem):]
		if !strings.ContainsRune("._- ", rune(rest[0])) {
			continue
		}
		// 带分片标记的字幕归那一片，不归裸名视频（`{番号}-cd9.ass` 不是 `{番号}.mkv` 的字幕）。
		if shardPattern.MatchString(rest) {
			continue
		}
		if len(video.Stem) > matchLen {
			match, matchLen = i, len(video.Stem)
		}
	}
	return match
}

// shardIndexOf 取文件名里的分片序号；没有就是 false。
func shardIndexOf(stem string) (int, bool) {
	matches := shardPattern.FindAllStringSubmatch(stem, -1)
	if len(matches) == 0 {
		return 0, false
	}
	last := matches[len(matches)-1]
	index, err := strconv.Atoi(last[1])
	if err != nil || index < 1 {
		return 0, false
	}
	return index, true
}

// validateNumberForPath 挡住把番号当路径用的输入（番号进目录名，不能带分隔符）。
func validateNumberForPath(number string) error {
	if number == "" || number == "." || number == ".." {
		return fmt.Errorf("organize: 番号 %q 不能当目录名", number)
	}
	if strings.ContainsAny(number, `/\`) || strings.ContainsRune(number, 0) {
		return fmt.Errorf("organize: 番号 %q 含有路径分隔符", number)
	}
	return nil
}

// sortNatural 是 digit-aware 的自然排序：`cd2` 在 `cd10` 前面（规格 §7.2 无序号分片）。
func sortNatural(files []sourceFile) {
	sort.SliceStable(files, func(i, j int) bool {
		return naturalLess(files[i].RelPath, files[j].RelPath)
	})
}

// naturalLess 比较两个字符串，把数字段按数值比（其余按字节序、大小写不敏感地兜底）。
func naturalLess(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		if isDigit(a[ai]) && isDigit(b[bi]) {
			aStart, bStart := ai, bi
			for ai < len(a) && isDigit(a[ai]) {
				ai++
			}
			for bi < len(b) && isDigit(b[bi]) {
				bi++
			}
			aNum := strings.TrimLeft(a[aStart:ai], "0")
			bNum := strings.TrimLeft(b[bStart:bi], "0")
			if len(aNum) != len(bNum) {
				return len(aNum) < len(bNum)
			}
			if aNum != bNum {
				return aNum < bNum
			}
			continue
		}
		if a[ai] != b[bi] {
			return a[ai] < b[bi]
		}
		ai, bi = ai+1, bi+1
	}
	return len(a)-ai < len(b)-bi
}

func isDigit(char byte) bool { return char >= '0' && char <= '9' }
