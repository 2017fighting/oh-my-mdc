import * as React from "react";
import { cn } from "cn";

import { Button } from "~/components/ui/button";

/**
 * ConfirmBar 是「不可逆动作先说后果，再给按钮」（DESIGN 的 Consequence-First Rule）。
 *
 * 以前这段结构只活在任务抽屉里：批量补录（删 `{番号}.nfo`）与「待补齐」的强制重刮都
 * 是单击即发，后果写在卡片头的 12px 灰字里、离按钮几千像素。现在它们共用这一条——
 * 一处改文案，三处同时生效；不存在「某处忘了确认」。
 *
 * 三条不可省的细节：
 *
 * - **焦点进确认、取消后还给触发它的那颗按钮**：确认条是刚出现的、要人读一句再决定的
 *   东西，不接焦点就等于对键盘与读屏用户不存在。`cancelFocusRef` 由调用方给。
 * - **不开对话框**：确认与取消都在条子里，屏幕上不发生第二次跳转（DESIGN 同一条）。
 * - **不预支结果**：`confirmLabel` 只说这份动作叫什么，不说它会成功。
 */
export function ConfirmBar({
  label,
  text,
  confirmLabel,
  cancelLabel = "取消",
  tone = "progress",
  busy = false,
  onConfirm,
  onCancel,
  cancelFocusRef,
}: {
  /** 读屏用的组名：`确认强制重刮`、`确认补录一批目录`。 */
  label: string;
  /** 会发生的后果，一句整话。 */
  text: React.ReactNode;
  confirmLabel: string;
  cancelLabel?: string;
  /** 琥珀是「会被拉回去重做」，红是「会删掉东西」——按后果的不可逆程度选，不按子系统。 */
  tone?: "progress" | "danger";
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  /** 取消后焦点回到哪：通常是打开确认条的那颗按钮。 */
  cancelFocusRef?: React.RefObject<HTMLElement | null>;
}) {
  const confirmRef = React.useRef<HTMLButtonElement>(null);
  const wantRestore = React.useRef(false);

  React.useEffect(() => {
    if (wantRestore.current) {
      wantRestore.current = false;
      cancelFocusRef?.current?.focus();
      return;
    }
    confirmRef.current?.focus();
    // 只在挂载时接一次焦点：之后的重渲染不该把人的焦点从「取消」上抢回来。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const cancel = () => {
    wantRestore.current = true;
    onCancel();
  };

  return (
    <div
      role="group"
      aria-label={label}
      className={cn(
        "space-y-2 rounded-lg border px-3 py-2",
        tone === "danger"
          ? "border-status-danger-border bg-status-danger-surface"
          : "border-status-progress-border bg-status-progress-surface",
      )}
    >
      <p
        className={cn(
          "text-xs leading-relaxed",
          tone === "danger" ? "text-status-danger-text" : "text-status-progress-text",
        )}
      >
        {text}
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          ref={confirmRef}
          className="h-11 px-3 dense md:h-7 md:px-2.5 md:text-xs"
          disabled={busy}
          onClick={onConfirm}
        >
          {confirmLabel}
        </Button>
        <Button
          variant="ghost"
          className="h-11 px-3 dense md:h-7 md:px-2.5 md:text-xs"
          onClick={cancel}
        >
          {cancelLabel}
        </Button>
      </div>
    </div>
  );
}
