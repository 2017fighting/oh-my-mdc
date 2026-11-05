/**
 * 时间显示的唯一出口。
 *
 * 值班台上要回答的是「多久以前」，不是「几时几分秒」：绝对时间每个单元格都写着
 * `2026/11/4 14:29:16` 时，人得自己减一遍才知道这一行是刚动的还是昨天动的。
 * 所以默认给相对时间，绝对时间留在 `title` 里随 hover 出现。
 */

/** 当天时刻，秒级（`14:42:05`）。用于「上次读取」。 */
export function formatClock(value: string | number | Date | null | undefined): string {
  const date = parse(value);
  if (!date) return "—";
  return date.toLocaleTimeString("zh-CN", { hour12: false });
}

/** 完整绝对时间（`2026/11/4 14:42:05`）。只用在 `title` 与超过一周的旧时间上。 */
export function formatAbsolute(value: string | number | Date | null | undefined): string {
  const date = parse(value);
  if (!date) return "—";
  return date.toLocaleString("zh-CN", { hour12: false });
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** 一段进行中的耗时（「正在跑 N 秒」）：刚刚 / N 秒 / N 分钟 / N 小时，不带「前」。
 * 与 formatWhen 同源的分档，但方向是「已经跑了多久」而不是「多久以前」。 */
export function formatElapsed(
  value: string | number | Date | null | undefined,
  now: number = Date.now(),
): string {
  const date = parse(value);
  if (!date) return "—";
  const delta = Math.max(0, now - date.getTime());
  if (delta < 5_000) return "刚刚";
  if (delta < MINUTE) return `${Math.floor(delta / 1000)} 秒`;
  if (delta < HOUR) return `${Math.floor(delta / MINUTE)} 分钟`;
  return `${Math.floor(delta / HOUR)} 小时`;
}

/** 相对时间：刚刚 / N 分钟前 / N 小时前 / 昨天 HH:MM / 超过一周给绝对时间。 */
export function formatWhen(
  value: string | number | Date | null | undefined,
  now: number = Date.now(),
): string {
  const date = parse(value);
  if (!date) return "—";

  const delta = now - date.getTime();
  // 未来时间（对账的「下次尝试」会排在未来）走正向的相对说法。
  if (delta < -MINUTE) return forward(-delta, date);
  if (delta < MINUTE) return "刚刚";
  if (delta < HOUR) return `${Math.floor(delta / MINUTE)} 分钟前`;
  if (delta < DAY) return `${Math.floor(delta / HOUR)} 小时前`;
  if (delta < 7 * DAY) {
    const days = Math.floor(delta / DAY);
    return days === 1 ? "昨天" : `${days} 天前`;
  }
  return formatAbsolute(date);
}

/**
 * 「已经挂了多久」：给「需要人工」的分节标题用。
 *
 * 与 formatWhen 同一条阶梯，只有一处不同：超过一周也不回落到绝对时间。
 * 「最早 12 天前」回答的是「这件事我躲了多久」，而 `2026/10/24 09:11` 回答的是
 * 「这是哪一天」——后者要人自己减一遍，正是值班台不该让人做的算术。
 */
export function formatAge(
  value: string | number | Date | null | undefined,
  now: number = Date.now(),
): string {
  const date = parse(value);
  if (!date) return "—";
  const delta = Math.max(0, now - date.getTime());
  if (delta < MINUTE) return "刚刚";
  if (delta < HOUR) return `${Math.floor(delta / MINUTE)} 分钟前`;
  if (delta < DAY) return `${Math.floor(delta / HOUR)} 小时前`;
  const days = Math.floor(delta / DAY);
  return days === 1 ? "昨天" : `${days} 天前`;
}

/** 「多久以后」：副作用的下次尝试时间通常在将来。 */
export function formatFuture(
  value: string | number | Date | null | undefined,
  now: number = Date.now(),
): string {
  const date = parse(value);
  if (!date) return "—";
  return forward(date.getTime() - now, date);
}

function forward(delta: number, date: Date): string {
  if (delta < MINUTE) return "马上";
  if (delta < HOUR) return `${Math.ceil(delta / MINUTE)} 分钟后`;
  if (delta < DAY) return `${Math.ceil(delta / HOUR)} 小时后`;
  return formatAbsolute(date);
}

/**
 * 循环间隔说成人话。
 *
 * `app_config` 里存的是 Go duration 串（`15m0s`），前端也可能收到秒数，
 * 两种都吃。读不到就返回 null——宁可不说，也不编一个「每 15 分钟」。
 */
export function formatInterval(raw: unknown): string | null {
  const seconds = durationSeconds(raw);
  if (seconds === null) return null;
  if (seconds < 60) return `每 ${seconds} 秒`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return minutes === 1 ? "每分钟" : `每 ${minutes} 分钟`;
  const hours = seconds / 3600;
  if (Number.isInteger(hours)) return hours === 1 ? "每小时" : `每 ${hours} 小时`;
  return `每 ${minutes} 分钟`;
}

/** durationSeconds 把 Go duration 串（`1h30m0s`）或裸秒数读成秒；读不出返回 null。 */
export function durationSeconds(raw: unknown): number | null {
  if (typeof raw === "number") return Number.isFinite(raw) && raw > 0 ? Math.round(raw) : null;
  if (typeof raw !== "string") return null;
  const text = raw.trim();
  if (text === "") return null;
  if (/^\d+$/.test(text)) {
    const seconds = Number(text);
    return seconds > 0 ? seconds : null;
  }
  const match = /^(?:(\d+(?:\.\d+)?)h)?(?:(\d+(?:\.\d+)?)m)?(?:(\d+(?:\.\d+)?)s)?$/.exec(text);
  if (!match || match.slice(1).every((part) => part === undefined)) return null;
  const [, h, m, s] = match;
  const total = Number(h ?? 0) * 3600 + Number(m ?? 0) * 60 + Number(s ?? 0);
  return total > 0 ? Math.round(total) : null;
}

function parse(value: string | number | Date | null | undefined): Date | null {
  if (value === null || value === undefined || value === "") return null;
  const date = value instanceof Date ? value : new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}
