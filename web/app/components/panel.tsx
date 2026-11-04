import * as React from "react";
import { createPortal } from "react-dom";
import { Link } from "react-router";
import { AlertTriangle, ChevronRight, Loader2, RefreshCw, RotateCcw, Search, X } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { Notice, TextInput } from "~/components/form";
import { groupByNumber, type FailureGroup } from "~/lib/attention";
import { formatAbsolute, formatClock, formatFuture, formatWhen } from "~/lib/time";
import {
  remedyForFailure,
  remedyForSideEffect,
  remedyForStalled,
  settingsLabel,
} from "~/lib/remedy";
import {
  api,
  notifyEventLabels,
  notifyEventOf,
  type Attention,
  type AttentionItem,
  type Overview,
  type SideEffect,
  type TaskDetail,
  type TaskEvent,
  type TaskRow,
  type TaskRetryOutcome,
  type TaskState,
  type TaskVersion,
} from "~/lib/api";

/**
 * 面板（规格 §8.2）：统计行 →「需要人工」区 → 稠密任务表 → 副作用队列 → 循环健康。
 *
 * 这里只放面板自己的东西；整站外壳（导航、门禁、视图切换）在 routes/home.tsx。
 */

/** 面板的筛选与页号：住在地址栏里（`?q=&state=&page=`），刷新不丢。 */
export type PanelQuery = { page: number; q: string; state: TaskState | "" };

