import * as React from "react";
import { Loader2, Search } from "lucide-react";

import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card";
import { ConfirmBar } from "~/components/confirm-bar";
import { Notice, TextInput } from "~/components/form";
import { ReceiptAnnouncer, ReceiptList, RECEIPT_CEILING, useReceipts } from "~/components/receipts";
import { ROW_ACTION_SIZE, RowAction } from "~/components/row-action";
import {
  BACKFILL_BATCH,
  BACKFILL_PAGE,
  EMPTY_FILTER,
  backfillBatchNote,
  backfillConfirmText,
  backfillGroups,
  backfillPrefix,
  filterBackfill,
  nextBackfillBatch,
  type BackfillOutcome,
} from "~/lib/backfill";
import { useNow } from "~/lib/clock";
import { api, type BackfillScan } from "~/lib/api";

/**
 * BackfillSection 是「补录目录」这一节（#43 / GLOSSARY「补录」）。
 *
 * 它以前是面板上的一节：`dirs.map()` 一把铺完，502 行、502 颗「补录」、26,533px
 * （整页 85%，手机上 57.7 屏），页面里 76% 的可聚焦元素都在这块。现在它是集成设置里
 * 一个有自己的地址的节（`/settings/backfill`），四件事同时改：
 *
 * 1. **进得来、找得到**：搜索番号（`/` 聚焦）＋按番号前缀筛（114 个前缀是一层真查询轴，
 *    但它们不该铺成 114 颗 chip）。
 * 2. **有批次**：卡头只有一颗主操作「每批补录 50 个」——按钮印多少就补多少，
 *    数字来自当前筛选，和确认条上那句话同一个来源。
 * 3. **先要一句确认**：补录会删旧 `{番号}.nfo`，所以批量与单行都走同一条确认条，
 *    红三件套说清范围与后果（Consequence-First Rule）。
 * 4. **列表有边界**：默认 50 行 +「显示更多」，卡头 sticky，回执行留在这张卡顶上。
 *
 * 列表与任务表实时一致：每个请求后端都现扫现比，补录成功的行随下一次扫描消失。
 */
