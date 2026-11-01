// PROTOTYPE ONLY — throwaway.
// Variant A「分步向导 + 表格面板」: classic admin console. One wizard step on screen
// at a time behind a left stepper; the dashboard is a dense task table plus a detail
// drawer. Re-configuring later = the same stepper, opened in 「集成」 edit mode.
import * as React from 'react';
import {
  AlertTriangle,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  LayoutDashboard,
  ListChecks,
  Pause,
  Play,
  Plug,
  RefreshCw,
  RotateCcw,
  ServerCog,
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
  wizardSteps,
  type Task,
  type WizardForm,
} from '../data';

type Props = { view: 'wizard' | 'dashboard'; fresh: boolean; onView: (v: 'wizard' | 'dashboard') => void };

export function VariantA({ view, fresh, onView }: Props) {
  const [step, setStep] = React.useState(0);
  const [form, setForm] = React.useState<WizardForm>(initialForm);
  const [formOn, setFormOn] = React.useState(true);
  const [paused, setPaused] = React.useState(false);
  const [note, setNote] = React.useState<React.ReactNode>(null);
  const [selected, setSelected] = React.useState<Task | null>(null);
  const [retried, setRetried] = React.useState<string[]>([]);

  const set = <K extends keyof WizardForm>(k: K, patch: Partial<WizardForm[K]>) =>
    setForm((f) => ({ ...f, [k]: { ...f[k], ...patch } }));

  const failed = tasks.filter((t) =>
    t.versions.some((v) => v.detail.state === 'failed'),
  );
  const retry = (t: Task) => {
    setRetried((r) => [...r, t.movieId]);
    setNote(
      <>
        已提交人工重试：<span className="font-mono">{t.number}</span> → processing（task_event 记一条
        manual_retry）
      </>,
    );
  };

  return (
    <div className="flex min-h-screen bg-background">
      {/* sidebar */}
      <aside className="hidden w-60 shrink-0 flex-col justify-between border-r bg-muted/30 px-3 py-4 lg:flex">
        <div className="space-y-6">
          <div className="flex items-center gap-2 px-2">
            <div className="grid size-7 place-items-center rounded-md bg-primary text-[11px] font-bold text-primary-foreground">
              mdc
            </div>
            <div className="leading-tight">
              <div className="text-sm font-semibold">oh-my-mdc</div>
              <div className="text-[10px] text-muted-foreground">自托管 · 单用户</div>
            </div>
          </div>
          <nav className="space-y-0.5 text-[13px]">
            <NavItem
              icon={<LayoutDashboard className="size-4" />}
              label="面板"
              active={view === 'dashboard'}
              onClick={() => onView('dashboard')}
              disabled={fresh}
            />
            <NavItem
              icon={<ListChecks className="size-4" />}
              label="任务"
              badge={String(tasks.length)}
              active={false}
              onClick={() => onView('dashboard')}
              disabled={fresh}
            />
            <NavItem
              icon={<Plug className="size-4" />}
              label="集成设置"
              active={view === 'wizard'}
              onClick={() => {
                setFormOn(true);
                onView('wizard');
              }}
            />
            <NavItem
              icon={<ServerCog className="size-4" />}
              label="高级"
              active={false}
              onClick={() => {
                setStep(6);
                onView('wizard');
              }}
            />
          </nav>
        </div>
        <div className="space-y-2 px-2 text-[11px] text-muted-foreground">
          <Separator />
          <div className="flex items-center justify-between">
            <span>循环</span>
            <span className="flex items-center gap-1.5">
              <Dot tone={fresh ? 'muted' : paused ? 'warn' : 'ok'} />
              {fresh ? '未启动' : paused ? '已暂停' : '运行中'}
            </span>
          </div>
          <p className="leading-relaxed">
            v0.1.0-prototype · 所有数字都是假数据，只用来挑结构
          </p>
        </div>
      </aside>

      {/* main */}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center justify-between border-b px-5">
          <div className="flex items-center gap-2 text-[13px] text-muted-foreground">
            <span>oh-my-mdc</span>
            <ChevronRight className="size-3.5" />
            <span className="font-medium text-foreground">
              {view === 'wizard' ? '集成设置' : '面板'}
            </span>
          </div>
          <div className="flex items-center gap-2">
            <Badge tone={fresh ? 'warn' : 'ok'}>
              <Dot tone={fresh ? 'warn' : 'ok'} />
              {fresh ? '未初始化' : '已初始化'}
            </Badge>
            <Button
              variant="outline"
              size="sm"
              disabled={fresh}
              onClick={() => {
                setPaused((p) => !p);
                setNote(paused ? '循环已恢复' : '循环已暂停：正在跑的版本会跑完，不接新的');
              }}
            >
              {paused ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}
              {paused ? '恢复' : '暂停'}
            </Button>
          </div>
        </header>

        <main className="flex-1 px-5 py-5">
          {note && (
            <div className="mb-4">
              <Notice text={note} onClose={() => setNote(null)} />
            </div>
          )}

          {fresh && view === 'dashboard' ? (
            <FreshGate onStart={() => onView('wizard')} />
          ) : view === 'wizard' ? (
            <Wizard
              step={step}
              setStep={setStep}
              form={form}
              set={set}
              formOn={formOn}
              fresh={fresh}
              onDone={() => {
                onView('dashboard');
                setNote('已初始化：三个循环开始运行（清单轮询 15m / 对账 5m / 副作用 1m）');
              }}
            />
          ) : (
            <Dashboard
              failed={failed}
              retried={retried}
              retry={retry}
              selected={selected}
              setSelected={setSelected}
            />
          )}
        </main>
      </div>

      {selected && <Drawer task={selected} onClose={() => setSelected(null)} onRetry={retry} />}
    </div>
  );
}

