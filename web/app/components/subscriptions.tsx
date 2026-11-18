import * as React from "react";
import { ChevronDown, Loader2, Plus, Search } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card";
import { Notice, TextInput } from "~/components/form";
import { ReceiptAnnouncer, ReceiptList, useReceipts } from "~/components/receipts";
import { ROW_ACTION_SIZE, RowAction } from "~/components/row-action";
import { useNow } from "~/lib/clock";
import { formatAbsolute, formatWhen } from "~/lib/time";
import { subscriptionAddBody, subscriptionBackfillDays, subscriptionSubtitle } from "~/lib/subscription";
import { skipAcceptText, skipReasonLabel, skipRowLine, skipsBadge, type SubscriptionSkip } from "~/lib/skips";
import { collectedImportItems, importIDs, importReceipts, importResultLabel, type ImportOutcome } from "~/lib/collected";
import type { ReceiptDraft } from "~/lib/receipts";
import {
  api,
  type CollectedActresses,
  type Subscription,
  type SubscriptionBackfillBody,
  type SubscriptionList,
} from "~/lib/api";

/**
 * 订阅的两节（规格 §8.2/§6.4，GLOSSARY「女优订阅 / 跳过 / 收下」）：
 * `/settings/actresses` 与 `/settings/actress-import`。
 *
 * 它们以前是面板上的一张卡，一卡四层：卡 > 带边框的子盒 > 144 行复选框 > 5,265px 之外
 * 的提交按钮；每行七个可见控件（`展开 + 90 天 + 365 天 + 全部 + 30 + 回灌 + 停用`），
 * 而可见产出是三个 `水位线 … · 带来 0 部`。订阅是**配置面**，不是待办：
 *
 * - 进集成设置，各有一个地址（可书签、可「去 X」直达）；
 * - 每行默认只有两个动作：一颗 `回灌`（展开才是三档预设 + 天数）、一颗 `停用`；
 * - 「从 App 收藏导入」独立成节：搜索、全选、**底部 sticky 的提交行**（人勾完最后一行
 *   就该看到「导入 N 位」，而不是往上翻七屏）。
 *
 * 判定与文案仍在 `lib/subscription`、`lib/skips`、`lib/collected`：这一层只渲染。
 */

type SubscriptionActions = {
  onReceipt: (draft: ReceiptDraft) => void;
  onRefresh: () => Promise<void>;
};

