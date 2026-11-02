import * as React from "react";
import { ChevronLeft, ChevronRight, CircleHelp, Loader2 } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Card } from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import {
  Field,
  Notice,
  TestChip,
  TextInput,
  Toggle,
  type TestChipState,
} from "~/components/form";
import {
  api,
  HttpError,
  type ConfigSnapshot,
  type ListEntry,
  type Masked,
  type SectionName,
  type SectionState,
} from "~/lib/api";

/**
 * 八步向导壳（规格 §8.1）：一屏一步，一步一张表。
 *
 * 顺序与 REST 的八节一一对应：JavDB 账号 → 清单登记 → qBittorrent → Jellyfin →
 * M-Team（可选）→ AI 翻译（可选）→ Telegram（可选）→ 完成（间隔与重试）。
 * 每步独立「保存本步」＝按表 PATCH；「测试连接」的结果写 last_test_*。
 * 未初始化时整站只剩这一页，面板入口被门禁挡住（规格 §8.3）。
 */

type FieldKind = "text" | "secret" | "number" | "duration" | "bool";

type FieldSpec = {
  key: string;
  label: string;
  hint?: string;
  kind: FieldKind;
  placeholder?: string;
  mono?: boolean;
};

type StepSpec = {
  section: SectionName;
  title: string;
  help: string;
  optional?: boolean;
  /** 有测试按钮就有它；testSection 默认就是本步的表，清单步借 JavDB 的读测试。 */
  test?: { label: string; section?: SectionName };
  fields: FieldSpec[];
  /** 固定值：不在界面上露脸，但保存时必须带上（如清单用途）。 */
  fixed?: Record<string, string>;
};

