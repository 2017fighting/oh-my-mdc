import type { Subscription } from "./api";

/**
 * 订阅区（规格 §6.4/§8.2）的纯逻辑：面板一行的那句话，与「添加」那颗按钮发出去的形状。
 *
 * 上游没有按名字搜女优的端点（§13 实测：`/api/v1/actors` 是分页名单，q/name 全被静默
 * 忽略），所以入口只有「贴 id / 女优页链接」两种。判定哪一种在这里做——面板那颗按钮
 * 与它的校验提示读同一处，免得两处各猜一套。
 */

/** SubscriptionAddBody 是 POST /api/subscriptions 的两种输入之一（规格 §8.4）。 */
export type SubscriptionAddBody = { actress_id: string } | { javdb_url: string };

/** subscriptionAddBody 把一串粘贴翻成 POST /api/subscriptions 的请求体；空输入回 null。 */
export function subscriptionAddBody(raw: string): SubscriptionAddBody | null {
  const input = raw.trim();
  if (input === "") return null;
  // 有斜杠就是一条链接（裸 id 是字母数字短串，不会有斜杠）：主机白名单会随镜像换域名
  // 而失效，而这里要判的只是「该交给服务端按 URL 解析，还是按 id」。
  if (input.includes("/")) return { javdb_url: input };
  return { actress_id: input };
}

/**
 * subscriptionBackfillDays 把「自由天数」那一格翻成能发的天数，不合法回 null。
 *
 * 两条判据：正整数，且**越过回看窗口**。后者不是洁癖——水位线 − days 落在回看窗口里
 * 就是空操作（下一轮本来就会看那么远），服务端会 400；面板先自己守住，那颗按钮因此
 * 只在真有作用时亮着（规格 §8.4）。
 */
export function subscriptionBackfillDays(raw: string, lookbackDays: number): number | null {
  const trimmed = raw.trim();
  // 只认十进制数字：`1e3`、`1.5`、`-30` 都不是「天数」的写法。
  if (!/^[0-9]+$/.test(trimmed)) return null;
  const days = Number(trimmed);
  if (!Number.isSafeInteger(days) || days <= lookbackDays) return null;
  return days;
}

/** subscriptionWatermark 说「上次见过哪一天」；不知道就说不知道，不编今天。 */
export function subscriptionWatermark(date: string): string {
  return date === "" ? "还没跑过" : date;
}

/**
 * subscriptionSubtitle 是订阅区一行的那句话：水位线、带来几部、两个「待办」的数、
 * 回灌开着没有、停用没有。
 *
 * 两个数各管一段（§6.4「节流与去重」）：`pending_count` 是已经入队、还在等加种的版本；
 * `deferred_count` 是「还有 N 部待入队」——回看窗口里已发现、被每轮限量留在门外的作品。
 * 后者是限量的现场读数（每轮末尾置数），余量走完就自己消失。
 */
export function subscriptionSubtitle(row: Subscription): string {
  const parts = [
    `水位线 ${subscriptionWatermark(row.watermark_date)}`,
    `带来 ${row.tasks_count} 部`,
  ];
  if (row.pending_count > 0) {
    parts.push(`${row.pending_count} 部待加种`);
  }
  if (row.deferred_count > 0) {
    parts.push(`还有 ${row.deferred_count} 部待入队`);
  }
  if (row.backfill_from) {
    parts.push(`回灌自 ${row.backfill_from}`);
  }
  if (!row.enabled) {
    parts.push("已停用");
  }
  return parts.join(" · ");
}
