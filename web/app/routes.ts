import { type RouteConfig, index, route } from "@react-router/dev/routes";

/**
 * 全站只有一个外壳，视图由 pathname 决定：
 *
 * - `/` 是面板；
 * - `/settings/<section>` 是集成设置里的一节（初始化前是向导的一步）。
 *
 * 两条路由渲染同一个外壳（settings.tsx 只是转出 home.tsx 的组件，好让两条路由
 * 各有 route id）：`index` 让构建期能渲染出带 route 模块的壳页（纯 `*` 通配在
 * SPA 构建时不匹配 `/`，壳页里就只剩 root，整页空白），`route` 让每一节
 * 有自己的地址——于是「去 Telegram 设置」是真链接：可书签、可刷新、可中键新开，
 * 落到它点名的那一节，而不是向导第 1 步。
 */
export default [
  index("routes/home.tsx"),
  route("settings/:section", "routes/settings.tsx"),
] satisfies RouteConfig;
