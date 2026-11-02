package organize

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// 测试缝：落盘三件事（硬链 / 原子写 / 收缩清理），规格 §7.1–§7.2。

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func sameInode(t *testing.T, a, b string) bool {
	t.Helper()
	aInfo, err := os.Stat(a)
	if err != nil {
		t.Fatalf("stat %s: %v", a, err)
	}
	bInfo, err := os.Stat(b)
	if err != nil {
		t.Fatalf("stat %s: %v", b, err)
	}
	return os.SameFile(aInfo, bInfo)
}

// TestLinkFileIsHardLinkAndIdempotent 钉住「硬链 + 零拷贝 + 重跑幂等」。
func TestLinkFileIsHardLinkAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "download.mkv")
	target := filepath.Join(dir, "library", "ABP-123.mkv")
	write(t, source, "video-bytes")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	linked, err := linkFile(source, target)
	if err != nil || !linked {
		t.Fatalf("linkFile = (%v, %v), want (true, nil)", linked, err)
	}
	if !sameInode(t, source, target) {
		t.Fatal("不是硬链（inode 不同）")
	}

	// 重跑：已落盘，零写入也不是错误。
	linked, err = linkFile(source, target)
	if err != nil || linked {
		t.Fatalf("重跑 linkFile = (%v, %v), want (false, nil)", linked, err)
	}
}

// TestLinkFileRefusesToOverwrite 钉住碰撞：目标被别的文件占着就是 ErrCollision，绝不覆盖。
func TestLinkFileRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "download.mkv")
	target := filepath.Join(dir, "ABP-123.mkv")
	write(t, source, "new")
	write(t, target, "existing")

	if _, err := linkFile(source, target); !errors.Is(err, ErrCollision) {
		t.Fatalf("err = %v, want ErrCollision", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != "existing" {
		t.Errorf("目标被覆盖了: %q", content)
	}
}

// TestLinkFileReportsCrossDevice 钉住 EXDEV：换挂载才是解法，绝不降级 copy。
func TestLinkFileReportsCrossDevice(t *testing.T) {
	source := filepath.Join(t.TempDir(), "download.mkv")
	write(t, source, "video")

	// /dev/shm 通常是另一个挂载（tmpfs）；不是就跳过这段真实跨设备验证。
	other, err := os.MkdirTemp("/dev/shm", "mdc-exdev-")
	if err != nil {
		t.Skipf("/dev/shm 不可用（%v）：跳过真实跨设备验证", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })

	target := filepath.Join(other, "ABP-123.mkv")
	linked, err := linkFile(source, target)
	if !errors.Is(err, ErrCrossDevice) {
		t.Fatalf("linkFile = (%v, %v), want ErrCrossDevice", linked, err)
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("跨设备时不该留下任何副本（绝不降级 copy）")
	}
}

// TestIsCrossDeviceRecognizesEXDEV 是分类器的单点验证（不依赖挂载布局）。
func TestIsCrossDeviceRecognizesEXDEV(t *testing.T) {
	if !isCrossDevice(&os.LinkError{Op: "link", Err: syscall.EXDEV}) {
		t.Error("EXDEV 没被认出来")
	}
	if isCrossDevice(&os.LinkError{Op: "link", Err: syscall.EEXIST}) {
		t.Error("EEXIST 被误判成跨设备")
	}
}

// TestWriteIfChangedWritesOnce 钉住原子写与「内容同则不重写」。
func TestWriteIfChangedWritesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movie.nfo")

	written, err := writeIfChanged(path, []byte("<movie/>\n"))
	if err != nil || !written {
		t.Fatalf("writeIfChanged = (%v, %v), want (true, nil)", written, err)
	}

	written, err = writeIfChanged(path, []byte("<movie/>\n"))
	if err != nil || written {
		t.Fatalf("内容相同 writeIfChanged = (%v, %v), want (false, nil)", written, err)
	}

	written, err = writeIfChanged(path, []byte("<movie><title>x</title></movie>\n"))
	if err != nil || !written {
		t.Fatalf("内容变了 writeIfChanged = (%v, %v), want (true, nil)", written, err)
	}

	// 落盘后不留临时文件：原子写用 rename，不是写完再拷。
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "movie.nfo" {
		t.Errorf("目录里有多余文件: %+v", entries)
	}
}

// TestCleanExtraFanartRemovesOnlyStaleOnes 钉住收缩清理：只删自己命名、编号超出的旧图。
func TestCleanExtraFanartRemovesOnlyStaleOnes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"fanart1.jpg", "fanart2.jpg", "fanart3.jpg", "cover.jpg"} {
		write(t, filepath.Join(dir, name), "x")
	}

	removed, err := cleanExtraFanart(dir, 1)
	if err != nil {
		t.Fatalf("cleanExtraFanart: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
	for _, name := range []string{"fanart1.jpg", "cover.jpg"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s 不该被删: %v", name, err)
		}
	}
	for _, name := range []string{"fanart2.jpg", "fanart3.jpg"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s 应该被删", name)
		}
	}

	// 目录不存在不是错误（还没落过图）。
	if removed, err := cleanExtraFanart(filepath.Join(dir, "missing"), 3); err != nil || removed != 0 {
		t.Errorf("不存在的目录 = (%d, %v), want (0, nil)", removed, err)
	}
}
