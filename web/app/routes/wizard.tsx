import * as React from "react";
import { Link, useNavigate } from "react-router";
import { ChevronLeft, ChevronRight, CircleHelp, Loader2, Plus, RefreshCw } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Card } from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { ShortcutsHelp } from "~/components/panel";
import {
  Field,
  StatusLine,
  TestChip,
  TextInput,
  Toggle,
  type TestChipState,
} from "~/components/form";
import {
  api,
  HttpError,
  type AutorunPlan,
  type ConfigSnapshot,
  type JavDBList,
  type ListEntry,
  type Masked,
  type SectionName,
  type SectionState,
} from "~/lib/api";
import { clearDrafts, draftFor, setFields, type Drafts } from "~/lib/draft";
import { focusablesIn, holdShellInert } from "~/lib/modal";
import { dirtyKeys } from "~/lib/stepform";
import { formatInterval } from "~/lib/time";

/**
 * 八步向导壳（规格 §8.1）：一屏一步，一步一张表。
 *
 * 顺序与 REST 的八节一一对应：JavDB 账号 → 清单登记 → qBittorrent → Jellyfin →
 * M-Team（可选）→ AI 翻译（可选）→ Telegram（可选）→ 完成（间隔与重试）。
 * 每步独立保存＝按表 PATCH；「测试连接」的结果写 last_test_*。
 * 未初始化时整站只剩这一页，面板入口被门禁挡住（规格 §8.3）。
 *
 * 提交模型（一次真实的返工之后定的）：
 *
 * - **保存不是一件需要单独记着做的事**。`下一步` 与 `测试` 都先保存本步，再各做各的
 *   （前进 / 连上游）。以前 `下一步` 只是一条链接 + 一个墨色主按钮，看起来像「保存并
 *   继续」，实际只导航：屏幕上看不出「改了但没存」，测试还测的是旧值。
 * - **草稿按表各存一份**（`lib/draft`）。改完 A 步去动 B 步，A 的输入还在。
 * - **脏就是脏**：字段旁写「未保存」，动作行写「未保存 · 下一步与测试会先保存」。刷新/关页会拦
 *   一次（草稿只在内存里）。
 * - **最后一步的那颗主按钮真的会按得动**：它保存本步、重读门禁，然后要么给一条
 *   「已初始化」的回执，要么把还差哪几项列出来并给出链接。
 */

type FieldKind = "text" | "secret" | "number" | "duration" | "bool";