export function BackfillSection() {
  const [scan, setScan] = React.useState<BackfillScan | null>(null);
  const [loadError, setLoadError] = React.useState<string | null>(null);
  const [filter, setFilter] = React.useState(EMPTY_FILTER);
  const [shown, setShown] = React.useState(BACKFILL_PAGE);
  // 待确认的那一批：批量是「当前筛选的前 50 个」，单行就是那一个。
  const [pending, setPending] = React.useState<{ numbers: string[]; batch: boolean } | null>(null);
  const [running, setRunning] = React.useState(false);
  const receiptsState = useReceipts();
  const now = useNow();
  const searchRef = React.useRef<HTMLInputElement>(null);
  const batchTriggerRef = React.useRef<HTMLButtonElement>(null);
  const rowTriggerRef = React.useRef<HTMLButtonElement | null>(null);
  const searchID = React.useId();
  const prefixID = React.useId();

  const load = React.useCallback(async () => {
    try {
      const loaded = await api.backfillScan();
      setScan(loaded);
      setLoadError(null);
    } catch (cause: unknown) {
      setScan(null);
      setLoadError(cause instanceof Error ? cause.message : String(cause));
    }
  }, []);

  // 首读在 effect 里直接等：挂载即扫一次盘（后端的每个请求都现扫现比）。
  React.useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const loaded = await api.backfillScan();
        if (cancelled) return;
        setScan(loaded);
        setLoadError(null);
      } catch (cause: unknown) {
        if (cancelled) return;
        setScan(null);
        setLoadError(cause instanceof Error ? cause.message : String(cause));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // `/` 聚焦这一节的搜索框：任务的 `/` 只到任务表，最大的那张列表以前没有键盘路径。
  React.useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target;
      if (
        target instanceof HTMLInputElement ||
        target instanceof HTMLTextAreaElement ||
        target instanceof HTMLSelectElement ||
        (target instanceof HTMLElement && target.isContentEditable)
      ) {
        return;
      }
      event.preventDefault();
      searchRef.current?.focus();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const dirs = React.useMemo(() => scan?.dirs ?? [], [scan]);
  const groups = React.useMemo(() => backfillGroups(dirs), [dirs]);
  const rows = React.useMemo(() => filterBackfill(dirs, filter), [dirs, filter]);
  const batch = React.useMemo(() => nextBackfillBatch(rows), [rows]);
  const visible = rows.slice(0, shown);

  const run = async (numbers: string[]) => {
    if (running || numbers.length === 0) return;
    setRunning(true);

    const outcomes: BackfillOutcome[] = [];
    for (const number of numbers) {
      try {
        const result = await api.backfill(number);
        const message =
          result.outcome === "duplicate"
            ? result.message ?? "已有任务，不用补录"
            : `已补录 ${result.versions} 条版本，重刮会重写 NFO 与图片`;
        outcomes.push({ number, ok: true });
        receiptsState.push({ title: number, tone: "ok", verb: "补录", text: message });
      } catch (cause: unknown) {
        const message = cause instanceof Error ? cause.message : String(cause);
        outcomes.push({ number, ok: false, message });
        receiptsState.push({ title: number, tone: "warn", text: message });
      }
    }

    // 这一批的账单独留一条：逐条回执会被后面的批量盖掉，人先读的是这一句总账。
    receiptsState.push(
      {
        title: `${numbers.length} 个目录`,
        tone: outcomes.every((outcome) => outcome.ok) ? "ok" : "warn",
        verb: "补录",
        text: backfillBatchNote(outcomes),
      },
      RECEIPT_CEILING,
    );

    setRunning(false);
    // 成功的行随下一次扫描消失：列表与任务表是同一份真相的两个视图。
    await load();
  };

  const empty = scan !== null && dirs.length === 0;

  return (
    // overflow-visible：ui 的 Card 默认 overflow-hidden，那会把卡头的 sticky 变成
    // 相对卡片自身的定位（等于没粘住）。圆角由内容自己收。
    <Card role="region" aria-labelledby="backfill-heading" aria-busy={running} className="overflow-visible">
      <CardHeader className="sticky top-0 z-10 rounded-t-xl border-b bg-card">
        <div>
          <CardTitle as="h2" id="backfill-heading">
            补录目录
          </CardTitle>
          <CardDescription className="mt-1 text-xs leading-relaxed">
            媒体库里还不归本项目的旧目录。补录把现有视频原地入库，随后重刮重写 NFO 与图片，
            旧的 {'{番号}'}.nfo 会被删掉（movie.nfo 除外）。
          </CardDescription>
        </div>
        <CardAction className="max-sm:col-start-1 max-sm:row-start-2">
          {batch.length > 0 && pending === null ? (
            <Button
              ref={batchTriggerRef}
              className={ROW_ACTION_SIZE}
              disabled={running}
              onClick={() => setPending({ numbers: batch.map((dir) => dir.number), batch: true })}
            >
              {running ? <Loader2 className="size-3.5 animate-spin" aria-hidden /> : null}
              每批补录 {batch.length} 个
            </Button>
          ) : (
            <span className="text-xs tabular-nums text-muted-foreground">
              {scan === null ? "读不到" : `${dirs.length} 个目录`}
            </span>
          )}
        </CardAction>
      </CardHeader>

      <CardContent className="rounded-b-xl">
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

        {empty ? (
          <p className="dense leading-relaxed text-muted-foreground">
            媒体库里没有不归本项目的旧目录了：这一节会一直空着，直到又多出一个。
          </p>
        ) : null}

        {dirs.length > 0 ? (
          <>
            <ReceiptAnnouncer receipts={receiptsState.receipts} />
            <ReceiptList
              receipts={receiptsState.receipts}
              now={now}
              listRef={receiptsState.listRef}
              onDismiss={receiptsState.dismiss}
            />

            <div className="mb-3 flex flex-col gap-2 sm:flex-row sm:items-end">
              <div className="min-w-0 flex-1">
                <label htmlFor={searchID} className="mb-1.5 block text-xs font-medium">
                  搜索番号
                  <span className="ml-1.5 font-normal text-muted-foreground">
                    按 <span className="font-mono">/</span> 直接到这里
                  </span>
                </label>
                <div className="relative">
                  <Search
                    className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
                    aria-hidden
                  />
                  <TextInput
                    id={searchID}
                    ref={searchRef}
                    type="search"
                    value={filter.q}
                    onChange={(event) => {
                      setFilter((current) => ({ ...current, q: event.target.value }));
                      setShown(BACKFILL_PAGE);
                    }}
                    placeholder="ABF-017"
                    autoComplete="off"
                    className="pl-8"
                  />
                </div>
              </div>
              <div className="min-w-0 sm:w-56">
                <label htmlFor={prefixID} className="mb-1.5 block text-xs font-medium">
                  番号前缀
                </label>
                <select
                  id={prefixID}
                  value={filter.prefix}
                  onChange={(event) => {
                    setFilter((current) => ({ ...current, prefix: event.target.value }));
                    setShown(BACKFILL_PAGE);
                  }}
                  className="h-8 w-full rounded-lg border bg-card px-2 text-[0.8125rem] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring max-md:h-11"
                >
                  <option value="">全部前缀（{groups.length}）</option>
                  {groups.map((group) => (
                    <option key={group.prefix} value={group.prefix}>
                      {group.prefix}（{group.count}）
                    </option>
                  ))}
                </select>
              </div>
            </div>

            {pending ? (
              <div className="mb-3">
                <ConfirmBar
                  tone="danger"
                  label={pending.batch ? "确认补录一批目录" : `确认补录 ${pending.numbers[0]}`}
                  text={backfillConfirmText(pending.numbers.length)}
                  confirmLabel={pending.batch ? `确认补录 ${pending.numbers.length} 个` : "确认补录"}
                  busy={running}
                  cancelFocusRef={pending.batch ? batchTriggerRef : rowTriggerRef}
                  onCancel={() => setPending(null)}
                  onConfirm={() => {
                    const numbers = pending.numbers;
                    setPending(null);
                    void run(numbers);
                  }}
                />
              </div>
            ) : null}

            <p className="dense mb-1 tabular-nums text-muted-foreground">
              {rows.length === dirs.length
                ? `${rows.length} 个目录`
                : `筛出 ${rows.length} 个目录（共 ${dirs.length} 个）`}
              {rows.length > 0 ? ` · 一批最多 ${BACKFILL_BATCH} 个` : null}
            </p>

            {rows.length === 0 ? (
              <p className="dense py-3 text-muted-foreground">
                没有匹配的目录：清掉搜索词或把前缀换回「全部」。
              </p>
            ) : (
              <div className="divide-y">
                {visible.map((dir) => (
                  <div
                    key={dir.number}
                    data-backfill={dir.number}
                    className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:justify-between sm:gap-x-4"
                  >
                    <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 gap-y-1">
                      <span className="dense font-medium tabular-nums">{dir.number}</span>
                      <span className="text-xs tabular-nums text-muted-foreground">
                        {dir.videos} 个视频
                      </span>
                      <Badge variant="outline" className="font-normal">
                        {backfillPrefix(dir.number)}
                      </Badge>
                    </div>
                    <RowAction
                      variant="ghost"
                      disabled={running}
                      aria-label={`补录 ${dir.number}`}
                      onClick={(event) => {
                        rowTriggerRef.current = event.currentTarget;
                        setPending({ numbers: [dir.number], batch: false });
                      }}
                    >
                      补录
                    </RowAction>
                  </div>
                ))}
              </div>
            )}

            {rows.length > visible.length ? (
              <div className="mt-3 flex items-center justify-between gap-3">
                <span className="text-xs tabular-nums text-muted-foreground">
                  已显示 {visible.length} / {rows.length}
                </span>
                <Button
                  variant="outline"
                  className={ROW_ACTION_SIZE}
                  onClick={() => setShown((current) => current + BACKFILL_PAGE)}
                >
                  显示更多 {Math.min(BACKFILL_PAGE, rows.length - visible.length)} 个
                </Button>
              </div>
            ) : null}
          </>
        ) : null}
      </CardContent>
    </Card>
  );
}
