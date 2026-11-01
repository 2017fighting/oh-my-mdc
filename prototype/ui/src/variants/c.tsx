// PROTOTYPE ONLY — throwaway.
// Variant C「控制台 · 事件优先」: narrow icon rail, dark console skin, the event
// stream is the front page; the task queue is a dense mono table beside it; the
// wizard is one scroll of numbered blocks with a sticky progress header.
import * as React from 'react';
import {
  Activity,
  ChevronRight,
  CircleDot,
  ListTree,
  Pause,
  Play,
  Plug,
  RotateCcw,
  Settings2,
  Zap,
} from 'lucide-react';
import { Badge, Button, cn, Dot, Input, Label, SectionTitle, Switch, TestChip, useTest } from '../ui';
import { events, initialForm, loops, sideEffectLabel, sideEffects, tasks, type Task, type WizardForm } from '../data';

type Props = {
  view: 'wizard' | 'dashboard';
  fresh: boolean;
  onView: (v: 'wizard' | 'dashboard') => void;
};

export function VariantC({ view, fresh, onView }: Props) {
  const [paused, setPaused] = React.useState(false);
  const [retried, setRetried] = React.useState<string[]>([]);
  const [note, setNote] = React.useState<string | null>(null);
  const [selected, setSelected] = React.useState<Task | null>(null);
  const [form, setForm] = React.useState<WizardForm>(initialForm);
  const set = <K extends keyof WizardForm>(k: K, patch: Partial<WizardForm[K]>) =>
    setForm((f) => ({ ...f, [k]: { ...f[k], ...patch } }));

  const retry = (t: Task) => {
    setRetried((r) => [...r, t.movieId]);
    setSelected(null);
    setNote(`retry ${t.number} → processing (manual_retry 已记录)`);
  };

  return (
    <div className="flex min-h-screen bg-zinc-950 text-zinc-100">
      {/* rail */}
      <nav className="flex w-14 shrink-0 flex-col items-center gap-1 border-r border-zinc-800 py-3">
        <div className="mb-2 grid size-8 place-items-center rounded-md bg-zinc-100 text-[10px] font-bold text-zinc-900">
          mdc
        </div>
        <RailBtn icon={<Activity className="size-4" />} label="事件流" active={view === 'dashboard'} onClick={() => onView('dashboard')} disabled={fresh} />
        <RailBtn icon={<ListTree className="size-4" />} label="队列" active={false} onClick={() => onView('dashboard')} disabled={fresh} />
        <RailBtn icon={<Plug className="size-4" />} label="集成" active={view === 'wizard'} onClick={() => onView('wizard')} />
        <RailBtn icon={<Settings2 className="size-4" />} label="高级" active={false} onClick={() => onView('wizard')} />
        <div className="mt-auto flex flex-col items-center gap-2">
          <button
            onClick={() => {
              setPaused((p) => !p);
              setNote(paused ? '循环已恢复' : '循环已暂停');
            }}
            disabled={fresh}
            className="grid size-9 place-items-center rounded-md text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100 disabled:opacity-30"
            aria-label="暂停/恢复"
          >
            {paused ? <Play className="size-4" /> : <Pause className="size-4" />}
          </button>
        </div>
      </nav>

      <div className="flex min-w-0 flex-1 flex-col">
        {/* status bar */}
        <div className="flex h-9 shrink-0 items-center gap-4 border-b border-zinc-800 bg-zinc-900/60 px-4 font-mono text-[11px] text-zinc-400">
          <span className={cn('flex items-center gap-1.5', fresh ? 'text-amber-400' : 'text-emerald-400')}>
            <Dot tone={fresh ? 'warn' : paused ? 'warn' : 'ok'} />
            {fresh ? '未初始化' : paused ? '已暂停' : '已初始化'}
          </span>
          {!fresh &&
            loops.map((l) => (
              <span key={l.name}>
                {l.name} {l.interval}m/{l.last}
              </span>
            ))}
          {!fresh && <span>待办 {sideEffects.length}</span>}
          <span className="ml-auto">{note ?? '所有数据均为假数据 · 只用来挑结构'}</span>
        </div>

        <main className="flex-1 overflow-x-hidden p-4 text-[13px]">
          {fresh && view === 'dashboard' ? (
            <div className="mx-auto mt-16 max-w-md rounded-lg border border-zinc-800 p-6 text-center">
              <Zap className="mx-auto mb-3 size-5 text-amber-400" />
              <div className="text-sm font-medium">系统未初始化</div>
              <p className="mt-2 text-xs leading-relaxed text-zinc-400">
                主线三件套未配齐，循环不会启动。此页面只提供向导。
              </p>
              <Button
                className="mt-4"
                size="sm"
                onClick={() => onView('wizard')}
              >
                进入初始化 <ChevronRight className="size-3.5" />
              </Button>
            </div>
          ) : view === 'wizard' ? (
            <CWizard form={form} set={set} fresh={fresh} onFinish={() => { onView('dashboard'); setNote('初始化完成，循环启动'); }} />
          ) : (
            <CDashboard retried={retried} retry={retry} selected={selected} setSelected={setSelected} />
          )}
        </main>
      </div>
    </div>
  );
}

