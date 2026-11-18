import type { Subscription } from "./api";
import { formatWhen } from "./time.ts";

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
 * 只判一件面板能判的事：正整数。**不**再拿它和回看窗口比——回看窗口只在起追日之后
 * 游走，盖不住回灌那一段（新订阅的起追日就是今天，回灌 30 天正是把窗口拨回去的
 * 那一下，§8.4）。「这一次回灌是不是空操作」要比起追日，面板手里没有那一列
 * （`Subscription` 只有水位线）：服务端会算出落点，被起追日盖住时回 400，那句话
 * 原样落在这一行的错误条上。
 */
export function subscriptionBackfillDays(raw: string): number | null {
  const trimmed = raw.trim();
  // 只认十进制数字：`1e3`、`1.5`、`-30` 都不是「天数」的写法。
  if (!/^[0-9]+$/.test(trimmed)) return null;
  const days = Number(trimmed);
  if (!Number.isSafeInteger(days) || days < 1) return null;
  return days;
}

/**
 * subscriptionWatermark 说「上次见过哪一天」；不知道就说不知道，不编今天。
 *
 * 说相对时间（`3 天前` / `昨天`），不说 `2026-11-18`：订阅行问的是「她最近一次
 * 带来东西是多久之前」，不是「那是哪一天」（DESIGN 的 Relative-Forever Rule）。
 * 绝对日期仍进 `title`，由调用方给。
 */
export function subscriptionWatermark(date: string, now: number = Date.now()): string {
  return date === "" ? "还没跑过" : formatWhen(date, now);
}

/**
 * subscriptionSubtitle 是订阅区一行的那句话：水位线、带来几部、两个「待办」的数、
 * 回灌开着没有、停用没有。
 *
 * 两个数各管一段（§6.4「节流与去重」）：`pending_count` 是已经入队、还在等加种的版本；
 * `deferred_count` 是「还有 N 部待入队」——回看窗口里已发现、被每轮限量留在门外的作品。
 * 后者是限量的现场读数（每轮末尾置数），余量走完就自己消失。
 */
export function subscriptionSubtitle(row: Subscription, now: number = Date.now()): string {
  const parts = [
    `水位线 ${subscriptionWatermark(row.watermark_date, now)}`,
    `带来 ${row.tasks_count} 部`,
  ];
  if (row.pending_count > 0) {
    parts.push(`${row.pending_count} 部待加种`);
  }
  if (row.deferred_count > 0) {
    parts.push(`还有 ${row.deferred_count} 部待入队`);
  }
  if (row.backfill_from) {
    // 同一份台阶：行上说「多久以前」，绝对日期进 title（Relative-Forever Rule）。
    parts.push(`回灌自 ${formatWhen(row.backfill_from, now)}`);
  }
  if (!row.enabled) {
    parts.push("已停用");
  }
  return parts.join(" · ");
}

/**
 * subscriptionSummaryLine 是面板上那一行摘要（订阅区本体在 `/settings/actresses`）。
 *
 * 面板只回答两个问题：「订了几个人、还在不在追」「最近一次带来东西是什么时候」。
 * 一条订阅都没有时返回 null：面板据此整行不渲染，不留一句「还没订阅」的家具
 * （DESIGN 的 Nothing-To-Do Rule）——那句话属于设置面。
 */
export function subscriptionSummaryLine(
  rows: Subscription[],
  now: number = Date.now(),
): string | null {
  if (rows.length === 0) return null;
  const enabled = rows.filter((row) => row.enabled).length;
  const latestWatermark = rows
    .map((row) => row.watermark_date)
    .filter((date) => date !== "")
    .sort()
    .at(-1);
  const tasks = rows.reduce((sum, row) => sum + row.tasks_count, 0);
  const parts = [
    enabled === rows.length ? `${rows.length} 位女优` : `${rows.length} 位女优 · 启用 ${enabled}`,
    latestWatermark === undefined ? "还没跑过" : `最近水位线 ${formatWhen(latestWatermark, now)}`,
    `带来 ${tasks} 部`,
  ];
  return parts.join(" · ");
}