/** Panel 是面板：统计卡 → 「需要人工」区 → 稠密任务表 → 副作用队列 → 循环健康（规格 §8.2）。 */
export function Panel({
  overview,
  tasks,
  total,
  attention,
  sideEffects,
  query,
  pageSize,
  loadedAt,
  intervals,
  onQuery,
  onRefresh,
}: {
  overview: Overview | null;
  tasks: TaskRow[];
  /** 同筛选条件下的总条数：分页与空态都靠它，不靠「这一页满了」猜。 */
  total: number;
  attention: Attention | null;
  sideEffects: SideEffect[];
  query: PanelQuery;
  pageSize: number;
  /** 上一次读到数据的时刻（客户端时钟）：值班台必须知道自己有多旧。 */
  loadedAt: number | null;
  /** 三条循环的间隔，读自 app_config；读不到就是 null，不编默认值。 */
  intervals: { poll: string | null; reconcile: string | null; sidefx: string | null };
  onQuery: (next: PanelQuery) => void;
  onRefresh: () => Promise<void>;
}) {
  const [selected, setSelected] = React.useState<string | null>(null);
  const [refreshing, setRefreshing] = React.useState(false);
  // 相对时间要自己走，否则一屏「3 分钟前」会挂到用户关掉标签页为止。
  const now = useNow();
  // 回执挂在 Panel 上而不是 NeedsAttention 里：那张卡在清空之后整块不渲染，
  // 而「刚才那一按成了没有」的播报位必须一直在树上——活区带内容挂载就不会播报。
  const receiptsState = useReceipts();

  // 打开抽屉的人和归还焦点的人都必须是 Panel：触发元素在打开的那一刻是焦点，
  // 而 inert 一挂上去焦点就被浏览器挪走了，晚一步再记就只记得住「关闭按钮」。
  const previousFocus = React.useRef<HTMLElement | null>(null);
  const openTask = React.useCallback((movieID: string) => {
    previousFocus.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setSelected(movieID);
  }, []);

  // 抽屉是模态：整个外壳 inert，页面不跟着滚；关掉时先撤 inert 再还焦点，
  // 顺序反了的话 focus() 会被 inert 子树吞掉，人就被扔回页首。
  // 抽屉 portal 到 body，才能既 inert 外壳、又让自己可点。
  React.useEffect(() => {
    if (!selected) return;
    const shell = document.getElementById("app-shell");
    const previousOverflow = document.body.style.overflow;
    shell?.setAttribute("inert", "");
    document.body.style.overflow = "hidden";
    return () => {
      shell?.removeAttribute("inert");
      document.body.style.overflow = previousOverflow;
      const target = previousFocus.current;
      previousFocus.current = null;
      if (target?.isConnected) target.focus();
      else document.getElementById("main")?.focus();
    };
  }, [selected]);

  const counts = overview?.counts;
  // 统计行只放任务级状态数。不放「需要人工」：那个数是一行一件事的**行数**，
  // 它唯一的住处是页面标题（`N 件事需要人工`）与卡片的构成行；counts.failed 是
  // 另一个口径（全部版本都失败的任务），只服务「需人工」筛选，两者不合成一个数。
  // 统计行只放「还在动」的两个数。已入库是永远不需要动作的那个，挪到右端与新鲜度
  // 同处：它是背景，不是待办。
  const stats = [
    { label: "下载中", value: counts?.downloading ?? 0 },
    { label: "加工中", value: counts?.processing ?? 0 },
  ];

  const refresh = async () => {
    setRefreshing(true);
    try {
      await onRefresh();
    } finally {
      setRefreshing(false);
    }
  };

  const pageCount = Math.max(1, Math.ceil(total / pageSize));

  return (
    <>
      <div className="flex flex-col gap-6">
      {/* 统计走一行，把首屏第一块让给「需要人工」；新鲜度与刷新贴在这一行的右端。 */}
      <div className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-2">
        <section
          aria-label="任务统计"
          className="dense flex flex-wrap items-baseline gap-x-4 gap-y-1 text-muted-foreground"
        >
          {stats.map((stat) => (
            <span key={stat.label} className="flex items-baseline gap-1.5">
              <span>{stat.label}</span>
              <span className="font-medium tabular-nums text-foreground">{stat.value}</span>
            </span>
          ))}
          {/* 手机上侧栏是藏起来的，心跳得在这里说两句：清完队列之后，「下一轮什么时候
              去拿我刚加的那部」与「对账还在跑吗」就是接下来两个问题，而它们不该在
              一张分页表格之后再回答。副作用那一行由下面那张队列卡承担。 */}
          {loopLines(overview, intervals, now)
            .slice(0, 2)
            .map((line) => (
              <span key={line.label} className="flex items-baseline gap-1.5 lg:hidden">
                <span>{line.label}</span>
                <span className={cn("tabular-nums", line.failing && "text-status-danger-text")}>
                  {line.text}
                </span>
              </span>
            ))}
        </section>

        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          <span className="tabular-nums">
            已入库 <span className="tabular-nums">{counts?.finished ?? 0}</span>
          </span>
          <span className="tabular-nums">
            {loadedAt ? (
              <>
                上次读取 <time dateTime={new Date(loadedAt).toISOString()}>{formatClock(loadedAt)}</time>
                （{formatWhen(loadedAt, now)}）
              </>
            ) : (
              "还没读到过"
            )}
          </span>
          <Button
            variant="outline"
            className="h-11 px-3 dense md:h-7 md:px-2.5 md:text-xs"
            onClick={() => void refresh()}
            disabled={refreshing}
          >
            <RefreshCw className={cn("size-3.5", refreshing && "animate-spin")} aria-hidden />
            刷新
          </Button>
        </div>
      </div>

      <ReceiptAnnouncer receipts={receiptsState.receipts} />

      <NeedsAttention
        attention={attention}
        stuck={sideEffects.filter((effect) => effect.stuck)}
        now={now}
        receiptsState={receiptsState}
        onOpenTask={openTask}
        onRefresh={onRefresh}
      />

      <Card>
        <CardHeader>
          <CardTitle as="h2" title="一行一任务，按 movie_id 聚合；版本以小 chip 展示。">
            任务
          </CardTitle>
          <CardDescription className="text-xs tabular-nums">
            {query.q || query.state ? `筛选后 ${total} 条` : `共 ${total} 条`}
          </CardDescription>
        </CardHeader>
        <CardContent className="px-0">
          <TaskFilters query={query} onQuery={onQuery} />
          {tasks.length === 0 ? (
            <EmptyTasks query={query} onQuery={onQuery} failedVersions={attention?.failed.length ?? 0} />
          ) : (
            <>
              <ul className="divide-y lg:hidden">
                {tasks.map((task) => (
                  <li key={task.movie_id}>
                    <button
                      type="button"
                      onClick={() => openTask(task.movie_id)}
                      aria-label={`打开 ${task.number} 的详情，${TASK_STATE_LOOK[isTaskState(task.state) ? task.state : "downloading"].label}，${task.versions.length} 个版本`}
                      className="flex w-full items-start justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/40 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring"
                    >
                      <span className="min-w-0">
                        <span className="block dense font-medium tabular-nums">
                          {task.number}
                        </span>
                        <span className="block font-mono text-xs text-muted-foreground">
                          {task.movie_id}
                        </span>
                        <span className="mt-1.5 flex flex-wrap gap-1">
                          {task.versions.map((version) => (
                            <VersionChip key={version.infohash} version={version} />
                          ))}
                        </span>
                        <span
                          className="mt-1 block text-xs tabular-nums text-muted-foreground"
                          title={formatAbsolute(task.updated_at)}
                        >
                          更新 {formatWhen(task.updated_at, now)}
                        </span>
                      </span>
                      <span className="flex shrink-0 items-center gap-2">
                        <StateBadge state={task.state} />
                        <ChevronRight className="size-4 text-muted-foreground" aria-hidden />
                      </span>
                    </button>
                  </li>
                ))}
              </ul>

              <div className="hidden overflow-x-auto lg:block">
              <table className="w-full table-fixed border-collapse dense lg:min-w-[34rem]">
                <colgroup>
                  <col className="w-[11rem]" />
                  <col className="w-[6.5rem]" />
                  <col />
                  <col className="w-[8rem]" />
                  <col className="w-8" />
                </colgroup>
                <thead>
                  <tr className="border-y bg-muted/40 text-left text-xs font-medium tracking-wide text-muted-foreground">
                    <th scope="col" className="px-4 py-2 font-medium">番号</th>
                    <th scope="col" className="px-4 py-2 font-medium">状态</th>
                    <th scope="col" className="px-4 py-2 font-medium">版本</th>
                    {/* 排序固定在「最近更新」：唯一会变的那一列，也是人判断新鲜度的依据。 */}
                    <th scope="col" aria-sort="descending" className="px-4 py-2 font-medium">
                      更新
                    </th>
                    <th scope="col" className="w-8 px-2 py-2">
                      <span className="sr-only">打开详情</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {tasks.map((task) => (
                    <tr
                      key={task.movie_id}
                      onClick={() => openTask(task.movie_id)}
                      className="cursor-pointer border-b transition-colors last:border-b-0 hover:bg-muted/40"
                    >
                      <td className="px-4 py-2.5 align-top">
                        <div className="font-medium tabular-nums">
                          <button
                            type="button"
                            onClick={(event) => {
                              event.stopPropagation();
                              openTask(task.movie_id);
                            }}
                            className="inline-flex min-h-6 items-center rounded-sm text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                            aria-label={`打开 ${task.number} 的详情`}
                          >
                            {task.number}
                          </button>
                        </div>
                        <div className="font-mono text-xs text-muted-foreground">{task.movie_id}</div>
                      </td>
                      <td className="px-4 py-2.5 align-top">
                        <StateBadge state={task.state} />
                      </td>
                      <td className="px-4 py-2.5 align-top">
                        <div className="flex flex-wrap gap-1">
                          {task.versions.map((version) => (
                            <VersionChip key={version.infohash} version={version} />
                          ))}
                        </div>
                      </td>
                      <td
                        className="px-4 py-2.5 align-top text-xs whitespace-nowrap tabular-nums text-muted-foreground"
                        title={formatAbsolute(task.updated_at)}
                      >
                        {formatWhen(task.updated_at, now)}
                      </td>
                      <td className="px-2 py-2.5 align-top text-muted-foreground">
                        <ChevronRight className="size-4" aria-hidden />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            </>
          )}
          {pageCount > 1 ? (
            <div className="flex flex-wrap items-center justify-between gap-2 border-t px-4 py-2.5 text-xs text-muted-foreground">
              <span className="tabular-nums">
                第 {query.page} / {pageCount} 页
              </span>
              <div className="flex items-center gap-1.5">
                <RowAction
                  variant="outline"
                  disabled={query.page <= 1}
                  onClick={() => onQuery({ ...query, page: query.page - 1 })}
                >
                  上一页
                </RowAction>
                <RowAction
                  variant="outline"
                  disabled={query.page >= pageCount}
                  onClick={() => onQuery({ ...query, page: query.page + 1 })}
                >
                  下一页
                </RowAction>
              </div>
            </div>
          ) : null}
        </CardContent>
      </Card>

      <SideEffectQueue
        effects={sideEffects.filter((effect) => !effect.stuck)}
        now={now}
        onRefresh={onRefresh}
      />

      <Card>
        <CardHeader>
          <CardTitle
            as="h2"
            title="三条循环各自单飞：轮询拉清单、对账补 qBittorrent 的漏、副作用队列各自重试。"
          >
            循环健康
          </CardTitle>
          <CardDescription>
            副作用失败退避重试，耗尽后移到「需要人工」等人工重发。
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-1.5 dense">
          {(["poll", "reconcile", "sidefx"] as const).map((loop) => {
            const health = overview?.loops?.[loop];
            const label = loop === "poll" ? "清单轮询" : loop === "reconcile" ? "对账" : "副作用";
            const interval = intervals[loop];
            return (
              <div key={loop} className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                <span>
                  {label}
                  {interval ? (
                    <span className="ml-1.5 text-xs text-muted-foreground">{interval}</span>
                  ) : null}
                </span>
                <span className="flex items-baseline gap-2 text-xs text-muted-foreground">
                  <span className="tabular-nums" title={formatAbsolute(health?.last_run_at)}>
                    {health?.last_run_at ? formatWhen(health.last_run_at, now) : "还没跑过"}
                  </span>
                  {health?.last_run_at ? (
                    <span className={health.last_ok ? "" : "text-status-danger-text"}>
                      {health.last_ok ? "成功" : "失败"}
                    </span>
                  ) : null}
                </span>
              </div>
            );
          })}
          <Separator className="my-1" />
          <Button variant="link" size="sm" asChild className="h-11 self-start px-0 text-sm md:h-7">
            <Link to="/settings/app">去集成设置</Link>
          </Button>
        </CardContent>
      </Card>
      </div>

      {selected ? (
        <TaskDrawer
          key={selected}
          movieID={selected}
          onClose={() => setSelected(null)}
          onRefresh={onRefresh}
        />
      ) : null}
    </>
  );
}

/** NeedsAttention 是「需要人工」区（规格 §8.2：置顶，行内直接动手）。
 *
 * 三组行：失败 / 停滞 / 驻留副作用，每组带计数。任务侧的两组再按番号收一次：
 * 一部作品两个版本同时失败时，出现的是一个番号 + 两颗按钮（而不是两行同款「重试」），
 * 版本的身份（中文 / 破解 / M-Team 种子）写在每一条失败下面。
 *
 * 动作按病因决定（`lib/remedy.ts`）：宿主机问题不给重试、设置问题给一条真链接到
 * 那一节（`/settings/telegram`），只在流水线自己就有救的时候，才让重试做主角。
 */
function NeedsAttention({
  attention,
  stuck,
  now,
  receiptsState,
  onOpenTask,
  onRefresh,
}: {
  attention: Attention | null;
  stuck: SideEffect[];
  now: number;
  receiptsState: ReturnType<typeof useReceipts>;
  onOpenTask: (movieID: string) => void;
  onRefresh: () => Promise<void>;
}) {
  const failed = attention?.failed ?? [];
  const stalled = attention?.stalled ?? [];
  const failedGroups = groupByNumber(failed);
  const stalledGroups = groupByNumber(stalled);
  // 回执住在卡片上而不是行里：行会在按下成功之后消失，回执却是那一刻唯一的证据。
  const { receipts, push: receiptOf, dismiss: dismissReceipt, listRef: receiptsRef } = receiptsState;
  // 被上限切过的那一半只数得到 50，所以数字后面跟一个 +，不把截断说成总量。
  const cut = {
    failed: attention?.failed_truncated ?? false,
    stalled: attention?.stalled_truncated ?? false,
  };

  // 总数是**行数**：一行就是一个要动手的对象（一个番号的失败版本、一个番号的停滞
  // 下载，或一条驻留副作用）。这是「我还有几件事」唯一的答案，说明里给它的构成，
  // 不给第二个总数。
  const total = failedGroups.length + stalledGroups.length + stuck.length;
  const broke = [
    failedGroups.length > 0 && `失败 ${failedGroups.length}${cut.failed ? "+" : ""} 部`,
    stalledGroups.length > 0 && `停滞 ${stalledGroups.length}${cut.stalled ? "+" : ""} 部`,
    stuck.length > 0 && `驻留副作用 ${stuck.length} 条`,
  ].filter(Boolean);

  // 「我在这里能不能修」才是第一问：能修的一组给重试，修不动的一组只给查看任务，
  // 并把那句「重试不会变好」收起来（它的全文带路径，手机上一行要占四行）。
  const canFixHere = (group: FailureGroup) =>
    group.items.some((item) => remedyForFailure(item).retryable);
  const fixableGroups = failedGroups.filter(canFixHere);
  const elsewhereGroups = failedGroups.filter((group) => !canFixHere(group));

  const renderFailedGroup = (group: FailureGroup, collapseHint = false) => (
    <VersionGroup
      key={group.movieID}
      group={group}
      kind="failed"
      collapseHint={collapseHint}
      now={now}
      busy={taskRetry.busy.has(taskRetryKey(group.movieID, false))}
      onRetry={(infohashes) =>
        void taskRetry.retry(group.movieID, false, {
          title: group.number,
          infohashes,
          names: versionNames(group),
        })
      }
      onOpenTask={() => onOpenTask(group.movieID)}
    />
  );

  const retryHooks = { onReceipt: receiptOf, focusFallback: receiptsRef };
  const taskRetry = useTaskRetry(onRefresh, retryHooks);
  // 任务侧重试与副作用重发是两件事，各有自己的忙位；回执则合并成一条流：
  // 作者关心的不是「哪一个子系统的」，而是「我刚才那一按成了没有」。
  const sideEffect = useSideEffectRetry(onRefresh, retryHooks);

  // 没事可做、也没有刚做完的回执：这张卡整块不出现。页面标题已经说了「没有要动手的事」，
  // 再留一张空卡把同一个意思说第二遍，只是给首屏加了一段家具。
  if (total === 0 && receipts.length === 0) return null;

  return (
    <Card className={cn(total > 0 && "ring-status-danger-border")}>
      <CardHeader>
        <div>
          {/* 页标题已经说了「N 件事需要人工」，卡片不把同一个词再说一遍：可见的首行
              就是它的构成（失败几部 · 停滞几部 · 驻留几条），标题只留给大纲与读屏。 */}
          <h2 className="sr-only">需要人工</h2>
          {/* 这行是卡片唯一可见的首行，所以它得和它下面的卡标题一个分量：以前它是
              13px/400，比「任务」「副作用队列」的 16px/500 都轻。播报不在这里——
              那个数由 home.tsx 的常驻活区开口，这里不再抢第二个声音。 */}
          <p className="flex items-start gap-2 text-base leading-relaxed font-medium text-foreground">
            <AlertTriangle
              className={cn(
                "mt-0.5 size-4 shrink-0",
                total > 0 ? "text-status-danger-text" : "text-muted-foreground",
              )}
              aria-hidden
            />
            <span>{total === 0 ? "已经清完了。" : broke.join(" · ")}</span>
          </p>
        </div>
      </CardHeader>
      {total > 0 || receipts.length > 0 ? (
        <CardContent>
          <ReceiptList receipts={receipts} now={now} listRef={receiptsRef} onDismiss={dismissReceipt} />

          {taskRetry.error ? (
            <div className="mb-2">
              <Notice tone="bad" onClose={taskRetry.clearError}>
                {taskRetry.error}
              </Notice>
            </div>
          ) : null}

          {total > 0 ? (
            // 顺序就是「我在这里能不能修」：能修 → 要等一等（停滞）→ 要去别处 → 驻留。
            <div className="divide-y">
              {fixableGroups.length > 0 ? (
                <section aria-label="能在这里修的失败版本" className="py-3 first:pt-0 last:pb-0">
                  <AttentionHeading label="能在这里修" />
                  <div className="divide-y">
                    {fixableGroups.map((group) => renderFailedGroup(group))}
                  </div>
                </section>
              ) : null}

              {stalledGroups.length > 0 ? (
                <section aria-label="停滞的下载" className="py-3 first:pt-0 last:pb-0">
                  <AttentionHeading label="停滞" />
                  {/* 停滞的补法是一句常量（先在 qBittorrent 里确认…），整节说一次就够了。 */}
                  <p className="mb-2 dense leading-relaxed text-foreground">
                    {remedyForStalled().hint}
                  </p>
                  <div className="divide-y">
                    {stalledGroups.map((group) => (
                      <VersionGroup
                        key={group.movieID}
                        group={group}
                        kind="stalled"
                        hints={false}
                        now={now}
                        busy={taskRetry.busy.has(taskRetryKey(group.movieID, false))}
                        onRetry={(infohashes) =>
                          void taskRetry.retry(group.movieID, false, {
                            title: group.number,
                            infohashes,
                            names: versionNames(group),
                          })
                        }
                        onOpenTask={() => onOpenTask(group.movieID)}
                      />
                    ))}
                  </div>
                </section>
              ) : null}

              {elsewhereGroups.length > 0 ? (
                <section aria-label="要去别处修的失败版本" className="py-3 first:pt-0 last:pb-0">
                  <AttentionHeading label="要去别处修" />
                  <div className="divide-y">
                    {elsewhereGroups.map((group) => renderFailedGroup(group, true))}
                  </div>
                </section>
              ) : null}

              {stuck.length > 0 ? (
                <section aria-label="驻留的副作用" className="py-3 first:pt-0 last:pb-0">
                  <AttentionHeading label="驻留副作用" />
                  <div className="divide-y">
                    {stuck.map((effect) => (
                      <StuckRow
                        key={effect.id}
                        effect={effect}
                        notice={sideEffect.errors[effect.id]}
                        busy={sideEffect.busy === effect.id}
                        onRetry={() => void sideEffect.retry(effect.id, sideEffectTitle(effect))}
                      />
                    ))}
                  </div>
                </section>
              ) : null}
            </div>
          ) : null}
        </CardContent>
      ) : null}
    </Card>
  );
}

/** ReceiptDraft 是一条新回执（还没编号）：番号或待办名 + 结局 + 一句后果。 */
type ReceiptDraft = RetryNotice & {
  title: string;
  /** 回执说的那个动作本身：任务侧是「重试」，副作用侧是「重发」。 */
  verb?: "重试" | "重发";
};

/** Receipt 是已经发出的一条回执。 */
type Receipt = ReceiptDraft & { id: number; at: number };

/** 回执留 3 条、活 10 分钟：足够看见刚才那几下，又不至于把卡片变成一块日志。 */
const RECEIPT_LIMIT = 3;
const RECEIPT_TTL = 10 * 60_000;

/**
 * useReceipts 是「动完手」的回执队列。
 *
 * 它必须挂在卡片上，不能挂在行里：重试成功之后那一行就从树上消失了，而回执正是
 * 那一刻唯一的证据。以前回执写在行里，于是被拒时有话、成功时无话。
 */
function useReceipts() {
  const [receipts, setReceipts] = React.useState<Receipt[]>([]);
  const nextID = React.useRef(1);
  const listRef = React.useRef<HTMLDivElement>(null);

  const push = React.useCallback((draft: ReceiptDraft) => {
    setReceipts((current) =>
      [{ ...draft, id: nextID.current++, at: Date.now() }, ...current].slice(0, RECEIPT_LIMIT),
    );
  }, []);

  const dismiss = React.useCallback((id: number) => {
    setReceipts((current) => current.filter((receipt) => receipt.id !== id));
  }, []);

  // 回执自己退场：不用人清理，但退场前有整整十分钟可被读到。
  React.useEffect(() => {
    if (receipts.length === 0) return;
    const timer = window.setInterval(() => {
      const cutoff = Date.now() - RECEIPT_TTL;
      setReceipts((current) =>
        current.some((receipt) => receipt.at < cutoff)
          ? current.filter((receipt) => receipt.at >= cutoff)
          : current,
      );
    }, 30_000);
    return () => window.clearInterval(timer);
  }, [receipts.length]);

  return { receipts, push, dismiss, listRef };
}

/** ReceiptList 是卡片顶部的回执行：一行一条，语气词打头，能逐条收起。 */
function ReceiptList({
  receipts,
  now,
  listRef,
  onDismiss,
}: {
  receipts: Receipt[];
  now: number;
  listRef: React.RefObject<HTMLDivElement | null>;
  onDismiss: (id: number) => void;
}) {
  if (receipts.length === 0) return null;
  return (
    <div
      ref={listRef}
      tabIndex={-1}
      role="group"
      aria-label="操作回执"
      className="mb-3 divide-y rounded-lg bg-muted/50 px-3 outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
    >
      {receipts.map((receipt) => (
        <div key={receipt.id} className="flex items-start gap-2 py-1.5 text-xs leading-relaxed">
          <span
            className={cn(
              "shrink-0 font-medium",
              receipt.tone === "ok" ? "text-status-done-text" : "text-status-progress-text",
            )}
          >
            {receipt.tone === "ok" ? `已${receipt.verb ?? "重试"}` : "没成"}
          </span>
          <span className="min-w-0 flex-1 break-words">
            <span className="font-medium tabular-nums">{receipt.title}</span>
            <span> · {receipt.text}</span>
            <span className="ml-1.5 tabular-nums text-muted-foreground">
              {formatWhen(receipt.at, now)}
            </span>
          </span>
          <button
            type="button"
            onClick={() => onDismiss(receipt.id)}
            aria-label={`收起回执：${receipt.title}`}
            className="-m-1 grid size-8 shrink-0 place-items-center rounded-md text-muted-foreground transition-colors before:absolute before:-inset-2 before:content-[''] relative hover:bg-background hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring md:size-6"
          >
            <X className="size-3" aria-hidden />
          </button>
        </div>
      ))}
    </div>
  );
}

/**
 * AttentionHeading 只报分节名，不再报数：数已经有唯一的说话人（页标题 + 构成行）。
 * 以前这里写「失败 4 条版本」而构成行写「失败 3 部」，两个单位并排 40px，
 * 人得自己算它们是不是同一个东西。
 */
function AttentionHeading({ label }: { label: string }) {
  return <h3 className="mb-1 text-xs font-medium text-muted-foreground">{label}</h3>;
}

/**
 * VersionGroup 是「一部作品的若干条同病版本」：番号 + 一颗写明爆炸半径的重试
 * + 逐版的身份、失败步、原因与补法。
 *
 * 行按版本切、动作按任务发：所以按钮上要写清它会动几条版本（「重试全部 2 条版本」），
 * 回执记在卡片上（`ReceiptList`），不在这一行里——按下成功之后这一行就没了。
 */
function VersionGroup({
  group,
  kind,
  hints = true,
  collapseHint = false,
  now,
  busy,
  onRetry,
  onOpenTask,
}: {
  group: FailureGroup;
  kind: "failed" | "stalled";
  /** 分节已经把补法说过了就不再逐行印（停滞是常量串）。 */
  hints?: boolean;
  /** 「要去别处修」那一组：补法默认收起，只留一句「重试不会变好」。 */
  collapseHint?: boolean;
  now: number;
  busy: boolean;
  /** 只把这条组里还有救的版本交出去：按钮上的数字就是会动的条数。 */
  onRetry: (infohashes: string[]) => void;
  onOpenTask: () => void;
}) {
  const remedyOf = (item: AttentionItem) =>
    kind === "failed" ? remedyForFailure(item) : remedyForStalled();
  // 宿主机病因（权限/只读/满盘）那条自己也重试不好：不点名、不进按钮，也不跟着被重试。
  // 以前只要组里有一条有救，按钮就会把整个任务一起重新发下去，那条永远失败的版本
  // 于是从「重试不会变好」的规矩里滑了过去。
  const retryTargets = group.items.filter((item) => remedyOf(item).retryable);

  return (
    <div className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:flex-wrap sm:items-start sm:justify-between sm:gap-x-4">
      <div className="min-w-0 flex-1 space-y-1.5">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 dense font-medium tabular-nums">
          {group.number}
          <span className="font-mono text-xs font-normal text-muted-foreground">{group.movieID}</span>
        </div>
        <ul className="space-y-2">
          {group.items.map((item, index) => {
            const remedy = remedyOf(item);
            // 同一组里连着两条同一个病因（两版都挂满盘、都停在同一句告警）时，
            // 补法只说一次：逐行重复的那句话是纯噪声，还会把真正的差别淹掉。
            const previousHint = index > 0 ? remedyOf(group.items[index - 1]).hint : "";
            const hint = !hints || remedy.hint === previousHint ? "" : remedy.hint;
            // 原因也一样：两个版本沦于同一句失败时，第二过只留一行「原因同上」。
            const repeatedReason = index > 0 && group.items[index - 1].reason === item.reason;
            return (
              <li key={item.infohash} className="space-y-1">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
                  <Badge variant="outline" className="h-5 font-normal text-muted-foreground">
                    {versionIdentity(item)}
                  </Badge>
                  <Badge
                    variant="outline"
                    className={
                      kind === "failed"
                        ? "border-transparent bg-status-danger-surface text-status-danger-text"
                        : "border-status-progress-border bg-status-progress-surface text-status-progress-text"
                    }
                  >
                    {kind === "failed" ? `${phaseLabel(item.phase)}失败` : "下载停滞"}
                  </Badge>
                  <span className="text-muted-foreground" title={formatAbsolute(item.at)}>
                    {formatWhen(item.at, now)}
                  </span>
                </div>
                {repeatedReason ? (
                  <p className="text-xs text-muted-foreground">原因同上</p>
                ) : (
                  <p className="dense leading-relaxed break-words text-foreground">
                    {item.reason}
                  </p>
                )}
                {hint ? (
                  // 补法按「人该照哪句动」给权重：给了按钮时它是脚注，
                  // 不给按钮时它就是那句指令（宿主机问题的「重试不会变好」）。
                  remedy.retryable || !collapseHint ? (
                    <p
                      className={cn(
                        "leading-relaxed break-words",
                        remedy.retryable ? "text-xs text-muted-foreground" : "dense text-foreground",
                      )}
                    >
                      {hint}
                    </p>
                  ) : (
                    <CollapsibleRemedy hint={hint} />
                  )
                ) : null}
              </li>
            );
          })}
        </ul>
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-1.5">
        {retryTargets.length > 0 ? (
          <RowAction
            variant="outline"
            disabled={busy}
            data-retry={group.movieID}
            onClick={() => onRetry(retryTargets.map((item) => item.infohash))}
          >
            <RotateCcw className={cn("size-3.5", busy && "animate-spin")} aria-hidden />
            {retryTargets.length > 1 ? `重试 ${retryTargets.length} 条版本` : "重试"}
          </RowAction>
        ) : null}
        <RowAction variant="ghost" onClick={onOpenTask}>
          查看任务
        </RowAction>
      </div>
    </div>
  );
}

/**
 * ReceiptAnnouncer 是「刚才那一按成了没有」的唯一播报位。
 *
 * 它常驻在 Panel 上（卡片会被清空、会消失，而这个位不能），且只拿最新的那一条：
 * 三条一起念会把一句结论变成一段日志。可见的回执行是它的视觉版，不再兼任活区。
 */
function ReceiptAnnouncer({ receipts }: { receipts: Receipt[] }) {
  const latest = receipts[0];
  const text = latest
    ? `${latest.tone === "ok" ? `已${latest.verb ?? "重试"}` : "没成"} ${latest.title} · ${latest.text}`
    : "";
  return (
    <p role="status" aria-live="polite" aria-atomic="true" className="sr-only">
      {text}
    </p>
  );
}

/**
 * CollapsibleRemedy 把「修不动」那一行的补法收成一行。
 *
 * 在失败节里，这一句（含 `/media/...` 路径）手机上要占四行，而它所在的行本来就没有
 * 可点的动作。收起后操作员先看到「重试不会变好」，需要时再展开看要改哪个目录。
 */
function CollapsibleRemedy({ hint }: { hint: string }) {
  const [open, setOpen] = React.useState(false);
  return (
    <div>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
        className="dense inline-flex min-h-11 items-center gap-1 rounded-sm text-muted-foreground underline underline-offset-4 hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring md:min-h-0"
      >
        重试不会变好
        <span aria-hidden>·</span>
        {open ? "收起原因" : "展开原因"}
      </button>
      {open ? (
        <p className="dense leading-relaxed break-words text-foreground">{hint}</p>
      ) : null}
    </div>
  );
}

/** StuckRow 是驻留副作用的一行：它缺的那一样东西在哪一节，就给哪一节的链接。 */
function StuckRow({
  effect,
  notice,
  busy,
  onRetry,
}: {
  effect: SideEffect;
  notice?: string;
  busy: boolean;
  onRetry: () => void;
}) {
  const remedy = remedyForSideEffect(effect);
  return (
    <div className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:flex-wrap sm:items-start sm:justify-between sm:gap-x-4">
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 dense font-medium">
          {sideEffectTitle(effect)}
          <Badge
            variant="outline"
            className="border-transparent bg-status-danger-surface text-status-danger-text"
          >
            驻留副作用
          </Badge>
          <span className="text-xs font-normal tabular-nums text-muted-foreground">
            {effect.attempts > 0 ? `已试 ${effect.attempts} 次` : "还没试过"}
          </span>
        </div>
        <p className="dense leading-relaxed break-words text-foreground">
          {effect.last_error || "自动重试已放弃，等人工重发。"}
        </p>
        {remedy.hint ? (
          <p className="text-xs leading-relaxed break-words text-muted-foreground">{remedy.hint}</p>
        ) : null}
        {notice ? (
          <div className="pt-0.5">
            <Notice tone="bad">{notice}</Notice>
          </div>
        ) : null}
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-2">
        {remedy.settings ? (
          <RowAction variant="outline" asChild>
            <Link to={`/settings/${remedy.settings}`}>去 {settingsLabel(remedy.settings)} 设置</Link>
          </RowAction>
        ) : null}
        <RowAction variant={remedy.settings ? "ghost" : "outline"} disabled={busy} onClick={onRetry}>
          <RotateCcw className={cn("size-3.5", busy && "animate-spin")} aria-hidden />
          重发
        </RowAction>
      </div>
    </div>
  );
}

/** versionIdentity 是一条版本的人话身份：标签（中文/破解）优先，没有就报来源。 */
function versionIdentity(item: AttentionItem): string {
  const source = sourceLabel(item.source);
  return item.label ? `${item.label} · ${source}` : source;
}

/**
 * versionNames 是 infohash → 身份标签的映射。
 *
 * 回执按「中文 / 破解」说话，不拿 8 位哈希当名字：屏幕上没有任何地方写着哈希，
 * 多版本回执里的 `3f9ac1b7：回到加工开头` 是一条人对不上的信息。
 */
function versionNames(group: FailureGroup): Record<string, string> {
  return Object.fromEntries(
    group.items.map((item) => [item.infohash, item.label || sourceLabel(item.source)]),
  );
}

/** RetryNotice 是一次任务重试的结果提示：成功用 ok，被拒/失败用 warn。 */
type RetryNotice = { tone: "ok" | "warn"; text: string };

/** RetryPlan 是一次重试的作用域：叫什么名字、只动哪几条、它们各自叫什么。 */
type RetryPlan = {
  /** 回执上的名字（番号）；不给就用 movie_id。 */
  title?: string;
  /** 只动这几条版本；不给＝整个任务（抽屉的强制重刮要这个语义）。 */
  infohashes?: string[];
  /** infohash → 身份标签，回执按它说话。 */
  names?: Record<string, string>;
};

/**
 * useTaskRetry 是人工重试（规格 §6.1 / P13）的共用逻辑：忙位、错误、逐版结论。
 *
 * 忙位按行记（键 = movie_id + 是否强制），所以别人在看行 B 时行 A 不会被重新
 * 放开。结论同时写两份：抽屉里按 movie_id 留一份（那里行不会消失），卡片上传一条
 * 回执（那里行会消失）。
 */
function useTaskRetry(
  onRefresh: () => Promise<void>,
  hooks?: {
    onReceipt?: (receipt: ReceiptDraft) => void;
    /** 行在成功后消失时的焦点去处：回执列表。 */
    focusFallback?: React.RefObject<HTMLDivElement | null>;
  },
) {
  const { onReceipt, focusFallback } = hooks ?? {};
  const [busy, setBusy] = React.useState<ReadonlySet<string>>(() => new Set());
  const busyRef = React.useRef<ReadonlySet<string>>(new Set());
  const [error, setError] = React.useState<string | null>(null);
  const [notices, setNotices] = React.useState<Record<string, RetryNotice>>({});
  const clearError = React.useCallback(() => setError(null), []);
  const clearNotice = React.useCallback((movieID: string) => {
    setNotices((current) => {
      if (!(movieID in current)) return current;
      const next = { ...current };
      delete next[movieID];
      return next;
    });
  }, []);

  const retry = React.useCallback(
    async (movieID: string, force: boolean, plan?: RetryPlan) => {
      const title = plan?.title ?? movieID;
      // 按下时元素还在；刷新之后它就没了，所以先把它的引用拿在手上。
      const origin = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      const key = taskRetryKey(movieID, force);
      // 同一行连点不二次下发：两个 POST 会让后到的那张回执顶掉前一张。
      if (busyRef.current.has(key)) return;
      busyRef.current = new Set(busyRef.current).add(key);
      setBusy(busyRef.current);
      setError(null);
      // 忙位先释放：以前它跟在 afterPaint 后面，于是按钮在「等下一帧」的那段时间里
      // 还在转，后台标签页里甚至一直转（rAF 不跑，finally 也就不跑）。
      let released = false;
      const release = () => {
        if (released) return;
        released = true;
        const next = new Set(busyRef.current);
        next.delete(key);
        busyRef.current = next;
        setBusy(next);
      };
      setNotices((current) => {
        if (!(movieID in current)) return current;
        const next = { ...current };
        delete next[movieID];
        return next;
      });
      try {
        const result = await api.retryTask(movieID, force, plan?.infohashes);
        const notice = summarizeRetry(result.results, plan?.names);
        setNotices((current) => ({ ...current, [movieID]: notice }));
        onReceipt?.({ title, ...notice });
        await onRefresh();
        release();
        // 行在成功之后从树上消失了：焦点跟到下一条还能按的重试，一条不剩才落到回执上。
        // 落到回执会把整页滚回卡片头，而回执本身已经在播报了。
        await afterPaint();
        if (focusFallback && origin && !origin.isConnected) {
          const next = document.querySelector<HTMLElement>("[data-retry]:not([disabled])");
          if (next) next.focus();
          else focusFallback.current?.focus();
        }
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      } finally {
        release();
      }
    },
    [onRefresh, onReceipt, focusFallback],
  );

  return { busy, error, notices, retry, clearError, clearNotice };
}

/**
 * afterPaint 等到这次刷新真的提交到 DOM 之后。
 *
 * await 一个 fetch 回来的时刻，React 的 setState 可能还没提交，`isConnected` 此时
 * 仍是真的；等到下一帧，行已经真的没了，判断才作数。
 *
 * 但「下一帧」不能只靠 rAF：后台标签页、锁屏、被遮住的窗口都不会跑 rAF，而按完就
 * 锁屏正是手机上的常态动作。所以这里同时排一个超时兑底——谁先到算谁，永不挂死。
 */
function afterPaint(): Promise<void> {
  return new Promise((resolve) => {
    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      resolve();
    };
    if (typeof window === "undefined") {
      finish();
      return;
    }
    if (typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(() => window.requestAnimationFrame(finish));
    }
    // 后台标签页里 rAF 不触发，这一行是那条链的唯一出口。
    window.setTimeout(finish, 120);
  });
}

/** taskRetryKey 是忙位的键：同一任务的「重试」与「强制重刮」互不复用。 */
function taskRetryKey(movieID: string, force: boolean): string {
  return `${movieID}:${force ? "force" : "retry"}`;
}

/** summarizeRetry 把逐版结果折成一句话：被拒/失败的排在前面。
 *
 * 回执现在落在出事的那一行里，所以单版本时不再报 8 位哈希（那一行已经写了番号）；
 * 只有一任务多版本时，哈希才是分流的关键。
 */
function summarizeRetry(
  results: TaskRetryOutcome[],
  names?: Record<string, string>,
): RetryNotice {
  if (results.length === 0) {
    return { tone: "warn", text: "这条任务还没有版本，没有可重试的东西。" };
  }

  // 名字优先用版本自己的判别词；抽屉（没有 names）仍然回落到短哈希。
  const prefixOf = (result: TaskRetryOutcome) =>
    names?.[result.infohash] || (results.length > 1 ? shortHash(result.infohash) : "");

  const blocked = results.filter((result) => result.action === "skipped" || result.action === "failed");
  if (blocked.length > 0) {
    return {
      tone: "warn",
      text: blocked.map((result) => labelResult(result, prefixOf(result))).join("；"),
    };
  }

  // 不再前缀「已下发：」：回执与抽屉的提示都自己带结局语气词，
  // 正文只说发生了什么（「回到加工开头」），一句话只有一个说话人。
  const done = results.map((result) => labelResult(result, prefixOf(result)));
  return { tone: "ok", text: done.join("；") };
}

/** labelResult 拼一条版本结局：有名字就前缀上去，没有就不前缀。 */
function labelResult(result: TaskRetryOutcome, prefix = ""): string {
  const body =
    result.action === "skipped" || result.action === "failed"
      ? result.reason
      : retryActionLabel(result.action);
  return prefix ? `${prefix}：${body}` : body;
}

function retryActionLabel(action: TaskRetryOutcome["action"]): string {
  switch (action) {
    case "reset_processing":
      return "回到加工开头";
    case "readded":
      return "重新加种，回到下载中";
    case "skipped":
      return "已跳过";
    case "failed":
      return "这次没成";
    default:
      return action;
  }
}

function shortHash(infohash: string): string {
  return infohash.slice(0, 8);
}

/**
 * useSideEffectRetry 是「重发」的共用逻辑：忙位、逐条错误、刷新面板。
 *
 * 重发成功时驻留行会退回队列（或干脆消失），与任务重试一样，证据只能留在卡片上，
 * 所以它也传回执；副作用队列卡自己传空，那里的行不会跑掉，原地就看得见。
 */
function useSideEffectRetry(
  onRefresh: () => Promise<void>,
  hooks?: {
    onReceipt?: (receipt: ReceiptDraft) => void;
    focusFallback?: React.RefObject<HTMLDivElement | null>;
  },
) {
  const { onReceipt, focusFallback } = hooks ?? {};
  const [busy, setBusy] = React.useState<number | null>(null);
  const [errors, setErrors] = React.useState<Record<number, string>>({});

  const retry = React.useCallback(
    async (id: number, title = `副作用 ${id}`) => {
      const origin = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      if (busy === id) return;
      setBusy(id);
      setErrors((current) => {
        if (!(id in current)) return current;
        const next = { ...current };
        delete next[id];
        return next;
      });
      try {
        await api.retrySideEffect(id);
        onReceipt?.({ title, tone: "ok", verb: "重发", text: "已重发，回到副作用队列。" });
        await onRefresh();
        // 忙位先放：等下一帧只属于焦点交接，不能把按钮的旋转一起拖住。
        setBusy(null);
        await afterPaint();
        if (focusFallback && origin && !origin.isConnected) {
          const next = document.querySelector<HTMLElement>("[data-retry]:not([disabled])");
          if (next) next.focus();
          else focusFallback.current?.focus();
        }
      } catch (cause) {
        setErrors((current) => ({
          ...current,
          [id]: cause instanceof Error ? cause.message : String(cause),
        }));
      } finally {
        setBusy(null);
      }
    },
    [busy, onRefresh, onReceipt, focusFallback],
  );

  return { busy, errors, retry };
}

/** SideEffectQueue 是底部的副作用队列：排队中的待办与它们的下一次尝试时间。 */
function SideEffectQueue({
  effects,
  now,
  onRefresh,
}: {
  effects: SideEffect[];
  now: number;
  onRefresh: () => Promise<void>;
}) {
  const { busy, errors, retry } = useSideEffectRetry(onRefresh);

  // 队列空着就不渲染这张卡：没有待办时，少一块结构就是信息本身。
  if (effects.length === 0) return null;

  return (
    <Card>
      <CardHeader>
        <CardTitle
          as="h2"
          title="删清单项、刷 Jellyfin、发通知都走这张表：同目标只排一条，失败退避重试，耗尽后移到「需要人工」。"
        >
          副作用队列
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-2">
        <div className="divide-y">
          {effects.map((effect) => {
            return (
              <div
                key={effect.id}
                className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:flex-wrap sm:items-start sm:justify-between sm:gap-x-4"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 dense font-medium">
                    {sideEffectTitle(effect)}
                    <span className="text-xs font-normal tabular-nums text-muted-foreground">
                      {effect.attempts > 0 ? `已试 ${effect.attempts} 次` : "还没试过"}
                    </span>
                  </div>
                  <p
                    className="text-xs tabular-nums text-muted-foreground"
                    title={formatAbsolute(effect.next_attempt_at)}
                  >
                    下次尝试 {formatFuture(effect.next_attempt_at, now)}
                    {effect.last_error ? ` · 上次：${effect.last_error}` : ""}
                  </p>
                  {errors[effect.id] ? (
                    <div className="mt-1">
                      <Notice tone="bad">{errors[effect.id]}</Notice>
                    </div>
                  ) : null}
                </div>
                <div className="flex shrink-0 flex-wrap items-center gap-1.5">
                  <RowAction
                    variant="outline"
                    disabled={busy === effect.id}
                    onClick={() => void retry(effect.id)}
                  >
                    <RotateCcw
                      className={cn("size-3.5", busy === effect.id && "animate-spin")}
                      aria-hidden
                    />
                    重发
                  </RowAction>
                </div>
              </div>
            );
          })}
        </div>
      </CardContent>
    </Card>
  );
}

/** 一条待办的人话标题：番号（有的话）+ 这一条在做什么。 */
function sideEffectTitle(effect: SideEffect): string {
  const number = typeof effect.payload.number === "string" ? effect.payload.number : "";
  const label = sideEffectKindLabel(effect);
  return number ? `${number} · ${label}` : label;
}

/**
 * 待办的动作名。
 *
 * 通知的三种死法要能分辨（规格 §5.6）：token 无效、chat 未激活、其余发送失败——
 * 因为驻留的通知**不会**自动禁用集成，人要看着这一行去改哪一格。
 */
function sideEffectKindLabel(effect: SideEffect): string {
  switch (effect.kind) {
    case "list_removal":
      return "删清单项";
    case "jellyfin_refresh":
      return "刷新 Jellyfin";
    case "notify": {
      const event = notifyEventOf(effect.target_key);
      if (!event) return "发 Telegram";
      return `发 Telegram · ${notifyEventLabels[event]}`;
    }
    default:
      return effect.kind;
  }
}

/**
 * RowAction 是行内动作的唯一尺寸：md 以下 36px 热区 + 13px 字，md 起收回 28px + 12px。
 *
 * 以前这段类名在本文件里抄了十遍，DESIGN 的「md 以下 h-9 / 13px」只以散文存在；
 * 收成一个组件之后，改规则就是改这一行。
 */
function RowAction({ className, variant = "outline", ...props }: React.ComponentProps<typeof Button>) {
  return (
    <Button
      variant={variant}
      // 手机上一律 44px 热区：值班时是单手刷屏，36px 的行内动作实测要瞄。
      className={cn("h-11 px-3 dense md:h-7 md:px-2.5 md:text-xs", className)}
      {...props}
    />
  );
}

const STATE_FILTERS: { value: TaskState | ""; label: string }[] = [
  { value: "", label: "全部" },
  { value: "downloading", label: "下载中" },
  { value: "processing", label: "加工中" },
  { value: "finished", label: "已入库" },
  { value: "failed", label: "需人工" },
];

/**
 * TaskFilters 是任务卡的搜索与状态筛选（规格 §8.4 的 ?state=&q=）。
 *
 * 筛选住在地址栏里：F5 不丢，还能把「只看需人工」发给自己的另一个标签页。
 */
function TaskFilters({ query, onQuery }: { query: PanelQuery; onQuery: (next: PanelQuery) => void }) {
  const [draft, setDraft] = React.useState(query.q);
  const [seenQ, setSeenQ] = React.useState(query.q);

  // 地址栏是真相：别人改了 URL（或点了「清除筛选」）时输入框要跟上。
  // 在渲染期对齐（而不是放进 effect），避免一次多余的级联渲染。
  if (seenQ !== query.q) {
    setSeenQ(query.q);
    setDraft(query.q);
  }

  // 边打字边发请求会把 142 行的库打成连点器：停手 300ms 再查。
  React.useEffect(() => {
    if (draft === query.q) return;
    const timer = window.setTimeout(() => onQuery({ page: 1, q: draft, state: query.state }), 300);
    return () => window.clearTimeout(timer);
  }, [draft, query.q, query.state, onQuery]);

  return (
    <div className="flex flex-col gap-2 border-b px-4 pb-2.5 sm:flex-row sm:items-center sm:justify-between">
      <div className="relative flex items-center sm:w-64">
        <Search
          className="pointer-events-none absolute left-2.5 size-3.5 text-muted-foreground"
          aria-hidden
        />
        <TextInput
          type="search"
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          placeholder="搜番号或 movie_id"
          aria-label="搜番号或 movie_id"
          className="h-11 pl-8 md:h-8"
        />
      </div>
      <div className="flex flex-wrap items-center gap-2" role="group" aria-label="按状态筛选">
        {STATE_FILTERS.map((filter) => {
          const current = query.state === filter.value;
          return (
            <button
              key={filter.value || "all"}
              type="button"
              aria-pressed={current}
              onClick={() => onQuery({ page: 1, q: query.q, state: filter.value })}
              // 「需人工」筛的是**任务级**状态：全部版本都失败的作品。失败版本本身属于
              // 上面那张卡，两者口径不同，所以这里把它们说出来。
              title={
                filter.value === "failed" ? "任务级：只算全部版本都失败的作品" : undefined
              }
              className={cn(
                "h-11 rounded-lg px-3 dense transition-colors md:h-7 md:px-2.5 md:text-xs",
                "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
                current
                  ? "bg-secondary font-medium text-secondary-foreground"
                  : "text-muted-foreground hover:bg-muted",
              )}
            >
              {filter.label}
            </button>
          );
        })}
      </div>
    </div>
  );
}

function EmptyTasks({
  query,
  onQuery,
  failedVersions,
}: {
  query: PanelQuery;
  onQuery: (next: PanelQuery) => void;
  /** 「需要人工」里的失败版本条数：用来解释「为什么筛需人工是空的」。 */
  failedVersions: number;
}) {
  // 筛「需人工」却是空的，不等于没事：整个任务没失败、但里面的破解版失败时，
  // 任务状态会随已入库的中文版走。这时候不说清，人会以为上面那张卡在说谎。
  if (query.state === "failed") {
    return (
      <div className="px-4 py-10 text-center text-sm text-muted-foreground">
        <p>没有全部版本都失败的作品。</p>
        <p className="mt-1 text-xs leading-relaxed">
          {failedVersions > 0
            ? `上面「需要人工」里有 ${failedVersions} 条失败版本：它们所属的作品还有别的版本已入库或在跑，所以任务状态不算失败。`
            : "失败版本都挂在上面「需要人工」里；这里只算整个任务都失败的作品。"}
        </p>
        <Button
          variant="outline"
          size="sm"
          className="mt-3"
          onClick={() => onQuery({ page: 1, q: "", state: "" })}
        >
          清除筛选
        </Button>
      </div>
    );
  }
  // 筛不到 ≠ 库是空的：最后一页的空页或搜索无结果时，不该拿新装引导糊弄人。
  if (!query.q && !query.state) {
    return (
      <div className="px-4 py-10 text-center text-sm text-muted-foreground">
        <p>还没有任务。</p>
        <p className="mt-1 text-xs">
          在 JavDB 的想看清单里加一部有磁力的片，下一轮轮询就会出现在这里。
        </p>
      </div>
    );
  }
  return (
    <div className="px-4 py-10 text-center text-sm text-muted-foreground">
      <p>没有匹配的任务。</p>
      <p className="mt-1 text-xs">换个番号，或把状态筛选放回「全部」。</p>
      <Button
        variant="outline"
        size="sm"
        className="mt-3"
        onClick={() => onQuery({ page: 1, q: "", state: "" })}
      >
        清除筛选
      </Button>
    </div>
  );
}
/** 四个状态名的唯一真相：徽章、时间线、图例都从这里取文案与色调。 */
const TASK_STATE_LOOK: Record<TaskState, { label: string; className: string }> = {
  downloading: {
    label: "下载中",
    className: "border-status-info-border bg-status-info-surface text-status-info-text",
  },
  processing: {
    label: "加工中",
    className: "border-status-progress-border bg-status-progress-surface text-status-progress-text",
  },
  finished: {
    label: "已入库",
    className: "border-status-done-border bg-status-done-surface text-status-done-text",
  },
  failed: {
    label: "需人工",
    className: "border-status-danger-border bg-status-danger-surface text-status-danger-text",
  },
};

export function isTaskState(value: string | undefined): value is TaskState {
  return value === "downloading" || value === "processing" || value === "finished" || value === "failed";
}

function StateBadge({ state }: { state: TaskState | "" }) {
  if (!isTaskState(state)) {
    return <Badge variant="outline">未知</Badge>;
  }
  const badge = TASK_STATE_LOOK[state];
  return (
    <Badge variant="outline" className={badge.className}>
      {badge.label}
    </Badge>
  );
}

/**
 * VersionChip 是版本的小 chip：带标签，失败的那条自己说出来。
 *
 * 状态不能只活在前 1px 边框色上：那对触屏与读屏都等于不存在，色弱的人又只能
 * 靠红绿区分。所以失败写「失败」两个字，其余状态交给行上的任务徽章与抽屉。
 */
function VersionChip({ version }: { version: TaskVersion }) {
  const name = version.label || sourceShortLabel(version.source);
  const state = versionState(version.status);
  return (
    <Badge
      variant="outline"
      title={`${version.label || sourceLabel(version.source)} · ${state.label} · ${version.infohash}`}
      aria-label={`${version.label || sourceLabel(version.source)}，${state.label}`}
      className={cn("h-5 max-w-28 font-normal", state.className)}
    >
      <span className="truncate">{name}</span>
      <span className="shrink-0">{state.label}</span>
    </Badge>
  );
}

/**
 * versionState 是版本胶囊与抽屉里的那一颗：状态词 + 三件套 token。
 *
 * 只换边框色的话，下载中与加工中长得一模一样；而版本级不说「需人工」——
 * 任务才需人工，这条版本只是失败了。
 */
function versionState(status: TaskState): { label: string; className: string } {
  const look = TASK_STATE_LOOK[isTaskState(status) ? status : "downloading"];
  return { label: status === "failed" ? "失败" : look.label, className: look.className };
}

/** 胶囊里用来源的短名：112px 装不下「JavDB 磁力 + 已入库」，全名在 title 里。 */
function sourceShortLabel(source: TaskVersion["source"]): string {
  return source === "mteam_torrent" ? "M-Team" : "JavDB";
}

/**
 * TaskDrawer 是任务详情抽屉（规格 §8.2）：版本列表（infohash/来源/标签/失败原因）
 * + 事件时间线 + 重试/强制重刮。只看刮削字段以外的状态面（v1 不做 provenance）。
 */
function TaskDrawer({
  movieID,
  onClose,
  onRefresh,
}: {
  movieID: string;
  onClose: () => void;
  onRefresh: () => Promise<void>;
}) {
  const [detail, setDetail] = React.useState<TaskDetail | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  // 强制重刮的确认条住在这里而不是在按钮组里：遮罩要知道「确认条正开着」，
  // 否则一次误触就把刚写清后果的那句话关掉，人还得重新走一遍。
  const [confirming, setConfirming] = React.useState(false);
  const closeRef = React.useRef<HTMLButtonElement>(null);
  const dialogRef = React.useRef<HTMLElement>(null);
  const now = useNow();

  // 点完重试要重读一次抽屉：版本列表与时间线是重试的产物，得当场看得见。
  const [reloadKey, setReloadKey] = React.useState(0);
  const reload = React.useCallback(async () => {
    setReloadKey((key) => key + 1);
  }, []);

  React.useEffect(() => {
    let cancelled = false;
    api.task(movieID).then(
      (loaded) => {
        if (!cancelled) {
          setDetail(loaded);
          setError(null);
        }
      },
      (cause: unknown) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : String(cause));
      },
    );
    return () => {
      cancelled = true;
    };
  }, [movieID, reloadKey]);

  // 模态：开时焦点进抽屉，Tab 在内部循环，Esc 关闭；焦点归还在 Panel 那边做。
  //
  // 这个 effect 只在挂载时跑一次：onClose 是父组件每次渲染新建的箭头函数，写进
  // 依赖就会每个 render 重跑一遍（StrictMode 下更会跑两次），把状态搅乱。
  const onCloseRef = React.useRef(onClose);
  React.useEffect(() => {
    onCloseRef.current = onClose;
  }, [onClose]);

  React.useEffect(() => {
    const close = () => onCloseRef.current();
    closeRef.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        close();
        return;
      }
      if (event.key !== "Tab" || !dialogRef.current) return;
      const items = Array.from(
        dialogRef.current.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ),
      ).filter((element) => element.offsetParent !== null);
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
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const task = detail?.task;
  const finishedCount = task?.versions.filter((version) => version.status === "finished").length ?? 0;

  // 抽屉 portal 到 body：只有这样外壳才能整块 inert（侧栏、顶栏一起），
  // 而抽屉自己仍可点、可 Tab。
  if (typeof document === "undefined") return null;

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex justify-end bg-foreground/20"
      role="presentation"
      onClick={confirming ? undefined : onClose}
    >
      <aside
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="task-drawer-title"
        onClick={(event) => event.stopPropagation()}
        className="flex h-full w-full max-w-lg flex-col overflow-hidden border-l border-border bg-background shadow-2xl"
      >
        <header className="flex items-start justify-between gap-3 border-b px-4 py-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <h2 id="task-drawer-title" className="font-heading text-lg font-semibold tabular-nums">
                {task?.number ?? movieID}
              </h2>
              {task ? <StateBadge state={task.state} /> : null}
            </div>
            <p className="mt-0.5 truncate font-mono text-xs text-muted-foreground">
              {task?.movie_id ?? movieID}
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {task ? (
              <span
                className="hidden text-xs tabular-nums text-muted-foreground sm:inline"
                title={formatAbsolute(task.updated_at)}
              >
                更新 {formatWhen(task.updated_at, now)}
              </span>
            ) : null}
            <button
              ref={closeRef}
              type="button"
              onClick={onClose}
              aria-label="关闭抽屉"
              className="grid size-11 shrink-0 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring sm:size-8"
            >
              <X className="size-4" aria-hidden />
            </button>
          </div>
        </header>

        <div className="flex-1 space-y-6 overflow-y-auto px-4 py-4">
          {error ? <Notice tone="bad">读不到任务详情：{error}</Notice> : null}
          {!detail && !error ? (
            <div role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden />
              正在读取…
            </div>
          ) : null}

          {task ? (
            <section aria-label="版本列表" className="space-y-2">
              <h3 className="dense font-medium">版本（{task.versions.length}）</h3>
              <div className="divide-y">
                {task.versions.map((version) => (
                  <VersionCard key={version.infohash} version={version} />
                ))}
              </div>
            </section>
          ) : null}

          {detail ? (
            <section aria-label="事件时间线" className="space-y-2">
              <h3 className="dense font-medium">最近事件</h3>
              {detail.events.length === 0 ? (
                <p className="text-xs text-muted-foreground">还没有事件。</p>
              ) : (
                <ol className="space-y-3">
                  {detail.events.map((event) => (
                    <EventRow key={event.id} event={event} now={now} />
                  ))}
                </ol>
              )}
            </section>
          ) : null}
        </div>

        <footer className="space-y-2 border-t px-4 py-3">
          <DrawerActions
            movieID={movieID}
            versionCount={task?.versions.length ?? 0}
            finishedCount={finishedCount}
            confirming={confirming}
            onConfirming={setConfirming}
            onRetried={reload}
            onRefresh={onRefresh}
          />
        </footer>
      </aside>
    </div>,
    document.body,
  );
}

