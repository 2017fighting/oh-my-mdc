package qbittorrent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 人工重试按版本行存的 payload 重新加种（规格 §6.1 人工重试行 / #31）。
// 测试缝：FromPayload —— 把「磁力串或 .torrent 备份路径」翻成本包的加种请求。

// TestFromPayloadBuildsMagnetDelivery 钉住磁力这一支：payload 原样进 Magnet，savepath 取设置。
func TestFromPayloadBuildsMagnetDelivery(t *testing.T) {
	cfg := testConfig()

	torrent, err := FromPayload(cfg, "0123456789abcdef0123456789abcdef01234567", "ABP-123",
		"magnet", testMagnet)
	if err != nil {
		t.Fatalf("FromPayload(magnet): %v", err)
	}
	if torrent.Magnet != testMagnet || len(torrent.Body) != 0 {
		t.Errorf("torrent = %+v, want 只有 Magnet", torrent)
	}
	if torrent.Number != "ABP-123" || torrent.SavePath != cfg.DownloadRoot {
		t.Errorf("number/savepath = %q/%q", torrent.Number, torrent.SavePath)
	}
}

// TestFromPayloadReadsBackTheTorrentBackup 钉住私种这一支：payload 是备份路径，字节要从盘上读回来。
func TestFromPayloadReadsBackTheTorrentBackup(t *testing.T) {
	cfg := testConfig()
	body := []byte("d4:infod4:name4:testee")
	path := filepath.Join(t.TempDir(), "0123456789abcdef0123456789abcdef01234567.torrent")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("写种子备份: %v", err)
	}

	torrent, err := FromPayload(cfg, "0123456789abcdef0123456789abcdef01234567", "ABP-123", "torrent", path)
	if err != nil {
		t.Fatalf("FromPayload(torrent): %v", err)
	}
	if string(torrent.Body) != string(body) {
		t.Errorf("Body = %q, want 备份文件原文", torrent.Body)
	}
	if torrent.Magnet != "" {
		t.Errorf("Magnet = %q, want 空（一条版本只有一种交付形态）", torrent.Magnet)
	}
	if torrent.Filename != "0123456789abcdef0123456789abcdef01234567.torrent" {
		t.Errorf("Filename = %q, want <infohash>.torrent", torrent.Filename)
	}
}

// TestFromPayloadExplainsAMissingBackup 钉住失败时的措辞与身份：
// 备份在 /tmp 里，重启后可能已经没了——错误要说清这一点（而不是只报 an error），
// 并且要能被 errors.Is 认出来：材料缺失是可补的，调用方要回上游重取一次（#36）。
func TestFromPayloadExplainsAMissingBackup(t *testing.T) {
	_, err := FromPayload(testConfig(), "0123456789abcdef0123456789abcdef01234567", "ABP-123",
		"torrent", filepath.Join(t.TempDir(), "gone.torrent"))
	if err == nil {
		t.Fatal("备份读不到时应当报错")
	}
	if !errors.Is(err, ErrPayloadMissing) {
		t.Errorf("err = %v, want errors.Is(ErrPayloadMissing)", err)
	}
	if !strings.Contains(err.Error(), "私种备份") {
		t.Errorf("err = %v, want 提到私种备份路径", err)
	}
}

func testConfig() Config {
	return Config{DownloadRoot: "/media/downloads", Category: "mdc"}
}
