// PROTOTYPE ONLY — throwaway.
// Variant B「单页向导 + 看板面板」: no steps at all. Every integration is a card on
// one scrollable page, each card saved on its own (per-table PATCH); the dashboard
// is a kanban of task states with a centered detail dialog.
import * as React from 'react';
import {
  AlertTriangle,
  Bell,
  Check,
  ChevronDown,
  Languages,
  Pause,
  Play,
  RefreshCw,
  RotateCcw,
  Send,
  Server,
  Table2,
  Tv,
  Upload,
  X,
} from 'lucide-react';
import {
  Badge,
  Button,
  Card,
  cn,
  Dot,
  Field,
  Input,
  Notice,
  SectionTitle,
  Separator,
  Switch,
  TestChip,
  useTest,
} from '../ui';
import {
  autorunAfter,
  autorunBefore,
  events,
  initialForm,
  loops,
  sideEffectLabel,
  sideEffects,
  stateMeta,
  tasks,
  type Task,
  type WizardForm,
} from '../data';

type Props = {
  view: 'wizard' | 'dashboard';
  fresh: boolean;
  onView: (v: 'wizard' | 'dashboard') => void;
};

export function VariantB({ view, fresh, onView }: Props) {
  const [paused, setPaused] = React.useState(false);
  const [note, setNote] = React.useState<React.ReactNode>(null);
  const [selected, setSelected] = React.useState<Task | null>(null);
  const [retried, setRetried] = React.useState<string[]>([]);
  const [form, setForm] = React.useState<WizardForm>(initialForm);
  const set = <K extends keyof WizardForm>(k: K, patch: Partial<WizardForm[K]>) =>
    setForm((f) => ({ ...f, [k]: { ...f[k], ...patch } }));

  const retry = (t: Task) => {
    setRetried((r) => [...r, t.movieId]);
    setSelected(null);
    setNote(
      <>
        <span className="font-mono">{t.number}</span> 已回队列开头（manual_retry 已记录）
      </>,
    );
  };

  return (
    <div className="min-h-screen bg-muted/30">
      {/* top nav */}
      <header className="sticky top-0 z-30 border-b bg-background/85 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4">
          <div className="flex items-center gap-2">
            <div className="grid size-7 place-items-center rounded-md bg-primary text-[11px] font-bold text-primary-foreground">
              mdc
            </div>
            <span className="text-sm font-semibold">oh-my-mdc</span>
          </div>
          <nav className="flex items-center gap-1 text-[13px]">
            <TabBtn active={view === 'dashboard'} onClick={() => onView('dashboard')} disabled={fresh}>
              面板
            </TabBtn>
            <TabBtn active={view === 'wizard'} onClick={() => onView('wizard')}>
              {fresh ? '初始化' : '设置'}
            </TabBtn>
          </nav>
          <div className="ml-auto flex items-center gap-2">
            {fresh ? (
              <Badge tone="warn">
                <Dot tone="warn" /> 未初始化
              </Badge>
            ) : (
              <>
                <Badge tone="ok">
                  <Dot tone={paused ? 'warn' : 'ok'} /> {paused ? '已暂停' : '运行中'}
                </Badge>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => {
                    setPaused((p) => !p);
                    setNote(paused ? '循环已恢复' : '循环已暂停');
                  }}
                  aria-label="暂停/恢复"
                >
                  {paused ? <Play className="size-4" /> : <Pause className="size-4" />}
                </Button>
              </>
            )}
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-4 py-6">
        {note && (
          <div className="mb-4">
            <Notice text={note} onClose={() => setNote(null)} />
          </div>
        )}
        {fresh && view === 'dashboard' ? (
          <Card className="p-8 text-center">
            <h2 className="text-base font-semibold">先完成初始化</h2>
            <p className="mx-auto mt-2 max-w-md text-sm text-muted-foreground">
              三件套齐了循环才启动：JavDB 可读 + 清单登记、qBittorrent 可连、Jellyfin 可写。
            </p>
            <Button className="mt-4" onClick={() => onView('wizard')}>
              去初始化
            </Button>
          </Card>
        ) : view === 'wizard' ? (
          <BWizard form={form} set={set} fresh={fresh} onFinish={() => { onView('dashboard'); setNote('已初始化：循环启动'); }} />
        ) : (
          <BDashboard retried={retried} retry={retry} setSelected={setSelected} />
        )}
      </main>

      {selected && <Dialog task={selected} onClose={() => setSelected(null)} onRetry={retry} />}
    </div>
  );
}