/** ActressesSection：订阅本体（添加 + 每行 + 被排掉的折叠格）。 */
export function ActressesSection() {
  const [list, setList] = React.useState<SubscriptionList | null>(null);
  const [loadError, setLoadError] = React.useState<string | null>(null);
  const receiptsState = useReceipts();
  const now = useNow();

  const load = React.useCallback(async () => {
    try {
      const loaded = await api.subscriptions();
      setList(loaded);
      setLoadError(null);
    } catch (cause: unknown) {
      setList(null);
      setLoadError(cause instanceof Error ? cause.message : String(cause));
    }
  }, []);

  React.useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const loaded = await api.subscriptions();
        if (cancelled) return;
        setList(loaded);
        setLoadError(null);
      } catch (cause: unknown) {
        if (cancelled) return;
        setList(null);
        setLoadError(cause instanceof Error ? cause.message : String(cause));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const actions: SubscriptionActions = React.useMemo(
    () => ({ onReceipt: receiptsState.push, onRefresh: load }),
    [receiptsState.push, load],
  );

  const rows = list?.actresses ?? [];

  return (
    <Card role="region" aria-labelledby="subscriptions-heading" className="overflow-visible">
      <CardHeader>
        <div>
          <CardTitle as="h2" id="subscriptions-heading">
            女优订阅
          </CardTitle>
          <CardDescription className="mt-1 text-xs leading-relaxed">
            盯住女优新作的订阅：贴她的 JavDB id 或女优页链接（上游没有按名字搜的端点），
            或者去「从 App 收藏导入」勾选。新订阅从建订阅当天开始追，更早的历史要按「回灌」才翻。
          </CardDescription>
        </div>
        <CardAction className="max-sm:col-start-1 max-sm:row-start-2">
          <span className="text-xs tabular-nums text-muted-foreground">
            {list === null ? "读不到" : `${rows.length} 位女优`}
          </span>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <ReceiptAnnouncer receipts={receiptsState.receipts} />
        <ReceiptList
          receipts={receiptsState.receipts}
          now={now}
          listRef={receiptsState.listRef}
          onDismiss={receiptsState.dismiss}
        />

        {loadError ? (
          <Notice tone="bad" onClose={() => setLoadError(null)}>
            {loadError}
            <span className="mt-1 flex flex-wrap items-center gap-2">
              <Button variant="outline" className={ROW_ACTION_SIZE} onClick={() => void load()}>
                再读一次
              </Button>
            </span>
          </Notice>
        ) : null}

        <SubscribeForm actions={actions} />

        {list !== null && rows.length === 0 ? (
          <p className="dense leading-relaxed text-muted-foreground">
            还没订阅任何女优。贴一个 id 或女优页链接，她的新作就会走与清单轮询同一条流水线。
          </p>
        ) : null}

        {rows.length > 0 ? (
          <div className="divide-y">
            {rows.map((row) => (
              <SubscriptionRow key={row.id} row={row} now={now} actions={actions} />
            ))}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

/**
 * SubscriptionRow 是订阅的一行：谁、最近一次带来东西是多久前、还有多少在路上。
 *
 * 默认只露两颗动作（`回灌`、`停用/恢复`）：七个控件并排时，唯一有意思的那一颗
 * （回灌）被夹在四颗预设里认不出来。回灌点开才展开——它是罕见的、贵的那一下。
 */
function SubscriptionRow({
  row,
  now,
  actions,
}: {
  row: Subscription;
  now: number;
  actions: SubscriptionActions;
}) {
  const { onReceipt, onRefresh } = actions;
  const [busy, setBusy] = React.useState<"toggle" | "backfill" | null>(null);
  const [backfillOpen, setBackfillOpen] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [daysInput, setDaysInput] = React.useState("");
  const daysID = React.useId();
  const days = subscriptionBackfillDays(daysInput);
  const triggerRef = React.useRef<HTMLButtonElement>(null);
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

  const backfill = async (body: SubscriptionBackfillBody) => {
    if (busy) return;
    setBusy("backfill");
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
      setBackfillOpen(false);
      await onRefresh();
    } catch (cause: unknown) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(null);
    }
  };

  const backfillDays = async (event: React.FormEvent) => {
    event.preventDefault();
    if (days === null) return;
    await backfill({ days });
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
            <span className="text-xs font-normal tabular-nums text-muted-foreground">
              {row.actor.javdb_actor_id}
            </span>
          ) : null}
          {row.enabled ? null : <Badge variant="outline">已停用</Badge>}
        </div>
        <p
          className="dense tabular-nums text-muted-foreground"
          title={row.watermark_date ? `水位线 ${formatAbsolute(row.watermark_date)}` : undefined}
        >
          {subscriptionSubtitle(row, now)}
        </p>
        <SkippedBlock row={row} actions={actions} />
        {error ? (
          <Notice tone="bad" onClose={() => setError(null)}>
            {error}
          </Notice>
        ) : null}
      </div>

      <div className="flex flex-wrap items-center gap-1.5">
        <RowAction
          ref={triggerRef}
          aria-expanded={backfillOpen}
          onClick={() => setBackfillOpen((current) => !current)}
          title="把取数下界往回拨：90 天 / 365 天 / 全部 / 自定义天数"
        >
          回灌
          <ChevronDown
            className={cn("size-3 transition-transform", backfillOpen && "rotate-180")}
            aria-hidden
          />
        </RowAction>
        <RowAction
          onClick={() => void toggle()}
          disabled={busy !== null}
          aria-label={`${row.enabled ? "停用" : "恢复"} ${name}`}
        >
          {busy === "toggle" ? <Loader2 className="size-3 animate-spin" aria-hidden /> : null}
          {row.enabled ? "停用" : "恢复"}
        </RowAction>
      </div>

      {backfillOpen ? (
        // 不套带边框的盒子：卡里再套一张卡是明令禁止的（也是设计要求里的一条）；
        // 展开面用一条上发丝线与缩进说「这是这一行的第二层」。
        <div className="basis-full border-t pt-2 sm:max-w-md">
          <p className="dense mb-2 text-muted-foreground">
            回灌只拨一次：把取数下界往回挪，扫完自动清掉。「全部」最贵——
            她那段历史里每部还没进任务表的作品都会拉一次详情过筛子。
          </p>
          <div className="flex flex-wrap items-center gap-1.5">
            {BACKFILL_PRESETS.map((preset) => (
              <RowAction
                key={preset.key}
                onClick={() => void backfill(preset.body)}
                disabled={busy !== null}
                aria-label={`回灌 ${name} ${preset.aria}`}
                title={preset.title}
              >
                {busy === "backfill" ? (
                  <Loader2 className="size-3 animate-spin" aria-hidden />
                ) : null}
                {preset.label}
              </RowAction>
            ))}
          </div>
          <form onSubmit={backfillDays} className="mt-2 flex flex-wrap items-end gap-1.5">
            <div className="w-24">
              <label htmlFor={daysID} className="mb-1 block text-xs font-medium">
                天数
              </label>
              <TextInput
                id={daysID}
                value={daysInput}
                onChange={(event) => setDaysInput(event.target.value)}
                inputMode="numeric"
                placeholder="30"
                title="往回拨多少天（落点已被起追日盖住时会被拒）"
                className="h-11 text-center tabular-nums md:h-7 md:text-xs"
              />
            </div>
            <RowAction type="submit" disabled={busy !== null || days === null}>
              {busy === "backfill" ? <Loader2 className="size-3 animate-spin" aria-hidden /> : null}
              回灌这么多天
            </RowAction>
          </form>
        </div>
      ) : null}
    </div>
  );
}

