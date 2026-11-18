import * as React from "react";
import { Download, Loader2, Plus } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Card, CardAction, CardContent, CardHeader } from "~/components/ui/card";
import { Notice, TextInput, Field } from "~/components/form";
import { RowAction } from "~/components/row-action";
import { formatWhen } from "~/lib/time";
import {
  subscriptionAddBody,
  subscriptionBackfillDays,
  subscriptionSubtitle,
} from "~/lib/subscription";
import { skipAcceptText, skipReasonLabel, skipRowLine, skipsBadge, type SubscriptionSkip } from "~/lib/skips";
import {
  collectedImportItems,
  importIDs,
  importReceipts,
  importResultLabel,
  type ImportOutcome,
} from "~/lib/collected";
import type { ReceiptDraft } from "~/lib/receipts";
import {
  api,
  type CollectedActresses,
  type Subscription,
  type SubscriptionBackfillBody,
  type SubscriptionList,
} from "~/lib/api";

/**
 * 订阅区（规格 §8.2/§6.4，GLOSSARY「女优订阅 / 跳过 / 收下」）。
 *
 * 一块四件东西：添加（贴 id / 链接）、从 App 收藏导入、每条订阅一行（水位线 + 启停 +
 * 回灌）、被筛子排掉的折叠格（可「收下」）。它们共享同一个外壳（卡片标题 + 计数），
 * 也共享同两个动作——各自单独搬进 panel.tsx 只会让那个文件继续长。
 *
 * 这里只有 UI：判定与文案都在 `~/lib/subscription`、`~/lib/skips`、`~/lib/collected`。
 */

/**
 * SubscriptionActions 是订阅区每颗按钮都要的那两件事：一条回执（播报 + 留在卡片上）
 * 与一次重读（面板与跳过表都靠同一份快照刷新）。
 *
 * 它们是**同一件事的两半**——按下一个动作的结局要说出来，说完了要让屏幕上的数字跟着变
 * ——所以一路只递一份，不递两个参数：五个组件（Panel/Row/折叠格/添加/导入）签名统一，
 * 少一处就能少漂一处。
 */
export type SubscriptionActions = {
  onReceipt: (draft: ReceiptDraft) => void;
  onRefresh: () => Promise<void>;
};

/**
 * SubscriptionPanel 是「订阅」区（规格 §8.2/§6.4，GLOSSARY「女优订阅」）。
 *
 * 只列**已订阅**的女优：上游没有按名字搜女优的端点（§13 实测），所以这里没有搜索框，
 * 入口只有「贴 id / 女优页链接」。每行三样：她是谁（名字快照 + 水位线 + 带来 N 部）、
 * 启停、回灌。
 *
 * 回灌之所以要按钮：一条订阅的起追日（floor_date）是建订阅当天，她更早的作品它永远
 * 不看；想追历史只能显式回灌一次（§6.4 回灌），它把起追日往前挪。三档预设
 * （90 天 / 365 天 / 全部）覆盖「最近三个月 / 一年 / 从头」，再加一格自由天数——30 天
 * 的那一下是**有效**的（新订阅的起追日就是今天），面板因此不再拿回看窗口卡它。
 * 点完之后下界会写进行上，窗口扫完由循环清掉——面板不需要第二个「取消回灌」的入口。
 */
