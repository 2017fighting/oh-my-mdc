import * as React from "react";
import { AlertTriangle, Check, Loader2, X } from "lucide-react";
import { cn } from "cn";

import { formatAbsolute, formatWhen } from "~/lib/time";
import { testCodeLabel } from "~/lib/testcodes";

/**
 * 向导用的小零件：带说明的字段、输入框、开关、测试结果 chip、提示条。
 *
 * 不引 shadcn 的 Input/Switch：向导只需要一两个形状，手写这几行比多生成两个组件更好读。
 */

/**
 * Field 是一行带标签的表单字段。
 *
 * 标签用 `htmlFor` 指输入框，说明单独一段并用 `aria-describedby` 挂上：以前说明写在
 * `<label>` 里面，读屏器把「密码留空＝不改」念成了输入框名字的一部分。
 * 「未保存」只在脏字段上出现——它是这一行的事，不该只由页脚一句「未保存」代替。
 */
export function Field({
  id,
  label,
  hint,
  dirty,
  error,
  children,
}: {
  id: string;
  label: string;
  hint?: string;
  dirty?: boolean;
  error?: string;
  children: React.ReactNode;
}) {
  const hintId = `${id}-hint`;
  return (
    <div className="block">
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <label htmlFor={id} className="dense font-medium">
          {label}
        </label>
        {hint ? (
          <span id={hintId} className="text-xs text-muted-foreground">
            {hint}
          </span>
        ) : null}
        {dirty ? <DirtyMark /> : null}
      </div>
      <div className="mt-1.5">{children}</div>
      {error ? (
        <p role="alert" className="mt-1 text-xs leading-relaxed text-status-danger-text">
          {error}
        </p>
      ) : null}
    </div>
  );
}

/** DirtyMark 是「这一格改了但还没保存」。文字自己说话，不靠颜色（Signal-Only 的反面）。 */
export function DirtyMark() {
  return <span className="text-xs text-status-progress-text">未保存</span>;
}

type TextInputProps = React.ComponentProps<"input"> & { mono?: boolean };

export function TextInput({ className, mono, ...props }: TextInputProps) {
  return (
    <input
      {...props}
      className={cn(
        "h-8 w-full rounded-lg border border-input bg-background px-2.5 text-base outline-none sm:dense",
        "placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring",
        "disabled:cursor-not-allowed disabled:opacity-60",
        mono && "font-mono",
        className,
      )}
    />
  );
}

export function Toggle({
  id,
  label,
  desc,
  checked,
  dirty,
  onChange,
}: {
  id: string;
  label: string;
  desc?: string;
  checked: boolean;
  dirty?: boolean;
  onChange: (next: boolean) => void;
}) {
  const descId = `${id}-desc`;
  return (
    // 一行用发丝线分隔，不再套一个圆角边框的盒子：卡片里再出现「卡片」会被看成一层
    // 不存在的结构（检测器的 nested-cards 指的正是这类盒子）。
    <div className="flex items-start justify-between gap-4 border-t border-border/70 pt-3 first:border-t-0 first:pt-0">
      <div className="min-w-0">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <span id={`${id}-label`} className="dense font-medium">
            {label}
          </span>
          {dirty ? <DirtyMark /> : null}
        </div>
        {desc ? (
          <p id={descId} className="mt-0.5 max-w-md text-xs leading-relaxed text-muted-foreground">
            {desc}
          </p>
        ) : null}
      </div>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        aria-labelledby={`${id}-label`}
        aria-describedby={desc ? descId : undefined}
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
  | { kind: "ok"; detail?: string; durationMs?: number; at?: string }
  | { kind: "fail"; code: string; detail?: string; durationMs?: number; at?: string }
  | { kind: "placeholder"; detail?: string };

/**
 * TestChip 是「测试连接」旁边那个结论位。
 *
 * 三件事按重要顺序排：人话结论（哪一类没过）、多久以前、服务端的原话。原话以前只在
 * `title` 里——触屏没有 hover，读屏也不会念 title，等于那句「该改什么」从来没上过屏。
 * 现在原话单独一行展示；`title` 里留机器码与绝对时间，给排查的人。
 */
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
  const age = state.kind === "ok" || state.kind === "fail" ? state.at : undefined;
  const raw = state.kind === "fail" ? state.code : undefined;
  return (
    <span className="flex min-w-0 flex-col gap-1">
      <span
        className={cn(
          "inline-flex w-fit max-w-full items-center gap-1 rounded-md border px-1.5 py-0.5 text-xs",
          look.tone,
        )}
        title={
          [raw, state.detail, age ? formatAbsolute(age) : undefined].filter(Boolean).join(" · ") ||
          undefined
        }
      >
        {look.icon}
        <span className="truncate">
          {look.label}
          {age ? ` · ${formatWhen(age)}` : ""}
        </span>
      </span>
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
        label: `失败 · ${testCodeLabel(state.code)}${state.durationMs !== undefined ? ` · ${state.durationMs}ms` : ""}`,
        icon: <X className="size-3 shrink-0" aria-hidden />,
      };
  }
}

/**
 * StatusLine 是动作行里的那一行字：保存回执、未保存、错误。
 *
 * 与 Notice 的分工：Notice 是块级提示（外壳读不到数据那种），StatusLine 是「你刚按的这颗
 * 键怎么样了」。它必须活在按下去的地方旁边——顶部那条回执在长表单上永远是屏外的。
 */
export function StatusLine({
  tone,
  children,
}: {
  tone: "ok" | "warn" | "bad" | "muted";
  children: React.ReactNode;
}) {
  const look = {
    ok: "text-status-done-text",
    warn: "text-status-progress-text",
    bad: "text-status-danger-text",
    muted: "text-muted-foreground",
  }[tone];
  const alert = tone === "warn" || tone === "bad";
  return (
    <p
      role={alert ? "alert" : "status"}
      className={cn("flex items-start gap-1.5 text-xs leading-relaxed", look)}
    >
      {tone === "ok" ? <Check className="mt-0.5 size-3 shrink-0" aria-hidden /> : null}
      {alert ? <AlertTriangle className="mt-0.5 size-3 shrink-0" aria-hidden /> : null}
      <span className="min-w-0">{children}</span>
    </p>
  );
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
          // 图标只有 14px，热区用伪元素撑到 46：手机上这条提示旁边的关闭靶
          // 实测只有 14×14，比指尖小一圈。
          className="relative shrink-0 opacity-60 before:absolute before:-inset-4 before:content-[''] hover:opacity-100"
          aria-label="关闭提示"
        >
          <X className="size-3.5" aria-hidden />
        </button>
      ) : null}
    </div>
  );
}