type FieldSpec = {
  key: string;
  label: string;
  hint?: string;
  kind: FieldKind;
  placeholder?: string;
  mono?: boolean;
  /** 数字类字段在界面单位上的范围（服务端另有自己的下限，这里只是先拦一次）。 */
  min?: number;
  max?: number;
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
        hint: "Bearer JWT · 在 JavDB App 的设置里复制后粘贴；已保存的只显示掩码，留空＝不改",
        kind: "secret",
        mono: true,
        placeholder: "粘贴手机里导出的 token",
      },
    ],
  },
  {
    section: "list_registry",
    title: "清单登记",
    help: "按 id 认清单，名字只是快照：清单改名不影响轮询。「测试读取」会先保存本步，再连 JavDB 读这张清单——凭据与清单是一起验证的。",
    // 清单不是集成表（没有 last_test_*），它的「测试读取」走 JavDB 那一节：
    // 真实的测试内容就是「用户域读 + 登记清单读」（规格 §8.4）。
    test: { label: "测试读取", section: "javdb" },
    fields: [
      { key: "list_id", label: "清单 id", hint: "JavDB 清单 id（6 位，如 k4EVE4）；也可用下面的探测/代建自动填入", kind: "text", mono: true },
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
      { key: "url", label: "地址", kind: "text", mono: true, placeholder: "http://qbittorrent.qbittorrent-private.svc:8082" },
      { key: "username", label: "用户名", hint: "可选", kind: "text" },
      { key: "password", label: "密码", hint: "留空＝不改", kind: "secret" },
      { key: "category", label: "分类", hint: "qBittorrent 的 category，默认 mdc", kind: "text" },
      { key: "download_root", label: "下载根目录", hint: "每次加种显式传保存路径；须与 Jellyfin 媒体库目录同一 host 目录", kind: "text", mono: true },
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
    help: "地址 + API key + 媒体库目录。「测试可写」过两关：System/Info 鉴权，以及这项目录在本机可见——三个容器必须把同一个 host 目录挂到同一路径。",
    test: { label: "测试可写" },
    fields: [
      { key: "url", label: "地址", kind: "text", mono: true, placeholder: "http://jellyfin.jellyfin.svc:8096" },
      { key: "api_key", label: "API Key", hint: "留空＝不改", kind: "secret", mono: true },
      {
        key: "media_root",
        label: "媒体库目录",
        hint: "与 qBittorrent 下载根目录同一 host 目录；目录级刷新靠它",
        kind: "text",
        mono: true,
        placeholder: "/media/jellyfin/jav",
      },
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
    help: "间隔与重试都在这一步。三件套齐了「已初始化」才成立，三个循环才开始跑；「完成初始化」会先保存本步再检查门禁——齐了给回执，没齐把差的项列成链接。",
    fields: [
      { key: "poll_interval", label: "清单轮询（分钟）", kind: "duration", min: 1, max: 1440 },
      { key: "reconcile_interval", label: "对账（分钟）", kind: "duration", min: 1, max: 1440 },
      { key: "sidefx_interval", label: "副作用（分钟）", kind: "duration", min: 1, max: 60 },
      { key: "max_attempts", label: "最大重试次数", kind: "number", min: 1, max: 100 },
      { key: "paused", label: "暂停循环", hint: "正在跑的版本会跑完，不接新的；初始化请保持关闭", kind: "bool" },
    ],
  },
];

/** 八节的地址顺序：`/settings/<section>` 认的就是这张表。 */
export const SECTION_ORDER: SectionName[] = steps.map((step) => step.section);

/** isSectionName 收窄地址里的一段，认不出的一律回落第一节。 */
export function isSectionName(value: string | undefined): value is SectionName {
  return value !== undefined && (SECTION_ORDER as string[]).includes(value);
}

/** 动作行里的按钮：手机上 44px 热区，md 起回到 28px 的紧凑高度（DESIGN 的拇指规则）。 */
const ACTION = "h-11 px-3 text-[0.8125rem] md:h-7 md:px-2.5 md:text-xs";

/**
 * Wizard 是集成设置的八节（规格 §8.1）：一屏一节，一步一张表。
 *
 * 节由地址决定（`/settings/<section>`），不是内存里的步号：于是「去 Telegram 设置」
 * 落到 Telegram 那一节，可书签、可刷新。未初始化时同一条路由渲染成向导——多一个
 * 门禁卡、步骤徽章与上一步/下一步；初始化之后它就是八个可寻址的分区。
 */
export function Wizard({
  snapshot,
  section: sectionName,
  initialized,
  onRefresh,
}: {
  snapshot: ConfigSnapshot;
  section: SectionName;
  initialized: boolean;
  /** 保存之后重读设置；返回新快照，读不到时返回 null（界面据此说「已保存但没读回来」）。 */
  onRefresh: () => Promise<ConfigSnapshot | null>;
}) {
  const navigate = useNavigate();
  const stepIndex = Math.max(
    0,
    steps.findIndex((candidate) => candidate.section === sectionName),
  );
  const step = steps[stepIndex];
  const isLast = stepIndex === steps.length - 1;
  const section = snapshot.sections[step.section];
  const testedName = step.test?.section ?? step.section;
  const testedSection = snapshot.sections[testedName];

  const [drafts, setDrafts] = React.useState<Drafts>({});
  const [chips, setChips] = React.useState<{
    section: SectionName;
    revision: string;
    chip: TestChipState;
  } | null>(null);
  const [pending, setPending] = React.useState<"save" | "test" | "finish" | null>(null);
  const [status, setStatus] = React.useState<{
    tone: "ok" | "warn" | "bad" | "muted";
    text: React.ReactNode;
  } | null>(null);
  const [fieldErrors, setFieldErrors] = React.useState<Record<string, string>>({});
  const [helpOpen, setHelpOpen] = React.useState(false);
  // 门禁在这一屏被填满的那一刻：页面会切到「集成设置」，终点动作随之换成「去面板」。
  const [justInitialized, setJustInitialized] = React.useState(false);

  // 表单不做成 effect 里的状态：草稿带着「它属于哪一节哪个 revision」的钥匙。
  // 换步或保存后（revision 变了）自然回落到服务器最新值 —— 敏感字段永远是空串
  // （掩码只做 placeholder），留空提交就等于不改。测试 chip 同理。
  const formKey = `${step.section}:${section.revision}`;
  const baseValues = initialValues(step, section);
  const values = draftFor(drafts, formKey, () => baseValues);
  const dirty = dirtyKeys(step.fields, baseValues, values);
  const dirtySet = new Set(dirty);
  const isDirty = dirty.length > 0;

  // chip 跟着**被测那一节**的账本走（清单步测的是 JavDB），所以它的钥匙里也要有那一节的
  // revision；改了本步任何一格，上一次的结论就不再代表屏幕上的配置，直接收回。
  const chip =
    chips !== null && chips.section === testedName && chips.revision === testedSection.revision
      ? chips.chip
      : chipFromLastTest(testedSection);

  // 换节时把上一节的回执收掉：它说的是另一张表的事。
  // 在渲染期对齐，而不是放进 effect——后者会多跑一轮级联渲染。
  const [seenSection, setSeenSection] = React.useState(step.section);
  if (seenSection !== step.section) {
    setSeenSection(step.section);
    setStatus(null);
    setFieldErrors({});
    setHelpOpen(false);
    setJustInitialized(false);
  }

  // Telegram 未启用时，四个通知开关先不露脸：为一个没启用的集成先做四个偏好决策
  // 是倒置的（渐进披露）；启用后它们带着已存的值出现。
  const telegramIdle = step.section === "telegram" && values.enabled === false;

  // 换步之后做两件「这一步才开始」的事：
  // ① 焦点交给新卡的标题——SPA 里换的只是同一实例的 props，不管理焦点的话，读屏用户
  //    每按一次「下一步」就迷路一次；首次挂载不抢焦点。
  // ② 重读一次设置——门禁有一半真值在服务端（目录在盘、清单登记），不能等下一次
  //    保存才更新（面板 Freshness 规则的向导版）。
  const headingRef = React.useRef<HTMLHeadingElement>(null);
  const refreshRef = React.useRef(onRefresh);
  React.useEffect(() => {
    refreshRef.current = onRefresh;
  });
  const enteredSection = React.useRef(step.section);
  React.useEffect(() => {
    if (enteredSection.current === step.section) return;
    enteredSection.current = step.section;
    headingRef.current?.focus();
    void refreshRef.current();
  }, [step.section]);

  // 草稿只在内存里：刷新或关页会丢。脏的时候拦一次，不脏就不打扰。
  React.useEffect(() => {
    if (!isDirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [isDirty]);

  const setField = (key: string, value: string | boolean) => {
    setDrafts((current) =>
      setFields(current, formKey, () => baseValues, { [key]: value }),
    );
    setChips(null);
    setFieldErrors((current) => {
      if (!(key in current)) return current;
      const next = { ...current };
      delete next[key];
      return next;
    });
    // 上一次回执说的是改之前那次保存；屏幕一变，它就不再是「你刚按的这颗键」的答案。
    setStatus(null);
  };

  /**
   * saveStep 保存本步（只有脏了才发请求），并顺手把新快照带回去给调用方。
   *
   * 三条调用路径共用它：设置面的「保存本步」、向导的「下一步」、「测试连接」。
   * 这就是「保存不是一件需要单独记着做的事」在代码里的样子。
   */
  const saveStep = async (): Promise<{ ok: boolean; snapshot: ConfigSnapshot | null }> => {
    if (!isDirty) return { ok: true, snapshot: null };

    const invalid = validateValues(step, values);
    setFieldErrors(invalid);
    if (Object.keys(invalid).length > 0) {
      setStatus({ tone: "warn", text: "还有字段不对：改好再保存。" });
      return { ok: false, snapshot: null };
    }

    setPending("save");
    setStatus(null);
    try {
      await api.save(step.section, buildPatch(step, section, values));
      const next = await onRefresh();
      setDrafts((current) => clearDrafts(current, `${step.section}:`));
      if (next === null) {
        setStatus({ tone: "warn", text: "已保存，但没读回最新设置：刷新页面再确认一次。" });
      } else if (next.initialized && !initialized) {
        setJustInitialized(true);
        setStatus({ tone: "ok", text: initializedReceipt(next) });
      } else {
        setStatus({ tone: "ok", text: "已保存本步" });
      }
      return { ok: true, snapshot: next };
    } catch (error) {
      if (error instanceof HttpError && error.isRevisionConflict) {
        const next = await onRefresh();
        setDrafts((current) => clearDrafts(current, `${step.section}:`));
        setStatus({
          tone: "warn",
          text: "这一节已被别处改过：已拉取最新内容，确认后再保存一次。",
        });
        return { ok: false, snapshot: next };
      }
      const field = fieldOf(error, step);
      if (field) setFieldErrors({ [field]: messageOf(error) });
      setStatus({ tone: "bad", text: messageOf(error) });
      return { ok: false, snapshot: null };
    } finally {
      setPending(null);
    }
  };

  /**
   * 测试连接：先保存，再测。
   *
   * 服务端测的是**已保存**的配置（POST 不带请求体）。于是「先测后存」永远在测上一版的
   * 值，屏幕上一个错地址也能拿到绿色结论。把保存放在测试前面，是让这颗按钮说的话与
   * 屏幕说的是同一件事——它同时也是这一步的提交动作。
   */
  const runTest = async () => {
    if (!step.test) return;
    const saved = await saveStep();
    if (!saved.ok) return;

    setPending("test");
    const revisionNow = saved.snapshot?.sections[testedName]?.revision ?? testedSection.revision;
    setChips({ section: testedName, revision: revisionNow, chip: { kind: "testing" } });
    try {
      const result = await api.test(testedName);
      const revision = saved.snapshot?.sections[testedName]?.revision ?? testedSection.revision;
      const at = new Date().toISOString();
      const next: TestChipState = result.ok
        ? { kind: "ok", detail: result.detail, durationMs: result.duration_ms, at }
        : result.code === "not_implemented"
          ? { kind: "placeholder", detail: result.detail }
          : {
              kind: "fail",
              code: result.code ?? "error",
              detail: result.detail,
              durationMs: result.duration_ms,
              at,
            };
      setChips({ section: testedName, revision, chip: next });
    } catch (error) {
      setChips({
        section: testedName,
        revision: testedSection.revision,
        chip: {
          kind: "fail",
          code: codeOf(error),
          detail: messageOf(error),
          at: new Date().toISOString(),
        },
      });
    } finally {
      setPending(null);
    }
  };

  /** 下一步＝保存并前进。保存失败就留在原地——包括字段校验与 revision 冲突。 */
  const goNext = async () => {
    const saved = await saveStep();
    if (!saved.ok) return;
    // 这一保存把门禁填满了：向导到此为止。留在原地把回执说完（终点动作已换成「去面板」），
    // 不把人推进一个已经不存在的「下一步」。
    if (saved.snapshot?.initialized) return;
    const next = steps[stepIndex + 1];
    if (next) navigate(`/settings/${next.section}`);
  };

  /**
   * 完成初始化：保存这一步，重读门禁，然后给一条回执。
   *
   * 初始化本身是门禁的结论（三件套 + 目录在盘），不是一次可以「触发」的事务；以前那颗
   * 按钮因此永远是灰的——门禁真的齐了的时候，页面已经切成「第 N / 8 节」了，所以它
   * 一次也按不着。现在它做一件真事：把你改的间隔存下来、把门禁读一遍，齐了给回执，
   * 没齐就把差的几项列出来并给出链接。按钮不再承诺一个它做不到的动作。
   */
  const finish = async () => {
    setPending("finish");
    try {
      const saved = await saveStep();
      if (!saved.ok) return;
      const next = saved.snapshot ?? (await onRefresh());
      if (next === null) {
        setStatus({ tone: "warn", text: "读不到设置：刷新页面再确认一次。" });
        return;
      }
      if (next.initialized) {
        setJustInitialized(true);
        setStatus({ tone: "ok", text: initializedReceipt(next) });
        return;
      }
      const missing = requirements(next).filter((item) => !item.ok);
      if (missing.length === 0) {
        // 门禁说没齐，却列不出差哪项：两份判据不同步了。给防御句，不渲染「还差 0 项」。
        setStatus({
          tone: "warn",
          text: "屏幕上看不出还差哪项：刷新一次再看（门禁读的是保存后的最新设置）。",
        });
        return;
      }
      setStatus({
        tone: "warn",
        text: (
          <>
            还差 {missing.length} 项才能初始化：
            {missing.map((item, index) => (
              <React.Fragment key={item.key}>
                {index > 0 ? "、" : ""}
                <Link
                  to={`/settings/${item.section}`}
                  className="underline underline-offset-4 hover:text-foreground"
                >
                  {item.short}
                </Link>
              </React.Fragment>
            ))}
          </>
        ),
      });
    } finally {
      setPending(null);
    }
  };

  // 动作行里的那行字：正在保存 → 刚发生的事 → 未保存。它必须活在按钮旁边。
  // 测试失败时那句「该改什么」单独一行、占满宽度：塞在胶囊旁边会被挤成窄条，
  // 而它恰恰是这一刻最该被读到的一句（DESIGN 的 Fixed-Row 同一个理由）。
  const testDetail = chip.kind === "fail" && chip.detail ? chip.detail : null;

  // 终点回执（刚初始化成功那一刻）不是一行 12px 小字：它是全旅程唯一「机器接班」的
  // 时刻，抬到动作行上方，绿三件套、半秒入场；普通回执仍在按钮下面的 StatusLine。
  const receipt =
    justInitialized && status?.tone === "ok" && pending === null ? (
      <div
        role="status"
        className="rounded-lg border border-status-done-border bg-status-done-surface px-3 py-2.5 text-sm font-medium leading-relaxed text-status-done-text motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-1 motion-safe:duration-300"
      >
        {status.text}
      </div>
    ) : null;

  const statusLine =
    pending === "save" ? (
      <StatusLine tone="muted">
        <span className="inline-flex items-center gap-1">
          <Loader2 className="size-3 animate-spin" aria-hidden />
          正在保存…
        </span>
      </StatusLine>
    ) : status && !receipt ? (
      <StatusLine tone={status.tone}>{status.text}</StatusLine>
    ) : isDirty ? (
      <StatusLine tone="muted">未保存 · 下一步与测试会先保存</StatusLine>
    ) : null;

  const railLabel = initialized ? "设置分区" : "向导步骤";

  return (
    <div className="mx-auto grid max-w-5xl gap-6 lg:grid-cols-[230px_1fr]">
      {/* 窄屏：一条可横滑的分区链。以前 lg 以下整条导航是 hidden，手机上「去 Telegram 设置」
          只能靠六个「下一步」穿过去；现在每一节都是一个地址。
          当前项自动滚到链中央：第 8 步时它原本在 350px 视口外，屏上只剩前三步。 */}
      <nav aria-label={railLabel} className="min-w-0 lg:hidden">
        <StepChain stepIndex={stepIndex} snapshot={snapshot} />
      </nav>

      <ol className="hidden space-y-1 lg:block" aria-label={railLabel}>
        {steps.map((candidate, index) => {
          const current = index === stepIndex;
          return (
            <li key={candidate.section}>
              <Link
                to={`/settings/${candidate.section}`}
                aria-current={current ? "step" : undefined}
                className={cn(
                  "flex w-full items-start gap-2.5 rounded-lg px-2 py-2 text-left dense transition-colors",
                  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
                  current
                    ? "bg-background font-medium ring-1 ring-foreground/10"
                    : "text-muted-foreground hover:bg-background/60",
                )}
              >
                <StepDot index={index} configured={isStepConfigured(candidate, snapshot)} current={current} />
                <span className="leading-snug">
                  {candidate.title}
                  {candidate.optional ? (
                    <span className="ml-1 text-xs text-muted-foreground">可跳过</span>
                  ) : null}
                  <span className="sr-only">
                    {isStepConfigured(candidate, snapshot) ? "，已配置" : "，未配置"}
                  </span>
                </span>
              </Link>
            </li>
          );
        })}
      </ol>

      <div className="min-w-0 space-y-4">
        {!snapshot.initialized ? (
          isLast || stepIndex === 0 ? (
            <GateCard snapshot={snapshot} onOpenHelp={() => setHelpOpen(true)} />
          ) : (
            <GateStrip snapshot={snapshot} currentSection={step.section} />
          )
        ) : null}

        <Card className="p-5" aria-busy={pending !== null}>
          <div className="mb-3 flex flex-wrap items-start justify-between gap-2">
            <div className="min-w-0 flex-1">
              <h2 ref={headingRef} tabIndex={-1} className="font-heading text-base font-semibold outline-none">{step.title}</h2>
              <p className="mt-1 max-w-xl dense leading-relaxed text-muted-foreground">
                {step.help}
              </p>
            </div>
            <Badge variant="outline" className="shrink-0">
              {initialized ? `第 ${stepIndex + 1} / ${steps.length} 节` : `第 ${stepIndex + 1} / ${steps.length} 步`}
            </Badge>
          </div>
          <Separator className="mb-4" />

          <div className="space-y-3">
            {step.fields
              .filter((field) => !(telegramIdle && field.key.startsWith("notify_")))
              .map((field) => (
                <FieldRow
                  key={field.key}
                  field={field}
                  value={values[field.key]}
                  section={section}
                  dirty={dirtySet.has(field.key)}
                  error={fieldErrors[field.key]}
                  onChange={(value) => setField(field.key, value)}
                />
              ))}
            {telegramIdle ? (
              <p className="dense text-muted-foreground">
                启用 Telegram 后，这里会出现四个通知开关（下载开始 / 入库完成 / 加工失败 /
                下载停滞），逐事件各自开关。
              </p>
            ) : null}
          </div>

          {step.section === "list_registry" ? (
            <ListAssist
              tokenConfigured={snapshot.sections.javdb.configured}
              onPick={(id, name) => {
                setField("list_id", id);
                if (name) setField("name", name);
              }}
            />
          ) : null}

          {step.section === "qbittorrent" ? (
            <AutorunAssist configured={section.configured} />
          ) : null}

          {step.section === "list_registry" ? (
            <RegisteredListHint entries={entriesOf(section)} />
          ) : null}

          <Separator className="my-4" />

          <div className="space-y-2">
            {receipt}
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2 md:justify-between">
              <div className="flex flex-wrap items-center gap-2">
                {initialized && justInitialized ? null : initialized ? (
                  <Button variant="ghost" size="sm" className={ACTION} asChild>
                    <Link to="/">回到面板</Link>
                  </Button>
                ) : stepIndex === 0 ? (
                  <Button variant="ghost" size="sm" className={ACTION} disabled>
                    <ChevronLeft aria-hidden />
                    上一步
                  </Button>
                ) : (
                  <Button variant="ghost" size="sm" className={ACTION} asChild>
                    <Link to={`/settings/${steps[stepIndex - 1].section}`}>
                      <ChevronLeft aria-hidden />
                      上一步
                    </Link>
                  </Button>
                )}

                {step.test ? (
                  <>
                    <Button
                      variant="outline"
                      size="sm"
                      className={ACTION}
                      onClick={runTest}
                      disabled={pending !== null}
                    >
                      {pending === "test" ? <Loader2 className="animate-spin" aria-hidden /> : null}
                      {step.test.label}
                    </Button>
                    <TestChip state={chip} />
                  </>
                ) : null}
              </div>

              <div className="flex flex-wrap items-center gap-2 max-md:w-full md:ml-auto md:justify-end">
                {!initialized && step.optional ? (
                  <Button variant="ghost" size="sm" className={ACTION} asChild>
                    <Link to={`/settings/${steps[stepIndex + 1]?.section ?? step.section}`}>跳过本步</Link>
                  </Button>
                ) : null}

                {initialized && justInitialized ? (
                  // 刚刚齐了：这一刻唯一有意义的动作是去看面板，而不是一个灰着的「保存本步」。
                  <Button size="sm" className={cn(ACTION, "max-md:flex-1")} asChild>
                    <Link to="/">
                      去面板
                      <ChevronRight aria-hidden />
                    </Link>
                  </Button>
                ) : initialized ? (
                  <Button
                    size="sm"
                    className={ACTION}
                    onClick={() => void saveStep()}
                    disabled={pending !== null || !isDirty}
                  >
                    {pending === "save" ? <Loader2 className="animate-spin" aria-hidden /> : null}
                    保存本步
                  </Button>
                ) : isLast ? (
                  <Button
                    size="sm"
                    className={cn(ACTION, "max-md:flex-1")}
                    onClick={finish}
                    disabled={pending !== null}
                  >
                    {pending === "finish" || pending === "save" ? (
                      <Loader2 className="animate-spin" aria-hidden />
                    ) : null}
                    完成初始化
                  </Button>
                ) : (
                  <Button
                    size="sm"
                    className={cn(ACTION, "max-md:flex-1")}
                    onClick={goNext}
                    disabled={pending !== null}
                  >
                    下一步
                    <ChevronRight aria-hidden />
                  </Button>
                )}
              </div>
            </div>
            {testDetail || statusLine ? (
              <div className="space-y-1">
                {testDetail ? (
                  <p className="text-xs leading-relaxed text-status-danger-text">{testDetail}</p>
                ) : null}
                {statusLine}
              </div>
            ) : null}
          </div>
        </Card>

        <div className="flex flex-wrap items-center justify-between gap-2 px-1">
          <button
            type="button"
            onClick={() => setHelpOpen(true)}
            className="inline-flex min-h-11 items-center rounded-sm text-xs text-muted-foreground underline underline-offset-4 hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring md:min-h-0"
          >
            术语
          </button>
          {isDirty ? (
            <span className="text-xs text-muted-foreground">草稿只在这台浏览器里，刷新会丢</span>
          ) : null}
        </div>
      </div>

      {helpOpen ? (
        <WizardHelp
          onClose={() => setHelpOpen(false)}
        />
      ) : null}
    </div>
  );
}

/**
 * StepChain 是窄屏那条可横滑的步骤链。
 *
 * 它有两个以前没有的东西：当前项自动滚到链中央（第 8 步原本整个在视口外，屏上只剩
 * 前三步，导航等于没有），以及给读屏器的「已配置 / 未配置」——小圆点里的 ✓ 是
 * aria-hidden 的，状态以前只存在于颜色与字形里。
 */
function StepChain({ stepIndex, snapshot }: { stepIndex: number; snapshot: ConfigSnapshot }) {
  const listRef = React.useRef<HTMLOListElement>(null);

  React.useEffect(() => {
    const list = listRef.current;
    const current = list?.querySelector<HTMLElement>('[aria-current="step"]');
    if (!list || !current) return;
    // 直接算 scrollLeft，不用 scrollIntoView：后者会把整页也竖着滚一下。
    const target = current.offsetLeft - (list.clientWidth - current.offsetWidth) / 2;
    list.scrollLeft = Math.max(0, target);
  }, [stepIndex]);

  return (
    <ol ref={listRef} className="-mx-1 flex gap-1 overflow-x-auto px-1 py-1">
      {steps.map((candidate, index) => {
        const current = index === stepIndex;
        return (
          <li key={candidate.section} className="shrink-0">
            <Link
              to={`/settings/${candidate.section}`}
              aria-current={current ? "step" : undefined}
              className={cn(
                "flex h-11 items-center gap-1.5 rounded-lg border px-2.5 dense transition-colors",
                "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
                current
                  ? "border-border bg-background font-medium"
                  : "border-transparent text-muted-foreground",
              )}
            >
              <StepDot index={index} configured={isStepConfigured(candidate, snapshot)} current={current} />
              {candidate.title}
              <span className="sr-only">
                {isStepConfigured(candidate, snapshot) ? "，已配置" : "，未配置"}
              </span>
            </Link>
          </li>
        );
      })}
    </ol>
  );
}

function isStepConfigured(candidate: StepSpec, snapshot: ConfigSnapshot): boolean {
  // 「完成」步没有自己的表：它反映门禁，而不是 app_config 那一行。
  return candidate.section === "app"
    ? snapshot.initialized
    : snapshot.sections[candidate.section].configured;
}

/** StepDot 是导航里的那一小圈：配好了是勾，否则是步号。字形不进读屏器，状态由链接自己的文字说。 */
function StepDot({
  index,
  configured,
  current,
}: {
  index: number;
  configured: boolean;
  current: boolean;
}) {
  return (
    <span
      className={cn(
        "grid size-5 shrink-0 place-items-center rounded-full border text-xs tabular-nums",
        configured && "border-status-done-border bg-status-done-surface text-status-done-text",
        current && !configured && "border-primary bg-primary text-primary-foreground",
      )}
      aria-hidden
    >
      {configured ? "✓" : index + 1}
    </span>
  );
}

function FieldRow({
  field,
  value,
  section,
  dirty,
  error,
  onChange,
}: {
  field: FieldSpec;
  value: string | boolean | undefined;
  section: SectionState;
  dirty: boolean;
  error?: string;
  onChange: (value: string | boolean) => void;
}) {
  const id = React.useId();

  if (field.kind === "bool") {
    return (
      <Toggle
        id={id}
        label={field.label}
        desc={field.hint}
        checked={Boolean(value)}
        dirty={dirty}
        onChange={onChange}
      />
    );
  }

  const secret = field.kind === "secret";
  const numeric = field.kind === "number" || field.kind === "duration";
  const masked = section.data[field.key];
  const placeholder = secret ? maskedValue(masked, field.placeholder) : field.placeholder;

  return (
    <Field
      id={id}
      label={field.label}
      hint={field.hint}
      dirty={dirty}
      error={error}
    >
      <TextInput
        id={id}
        aria-describedby={field.hint ? `${id}-hint` : undefined}
        aria-invalid={error ? true : undefined}
        mono={field.mono}
        type={secret ? "password" : numeric ? "number" : "text"}
        inputMode={numeric ? "numeric" : undefined}
        min={field.min}
        max={field.max}
        step={numeric ? 1 : undefined}
        placeholder={placeholder}
        autoComplete={secret ? "new-password" : "off"}
        value={String(value ?? "")}
        onChange={(event) => onChange(event.target.value)}
        className={secret ? "placeholder:font-mono" : undefined}
      />
    </Field>
  );
}

/** GateCard 是规格 §8.3 的解释卡：三件套齐了循环才会启动。只在第一步与最后一步展开。 */
function GateCard({
  snapshot,
  onOpenHelp,
}: {
  snapshot: ConfigSnapshot;
  onOpenHelp: () => void;
}) {
  return (
    <Card className="p-4">
      <div className="flex items-start gap-3">
        <div className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-full bg-muted">
          <CircleHelp className="size-4 text-muted-foreground" aria-hidden />
        </div>
        <div className="min-w-0">
          <h2 className="text-sm font-semibold">还没初始化</h2>
          <p className="mt-1 dense leading-relaxed text-muted-foreground">
            主线三件套齐了，循环才会启动：JavDB 可读且清单登记就位、qBittorrent 可连、
            Jellyfin 可写。Jellyfin 那一项还包括媒体库目录在这台机器上可见——三个容器把
            同一个 host 目录挂到同一路径。在那之前这个页面只有向导，面板进不去。
          </p>
          <ul className="mt-2.5 space-y-1">
            {requirements(snapshot).map((item) => (
              <CheckLine key={item.key} item={item} />
            ))}
          </ul>
          <button
            type="button"
            onClick={onOpenHelp}
            className="mt-2.5 inline-flex min-h-11 items-center rounded-sm text-xs text-muted-foreground underline underline-offset-4 hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring md:min-h-0"
          >
            三件套、清单 id、测试连接分别是什么意思
          </button>
        </div>
      </div>
    </Card>
  );
}

/**
 * GateStrip 是中间几步的那一行。
 *
 * 同一张五行卡在八步上重复，会把「还没初始化」从一个状态变成背景噪音——而且它用的
 * 是琥珀（进度/警告）色，一分钟里看八遍就不再是信号。中间步只留一句「差什么」，
 * 点一下就到能修它的那一节。
 */
function GateStrip({
  snapshot,
  currentSection,
}: {
  snapshot: ConfigSnapshot;
  currentSection: SectionName;
}) {
  const all = requirements(snapshot);
  const missing = all.filter((item) => !item.ok);
  const first = missing[0];

  return (
    <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 rounded-xl bg-card px-3 py-2.5 ring-1 ring-foreground/10">
      {/* 计数变化（保存后 4/5→5/5）没有别的出口，这行自己播报。 */}
      <p aria-live="polite" className="dense text-muted-foreground">
        还没初始化 · 三件套 {all.length - missing.length}/{all.length}
        {first ? ` · 差 ${first.short}` : ""}
      </p>
      {/* 「去 X」不指向当前步自己：站在清单登记步时链接会变成一次空操作。 */}
      {first && first.section !== currentSection ? (
        <Link
          to={`/settings/${first.section}`}
          className="shrink-0 rounded-sm text-xs text-foreground underline underline-offset-4 hover:text-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
        >
          去 {sectionTitle(first.section)}
        </Link>
      ) : null}
    </div>
  );
}

type Requirement = {
  key: string;
  /** 整句：解释卡里用，读者需要知道「哪一种没满足」。 */
  label: string;
  /** 短语：那一行与「还差 N 项」的句子里用，窄屏读得完。 */
  short: string;
  ok: boolean;
  section: SectionName;
};

/** requirements 是门禁的五个判据（四个配置 + 目录在盘），单一真相，卡片与那一行都读它。 */
function requirements(snapshot: ConfigSnapshot): Requirement[] {
  const { javdb, list_registry, qbittorrent, jellyfin } = snapshot.sections;
  const wantWatch = entriesOf(list_registry).find((entry) => entry.purpose === "want_watch");
  const mediaOK = jellyfin.configured && snapshot.media_root_visible;
  return [
    {
      key: "javdb",
      label: javdb.configured ? "JavDB token 已保存" : "JavDB token 还没保存",
      short: "JavDB token",
      ok: javdb.configured,
      section: "javdb",
    },
    {
      key: "list",
      label: wantWatch ? `清单 #${wantWatch.list_id} 已登记` : "清单还没登记",
      short: "清单登记",
      ok: Boolean(wantWatch),
      section: "list_registry",
    },
    {
      key: "qbittorrent",
      label: qbittorrent.configured ? "qBittorrent 已配置" : "qBittorrent 还没配置",
      short: "qBittorrent",
      ok: qbittorrent.configured,
      section: "qbittorrent",
    },
    {
      key: "jellyfin",
      label: jellyfin.configured ? "Jellyfin 已配置" : "Jellyfin 还没配置",
      short: "Jellyfin",
      ok: jellyfin.configured,
      section: "jellyfin",
    },
    {
      key: "media",
      label: !jellyfin.configured
        ? "媒体库目录（Jellyfin 步里填）"
        : snapshot.media_root_visible
          ? "媒体库目录在盘可见"
          : "媒体库目录不在盘：三个容器要挂同一路径，去宿主机核对挂载",
      short: "媒体库目录在盘",
      ok: mediaOK,
      section: "jellyfin",
    },
  ];
}

function sectionTitle(section: SectionName): string {
  return steps.find((step) => step.section === section)?.title ?? section;
}

function CheckLine({ item }: { item: Requirement }) {
  if (item.ok) {
    return (
      <li className="flex items-center gap-2 dense">
        <span
          className="grid size-4 shrink-0 place-items-center rounded-full border border-status-done-border bg-status-done-surface text-xs text-status-done-text"
          aria-hidden
        >
          ✓
        </span>
        <span className="min-w-0">{item.label}</span>
      </li>
    );
  }
  return (
    <li className="flex items-center gap-2 dense">
      {/* 待办不是警告：未满足的圆点用中性纸白（Gate-Once 的同一理由——琥珀留给
          真正的警告，五颗「!」齐刷刷亮琥珀就把信号磨平了）。 */}
      <span
        className="grid size-4 shrink-0 place-items-center rounded-full border border-border bg-background text-xs text-muted-foreground"
        aria-hidden
      >
        !
      </span>
      <span className="min-w-0 text-muted-foreground">{item.label}</span>
      <Link
        to={`/settings/${item.section}`}
        aria-label={`去补 ${item.short}`}
        className="inline-flex min-h-11 shrink-0 items-center rounded-sm text-xs underline underline-offset-4 hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring md:min-h-0"
      >
        去补
      </Link>
    </li>
  );
}

/**
 * WizardHelp 是向导的术语浮层：复用抽屉/帮助那套模态（portal 到 body、外壳 inert、
 * Tab 循环、Esc、焦点归还），内容换成向导自己的四个词。
 *
 * 面板上的 `?` 以前在向导里打不开（入口挂在「面板且已初始化」上），于是第一次跑
 * 向导的人恰好是最需要词表的那一个，却唯独拿不到它。
 */
function WizardHelp({ onClose }: { onClose: () => void }) {
  const closeRef = React.useRef<HTMLButtonElement>(null);
  const dialogRef = React.useRef<HTMLElement>(null);
  const returnRef = React.useRef<HTMLElement | null>(null);

  React.useEffect(() => {
    returnRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const releaseShell = holdShellInert();
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    closeRef.current?.focus();

    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        onClose();
        return;
      }
      if (event.key !== "Tab" || !dialogRef.current) return;
      const items = focusablesIn(dialogRef.current);
      if (items.length === 0) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      } else if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => {
      window.removeEventListener("keydown", onKey, true);
      document.body.style.overflow = previousOverflow;
      releaseShell();
      const back = returnRef.current;
      returnRef.current = null;
      if (back?.isConnected) back.focus();
    };
  }, [onClose]);

  return <ShortcutsHelp onClose={onClose} closeRef={closeRef} dialogRef={dialogRef} variant="wizard" />;
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

function initialValues(step: StepSpec, section: SectionState): Record<string, string | boolean> {
  const values: Record<string, string | boolean> = { ...step.fixed };
  for (const field of step.fields) {
    values[field.key] = kindHandlers[field.kind].initial(field, section);
  }
  return values;
}

function buildPatch(
  step: StepSpec,
  section: SectionState,
  values: Record<string, string | boolean>,
): Record<string, unknown> {
  const patch: Record<string, unknown> = { revision: section.revision, ...step.fixed };
  for (const field of step.fields) {
    const [include, value] = kindHandlers[field.kind].payload(values[field.key]);
    if (include) patch[field.key] = value;
  }
  return patch;
}

/** validateValues 先把「填了但不是个数 / 超出范围」拦下来，省得为它跑一趟服务器。 */
function validateValues(
  step: StepSpec,
  values: Record<string, string | boolean>,
): Record<string, string> {
  const errors: Record<string, string> = {};
  for (const field of step.fields) {
    if (field.kind !== "number" && field.kind !== "duration") continue;
    const raw = String(values[field.key] ?? "").trim();
    if (raw === "") continue; // 省略＝不改
    const value = Number(raw);
    if (!Number.isFinite(value)) {
      errors[field.key] = "请填数字";
    } else if (field.min !== undefined && value < field.min) {
      errors[field.key] = `至少 ${field.min}`;
    } else if (field.max !== undefined && value > field.max) {
      errors[field.key] = `最多 ${field.max}`;
    }
  }
  return errors;
}

/** fieldOf 从 400 的文案里认回字段名（服务端的 ValidationError 形如「字段: 说明」）。 */
function fieldOf(error: unknown, step: StepSpec): string | null {
  const match = /^([a-z_]+): /.exec(messageOf(error));
  if (!match) return null;
  return step.fields.some((field) => field.key === match[1]) ? match[1] : null;
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

/** 上一次测试的结论：人话 + 相对时间，绝对时间与机器码进 TestChip 的 title。 */
function chipFromLastTest(section: SectionState): TestChipState {
  const lastTest = section.last_test;
  if (!lastTest) return { kind: "idle" };
  if (lastTest.ok) {
    return { kind: "ok", at: lastTest.at };
  }
  const [code, ...rest] = (lastTest.error ?? "error").split(":");
  const detail = rest.join(":").trim();
  return {
    kind: "fail",
    code: code.trim(),
    detail: detail === "" ? undefined : detail,
    at: lastTest.at,
  };
}

/** initializedReceipt 说清「现在开始跑的是什么」，而不是只说一句「成功」。 */
function initializedReceipt(snapshot: ConfigSnapshot): string {
  const data = snapshot.sections.app.data as Record<string, unknown>;
  const parts = [
    ["清单轮询", formatInterval(data.poll_interval)],
    ["对账", formatInterval(data.reconcile_interval)],
    ["副作用", formatInterval(data.sidefx_interval)],
  ]
    .filter(([, text]) => text !== null)
    .map(([label, text]) => `${label} ${text}`);
  return parts.length > 0
    ? `已初始化：${parts.join("、")}，三条循环已开始跑。`
    : "已初始化：三条循环已开始跑。";
}

/** ListAssist 是清单步的助手：探测已有清单 → 按 id 选中，或一键代建，或手动填 id 兜底。 */
function ListAssist({
  tokenConfigured,
  onPick,
}: {
  tokenConfigured: boolean;
  onPick: (id: string, name?: string) => void;
}) {
  const [lists, setLists] = React.useState<JavDBList[] | null>(null);
  const [pending, setPending] = React.useState<"probe" | "create" | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  const probe = async () => {
    setPending("probe");
    setError(null);
    setNotice(null);
    try {
      const result = await api.javdbLists();
      setLists(result.lists);
      if (result.lists.length === 0) {
        setNotice("你的账户还没有清单，可以一键创建「想看清单」。");
      }
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setPending(null);
    }
  };

  const create = async () => {
    setPending("create");
    setError(null);
    setNotice(null);
    try {
      // 名字/描述留空：默认值由服务端单点提供（「想看清单」/「oh-my-mdc 自动创建」），
      // 前端不再各存一份字面量。
      const created = await api.javdbCreateList("", "");
      onPick(created.id, created.name);
      setNotice(`已创建清单「${created.name}」（id ${created.id}），确认后下一步会保存。`);
    } catch (err) {
      // 同名拒等业务错原样透出：用户需要看到服务端的原话才知道改什么名字。
      setError(messageOf(err));
    } finally {
      setPending(null);
    }
  };

  // 只把「名字里带想看」的那张作为**推荐**（不自动选中）：判定永远靠 id，名字只是快照
  // （规格 §5.1/P2-4）。没有匹配时不给推荐，绝不拿默认清单冒充「想看清单」。
  const suggested = lists?.find((list) => list.name.includes("想看"));

  return (
    <div className="mt-4 space-y-2 border-t border-border pt-4">
      <div className="dense font-medium">从 JavDB 选清单</div>
      <p className="max-w-xl text-xs leading-relaxed text-muted-foreground">
        探测会列出你自建的清单，按 id 选中登记；也可以一键创建「想看清单」，或手动填 id 兜底。
      </p>

      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          className={ACTION}
          onClick={probe}
          disabled={pending !== null || !tokenConfigured}
          title={tokenConfigured ? undefined : "先在 JavDB 步保存 token"}
        >
          {pending === "probe" ? <Loader2 className="animate-spin" aria-hidden /> : <RefreshCw aria-hidden />}
          探测已有清单
        </Button>
        <Button
          variant="outline"
          size="sm"
          className={ACTION}
          onClick={create}
          disabled={pending !== null || !tokenConfigured}
          title={tokenConfigured ? undefined : "先在 JavDB 步保存 token"}
        >
          {pending === "create" ? <Loader2 className="animate-spin" aria-hidden /> : <Plus aria-hidden />}
          一键创建「想看清单」
        </Button>
        {!tokenConfigured ? (
          <Link
            to="/settings/javdb"
            className="inline-flex min-h-11 items-center text-xs text-muted-foreground underline underline-offset-4 hover:text-foreground md:min-h-0"
          >
            先去保存 JavDB token
          </Link>
        ) : null}
        {suggested ? (
          <Button
            variant="ghost"
            size="sm"
            className={ACTION}
            onClick={() => onPick(suggested.id, suggested.name)}
          >
            推荐「{suggested.name}」
          </Button>
        ) : null}
      </div>

      {lists && lists.length > 0 ? (
        <ul className="divide-y rounded-md border bg-background">
          {lists.map((list) => (
            <li key={list.id}>
              <button
                type="button"
                onClick={() => onPick(list.id, list.name)}
                className="flex min-h-11 w-full items-center justify-between gap-2 px-2.5 py-2 text-left text-[0.8125rem] hover:bg-accent/50 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring md:min-h-0 md:text-xs"
              >
                <span className="min-w-0 truncate">
                  <span className="font-mono text-xs text-muted-foreground">{list.id}</span>{" "}
                  {list.name}
                  {list.is_default ? <span className="ml-1 text-muted-foreground">默认</span> : null}
                </span>
                <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                  {list.movies_count} 项
                </span>
              </button>
            </li>
          ))}
        </ul>
      ) : null}

      {notice ? <p className="text-xs text-status-done-text">{notice}</p> : null}
      {error ? <p className="text-xs text-status-danger-text">{error}</p> : null}
    </div>
  );
}

function RegisteredListHint({ entries }: { entries: ListEntry[] }) {
  const wantWatch = entries.find((entry) => entry.purpose === "want_watch");
  if (!wantWatch) {
    return (
      <p className="mt-3 text-xs text-muted-foreground">
        还没登记清单：填 id 保存后，轮询只认这个 id，名字改了也不影响。
      </p>
    );
  }
  return (
    <p className="mt-3 text-xs text-muted-foreground">
      当前登记：<span className="font-mono">#{wantWatch.list_id}</span>
      {wantWatch.name ? ` · ${wantWatch.name}` : ""}（用途：想看）
    </p>
  );
}

/**
 * AutorunAssist 是 qbt 步的 autorun 共存改写面板（ADR-0007、规格 §5.4）。
 *
 * 两段式：先「预览改写」拿完整的前后对比，人看过才允许「确认改写并写入」。
 * 只写 autorun_program；`autorun_on_torrent_added_*` 与 autorun 开关都不碰，
 * 开关关着时只给警告（命令写进去也不会执行）。
 */
function AutorunAssist({ configured }: { configured: boolean }) {
  const [plan, setPlan] = React.useState<AutorunPlan | null>(null);
  const [pending, setPending] = React.useState<"preview" | "apply" | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  const preview = async () => {
    setPending("preview");
    setError(null);
    setNotice(null);
    try {
      setPlan(await api.qbtAutorunPlan());
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setPending(null);
    }
  };

  const apply = async () => {
    setPending("apply");
    setError(null);
    setNotice(null);
    try {
      const applied = await api.qbtAutorunApply();
      setPlan(applied);
      setNotice(
        applied.applied
          ? "已写入 qBittorrent：autorun 现在是共存命令，moviepilot 的原有回调照旧。"
          : "现值已含本项目回调，没有改写。",
      );
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setPending(null);
    }
  };

  const canApply = Boolean(plan && !plan.skip && plan.changed);

  return (
    <div className="mt-4 space-y-2 border-t border-border pt-4">
      <div className="dense font-medium">autorun 共存改写</div>
      <p className="max-w-xl text-xs leading-relaxed text-muted-foreground">
        qBittorrent 的 autorun 只能写一条命令，本项目用
        <code className="mx-1 font-mono">/bin/sh -c</code>
        把现有命令和回调拼在一起：外层双引号、payload 内单引号、
        <code className="mx-1 font-mono">;</code>
        让两条命令各自执行。只会写 autorun_program，不碰 autorun_on_torrent_added_*。
      </p>

      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          className={ACTION}
          onClick={preview}
          disabled={pending !== null || !configured}
          title={configured ? undefined : "先保存本步再预览"}
        >
          {pending === "preview" ? <Loader2 className="animate-spin" aria-hidden /> : <RefreshCw aria-hidden />}
          预览改写
        </Button>
        <Button
          variant="outline"
          size="sm"
          className={ACTION}
          onClick={apply}
          disabled={pending !== null || !canApply}
          title={canApply ? undefined : "先预览，且现值需要改"}
        >
          {pending === "apply" ? <Loader2 className="animate-spin" aria-hidden /> : null}
          确认改写并写入
        </Button>
        {plan ? (
          <Badge variant="outline">
            {plan.skip ? "已含本项目回调" : plan.changed ? "需要改写" : "无需改动"}
          </Badge>
        ) : null}
      </div>

      {plan ? (
        <div className="space-y-2">
          {plan.warning ? (
            <p className="rounded-md border border-status-progress-border bg-status-progress-surface px-2.5 py-1.5 text-xs leading-relaxed text-status-progress-text">
              {plan.warning}
            </p>
          ) : null}

          <AutorunLine label="现在" value={plan.before || "（空）"} />
          <AutorunLine label="改写后" value={plan.after || "（空）"} highlight />

          <p className="text-xs text-muted-foreground">
            {plan.skip
              ? "现值已经包含本项目回调：再保存也不会重复改写（幂等）。"
              : "确认后只写 autorun_program 这一个偏好键；原来的 moviepilot 命令原样保留。"}
          </p>
        </div>
      ) : null}

      {notice ? <p className="text-xs text-status-done-text">{notice}</p> : null}
      {error ? <p className="text-xs text-status-danger-text">{error}</p> : null}
    </div>
  );
}

/** AutorunLine 是一行完整命令：不截断、不换行折半，横向滚动让人看全。 */
function AutorunLine({
  label,
  value,
  highlight,
}: {
  label: string;
  value: string;
  highlight?: boolean;
}) {
  return (
    <div>
      <div className="mb-1 flex items-baseline justify-between gap-2 text-xs text-muted-foreground">
        <span>{label}</span>
        {value.length > 40 ? <span className="shrink-0 md:hidden">长命令可左右滑动</span> : null}
      </div>
      <pre
        className={cn(
          "overflow-x-auto rounded-md border px-2.5 py-1.5 font-mono text-xs leading-relaxed whitespace-pre",
          highlight ? "border-status-done-border bg-status-done-surface" : "bg-background",
        )}
      >
        {value}
      </pre>
    </div>
  );
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function codeOf(error: unknown): string {
  return error instanceof HttpError ? error.code : "network";
}
