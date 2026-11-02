// 把 react-router 的静态构建（build/client）抄进 Go 的 embed 目录。
//
// SPA 模式下构建产物只有静态文件，Go 用 //go:embed all:dist 打包它们（规格 §2）。
import { cp, mkdir, rm } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const source = join(here, "..", "build", "client");
const target = join(here, "..", "..", "internal", "web", "dist");

await rm(target, { recursive: true, force: true });
await mkdir(target, { recursive: true });
await cp(source, target, { recursive: true });

console.log(`前端产物已就位：${target}`);
