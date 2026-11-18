import * as React from "react";
import { X } from "lucide-react";
import { cn } from "cn";

import { formatWhen } from "~/lib/time";
import type { Receipt, ReceiptDraft } from "~/lib/receipts";

/**
 * 回执（规格 §8.2「动完手的回执」）：队列、渲染与播报。
 *
 * 这三件东西以前住在 `panel.tsx` 里，只有面板那一张卡挂得上。补录与订阅搬进集成设置
 * 之后，它们也需要「按下去 -> 屏幕上留一句证据」，于是搬到这里——同一个形状、同一份
 * 时长、同一个播报规则，两处不许各写一套。
 *
 * 语义仍属于外壳：队列只活在一次挂载里，不落库、不跨页面。分区自己持有一份。
 */

/** 一条回执留多久：够读到，又不会变成第二张列表。 */
export const RECEIPT_LIMIT = 3;
/** 一次批量动作的回执上限：逐条回执再多，也只为这一批留这么多面。 */
export const RECEIPT_CEILING = 8;
const RECEIPT_TTL = 10 * 60_000;

export type ReceiptsState = ReturnType<typeof useReceipts>;

/**
 * useReceipts 是回执队列：压一条进队、收一条、到点自己退场。
 *
 * 面板与集成设置的批量面共用它，所以「留 3 条、活 10 分钟」只有一个出处。
 */
export function useReceipts() {
  const [receipts, setReceipts] = React.useState<Receipt[]>([]);
  const nextID = React.useRef(1);
  const listRef = React.useRef<HTMLDivElement>(null);

  const push = React.useCallback((draft: ReceiptDraft, limit = RECEIPT_LIMIT) => {
    setReceipts((current) =>
      [{ ...draft, id: nextID.current++, at: Date.now() }, ...current].slice(
        0,
        Math.min(limit, RECEIPT_CEILING),
      ),
    );
  }, []);

  const dismiss = React.useCallback((id: number) => {
    setReceipts((current) => current.filter((receipt) => receipt.id !== id));
  }, []);

  // 回执自己退场：不用人清理，但退场前有整整十分钟可被读到。
  React.useEffect(() => {
    if (receipts.length === 0) return;
    const timer = window.setInterval(() => {
      const cutoff = Date.now() - RECEIPT_TTL;
      setReceipts((current) =>
        current.some((receipt) => receipt.at < cutoff)
          ? current.filter((receipt) => receipt.at >= cutoff)
          : current,
      );
    }, 30_000);
    return () => window.clearInterval(timer);
  }, [receipts.length]);

  return { receipts, push, dismiss, listRef };
}

/** ReceiptList 是卡片顶部的回执行：一行一条，语气词打头，能逐条收起。 */
export function ReceiptList({
  receipts,
  now,
  listRef,
  onDismiss,
}: {
  receipts: Receipt[];
  now: number;
  listRef: React.RefObject<HTMLDivElement | null>;
  onDismiss: (id: number) => void;
}) {
  if (receipts.length === 0) return null;
  return (
    <div
      ref={listRef}
      tabIndex={-1}
      role="group"
      aria-label="操作回执"
      className="mb-3 divide-y rounded-lg bg-muted/50 px-3 outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
    >
      {receipts.map((receipt) => (
        <div key={receipt.id} className="flex items-start gap-2 py-1.5 text-xs leading-relaxed">
          <span
            className={cn(
              "shrink-0 font-medium",
              receipt.tone === "ok" ? "text-status-done-text" : "text-status-progress-text",
            )}
          >
            {receipt.tone === "ok" ? `已${receipt.verb ?? "重试"}` : "没成"}
          </span>
          <span className="min-w-0 flex-1 break-words">
            <span className="font-medium tabular-nums">{receipt.title}</span>
            {/* 两个版本以上时一行会变成日志：逐版结论各占一行，
                番号仍在第一行开头，回执是一段话不是一串分号。 */}
            {(receipt.parts ?? receipt.text.split("；")).map((part, index) => (
              <span key={index} className={index > 0 ? "block pl-2" : undefined}>
                {index === 0 ? ` · ${part}` : part}
              </span>
            ))}
            <span className="ml-1.5 tabular-nums text-muted-foreground">
              {formatWhen(receipt.at, now)}
            </span>
          </span>
          <button
            type="button"
            onClick={() => onDismiss(receipt.id)}
            aria-label={`收起回执：${receipt.title}`}
            className="relative -m-1 grid size-8 shrink-0 place-items-center rounded-md text-muted-foreground transition-colors before:absolute before:-inset-2 before:content-[''] hover:bg-popover hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring md:size-6"
          >
            <X className="size-3" aria-hidden />
          </button>
        </div>
      ))}
    </div>
  );
}

/**
 * ReceiptAnnouncer 把最新一条回执念一遍：回执在屏幕上留 10 分钟，读屏用户得有同一份。
 *
 * 常驻在树上（空内容也渲染）：带内容挂载的活区不会播报。
 */
export function ReceiptAnnouncer({ receipts }: { receipts: Receipt[] }) {
  const latest = receipts[0];
  const text = latest
    ? `${latest.tone === "ok" ? `已${latest.verb ?? "重试"}` : "没成"} ${latest.title} · ${latest.text}`
    : "";
  return (
    <p role="status" aria-live="polite" aria-atomic="true" className="sr-only">
      {text}
    </p>
  );
}
