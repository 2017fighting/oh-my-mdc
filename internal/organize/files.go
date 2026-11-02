package organize

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
)

// 落盘三件事：硬链（唯一模式，EXDEV 是失败）、原子写（temp+rename，内容同就不写）、
// 收缩清理（extrafanart 多出来的旧图）。

// linkFile 把源文件硬链到目标路径。
//
// 三个结局：
//   - 目标不存在 → 建链，返回 linked=true；
//   - 目标已存在且是同一个 inode → 已落盘（幂等重跑），返回 linked=false；
//   - 目标已存在但不是同一个文件 → ErrCollision（不覆盖、不 -v2，规格 §7.2）。
//
// 跨设备（EXDEV）包成 ErrCrossDevice：绝不静默降级 copy。
func linkFile(source, target string) (bool, error) {
	if err := os.Link(source, target); err == nil {
		return true, nil
	} else if isCrossDevice(err) {
		return false, fmt.Errorf("%w：%s → %s", ErrCrossDevice, source, target)
	} else if !errors.Is(err, os.ErrExist) {
		return false, fmt.Errorf("organize: 硬链 %s → %s: %w", source, target, err)
	}

	// 目标已存在：同一个 inode 就是「已经落过盘」，否则是碰撞。
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return false, fmt.Errorf("organize: 读源文件 %s: %w", source, err)
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		return false, fmt.Errorf("organize: 读目标文件 %s: %w", target, err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return false, nil
	}
	return false, fmt.Errorf("%w: %s（已有别的文件占着这个名字）", ErrCollision, target)
}

// isCrossDevice 认出硬链的 EXDEV。
func isCrossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}

// writeIfChanged 原子写一个文件：先写同目录的临时文件，再 rename 覆盖。
//
// 内容与现值完全一致时零写入（返回 false）：NFO 重写会让 Jellyfin 白扫一遍
// （规格 §7.1「内容 hash 同则不重写」）。rename 保证读写双方永远看不到半个文件。
func writeIfChanged(path string, content []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == string(content) {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("organize: 读现值 %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".mdc-tmp-*")
	if err != nil {
		return false, fmt.Errorf("organize: 建临时文件（%s）: %w", dir, err)
	}
	tempPath := temp.Name()
	cleanup := func() { _ = os.Remove(tempPath) }

	if _, err := temp.Write(content); err != nil {
		temp.Close()
		cleanup()
		return false, fmt.Errorf("organize: 写临时文件 %s: %w", tempPath, err)
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return false, fmt.Errorf("organize: 关闭临时文件 %s: %w", tempPath, err)
	}
	if err := os.Chmod(tempPath, 0o644); err != nil {
		cleanup()
		return false, fmt.Errorf("organize: 设权限 %s: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		cleanup()
		return false, fmt.Errorf("organize: 落盘 %s: %w", path, err)
	}
	return true, nil
}

// extraFanartPattern 是 extrafanart 的文件名（fanart1.jpg / fanart12.png）。
var extraFanartPattern = regexp.MustCompile(`^fanart(\d+)$`)

// cleanExtraFanart 删掉 extrafanart 目录里编号超出 keep 的旧图。
//
// 只碰自己命名的那一套（`fanart{N}.{ext}`）：用户/别的工具放进来的文件不归本项目管理。
func cleanExtraFanart(dir string, keep int) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("organize: 读 %s: %w", dir, err)
	}

	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		stem := entry.Name()[:len(entry.Name())-len(filepath.Ext(entry.Name()))]
		match := extraFanartPattern.FindStringSubmatch(stem)
		if match == nil {
			continue
		}
		index, err := strconv.Atoi(match[1])
		if err != nil || index <= keep {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return removed, fmt.Errorf("organize: 删旧图 %s: %w", entry.Name(), err)
		}
		removed++
	}
	return removed, nil
}