const steps: StepSpec[] = [
  {
    section: "javdb",
    title: "JavDB 账号",
    help: "只管一件事：让我们能读到那张自建清单。Token 只在读清单与删清单项时用，从不自动重新登录（单会话账号会被踢）。",
    test: { label: "测试读取" },
    fields: [
      {
        key: "token",
        label: "JavDB Token",
        hint: "Bearer JWT · 已保存的只显示掩码，留空＝不改",
        kind: "secret",
        mono: true,
        placeholder: "粘贴手机里导出的 token",
      },
    ],
  },
  {
    section: "list_registry",
    title: "清单登记",
    help: "按 id 认清单，名字只是快照：清单改名不影响轮询。「测试读取」会连 JavDB 读这张清单——凭据与清单是一起验证的。",
    // 清单不是集成表（没有 last_test_*），它的「测试读取」走 JavDB 那一节：
    // 真实的测试内容就是「用户域读 + 登记清单读」（规格 §8.4）。
    test: { label: "测试读取", section: "javdb" },
    fields: [
      { key: "list_id", label: "清单 id", hint: "JavDB 清单页地址里的数字", kind: "number", mono: true },
      { key: "name", label: "清单名字", hint: "只用于展示", kind: "text", placeholder: "想看清单" },
    ],
    fixed: { purpose: "want_watch" },
  },
  {
    section: "qbittorrent",
    title: "qBittorrent",
    help: "地址、账号、分类、下载根目录、回调 base URL。回调地址要走集群内 svc DNS：qBittorrent 在集群里，公网域名留给人类。",
    test: { label: "测试连接" },
    fields: [
      { key: "url", label: "地址", kind: "text", mono: true, placeholder: "http://qbittorrent.media.svc:8080" },
      { key: "username", label: "用户名", hint: "可选", kind: "text" },
      { key: "password", label: "密码", hint: "留空＝不改", kind: "secret" },
      { key: "category", label: "分类 (category)", hint: "默认 mdc", kind: "text" },
      { key: "download_root", label: "下载根目录", hint: "每次加种显式传 savepath", kind: "text", mono: true },
      {
        key: "webhook_base_url",
        label: "回调 base URL",
        hint: "autorun 里要用的那个地址",
        kind: "text",
        mono: true,
        placeholder: "http://oh-my-mdc.oh-my-mdc.svc:8080",
      },
    ],
  },
  {
    section: "jellyfin",
    title: "Jellyfin",
    help: "地址 + API key。媒体库目录在最后一步填（media_root），入库后靠它做目录级刷新。",
    test: { label: "测试可写" },
    fields: [
      { key: "url", label: "地址", kind: "text", mono: true, placeholder: "http://jellyfin.media.svc:8096" },
      { key: "api_key", label: "API Key", hint: "留空＝不改", kind: "secret", mono: true },
    ],
  },
  {
    section: "mteam",
    title: "M-Team",
    optional: true,
    help: "可选。启用后优先从 M-Team 取种，失败降级回 JavDB 磁力（会产生第二条版本）。不配就全走磁力。",
    test: { label: "测试检索" },
    fields: [
      { key: "enabled", label: "启用 M-Team", kind: "bool" },
      { key: "api_key", label: "x-api-key", hint: "留空＝不改", kind: "secret", mono: true },
    ],
  },
  {
    section: "ai_translation",
    title: "AI 翻译",
    optional: true,
    help: "可选。只翻标题与简介成中文；失败降级日文原文照常入库并告警，不阻塞流水线。",
    test: { label: "测试一次翻译" },
    fields: [
      { key: "enabled", label: "启用 AI 翻译", kind: "bool" },
      { key: "base_url", label: "Base URL", hint: "OpenAI 兼容", kind: "text", mono: true },
      { key: "api_key", label: "API Key", hint: "留空＝不改", kind: "secret", mono: true },
      { key: "model", label: "模型", kind: "text" },
    ],
  },
  {
    section: "telegram",
    title: "Telegram",
    optional: true,
    help: "可选。四个通知点各自开关，同一事件只发一次；失败进副作用队列重试，不阻塞入库。",
    test: { label: "发一条测试消息" },
    fields: [
      { key: "enabled", label: "启用 Telegram", kind: "bool" },
      { key: "bot_token", label: "Bot Token", hint: "留空＝不改", kind: "secret", mono: true },
      { key: "chat_id", label: "Chat ID", kind: "text", mono: true },
      { key: "notify_download_started", label: "通知：下载开始", kind: "bool" },
      { key: "notify_filed", label: "通知：入库完成", kind: "bool" },
      { key: "notify_processing_failed", label: "通知：加工失败", kind: "bool" },
      { key: "notify_download_stalled", label: "通知：下载停滞", kind: "bool" },
    ],
  },
  {
    section: "app",
    title: "完成",
    help: "间隔与重试都在这一步。三件套齐了「已初始化」才成立，三个循环才开始跑。",
    fields: [
      { key: "poll_interval", label: "清单轮询（分钟）", kind: "duration" },
      { key: "reconcile_interval", label: "对账（分钟）", kind: "duration" },
      { key: "sidefx_interval", label: "副作用（分钟）", kind: "duration" },
      { key: "max_attempts", label: "最大重试次数", kind: "number" },
      { key: "media_root", label: "媒体库目录", hint: "与下载根目录必须同路径可见", kind: "text", mono: true, placeholder: "/media/jellyfin/jav" },
      { key: "paused", label: "暂停循环", hint: "正在跑的版本会跑完，不接新的", kind: "bool" },
    ],
  },
];

type FormValues = Record<string, string | boolean>;

