package qbittorrent

import (
	"encoding/base32"
	"encoding/hex"
	"testing"
)

// TestHashFromMagnetAcceptsBothSpellings 确认 btih 的十六进制与 base32 两种拼写都认。
func TestHashFromMagnetAcceptsBothSpellings(t *testing.T) {
	want := "0123456789abcdef0123456789abcdef01234567"
	raw, err := hex.DecodeString(want)
	if err != nil {
		t.Fatalf("hex.DecodeString: %v", err)
	}
	base32Spelling := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)

	for name, magnet := range map[string]string{
		"40 位十六进制（大写）": "magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&dn=ABP-123",
		"32 位 base32":  "magnet:?xt=urn:btih:" + base32Spelling + "&dn=ABP-123",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := HashFromMagnet(magnet)
			if err != nil {
				t.Fatalf("HashFromMagnet: %v", err)
			}
			if got != want {
				t.Errorf("hash = %q, want %q（统一成小写十六进制）", got, want)
			}
		})
	}
}

// TestHashFromMagnetRejectsGarbage 确认取不到 infohash 时本地报错，不猜。
func TestHashFromMagnetRejectsGarbage(t *testing.T) {
	for _, magnet := range []string{"", "magnet:?dn=ABP-123", "magnet:?xt=urn:btih:notahash"} {
		if _, err := HashFromMagnet(magnet); err == nil {
			t.Errorf("HashFromMagnet(%q) 应当报错", magnet)
		}
	}
}