/**
 * DrawerActions 是抽屉底部的两个按钮（规格 §8.3 文案：重试 / 强制重刮）。
 *
 * 强制重刮会把已入库的版本拉回加工开头并覆盖落盘，所以它只在真有已入库版本时
 * 存在，且先出一行确认——把后果写在按钮旁边，而不是靠一个 disabled 去躲。
 */
function DrawerActions({
  movieID,
  versionCount,
  finishedCount,
  confirming,
  onConfirming: setConfirming,
  onRetried,
  onRefresh,
}: {
  movieID: string;
  versionCount: number;
  finishedCount: number;
  confirming: boolean;
  onConfirming: (confirming: boolean) => void;
  onRetried: () => Promise<void>;
  onRefresh: () => Promise<void>;
}) {
  const taskRetry = useTaskRetry(onRefresh);
  const notice = taskRetry.notices[movieID];
  const busy = taskRetry.busy.size > 0;
  const confirmRef = React.useRef<HTMLButtonElement>(null);
  const forceRef = React.useRef<HTMLButtonElement>(null);
  // 确认条是「刚出现的、需要人读一句再决定」的东西：不接焦点、不进活区，键盘与读屏
  // 用户就不知道它开了。取消后把焦点还给「强制重刮」，不把人扔回 body。
  const wantForceFocus = React.useRef(false);
  React.useEffect(() => {
    if (confirming) confirmRef.current?.focus();
    else if (wantForceFocus.current) {
      wantForceFocus.current = false;
      forceRef.current?.focus();
    }
  }, [confirming]);

  const retry = async (force: boolean) => {
    setConfirming(false);
    await taskRetry.retry(movieID, force);
    await onRetried();
  };

  return (
    <>
      {taskRetry.error ? (
        <Notice tone="bad" onClose={taskRetry.clearError}>
          {taskRetry.error}
        </Notice>
      ) : null}
      {notice ? (
        <Notice
          tone={notice.tone}
          onClose={() => {
            taskRetry.clearNotice(movieID);
          }}
        >
          {notice.text}
        </Notice>
      ) : null}

      {confirming ? (
        <div
          role="group"
          aria-live="polite"
          aria-label="确认强制重刮"
          className="space-y-2 rounded-lg border border-status-progress-border bg-status-progress-surface px-3 py-2"
        >
          <p className="text-xs leading-relaxed text-status-progress-text">
            确认把 {finishedCount} 条已入库版本拉回加工开头？重刮会重新取数并重写落盘，原来的取数快照留在任务事件里。
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              ref={confirmRef}
              className="h-11 px-3 dense md:h-8 md:px-2.5 md:text-sm"
              disabled={busy}
              onClick={() => void retry(true)}
            >
              <RotateCcw
                className={cn("size-3.5", taskRetry.busy.has(taskRetryKey(movieID, true)) && "animate-spin")}
                aria-hidden
              />
              确认强制重刮
            </Button>
            <Button
              variant="ghost"
              className="h-11 px-3 dense md:h-8 md:px-2.5 md:text-sm"
              onClick={() => {
                wantForceFocus.current = true;
                setConfirming(false);
              }}
            >
              取消
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <Button
            className="h-11 px-3 dense md:h-8 md:px-2.5 md:text-sm"
            disabled={busy}
            title="重试＝回加工开头，或按存的交付材料重新加种；已入库的版本只有「强制重刮」才会重跑。"
            onClick={() => void retry(false)}
          >
            <RotateCcw
              className={cn("size-3.5", taskRetry.busy.has(taskRetryKey(movieID, false)) && "animate-spin")}
              aria-hidden
            />
            {versionCount > 1 ? `重试 ${versionCount} 条版本` : "重试"}
          </Button>
          {finishedCount > 0 ? (
            <Button
              ref={forceRef}
              variant="outline"
              className="h-11 px-3 dense md:h-8 md:px-2.5 md:text-sm"
              disabled={busy}
              onClick={() => setConfirming(true)}
            >
              强制重刮
            </Button>
          ) : null}
        </div>
      )}
    </>
  );
}

