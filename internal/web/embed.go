// Package web 把前端构建产物编进二进制。
//
// dist/ 是 `make web-build`（vite build）的输出；仓库里带一份占位 index.html，
// 让没装 Node 的机器也能 go build / go vet / go test。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// distRoot 是嵌入的根目录，Assets 返回它下面那一层。
const distRoot = "dist"

// Assets 返回 SPA 构建产物，根目录含 index.html。
func Assets() fs.FS {
	sub, err := fs.Sub(embedded, distRoot)
	if err != nil {
		// 只有在 distRoot 常量与 embed 指令不一致时才会发生，属于编译期错误。
		panic("web: 嵌入的前端目录缺失: " + err.Error())
	}
	return sub
}