/* --------------------------------- pieces ---------------------------------- */

function NavItem({
  icon,
  label,
  active,
  badge,
  onClick,
  disabled,
}: {
  icon: React.ReactNode;
  label: string;
  active: boolean;
  badge?: string;
  onClick: () => void;
  disabled?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={cn(
        'flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors',
        active ? 'bg-background font-medium shadow-xs' : 'text-muted-foreground hover:bg-background/60',
        disabled && 'pointer-events-none opacity-40',
      )}
    >
      {icon}
      <span className="flex-1">{label}</span>
      {badge && <span className="text-[10px] text-muted-foreground">{badge}</span>}
    </button>
  );
}

function FreshGate({ onStart }: { onStart: () => void }) {
  return (
    <Card className="mx-auto mt-10 max-w-lg p-8 text-center">
      <div className="mx-auto mb-4 grid size-11 place-items-center rounded-full bg-muted">
        <CircleHelp className="size-5 text-muted-foreground" />
      </div>
      <h2 className="text-base font-semibold">还没初始化</h2>
      <p className="mx-auto mt-2 max-w-sm text-sm leading-relaxed text-muted-foreground">
        主线三件套（JavDB 可读且清单登记就位、qBittorrent 可连、Jellyfin 可写）齐了，循环才会启动。
        在那之前这个页面只有向导，面板是空的。
      </p>
      <Button className="mt-5" onClick={onStart}>
        打开向导
      </Button>
    </Card>
  );
}

function Stat({ label, value, tone, hint }: { label: string; value: string; tone?: string; hint?: string }) {
  return (
    <Card className="px-4 py-3">
      <div className="text-[11px] text-muted-foreground">{label}</div>
      <div className={cn('mt-1 text-2xl font-semibold tabular-nums tracking-tight', tone)}>{value}</div>
      {hint && <div className="mt-0.5 text-[11px] text-muted-foreground">{hint}</div>}
    </Card>
  );
}

/* --------------------------------- wizard ---------------------------------- */