function TabBtn({
  active,
  onClick,
  disabled,
  children,
}: {
  active: boolean;
  onClick: () => void;
  disabled?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={cn(
        'rounded-md px-3 py-1.5 transition-colors',
        active ? 'bg-accent font-medium' : 'text-muted-foreground hover:bg-accent/60',
        disabled && 'pointer-events-none opacity-40',
      )}
    >
      {children}
    </button>
  );
}

/* ---------------------------------- wizard --------------------------------- */

function BWizard({
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
  const tJavdb = useTest();
  const tQbt = useTest();
  const tJf = useTest();
  const [expanded, setExpanded] = React.useState<string | null>('javdb');
  const [savedCard, setSavedCard] = React.useState<string[]>([]);

  const coreDone = 3;

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      {/* sticky progress header */}
      <Card className="sticky top-16 z-20 flex items-center gap-3 px-4 py-3">
        <div className="flex -space-x-1">
          {['JavDB', 'qBt', 'JF'].map((n, i) => (
            <span
              key={n}
              className={cn(
                'grid size-6 place-items-center rounded-full border-2 border-card text-[9px] font-semibold',
                i < coreDone ? 'bg-emerald-100 text-emerald-700' : 'bg-muted text-muted-foreground',
              )}
              title={n}
            >
              {i < coreDone ? '✓' : n[0]}
            </span>
          ))}
        </div>
        <div className="min-w-0">
          <div className="text-[13px] font-medium">
            {fresh ? '初始化' : '设置'} · 主线三件套 {coreDone}/3
          </div>
          <div className="truncate text-[11px] text-muted-foreground">
            每张卡片独立保存；随时可以回来改，不打断正在跑的任务
          </div>
        </div>
        {fresh && (
          <Button size="sm" className="ml-auto" onClick={onFinish}>
            <Check className="size-3.5" /> 开始使用
          </Button>
        )}
      </Card>

      <IntegrationCard
        id="javdb"
        icon={<Server className="size-4" />}
        title="JavDB"
        required
        test={<TestChip state={tJavdb.state} label="2 分钟前成功" />}
        expanded={expanded === 'javdb'}
        onToggle={() => setExpanded(expanded === 'javdb' ? null : 'javdb')}
        saved={savedCard.includes('javdb')}
        onSave={() => setSavedCard((s) => [...s, 'javdb'])}
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Token" hint="只吐掩码" className="sm:col-span-2">
            <Input mono value={form.javdb.token} onChange={(e) => set('javdb', { token: e.target.value })} />
          </Field>
          <Field label="清单" hint="按 id 认，不认名字">
            <div className="flex h-9 items-center justify-between rounded-md border bg-muted/30 px-3 text-sm">
              <span>
                想看 <span className="font-mono text-xs text-muted-foreground">#{form.javdb.listId}</span>
              </span>
              <Badge tone="ok">已登记</Badge>
            </div>
          </Field>
          <div className="flex items-end gap-2 sm:col-span-2">
            <Button variant="outline" size="sm" onClick={() => tJavdb.run()} loading={tJavdb.state === 'testing'}>
              测试读取
            </Button>
            <Button variant="ghost" size="sm">
              改选清单
            </Button>
            <Button variant="ghost" size="sm">
              新建「想看」清单
            </Button>
          </div>
        </div>
      </IntegrationCard>

      <IntegrationCard
        id="qbt"
        icon={<Upload className="size-4" />}
        title="qBittorrent"
        required
        test={<TestChip state={tQbt.state} label="5 分钟前成功" />}
        expanded={expanded === 'qbt'}
        onToggle={() => setExpanded(expanded === 'qbt' ? null : 'qbt')}
        saved={savedCard.includes('qbt')}
        onSave={() => setSavedCard((s) => [...s, 'qbt'])}
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="地址" className="sm:col-span-2">
            <Input mono value={form.qbt.url} onChange={(e) => set('qbt', { url: e.target.value })} />
          </Field>
          <Field label="分类">
            <Input value={form.qbt.category} onChange={(e) => set('qbt', { category: e.target.value })} />
          </Field>
          <Field label="下载根目录">
            <Input mono value={form.qbt.downloadRoot} onChange={(e) => set('qbt', { downloadRoot: e.target.value })} />
          </Field>
          <Field label="回调 base URL" hint="集群内 svc DNS" className="sm:col-span-2">
            <Input mono value={form.qbt.baseUrl} onChange={(e) => set('qbt', { baseUrl: e.target.value })} />
          </Field>
          <div className="sm:col-span-2">
            <div className="rounded-lg border p-3">
              <div className="flex items-center justify-between text-[13px] font-medium">
                autorun 改写预览 <Badge tone="ok">已挂上</Badge>
              </div>
              <div className="mt-2 space-y-1 text-[11px]">
                <div className="font-mono break-all text-muted-foreground line-through decoration-red-300">{autorunBefore}</div>
                <div className="font-mono break-all text-emerald-800">{autorunAfter}</div>
              </div>
            </div>
          </div>
          <div className="flex items-center gap-2 sm:col-span-2">
            <Button variant="outline" size="sm" onClick={() => tQbt.run()} loading={tQbt.state === 'testing'}>
              测试连接
            </Button>
          </div>
        </div>
      </IntegrationCard>

      <IntegrationCard
        id="jellyfin"
        icon={<Tv className="size-4" />}
        title="Jellyfin"
        required
        test={<TestChip state={tJf.state} label="1 小时前成功" />}
        expanded={expanded === 'jellyfin'}
        onToggle={() => setExpanded(expanded === 'jellyfin' ? null : 'jellyfin')}
        saved={savedCard.includes('jellyfin')}
        onSave={() => setSavedCard((s) => [...s, 'jellyfin'])}
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="地址">
            <Input mono value={form.jellyfin.url} onChange={(e) => set('jellyfin', { url: e.target.value })} />
          </Field>
          <Field label="API Key">
            <Input mono value={form.jellyfin.apiKey} onChange={(e) => set('jellyfin', { apiKey: e.target.value })} />
          </Field>
          <Field label="媒体库目录" hint="与下载根目录同路径可见" className="sm:col-span-2">
            <Input mono value={form.jellyfin.mediaRoot} onChange={(e) => set('jellyfin', { mediaRoot: e.target.value })} />
          </Field>
          <div className="sm:col-span-2">
            <Button variant="outline" size="sm" onClick={() => tJf.run()} loading={tJf.state === 'testing'}>
              测试可写
            </Button>
          </div>
        </div>
      </IntegrationCard>

      <OptionalCard
        id="mteam"
        icon={<Table2 className="size-4" />}
        title="M-Team"
        desc="优先从 M-Team 取种（私有种子，无磁力）；不配就全走 JavDB 磁力"
        enabled={form.mteam.enabled}
        onToggle={(v) => set('mteam', { enabled: v })}
        expanded={expanded === 'mteam'}
        onExpand={() => setExpanded(expanded === 'mteam' ? null : 'mteam')}
        saved={savedCard.includes('mteam')}
        onSave={() => setSavedCard((s) => [...s, 'mteam'])}
      >
        <Field label="x-api-key" hint="限速 100 下载/小时">
          <Input mono value={form.mteam.apiKey} onChange={(e) => set('mteam', { apiKey: e.target.value })} />
        </Field>
      </OptionalCard>

      <OptionalCard
        id="ai"
        icon={<Languages className="size-4" />}
        title="AI 翻译"
        desc="只翻标题；失败降级日文入库并告警"
        enabled={form.ai.enabled}
        onToggle={(v) => set('ai', { enabled: v })}
        expanded={expanded === 'ai'}
        onExpand={() => setExpanded(expanded === 'ai' ? null : 'ai')}
        saved={savedCard.includes('ai')}
        onSave={() => setSavedCard((s) => [...s, 'ai'])}
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Base URL" className="sm:col-span-2">
            <Input mono value={form.ai.baseUrl} onChange={(e) => set('ai', { baseUrl: e.target.value })} />
          </Field>
          <Field label="API Key">
            <Input mono value={form.ai.apiKey} onChange={(e) => set('ai', { apiKey: e.target.value })} />
          </Field>
          <Field label="模型">
            <Input value={form.ai.model} onChange={(e) => set('ai', { model: e.target.value })} />
          </Field>
        </div>
      </OptionalCard>

      <OptionalCard
        id="telegram"
        icon={<Send className="size-4" />}
        title="Telegram"
        desc="下载开始 / 入库完成 / 加工失败 / 停滞告警，同一事件只发一次"
        enabled={form.telegram.enabled}
        onToggle={(v) => set('telegram', { enabled: v })}
        expanded={expanded === 'telegram'}
        onExpand={() => setExpanded(expanded === 'telegram' ? null : 'telegram')}
        saved={savedCard.includes('telegram')}
        onSave={() => setSavedCard((s) => [...s, 'telegram'])}
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Bot Token">
            <Input mono value={form.telegram.botToken} onChange={(e) => set('telegram', { botToken: e.target.value })} />
          </Field>
          <Field label="Chat ID">
            <Input mono value={form.telegram.chatId} onChange={(e) => set('telegram', { chatId: e.target.value })} />
          </Field>
        </div>
      </OptionalCard>

      <Card className="p-4">
        <SectionTitle title="循环与重试" desc="改完即时生效，无需重启" />
        <div className="mt-3 grid gap-3 sm:grid-cols-4">
          <Field label="清单轮询（分）">
            <Input value={form.advanced.poll} onChange={(e) => set('advanced', { poll: e.target.value })} />
          </Field>
          <Field label="对账（分）">
            <Input value={form.advanced.reconcile} onChange={(e) => set('advanced', { reconcile: e.target.value })} />
          </Field>
          <Field label="副作用（分）">
            <Input value={form.advanced.sidefx} onChange={(e) => set('advanced', { sidefx: e.target.value })} />
          </Field>
          <Field label="最大重试">
            <Input value={form.advanced.maxAttempts} onChange={(e) => set('advanced', { maxAttempts: e.target.value })} />
          </Field>
        </div>
      </Card>
    </div>
  );
}

