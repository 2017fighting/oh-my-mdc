# ADR-0012: NFO 只写 tag，不写 genre

- 状态：已接受（2026-11-15 刮削质量复盘）
- 背景：mdcng 时代 tags 同写 `<genre>` 与 `<tag>`，Jellyfin 的类型浏览吃 genre，旧库因此有类型导航。omd 只写 `<tag>` 后，它触达的目录 genre 全空，类型浏览/筛选消失——复盘时被当回归报上来。spec §7.1 当初的论据「Jellyfin 按 `/` 拆」站不住：那是 Kodi 多 genre 语法，不是省略 genre 的理由。
- 决策：维持不写 `<genre>`。`<tag>` 承载 JavDB tags（干净、无「系列: 」「片商: 」前缀噪声），genre 留空；类型浏览的缺位是有意代价，不是缺陷。
- 后果：Jellyfin 的类型页与筛选对本库不可用，tag 在条目详情可见。要翻案，改的是 `organize/nfo.go` 的 Render，并与本 ADR 一起 supersede，不是 bug 修复。
