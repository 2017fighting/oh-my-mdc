package settings

import "os"

// DirectoryVisible 报告一个目录在这台机器上是否可见（存在且是目录）。
//
// 它守的是「Jellyfin 可写」那一件（规格 §5.5）：qBittorrent / oh-my-mdc / Jellyfin 三个
// 容器把同一 host 目录挂到同一路径，看不见媒体库目录就是挂载没对齐——刷新与硬链接
// 都会失败，所以门禁把它算进 initialized（P4-2）。刷新路径也用它判「目录在盘才调」。
// 它查的是本机文件系统，不打上游。
//
// 放在 settings 里的原因很实际：initialized 的判据在 settings，而 settings 不能反向
// import 集成包（jellyfin 依赖 settings）；刷新路径复用同一个判据，不各写一份 os.Stat。
func DirectoryVisible(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
