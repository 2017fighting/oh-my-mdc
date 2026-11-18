import type { CollectedActress } from "./api";

/**
 * 「从 App 收藏导入」的纯逻辑（#50，规格 §6.4/§8.4）。
 *
 * 名单来自 `GET /api/javdb/collected-actors`（需 token；`subscribed` 是服务端拿订阅表算的），
 * 建订阅仍走 `POST /api/subscriptions`（#47 已有的那个端点）——一次一位，逐条回执。
 * 为什么不是批量端点见 commit 说明：逐条回执本来就要每条一个结局，批量端点只是把同一份
 * 循环搬到服务端，还会让「第 3 位门禁没开」变成一次整批失败。
 *
 * **这里没有搜索**：上游不提供按名字搜女优的端点（§13 实测），面板也不开搜索框。
 */

/** 上游性别码里唯一被实测确认的「男优」（0 = 女优，1 = 男优）。 */
const GENDER_MALE = 1;

/** CollectedImportItem 是名单里的一行加上「本地该怎么处置」。 */
export type CollectedImportItem = {
  actress: CollectedActress;
  /** selectable 表示这一行勾得动：是女优，且还没订阅。 */
  selectable: boolean;
  /** reason 说清为什么勾不动（能勾时是空串）。行不藏起来——藏起来用户永远不知道为什么少人。 */
  reason: string;
};

/**
 * collectedImportItems 把上游名单翻成「能不能勾」。
 *
 * 两条判据：已订阅的置灰（`subscribed` 由服务端算，重复导入是幂等的，但让人再勾一遍没有意义），
 * gender=1 的男优勾不动（这是女优订阅）。不认识的性别码**不猜**：一律当成可勾，
 * 宁让人自己决定，也不静默藏一位女优。
 */
export function collectedImportItems(rows: CollectedActress[]): CollectedImportItem[] {
  return rows.map((actress) => {
    if (actress.subscribed) return { actress, selectable: false, reason: "已订阅" };
    if (actress.gender === GENDER_MALE) {
      return { actress, selectable: false, reason: "男优，女优订阅不收" };
    }
    return { actress, selectable: true, reason: "" };
  });
}

/**
 * importIDs 算出这一按要发哪几个 id：只发「能勾且勾上了」的，顺序照名单。
 *
 * 顺序照名单而不是照点击顺序：逐条回执按那个顺序落，屏幕上的行就不跳。
 * 一个都没勾时回空数组——不发一个空请求去让服务端说「没东西」。
 */
export function importIDs(
  items: CollectedImportItem[],
  selected: ReadonlySet<string>,
): string[] {
  const ids: string[] = [];
  for (const item of items) {
    if (item.selectable && selected.has(item.actress.id)) ids.push(item.actress.id);
  }
  return ids;
}

/**
 * ImportOutcome 是一位女优的逐条回执：新建、本来就有（幂等放行）、失败。
 *
 * `outcome` 的两个成功值与 `POST /api/subscriptions` 的 `outcome` 同名（created/duplicate），
 * 失败那一档是我们补的——那条请求本身没成功，服务端不会给 outcome。
 */
export type ImportOutcome = {
  id: string;
  name: string;
  outcome: "created" | "duplicate" | "failed";
  /** message 只在失败时有值：端点信封里那句 zh-CN，原样给人看。 */
  message?: string;
};

/** importResultLabel 是那一行在导入之后自己说的话。 */
export function importResultLabel(result: ImportOutcome): string {
  switch (result.outcome) {
    case "created":
      return "已订阅";
    case "duplicate":
      return "本来就有，水位线原样保留";
    default:
      return `失败：${result.message ?? "不知道原因"}`;
  }
}

/**
 * importReceipts 把逐条回执合成一句话：新建几位、本来就有几位、失败几位。
 *
 * 三个数分开数，因为它们对用户的含义不同：新建 = 真的多了人；本来就有 = 幂等放行、
 * 什么都没变；失败 = 要看那句话。失败的名字与原因都写出来——一张只报总数的回执
 * 会让人回头一行行找是哪位没进去。全成是 ok 那一档，有一条失败就是 warn（回执的
 * tone 只有 ok / warn 两档，与 parallel 的重试回执同一套词）。
 */
export function importReceipts(results: ImportOutcome[]): {
  tone: "ok" | "warn";
  text: string;
} {
  if (results.length === 0) return { tone: "ok", text: "" };

  const created = results.filter((result) => result.outcome === "created").length;
  const duplicate = results.filter((result) => result.outcome === "duplicate").length;
  const failed = results.filter((result) => result.outcome === "failed");

  const parts = [`新建 ${created}`, `本来就有 ${duplicate}`];
  if (failed.length > 0) parts.push(`失败 ${failed.length}`);

  let text = `导入 ${results.length} 位：${parts.join("、")}`;
  if (failed.length > 0) {
    text += `（${failed.map((result) => `${result.name}：${result.message ?? "不知道原因"}`).join("；")}）`;
  }
  // warn 而不是 bad：多数成功了，失败的那几条在括号里点了名。
  return { tone: failed.length > 0 ? "warn" : "ok", text };
}