export function SubscriptionPanel({
  subscriptions,
  subscriptionSkips,
  javdbConfigured,
  actions,
}: {
  subscriptions: SubscriptionList | null;
  /** 最近被排掉的（null＝那一条读不到，那块折叠格不渲染）。 */
  subscriptionSkips: Record<number, SubscriptionSkip[] | null>;
  /** JavDB 配好了没有（「从 App 收藏导入」要凭据，没配就整颗禁掉）。 */
  javdbConfigured: boolean;
  /** 回执与重读：这块里每颗按钮都要的那两件事。 */
  actions: SubscriptionActions;
}) {
  // 读不到就不渲染：空卡片会让人以为「一位都没订阅」，而那是另一个事实。
  if (!subscriptions) return null;

  const rows = subscriptions.actresses;

  return (
    <Card role="region" aria-labelledby="subscriptions-heading">
      <CardHeader className="max-sm:grid-cols-1">
        <div>
          <h2 id="subscriptions-heading" className="font-heading text-base font-medium">
            订阅
          </h2>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
            盯住女优新作的订阅：贴她的 JavDB id 或女优页链接，或者从 App 收藏里勾
            （上游没有按名字搜的端点）。新订阅从建订阅当天开始追，更早的历史要按「回灌」才翻。
          </p>
        </div>
        <CardAction className="max-sm:col-start-1 max-sm:row-start-2">
          <span className="text-xs tabular-nums text-muted-foreground">{rows.length} 位女优</span>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <SubscribeForm actions={actions} />
        <CollectedImport javdbConfigured={javdbConfigured} actions={actions} />
        {rows.length === 0 ? (
          <p className="dense text-muted-foreground">
            还没订阅任何女优。贴一个 id 或女优页链接，她的新作就会走与清单轮询同一条流水线。
          </p>
        ) : (
          <div className="divide-y">
            {rows.map((row) => (
              <SubscriptionRow
                key={row.id}
                row={row}
                skips={subscriptionSkips[row.id] ?? null}
                actions={actions}
              />
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

/** SubscriptionRow 是订阅区的一行：身份、水位线与「带来 N 部」，加回灌与启停两组动作。 */
function SubscriptionRow({
  row,
  skips,
  actions,
}: {
  row: Subscription;
  /** 最近被排掉的（null＝那一条读不到，这块不渲染）。 */
  skips: SubscriptionSkip[] | null;
  actions: SubscriptionActions;
}) {
  const { onReceipt, onRefresh } = actions;
  const [busy, setBusy] = React.useState<"toggle" | "backfill" | null>(null);
  // pending 记住按的是哪一颗：回灌有四颗按钮，转圈要转在被按的那一颗上。
  const [pending, setPending] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [daysInput, setDaysInput] = React.useState("");
  const daysID = React.useId();
  const days = subscriptionBackfillDays(daysInput);

  const name = row.actor.name || row.actor.javdb_actor_id;

  const toggle = async () => {
    if (busy) return;
    setBusy("toggle");
    setError(null);
    try {
      const result = await api.setSubscriptionEnabled(row.id, !row.enabled);
      onReceipt({
        title: name,
        tone: "ok",
        verb: result.actress.enabled ? "恢复" : "停用",
        text: result.actress.enabled
          ? "已恢复追新，已建任务照走完"
          : "已停用追新；已建任务照常下载、入库",
      });
      await onRefresh();
    } catch (cause: unknown) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(null);
    }
  };

  const backfill = async (body: SubscriptionBackfillBody, key: string) => {
    if (busy) return;
    setBusy("backfill");
    setPending(key);
    setError(null);
    try {
      const result = await api.backfillSubscription(row.id, body);
      onReceipt({
        title: name,
        tone: "ok",
        verb: "回灌",
        text: result.backfill_from
          ? `回灌自 ${result.backfill_from}，下一轮把她那段作品过一遍`
          : "回灌窗口已设好，下一轮过一遍",
      });
      await onRefresh();
    } catch (cause: unknown) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(null);
      setPending(null);
    }
  };

  const backfillDays = async (event: React.FormEvent) => {
    event.preventDefault();
    if (days === null) return;
    await backfill({ days }, "days");
  };

  return (
    <div
      data-subscription={row.actor.javdb_actor_id}
      className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:flex-wrap sm:items-start sm:justify-between sm:gap-x-4"
    >
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-baseline gap-x-2 font-medium">
          {name}
          {row.actor.name ? (
            <span className="text-xs font-normal text-muted-foreground tabular-nums">
              {row.actor.javdb_actor_id}
            </span>
          ) : null}
          {row.enabled ? null : <Badge variant="outline">已停用</Badge>}
        </div>
        <p className="dense tabular-nums text-muted-foreground">{subscriptionSubtitle(row)}</p>
        <SkippedBlock row={row} skips={skips} actions={actions} />
        {error ? (
          <Notice tone="bad" onClose={() => setError(null)}>
            {error}
          </Notice>
        ) : null}
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        {/* 回灌是一组：三档预设 + 一格天数。天数那一格只判正整数——「这一次是不是
            空操作」要比起追日算出落点，那是服务端的账（被起追日盖住时 400，message 原样
            落在这一行的错误条上）。 */}
        <span className="dense text-muted-foreground">回灌</span>
        {BACKFILL_PRESETS.map((preset) => (
          <RowAction
            key={preset.key}
            onClick={() => void backfill(preset.body, preset.key)}
            disabled={busy !== null}
            aria-label={`回灌 ${name} ${preset.aria}`}
            title={preset.title ?? `把取数下界往回拨${preset.aria}，扫完自动清掉`}
          >
            {pending === preset.key ? (
              <Loader2 className="size-3 animate-spin" aria-hidden />
            ) : null}
            {preset.label}
          </RowAction>
        ))}
        <form onSubmit={backfillDays} className="flex items-center gap-1">
          <label htmlFor={daysID} className="sr-only">
            回灌 {name} 多少天
          </label>
          <TextInput
            id={daysID}
            value={daysInput}
            onChange={(event) => setDaysInput(event.target.value)}
            inputMode="numeric"
            placeholder="30"
            aria-label={`回灌 ${name} 多少天`}
            title="往回拨多少天（落点已被起追日盖住时会被拒）"
            className="h-11 w-16 px-2 text-center tabular-nums md:h-7 md:text-xs"
          />
          <RowAction type="submit" disabled={busy !== null || days === null}>
            {pending === "days" ? <Loader2 className="size-3 animate-spin" aria-hidden /> : null}
            回灌
          </RowAction>
        </form>
        <RowAction
          variant="outline"
          onClick={() => void toggle()}
          disabled={busy !== null}
          aria-label={`${row.enabled ? "停用" : "恢复"} ${name}`}
        >
          {busy === "toggle" ? <Loader2 className="size-3 animate-spin" aria-hidden /> : null}
          {row.enabled ? "停用" : "恢复"}
        </RowAction>
      </div>
    </div>
  );
}

/**
 * SkippedBlock 是订阅区内那个「最近被排掉的」折叠格（规格 §8.2，GLOSSARY「跳过 / 收下」）。
 *
 * 收起时就是一行：一句标题 + 计数徽标。徽标要在收起时就能读——它回答的是「要不要展开」。
 * 展开后逐行：番号/标题/发行日、为什么被排掉、何时，加一颗「收下」。
 *
 * 判据可以错（ADR-0017），所以这块不是日志，而是出口：误伤的真四小时单体作品就在这里
 * 被救回来。收下后重新读一次跳过表与面板（任务表里多了一部，那才是收下的证据）。
 */
function SkippedBlock({
  row,
  skips,
  actions,
}: {
  row: Subscription;
  skips: SubscriptionSkip[] | null;
  actions: SubscriptionActions;
}) {
  const { onReceipt, onRefresh } = actions;
  const [open, setOpen] = React.useState(false);
  const [busy, setBusy] = React.useState<number | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const bodyID = React.useId();
  const name = row.actor.name || row.actor.javdb_actor_id;

  // 还没读到（或这一行没有数据）时这一块不渲染：一个“0”会被误读成“没排掉任何作品”。
  if (skips === null) return null;

  const accept = async (skip: SubscriptionSkip) => {
    if (busy !== null) return;
    setBusy(skip.id);
    setError(null);
    try {
      const result = await api.acceptSubscriptionSkip(row.id, skip.id);
      onReceipt({ title: skip.number, tone: "ok", verb: "收下", text: skipAcceptText(result.outcome) });
      // 整页重读：任务表里多了一部、跳过格里少了一行，两件事都在同一份快照里。
      await onRefresh();
    } catch (cause: unknown) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="mt-1">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={bodyID}
        onClick={() => setOpen((current) => !current)}
        className="dense inline-flex min-h-11 items-center gap-1.5 rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring md:min-h-0"
      >
        最近被排掉的
        <Badge variant="outline" className="tabular-nums">
          {skipsBadge(skips.length)}
        </Badge>
        <span aria-hidden>·</span>
        {open ? "收起" : "展开"}
      </button>
      {open ? (
        <div id={bodyID} className="mt-2 flex flex-col gap-2 border-l pl-3">
          {skips.length === 0 ? (
            <p className="dense text-muted-foreground">
              最近 30 天没有被排掉的作品（多人一起的 / 精選綜合 / 长合集）。
            </p>
          ) : (
            skips.map((skip) => (
              <div
                key={skip.id}
                data-skip={skip.movie_id}
                className="flex flex-col gap-1 sm:flex-row sm:items-start sm:justify-between sm:gap-x-4"
              >
                <div className="min-w-0 flex-1">
                  <p className="dense break-words">{skipRowLine(skip)}</p>
                  <p className="dense tabular-nums text-muted-foreground">
                    {skipReasonLabel(skip.reason)} · {formatWhen(skip.skipped_at)}被排掉
                  </p>
                </div>
                <RowAction
                  onClick={() => void accept(skip)}
                  disabled={busy !== null}
                  aria-label={`收下 ${skip.number}`}
                  title="判据可以错，出口必须留：收下就当它从没被排过——建任务并入队"
                >
                  {busy === skip.id ? <Loader2 className="size-3 animate-spin" aria-hidden /> : null}
                  收下
                </RowAction>
              </div>
            ))
          )}
          {error ? (
            <Notice tone="bad" onClose={() => setError(null)}>
              {error}
            </Notice>
          ) : null}
          {skips.length > 0 ? (
            <p className="dense text-muted-foreground">
              {name} 的这些作品按三条判据不该收下；收下会把它们转成正常待办。
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/**
 * BACKFILL_PRESETS 是「回灌」的三档预设：最近三个月 / 一年 / 从头。
 *
 * 前两档是按天数往回拨；「全部」是同一个端点的另一种形状（{all:true} → 起追日拨到
 * 1970-01-01）。它比按天数贵得多，所以要写明代价（§13）：窗口里每一部**还没进任务表**
 * 的作品都要拉一次详情过筛子——翻页有上限（200 页），但那仍然是一整段历史。
 */
const BACKFILL_PRESETS: {
  key: string;
  label: string;
  aria: string;
  /** 悬浮说明；不给就用「往回拨 N 天」那句通用文案。 */
  title?: string;
  body: SubscriptionBackfillBody;
}[] = [
  { key: "90", label: "90 天", aria: "最近 90 天", body: { days: 90 } },
  { key: "365", label: "365 天", aria: "最近 365 天", body: { days: 365 } },
  {
    key: "all",
    label: "全部",
    aria: "全部作品",
    title: "拨到她最早的作品：这段历史里每部还没进任务表的作品都会拉一次详情过筛子",
    body: { all: true },
  },
];

/**
 * SubscribeForm 是「添加」那一步：一个输入框，贴 id 或女优页链接。
 *
 * 没有搜索框（上游没有按名字搜的端点）。判定「这是 id 还是链接」交给
 * `subscriptionAddBody`：面板不猜，服务端也不猜。
 */
function SubscribeForm({ actions }: { actions: SubscriptionActions }) {
  const { onReceipt, onRefresh } = actions;
  const [value, setValue] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const body = subscriptionAddBody(value);
  const inputID = React.useId();

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!body || busy) return;
    setBusy(true);
    setError(null);
    try {
      const result = await api.addSubscription(body);
      const name = result.actress.actor.name || result.actress.actor.javdb_actor_id;
      onReceipt({
        title: name,
        tone: "ok",
        verb: "订阅",
        text:
          result.outcome === "duplicate"
            ? "这位女优已经订过了，水位线原样保留"
            : "已订阅；她的新作下一轮就会走同一条流水线",
      });
      setValue("");
      await onRefresh();
    } catch (cause: unknown) {
      // 错误文案来自端点的 zh-CN 信封（门禁、id 读不到、形状不对），直译即可。
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-2 sm:flex-row sm:items-end">
      <div className="min-w-0 flex-1">
        <Field id={inputID} label="女优 id 或女优页链接">
          <TextInput
            id={inputID}
            value={value}
            onChange={(event) => setValue(event.target.value)}
            placeholder="EvkJ 或 https://javdb.com/actors/EvkJ"
            mono
            autoComplete="off"
          />
        </Field>
      </div>
      <RowAction type="submit" disabled={!body || busy}>
        {busy ? <Loader2 className="size-3 animate-spin" aria-hidden /> : <Plus className="size-3.5" aria-hidden />}
        添加
      </RowAction>
      {error ? (
        <div className="sm:w-full">
          <Notice tone="bad" onClose={() => setError(null)}>
            {error}
          </Notice>
        </div>
      ) : null}
    </form>
  );
}

/**
 * CollectedImport 是「从 App 收藏导入」（#50，规格 §6.4）。
 *
 * 一条从 App 收藏里勾选建订阅的入口，补上「贴 id / 女优页链接」之外的第二种加法——
 * 手机 App 里早就收藏了一堆女优，让人一个个背 id 回来贴是没道理的。
 *
 * 三件事是刻意的：
 *
 *   1. **没有搜索框。** 上游不提供按名字搜女优的端点（§13 实测：`/api/v1/actors` 是分页
 *      名单、q/name 全被静默忽略），所以这一块只有「读名单 → 勾 → 导入」，没有输入框。
 *   2. **逐条回执。** 勾了 5 位就发 5 次 `POST /api/subscriptions`（#47 那个端点），每条
 *      自己的结局落在自己那行上，最后合成一句话。串行发：一位女优一次上游读取，并行只会
 *      把上游的自帽撞出来，而这里等的是人的一次点击，不是页面的首屏。
 *   3. **读不到就说读不到。** 没配 token 时入口整颗禁着并写出原因（收藏名单要凭据），
 *      上游抖了就红条原话给出来——静默空名单会让人以为「我 App 里一位都没收藏」。
 */
function CollectedImport({
  javdbConfigured,
  actions,
}: {
  javdbConfigured: boolean;
  actions: SubscriptionActions;
}) {
  const { onReceipt, onRefresh } = actions;
  const [open, setOpen] = React.useState(false);
  const [list, setList] = React.useState<CollectedActresses | null>(null);
  const [selected, setSelected] = React.useState<ReadonlySet<string>>(new Set());
  const [results, setResults] = React.useState<Record<string, ImportOutcome>>({});
  const [loading, setLoading] = React.useState(false);
  const [importing, setImporting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const items = React.useMemo(() => collectedImportItems(list?.actresses ?? []), [list]);
  const ids = importIDs(items, selected);

  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const loaded = await api.collectedActresses();
      setList(loaded);
      setSelected(new Set());
      setResults({});
    } catch (cause: unknown) {
      // 错误文案来自端点的 zh-CN 信封（没保存 token、token 失效、上游抖），直译即可。
      setError(cause instanceof Error ? cause.message : String(cause));
      setList(null);
    } finally {
      setLoading(false);
    }
  };

  const toggleOpen = async () => {
    if (open) {
      setOpen(false);
      return;
    }
    setOpen(true);
    // 每次展开都重读：收藏是在手机上改的，缓存一份名单只会给出过期的勾选。
    await load();
  };

  const toggle = (id: string) => {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const run = async () => {
    if (importing || ids.length === 0) return;
    setImporting(true);
    setError(null);

    try {
      const outcomes: ImportOutcome[] = [];
      for (const id of ids) {
        const name = items.find((item) => item.actress.id === id)?.actress.name ?? id;
        let outcome: ImportOutcome;
        try {
          const result = await api.addSubscription({ actress_id: id });
          // 端点的两个结局同名搬过来：duplicate 是幂等放行（已订过，水位线原样）。
          outcome = { id, name, outcome: result.outcome === "duplicate" ? "duplicate" : "created" };
        } catch (cause: unknown) {
          outcome = { id, name, outcome: "failed", message: cause instanceof Error ? cause.message : String(cause) };
        }
        outcomes.push(outcome);
        // 逐条落：第 1 位的结局不用等第 5 位。
        setResults((current) => ({ ...current, [id]: outcome }));
      }

      const receipt = importReceipts(outcomes);
      onReceipt({ title: `${outcomes.length} 位女优`, tone: receipt.tone, verb: "导入", text: receipt.text });

      // 成了的那几位已经在订阅表里：就地置灰，不让同一份名单上还留着可勾的旧状态。
      const done = new Set(outcomes.filter((outcome) => outcome.outcome !== "failed").map((outcome) => outcome.id));
      setList((current) =>
        current
          ? {
              ...current,
              actresses: current.actresses.map((actress) =>
                done.has(actress.id) ? { ...actress, subscribed: true } : actress,
              ),
            }
          : current,
      );
      setSelected(new Set());
      await onRefresh();
    } finally {
      // 忙位在 finally 里收：中途一次网络异常不该把「导入」按成永久禁用。
      setImporting(false);
    }
  };

  return (
    <div className="flex flex-col gap-2 rounded-lg border border-border/60 p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <p className="text-sm font-medium">从 App 收藏导入</p>
          <p className="dense text-muted-foreground">
            {javdbConfigured
              ? "把你 App 里收藏的女优勾过来建订阅；已订阅的置灰。"
              : "先在集成设置里保存 JavDB token——收藏名单要凭据才读得到。"}
          </p>
        </div>
        <RowAction
          onClick={() => void toggleOpen()}
          disabled={!javdbConfigured || loading || importing}
          aria-label={open ? "收起收藏名单" : "从 App 收藏导入"}
          aria-expanded={open}
          title={javdbConfigured ? undefined : "收藏名单匿名不可用，先保存 JavDB token"}
        >
          {loading ? (
            <Loader2 className="size-3 animate-spin" aria-hidden />
          ) : (
            <Download className="size-3.5" aria-hidden />
          )}
          {open ? "收起" : "读收藏"}
        </RowAction>
      </div>

      {error ? (
        <Notice tone="bad" onClose={() => setError(null)}>
          {error}
        </Notice>
      ) : null}

      {open && list ? (
        <>
          {list.actresses.length === 0 ? (
            <p className="dense text-muted-foreground">
              App 里还没收藏任何演员。收藏之后回到这里点「读收藏」。
            </p>
          ) : (
            <div className="divide-y">
              {items.map((item) => {
                const result = results[item.actress.id];
                return (
                  <label
                    key={item.actress.id}
                    data-collected={item.actress.id}
                    className={cn(
                      "flex items-start gap-2 py-2 first:pt-0 last:pb-0",
                      item.selectable ? "cursor-pointer" : "cursor-default opacity-70",
                    )}
                  >
                    <input
                      type="checkbox"
                      className="mt-0.5 size-4 shrink-0 accent-primary"
                      checked={item.selectable ? selected.has(item.actress.id) : false}
                      disabled={!item.selectable || importing}
                      onChange={() => toggle(item.actress.id)}
                    />
                    <span className="min-w-0 flex-1">
                      <span className="flex flex-wrap items-baseline gap-x-2 text-sm font-medium">
                        {item.actress.name || item.actress.id}
                        <span className="text-xs font-normal tabular-nums text-muted-foreground">
                          {item.actress.id} · 作品 {item.actress.videos_count} 部
                        </span>
                        {item.reason ? <span className="dense text-muted-foreground">{item.reason}</span> : null}
                      </span>
                      {result ? (
                        <span
                          className={cn(
                            "dense",
                            result.outcome === "failed" ? "text-status-danger-text" : "text-status-done-text",
                          )}
                        >
                          {importResultLabel(result)}
                        </span>
                      ) : null}
                    </span>
                  </label>
                );
              })}
            </div>
          )}

          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="dense text-muted-foreground">
              {list.truncated
                ? "收藏超过 200 位，这里只列到前 200 位"
                : `收藏里 ${items.length} 位（可导入 ${items.filter((item) => item.selectable).length} 位）`}
            </span>
            <RowAction
              variant="default"
              onClick={() => void run()}
              disabled={ids.length === 0 || importing}
              aria-label={`导入选中的 ${ids.length} 位女优`}
            >
              {importing ? <Loader2 className="size-3 animate-spin" aria-hidden /> : null}
              导入 {ids.length} 位
            </RowAction>
          </div>
        </>
      ) : null}
    </div>
  );
}