function Wizard({
  step,
  setStep,
  form,
  set,
  formOn,
  fresh,
  onDone,
}: {
  step: number;
  setStep: (n: number) => void;
  form: WizardForm;
  set: <K extends keyof WizardForm>(k: K, patch: Partial<WizardForm[K]>) => void;
  formOn: boolean;
  fresh: boolean;
  onDone: () => void;
}) {
  const current = wizardSteps[step];
  const [saved, setSaved] = React.useState<React.ReactNode>(null);
  const tJavdb = useTest();
  const tQbt = useTest();
  const tJelly = useTest();
  const tMteam = useTest();
  const tAi = useTest();
  const tTg = useTest();

  return (
    <div className="mx-auto max-w-5xl">
      <div className="mb-5">
        <SectionTitle
          title={fresh ? '初始化向导' : '集成设置'}
          desc={
            fresh
              ? '按表提交：每一步只 PATCH 这一张集成表，敏感字段省略＝保持原值'
              : '随时可回来改。改动不会打断正在跑的任务'
          }
          right={
            <Badge tone="muted">
              第 {step + 1} / {wizardSteps.length} 步
            </Badge>
          }
        />
      </div>

      <div className="grid gap-6 lg:grid-cols-[230px_1fr]">
        <ol className="space-y-1">
          {wizardSteps.map((s, i) => {
            const done = i < step;
            return (
              <li key={s.key}>
                <button
                  onClick={() => setStep(i)}
                  className={cn(
                    'flex w-full items-start gap-2.5 rounded-md px-2 py-2 text-left text-[13px] transition-colors',
                    i === step ? 'bg-accent font-medium' : 'text-muted-foreground hover:bg-accent/50',
                  )}
                >
                  <span
                    className={cn(
                      'mt-0.5 grid size-5 shrink-0 place-items-center rounded-full border text-[10px] tabular-nums',
                      done && 'border-emerald-200 bg-emerald-50 text-emerald-700',
                      i === step && !done && 'border-primary bg-primary text-primary-foreground',
                    )}
                  >
                    {done ? '✓' : i + 1}
                  </span>
                  <span className="leading-snug">
                    {s.title}
                    {!s.required && <span className="ml-1 text-[10px] opacity-60">可跳过</span>}
                  </span>
                </button>
              </li>
            );
          })}
        </ol>

        <div className="space-y-4">
          {saved && (
            <Notice
              tone="ok"
              text={saved}
              onClose={() => setSaved(null)}
            />
          )}

          <Card className="p-5">
            <div className="mb-4">
              <h3 className="text-sm font-semibold">{current.title}</h3>
              <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                {stepHelp(step)}
              </p>
            </div>
            <Separator className="mb-4" />

            <div className="space-y-4">
              {step === 0 && (
                <>
                  <Field label="JavDB Token" hint="Bearer，非过期 JWT · 只吐掩码">
                    <Input
                      mono
                      value={form.javdb.token}
                      onChange={(e) => set('javdb', { token: e.target.value })}
                    />
                  </Field>
                  <div className="flex items-center gap-2">
                    <Button variant="outline" size="sm" onClick={() => tJavdb.run()} loading={tJavdb.state === 'testing'}>
                      测试读取
                    </Button>
                    <TestChip state={tJavdb.state} />
                    <span className="text-[11px] text-muted-foreground">
                      GET /api/v1/lists?page=1&amp;limit=10&amp;sort_by=created
                    </span>
                  </div>
                  <Separator />
                  <div className="space-y-2">
                    <div className="text-[13px] font-medium">清单登记</div>
                    <p className="text-[11px] text-muted-foreground">
                      按 id 认清单，名字只是快照。同名清单已存在就跳过创建。
                    </p>
                    <div className="flex gap-2">
                      <Button
                        variant={form.javdb.listMode === 'existing' ? 'secondary' : 'outline'}
                        size="sm"
                        onClick={() => set('javdb', { listMode: 'existing' })}
                      >
                        读取已有清单
                      </Button>
                      <Button
                        variant={form.javdb.listMode === 'create' ? 'secondary' : 'outline'}
                        size="sm"
                        onClick={() => set('javdb', { listMode: 'create' })}
                      >
                        一键创建「想看」
                      </Button>
                    </div>
                    {form.javdb.listMode === 'existing' ? (
                      <div className="rounded-lg border bg-muted/30 p-3 text-xs">
                        <div className="flex items-center justify-between">
                          <span>
                            想看 <span className="font-mono text-muted-foreground">#{form.javdb.listId}</span>
                          </span>
                          <Badge tone="ok">已登记</Badge>
                        </div>
                        <div className="mt-1 text-[11px] text-muted-foreground">
                          用途 want_watch · 匿名可读（写侧仍需 token）
                        </div>
                      </div>
                    ) : (
                      <Field label="清单名" hint="4–30 字符 · 接口形状未抓包，失败可手填 id">
                        <Input
                          value={form.javdb.listName}
                          onChange={(e) => set('javdb', { listName: e.target.value })}
                        />
                      </Field>
                    )}
                  </div>
                </>
              )}

              {step === 1 && (
                <>
                  <Field label="地址" hint="集群内 svc DNS 优先">
                    <Input mono value={form.qbt.url} onChange={(e) => set('qbt', { url: e.target.value })} />
                  </Field>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field label="用户名" hint="可选">
                      <Input value={form.qbt.username} onChange={(e) => set('qbt', { username: e.target.value })} />
                    </Field>
                    <Field label="密码" hint="可选 · 省略＝不改">
                      <Input type="password" value={form.qbt.password} onChange={(e) => set('qbt', { password: e.target.value })} />
                    </Field>
                  </div>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field label="分类 (category)" hint="默认 mdc">
                      <Input value={form.qbt.category} onChange={(e) => set('qbt', { category: e.target.value })} />
                    </Field>
                    <Field label="下载根目录" hint="每次加种显式传 savepath">
                      <Input mono value={form.qbt.downloadRoot} onChange={(e) => set('qbt', { downloadRoot: e.target.value })} />
                    </Field>
                  </div>
                  <Field label="回调 base URL" hint="qbt 只能走集群内，公网域名留给人类">
                    <Input mono value={form.qbt.baseUrl} onChange={(e) => set('qbt', { baseUrl: e.target.value })} />
                  </Field>
                  <div className="rounded-lg border bg-muted/30 p-3">
                    <div className="mb-2 flex items-center justify-between">
                      <span className="text-[13px] font-medium">完成时自动运行（autorun）</span>
                      <Badge tone="warn">将改写</Badge>
                    </div>
                    <div className="space-y-1.5 text-[11px] leading-relaxed">
                      <div>
                        <span className="text-muted-foreground">现状：</span>
                        <code className="font-mono break-all text-foreground/80">{autorunBefore}</code>
                      </div>
                      <div>
                        <span className="text-muted-foreground">改写为：</span>
                        <code className="font-mono break-all text-emerald-800">{autorunAfter}</code>
                      </div>
                    </div>
                    <p className="mt-2 text-[11px] text-muted-foreground">
                      幂等：现值已含本项目 URL 就跳过；不动 on_torrent_added。保存前会再展示一次前后对比。
                    </p>
                  </div>
                  <div className="flex items-center gap-2">
                    <Button variant="outline" size="sm" onClick={() => tQbt.run()} loading={tQbt.state === 'testing'}>
                      测试连接
                    </Button>
                    <TestChip state={tQbt.state} />
                  </div>
                </>
              )}

              {step === 2 && (
                <>
                  <Field label="地址">
                    <Input mono value={form.jellyfin.url} onChange={(e) => set('jellyfin', { url: e.target.value })} />
                  </Field>
                  <Field label="API Key" hint="只吐掩码">
                    <Input mono value={form.jellyfin.apiKey} onChange={(e) => set('jellyfin', { apiKey: e.target.value })} />
                  </Field>
                  <Field label="媒体库目录" hint="与下载根目录必须同路径可见（否则 EXDEV 直接失败）">
                    <Input mono value={form.jellyfin.mediaRoot} onChange={(e) => set('jellyfin', { mediaRoot: e.target.value })} />
                  </Field>
                  <div className="flex items-center gap-2">
                    <Button variant="outline" size="sm" onClick={() => tJelly.run()} loading={tJelly.state === 'testing'}>
                      测试可写
                    </Button>
                    <TestChip state={tJelly.state} />
                  </div>
                </>
              )}

              {step === 3 && (
                <>
                  <OptToggle
                    label="启用 M-Team"
                    desc="配了但可以关。启用后优先从 M-Team 取种，失败降级回 JavDB 磁力（会产生第二条版本）"
                    checked={form.mteam.enabled}
                    onChange={(v) => set('mteam', { enabled: v })}
                  />
                  <Field label="x-api-key">
                    <Input
                      mono
                      placeholder="未配置"
                      value={form.mteam.apiKey}
                      onChange={(e) => set('mteam', { apiKey: e.target.value })}
                    />
                  </Field>
                  <div className="flex items-center gap-2">
                    <Button variant="outline" size="sm" onClick={() => tMteam.run()} loading={tMteam.state === 'testing'}>
                      测试检索
                    </Button>
                    <TestChip state={tMteam.state} label="未测试" />
                  </div>
                </>
              )}

              {step === 4 && (
                <>
                  <OptToggle
                    label="启用 AI 翻译"
                    desc="只翻标题。失败不影响入库，只降级成日文标题并告警"
                    checked={form.ai.enabled}
                    onChange={(v) => set('ai', { enabled: v })}
                  />
                  <Field label="Base URL" hint="OpenAI 兼容">
                    <Input mono value={form.ai.baseUrl} onChange={(e) => set('ai', { baseUrl: e.target.value })} />
                  </Field>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field label="API Key">
                      <Input mono value={form.ai.apiKey} onChange={(e) => set('ai', { apiKey: e.target.value })} />
                    </Field>
                    <Field label="模型">
                      <Input value={form.ai.model} onChange={(e) => set('ai', { model: e.target.value })} />
                    </Field>
                  </div>
                  <div className="flex items-center gap-2">
                    <Button variant="outline" size="sm" onClick={() => tAi.run()} loading={tAi.state === 'testing'}>
                      测试一次翻译
                    </Button>
                    <TestChip state={tAi.state} />
                  </div>
                </>
              )}

              {step === 5 && (
                <>
                  <OptToggle
                    label="启用 Telegram"
                    desc="四个通知点：下载开始 / 入库完成 / 加工失败 / 下载停滞。同一事件只发一次"
                    checked={form.telegram.enabled}
                    onChange={(v) => set('telegram', { enabled: v })}
                  />
                  <Field label="Bot Token">
                    <Input mono value={form.telegram.botToken} onChange={(e) => set('telegram', { botToken: e.target.value })} />
                  </Field>
                  <Field label="Chat ID">
                    <Input mono value={form.telegram.chatId} onChange={(e) => set('telegram', { chatId: e.target.value })} />
                  </Field>
                  <div className="flex items-center gap-2">
                    <Button variant="outline" size="sm" onClick={() => tTg.run()} loading={tTg.state === 'testing'}>
                      发一条测试消息
                    </Button>
                    <TestChip state={tTg.state} />
                  </div>
                </>
              )}

              {step === 6 && (
                <>
                  <div className="rounded-lg border bg-muted/30 p-4">
                    <div className="text-[13px] font-medium">主线三件套</div>
                    <ul className="mt-2 space-y-1.5 text-xs">
                      <CheckLine ok label="JavDB 可读，清单 #48213 已登记" />
                      <CheckLine ok label="qBittorrent 可连，autorun 已挂上" />
                      <CheckLine ok label="Jellyfin 可写" />
                    </ul>
                    <Separator className="my-3" />
                    <div className="text-[11px] text-muted-foreground">
                      齐了才算「已初始化」，循环才会启动。M-Team / AI / Telegram 缺席只降级对应能力。
                    </div>
                  </div>
                  <div className="grid gap-3 sm:grid-cols-4">
                    <Field label="清单轮询（分钟）">
                      <Input value={form.advanced.poll} onChange={(e) => set('advanced', { poll: e.target.value })} />
                    </Field>
                    <Field label="对账（分钟）">
                      <Input value={form.advanced.reconcile} onChange={(e) => set('advanced', { reconcile: e.target.value })} />
                    </Field>
                    <Field label="副作用（分钟）">
                      <Input value={form.advanced.sidefx} onChange={(e) => set('advanced', { sidefx: e.target.value })} />
                    </Field>
                    <Field label="最大重试">
                      <Input value={form.advanced.maxAttempts} onChange={(e) => set('advanced', { maxAttempts: e.target.value })} />
                    </Field>
                  </div>
                </>
              )}
            </div>

            <Separator className="my-4" />
            <div className="flex items-center justify-between">
              <Button variant="ghost" size="sm" disabled={step === 0} onClick={() => setStep(step - 1)}>
                <ChevronLeft className="size-3.5" /> 上一步
              </Button>
              <div className="flex items-center gap-2">
                {!formOn && <span className="text-[11px] text-muted-foreground">只读演示</span>}
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setSaved(`已保存（PATCH /api/v1/settings/${stepKey(step)}）`)}
                >
                  保存本步
                </Button>
                {step === wizardSteps.length - 1 ? (
                  <Button size="sm" onClick={onDone}>
                    {fresh ? '完成初始化' : '回到面板'}
                  </Button>
                ) : (
                  <Button size="sm" onClick={() => setStep(step + 1)}>
                    下一步 <ChevronRight className="size-3.5" />
                  </Button>
                )}
              </div>
            </div>
          </Card>

          <p className="px-1 text-[11px] leading-relaxed text-muted-foreground">
            并发编辑有 revision 指纹：两个标签页同时改同一张表时，后提交的那个会拿到冲突提示，不静默互盖。
          </p>
        </div>
      </div>
    </div>
  );
}