function IntegrationCard({
  id,
  icon,
  title,
  required,
  test,
  expanded,
  onToggle,
  saved,
  onSave,
  children,
}: {
  id: string;
  icon: React.ReactNode;
  title: string;
  required?: boolean;
  test?: React.ReactNode;
  expanded: boolean;
  onToggle: () => void;
  saved: boolean;
  onSave: () => void;
  children: React.ReactNode;
}) {
  return (
    <Card className="overflow-hidden">
      <button onClick={onToggle} className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-muted/40">
        <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted">{icon}</span>
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium">
            {title}
            {required ? <Badge tone="solid">必需</Badge> : <Badge tone="muted">可选</Badge>}
            {saved && !expanded && <Badge tone="ok">已保存</Badge>}
          </div>
          <div className="text-[11px] text-muted-foreground">PATCH /api/v1/settings/{id}</div>
        </div>
        <div className="ml-auto flex items-center gap-2">
          {test}
          <ChevronDown className={cn('size-4 text-muted-foreground transition-transform', expanded && 'rotate-180')} />
        </div>
      </button>
      {expanded && (
        <div className="border-t px-4 pb-4 pt-4">
          {children}
          <div className="mt-4 flex items-center justify-end gap-2">
            <span className="mr-auto text-[10px] text-muted-foreground">revision: 7f3c9e · 冲突时提示刷新</span>
            <Button size="sm" variant="outline">
              测试连接
            </Button>
            <Button size="sm" onClick={onSave}>
              保存
            </Button>
          </div>
        </div>
      )}
    </Card>
  );
}