function VersionCard({ version }: { version: TaskVersion }) {
  // 抽屉里要回答的第一个问题是「这两条哪条是哪条」，所以判别词（中文/破解/来源）打头，
  // hash 退成 8 位前缀、全值进 title。
  return (
    <div className="space-y-1.5 py-3 first:pt-0 last:pb-0">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            {/* 没有判别词就不占位，也不写「无标签」：来源就在旁边，重复一遍只是噪声。 */}
            {version.label ? <span className="dense font-medium">{version.label}</span> : null}
            <span className="text-xs text-muted-foreground">{sourceLabel(version.source)}</span>
          </div>
          <span className="font-mono text-xs text-muted-foreground" title={version.infohash}>
            {shortHash(version.infohash)}
          </span>
        </div>
        <Badge variant="outline" className={cn("h-5 shrink-0", versionState(version.status).className)}>
          {versionState(version.status).label}
        </Badge>
      </div>
      <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
        {version.attempts > 1 ? <span>尝试 {version.attempts} 次</span> : null}
        {version.add_pending ? (
          <Badge variant="outline" className="h-5 border-status-progress-border font-normal text-status-progress-text">
            待加种
          </Badge>
        ) : null}
        {version.list_removal_pending ? (
          <Badge variant="outline" className="h-5 font-normal text-muted-foreground">
            待删清单项
          </Badge>
        ) : null}
        {version.force_rescrape ? (
          <Badge variant="outline" className="h-5 border-status-progress-border font-normal text-status-progress-text">
            待强制重刮
          </Badge>
        ) : null}
      </div>
      {version.error_reason ? (
        <p className="text-xs text-status-danger-text">
          {failureLine(version.error_phase, version.error_reason)}
        </p>
      ) : null}
    </div>
  );
}

