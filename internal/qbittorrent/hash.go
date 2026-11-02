package qbittorrent

import (
	"encoding/base32"
	"encoding/hex"
	"regexp"
	"strings"
)

// btihPattern 抓磁力串里的 `urn:btih:<hash>`：btih 有两种拼写，40 位十六进制或 32 位 base32。
var btihPattern = regexp.MustCompile(`(?i)urn:btih:([0-9a-z]{32,40})`)

// HashFromMagnet 从磁力串里取 infohash（统一成小写 40 位十六进制）。
//
// 身份是 infohash，与交付形态无关（GLOSSARY 的「磁力候选」）：JavDB 的磁力候选自带
// `hash` 字段，但 M-Team 的种子要本地算，两条路最终都要落到这个形状。
func HashFromMagnet(magnet string) (string, error) {
	match := btihPattern.FindStringSubmatch(strings.TrimSpace(magnet))
	if match == nil {
		return "", ValidationError{Message: "磁力串里没有 urn:btih:（取不到 infohash）: " + truncate(magnet, 80)}
	}

	encoded := strings.ToLower(match[1])
	if len(encoded) == 40 {
		if _, err := hex.DecodeString(encoded); err != nil {
			return "", ValidationError{Message: "磁力串里的 infohash 不是合法十六进制: " + encoded}
		}
		return encoded, nil
	}

	// base32 拼写：32 位、无填充。取回 20 字节后统一成十六进制。
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(encoded))
	if err != nil || len(decoded) != 20 {
		return "", ValidationError{Message: "磁力串里的 infohash 不是合法 base32: " + encoded}
	}
	return hex.EncodeToString(decoded), nil
}
