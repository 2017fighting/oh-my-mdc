// Package media 拥有媒体文件类型的事实：哪些扩展名算视频（规格 §7.2）。
//
// 这份视频白名单只有一处定义。下载完成判定（§6.1「content_path 可读含视频文件」）
// 与将来的入库整理（§7.2 只挑视频硬链接）必须读同一份，否则同一个文件会被判出两个结论。
//
// 扩展名一律大小写不敏感：源文件名不由本项目决定。
package media

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// videoExtensions 是规格 §7.2 点名的视频扩展名集合（小写）。
var videoExtensions = map[string]bool{
	"mp4": true, "mkv": true, "avi": true, "wmv": true, "mov": true,
	"m2ts": true, "ts": true, "rmvb": true, "flv": true, "webm": true,
	"mpg": true, "mpeg": true,
}

// IsVideo 报告一个文件名是不是视频（按扩展名判定，不看内容）。
func IsVideo(name string) bool {
	return videoExtensions[extensionOf(name)]
}

// HasVideo 报告这个路径是不是「可读且含视频文件」。
//
// content_path 可能是单个视频文件，也可能是种子的根目录，所以两种都要认：
// 文件按扩展名判定，目录递归找一个视频文件即可（命中就停）。
// 路径不存在或读不了返回错误：读不到就不是可读，调用方据此判「文件丢失」。
func HasVideo(path string) (bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("media: 读 %s: %w", path, err)
	}
	if !info.IsDir() {
		return IsVideo(path), nil
	}

	found := false
	err = filepath.WalkDir(path, func(candidate string, entry fs.DirEntry, err error) error {
		if err != nil {
			// 子目录读不了不该让整次判定失败：视频可能在别处。
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if IsVideo(candidate) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("media: 遍历 %s: %w", path, err)
	}
	return found, nil
}

// extensionOf 取小写扩展名（不带点）；没有扩展名时返回空串。
func extensionOf(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(ext, "."))
}
