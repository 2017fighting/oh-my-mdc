/**
 * 回执（receipt）的形状（规格 §8.2「动完手的回执」）。
 *
 * 一条回执 = 动作名 + 结局 + 一句后果。面板把它播报一遍（屏幕阅读器）并留在卡片上
 * 10 分钟；订阅区把同一套形状用于「订阅 / 回灌 / 停用 / 恢复 / 收下」。
 *
 * 只有类型住在这里：队列本体（useReceipts）留在 panel.tsx——那是面板外壳的事，
 * 而 subscriptions.tsx 只需要知道「回执长什么样」。
 */

/** RetryNotice 是一次任务重试的结果提示：成功用 ok，被拒/失败用 warn。
 *  parts 是逐版结论：回执按它换行，text 是同一句话的单行串（播报与抽屉用）。 */
export type RetryNotice = { tone: "ok" | "warn"; text: string; parts?: string[] };

/** ReceiptDraft 是一条新回执（还没编号）：番号或待办名 + 结局 + 一句后果。 */
export type ReceiptDraft = RetryNotice & {
  title: string;
  /**
   * 回执说的那个动作本身：任务侧是「重试」，副作用侧是「重发」，订阅区是
   * 「订阅 / 回灌 / 停用 / 恢复」，跳过表格里是「收下」。播报位念的是「已<verb> <title> · <text>」。
   */
  verb?: "重试" | "重发" | "订阅" | "导入" | "回灌" | "停用" | "恢复" | "收下";
};

/** Receipt 是已经发出的一条回执。 */
export type Receipt = ReceiptDraft & { id: number; at: number };
