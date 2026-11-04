import type { AttentionItem } from "./api";

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