function EventRow({ event, now }: { event: TaskEvent; now: number }) {
  const kind = eventKindLabel(event.kind);
  // 事件名已经在上一行报过，原因又用同一个词开头就是口吃（`人工重试` /
  // `人工重试：回到加工开头`）。已带上前缀的只留后半句。
  const reason = event.reason.startsWith(`${kind}：`)
    ? event.reason.slice(kind.length + 1)
    : event.reason;
  return (
    <li className="relative pl-4">
      <span className="absolute top-1.5 left-0 size-1.5 rounded-full bg-border" aria-hidden />
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-xs font-medium">{kind}</span>
        <time
          className="shrink-0 text-xs tabular-nums text-muted-foreground"
          dateTime={event.at}
          title={formatAbsolute(event.at)}
        >
          {formatWhen(event.at, now)}
        </time>
      </div>
      <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">{reason}</p>
      {event.from_state || event.to_state ? (
        <p className="mt-0.5 text-xs text-muted-foreground">
          {stateLabel(event.from_state)} → {stateLabel(event.to_state)}
        </p>
      ) : null}
    </li>
  );
}

function stateLabel(state: string | undefined): string {
  return isTaskState(state) ? TASK_STATE_LOOK[state].label : "—";
}

function sourceLabel(source: TaskVersion["source"]): string {
  return source === "mteam_torrent" ? "M-Team 种子" : "JavDB 磁力";
}