export function Wizard({
  snapshot,
  onRefresh,
  onDone,
}: {
  snapshot: ConfigSnapshot;
  onRefresh: () => Promise<void>;
  onDone: () => void;
}) {
  const [stepIndex, setStepIndex] = React.useState(0);
  const [notice, setNotice] = React.useState<{
    tone: "ok" | "warn" | "bad" | "muted";
    text: string;
  } | null>(null);
  const [pending, setPending] = React.useState<"save" | "test" | null>(null);

  const step = steps[stepIndex];
  const section = snapshot.sections[step.section];
  const testedSection = step.test?.section ? snapshot.sections[step.test.section] : section;

  // 表单不做成 effect 里的状态：草稿带着「它属于哪一节哪个 revision」的钥匙。
  // 换步或保存后（revision 变了）自然回落到服务器最新值 —— 敏感字段永远是空串
  // （掩码只做 placeholder），留空提交就等于不改。测试 chip 同理。
  const formKey = `${step.section}:${section.revision}`;
  // chip 跟着**被测那一节**的账本走（清单步测的是 JavDB），所以它的钥匙里也要有那一节的指纹。
  const chipKey = `${formKey}:${testedSection.revision}`;
  const [draft, setDraft] = React.useState<{ key: string; values: FormValues } | null>(null);
  const [chips, setChips] = React.useState<{ key: string; chip: TestChipState } | null>(null);

  const values = draft?.key === formKey ? draft.values : initialValues(step, section);
  const chip = chips?.key === chipKey ? chips.chip : chipFromLastTest(testedSection);

  const setField = (key: string, value: string | boolean) =>
    setDraft({ key: formKey, values: { ...values, [key]: value } });
  const setChip = (next: TestChipState) => setChips({ key: chipKey, chip: next });

  const save = async () => {
    setPending("save");
    setNotice(null);
    try {
      const patch = buildPatch(step, section, values);
      await api.save(step.section, patch);
      await onRefresh();
      setNotice({ tone: "ok", text: "已保存本步" });
    } catch (error) {
      if (error instanceof HttpError && error.isRevisionConflict) {
        await onRefresh();
        setNotice({
          tone: "warn",
          text: "这一节已被别处改过：已拉取最新内容，确认后再保存一次。",
        });
      } else {
        setNotice({ tone: "bad", text: messageOf(error) });
      }
    } finally {
      setPending(null);
    }
  };

  const test = async () => {
    if (!step.test) return;
    setPending("test");
    setChip({ kind: "testing" });
    try {
      const result = await api.test(step.test.section ?? step.section);
      if (result.ok) {
        setChip({ kind: "ok", detail: result.detail, durationMs: result.duration_ms });
      } else if (result.code === "not_implemented") {
        setChip({ kind: "placeholder", detail: result.detail });
      } else {
        setChip({
          kind: "fail",
          code: result.code ?? "error",
          detail: result.detail,
          durationMs: result.duration_ms,
        });
      }
    } catch (error) {
      setChip({ kind: "fail", code: codeOf(error), detail: messageOf(error) });
    } finally {
      setPending(null);
    }
  };

  const goTo = (next: number) => {
    setStepIndex(Math.max(0, Math.min(steps.length - 1, next)));
    setNotice(null);
  };

  const isLast = stepIndex === steps.length - 1;

  return (
    <div className="mx-auto grid max-w-5xl gap-6 lg:grid-cols-[230px_1fr]">
      <ol className="space-y-1" aria-label="向导步骤">
        {steps.map((candidate, index) => {
          // 「完成」步没有自己的表：它反映门禁，而不是 app_config 那一行。
          const configured =
            candidate.section === "app"
              ? snapshot.initialized
              : snapshot.sections[candidate.section].configured;
          const current = index === stepIndex;
          return (
            <li key={candidate.section}>
              <button
                type="button"
                onClick={() => goTo(index)}
                aria-current={current ? "step" : undefined}
                className={cn(
                  "flex w-full items-start gap-2.5 rounded-md px-2 py-2 text-left text-[13px] transition-colors",
                  current ? "bg-accent font-medium" : "text-muted-foreground hover:bg-accent/50",
                )}
              >
                <span
                  className={cn(
                    "mt-0.5 grid size-5 shrink-0 place-items-center rounded-full border text-[10px] tabular-nums",
                    configured && "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-300",
                    current && !configured && "border-primary bg-primary text-primary-foreground",
                  )}
                >
                  {configured ? "✓" : index + 1}
                </span>
                <span className="leading-snug">
                  {candidate.title}
                  {candidate.optional ? (
                    <span className="ml-1 text-[10px] opacity-60">可跳过</span>
                  ) : null}
                </span>
              </button>
            </li>
          );
        })}
      </ol>

      <div className="min-w-0 space-y-4">
        {!snapshot.initialized ? <GateCard snapshot={snapshot} /> : null}
        {notice ? (
          <Notice tone={notice.tone} onClose={() => setNotice(null)}>
            {notice.text}
          </Notice>
        ) : null}

        <Card className="p-5">
          <div className="mb-4 flex flex-wrap items-start justify-between gap-2">
            <div>
              <h2 className="font-heading text-base font-semibold">{step.title}</h2>
              <p className="mt-1 max-w-xl text-xs leading-relaxed text-muted-foreground">
                {step.help}
              </p>
            </div>
            <Badge variant="outline">
              第 {stepIndex + 1} / {steps.length} 步
            </Badge>
          </div>
          <Separator className="mb-4" />

          <div className="space-y-3">
            {step.fields.map((field) => (
              <FieldRow
                key={field.key}
                field={field}
                value={values[field.key]}
                section={section}
                onChange={(value) => setField(field.key, value)}
              />
            ))}
          </div>

          {step.section === "list_registry" ? (
            <RegisteredListHint entries={entriesOf(section)} />
          ) : null}

          {isLast ? <MainlineChecklist snapshot={snapshot} /> : null}

          <Separator className="my-4" />

          <div className="flex flex-wrap items-center justify-between gap-2">
            <Button
              variant="ghost"
              size="sm"
              disabled={stepIndex === 0}
              onClick={() => goTo(stepIndex - 1)}
            >
              <ChevronLeft aria-hidden />
              上一步
            </Button>

            <div className="flex flex-wrap items-center gap-2">
              {step.test ? (
                <>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={test}
                    disabled={pending !== null || !testedSection.configured}
                    title={testedSection.configured ? undefined : "先保存本步再测试"}
                  >
                    {pending === "test" ? <Loader2 className="animate-spin" aria-hidden /> : null}
                    {step.test.label}
                  </Button>
                  <TestChip state={chip} />
                </>
              ) : null}

              <Button variant="outline" size="sm" onClick={save} disabled={pending !== null}>
                {pending === "save" ? <Loader2 className="animate-spin" aria-hidden /> : null}
                保存本步
              </Button>

              {step.optional ? (
                <Button variant="ghost" size="sm" onClick={() => goTo(stepIndex + 1)}>
                  跳过本步
                </Button>
              ) : null}

              {isLast ? (
                <Button size="sm" onClick={onDone} disabled={!snapshot.initialized}>
                  {snapshot.initialized ? "回到面板" : "完成初始化"}
                </Button>
              ) : (
                <Button size="sm" onClick={() => goTo(stepIndex + 1)}>
                  下一步
                  <ChevronRight aria-hidden />
                </Button>
              )}
            </div>
          </div>
        </Card>

        <p className="px-1 text-[11px] leading-relaxed text-muted-foreground">
          并发编辑有 revision 指纹：两个标签页改同一节时，后提交的会拿到冲突提示，不静默互盖。
          敏感字段只回掩码，留空就是保持原值。
        </p>
      </div>
    </div>
  );
}

