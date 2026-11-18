import * as React from "react";

/**
 * useNow 是一个只用来「把相对时间走起来」的时钟。
 *
 * 一屏「3 分钟前」不能挂到用户关掉标签页为止：统一在这里每 60 秒走一格，
 * 具体文案全部交给 `lib/time` 的那份台阶。
 *
 * 住在 lib 里而不是某个组件文件里：面板、集成设置的两个维护节与订阅行都要它，
 * 谁都从谁那里要一次，就会把三个页面连成一圈 import。
 */
export function useNow(interval = 60_000): number {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), interval);
    return () => window.clearInterval(timer);
  }, [interval]);
  return now;
}
