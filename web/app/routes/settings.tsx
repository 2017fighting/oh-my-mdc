/**
 * `/settings/:section` 与 `/` 渲染同一个外壳：视图由地址决定，不是两套界面。
 *
 * 单独一个模块只为让两条路由各有自己的 route id——react-router 不允许两条路由
 * 指向同一个文件（duplicate route id: "routes/home"）。
 */
export { default, meta } from "./home";