function phaseLabel(phase: TaskVersion["error_phase"] | AttentionItem["phase"]): string {
  switch (phase) {
    case "scrape":
      return "刮削";
    case "organize":
      return "整理";
    default:
      return "下载";
  }
}

/**
 * failureLine 拼「失败于哪一步：原话」。
 *
 * 上游原话本身常常已经以阶段词开头（`刮削取数失败：DMM 接口 503…`），直接前缀会变成
 * `失败于刮削：刮削取数失败：…`——同一个词说了两遍，而那正是最需要一眼读完的一句。
 */
function failureLine(
  phase: TaskVersion["error_phase"] | AttentionItem["phase"],
  reason: string,
): string {
  const label = phaseLabel(phase);
  // 原话自己就开了头（`刮削取数失败：…`）就不再加前缀，否则同一个词会说两遍。
  return reason.startsWith(label) ? reason : `失败于${label}：${reason}`;
}

function eventKindLabel(kind: TaskEvent["kind"]): string {
  switch (kind) {
    case "state_change":
      return "状态变化";
    case "side_effect":
      return "旁路副作用";
    case "alert":
      return "告警";
    case "manual_retry":
      return "人工重试";
    default:
      return kind;
  }
}

/**
 * useNow 是一分钟一跳的「现在」。
 *
 * 相对时间（3 分钟前）不自己走的话，一屏「3 分钟前」会挂到用户关掉标签页为止，
 * 那比写绝对时间更骗人。
 */
