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

/** subscriptionWatermark 说「上次见过哪一天」；不知道就说不知道，不编今天。 */
export function subscriptionWatermark(date: string): string {
  return date === "" ? "还没跑过" : date;
}

/** subscriptionSubtitle 是订阅区一行的那句话：水位线、带来几部、回灌开着没有、停用没有。 */
export function subscriptionSubtitle(row: Subscription): string {
  const parts = [
    `水位线 ${subscriptionWatermark(row.watermark_date)}`,
    `带来 ${row.tasks_count} 部`,
  ];
  if (row.pending_count > 0) {
    parts.push(`${row.pending_count} 部待加种`);
  }
  if (row.backfill_from) {
    parts.push(`回灌自 ${row.backfill_from}`);
  }
  if (!row.enabled) {
    parts.push("已停用");
  }
  return parts.join(" · ");
}
