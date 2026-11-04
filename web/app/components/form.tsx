import * as React from "react";
import { AlertTriangle, Check, Loader2, X } from "lucide-react";
import { cn } from "cn";

/**
 * 向导用的小零件：带说明的字段、输入框、开关、测试结果 chip、提示条。
 *
 * 不引 shadcn 的 Input/Switch：向导只需要一两个形状，手写这几行比多生成两个组件更好读。
 */

export function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block">
      <span className="text-dense font-medium">{label}</span>
      {hint ? (
        <span className="ml-2 text-xs text-muted-foreground">{hint}</span>
      ) : null}
      <span className="mt-1.5 block">{children}</span>
    </label>
  );
}

type TextInputProps = React.ComponentProps<"input"> & { mono?: boolean };

export function TextInput({ className, mono, ...props }: TextInputProps) {
  return (
    <input
      {...props}
      className={cn(
        "h-8 w-full rounded-lg border border-input bg-background px-2.5 text-base outline-none sm:text-dense",
        "placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring",
        "disabled:cursor-not-allowed disabled:opacity-60",
        mono && "font-mono",
        className,
      )}
    />
  );
}

export function Toggle({
  label,
  desc,
  checked,
  onChange,
}: {
  label: string;
  desc?: string;
  checked: boolean;
  onChange: (next: boolean) => void;
}) {
  return (
    <div className="flex items-start justify-between gap-4 rounded-lg border p-3">
      <div>
        <div className="text-dense font-medium">{label}</div>
        {desc ? (
          <p className="mt-0.5 max-w-md text-xs leading-relaxed text-muted-foreground">
            {desc}
          </p>
        ) : null}
      </div>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        aria-label={label}
        onClick={() => onChange(!checked)}
        className={cn(
          "relative mt-0.5 inline-flex h-5 w-9 shrink-0 items-center rounded-full border transition-colors",
          "after:absolute after:-inset-x-3 after:-inset-y-3 after:content-['']",
          "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
          checked ? "border-primary bg-primary" : "border-input bg-muted",
        )}
      >
        <span
          className={cn(
            "size-3.5 rounded-full bg-background transition-transform",
            checked ? "translate-x-4.5" : "translate-x-0.5",
          )}
        />
      </button>
    </div>
  );
}

/** TestChipState 是「测试连接」按钮旁那个结果位。 */
export type TestChipState =
  | { kind: "idle" }
  | { kind: "testing" }
  | { kind: "ok"; detail?: string; durationMs?: number }
  | { kind: "fail"; code: string; detail?: string; durationMs?: number }
  | { kind: "placeholder"; detail?: string };

export function TestChip({ state }: { state: TestChipState }) {
  if (state.kind === "idle") {
    return <span className="text-xs text-muted-foreground">还没测试</span>;
  }
  if (state.kind === "testing") {
    return (
      <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
        <Loader2 className="size-3 animate-spin" aria-hidden />
        测试中…
      </span>
    );
  }

  const look = chipLook(state);
  return (
    <span
      className={cn(
        "inline-flex max-w-full items-center gap-1 rounded-md border px-1.5 py-0.5 text-xs",
        look.tone,
      )}
      title={state.detail}
    >
      {look.icon}
      <span className="truncate">{look.label}</span>
    </span>
  );
}

/** chipLook 把一次测试结论翻成颜色、文案与图标。 */
function chipLook(state: Exclude<TestChipState, { kind: "idle" } | { kind: "testing" }>) {
  switch (state.kind) {
    case "ok":
      return {
        tone:
          "border-status-done-border bg-status-done-surface text-status-done-text",
        label: `通过${state.durationMs !== undefined ? ` · ${state.durationMs}ms` : ""}`,
        icon: <Check className="size-3 shrink-0" aria-hidden />,
      };
    case "placeholder":
      return {
        tone: "border-border bg-muted text-muted-foreground",
        label: "未接线",
        icon: null,
      };
    case "fail":
      return {
        tone: "border-status-danger-border bg-status-danger-surface text-status-danger-text",
        label: `失败 · ${errorCodeLabel(state.code)}${state.durationMs !== undefined ? ` · ${state.durationMs}ms` : ""}`,
        icon: <X className="size-3 shrink-0" aria-hidden />,
      };
  }
}

/**
 * errorCodeLabel 把测试错误码翻成向导里的人话（原码仍留在 title 的明细里）。
 *
 * Telegram 的两步测试专用码要说清是「哪一关没过」：token 无效 → 去换 bot token；
 * chat 未激活 → 先跟 bot 说句话再测。其余码保持原样，方便对照规格 §8.4 排查。
 */
function errorCodeLabel(code: string): string {
  switch (code) {
    case "token_invalid":
      return "token 无效";
    case "chat_unavailable":
      return "chat 未激活";
    default:
      return code;
  }
}

export function Notice({
  tone = "muted",
  children,
  onClose,
}: {
  tone?: "ok" | "warn" | "bad" | "muted";
  children: React.ReactNode;
  onClose?: () => void;
}) {
  const toneClass = {
    ok: "border-status-done-border bg-status-done-surface text-status-done-text",
    warn: "border-status-progress-border bg-status-progress-surface text-status-progress-text",
    bad: "border-status-danger-border bg-status-danger-surface text-status-danger-text",
    muted: "border-border bg-muted/50 text-foreground",
  }[tone];

  return (
    <div
      role={tone === "warn" || tone === "bad" ? "alert" : "status"}
      className={cn(
        "flex items-start gap-2 rounded-lg border px-3 py-2 text-xs leading-relaxed",
        toneClass,
      )}
    >
      {tone === "warn" || tone === "bad" ? (
        <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />
      ) : null}
      <div className="min-w-0 flex-1">{children}</div>
      {onClose ? (
        <button
          type="button"
          onClick={onClose}
          className="shrink-0 opacity-60 hover:opacity-100"
          aria-label="关闭提示"
        >
          <X className="size-3.5" aria-hidden />
        </button>
      ) : null}
    </div>
  );
}
