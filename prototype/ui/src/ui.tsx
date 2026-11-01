// PROTOTYPE ONLY — minimal shadcn-shaped primitives. Throwaway, no a11y work.
import * as React from 'react';
import { Check, Loader2, X } from 'lucide-react';

export const cn = (...parts: Array<string | false | null | undefined>) =>
  parts.filter(Boolean).join(' ');

/* ---------------------------------- button --------------------------------- */

type ButtonVariant = 'default' | 'outline' | 'ghost' | 'secondary' | 'destructive';
type ButtonSize = 'sm' | 'md' | 'icon';

export function Button({
  className,
  variant = 'default',
  size = 'md',
  loading,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
}) {
  const variants: Record<ButtonVariant, string> = {
    default: 'bg-primary text-primary-foreground hover:bg-primary/90',
    outline: 'border bg-background hover:bg-accent hover:text-accent-foreground',
    ghost: 'hover:bg-accent hover:text-accent-foreground',
    secondary: 'bg-secondary text-secondary-foreground hover:bg-secondary/70',
    destructive: 'bg-destructive text-destructive-foreground hover:bg-destructive/90',
  };
  const sizes: Record<ButtonSize, string> = {
    sm: 'h-8 px-3 text-xs gap-1.5',
    md: 'h-9 px-4 text-sm gap-2',
    icon: 'h-8 w-8',
  };
  return (
    <button
      className={cn(
        'inline-flex items-center justify-center rounded-md font-medium transition-colors',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:ring-offset-1',
        'disabled:pointer-events-none disabled:opacity-50',
        variants[variant],
        sizes[size],
        className,
      )}
      {...props}
    >
      {loading && <Loader2 className="size-3.5 animate-spin" />}
      {props.children}
    </button>
  );
}

/* ---------------------------------- inputs --------------------------------- */

export function Input({
  className,
  mono,
  ...props
}: React.InputHTMLAttributes<HTMLInputElement> & { mono?: boolean }) {
  return (
    <input
      className={cn(
        'h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-xs',
        'placeholder:text-muted-foreground/70',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:border-ring/60',
        'disabled:cursor-not-allowed disabled:opacity-60',
        mono && 'font-mono text-[13px]',
        className,
      )}
      {...props}
    />
  );
}

export function Label({ className, ...props }: React.LabelHTMLAttributes<HTMLLabelElement>) {
  return (
    <label
      className={cn('text-[13px] font-medium leading-none text-foreground/90', className)}
      {...props}
    />
  );
}

export function Field({
  label,
  hint,
  children,
  className,
}: {
  label: string;
  hint?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn('space-y-1.5', className)}>
      <div className="flex items-baseline justify-between gap-3">
        <Label>{label}</Label>
        {hint && <span className="text-[11px] text-muted-foreground">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

export function Switch({
  checked,
  onChange,
  label,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label?: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      className={cn(
        'relative h-5 w-9 shrink-0 rounded-full border transition-colors',
        checked ? 'border-primary bg-primary' : 'border-input bg-muted',
      )}
      aria-label={label}
    >
      <span
        className={cn(
          'absolute top-0.5 size-3.5 rounded-full bg-background shadow transition-all',
          checked ? 'left-[18px]' : 'left-0.5',
        )}
      />
    </button>
  );
}

/* ---------------------------------- chrome --------------------------------- */

export function Card({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('rounded-xl border bg-card text-card-foreground shadow-xs', className)}
      {...props}
    />
  );
}

export function Badge({
  tone = 'muted',
  className,
  children,
}: {
  tone?: 'muted' | 'ok' | 'warn' | 'bad' | 'info' | 'solid';
  className?: string;
  children: React.ReactNode;
}) {
  const tones = {
    muted: 'bg-muted text-muted-foreground border-transparent',
    ok: 'bg-emerald-50 text-emerald-700 border-emerald-200',
    warn: 'bg-amber-50 text-amber-700 border-amber-200',
    bad: 'bg-red-50 text-red-700 border-red-200',
    info: 'bg-sky-50 text-sky-700 border-sky-200',
    solid: 'bg-primary text-primary-foreground border-transparent',
  } as const;
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium leading-4 whitespace-nowrap',
        tones[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}

export function Dot({ tone = 'muted' }: { tone?: 'ok' | 'warn' | 'bad' | 'muted' | 'busy' }) {
  const tones = {
    ok: 'bg-emerald-500',
    warn: 'bg-amber-500',
    bad: 'bg-red-500',
    muted: 'bg-zinc-300',
    busy: 'bg-sky-500 animate-pulse',
  } as const;
  return <span className={cn('inline-block size-1.5 shrink-0 rounded-full', tones[tone])} />;
}

export function Separator({ className }: { className?: string }) {
  return <div className={cn('h-px w-full bg-border', className)} />;
}

/* -------------------------------- test state ------------------------------- */

export type TestState = 'idle' | 'testing' | 'ok' | 'fail';

export function TestChip({ state, label }: { state: TestState; label?: string }) {
  if (state === 'testing')
    return (
      <Badge tone="info">
        <Loader2 className="size-3 animate-spin" /> 测试中…
      </Badge>
    );
  if (state === 'ok')
    return (
      <Badge tone="ok">
        <Check className="size-3" /> 连接成功
      </Badge>
    );
  if (state === 'fail')
    return (
      <Badge tone="bad">
        <X className="size-3" /> 测试失败
      </Badge>
    );
  return <Badge tone="muted">{label ?? '未测试'}</Badge>;
}

export function useTest(ms = 900) {
  const [state, setState] = React.useState<TestState>('idle');
  const run = React.useCallback(
    (outcome: 'ok' | 'fail' = 'ok') => {
      setState('testing');
      window.setTimeout(() => setState(outcome), ms);
    },
    [ms],
  );
  return { state, run };
}

/* --------------------------------- notice ---------------------------------- */

export function Notice({
  text,
  tone = 'info',
  onClose,
}: {
  text: React.ReactNode;
  tone?: 'info' | 'ok' | 'bad';
  onClose?: () => void;
}) {
  const tones = {
    info: 'border-sky-200 bg-sky-50 text-sky-900',
    ok: 'border-emerald-200 bg-emerald-50 text-emerald-900',
    bad: 'border-red-200 bg-red-50 text-red-900',
  } as const;
  return (
    <div
      className={cn(
        'flex items-start justify-between gap-3 rounded-lg border px-3 py-2 text-xs leading-relaxed',
        tones[tone],
      )}
    >
      <span>{text}</span>
      {onClose && (
        <button onClick={onClose} className="opacity-50 hover:opacity-100" aria-label="关闭">
          <X className="size-3.5" />
        </button>
      )}
    </div>
  );
}

/* ---------------------------------- misc ----------------------------------- */

export function Kbd({ children }: { children: React.ReactNode }) {
  return (
    <kbd className="rounded border bg-muted px-1 py-0.5 font-mono text-[10px] text-muted-foreground">
      {children}
    </kbd>
  );
}

export function SectionTitle({
  title,
  desc,
  right,
}: {
  title: string;
  desc?: string;
  right?: React.ReactNode;
}) {
  return (
    <div className="flex items-end justify-between gap-4">
      <div>
        <h2 className="text-[15px] font-semibold tracking-tight">{title}</h2>
        {desc && <p className="mt-0.5 text-xs text-muted-foreground">{desc}</p>}
      </div>
      {right}
    </div>
  );
}
