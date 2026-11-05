import type { TaskState } from "./api";

/**
 * 面板上的词，与它们的一句话解释——单一真相。
 *
 * 存在的理由是一次真实的漂移：四个状态名曾经同时写在 `TASK_STATE_LOOK`、
 * `STATE_FILTERS`、DESIGN、PRODUCT、GLOSSARY 与 spec 里，改一个词要改六处，
 * 于是「需人工」在任务状态与待办区之间来回借用了很久（见 DESIGN 的
 * The Two-Scales Rule）。现在文案只在这里写一遍，UI 与 `?` 浮层都从它读。
 *
 * 不搬 `GLOSSARY.md`：那是产品真源，面向构建而非面向这块屏；浮层只收面板上
 * 真会出现、真会同屏混淆的词，末尾留一行指向仓库。
 */

/** 四个状态名。任务级与版本级同名，尺度由位置区分（状态列 / 版本列）。 */
export const STATUS_NAMES: Record<TaskState, string> = {
  downloading: "下载中",
  processing: "加工中",
  finished: "已入库",
  failed: "失败",
};

/** 每个状态的一句话：它此刻意味着什么、会不会要你动手。 */
export const STATUS_NOTES: Record<TaskState, string> = {
  downloading: "qBittorrent 正在下，或刚加完种还没报进度。",
  processing: "下完了在取数与整理：刮削、下图片、写 NFO、硬链接进媒体库。",
  finished: "文件已就位、媒体库已刷新。唯一不需要你动手的状态。",
  failed:
    "这一条版本卡在下载 / 刮削 / 整理的某一步。一部作品的版本全失败，任务状态也叫「失败」；只失败一条时任务随已入库或下载中走，那条失败版本仍在「需要人工」里。URL 与接口里写作 ?state=failed。",
};

/** 显示顺序：与筛选 chip 一致，读起来就是面板自己那一行。 */
export const STATUS_ORDER: TaskState[] = ["downloading", "processing", "finished", "failed"];

/** 一条术语：词 + 一句解释（不写第二段，浮层里每行都要能一眼扫完）。 */
export type GlossaryEntry = { term: string; note: string };

/** 「容易混的」四条：它们才是浮层真正存在的理由。 */
export const CONFUSABLES: GlossaryEntry[] = [
  {
    term: "任务级「失败」vs 版本级「失败」",
    note: "任务一行、按 movie_id 聚合；版本是它下面每条种子。任务的版本全失败才算任务级失败——只失败一条时，状态随已入库或下载中走。",
  },
  {
    term: "「需要人工」",
    note: "它是那块地方的名字，不是状态名：失败版本、停滞下载、驻留副作用都归在那里，页标题数的就是那里的行数（失败按番号、停滞按番号、驻留按病因各算一行）。",
  },
  {
    term: "驻留副作用",
    note: "退避耗尽的旁路待办（删清单项、刷 Jellyfin、发通知），等人工「重发」。同目标只排一条；同一原因卡住的多条收成一组，一组算一行、一颗「重发 N 条」，所以构成行写「2 组 4 条」。",
  },
  {
    term: "重试 / 重发 / 强制重刮",
    note: "重试是任务侧：回加工开头重跑，或按存的交付材料重新加种。重发是副作用侧：只把待办交回队列，执行仍归副作用循环。强制重刮只给存在已入库版本的场合，让它下一次取数不复用元数据。",
  },
];