function OptToggle({
  label,
  desc,
  checked,
  onChange,
}: {
  label: string;
  desc: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-start justify-between gap-4 rounded-lg border p-3">
      <div>
        <div className="text-[13px] font-medium">{label}</div>
        <p className="mt-0.5 max-w-md text-[11px] leading-relaxed text-muted-foreground">{desc}</p>
      </div>
      <Switch checked={checked} onChange={onChange} label={label} />
    </div>
  );
}

function CheckLine({ ok, label }: { ok?: boolean; label: string }) {
  return (
    <li className="flex items-center gap-2">
      <span
        className={cn(
          'grid size-4 place-items-center rounded-full border text-[9px]',
          ok ? 'border-emerald-200 bg-emerald-50 text-emerald-700' : 'border-amber-200 bg-amber-50 text-amber-700',
        )}
      >
        {ok ? '✓' : '!'}
      </span>
      <span className={ok ? '' : 'text-muted-foreground'}>{label}</span>
    </li>
  );
}

function stepKey(step: number) {
  return ['javdb', 'qbittorrent', 'jellyfin', 'mteam', 'ai_translation', 'telegram', 'app_config'][step];
}

function stepHelp(step: number) {
  return [
    '只管一件事：让我们能匿名读到那张自建清单，并记住它的 id。Token 只在读清单与删清单项时用。',
    '地址、账号、分类、下载根目录、回调 URL。这一屏还会改写 qbt 的 autorun——保存前给你看前后对比。',
    'Jellyfin 只做两件事：写入库条目（NFO/图片/硬链接），以及入库后刷新媒体库。必须能写到媒体库目录。',
    '可选。M-Team 是私有种子（无磁力，infohash 本地算），配额按 100 次/小时。不配就全走 JavDB 磁力。',
    '可选。只翻标题成中文；失败降级入库并告警，不阻塞流水线。',
    '可选。四个通知点，同一事件只发一次；失败进副作用队列重试，不阻塞入库。',
    '间隔与重试次数是用户设置。这里的数字改完即时生效，不用重启。',
  ][step];
}