function useNow(interval = 60_000): number {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), interval);
    return () => window.clearInterval(timer);
  }, [interval]);
  return now;
}

export { useNow };

type LoopLine = { label: string; text: string; failing: boolean };

/**
 * loopLines 是三条常驻循环的那两行字（侧栏与手机统计行共用）。
 *
 * 「机器还醒着吗、下一轮什么时候」（清单轮询），与「对账还在跑吗」（对账）是值班台
 * 第二个与第三个问题，它们以前只在桌面侧栏的底部与手机文档的末尾各出现一次。
 * 间隔读不到就不编（`intervals.*` 为 null 时不写）。
 */
function loopLines(
  overview: Overview | null,
  intervals: { poll: string | null; reconcile: string | null; sidefx: string | null },
  now: number,
): LoopLine[] {
  const rows: { key: "poll" | "reconcile" | "sidefx"; label: string }[] = [
    { key: "poll", label: "清单轮询" },
    { key: "reconcile", label: "对账" },
    { key: "sidefx", label: "副作用" },
  ];
  return rows.map(({ key, label }) => {
    const health = overview?.loops?.[key];
    // 读不到概览时不说「还没跑过」：那是一个我们并不知道的事实。
    if (!overview) return { label, text: "还没读到过", failing: false };
    const failing = health?.last_run_at ? health.last_ok === false : false;
    return {
      label,
      text: [
        intervals[key],
        health?.last_run_at ? formatWhen(health.last_run_at, now) : "还没跑过",
        failing ? "失败" : "",
      ]
        .filter(Boolean)
        .join(" · "),
      failing,
    };
  });
}

export { loopLines };