function OptionalCard({
  id,
  icon,
  title,
  desc,
  enabled,
  onToggle,
  expanded,
  onExpand,
  saved,
  onSave,
  children,
}: {
  id: string;
  icon: React.ReactNode;
  title: string;
  desc: string;
  enabled: boolean;
  onToggle: (v: boolean) => void;
  expanded: boolean;
  onExpand: () => void;
  saved: boolean;
  onSave: () => void;
  children: React.ReactNode;
}) {
  return (
    <Card className={cn('overflow-hidden transition-opacity', !enabled && 'opacity-70')}>
      <div className="flex items-center gap-3 px-4 py-3">
        <button onClick={onExpand} className="flex min-w-0 flex-1 items-center gap-3 text-left">
          <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted">{icon}</span>
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-sm font-medium">
              {title}
              <Badge tone={enabled ? 'ok' : 'muted'}>{enabled ? '已启用' : '已停用'}</Badge>
              {saved && <Badge tone="info">已保存</Badge>}
            </div>
            <div className="truncate text-[11px] text-muted-foreground">{desc}</div>
          </div>
        </button>
        <Switch checked={enabled} onChange={onToggle} label={`${title} 启用`} />
      </div>
      {enabled && expanded && (
        <div className="border-t px-4 pb-4 pt-4">
          {children}
          <div className="mt-4 flex items-center justify-end gap-2">
            <span className="mr-auto text-[10px] text-muted-foreground">PATCH /api/v1/settings/{id}</span>
            <Button size="sm" onClick={onSave}>
              保存
            </Button>
          </div>
        </div>
      )}
    </Card>
  );
}

