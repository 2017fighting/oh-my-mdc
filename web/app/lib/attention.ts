import type { Attention, AttentionItem, SideEffect } from "./api";

/**
 * 「需要人工」区按番号收组。
 *
 * 行的单位是**番号**而不是版本：一部作品两个版本同时失败时，人看到的应该是一个番号
 * 加一颗按钮，而不是两行同款「重试」。所以这里必须按键归并，不能只和上一条比——
 * 接口按 `updated_at DESC` 出数据，两部片交替失败时相邻归并会悄悄失效，同一番号变成
 * 两组、两颗一模一样的按钮，而卡角那个「还剩几件事」的数也跟着翻倍。
 */

/** FailureGroup 是同一个番号下同病的一串版本。 */
export type FailureGroup = { movieID: string; number: string; items: AttentionItem[] };

/** groupByNumber 按 movie_id 归并，保持首次出现的顺序（接口已按新鲜度排好）。 */
export function groupByNumber(items: AttentionItem[]): FailureGroup[] {
  const byMovie = new Map<string, FailureGroup>();
  for (const item of items) {
    const group = byMovie.get(item.movie_id);
    if (group) {
      group.items.push(item);
      continue;
    }
    byMovie.set(item.movie_id, { movieID: item.movie_id, number: item.number, items: [item] });
  }
  return [...byMovie.values()];
}

/**
 * StuckGroup 是同一条病因下的一串驻留副作用。
 *
 * 「需要人工」的一行是一个**要动手的对象**，不是一个数据库行：一个坏掉的 Telegram
 * token 让三条通知驻留时，人要修的是那一格 token，不是三条各自的通知。以前它们各占
 * 一行、各给一颗重发按钮，标题上的数于是只会涨不会落，最后那个数就变成了墙纸。
 * 归组键是 kind + last_error：同一类待办、同一句错，才算同一条病因。
 */
export type StuckGroup = { cause: string; effects: SideEffect[] };

/** groupStuck 把驻留待办按病因归组，保持首次出现的顺序（接口按尝试先后出数据）。 */
export function groupStuck(effects: SideEffect[]): StuckGroup[] {
  const byCause = new Map<string, StuckGroup>();
  for (const effect of effects) {
    const cause = `${effect.kind}\u0000${effect.last_error ?? ""}`;
    const group = byCause.get(cause);
    if (group) {
      group.effects.push(effect);
      continue;
    }
    byCause.set(cause, { cause, effects: [effect] });
  }
  return [...byCause.values()];
}

/**
 * attentionRowCount 是「需要人工」卡片里会显示几行。
 *
 * 行 = 一个要动手的对象：一个番号的失败版本、一个番号的停滞下载，或一条病因下的
 * 驻留副作用。页面标题与卡片说明读同一个数，不给第二个口径。
 */
export function attentionRowCount(
  attention: Attention | null,
  sideEffects: SideEffect[],
): number {
  const failed = groupByNumber(attention?.failed ?? []).length;
  const stalled = groupByNumber(attention?.stalled ?? []).length;
  const stuck = groupStuck(sideEffects.filter((effect) => effect.stuck)).length;
  return failed + stalled + stuck;
}