function FieldRow({
  field,
  value,
  section,
  onChange,
}: {
  field: FieldSpec;
  value: string | boolean | undefined;
  section: SectionState;
  onChange: (value: string | boolean) => void;
}) {
  if (field.kind === "bool") {
    return (
      <Toggle
        label={field.label}
        desc={field.hint}
        checked={Boolean(value)}
        onChange={onChange}
      />
    );
  }

  const secret = field.kind === "secret";
  const masked = section.data[field.key];
  const placeholder = secret ? maskedValue(masked, field.placeholder) : field.placeholder;

  return (
    <Field label={field.label} hint={field.hint}>
      <TextInput
        mono={field.mono}
        type={secret ? "password" : field.kind === "number" || field.kind === "duration" ? "number" : "text"}
        inputMode={field.kind === "number" || field.kind === "duration" ? "numeric" : undefined}
        placeholder={placeholder}
        autoComplete={secret ? "new-password" : "off"}
        value={String(value ?? "")}
        onChange={(event) => onChange(event.target.value)}
        className={secret ? "placeholder:font-mono" : undefined}
      />
    </Field>
  );
}

/** GateCard 是规格 §8.3 的解释卡：三件套齐了循环才会启动。 */
function GateCard({ snapshot }: { snapshot: ConfigSnapshot }) {
  const { javdb, list_registry, qbittorrent, jellyfin } = snapshot.sections;
  const wantWatch = entriesOf(list_registry).find((entry) => entry.purpose === "want_watch");

  return (
    <Card className="border-amber-200 bg-amber-50/60 p-4 dark:border-amber-900 dark:bg-amber-950/40">
      <div className="flex items-start gap-3">
        <div className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-full bg-background/70">
          <CircleHelp className="size-4 text-muted-foreground" aria-hidden />
        </div>
        <div className="min-w-0">
          <h2 className="text-sm font-semibold">还没初始化</h2>
          <p className="mt-1 text-[12px] leading-relaxed text-muted-foreground">
            主线三件套（JavDB 可读且清单登记就位、qBittorrent 可连、Jellyfin
            可写）齐了，循环才会启动。在那之前这个页面只有向导，面板进不去。
          </p>
          <ul className="mt-2 space-y-1 text-[12px]">
            <CheckLine ok={javdb.configured} label="JavDB token 已保存" />
            <CheckLine
              ok={Boolean(wantWatch)}
              label={wantWatch ? `清单 #${wantWatch.list_id} 已登记` : "清单还没登记"}
            />
            <CheckLine ok={qbittorrent.configured} label="qBittorrent 已配置" />
            <CheckLine ok={jellyfin.configured} label="Jellyfin 已配置" />
          </ul>
        </div>
      </div>
    </Card>
  );
}

