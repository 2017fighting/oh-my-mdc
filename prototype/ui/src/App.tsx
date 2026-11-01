// PROTOTYPE ONLY — throwaway. Variant switcher + URL state (?variant= &view= &state=).
// Question this prototype answers (issue #11): 向导和面板到底长什么样。
import * as React from 'react';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { cn, Kbd } from './ui';
import { VariantA } from './variants/a';
import { VariantB } from './variants/b';
import { VariantC } from './variants/c';

type VariantKey = 'a' | 'b' | 'c';
type View = 'wizard' | 'dashboard';

const variants: Array<{ key: VariantKey; name: string }> = [
  { key: 'a', name: 'A · 分步向导 + 表格面板' },
  { key: 'b', name: 'B · 单页卡片 + 看板' },
  { key: 'c', name: 'C · 控制台 · 事件优先' },
];

function readParams() {
  const p = new URLSearchParams(window.location.search);
  return {
    variant: (p.get('variant') ?? 'a') as VariantKey,
    view: (p.get('view') ?? 'wizard') as View,
    fresh: p.get('state') === 'fresh',
  };
}

export function App() {
  const [params, setParams] = React.useState(readParams);
  const { variant, view, fresh } = params;
  const valid = variants.some((v) => v.key === variant) ? variant : 'a';

  const update = (patch: Partial<typeof params>) => {
    const next = { ...params, ...patch };
    const p = new URLSearchParams();
    p.set('variant', next.variant);
    p.set('view', next.view);
    if (next.fresh) p.set('state', 'fresh');
    window.history.replaceState(null, '', `?${p.toString()}`);
    setParams(next);
  };

  // arrow keys cycle variants (not while typing)
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = document.activeElement;
      if (
        el instanceof HTMLElement &&
        (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable)
      )
        return;
      const i = variants.findIndex((v) => v.key === valid);
      if (e.key === 'ArrowLeft') {
        const prev = variants[(i - 1 + variants.length) % variants.length];
        update({ variant: prev.key });
      } else if (e.key === 'ArrowRight') {
        const nextV = variants[(i + 1) % variants.length];
        update({ variant: nextV.key });
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [valid, params]);

  const index = variants.findIndex((v) => v.key === valid);
  const View_ = valid === 'a' ? VariantA : valid === 'b' ? VariantB : VariantC;

  return (
    <>
      <View_ view={view} fresh={fresh} onView={(v) => update({ view: v })} />
      <Switcher
        current={valid}
        label={variants[index].name}
        onCycle={(dir) => {
          const i = variants.findIndex((v) => v.key === valid);
          update({ variant: variants[(i + dir + variants.length) % variants.length].key });
        }}
        view={view}
        onView={(v) => update({ view: v })}
        fresh={fresh}
        onFresh={(f) => update({ fresh: f, view: f ? 'wizard' : view })}
      />
    </>
  );
}

function Switcher({
  current,
  label,
  onCycle,
  view,
  onView,
  fresh,
  onFresh,
}: {
  current: VariantKey;
  label: string;
  onCycle: (dir: 1 | -1) => void;
  view: View;
  onView: (v: View) => void;
  fresh: boolean;
  onFresh: (f: boolean) => void;
}) {
  return (
    <div className="fixed bottom-4 left-1/2 z-50 -translate-x-1/2">
      <div className="flex items-center gap-1.5 rounded-full border bg-zinc-900/95 px-2 py-1.5 text-zinc-100 shadow-xl backdrop-blur">
        <button
          onClick={() => onCycle(-1)}
          className="grid size-7 place-items-center rounded-full hover:bg-zinc-800"
          aria-label="上一个变体"
        >
          <ChevronLeft className="size-4" />
        </button>
        <span className="min-w-[190px] px-1 text-center text-xs font-medium">{label}</span>
        <button
          onClick={() => onCycle(1)}
          className="grid size-7 place-items-center rounded-full hover:bg-zinc-800"
          aria-label="下一个变体"
        >
          <ChevronRight className="size-4" />
        </button>

        <span className="mx-1 h-4 w-px bg-zinc-700" />

        <SegBtn active={view === 'wizard'} onClick={() => onView('wizard')}>
          向导
        </SegBtn>
        <SegBtn active={view === 'dashboard'} onClick={() => onView('dashboard')}>
          面板
        </SegBtn>

        <span className="mx-1 h-4 w-px bg-zinc-700" />

        <SegBtn active={fresh} onClick={() => onFresh(!fresh)} title="模拟全新安装（未初始化，只有向导）">
          未初始化
        </SegBtn>

        <span className="ml-1 hidden items-center gap-1 pr-1 text-[10px] text-zinc-400 sm:flex">
          <Kbd>←</Kbd>
          <Kbd>→</Kbd>
          切换
        </span>
      </div>
    </div>
  );
}

function SegBtn({
  active,
  onClick,
  children,
  title,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
  title?: string;
}) {
  return (
    <button
      onClick={onClick}
      title={title}
      className={cn(
        'rounded-full px-2.5 py-1 text-[11px] font-medium transition-colors',
        active ? 'bg-zinc-100 text-zinc-900' : 'text-zinc-300 hover:bg-zinc-800',
      )}
    >
      {children}
    </button>
  );
}