function RailBtn({
  icon,
  label,
  active,
  onClick,
  disabled,
}: {
  icon: React.ReactNode;
  label: string;
  active: boolean;
  onClick: () => void;
  disabled?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={label}
      aria-label={label}
      className={cn(
        'group relative grid size-9 place-items-center rounded-md',
        active ? 'bg-zinc-800 text-zinc-100' : 'text-zinc-500 hover:bg-zinc-900 hover:text-zinc-200',
        disabled && 'pointer-events-none opacity-30',
      )}
    >
      {icon}
      <span className="pointer-events-none absolute left-full z-50 ml-2 hidden whitespace-nowrap rounded bg-zinc-800 px-2 py-1 text-[11px] text-zinc-200 shadow group-hover:block">
        {label}
      </span>
    </button>
  );
}

/* ---------------------------------- wizard --------------------------------- */

const cBlocks = [
  { key: '01', title: 'javdb', required: true },
  { key: '02', title: 'qbittorrent', required: true },
  { key: '03', title: 'jellyfin', required: true },
  { key: '04', title: 'mteam', required: false },
  { key: '05', title: 'ai_translation', required: false },
  { key: '06', title: 'telegram', required: false },
] as const;

function CWizard({
  form,
  set,
  fresh,
  onFinish,
}: {
  form: WizardForm;
  set: <K extends keyof WizardForm>(k: K, patch: Partial<WizardForm[K]>) => void;
  fresh: boolean;
  onFinish: () => void;
}) {
  const t = useTest();
  const t2 = useTest();
  const t3 = useTest();
  const [ok, setOk] = React.useState<string[]>(['01', '02', '03']);

  const mark = (k: string) => setOk((o) => (o.includes(k) ? o : [...o, k]));

  return (
    <div className="mx-auto max-w-2xl">
      <div className="sticky top-0 z-20 -mx-4 mb-4 flex items-center gap-3 border-b border-zinc-800 bg-zinc-950/95 px-4 py-2.5 backdrop-blur">
        <span className="font-mono text-[13px] text-zinc-100">
          {fresh ? 'mdc init' : 'mdc config'}
        </span>
        <span className="font-mono text-[11px] text-zinc-500">
          三件套 {Math.min(ok.filter((k) => ['01', '02', '03'].includes(k)).length, 3)}/3 ·
          可选 {Math.max(ok.length - 3, 0)}/3
        </span>
        <div className="ml-2 h-1 flex-1 overflow-hidden rounded-full bg-zinc-800">
          <div
            className="h-full rounded-full bg-emerald-500 transition-all"
            style={{ width: `${(ok.length / 6) * 100}%` }}
          />
        </div>
        {fresh && (
          <Button size="sm" onClick={onFinish}>
            完成
          </Button>
        )}
      </div>

      <div className="space-y-3">
        <Block n="01" title="javdb" required done={ok.includes('01')} onRun={() => { t.run(); mark('01'); }} test={<TestChip state={t.state} />}>
          <MonoField label="token" hint="掩码显示，改写才会提交新值">
            <Input
              mono
              className="border-zinc-800 bg-zinc-900 text-zinc-100"
              value={form.javdb.token}
              onChange={(e) => set('javdb', { token: e.target.value })}
            />
          </MonoField>
          <MonoField label="清单登记" hint="认 id 不认名字">
            <div className="flex h-9 items-center gap-2 rounded-md border border-zinc-800 bg-zinc-900 px-3 font-mono text-xs text-zinc-300">
              want_watch → 想看 #{form.javdb.listId}
              <Badge tone="ok">已登记</Badge>
            </div>
          </MonoField>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" className="border-zinc-700 bg-transparent text-zinc-200 hover:bg-zinc-800" onClick={() => { t.run(); mark('01'); }} loading={t.state === 'testing'}>
              测试读取
            </Button>
            <Button variant="ghost" size="sm" className="text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100">
              新建清单
            </Button>
          </div>
        </Block>

        <Block n="02" title="qbittorrent" required done={ok.includes('02')} onRun={() => { t2.run(); mark('02'); }} test={<TestChip state={t2.state} />}>
          <div className="grid gap-3 sm:grid-cols-2">
            <MonoField label="url" className="sm:col-span-2">
              <Input mono className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.qbt.url} onChange={(e) => set('qbt', { url: e.target.value })} />
            </MonoField>
            <MonoField label="category">
              <Input className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.qbt.category} onChange={(e) => set('qbt', { category: e.target.value })} />
            </MonoField>
            <MonoField label="download_root">
              <Input mono className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.qbt.downloadRoot} onChange={(e) => set('qbt', { downloadRoot: e.target.value })} />
            </MonoField>
            <MonoField label="callback_base_url" hint="集群内 svc DNS" className="sm:col-span-2">
              <Input mono className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.qbt.baseUrl} onChange={(e) => set('qbt', { baseUrl: e.target.value })} />
            </MonoField>
          </div>
          <div className="rounded-md border border-zinc-800 bg-zinc-900/60 p-2.5 font-mono text-[11px] leading-relaxed text-zinc-400">
            <span className="text-zinc-500">$</span> autorun →{' '}
            <span className="text-emerald-400">sh -c "…moviepilot… ; curl …/api/hooks/qbt?ih=%I"</span>
            <div className="mt-1 text-[10px] text-zinc-500">幂等：已含本项目 URL 则跳过 · 保存前展示前后对比</div>
          </div>
        </Block>

        <Block n="03" title="jellyfin" required done={ok.includes('03')} onRun={() => { t3.run(); mark('03'); }} test={<TestChip state={t3.state} />}>
          <div className="grid gap-3 sm:grid-cols-2">
            <MonoField label="url">
              <Input mono className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.jellyfin.url} onChange={(e) => set('jellyfin', { url: e.target.value })} />
            </MonoField>
            <MonoField label="api_key" hint="掩码">
              <Input mono className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.jellyfin.apiKey} onChange={(e) => set('jellyfin', { apiKey: e.target.value })} />
            </MonoField>
            <MonoField label="media_root" hint="与 download_root 同路径可见，否则 EXDEV 失败" className="sm:col-span-2">
              <Input mono className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.jellyfin.mediaRoot} onChange={(e) => set('jellyfin', { mediaRoot: e.target.value })} />
            </MonoField>
          </div>
        </Block>

        <OptBlock n="04" title="mteam" desc="优先 M-Team 种子（无磁力，hash 本地算），失败降级磁力会产生第二条版本" enabled={form.mteam.enabled} onToggle={(v) => { set('mteam', { enabled: v }); if (v) mark('04'); }}>
          <MonoField label="x_api_key">
            <Input mono className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.mteam.apiKey} onChange={(e) => set('mteam', { apiKey: e.target.value })} />
          </MonoField>
        </OptBlock>

        <OptBlock n="05" title="ai_translation" desc="只翻标题；失败降级日文入库 + 告警" enabled={form.ai.enabled} onToggle={(v) => { set('ai', { enabled: v }); if (v) mark('05'); }}>
          <div className="grid gap-3 sm:grid-cols-3">
            <MonoField label="base_url" className="sm:col-span-2">
              <Input mono className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.ai.baseUrl} onChange={(e) => set('ai', { baseUrl: e.target.value })} />
            </MonoField>
            <MonoField label="model">
              <Input className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.ai.model} onChange={(e) => set('ai', { model: e.target.value })} />
            </MonoField>
            <MonoField label="api_key" className="sm:col-span-3">
              <Input mono className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.ai.apiKey} onChange={(e) => set('ai', { apiKey: e.target.value })} />
            </MonoField>
          </div>
        </OptBlock>

        <OptBlock n="06" title="telegram" desc="四个通知点，同一事件只发一次" enabled={form.telegram.enabled} onToggle={(v) => { set('telegram', { enabled: v }); if (v) mark('06'); }}>
          <div className="grid gap-3 sm:grid-cols-2">
            <MonoField label="bot_token">
              <Input mono className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.telegram.botToken} onChange={(e) => set('telegram', { botToken: e.target.value })} />
            </MonoField>
            <MonoField label="chat_id">
              <Input mono className="border-zinc-800 bg-zinc-900 text-zinc-100" value={form.telegram.chatId} onChange={(e) => set('telegram', { chatId: e.target.value })} />
            </MonoField>
          </div>
        </OptBlock>

        <div className="rounded-lg border border-zinc-800 p-3">
          <div className="mb-3 font-mono text-xs text-zinc-400">app_config · 循环与重试</div>
          <div className="grid gap-3 sm:grid-cols-4">
            {(
              [
                ['poll_min', 'poll', form.advanced.poll],
                ['reconcile_min', 'reconcile', form.advanced.reconcile],
                ['sidefx_min', 'sidefx', form.advanced.sidefx],
                ['max_attempts', 'maxAttempts', form.advanced.maxAttempts],
              ] as const
            ).map(([label, key, val]) => (
              <MonoField key={key} label={label}>
                <Input
                  className="border-zinc-800 bg-zinc-900 text-zinc-100"
                  value={val}
                  onChange={(e) => set('advanced', { [key]: e.target.value } as Partial<WizardForm['advanced']>)}
                />
              </MonoField>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

function Block({
  n,
  title,
  required,
  done,
  onRun,
  test,
  children,
}: {
  n: string;
  title: string;
  required?: boolean;
  done: boolean;
  onRun: () => void;
  test?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-lg border border-zinc-800">
      <header className="flex items-center gap-3 border-b border-zinc-800 px-3 py-2">
        <span className={cn('font-mono text-[11px]', done ? 'text-emerald-400' : 'text-zinc-500')}>{n}</span>
        <span className="font-mono text-[13px] text-zinc-100">{title}</span>
        {required ? <Badge tone="bad">必需</Badge> : <Badge tone="muted">可选</Badge>}
        <span className="ml-auto flex items-center gap-2">
          {test}
          <button
            onClick={onRun}
            className="rounded border border-zinc-700 px-2 py-1 font-mono text-[10px] text-zinc-300 hover:bg-zinc-800"
          >
            test
          </button>
          <button className="rounded border border-zinc-700 px-2 py-1 font-mono text-[10px] text-zinc-300 hover:bg-zinc-800">
            save
          </button>
        </span>
      </header>
      <div className="space-y-3 px-3 py-3">{children}</div>
    </section>
  );
}

function OptBlock({
  n,
  title,
  desc,
  enabled,
  onToggle,
  children,
}: {
  n: string;
  title: string;
  desc: string;
  enabled: boolean;
  onToggle: (v: boolean) => void;
  children: React.ReactNode;
}) {
  const [open, setOpen] = React.useState(enabled);
  return (
    <section className={cn('rounded-lg border border-zinc-800', !enabled && 'opacity-60')}>
      <header className="flex items-center gap-3 px-3 py-2">
        <span className="font-mono text-[11px] text-zinc-500">{n}</span>
        <span className="font-mono text-[13px] text-zinc-100">{title}</span>
        <Badge tone={enabled ? 'ok' : 'muted'}>{enabled ? 'on' : 'off'}</Badge>
        <Switch checked={enabled} onChange={(v) => { onToggle(v); setOpen(v); }} label={`${title} enabled`} />
        <button
          onClick={() => setOpen((o) => !o)}
          disabled={!enabled}
          className="ml-auto font-mono text-[10px] text-zinc-500 hover:text-zinc-200 disabled:pointer-events-none"
        >
          {open ? '[-] 收起' : '[+] 展开'}
        </button>
      </header>
      {enabled && open && (
        <>
          <div className="border-t border-zinc-800" />
          <div className="space-y-3 px-3 py-3">
            <p className="text-[11px] text-zinc-500">{desc}</p>
            {children}
          </div>
        </>
      )}
    </section>
  );
}

function MonoField({
  label,
  hint,
  children,
  className,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn('space-y-1.5', className)}>
      <div className="flex items-baseline justify-between gap-2">
        <Label className="font-mono text-[11px] text-zinc-400">{label}</Label>
        {hint && <span className="text-[10px] text-zinc-500">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

/* -------------------------------- dashboard -------------------------------- */

function CDashboard({
  retried,
  retry,
  selected,
  setSelected,
}: {
  retried: string[];
  retry: (t: Task) => void;
  selected: Task | null;
  setSelected: (t: Task | null) => void;
}) {
  const failed = tasks.filter((t) => t.versions.some((v) => v.detail.state === 'failed'));
  const state = (t: Task) => {
    if (t.versions.some((v) => v.detail.state === 'processing')) return 'processing';
    if (t.versions.some((v) => v.detail.state === 'downloading')) return 'downloading';
    if (t.versions.some((v) => v.detail.state === 'finished')) return 'finished';
    return 'failed';
  };

  return (
    <div className="space-y-4">
      {failed.length > 0 && (
        <div className="rounded-lg border border-red-900/60 bg-red-950/40 px-3 py-2.5">
          <div className="flex items-center gap-2 font-mono text-xs text-red-300">
            <CircleDot className="size-3.5" /> 需人工 · {failed.length}
          </div>
          <ul className="mt-2 space-y-1.5">
            {failed.map((t) => {
              const v = t.versions.find((x) => x.detail.state === 'failed')!;
              const d = v.detail as Extract<Task['versions'][number]['detail'], { state: 'failed' }>;
              return (
                <li key={t.movieId} className="flex items-center gap-3 text-xs text-zinc-300">
                  <span className="font-mono">{t.number}</span>
                  <span className="font-mono text-[10px] text-red-400">failed/{d.phase}</span>
                  <span className="min-w-0 flex-1 truncate text-zinc-400">{d.reason}</span>
                  {retried.includes(t.movieId) ? (
                    <span className="font-mono text-[10px] text-emerald-400">retry 已提交</span>
                  ) : (
                    <button
                      onClick={() => retry(t)}
                      className="flex items-center gap-1 rounded border border-zinc-700 px-2 py-0.5 font-mono text-[10px] text-zinc-200 hover:bg-zinc-800"
                    >
                      <RotateCcw className="size-3" /> retry
                    </button>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      )}

      <div className="grid gap-4 lg:grid-cols-[1fr_380px]">
        {/* feed */}
        <section className="rounded-lg border border-zinc-800">
          <header className="border-b border-zinc-800 px-3 py-2 font-mono text-xs text-zinc-400">
            task_event · append-only
          </header>
          <ul className="divide-y divide-zinc-800/70">
            {events.map((e, i) => (
              <li key={i} className="flex items-start gap-3 px-3 py-2 text-xs">
                <span className="w-14 shrink-0 font-mono text-[10px] text-zinc-500">{e.at}</span>
                <KindTag kind={e.kind} />
                <span className="flex-1 leading-relaxed text-zinc-300">{e.text}</span>
              </li>
            ))}
          </ul>
        </section>

        <div className="space-y-4">
          {/* queue */}
          <section className="rounded-lg border border-zinc-800">
            <header className="border-b border-zinc-800 px-3 py-2">
              <SectionTitle title="队列" desc="task_state 视图实时聚合" />
            </header>
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-zinc-800 font-mono text-[10px] text-zinc-500">
                  <th className="px-3 py-1.5 text-left font-normal">番号</th>
                  <th className="px-2 py-1.5 text-left font-normal">状态</th>
                  <th className="px-2 py-1.5 text-left font-normal">版本</th>
                  <th className="px-3 py-1.5 text-right font-normal" />
                </tr>
              </thead>
              <tbody>
                {tasks.map((t) => {
                  const s = state(t);
                  const tone =
                    s === 'finished'
                      ? 'text-emerald-400'
                      : s === 'processing'
                        ? 'text-amber-400'
                        : s === 'downloading'
                          ? 'text-sky-400'
                          : 'text-red-400';
                  return (
                    <tr
                      key={t.movieId}
                      onClick={() => setSelected(t)}
                      className="cursor-pointer border-b border-zinc-800/70 last:border-0 hover:bg-zinc-900"
                    >
                      <td className="px-3 py-2 font-mono text-zinc-100">{t.number}</td>
                      <td className={cn('px-2 py-2 font-mono text-[11px]', tone)}>{s}</td>
                      <td className="px-2 py-2 font-mono text-[10px] text-zinc-500">×{t.versions.length}</td>
                      <td className="px-3 py-2 text-right text-zinc-600">
                        <ChevronRight className="ml-auto size-3.5" />
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </section>

          {/* side effects */}
          <section className="rounded-lg border border-zinc-800">
            <header className="flex items-center justify-between border-b border-zinc-800 px-3 py-2">
              <SectionTitle title="副作用待办" desc="UNIQUE(kind,target) WHERE done_at IS NULL" />
              <Badge tone="warn">{sideEffects.length}</Badge>
            </header>
            <ul className="divide-y divide-zinc-800/70">
              {sideEffects.map((s, i) => (
                <li key={i} className="flex items-center gap-2 px-3 py-2 text-[11px]">
                  <span className="font-mono text-zinc-300">{s.kind}</span>
                  <span className="min-w-0 flex-1 truncate font-mono text-[10px] text-zinc-500">{s.target}</span>
                  <span className={cn('font-mono text-[10px]', s.lastError ? 'text-amber-400' : 'text-zinc-500')}>
                    {s.lastError ? `err ×${s.attempts}` : 'queued'}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        </div>
      </div>

      {selected && (
        <div className="fixed inset-0 z-40 flex justify-end" onClick={() => setSelected(null)}>
          <div className="absolute inset-0 bg-black/50" />
          <aside
            className="relative flex w-full max-w-md flex-col border-l border-zinc-800 bg-zinc-950 p-4 shadow-2xl"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-start justify-between">
              <div>
                <div className="font-mono text-sm font-semibold text-zinc-100">{selected.number}</div>
                <div className="mt-0.5 text-[11px] text-zinc-500">{selected.title}</div>
              </div>
              <button onClick={() => setSelected(null)} className="font-mono text-xs text-zinc-500 hover:text-zinc-200">
                esc
              </button>
            </div>
            <div className="mt-4 space-y-2">
              {selected.versions.map((v) => (
                <div key={v.infohash} className="rounded-md border border-zinc-800 p-2.5 text-[11px]">
                  <div className="flex items-center gap-2">
                    <span className="text-zinc-200">{v.label}</span>
                    <span className="font-mono text-[10px] text-zinc-500">{v.source}</span>
                  </div>
                  <div className="mt-1 font-mono text-[10px] text-zinc-600">{v.infohash}</div>
                  {v.detail.state === 'failed' && (
                    <p className="mt-1 text-red-400">{v.detail.reason}</p>
                  )}
                </div>
              ))}
            </div>
            {selected.note && (
              <p className="mt-3 rounded-md border border-zinc-800 bg-zinc-900/60 p-2.5 text-[11px] leading-relaxed text-zinc-400">
                {selected.note}
              </p>
            )}
            <div className="mt-auto space-y-2 pt-4">
              <Button size="sm" className="w-full bg-zinc-100 text-zinc-900 hover:bg-zinc-200" onClick={() => retry(selected)}>
                <RotateCcw className="size-3.5" /> 重试（回队列开头）
              </Button>
              <Button size="sm" variant="outline" className="w-full border-zinc-700 bg-transparent text-zinc-200 hover:bg-zinc-800">
                强制重刮（force=true）
              </Button>
            </div>
          </aside>
        </div>
      )}
    </div>
  );
}

function KindTag({ kind }: { kind: string }) {
  const map: Record<string, { label: string; cls: string }> = {
    state_change: { label: 'state', cls: 'border-sky-800 text-sky-400' },
    side_effect: { label: 'sidefx', cls: 'border-zinc-700 text-zinc-400' },
    alert: { label: 'alert', cls: 'border-red-900 text-red-400' },
    manual_retry: { label: 'manual', cls: 'border-amber-800 text-amber-400' },
  };
  const m = map[kind] ?? { label: kind, cls: 'border-zinc-700 text-zinc-400' };
  return (
    <span className={cn('shrink-0 rounded border px-1.5 py-0.5 font-mono text-[10px]', m.cls)}>{m.label}</span>
  );
}