/* -------------------------------- dashboard -------------------------------- */

function Dashboard({
  failed,
  retried,
  retry,
  selected,
  setSelected,
}: {
  failed: Task[];
  retried: string[];
  retry: (t: Task) => void;
  selected: Task | null;
  setSelected: (t: Task | null) => void;
}) {
  const downloading = tasks.filter((t) => t.versions.some((v) => v.detail.state === 'downloading')).length;
  const processing = tasks.filter((t) => t.versions.some((v) => v.detail.state === 'processing')).length;
  const finished = tasks.filter((t) => t.versions.every((v) => v.detail.state === 'finished')).length;

  return (
    <div className="mx-auto max-w-6xl space-y-5">
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="下载中" value={String(downloading)} hint="qbt 是进度真相" />
        <Stat label="加工中" value={String(processing)} hint="刮削 / 整理" />
        <Stat label="已入库" value={String(finished)} hint="jellyfin 已刷新" />
        <Stat label="需人工" value={String(failed.length)} tone="text-red-600" hint="自动重试已用尽" />
      </div>

      {failed.length > 0 && (
        <Card className="border-red-200/70 bg-red-50/30">
          <div className="flex items-center gap-2 border-b border-red-200/60 px-4 py-2.5">
            <AlertTriangle className="size-4 text-red-600" />
            <span className="text-[13px] font-medium">需要人工处理（{failed.length}）</span>
            <span className="ml-auto text-[11px] text-muted-foreground">
              自动重试 3 次已用尽 · 重试会记一条 manual_retry
            </span>
          </div>
          <ul className="divide-y divide-red-200/50">
            {failed.map((t) => {
              const v = t.versions.find((x) => x.detail.state === 'failed')!;
              const d = v.detail as Extract<typeof v.detail, { state: 'failed' }>;
              return (
                <li key={t.movieId} className="flex items-center gap-3 px-4 py-2.5">
                  <span className="font-mono text-[13px] font-medium">{t.number}</span>
                  <Badge tone="bad">{d.phase === 'downloading' ? '下载阶段' : d.phase === 'scrape' ? '刮削阶段' : '整理阶段'}</Badge>
                  <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">{d.reason}</span>
                  {retried.includes(t.movieId) ? (
                    <Badge tone="info">已提交重试</Badge>
                  ) : (
                    <Button size="sm" variant="outline" onClick={() => retry(t)}>
                      <RotateCcw className="size-3.5" /> 重试
                    </Button>
                  )}
                </li>
              );
            })}
          </ul>
        </Card>
      )}

      <Card>
        <div className="flex items-center justify-between border-b px-4 py-2.5">
          <SectionTitle title="任务" desc="一行一条任务（按 movie_id 聚合），点开看版本与事件" />
          <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
            <span>全部 · {tasks.length}</span>
          </div>
        </div>
        <table className="w-full text-[13px]">
          <thead className="text-[11px] text-muted-foreground">
            <tr className="border-b">
              <th className="px-4 py-2 text-left font-medium">番号</th>
              <th className="px-2 py-2 text-left font-medium">标题</th>
              <th className="px-2 py-2 text-left font-medium">状态</th>
              <th className="px-2 py-2 text-left font-medium">版本</th>
              <th className="px-2 py-2 text-left font-medium">清单</th>
              <th className="px-2 py-2 text-left font-medium">更新</th>
              <th className="px-4 py-2" />
            </tr>
          </thead>
          <tbody>
            {tasks.map((t) => {
              const meta = taskMeta(t);
              return (
                <tr
                  key={t.movieId}
                  className="cursor-pointer border-b last:border-0 hover:bg-muted/40"
                  onClick={() => setSelected(t)}
                >
                  <td className="px-4 py-2.5 font-mono font-medium">{t.number}</td>
                  <td className="max-w-[260px] truncate px-2 py-2.5 text-muted-foreground">{t.title}</td>
                  <td className="px-2 py-2.5">
                    <Badge tone={meta.tone}>
                      <Dot tone={meta.dot} /> {meta.label}
                    </Badge>
                  </td>
                  <td className="px-2 py-2.5">
                    <div className="flex flex-wrap gap-1">
                      {t.versions.map((v) => (
                        <span
                          key={v.infohash}
                          className="rounded border px-1 py-0.5 text-[10px] text-muted-foreground"
                          title={v.infohash}
                        >
                          {v.label}
                          {v.detail.state === 'failed' && ' !'}
                        </span>
                      ))}
                    </div>
                  </td>
                  <td className="px-2 py-2.5 text-xs text-muted-foreground">{t.listName}</td>
                  <td className="px-2 py-2.5 text-xs text-muted-foreground">{t.updatedAt}</td>
                  <td className="px-4 py-2.5 text-right text-muted-foreground">
                    <ChevronRight className="ml-auto size-4" />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <div className="border-b px-4 py-2.5">
            <SectionTitle title="副作用队列" desc="删清单项 / 刷 Jellyfin / 发通知，各自重试" right={<Button variant="ghost" size="sm"><RefreshCw className="size-3.5" /> 立即重试全部</Button>} />
          </div>
          <ul className="divide-y text-xs">
            {sideEffects.map((s, i) => (
              <li key={i} className="flex items-center gap-3 px-4 py-2.5">
                <Badge tone={s.lastError ? 'warn' : 'muted'}>{sideEffectLabel[s.kind]}</Badge>
                <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">{s.target}</span>
                <span className="text-[11px] text-muted-foreground">{s.lastError ?? s.nextAttempt}</span>
                <span className="tabular-nums text-[11px] text-muted-foreground">×{s.attempts}</span>
              </li>
            ))}
          </ul>
        </Card>

        <Card>
          <div className="flex items-center justify-between border-b px-4 py-2.5">
            <SectionTitle title="循环" desc="单飞：同类循环不重叠" />
            <Badge tone="ok">
              <Dot tone="ok" /> 3 / 3
            </Badge>
          </div>
          <ul className="divide-y text-xs">
            {loops.map((l) => (
              <li key={l.name} className="flex items-center gap-3 px-4 py-2.5">
                <Dot tone={l.state} />
                <span className="flex-1">{l.name}</span>
                <span className="text-muted-foreground">每 {l.interval} 分钟</span>
                <span className="w-24 text-right text-muted-foreground">{l.last}</span>
              </li>
            ))}
            <li className="flex items-center gap-3 px-4 py-2.5 text-muted-foreground">
              <Dot tone="muted" />
              <span className="flex-1">webhook</span>
              <span>qbt 完成回调即时触发</span>
            </li>
          </ul>
        </Card>
      </div>

      <Card>
        <div className="border-b px-4 py-2.5">
          <SectionTitle title="最近事件" desc="task_event，append-only；告警去重也靠它" />
        </div>
        <ul className="divide-y text-xs">
          {events.slice(0, 5).map((e, i) => (
            <li key={i} className="flex items-start gap-3 px-4 py-2">
              <span className="w-16 shrink-0 text-muted-foreground">{e.at}</span>
              <EventBadge kind={e.kind} />
              <span className="flex-1 leading-relaxed">{e.text}</span>
            </li>
          ))}
        </ul>
      </Card>
    </div>
  );
}

function EventBadge({ kind }: { kind: string }) {
  const map: Record<string, { label: string; tone: 'muted' | 'ok' | 'warn' | 'bad' | 'info' }> = {
    state_change: { label: '状态', tone: 'info' },
    side_effect: { label: '旁路', tone: 'muted' },
    alert: { label: '告警', tone: 'bad' },
    manual_retry: { label: '人工', tone: 'warn' },
  };
  const m = map[kind] ?? { label: kind, tone: 'muted' as const };
  return <Badge tone={m.tone}>{m.label}</Badge>;
}

function taskMeta(t: Task) {
  if (t.versions.some((v) => v.detail.state === 'processing')) return stateMeta.processing;
  if (t.versions.some((v) => v.detail.state === 'downloading')) return stateMeta.downloading;
  if (t.versions.some((v) => v.detail.state === 'finished')) return stateMeta.finished;
  return stateMeta.failed;
}

function Drawer({
  task,
  onClose,
  onRetry,
}: {
  task: Task;
  onClose: () => void;
  onRetry: (t: Task) => void;
}) {
  return (
    <div className="fixed inset-0 z-40 flex justify-end">
      <div className="absolute inset-0 bg-black/20" onClick={onClose} />
      <aside className="relative flex w-full max-w-[440px] flex-col border-l bg-card shadow-2xl">
        <div className="flex items-center justify-between border-b px-4 py-3">
          <div>
            <div className="font-mono text-sm font-semibold">{task.number}</div>
            <div className="text-[11px] text-muted-foreground">{task.title}</div>
          </div>
          <Button variant="ghost" size="icon" onClick={onClose}>
            <X className="size-4" />
          </Button>
        </div>

        <div className="flex-1 space-y-4 overflow-y-auto p-4">
          <div>
            <div className="mb-2 text-[11px] font-medium text-muted-foreground">版本（身份＝infohash）</div>
            <ul className="space-y-2">
              {task.versions.map((v) => (
                <li key={v.infohash} className="rounded-lg border p-2.5 text-xs">
                  <div className="flex items-center gap-2">
                    <span className="font-medium">{v.label}</span>
                    <Badge tone={v.source === 'mteam_torrent' ? 'info' : 'muted'}>
                      {v.source === 'mteam_torrent' ? 'M-Team 种子' : 'JavDB 磁力'}
                    </Badge>
                    <VersionBadge detail={v.detail} />
                  </div>
                  <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">{v.infohash}</div>
                  {v.detail.state === 'failed' && (
                    <div className="mt-1 text-[11px] text-red-700">{v.detail.reason}</div>
                  )}
                </li>
              ))}
            </ul>
          </div>

          {task.note && <Notice text={task.note} />}

          <div>
            <div className="mb-2 text-[11px] font-medium text-muted-foreground">事件</div>
            <ol className="space-y-2 border-l pl-3">
              {events.slice(0, 4).map((e, i) => (
                <li key={i} className="relative text-[11px] leading-relaxed">
                  <span className="absolute -left-[15px] top-1.5 size-1.5 rounded-full bg-border" />
                  <span className="text-muted-foreground">{e.at} · </span>
                  {e.text}
                </li>
              ))}
            </ol>
          </div>
        </div>

        <div className="flex items-center gap-2 border-t p-3">
          <Button size="sm" onClick={() => onRetry(task)}>
            <RotateCcw className="size-3.5" /> 重试
          </Button>
          <Button size="sm" variant="outline">
            强制重刮
          </Button>
          <span className="ml-auto text-[10px] text-muted-foreground">强制＝force=true，已入库版本也重跑</span>
        </div>
      </aside>
    </div>
  );
}

function VersionBadge({ detail }: { detail: Task['versions'][number]['detail'] }) {
  if (detail.state === 'downloading') return <Badge tone="info">下载中</Badge>;
  if (detail.state === 'processing')
    return <Badge tone="warn">{detail.phase === 'scrape' ? '刮削中' : '整理中'}</Badge>;
  if (detail.state === 'finished') return <Badge tone="ok">已入库</Badge>;
  return <Badge tone="bad">失败 ×{detail.attempts}</Badge>;
}