function MainlineChecklist({ snapshot }: { snapshot: ConfigSnapshot }) {
  const { javdb, qbittorrent, jellyfin } = snapshot.sections;
  return (
    <div className="mt-3 rounded-lg border bg-muted/30 p-3">
      <div className="text-[13px] font-medium">主线三件套</div>
      <ul className="mt-1.5 space-y-1 text-[12px]">
        <CheckLine ok={javdb.configured} label="JavDB 可读，清单已登记" />
        <CheckLine ok={qbittorrent.configured} label="qBittorrent 可连" />
        <CheckLine ok={jellyfin.configured} label="Jellyfin 可写" />
      </ul>
      <p className="mt-2 text-[11px] text-muted-foreground">
        M-Team / AI 翻译 / Telegram 缺席只降级对应能力，不影响初始化。
      </p>
    </div>
  );
}

function CheckLine({ ok, label }: { ok: boolean; label: string }) {
  return (
    <li className="flex items-center gap-2">
      <span
        className={cn(
          "grid size-4 shrink-0 place-items-center rounded-full border text-[9px]",
          ok
            ? "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-300"
            : "border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300",
        )}
      >
        {ok ? "✓" : "!"}
      </span>
      <span className={ok ? "" : "text-muted-foreground"}>{label}</span>
    </li>
  );
}

/* -------------------------------- helpers --------------------------------- */

/** 每种字段在「表单初始值」与「发出去的 JSON」上各有一个形状，集中成一张表。 */
type KindHandler = {
  initial: (field: FieldSpec, section: SectionState) => string | boolean;
  /** 返回 [是否带上这个字段, 值]；不带＝省略，服务端即「不改」。 */
  payload: (value: string | boolean) => [include: boolean, value?: unknown];
};

