import type { Config } from "@react-router/dev/config";

/**
 * SPA 模式（规格 §2）：不生成服务端构建，`react-router build` 只出静态文件，
 * 由 scripts/copy-dist.mjs 抄进 internal/web/dist，再由 //go:embed all:dist 编进二进制。
 */
export default {
  ssr: false,
} satisfies Config;