/**
 * SkippedBlock 是订阅行里那个「最近被排掉的」折叠格（GLOSSARY「跳过 / 收下」）。
 *
 * 收起时只有一行（标题 + 计数徽标），展开才去读：这块以前是每行一条预取请求，
 * 面板一次加载要为三条订阅发三个请求，而绝大多数时间它都合着。
 *
 * 判据可以错（ADR-0017），所以这块不是日志，而是出口：误伤的真四小时单体作品就在这里
 * 被救回来。收下后重读订阅表——任务表里多了一部才是收下的证据（回面板看）。
 */
function SkippedBlock({ row, actions }: { row: Subscription; actions: SubscriptionActions }) {
  const { onReceipt, onRefresh } = actions;
  const [open, setOpen] = React.useState(false);
  const [skips, setSkips] = React.useState<SubscriptionSkip[] | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [busy, setBusy] = React.useState<number | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const bodyID = React.useId();
  const name = row.actor.name || row.actor.javdb_actor_id;

  const openBlock = async () => {
    if (open) {
      setOpen(false);
      return;
    }
    setOpen(true);
    setLoading(true);
    setError(null);
    try {
      const loaded = await api.subscriptionSkips(row.id);
      setSkips(loaded.skips);
    } catch (cause: unknown) {
      setSkips(null);
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setLoading(false);
    }
  };

  const accept = async (skip: SubscriptionSkip) => {
    if (busy !== null) return;
    setBusy(skip.id);
    setError(null);
    try {
      const result = await api.acceptSubscriptionSkip(row.id, skip.id);
      onReceipt({ title: skip.number, tone: "ok", verb: "收下", text: skipAcceptText(result.outcome) });
      setSkips((current) => (current ? current.filter((item) => item.id !== skip.id) : current));
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
        onClick={() => void openBlock()}
        className="dense inline-flex min-h-11 items-center gap-1.5 rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring md:min-h-0"
      >
        最近被排掉的
        {skips !== null ? (
          <Badge variant="outline" className="tabular-nums">
            {skipsBadge(skips.length)}
          </Badge>
        ) : loading ? (
          <Loader2 className="size-3 animate-spin" aria-hidden />
        ) : null}
        <span aria-hidden>·</span>
        {open ? "收起" : "展开"}
      </button>
      {open ? (
        <div id={bodyID} className="mt-2 flex flex-col gap-2 border-l pl-3">
          {error ? (
            <Notice tone="bad" onClose={() => setError(null)}>
              {error}
            </Notice>
          ) : null}
          {skips === null && !loading ? null : loading ? (
            <p className="dense text-muted-foreground">正在读…</p>
          ) : skips && skips.length === 0 ? (
            <p className="dense text-muted-foreground">
              最近 30 天没有被排掉的作品（多人一起的 / 精選綜合 / 长合集）。
            </p>
          ) : (
            (skips ?? []).map((skip) => (
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
          {skips && skips.length > 0 ? (
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
 * 1970-01-01），比按天数贵得多，所以悬浮说明要写明代价（§13）。
 */
const BACKFILL_PRESETS: {
  key: string;
  label: string;
  aria: string;
  title?: string;
  body: SubscriptionBackfillBody;
}[] = [
  { key: "90", label: "90 天", aria: "最近 90 天", title: "把取数下界往回拨 90 天，扫完自动清掉", body: { days: 90 } },
  { key: "365", label: "365 天", aria: "最近 365 天", title: "把取数下界往回拨 365 天，扫完自动清掉", body: { days: 365 } },
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
 * `subscriptionAddBody`：界面不猜，服务端也不猜。
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
    <form onSubmit={submit} className="flex flex-col gap-2 border-b pb-3 sm:flex-row sm:items-end">
      <div className="min-w-0 flex-1">
        <label htmlFor={inputID} className="mb-1.5 block text-xs font-medium">
          女优 id 或女优页链接
        </label>
        <TextInput
          id={inputID}
          value={value}
          onChange={(event) => setValue(event.target.value)}
          placeholder="EvkJ 或 https://javdb.com/actors/EvkJ"
          mono
          autoComplete="off"
        />
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
 * ActressImportSection 是「从 App 收藏导入」这一节（#50，规格 §6.4）。
 *
 * 它以前是订阅卡里一个带边框的子盒（卡片里再套卡片，DESIGN 明令禁止），144 行复选框
 * 铺下来 5,265px，提交按钮在 7 屏之外、没有 sticky，右上角只写着「导入 0 位」。
 * 现在它是一个自己的地址，三件事是刻意的：
 *
 * 1. **搜索 + 全选**：144 行里找人靠打字，不靠滚。全选只作用在「筛出来的、还没订阅的」
 *    那些行上——一个动作的范围必须写在屏幕上。
 * 2. **提交行 sticky**：勾完最后一行就看得见「导入 N 位」。它钉在卡底，不是一个魔法数字。
 * 3. **逐条结局**：勾了 5 位就串行发 5 次（一位女优一次上游读取，并行只会把上游的
 *    自帽撞出来），每条结局落在自己那行上，最后合成一句话。
 */
export function ActressImportSection({ javdbConfigured }: { javdbConfigured: boolean }) {
  const [list, setList] = React.useState<CollectedActresses | null>(null);
  const [selected, setSelected] = React.useState<ReadonlySet<string>>(new Set());
  const [results, setResults] = React.useState<Record<string, ImportOutcome>>({});
  const [query, setQuery] = React.useState("");
  const [loading, setLoading] = React.useState(false);
  const [importing, setImporting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const receiptsState = useReceipts();
  const now = useNow();

  const load = React.useCallback(async () => {
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
  }, []);

  React.useEffect(() => {
    if (!javdbConfigured) return;
    let cancelled = false;
    void (async () => {
      try {
        const loaded = await api.collectedActresses();
        if (cancelled) return;
        setList(loaded);
        setSelected(new Set());
        setResults({});
        setError(null);
      } catch (cause: unknown) {
        if (cancelled) return;
        setList(null);
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [javdbConfigured]);

  const allItems = React.useMemo(() => collectedImportItems(list?.actresses ?? []), [list]);
  const items = React.useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (needle === "") return allItems;
    return allItems.filter(
      (item) =>
        item.actress.name.toLowerCase().includes(needle) ||
        item.actress.id.toLowerCase().includes(needle),
    );
  }, [allItems, query]);
  const selectable = items.filter((item) => item.selectable);
  const allSelected = selectable.length > 0 && selectable.every((item) => selected.has(item.actress.id));
  const ids = importIDs(items, selected);

  const toggle = (id: string) => {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleAll = () => {
    setSelected((current) => {
      const next = new Set(current);
      for (const item of selectable) {
        if (allSelected) next.delete(item.actress.id);
        else next.add(item.actress.id);
      }
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
        const name = allItems.find((item) => item.actress.id === id)?.actress.name ?? id;
        let outcome: ImportOutcome;
        try {
          const result = await api.addSubscription({ actress_id: id });
          outcome = { id, name, outcome: result.outcome === "duplicate" ? "duplicate" : "created" };
        } catch (cause: unknown) {
          outcome = { id, name, outcome: "failed", message: cause instanceof Error ? cause.message : String(cause) };
        }
        outcomes.push(outcome);
        // 逐条落：第 1 位的结局不用等第 5 位。
        setResults((current) => ({ ...current, [id]: outcome }));
      }

      const receipt = importReceipts(outcomes);
      receiptsState.push({
        title: `${outcomes.length} 位女优`,
        tone: receipt.tone,
        verb: "导入",
        text: receipt.text,
      });

      // 成了的那几位已经在订阅表里：重读一次名单，她们会以「已订阅」置灰回来
      // （`subscribed` 是服务端拿订阅表算的，前端不再自己翻转一份副本）。
      setSelected(new Set());
      const done = outcomes.filter((outcome) => outcome.outcome !== "failed");
      if (done.length > 0) await load();
    } finally {
      // 忙位在 finally 里收：中途一次网络异常不该把「导入」按成永久禁用。
      setImporting(false);
    }
  };

  // 首读期间 list 是 null：别让「正在读」被说成「名单是空的」。
  const busy = loading || importing;

  if (!javdbConfigured) {
    return (
      <Card role="region" aria-labelledby="actress-import-heading">
        <CardHeader>
          <CardTitle as="h2" id="actress-import-heading">
            从 App 收藏导入
          </CardTitle>
        </CardHeader>
        <CardContent>
          <p className="dense leading-relaxed text-muted-foreground">
            收藏名单要凭据才读得到：先到 JavDB 那一节保存 token，再回来勾。
          </p>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card
      role="region"
      aria-labelledby="actress-import-heading"
      aria-busy={busy}
      // 桌面：卡高度封顶，名单在卡内滚，提交行钉在卡底。
      // 窄屏：卡随内容长、页面整体滚，提交行 sticky 在视口底——手机上再套一层内滚，
      // 底部那颗「导入 N 位」会被卡到屏幕外（这张卡本身已经比一屏高）。
      className="flex flex-col overflow-visible md:max-h-[calc(100dvh-8rem)]"
    >
      <CardHeader>
        <div>
          <CardTitle as="h2" id="actress-import-heading">
            从 App 收藏导入
          </CardTitle>
          <CardDescription className="mt-1 text-xs leading-relaxed">
            把你 App 里收藏的女优勾过来建订阅；已订阅的置灰。全选只作用于筛出来的、还没订阅的那些。
          </CardDescription>
        </div>
        <CardAction className="max-sm:col-start-1 max-sm:row-start-2">
          <Button variant="outline" className={ROW_ACTION_SIZE} disabled={loading || importing} onClick={() => void load()}>
            {loading ? <Loader2 className="size-3.5 animate-spin" aria-hidden /> : null}
            重读名单
          </Button>
        </CardAction>
      </CardHeader>

      <CardContent className="flex min-h-0 flex-1 flex-col gap-3">
        <ReceiptAnnouncer receipts={receiptsState.receipts} />
        <ReceiptList
          receipts={receiptsState.receipts}
          now={now}
          listRef={receiptsState.listRef}
          onDismiss={receiptsState.dismiss}
        />

        {error ? (
          <Notice tone="bad" onClose={() => setError(null)}>
            {error}
          </Notice>
        ) : null}

        <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
          <div className="min-w-0 flex-1">
            <label htmlFor="actress-import-search" className="mb-1.5 block text-xs font-medium">
              搜索名字或 id
            </label>
            <div className="relative">
              <Search
                className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
                aria-hidden
              />
              <TextInput
                id="actress-import-search"
                type="search"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="七澤"
                autoComplete="off"
                className="pl-8"
              />
            </div>
          </div>
          <Button
            variant="outline"
            className={ROW_ACTION_SIZE}
            disabled={selectable.length === 0 || importing}
            aria-pressed={allSelected}
            onClick={toggleAll}
          >
            {allSelected ? "清空勾选" : `全选可导入（${selectable.length}）`}
          </Button>
        </div>

        <div className="-mx-4 px-4 md:min-h-0 md:flex-1 md:overflow-y-auto">
          {list === null && error === null ? (
            <p className="dense py-3 text-muted-foreground">正在读收藏名单…</p>
          ) : items.length === 0 ? (
            <p className="dense py-3 text-muted-foreground">
              {allItems.length === 0
                ? "收藏名单是空的：先在 JavDB App 里收藏几位女优。"
                : "没有匹配的：清掉搜索词。"}
            </p>
          ) : (
            <ul className="divide-y">
              {items.map((item) => {
                const result = results[item.actress.id];
                const disabled = !item.selectable || importing;
                const checked = item.selectable && selected.has(item.actress.id);
                return (
                  <li key={item.actress.id}>
                    <label
                      className={cn(
                        "flex min-h-11 items-center gap-2.5 py-2",
                        disabled ? "opacity-60" : "cursor-pointer",
                      )}
                    >
                      <input
                        type="checkbox"
                        checked={item.selectable && checked}
                        disabled={disabled}
                        onChange={() => toggle(item.actress.id)}
                        className="size-4 shrink-0 accent-primary"
                      />
                      <span className="min-w-0 flex-1">
                        <span className="dense block font-medium">{item.actress.name}</span>
                        <span className="block text-xs tabular-nums text-muted-foreground">
                          {item.actress.id} · {item.actress.videos_count} 部
                          {item.reason ? ` · ${item.reason}` : ""}
                          {result ? ` · ${importResultLabel(result)}` : ""}
                        </span>
                      </span>
                    </label>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </CardContent>

      {/* 提交行钉在卡底：勾完最后一行就该看得见它，而不是往上翻五屏。 */}
      <div className="sticky bottom-0 flex flex-wrap items-center justify-between gap-2 rounded-b-xl border-t bg-card px-4 py-3">
        <span className="text-xs tabular-nums text-muted-foreground">
          {list === null
            ? "还没读到名单"
            : `收藏里 ${allItems.length} 位（可导入 ${selectable.length} 位）${list.truncated ? " · 只列到前 200 位" : ""}`}
        </span>
        <Button className={ROW_ACTION_SIZE} disabled={ids.length === 0 || importing} onClick={() => void run()}>
          {importing ? <Loader2 className="size-3.5 animate-spin" aria-hidden /> : null}
          导入 {ids.length} 位
        </Button>
      </div>
    </Card>
  );
}