const kindHandlers: Record<FieldKind, KindHandler> = {
  text: {
    initial: (field, section) => textValue(section.data[field.key]),
    payload: (value) => [true, String(value ?? "")],
  },
  secret: {
    // 敏感字段永远从空开始：掩码只做 placeholder，留空提交就等于不改。
    initial: () => "",
    payload: (value) => {
      const text = String(value ?? "");
      return text.trim() === "" ? [false] : [true, text];
    },
  },
  number: {
    initial: (field, section) => textValue(section.data[field.key]),
    payload: (value) => {
      const text = String(value ?? "").trim();
      return text === "" ? [false] : [true, Number(text)];
    },
  },
  duration: {
    // 服务端给 Go 时长（"15m0s"），界面上只让人填分钟。
    initial: (field, section) => minutesOf(section.data[field.key]),
    payload: (value) => {
      const text = String(value ?? "").trim();
      return text === "" ? [false] : [true, `${Number(text)}m`];
    },
  },
  bool: {
    // 可选集成的 enabled 在节一级（section.enabled），其余 bool 在 data 里。
    initial: (field, section) =>
      field.key === "enabled" ? Boolean(section.enabled) : Boolean(section.data[field.key]),
    payload: (value) => [true, Boolean(value)],
  },
};

function initialValues(step: StepSpec, section: SectionState): FormValues {
  const values: FormValues = { ...step.fixed };
  for (const field of step.fields) {
    values[field.key] = kindHandlers[field.kind].initial(field, section);
  }
  return values;
}

function buildPatch(
  step: StepSpec,
  section: SectionState,
  values: FormValues,
): Record<string, unknown> {
  const patch: Record<string, unknown> = { revision: section.revision, ...step.fixed };
  for (const field of step.fields) {
    const [include, value] = kindHandlers[field.kind].payload(values[field.key]);
    if (include) patch[field.key] = value;
  }
  return patch;
}

function textValue(raw: unknown): string {
  return raw === null || raw === undefined ? "" : String(raw);
}

/** minutesOf 把 "15m0s" / "1h30m0s" 这样的 Go 时长翻成分钟数。 */
function minutesOf(raw: unknown): string {
  const text = textValue(raw);
  if (!text) return "";
  const hours = /^(\d+(?:\.\d+)?)h/.exec(text);
  if (hours) return String(Number(hours[1]) * 60);
  const minutes = /(?:^|\s)(\d+(?:\.\d+)?)m/.exec(text);
  return minutes ? String(Number(minutes[1])) : "";
}

function entriesOf(section: SectionState): ListEntry[] {
  const entries = section.data.entries;
  return Array.isArray(entries) ? (entries as ListEntry[]) : [];
}

function maskedValue(masked: unknown, fallback?: string): string | undefined {
  const value = masked as Masked | undefined;
  if (value?.masked) return `已保存：${value.masked}`;
  return fallback;
}

function chipFromLastTest(section: SectionState): TestChipState {
  const lastTest = section.last_test;
  if (!lastTest) return { kind: "idle" };
  if (lastTest.ok) {
    return { kind: "ok", detail: `上次测试通过 · ${formatWhen(lastTest.at)}` };
  }
  const [code, ...rest] = (lastTest.error ?? "error").split(":");
  return {
    kind: "fail",
    code: code.trim(),
    detail: `${rest.join(":").trim()} · ${formatWhen(lastTest.at)}`,
  };
}

function formatWhen(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  return at.toLocaleString("zh-CN", { hour12: false });
}

function RegisteredListHint({ entries }: { entries: ListEntry[] }) {
  const wantWatch = entries.find((entry) => entry.purpose === "want_watch");
  if (!wantWatch) {
    return (
      <p className="mt-3 text-[11px] text-muted-foreground">
        还没登记清单：填 id 保存后，轮询只认这个 id，名字改了也不影响。
      </p>
    );
  }
  return (
    <p className="mt-3 text-[11px] text-muted-foreground">
      当前登记：<span className="font-mono">#{wantWatch.list_id}</span>
      {wantWatch.name ? ` · ${wantWatch.name}` : ""}（用途 want_watch）
    </p>
  );
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function codeOf(error: unknown): string {
  return error instanceof HttpError ? error.code : "network";
}
