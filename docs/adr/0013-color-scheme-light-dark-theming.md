# ADR-0013: 双主题走 color-scheme + light-dark() token，不走 .dark 类

- 状态：已接受（2026-11-16 面板深色模式）
- 背景：DESIGN 当初明文「不复活深色主题」（`.dark` 与 `dark:` 变体已删），前提是「要加，先补切换入口并逐状态校对对比度」。值班台常在夜里看，深色的请求成立，切换入口（侧栏底部 + 窄屏顶行的三态控件）与对比度门禁这次一起补上。
- 决策：每个颜色 token 在 `app.css` 里一行带齐两值（`light-dark(浅, 深)`），主题由根上的 `color-scheme` 选边；覆盖用 `<meta name="color-scheme">` 驱动（跟随系统 = `light dark`，钉住 = `light`/`dark`），CSS 用 `:root:has(> head > meta[...])` 把 meta 映射回属性，内联脚本在首绘前写 meta 防闪，偏好存 `localStorage`、与系统一致时删条目。不引入 `.dark` 类、不引入 `dark:` 变体。考虑过 shadcn 传统的 `.dark` + `dark:` 做法：它把分支撒进每个组件、Three-Part State 三件套要靠人肉在两处保持成对，且原生 UI（滚动条、表单控件、canvas 底色）还得另开 color-scheme——一条 token 行带齐两值让「浅深成对」成为语法事实，探针缺一边就报错。也考虑过纯 `prefers-color-scheme` 媒体查询：省掉切换入口，但无法手动钉住，且违反上面那条旧前提。
- 后果：新增颜色必须一行写浅深两个值，只写一边探针即失败（`make contrast` 浅深各判一遍）；`prefers-color-scheme` 媒体查询从此不能用于站内配色分支（它永远代表系统而非当前主题），theme-color 的浏览器 chrome 随色由 ThemeToggle 同步；组件里表达「浮起」要按主题方向选 token（深色里更亮 = 更浮），页面画布上的浮起用 `card`/`popover`/`secondary`，卡内的井（输入框、代码块）保留 `background`。要翻案（比如回到纯跟随系统），改的是 `app.css` 的 `:root`、`root.tsx` 的 meta 与 `theme.tsx`，并与本 ADR 一起 supersede。
