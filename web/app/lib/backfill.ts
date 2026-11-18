import type { BackfillDir } from "./api";

/**
 * 补录目录（用于「未补录目录」这一节，GLOSSARY「补录」）的纯逻辑：分组、筛选、成批。
 *
 * 以前这块是 `dirs.map()` 一把铺完：502 个目录 = 26,533px，占整页 85%，每行一颗
 * 「补录」，没有搜索、没有分组、没有批量。判据与文案搬到这里之后，节里只剩渲染。
 *
 * 三件事刻意留在这里而不是组件里：
 *
 * 1. **分组按番号前缀**（`ABF-017` → `ABF`）：502 个目录是 114 个厂商的重复，
 *    人认的是前缀，不是字母序。
 * 2. **一批的边界是「当前筛选的前 N 个」**：`每批补录 50 部` 与确认条上的数字是同一个
 *    来源，按钮印多少就补多少，不许两边各算一遍。
 * 3. **后果是一句话**：补录会删旧 `{番号}.nfo`，这句必须和按钮一起出现（DESIGN 的
 *    Consequence-First Rule）。
 */

/** 一批补录的上限：后端一次一个番号，这里是串行发的条数。 */
export const BACKFILL_BATCH = 50;

/** 列表默认渲染多少行（与任务表一页同量级），其余收进「显示更多」。 */
export const BACKFILL_PAGE = 50;

/**
 * backfillPrefix 取番号的「厂商前缀」：第一个连字符之前的那一段。
 *
 * 没有连字符的番号（`T28313`、`MAAN-1153` 之外的杂号）整串就是它自己的前缀——
 * 不硬造一个 `其它` 组，也不丢行。
 */
export function backfillPrefix(number: string): string {
  const head = number.split("-")[0]?.trim();
  // 连字符打头的脏目录：头段是空的，取整串时把打头的分隔符去掉，别造一个空前缀组。
  const prefix = head === undefined || head === "" ? number.trim().replace(/^-+/, "") : head;
  return (prefix === "" ? number : prefix).toUpperCase();
}

export type BackfillGroup = { prefix: string; count: number };

/** backfillGroups 是前缀分组的结果：按条数从多到少，同数按字母序（稳定、可预期）。 */
export function backfillGroups(dirs: BackfillDir[]): BackfillGroup[] {
  const counts = new Map<string, number>();
  for (const dir of dirs) {
    const prefix = backfillPrefix(dir.number);
    counts.set(prefix, (counts.get(prefix) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([prefix, count]) => ({ prefix, count }))
    .sort((a, b) => b.count - a.count || a.prefix.localeCompare(b.prefix));
}

export type BackfillFilter = {
  /** 搜索词：按番号做不区分大小写的包含匹配，空串＝不筛。 */
  q: string;
  /** 前缀筛选：空串＝全部前缀。 */
  prefix: string;
};

export const EMPTY_FILTER: BackfillFilter = { q: "", prefix: "" };

/**
 * filterBackfill 把搜索词与前缀一起作用，保持后端给的顺序（番号序）。
 *
 * 搜索词里的连字符被吃掉再比：人打 `abf017` 与 `ABF-017` 想找的是同一个目录。
 */
export function filterBackfill(dirs: BackfillDir[], filter: BackfillFilter): BackfillDir[] {
  const needle = normalize(filter.q);
  return dirs.filter((dir) => {
    if (filter.prefix !== "" && backfillPrefix(dir.number) !== filter.prefix) return false;
    if (needle === "") return true;
    return normalize(dir.number).includes(needle);
  });
}

function normalize(value: string): string {
  return value.trim().toLowerCase().replace(/[\s-]/g, "");
}

/** nextBackfillBatch 是「按一下就动手」的那一批：当前筛选的前 BACKFILL_BATCH 个。 */
export function nextBackfillBatch(dirs: BackfillDir[], limit = BACKFILL_BATCH): BackfillDir[] {
  return dirs.slice(0, Math.max(0, limit));
}

/**
 * backfillConfirmText 是确认条上那句话：范围 + 后果 + 不做什么。
 *
 * 三句都必须在这里：删 `.nfo` 是最容易后悔的一步，而「零下载、不过 qBittorrent」
 * 是让人敢按下去的那点信息。
 */
export function backfillConfirmText(count: number): string {
  return `将补录 ${count} 个目录：现有视频原地入库，随后强制重刮重写 NFO 与图片；${count} 个旧的 {番号}.nfo 会被删掉（movie.nfo 除外）。不会下载，不过 qBittorrent。`;
}

/** BackfillOutcome 是串行一发一条的结局：够拼一句总账，也够逐条抖出来。 */
export type BackfillOutcome = { number: string; ok: boolean; message?: string };

/**
 * backfillBatchNote 把一批的结局合成一句话。
 *
 * 全成、全败、成败混合各说各的：混合时点名第一个失败的番号——失败的原因通常一样，
 * 但要人去看的是具体那一条。
 */
export function backfillBatchNote(outcomes: BackfillOutcome[]): string {
  const failed = outcomes.filter((outcome) => !outcome.ok);
  if (outcomes.length === 0) return "这一批没有目录可补";
  if (failed.length === 0) return `${outcomes.length} 个目录已补录，重刮会重写 NFO 与图片`;
  if (failed.length === outcomes.length) {
    return `${outcomes.length} 个目录都没成：${failed[0]?.message ?? "端点没回话"}`;
  }
  return `${outcomes.length - failed.length} 个已补录，${failed.length} 个没成（第一个是 ${failed[0]?.number}：${failed[0]?.message ?? "端点没回话"}）`;
}

/**
 * backfillSummary 是面板上那一行摘要（补录不常驻面板，只留一行指路）。
 *
 * 0 个目录时返回 null：面板据此整行不渲染，不留一句「没有待补录的目录」的家具
 * （DESIGN 的 Nothing-To-Do Rule）。
 */
export function backfillSummary(scan: { dirs: BackfillDir[] } | null): string | null {
  if (!scan) return null;
  const count = scan.dirs.length;
  if (count === 0) return null;
  const groups = backfillGroups(scan.dirs).length;
  return `${count} 个旧目录待补录 · ${groups} 个番号前缀`;
}