/* -------------------------------- dashboard -------------------------------- */

const columns: Array<{ key: Task['versions'][number]['detail']['state']; title: string }> = [
  { key: 'downloading', title: '下载中' },
  { key: 'processing', title: '加工中' },
  { key: 'finished', title: '已入库' },
  { key: 'failed', title: '需人工' },
];

function taskState(t: Task): Task['versions'][number]['detail']['state'] {
  if (t.versions.some((v) => v.detail.state === 'processing')) return 'processing';
  if (t.versions.some((v) => v.detail.state === 'downloading')) return 'downloading';
  if (t.versions.some((v) => v.detail.state === 'finished')) return 'finished';
  return 'failed';
}

function BDashboard({
  retried,
  retry,
  setSelected,
}: {
  retried: string[];
  retry: (t: Task) => void;
  setSelected: (t: Task | null) => void;
}) {
  return (
    <div className="space-y-4">
      {/* status strip */}
      <Card className="flex flex-wrap items-center gap-x-5 gap-y-2 px-4 py-3 text-xs">
        <span className="flex items-center gap-1.5 font-medium">
          <Dot tone="ok" /> 循环 3/3
        </span>
        {loops.map((l) => (
          <span key={l.name} className="text-muted-foreground">
            {l.name} {l.interval}m · {l.last}
          </span>
        ))}
        <span className="ml-auto flex items-center gap-2 text-muted-foreground">
          <Bell className="size-3.5" />
          副作用待办 {sideEffects.length} 条
          <Button variant="ghost" size="sm">
            查看
          </Button>
        </span>
      </Card>

      {/* kanban */}
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        {columns.map((col) => {
          const colTasks = tasks.filter((t) => taskState(t) === col.key);
          const meta = stateMeta[col.key];
          return (
            <div key={col.key} className="min-w-0 space-y-2">
              <div className="flex items-center gap-2 px-1 text-xs font-medium">
                <Dot tone={meta.dot} />
                {col.title}
                <span className="tabular-nums text-muted-foreground">{colTasks.length}</span>
              </div>
              <div className="space-y-2">
                {colTasks.map((t) => {
                  const failV = t.versions.find((v) => v.detail.state === 'failed');
                  const d = failV?.detail as Extract<Task['versions'][number]['detail'], { state: 'failed' }> | undefined;
                  return (
                    <Card
                      key={t.movieId}
                      className="cursor-pointer p-3 transition-shadow hover:shadow-md"
                      onClick={() => setSelected(t)}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="font-mono text-[13px] font-semibold">{t.number}</span>
                        <span className="text-[10px] text-muted-foreground">{t.updatedAt}</span>
                      </div>
                      <p className="mt-0.5 line-clamp-1 text-[11px] text-muted-foreground">{t.title}</p>
                      <div className="mt-2 flex flex-wrap items-center gap-1">
                        {t.versions.map((v) => (
                          <Badge key={v.infohash} tone={v.source === 'mteam_torrent' ? 'info' : 'muted'}>
                            {v.label}
                          </Badge>
                        ))}
                        {t.versions.length > 1 && (
                          <span className="text-[10px] text-muted-foreground">{t.versions.length} 版本</span>
                        )}
                      </div>
                      {d && (
                        <div className="mt-2 rounded-md bg-red-50 px-2 py-1.5 text-[11px] leading-snug text-red-800">
                          {d.reason}
                        </div>
                      )}
                      {d && retried.includes(t.movieId) && (
                        <div className="mt-1.5">
                          <Badge tone="info">重试已提交</Badge>
                        </div>
                      )}
                    </Card>
                  );
                })}
                {colTasks.length === 0 && (
                  <div className="rounded-xl border border-dashed px-3 py-6 text-center text-[11px] text-muted-foreground">
                    空
                  </div>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {/* side effects + events */}
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <div className="flex items-center justify-between border-b px-4 py-2.5">
            <SectionTitle title="副作用队列" />
            <Button variant="ghost" size="sm">
              <RefreshCw className="size-3.5" /> 重试全部
            </Button>
          </div>
          <ul className="divide-y text-xs">
            {sideEffects.map((s, i) => (
              <li key={i} className="flex items-center gap-3 px-4 py-2.5">
                <Badge tone={s.lastError ? 'warn' : 'muted'}>{sideEffectLabel[s.kind]}</Badge>
                <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">{s.target}</span>
                <span className="text-[11px] text-muted-foreground">{s.lastError ?? s.nextAttempt}</span>
              </li>
            ))}
          </ul>
        </Card>
        <Card>
          <div className="border-b px-4 py-2.5">
            <SectionTitle title="最近事件" desc="append-only" />
          </div>
          <ul className="divide-y text-xs">
            {events.slice(0, 5).map((e, i) => (
              <li key={i} className="flex items-start gap-3 px-4 py-2">
                <span className="w-16 shrink-0 text-muted-foreground">{e.at}</span>
                <span className="flex-1 leading-relaxed">{e.text}</span>
                {e.kind === 'alert' && <AlertTriangle className="size-3.5 shrink-0 text-red-500" />}
              </li>
            ))}
          </ul>
        </Card>
      </div>
    </div>
  );
}

function Dialog({
  task,
  onClose,
  onRetry,
}: {
  task: Task;
  onClose: () => void;
  onRetry: (t: Task) => void;
}) {
  return (
    <div className="fixed inset-0 z-40 grid place-items-center p-4">
      <div className="absolute inset-0 bg-black/30" onClick={onClose} />
      <Card className="relative w-full max-w-lg shadow-2xl">
        <div className="flex items-start justify-between border-b px-5 py-4">
          <div>
            <div className="font-mono text-base font-semibold">{task.number}</div>
            <div className="text-xs text-muted-foreground">{task.title}</div>
          </div>
          <Button variant="ghost" size="icon" onClick={onClose}>
            <X className="size-4" />
          </Button>
        </div>
        <div className="max-h-[60vh] space-y-4 overflow-y-auto px-5 py-4">
          <div className="space-y-2">
            {task.versions.map((v) => (
              <div key={v.infohash} className="rounded-lg border p-2.5 text-xs">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{v.label}</span>
                  <Badge tone={v.source === 'mteam_torrent' ? 'info' : 'muted'}>
                    {v.source === 'mteam_torrent' ? 'M-Team' : 'JavDB'}
                  </Badge>
                  <span className="ml-auto text-[10px] text-muted-foreground">{v.infohash.slice(0, 12)}…</span>
                </div>
                {v.detail.state === 'failed' && (
                  <p className="mt-1 text-[11px] text-red-700">{v.detail.reason}</p>
                )}
                {v.detail.state === 'processing' && (
                  <p className="mt-1 text-[11px] text-muted-foreground">
                    {v.detail.phase === 'scrape' ? '刮削中' : '整理中：硬链接 + NFO + 图片'}
                  </p>
                )}
              </div>
            ))}
          </div>
          {task.note && <Notice text={task.note} />}
          <div>
            <div className="mb-2 text-[11px] font-medium text-muted-foreground">时间线</div>
            <ol className="space-y-2 border-l pl-3 text-[11px] leading-relaxed">
              {events.slice(0, 4).map((e, i) => (
                <li key={i} className="relative">
                  <span className="absolute -left-[15px] top-1.5 size-1.5 rounded-full bg-border" />
                  <span className="text-muted-foreground">{e.at} · </span>
                  {e.text}
                </li>
              ))}
            </ol>
          </div>
        </div>
        <div className="flex items-center gap-2 border-t px-5 py-3">
          <Button size="sm" onClick={() => onRetry(task)}>
            <RotateCcw className="size-3.5" /> 重试
          </Button>
          <Button size="sm" variant="outline">
            强制重刮
          </Button>
          <span className="ml-auto text-[10px] text-muted-foreground">输入框外按 ← → 切换原型</span>
        </div>
      </Card>
    </div>
  );
}
