// 本文件是 .torrent 字节的解析与 infohash 计算。
//
// # 来源与致谢
//
// 算法与实现来自开源项目 javdb_rss（MIT License）的 internal/mteam/torrent.go，
// 经由它移植而来（规格 §2 移植清单、§5.2「infohash 本地算」）。
//
// # 为什么必须切原始切片
//
// infohash 是 `info` 字典**原始字节**的 SHA-1。把它解析出来再重新编码一遍看起来等价，
// 但那要求编码器逐字节复现原始的所有选择（键序、整数写法、字节串边界）——
// 而现实里的种子不保证键有序（bencode 规范要求，但不是所有工具都遵守）。
// 重编码会得到一个语法合法但错误的 infohash，而它会直接成为版本行的主键：
// 同一个种子从此被当成两个，去重失效、qbt 侧重复加种，且没有任何告警。
package mteam

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
)

// errTruncated 表示字节在读满一个值之前就用完了。
var errTruncated = errors.New("bencode: 数据被截断")

// decoder 是一个只用来定位字节区间的最小 bencode 遍历器。
//
// 它刻意不构造 Go 值：本包要的是 `info` 字典的原始字节切片，
// 而不是对它解析后再编码的结果。
type decoder struct{ b []byte }

// byteString 读位置 i 处的字节串，返回它的内容与结束位置。
func (d decoder) byteString(i int) ([]byte, int, error) {
	if i >= len(d.b) {
		return nil, 0, errTruncated
	}
	j := bytes.IndexByte(d.b[i:], ':')
	if j < 0 {
		return nil, 0, errTruncated
	}
	n := 0
	for _, c := range d.b[i : i+j] {
		if c < '0' || c > '9' {
			return nil, 0, errors.New("bencode: 字节串的长度不是数字")
		}
		n = n*10 + int(c-'0')
		if n > len(d.b) { // 早退，免得下面的 start+n 溢出
			return nil, 0, errTruncated
		}
	}
	start := i + j + 1
	if start+n > len(d.b) {
		return nil, 0, errTruncated
	}
	return d.b[start : start+n], start + n, nil
}

// skip 跳过位置 i 处的一个完整值，返回它的结束位置。
//
// 它只关心结构（哪一段字节属于这个值），不校验内容 —— 整数里写了什么、
// 字典的键是什么，对定位都没有影响。
func (d decoder) skip(i int) (int, error) {
	if i >= len(d.b) {
		return 0, errTruncated
	}
	switch d.b[i] {
	case 'i':
		j := bytes.IndexByte(d.b[i:], 'e')
		if j < 0 {
			return 0, errTruncated
		}
		return i + j + 1, nil
	case 'l', 'd':
		isDict := d.b[i] == 'd'
		i++
		for {
			if i >= len(d.b) {
				return 0, errTruncated
			}
			if d.b[i] == 'e' {
				return i + 1, nil
			}
			var err error
			if i, err = d.skip(i); err != nil { // 键（或列表元素）
				return 0, err
			}
			if isDict {
				if i, err = d.skip(i); err != nil { // 字典还要一个值
					return 0, err
				}
			}
		}
	default:
		_, end, err := d.byteString(i)
		return end, err
	}
}

// dictValue 在位置 i（必须是一个字典）里找 key，返回它值的原始字节区间 [start, end)。
//
// 第三个返回值报告「找没找到」—— 与「出错了」分开，因为调用方对这两种情况的处置
// 完全不同（缺 info 是「这不是种子」，截断是「这份种子坏了」）。
func (d decoder) dictValue(i int, key string) (start, end int, ok bool, err error) {
	if i >= len(d.b) || d.b[i] != 'd' {
		return 0, 0, false, errors.New("bencode: 期望一个字典")
	}
	i++
	for {
		if i >= len(d.b) {
			return 0, 0, false, errTruncated
		}
		if d.b[i] == 'e' {
			return 0, 0, false, nil
		}
		k, next, err := d.byteString(i)
		if err != nil {
			return 0, 0, false, err
		}
		vs := next
		ve, err := d.skip(vs)
		if err != nil {
			return 0, 0, false, err
		}
		if string(k) == key {
			return vs, ve, true, nil
		}
		i = ve
	}
}

// Infohash 从一份 .torrent 的字节算出它的 v1 infohash，并确认这份种子能用。
//
// 两件事合在一个函数里，是因为它们服务于同一个判断：「这份种子能不能交给 qBittorrent」。
// 拆开的话，调用方可以只取 infohash 而忘掉校验，而那种漏掉的表现是
// **qbt 里红种、我们这边无声**。
//
// 拒绝的两类（理由见 checkV1 与 announce 检查）：v2-only 的种子（另一套算法）、
// announce 里没有按次签发凭据的种子（私种连不上 tracker）。
func Infohash(data []byte) (string, error) {
	if len(data) == 0 || data[0] != 'd' {
		return "", errors.New("这不是一份种子：bencode 的顶层必须是字典")
	}
	d := decoder{b: data}

	infoStart, infoEnd, ok, err := d.dictValue(0, "info")
	if err != nil {
		return "", fmt.Errorf("解析种子: %w", err)
	}
	if !ok {
		return "", errors.New("这份种子里没有 info 字典")
	}
	if infoStart >= len(data) || data[infoStart] != 'd' {
		return "", errors.New("这份种子的 info 不是一个字典")
	}

	hasCredential, err := announceHasCredential(d, data)
	if err != nil {
		return "", fmt.Errorf("解析种子: %w", err)
	}
	if !hasCredential {
		return "", errors.New("tracker 地址里没有凭据（credential=）—— 这份种子连不上 tracker，加进去就是红种")
	}

	if err := checkV1(d, infoStart); err != nil {
		return "", err
	}

	sum := sha1.Sum(data[infoStart:infoEnd])
	return hex.EncodeToString(sum[:]), nil
}

// announceHasCredential 报告 tracker 地址里有没有按次签发的凭据。
//
// 判据是 announce 或 announce-list 里含 `credential=`（规格 §5.2 记的线格式）。
// 没有凭据的私种加进去就是红种，而 qBittorrent 那边不会有任何错误回执 ——
// 这正是本包要在加种之前拦下它的理由。
func announceHasCredential(d decoder, data []byte) (bool, error) {
	for _, key := range []string{"announce", "announce-list"} {
		start, end, ok, err := d.dictValue(0, key)
		if err != nil {
			return false, err
		}
		if ok && bytes.Contains(data[start:end], []byte("credential=")) {
			return true, nil
		}
	}
	return false, nil
}

// checkV1 确认 info 字典是 v1 形态，并拒绝 v2-only。
//
// 判据：v1 的单文件种子有 `length`，多文件有 `files`。
// **hybrid（v1+v2）通过** —— 它有 length/files，因此有 v1 infohash，能用。
func checkV1(d decoder, infoStart int) error {
	for _, key := range []string{"length", "files"} {
		if _, _, ok, err := d.dictValue(infoStart, key); err != nil {
			return fmt.Errorf("解析种子: %w", err)
		} else if ok {
			return nil
		}
	}
	if _, _, ok, err := d.dictValue(infoStart, "meta version"); err == nil && ok {
		return errors.New("这是 v2-only 的种子（有 meta version，但没有 v1 的 length/files）—— 本项目只认 v1")
	}
	return errors.New("这份种子的 info 里既没有 length 也没有 files，认不出是 v1 形态")
}
