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
/** 只用来切台阶：一个月按 30 天算，不去追日历（追了就要处理二月的 28/29）。 */
const MONTH = 30 * DAY;
const YEAR = 365 * DAY;

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

/**
 * 相对时间：刚刚 / N 分钟前 / N 小时前 / 昨天 / N 天前 / N 个月前 / N 年前。
 *
 * 未来时间（对账的「下次尝试」会排在未来）走同一份台阶的正向说法。
 */
export function formatWhen(
  value: string | number | Date | null | undefined,
  now: number = Date.now(),
): string {
  const date = parse(value);
  if (!date) return "—";

  const delta = now - date.getTime();
  return relative(Math.abs(delta), delta < 0 ? "future" : "past");
}

/**
 * 「已经挂了多久」：给「需要人工」的分节标题用。
 *
 * 与 formatWhen 共用那份台阶，只是永远取过去的方向：未来的时刻对「挂了多久」没有
 * 意义，夹成「刚刚」而不是算成负数。
 */
export function formatAge(
  value: string | number | Date | null | undefined,
  now: number = Date.now(),
): string {
  const date = parse(value);
  if (!date) return "—";
  return relative(Math.max(0, now - date.getTime()), "past");
}

/**
 * 「多久以后」：副作用的下次尝试时间通常在将来。
 *
 * 同一份台阶的正向说法；已经过期的（对账刚把这一轮推过头）夹成「马上」，不说负数。
 */
export function formatFuture(
  value: string | number | Date | null | undefined,
  now: number = Date.now(),
): string {
  const date = parse(value);
  if (!date) return "—";
  return relative(Math.max(0, date.getTime() - now), "future");
}

/**
 * relative 是唯一的一份相对时间台阶。
 *
 * 以前 formatWhen、formatAge、formatFuture 各写一份阶梯，于是「超过一周说什么」
 * 在这些出口里漂了两次（最后一次漂成绝对日期，正好违反 DESIGN 自己那条
 * 「绝对时间只进 title」）。现在方向由参数决定，台阶只在这里改。
 *
 * 台阶：<1 分钟（刚刚 / 马上）→ 分钟 → 小时 → 天（过去的第一天说「昨天」）→
 * 30 天换月、365 天换年。**没有绝对时间的出口**。
 */
function relative(delta: number, direction: "past" | "future"): string {
  const suffix = direction === "past" ? "前" : "后";
  if (delta < MINUTE) return direction === "past" ? "刚刚" : "马上";
  if (delta < HOUR) return `${Math.floor(delta / MINUTE)} 分钟${suffix}`;
  if (delta < DAY) return `${Math.floor(delta / HOUR)} 小时${suffix}`;
  const days = Math.floor(delta / DAY);
  if (days === 1 && direction === "past") return "昨天";
  if (delta < MONTH) return `${days} 天${suffix}`;
  if (delta < YEAR) return `${Math.max(1, Math.floor(delta / MONTH))} 个月${suffix}`;
  return `${Math.max(1, Math.floor(delta / YEAR))} 年${suffix}`;
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
