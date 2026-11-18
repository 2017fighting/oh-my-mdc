/**
 * 「最近被排掉的」那块折叠格（规格 §8.2/§8.4，GLOSSARY「跳过 / 收下」）。
 *
 * 订阅的筛子会在详情层排掉三类作品（多人一起的 / 精選綜合 / 长合集），被排掉的一律
 * 落跳过表、留在面板上 30 天——“判据可以错，出口必须留”这句话的落点就是这块折叠格。
 * 这里只放纯逻辑：判据名说成什么、一行怎么写、收下的回执怎么念。
 */

/**
 * SkipReason 是三条判据（§6.4 筛子，GLOSSARY「合集」）。
 *
 * 与服务端同一套词：`subscription_skip.reason` 有 CHECK 约束，Go 侧是
 * `metrics.ReasonNotSingle / ReasonCompilation / ReasonLongForm`。名字就是字符串本身
 * ——跳过的 identity 是 `(subscription_id, movie_id)`，reason 只是「为什么被排掉」的解释。
 */
export type SkipReason = "not_single" | "compilation" | "long_form";

/** SubscriptionSkip 是 GET /api/subscriptions/{id}/skips 的一行（§8.4）。 */
export type SubscriptionSkip = {
  id: number;
  movie_id: string;
  number: string;
  title: string;
  /** 发行日（`YYYY-MM-DD`）；上游没给时是 null（面板不说那一天，不编今天）。 */
  release_date: string | null;
  /** 三条判据之一（与服务端同一套词）。 */
  reason: SkipReason;
  /** 落行时刻（RFC3339）：面板要的是「多久之前」。 */
  skipped_at: string;
};

/**
 * skipReasonLabel 把判据说成一句人话。
 *
 * 三个词都取 GLOSSARY 允许的写法：「合集」那一族只许说「合集 / 精選綜合 / 长合集」，
 * 不许说「精选」（`_Avoid_` 那一行点名的正是它）。`compilation` 的判据就是类別标签
 * 「精選綜合」，所以那句人话直接用标签名——用户在上游页面看到的是同一个词。
 *
 * 认不出来就原样回那个词：面板多显示一个陌生词，好过把一条新判据说成某个已知的旧词
 * （那会让人对着“长合集”去救一部共演）。参数的宽类型是刻意的：值域由库里那几条 CHECK
 * 约束守着，面板对未知值只能原样透出。
 */
export function skipReasonLabel(reason: SkipReason | string): string {
  switch (reason) {
    case "not_single":
      return "多人一起的";
    case "compilation":
      return "精選綜合";
    case "long_form":
      return "长合集";
    default:
      return reason;
  }
}

/**
 * skipsBadge 是折叠格头上的计数徽标。
 *
 * 0 也照实写：一个空白的徽标分不清「没有被排掉的」与「还没读到」，而这两件事在对账
 * 场景里差得很远。超过 99 收成 `99+`——徽标是提示，不是列表。
 */
export function skipsBadge(count: number): string {
  if (!Number.isFinite(count) || count <= 0) return "0";
  if (count > 99) return "99+";
  return String(Math.trunc(count));
}

/**
 * skipRowLine 是折叠格里的一行：番号 + 标题 + 发行日。
 *
 * 缺哪一段就不说哪一段（不留空括号、不拿今天冒充发行日）：详情没给标题时这一行只剩
 * 番号与日期，仍然是可辨认的。
 */
export function skipRowLine(
  skip: Pick<SubscriptionSkip, "number" | "title" | "release_date">,
): string {
  return [skip.number, skip.title, skip.release_date ?? ""]
    .map((part) => part.trim())
    .filter((part) => part !== "")
    .join(" · ");
}

/**
 * skipAcceptText 是「收下」这颗按钮的回执（§8.4 的 outcome）。
 *
 * `no_magnet` 要说得像一件还没做完的事：收下时还没有磁力的话，跳过行留着、等出水了
 * 再点一次——说成「已收下」会让人以为这一部已经在下载了。
 */
export function skipAcceptText(outcome: string): string {
  if (outcome === "accepted") return "已收下，任务已建、开始加种";
  if (outcome === "no_magnet") return "还没有磁力，先留着——出水了再收下";
  // 认不出来的结果不許说成「开始加种」：那一句是关于下游的承诺，它只能由 accepted 说。
  return "已收下";
}
