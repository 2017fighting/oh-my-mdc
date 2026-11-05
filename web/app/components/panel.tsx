import * as React from "react";
import { createPortal } from "react-dom";
import { Link } from "react-router";
import { AlertTriangle, ChevronRight, Loader2, RefreshCw, RotateCcw, Search, X } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { Notice, TextInput } from "~/components/form";
import { CONFUSABLES, STATUS_NAMES, STATUS_NOTES, STATUS_ORDER } from "~/lib/copy";
import { groupByNumber, groupStuck, oldestCreatedAt, attentionRowCount, type FailureGroup, type StuckGroup } from "~/lib/attention";
import {
  formatAbsolute,
  formatAge,
  formatClock,
  formatElapsed,
  formatFuture,
  formatWhen,
} from "~/lib/time";
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
  type SideEffectKind,
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

/**
 * 行内动作的唯一尺寸：md 以下 44px 热区 + 13px 字，md 起 28px + 12px。
 *
 * 以前这串类名在本文件与 routes/home.tsx 里各抄了十几遍，「44px 是拇指热区」这条规则
 * 只活在散文里，于是最先漂掉的是空状态里的两颗「清除筛选」（28px）。
 * 收成常量之后，改规则就是改这一行，漏抄就不可能再发生。
 */
export const ROW_ACTION_SIZE = "h-11 px-3 dense md:h-7 md:px-2.5 md:text-xs";

/** 面板与错误块里的主按钮：比行内动作高 4px（md 起 32px），字号同族。 */
export const PANEL_ACTION_SIZE = "h-11 px-3 dense md:h-8 md:px-2.5 md:text-sm";

/**
 * 外壳的 inert 是共享的：抽屉与帮助浮层都可能要求它，谁也不能替另一半撤掉。
 *
 * 以前帮助浮层的清理靠 `document.querySelector('aside[role="dialog"]')` 猜
 * 「抽屉还开着吗」：抽屉恰好在这时关掉，外壳的 inert 就被提前撤掉，底下又能点。
 * 改用计数之后，两个弹层各拿一只把手，最后一个放手才真的放手。
 */
let shellInertHolds = 0;

function holdShellInert(): () => void {
  if (shellInertHolds === 0) document.getElementById("app-shell")?.setAttribute("inert", "");
  shellInertHolds += 1;
  let released = false;
  return () => {
    if (released) return;
    released = true;
    shellInertHolds = Math.max(0, shellInertHolds - 1);
    if (shellInertHolds === 0) document.getElementById("app-shell")?.removeAttribute("inert");
  };
}

/** 弹层里可 Tab 到的元素，按文档顺序；隐藏的不算。 */
function focusablesIn(root: HTMLElement): HTMLElement[] {
  return Array.from(
    root.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((element) => element.offsetParent !== null);
}

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
  autoRefreshMs,
  helpOpen,
  onHelpOpen,
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
  /** 自动复读的间隔（毫秒）；null = 读不到轮询间隔，宁可不自动读、也不编一个默认值。 */
  autoRefreshMs: number | null;
  /** 帮助浮层开着没有。状态住在 routes/home.tsx：侧栏那颗入口也在那边。 */
  helpOpen: boolean;
  onHelpOpen: (open: boolean) => void;
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
    // 只在抽屉第一次打开时记住触发它的那一行：抽屉里按 j/k 换任务时，焦点已经在
    // 抽屉内部，把它记成「来处」会让 Esc 把人送到一个不存在的行上，最后掉到 #main。
    if (previousFocus.current === null) {
      previousFocus.current =
        document.activeElement instanceof HTMLElement ? document.activeElement : null;
    }
    setSelected(movieID);
  }, []);

  // 抽屉是模态：整个外壳 inert，页面不跟着滚；关掉时先撤 inert 再还焦点，
  // 顺序反了的话 focus() 会被 inert 子树吞掉，人就被扔回页首。
  // 抽屉 portal 到 body，才能既 inert 外壳、又让自己可点。
  React.useEffect(() => {
    if (!selected) return;
    const releaseShell = holdShellInert();
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      releaseShell();
      document.body.style.overflow = previousOverflow;
      const target = previousFocus.current;
      previousFocus.current = null;
      if (target?.isConnected) target.focus();
      else document.getElementById("main")?.focus();
    };
  }, [selected]);

  // ---- 快捷键：Alex 主循环的加速器（brief：/、r、j/k、?；输入框内静默；桌面 only）----
  // 它们是焦点的别名，不是第二套导航：r 只触发「焦点所在行」的动作，j/k 只在
  // 抽屉内走当前任务表顺序；触屏设备（无精确指针）不响应，手机零提示。
  const helpCloseRef = React.useRef<HTMLButtonElement>(null);
  const helpDialogRef = React.useRef<HTMLElement>(null);
  const helpReturnRef = React.useRef<HTMLElement | null>(null);
  React.useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target;
      if (
        target instanceof HTMLInputElement ||
        target instanceof HTMLTextAreaElement ||
        (target instanceof HTMLElement && target.isContentEditable)
      ) {
        return;
      }
      if (!window.matchMedia("(hover: hover) and (pointer: fine)").matches) return;

      const isHelp = event.key === "?" || (event.code === "Slash" && event.shiftKey);
      if (isHelp) {
        event.preventDefault();
        if (!helpOpen) {
          helpReturnRef.current =
            document.activeElement instanceof HTMLElement ? document.activeElement : null;
          onHelpOpen(true);
        } else {
          onHelpOpen(false);
        }
        return;
      }
      // 帮助浮层开着时其余键不响应（Esc 由浮层自己在 capture 阶段接住）。
      if (helpOpen) return;

      // Shift+R = 重新读取面板。刷屏、盯着一轮对账等结果是值班时最常做的动作，
      // 而以前它只有右上角那颗按钮一条路，键盘和触屏（抽屉、手机）都够不到。
      if (event.key === "R" && event.shiftKey) {
        event.preventDefault();
        void onRefresh();
        return;
      }

      const drawerOpen = selected !== null;
      if (event.key === "/" && !event.shiftKey) {
        // 抽屉开着时搜索在外壳 inert 里，聚焦不到。
        if (drawerOpen) return;
        event.preventDefault();
        document.querySelector<HTMLInputElement>('input[type="search"]')?.focus();
        return;
      }
      if (event.key === "j" || event.key === "k") {
        if (!drawerOpen) return;
        event.preventDefault();
        const index = tasks.findIndex((task) => task.movie_id === selected);
        const next = tasks[index + (event.key === "j" ? 1 : -1)];
        if (next) openTask(next.movie_id);
        return;
      }
      if (event.key === "r") {
        event.preventDefault();
        // r 是焦点的别名：抽屉里=抽屉那颗；行内=焦点所在组的那颗；设有上下文
        // 时先聚焦第一颗可按的（与既有交接同一选择器），不盲按。
        if (drawerOpen) {
          document
            .querySelector<HTMLElement>("[data-drawer-retry]:not([disabled])")
            ?.click();
          return;
        }
        const active = document.activeElement;
        const groupEl =
          active instanceof HTMLElement ? active.closest<HTMLElement>("[data-group]") : null;
        const inGroup = groupEl?.querySelector<HTMLElement>("[data-retry]:not([disabled])");
        if (inGroup) {
          inGroup.click();
          return;
        }
        document
          .querySelector<HTMLElement>("[data-retry]:not([disabled])")
          ?.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selected, tasks, openTask, helpOpen, onHelpOpen, onRefresh]);

  // ---- 自动复读：值班台不能静止 ----
  //
  // 面板以前只在挂载、翻页、改筛选与按「刷新」时读一次，于是「看着流水线」这半个职责
  // 其实是靠人肉轮询完成的：一屏「3 分钟前」能挂到标签页关掉为止。现在只要页签可见、
  // 没有抽屉或帮助浮层开着，就按清单轮询的间隔静默重读一次，`上次读取` 因此一直是真话。
  //
  // 读失败不在这里说话：错误条由 home.tsx 的 Notice 承担，静默重读的职责只是别撒谎。
  // 打开抽屉时暂停：抽屉读的是当前这页的任务数据，行在背后被换掉比看到旧数据更坑。
  React.useEffect(() => {
    if (!autoRefreshMs) return;
    if (selected !== null || helpOpen) return;
    let inFlight = false;
    const read = () => {
      if (inFlight || document.visibilityState !== "visible") return;
      inFlight = true;
      void onRefresh().finally(() => {
        inFlight = false;
      });
    };
    const timer = window.setInterval(read, autoRefreshMs);
    // 从别的标签页回来时不必等下一格：那一刻正是人想知道「现在怎么样」的时候。
    const onVisible = () => {
      if (document.visibilityState === "visible") read();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [autoRefreshMs, selected, helpOpen, onRefresh]);

  // 帮助浮层的模态：与抽屉同一套规矩（inert 外壳 + Tab 在内部循环 + 背景不滚 + Esc + 焦点归还），
  // 抽屉开着时连抽屉一起 inert；Esc 在 capture 阶段接住并 stopPropagation，不会连抽屉一起关。
  React.useEffect(() => {
    if (!helpOpen) return;
    const releaseShell = holdShellInert();
    const drawer = document.querySelector<HTMLElement>("aside[role=\"dialog\"]");
    drawer?.setAttribute("inert", "");
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    helpCloseRef.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        onHelpOpen(false);
        return;
      }
      // 模态就得把 Tab 也管住：只抢 Esc 的弹层，键盘人一下 Tab 就回到了背后的页面上。
      if (event.key !== "Tab" || !helpDialogRef.current) return;
      const items = focusablesIn(helpDialogRef.current);
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
      drawer?.removeAttribute("inert");
      releaseShell();
      const back = helpReturnRef.current;
      helpReturnRef.current = null;
      if (back?.isConnected) back.focus();
    };
  }, [helpOpen, onHelpOpen]);

  const counts = overview?.counts;
  // 统计行只放「还在动」的两个数。已入库是永远不需要动作的那个，挪到右端与新鲜度
  // 同处：它是背景，不是待办。
  //
  // 任务级的「全部版本都失败」数两个地方都不上：`counts.failed` 只为筛选服务，
  // 想看有几部就点筛选——它不会要人立刻动手，不值得占首屏一个数。
  const stats = [
    { label: "下载中", value: counts?.downloading ?? 0 },
    { label: "加工中", value: counts?.processing ?? 0 },
  ];

  // 「回到需要人工」的落点：那块卡自己带焦点与名字（role=region + tabIndex=-1），
  // 光是滚过去对读屏用户等于没发生任何事。
  const attentionRef = React.useRef<HTMLDivElement>(null);
  const goToAttention = React.useCallback(() => {
    const card = attentionRef.current;
    if (!card) return;
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    card.scrollIntoView({ block: "start", behavior: reduce ? "auto" : "smooth" });
    card.focus({ preventScroll: true });
  }, []);

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
              一张分页表格之后再回答。失败的那条排最前：它才是「要不要起床」的答案，
              不能被 slice 恒定掉（副作用恰是三条里唯一会带失败进面板的）。
              `md:hidden` 与侧栏的 `md:flex` 同一道门：以前这里写的是 `lg:hidden`，
              于是 768–1023px 同一屏并排坐着两遍心跳。 */}
          {[...loopLines(overview, intervals, now)]
            .sort((a, b) => Number(b.failing) - Number(a.failing))
            .slice(0, 2)
            .map((line) => (
              <span key={line.label} className="flex items-baseline gap-1.5 md:hidden">
                <span>{line.label}</span>
                <span className={cn("tabular-nums", line.failing && "text-status-danger-text")}>
                  {line.text}
                </span>
              </span>
            ))}
        </section>

        {/* 新鲜度这一档不许出屏：手机上一行装不下就折成两行，而不是让「上次读取」
            与刷新按钮被挤到可见区之外（formatWhen 满一周后会换成更宽的绝对时间）。 */}
        <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1 text-xs text-muted-foreground">
          <span className="tabular-nums whitespace-nowrap">
            已入库 <span className="tabular-nums">{counts?.finished ?? 0}</span>
          </span>
          <span className="tabular-nums whitespace-nowrap">
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
            className={ROW_ACTION_SIZE}
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
        cardRef={attentionRef}
        onOpenTask={openTask}
        onRefresh={onRefresh}
      />

      <Card>
        <CardHeader>
          <CardTitle as="h2">任务</CardTitle>
          <CardDescription className="text-xs tabular-nums">
            {query.q || query.state ? `筛选后 ${total} 条` : `共 ${total} 条`}
          </CardDescription>
        </CardHeader>
        <CardContent className="px-0">
          <TaskFilters query={query} onQuery={onQuery} />
          {tasks.length === 0 ? (
            <EmptyTasks
              query={query}
              onQuery={onQuery}
              failedVersions={attention?.failed.length ?? 0}
              hasAttention={attentionRowCount(attention, sideEffects) > 0}
              onFocusAttention={goToAttention}
            />
          ) : (
            <>
              <ul className="divide-y lg:hidden">
                {tasks.map((task) => (
                  <li key={task.movie_id}>
                    <button
                      type="button"
                      onClick={() => openTask(task.movie_id)}
                      aria-label={`打开 ${task.number} 的详情，${TASK_STATE_LOOK[isTaskState(task.state) ? task.state : "downloading"].label}，版本：${versionsSpoken(task)}`}
                      className="flex w-full items-start justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/40 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring"
                    >
                      <span className="min-w-0">
                        <span className="block dense font-medium tabular-nums">
                          {task.number}
                        </span>
                        <span className="block font-mono text-xs break-all text-muted-foreground">
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
                    {/* 排序固定在「最近更新」：唯一会变的那一列，也是人判断新鲜度的依据。
                        不写 aria-sort：这一列没有排序控件，向读屏宣告一个不存在的
                        排序状态，比不说话更糟。 */}
                    <th scope="col" className="px-4 py-2 font-medium">
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
                        <div
                          className="truncate font-mono text-xs text-muted-foreground"
                          title={task.movie_id}
                        >
                          {task.movie_id}
                        </div>
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
          <CardTitle as="h2">循环健康</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-1.5 dense">
          {(["poll", "reconcile", "sidefx"] as const).map((loop) => {
            const health = overview?.loops?.[loop];
            const label = loop === "poll" ? "清单轮询" : loop === "reconcile" ? "对账" : "副作用";
            const interval = intervals[loop];
            // 四样数据一个优先级：正在跑 → 上轮 → 上次成功（连败时才可见）→ 迭代计数（title）。
            // 「正在跑」是「机器此刻醒着」的最直接证据；上轮的成败在跑着时退到第二行。
            const runningAt = health?.running_since ? Date.parse(health.running_since) : null;
            const failing = health?.last_run_at ? health.last_ok === false : false;
            const secondary = [
              runningAt && health?.last_run_at
                ? `上轮 ${formatWhen(health.last_run_at, now)}${failing ? " · 失败" : ""}`
                : "",
              failing
                ? health?.last_success_at
                  ? `上次成功 ${formatWhen(health.last_success_at, now)}`
                  : "还没成功过"
                : "",
            ].filter(Boolean);
            return (
              <div key={loop} className="flex flex-col gap-0.5">
                <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                  <span>
                    {label}
                    {interval ? (
                      <span className="ml-1.5 text-xs text-muted-foreground">{interval}</span>
                    ) : null}
                  </span>
                  <span className="flex items-baseline gap-2 text-xs text-muted-foreground">
                    <span
                      className={cn("tabular-nums", runningAt && "text-foreground")}
                      title={[
                        health?.last_run_at ? `上轮 ${formatAbsolute(health.last_run_at)}` : "",
                        health?.last_success_at
                          ? `上次成功 ${formatAbsolute(health.last_success_at)}`
                          : "",
                        health
                          ? `本进程累计 ${health.iterations.ok} 轮成功 · ${health.iterations.error} 轮失败`
                          : "",
                      ]
                        .filter(Boolean)
                        .join("\n")}
                    >
                      {runningAt
                        ? `正在跑 ${formatElapsed(runningAt, now)}`
                        : health?.last_run_at
                          ? formatWhen(health.last_run_at, now)
                          : "还没跑过"}
                    </span>
                    {!runningAt && health?.last_run_at ? (
                      <span className={health.last_ok ? "" : "text-status-danger-text"}>
                        {health.last_ok ? "成功" : "失败"}
                      </span>
                    ) : null}
                  </span>
                </div>
                {secondary.length > 0 ? (
                  <p className="text-right text-xs leading-relaxed text-muted-foreground">
                    {secondary.join(" · ")}
                  </p>
                ) : null}
              </div>
            );
          })}
          <Separator className="my-1" />
          <div className="flex flex-wrap items-center gap-x-6">
            <Button variant="link" size="sm" asChild className="h-11 self-start px-0 text-sm md:h-7">
              <Link to="/settings/app">去集成设置</Link>
            </Button>
            {/* 帮助唯一可见的入口：手机上侧栏不存在，这是那条路。 */}
            <Button
              variant="link"
              size="sm"
              className="h-11 self-start px-0 text-sm md:h-7"
              onClick={() => onHelpOpen(true)}
            >
              术语与快捷键
            </Button>
          </div>
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

      {helpOpen ? (
        <ShortcutsHelp
          onClose={() => onHelpOpen(false)}
          closeRef={helpCloseRef}
          dialogRef={helpDialogRef}
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
  cardRef,
  onOpenTask,
  onRefresh,
}: {
  attention: Attention | null;
  stuck: SideEffect[];
  now: number;
  receiptsState: ReturnType<typeof useReceipts>;
  /** 「回到需要人工」要把焦点送到这块地：光滚动对读屏等于什么都没发生。 */
  cardRef: React.RefObject<HTMLDivElement | null>;
  onOpenTask: (movieID: string) => void;
  onRefresh: () => Promise<void>;
}) {
  const failed = attention?.failed ?? [];
  const stalled = attention?.stalled ?? [];
  const failedGroups = groupByNumber(failed);
  const stalledGroups = groupByNumber(stalled);
  // 驻留副作用按病因收行：三条通知卡在同一个坏 token 上是一件事，不是三件。
  const stuckGroups = groupStuck(stuck);
  // 组的年龄：入箱时间最早的那一条。迁移前的老行没有 created_at，那就是 null（不说年龄）。
  const stuckAge = oldestCreatedAt(stuck);
  // 回执住在卡片上而不是行里：行会在按下成功之后消失，回执却是那一刻唯一的证据。
  const { receipts, push: receiptOf, dismiss: dismissReceipt, listRef: receiptsRef } = receiptsState;
  // 被上限切过的那一半只数得到 50，所以数字后面跟一个 +，不把截断说成总量。
  const cut = {
    failed: attention?.failed_truncated ?? false,
    stalled: attention?.stalled_truncated ?? false,
  };

  // 总数是**行数**：一行就是一个要动手的对象（一个番号的失败版本、一个番号的停滞
  // 下载，或一条病因下的驻留副作用）。这是「我还有几件事」唯一的答案，说明里给它的构成，
  // 不给第二个总数。
  const total = failedGroups.length + stalledGroups.length + stuckGroups.length;
  const broke = [
    failedGroups.length > 0 && `失败 ${failedGroups.length}${cut.failed ? "+" : ""} 部`,
    stalledGroups.length > 0 && `停滞 ${stalledGroups.length}${cut.stalled ? "+" : ""} 部`,
    // 这一行是标题那个数的**拆解**，所以它必须加得起来：驻留副作用按病因算行（标题也这么数），
    // 后面那个「N 条」是数据库行数——两个数都说，才不会让人拿 2 组 + 4 条去对 5 件事。
    stuck.length > 0 &&
      (stuck.length === stuckGroups.length
        ? `驻留副作用 ${stuck.length} 条`
        : `驻留副作用 ${stuckGroups.length} 组 ${stuck.length} 条`),
  ].filter(Boolean);

  // 「我在这里能不能修」才是第一问：能修的一组给重试，修不动的一组只给查看任务，
  // 并把那句「重试不会变好」收起来（它的全文带路径，手机上一行要占四行）。
  const canFixHere = (group: FailureGroup) =>
    group.items.some((item) => remedyForFailure(item).retryable);
  const fixableGroups = failedGroups.filter(canFixHere);
  // 「要去别处修」按最久没动排：这一组里没有当场能按的按钮，唯一要做的决定是
  // 「今天去弄哪一个」，而新挂上来的那部恰恰是最不紧急的一个。
  const elsewhereGroups = failedGroups
    .filter((group) => !canFixHere(group))
    .sort((a, b) => firstSeenAt(a) - firstSeenAt(b));

  // 停滞行按重试后行不会消失（它还在等下一轮对账确认），而回执已经说了「已重试」——
  // 屏幕说的（还停滞）与回执说的（已重新加种）在打架。行上要有瞬时中间态：
  // 阶段徽章换成「重新加种中」，并补一句等谁确认。中间态按当前停滞名单在渲染期
  // 过滤：行离开停滞名单的那一刻它自然退场，不需要另一次 effect 去打扫。
  const [reseeding, setReseeding] = React.useState<Record<string, number>>({});
  // 时序门：组里最新一条停滞的开始晚于这次重新加种 = 旧动作解释不了新停滞，
  // 中间态退场、徽章回「下载停滞」。停滞名单的成员资格是事件序口径
  // （task.stalledItems 的排除条件 + HasAlert 按集重臂），这里只防
  // 「行还在名单里但病因已经换了」的渲染期错觉。
  const reseededAtOf = (group: FailureGroup) => {
    const at = reseeding[group.movieID];
    if (at === undefined) return undefined;
    const latestStallAt = Math.max(...group.items.map((item) => Date.parse(item.at)));
    return at >= latestStallAt ? at : undefined;
  };

  // 批量重试的作用域 = Remedy 的聚合视图：能在这里修 + 未亮「重新加种中」的停滞组。
  // 数字与行级按钮同一契约：印几部动几部、印几条版本动几条版本。
  const batchTargets: { group: FailureGroup; kind: "failed" | "stalled" }[] = [
    ...fixableGroups.map((group) => ({ group, kind: "failed" as const })),
    ...stalledGroups
      .filter((group) => reseededAtOf(group) === undefined)
      .map((group) => ({ group, kind: "stalled" as const })),
  ];
  const batchVersions = batchTargets.reduce(
    (sum, { group, kind }) =>
      sum +
      group.items.filter((item) =>
        kind === "stalled" ? true : remedyForFailure(item).retryable,
      ).length,
    0,
  );

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

  // 批量时回执限额放宽到本次部数（≤天花板）：五部批量不能只留三条证据。
  const receiptLimitRef = React.useRef(RECEIPT_LIMIT);
  const pushReceipt = React.useCallback(
    (draft: ReceiptDraft) => receiptOf(draft, receiptLimitRef.current),
    [receiptOf],
  );
  const retryHooks = { onReceipt: pushReceipt, focusFallback: receiptsRef };
  const taskRetry = useTaskRetry(onRefresh, retryHooks);
  // 任务侧重试与副作用重发是两件事，各有自己的忙位；回执则合并成一条流：
  // 作者关心的不是「哪一个子系统的」，而是「我刚才那一按成了没有」。
  const sideEffect = useSideEffectRetry(onRefresh, retryHooks);

  // 一组驻留副作用共用一颗「重发」：逐条顺序发（同一条病因，修法也就一个），
  // 忙位挂在病因上而不是某一条 id 上——组里每条都动了才算这一组动完。
  // 中间不逐条刷新：重发只是交回队列，最后一次刷新就能看到整组的结果。
  const [stuckBusy, setStuckBusy] = React.useState<string | null>(null);
  const retryStuckGroup = async (group: StuckGroup) => {
    if (stuckBusy !== null) return;
    setStuckBusy(group.cause);
    try {
      for (const effect of group.effects) {
        await sideEffect.retry(effect.id, sideEffectTitle(effect), { quiet: true });
      }
      await onRefresh();
    } finally {
      setStuckBusy(null);
    }
  };

  // 批量重试：顺序逐部发起（作者规模 ≤10，不并发轰炸后端），一部失败不中断后续，
  // 逐部回执；「重新加种中」的行已在 batchTargets 里跳过。无确认条：重试幂等安全，
  // 爆炸半径印在按钮上（Consequence-First 只约束不可逆动作）。
  // 但幂等安全不等于不许叫停：按钮上要能看见走到了第几部，也要能在第三步之前收手——
  // 这三部是哪三部，就在 title 与 aria-description 里逐个写着。
  const [batchBusy, setBatchBusy] = React.useState(false);
  const [batchDone, setBatchDone] = React.useState(0);
  const haltBatch = React.useRef(false);
  const runBatch = async () => {
    if (batchBusy || taskRetry.busy.size > 0) return;
    haltBatch.current = false;
    setBatchDone(0);
    setBatchBusy(true);
    receiptLimitRef.current = Math.max(
      RECEIPT_LIMIT,
      Math.min(batchTargets.length, RECEIPT_CEILING),
    );
    try {
      for (const { group, kind } of batchTargets) {
        if (haltBatch.current) break;
        await taskRetry.retry(
          group.movieID,
          false,
          {
            title: group.number,
            infohashes: group.items
              .filter((item) => (kind === "stalled" ? true : remedyForFailure(item).retryable))
              .map((item) => item.infohash),
            names: versionNames(group),
          },
          { holdFocus: true },
        );
        setBatchDone((done) => done + 1);
      }
      // 批量的焦点交接只在收尾做一次：行消失 → 下一颗可按的重试；否则回执列表。
      await afterPaint();
      const next = document.querySelector<HTMLElement>("[data-retry]:not([disabled])");
      if (next) next.focus();
      else receiptsRef.current?.focus();
    } finally {
      receiptLimitRef.current = RECEIPT_LIMIT;
      setBatchBusy(false);
    }
  };

  // 没事可做、也没有刚做完的回执：这张卡整块不出现。页面标题已经说了「没有要动手的事」，
  // 再留一张空卡把同一个意思说第二遍，只是给首屏加了一段家具。
  if (total === 0 && receipts.length === 0) return null;

  return (
    <Card
      ref={cardRef}
      tabIndex={-1}
      role="region"
      aria-labelledby="attention-heading"
      className={cn("scroll-mt-6 outline-none", total > 0 && "ring-status-danger-border")}
    >
      <CardHeader className="max-sm:grid-cols-1">
        <div>
          {/* 页标题已经说了「N 件事需要人工」，卡片不把同一个词再说一遍：可见的首行
              就是它的构成（失败几部 · 停滞几部 · 驻留几条），标题只留给大纲与读屏。 */}
          <h2 id="attention-heading" className="sr-only">
            需要人工
          </h2>
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
        {/* One-Action Rule 的主操作位：批量重试印作用域，行级按钮仍是逐行动作。 */}
        {batchTargets.length > 0 && batchVersions > 0 ? (
          <CardAction className="max-sm:col-start-1 max-sm:row-start-2 max-sm:justify-self-stretch">
            <div className="flex items-center gap-2 max-sm:w-full">
              <RowAction
                variant="outline"
                disabled={batchBusy}
                data-batch-retry=""
                onClick={() => void runBatch()}
                className="max-sm:flex-1"
                /* 一行地方印不下名单：逐部发什么，就在 title 与 aria-description 里写全。 */
                title={`逐部重试：${batchTargets.map(({ group }) => group.number).join("、")}`}
                aria-description={`逐部重试：${batchTargets.map(({ group }) => group.number).join("、")}`}
              >
                <RotateCcw className={cn("size-3.5", batchBusy && "animate-spin")} aria-hidden />
                {batchBusy
                  ? `正在重试 ${Math.min(batchDone + 1, batchTargets.length)}/${batchTargets.length}`
                  : `重试 ${batchTargets.length} 部 · ${batchVersions} 条版本`}
              </RowAction>
              {batchBusy ? (
                <RowAction
                  variant="ghost"
                  onClick={() => {
                    haltBatch.current = true;
                  }}
                >
                  停下
                </RowAction>
              ) : null}
            </div>
          </CardAction>
        ) : null}
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
                  <AttentionHeading label="停滞" detail={ageNote(oldestAt(stalledGroups), now)} />
                  {/* 停滞的补法按这一轮的状态给：能修的组都已重新加种时，
                      「先去 qBittorrent 确认」是过去时，改说等什么。 */}
                  <p className="mb-2 dense leading-relaxed text-foreground">
                    {stalledGroups.every((group) => reseededAtOf(group) !== undefined)
                      ? "重新加种已经下发；qBittorrent 里确认还在下就不必动，等下一轮对账确认。"
                      : remedyForStalled().hint}
                  </p>
                  <div className="divide-y">
                    {stalledGroups.map((group) => (
                      <VersionGroup
                        key={group.movieID}
                        group={group}
                        kind="stalled"
                        hints={false}
                        reseededAt={reseededAtOf(group)}
                        now={now}
                        busy={taskRetry.busy.has(taskRetryKey(group.movieID, false))}
                        onRetry={async (infohashes) => {
                          // 重试成功≠已经回到下载中：种子重新交给了 qBittorrent，
                          // 要等下一轮对账确认。把这一步写在行上，不只在回执里。
                          const notice = await taskRetry.retry(group.movieID, false, {
                            title: group.number,
                            infohashes,
                            names: versionNames(group),
                          });
                          if (notice?.tone === "ok") {
                            setReseeding((current) => ({
                              ...current,
                              [group.movieID]: Date.now(),
                            }));
                          }
                        }}
                        onOpenTask={() => onOpenTask(group.movieID)}
                      />
                    ))}
                  </div>
                </section>
              ) : null}

              {elsewhereGroups.length > 0 ? (
                <section aria-label="要去别处修的失败版本" className="py-3 first:pt-0 last:pb-0">
                  <AttentionHeading
                    label="要去别处修"
                    detail={ageNote(oldestAt(elsewhereGroups), now)}
                  />
                  <div className="divide-y">
                    {elsewhereGroups.map((group) => renderFailedGroup(group, true))}
                  </div>
                </section>
              ) : null}

              {stuckGroups.length > 0 ? (
                <section aria-label="驻留的副作用" className="py-3 first:pt-0 last:pb-0">
                  <AttentionHeading
                    label="驻留副作用"
                    // 年龄优先：这一组里没有可当场按的按钮，「我躲了它多久」比「几条几种原因」
                    // 更该占这一行；没有年龄（迁移前的老行）才退回说组成。
                    detail={
                      stuckAge !== null
                        ? `最早 ${formatAge(stuckAge, now)}`
                        : stuck.length > stuckGroups.length
                          ? `${stuck.length} 条，${stuckGroups.length} 种原因`
                          : undefined
                    }
                  />
                  <div className="divide-y">
                    {stuckGroups.map((group) => (
                      <StuckRow
                        key={group.cause}
                        group={group}
                        now={now}
                        notices={group.effects
                          .map((effect) => sideEffect.errors[effect.id])
                          .filter((text): text is string => Boolean(text))}
                        busy={stuckBusy === group.cause}
                        onRetry={() => void retryStuckGroup(group)}
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

/** 回执留 3 条、活 10 分钟：足够看见刚才那几下，又不至于把卡片变成一块日志。
 *  批量重试时限额临时放宽（见 NeedsAttention.runBatch），但永不超天花板 8。 */
const RECEIPT_LIMIT = 3;
const RECEIPT_CEILING = 8;
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

  const push = React.useCallback((draft: ReceiptDraft, limit = RECEIPT_LIMIT) => {
    setReceipts((current) =>
      [{ ...draft, id: nextID.current++, at: Date.now() }, ...current].slice(
        0,
        Math.min(limit, RECEIPT_CEILING),
      ),
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
            {/* 两个版本以上时一行会变成日志：逐版结论各占一行，
                番号仍在第一行开头，回执是一段话不是一串分号。 */}
            {(receipt.parts ?? receipt.text.split("；")).map((part, index) => (
              <span key={index} className={index > 0 ? "block pl-2" : undefined}>
                {index === 0 ? ` · ${part}` : part}
              </span>
            ))}
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
 *
 * `detail` 只放一个东西：这一组最早挂上去是多久前。分组里没有当场的动作时，
 * 「我躲了它多久」就是人在这里唯一要判断的事实。
 */
function AttentionHeading({ label, detail }: { label: string; detail?: string }) {
  return (
    <h3 className="mb-1 flex flex-wrap items-baseline gap-x-2 text-xs font-medium text-muted-foreground">
      <span>{label}</span>
      {detail ? <span className="font-normal tabular-nums">{detail}</span> : null}
    </h3>
  );
}

/** 这一组里最早的那条失败/停滞发生在何时（毫秒）；认不出时间的行不算。 */
function firstSeenAt(group: FailureGroup): number {
  return Math.min(...group.items.map((item) => Date.parse(item.at)));
}

/** 一组里最早的那一条；没有可认的时间就给 null，不编一个「刚刚」。 */
function oldestAt(groups: FailureGroup[]): number | null {
  const stamps = groups
    .flatMap((group) => group.items.map((item) => Date.parse(item.at)))
    .filter((at) => !Number.isNaN(at));
  return stamps.length === 0 ? null : Math.min(...stamps);
}

/** 分节标题上的「最早 N 天前」；没有时间就不写。 */
function ageNote(at: number | null, now: number): string | undefined {
  return at === null ? undefined : `最早 ${formatAge(at, now)}`;
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
  reseededAt,
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
  /** 停滞组刚重试成功的时间：行不消失，行上要有「已重新加种」的瞬时中间态。 */
  reseededAt?: number;
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
    <div
      data-group={group.movieID}
      className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:flex-wrap sm:items-start sm:justify-between sm:gap-x-4"
    >
      <div className="min-w-0 flex-1 space-y-1.5">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 dense font-medium tabular-nums">
          {group.number}
          <span className="font-mono text-xs font-normal break-all text-muted-foreground">{group.movieID}</span>
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
                    {/* 停滞行重试成功后行还在等下一轮对账：徽章先换成中间态，
                        不能让「下载停滞」跟「已重新加种」的回执同屏打架。 */}
                    {kind === "failed"
                      ? `${phaseLabel(item.phase)}失败`
                      : reseededAt
                        ? "重新加种中"
                        : "下载停滞"}
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
        {reseededAt ? (
          <p className="text-xs leading-relaxed text-muted-foreground">
            已重新加种（{formatWhen(reseededAt, now)}），等下一轮对账确认后回到下载中。
          </p>
        ) : null}
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-2">
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
  // aria-expanded 必须说出它展开的是哪一块：没有 aria-controls 时，读屏只知道
  // 「收起了」，不知道展开后会多出什么。
  const hintID = React.useId();
  return (
    <div>
      <button
        type="button"
        aria-expanded={open}
        aria-controls={hintID}
        onClick={() => setOpen((current) => !current)}
        className="dense inline-flex min-h-11 items-center gap-1 rounded-sm text-muted-foreground underline underline-offset-4 hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring md:min-h-0"
      >
        重试不会变好
        <span aria-hidden>·</span>
        {open ? "收起原因" : "展开原因"}
      </button>
      {open ? (
        <p id={hintID} className="dense leading-relaxed break-words text-foreground">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

/**
 * StuckRow 是驻留副作用的一行：它缺的那一样东西在哪一节，就给哪一节的链接。
 *
 * 一行是**一条病因**，不是一条数据库行：同一个坏 token 卡住三条通知时，人要改的是
 * 那格 token 一次，不是分三次按同一颗按钮。以前三条各占一行、各给一颗「重发」，
 * 标题上的数于是只会涨不会落，那个数最后就变成了墙纸。多条的组把「哪几条」写在
 * 第二行，按钮上印会动几条。
 */
function StuckRow({
  group,
  now,
  notices,
  busy,
  onRetry,
}: {
  group: StuckGroup;
  now: number;
  /** 组内逐条重发失败的原因：一次只可能一条，但类型上给全。 */
  notices: string[];
  busy: boolean;
  onRetry: () => void;
}) {
  const first = group.effects[0];
  const many = group.effects.length > 1;
  const remedy = remedyForSideEffect(first);
  // 组内各条的尝试次数不一定一样：说最大的那个，不说平均值——这里要的是「试到哪一步了」。
  const attempts = Math.max(...group.effects.map((effect) => effect.attempts));
  const groupAge = oldestCreatedAt(group.effects);
  const titles = group.effects.map(sideEffectTitle);
  const roster = titles.slice(0, 4).join("、") + (titles.length > 4 ? ` 等 ${titles.length} 条` : "");
  return (
    <div className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:flex-wrap sm:items-start sm:justify-between sm:gap-x-4">
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 dense font-medium">
          {many ? sideEffectKindName(first.kind) : sideEffectTitle(first)}
          <Badge
            variant="outline"
            className="border-transparent bg-status-danger-surface text-status-danger-text"
          >
            驻留副作用
          </Badge>
          {many ? (
            <span className="text-xs font-normal tabular-nums text-muted-foreground">
              {group.effects.length} 条同一原因
            </span>
          ) : null}
          <span className="text-xs font-normal tabular-nums text-muted-foreground">
            {attempts > 0 ? `已试 ${attempts} 次` : "还没试过"}
            {groupAge !== null ? (
              <span title={formatAbsolute(groupAge)}> · {formatAge(groupAge, now)}</span>
            ) : null}
          </span>
        </div>
        {many ? (
          <p className="text-xs leading-relaxed break-words text-muted-foreground">{roster}</p>
        ) : null}
        <p className="dense leading-relaxed break-words text-foreground">
          {first.last_error || "自动重试已放弃，等人工重发。"}
        </p>
        {remedy.hint ? (
          <p className="text-xs leading-relaxed break-words text-muted-foreground">{remedy.hint}</p>
        ) : null}
        {notices.length > 0 ? (
          <div className="space-y-1 pt-0.5">
            {notices.map((text) => (
              <Notice key={text} tone="bad">
                {text}
              </Notice>
            ))}
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
          {many ? `重发 ${group.effects.length} 条` : "重发"}
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

/** RetryNotice 是一次任务重试的结果提示：成功用 ok，被拒/失败用 warn。
 *  parts 是逐版结论：回执按它换行，text 是同一句话的单行串（播报与抽屉用）。 */
type RetryNotice = { tone: "ok" | "warn"; text: string; parts?: string[] };

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
 * 回执（那里行会消失）；返回那条结论本身，给需要知道「按下去到底动了没」的调用方
 * （停滞行的中间态靠它决定要不要亮出来）。
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
    async (
      movieID: string,
      force: boolean,
      plan?: RetryPlan,
      opts?: { holdFocus?: boolean },
    ) => {
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
        await afterPaint();
        if (!opts?.holdFocus && focusFallback && origin) {
          if (!origin.isConnected) {
            // 行在成功之后从树上消失了：焦点跟到下一条还能按的重试，
            // 一条不剩才落到回执上。落到回执会把整页滚回卡片头，
            // 而回执本身已经在播报了。
            const next = document.querySelector<HTMLElement>("[data-retry]:not([disabled])");
            if (next) next.focus();
            else focusFallback.current?.focus();
          } else if (document.activeElement === document.body) {
            // 行没消失的重试（停滞、部分被拒）：按钮 disabled 一轮之后浏览器可能把
            // 焦点丢回 body。把它接回原按钮，否则纯键盘用户每行都要重新走一遍整页。
            origin.focus();
          }
        }
        return notice;
      } catch (cause) {
        const message = cause instanceof Error ? cause.message : String(cause);
        // 错误条补主语：成功回执有名有姓，失败时刻更该落在具体对象上。
        // 抽屉里番号已在标题，不再重复。
        setError(plan?.title ? `${plan.title} · ${message}` : message);
        return null;
      } finally {
        release();
        // 失败路径也要接回：行不消失的重试在请求失败时同样把焦点丢回 body。
        // 但忙位一放、React 要到下一帧才提交重启用——提交前 focus() 落在
        // disabled 按钮上是空操作，所以等一帧（afterPaint 带超时兑底，后台不挂）。
        await afterPaint();
        if (origin?.isConnected && document.activeElement === document.body) {
          origin.focus();
        }
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
    const parts = blocked.map((result) => labelResult(result, prefixOf(result)));
    return { tone: "warn", text: parts.join("；"), parts };
  }

  // 不再前缀「已下发：」：回执与抽屉的提示都自己带结局语气词，
  // 正文只说发生了什么（「回到加工开头」），一句话只有一个说话人。
  const parts = results.map((result) => labelResult(result, prefixOf(result)));
  return { tone: "ok", text: parts.join("；"), parts };
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
      // 不说「回到下载中」：种子刚交给 qBittorrent，要等下一轮对账确认才算数。
      // 回执说了屏幕下一秒兑现不了的话，就是教人怀疑回执是假的。
      return "重新加种，等下一轮对账确认";
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
    async (id: number, title = `副作用 ${id}`, options?: { quiet?: boolean }) => {
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
        // quiet：成组重发时由调用方在收尾统一刷一次，不为组里每一条拉一遍整面板。
        if (!options?.quiet) await onRefresh();
        // 忙位先放：等下一帧只属于焦点交接，不能把按钮的旋转一起拖住。
        setBusy(null);
        await afterPaint();
        if (focusFallback && origin) {
          if (!origin.isConnected) {
            const next = document.querySelector<HTMLElement>("[data-retry]:not([disabled])");
            if (next) next.focus();
            else focusFallback.current?.focus();
          } else if (document.activeElement === document.body) {
            origin.focus();
          }
        }
      } catch (cause) {
        setErrors((current) => ({
          ...current,
          [id]: cause instanceof Error ? cause.message : String(cause),
        }));
      } finally {
        setBusy(null);
        // 失败路径与成功路径同一套接回；同样等一帧，提交前 focus 是空操作。
        await afterPaint();
        if (origin?.isConnected && document.activeElement === document.body) {
          origin.focus();
        }
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
        <CardTitle as="h2">副作用队列</CardTitle>
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
                <div className="flex shrink-0 flex-wrap items-center gap-2">
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

/** 只报科目、不报事件：一行里合并了好几个事件时，拿第一条的事件当标题就是骗人。 */
function sideEffectKindName(kind: SideEffectKind): string {
  switch (kind) {
    case "list_removal":
      return "删清单项";
    case "jellyfin_refresh":
      return "刷新 Jellyfin";
    case "notify":
      return "发 Telegram";
    default:
      return kind;
  }
}

/**
 * 待办的动作名（带事件）。
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
      className={cn(ROW_ACTION_SIZE, className)}
      {...props}
    />
  );
}

const STATE_FILTERS: { value: TaskState | ""; label: string }[] = [
  { value: "", label: "全部" },
  // 顺序与名字都从 copy 来：chip 就是面板自己那一行状态。
  ...STATUS_ORDER.map((state) => ({ value: state, label: STATUS_NAMES[state] })),
];

/**
 * TaskFilters 是任务卡的搜索与状态筛选（规格 §8.4 的 ?state=&q=）。
 *
 * 筛选住在地址栏里：F5 不丢，还能把「只看失败」发给自己的另一个标签页。
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
    <div className="border-b px-4 pb-2.5">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
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
        {/* 手机上五个 chip 一行放不下，折成 4+1 会让「失败」孤悬第二行：
            改成横滑一行（sm 起恢复换行）。 */}
        <div
          className="flex items-center gap-2 overflow-x-auto sm:flex-wrap sm:overflow-visible"
          role="group"
          aria-label="按状态筛选"
        >
          {STATE_FILTERS.map((filter) => {
            const current = query.state === filter.value;
            return (
              <button
                key={filter.value || "all"}
                type="button"
                aria-pressed={current}
                onClick={() => onQuery({ page: 1, q: query.q, state: filter.value })}
                onFocus={(event) => {
                  // 键盘走到被裁掉的那颗时把它带进视口：Chromium 不会为
                  // 横向溢出容器自动横滚焦点项。
                  event.currentTarget.scrollIntoView({ inline: "nearest", block: "nearest" });
                }}
                // chip 说的是**任务状态**（「失败」= 全部版本都失败）；单条版本的失败属于
                // 上面那张卡，两者尺度不同。筛选条下不再常驻解释：词不再撒谎，
                // 空状态里那一次说清就够了。
                className={cn(
                  "h-11 shrink-0 rounded-lg px-3 dense transition-colors md:h-7 md:px-2.5 md:text-xs",
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
    </div>
  );
}

function EmptyTasks({
  query,
  onQuery,
  failedVersions,
  hasAttention,
  onFocusAttention,
}: {
  query: PanelQuery;
  onQuery: (next: PanelQuery) => void;
  /** 「需要人工」里的失败版本条数：用来解释「为什么筛失败是空的」。 */
  failedVersions: number;
  /** 上面那张卡是否真的在树上（没有它就没有可去的地方）。 */
  hasAttention: boolean;
  /** 把人送到「需要人工」：滚动过去并让读屏拿到那块地的名字。 */
  onFocusAttention: () => void;
}) {
  // 筛「失败」却是空的，不等于没事：一部作品有版本已入库、另一条破解版失败时，
  // 任务状态跟着已入库走、但它在「需要人工」里有一行。不说清，人会以为那张卡在撒谎。
  if (query.state === "failed") {
    return (
      <div className="px-4 py-10 text-center text-sm text-muted-foreground">
        <p>没有全部版本都失败的任务。</p>
        <p className="mt-1 text-xs leading-relaxed">
          {failedVersions > 0
            ? `上面「需要人工」里有 ${failedVersions} 条失败版本：它们所属的任务还有别的版本已入库或在跑，任务状态因此不是「失败」。要动手的是那些版本，不是任务。`
            : "「失败」是任务级状态：一部作品的版本全部失败才算。现在没有这样的任务。"}
        </p>
        <div className="mt-3 flex flex-wrap items-center justify-center gap-2">
          {hasAttention ? (
            <Button className={ROW_ACTION_SIZE} onClick={onFocusAttention}>
              回到「需要人工」
            </Button>
          ) : null}
          <Button
            variant="outline"
            className={ROW_ACTION_SIZE}
            onClick={() => onQuery({ page: 1, q: "", state: "" })}
          >
            清除筛选
          </Button>
        </div>
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
        className={cn(ROW_ACTION_SIZE, "mt-3")}
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
    label: STATUS_NAMES.downloading,
    className: "border-status-info-border bg-status-info-surface text-status-info-text",
  },
  processing: {
    label: STATUS_NAMES.processing,
    className: "border-status-progress-border bg-status-progress-surface text-status-progress-text",
  },
  finished: {
    label: STATUS_NAMES.finished,
    className: "border-status-done-border bg-status-done-surface text-status-done-text",
  },
  failed: {
    label: STATUS_NAMES.failed,
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
 * 只换边框色的话，下载中与加工中长得一模一样；而两级同名：任务的版本全失败叫
 * 「失败」，这一条版本失败也叫「失败」，尺度由位置（状态列 / 版本列）区分。
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
 * versionsSpoken 是窄屏任务行的一句话版本摘要。
 *
 * 整行是一个 `<button>`，而按钮上的 `aria-label` 会盖掉整棵子树：以前那句只报
 * 「3 个版本」，子元素里每颗胶囊自己的 aria-label 于是全部成了死代码——手机上
 * 看不到、也听不到版本级状态，而桌面表格有。现在这句话把状态带过去。
 */
function versionsSpoken(task: TaskRow): string {
  if (task.versions.length === 0) return "没有版本";
  return task.versions
    .map(
      (version) =>
        `${version.label || sourceShortLabel(version.source)} ${versionState(version.status).label}`,
    )
    .join("、");
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
          aria-label="确认强制重刮"
          className="space-y-2 rounded-lg border border-status-progress-border bg-status-progress-surface px-3 py-2"
        >
          <p className="text-xs leading-relaxed text-status-progress-text">
            确认把 {finishedCount} 条已入库版本拉回加工开头？重刮会重新取数并重写落盘，原来的取数快照留在任务事件里。
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              ref={confirmRef}
              className={PANEL_ACTION_SIZE}
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
              className={PANEL_ACTION_SIZE}
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
            className={PANEL_ACTION_SIZE}
            disabled={busy}
            data-drawer-retry=""
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
              className={PANEL_ACTION_SIZE}
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
          {event.from_state && event.to_state
            ? `${stateLabel(event.from_state)} → ${stateLabel(event.to_state)}`
            : // 只有一头的（首次落库没有 from）就只说那一头，「— → 下载中」
              // 左边那个破折号不回答任何问题。
              stateLabel(event.from_state ?? event.to_state)}
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
 * ShortcutsHelp 是 ? 的帮助浮层：先快捷键、后术语，桌面与手机都能开。
 *
 * 以前它只有一张键表，而「这个词在这块屏上是什么意思」只活在几个 hover 的 title 里，
 * 触屏等于没有。术语段收的是**面板上真会出现、真会同屏混淆**的词：四个状态名（左侧挂
 * 真徽章，颜色即教材）+ 四条易混的（任务级/版本级、需要人工是地方、驻留组、三个「重X」）。
 * 完整词表在仓内的 GLOSSARY.md，末尾留一行指过去，不在这里复制一份。
 *
 * 键表只在精确指针上列：手机没有键盘，列出来只是噪声；术语段两个尺寸都给。
 * 模式复用抽屉：portal 到 body、role=dialog、Esc 关闭、Tab 在内部循环、
 * 开着时外壳（与抽屉）inert。全站唯一不带阴影的浮层——阴影属于抽屉，这是卡片。
 */
function ShortcutsHelp({
  onClose,
  closeRef,
  dialogRef,
}: {
  onClose: () => void;
  closeRef: React.RefObject<HTMLButtonElement | null>;
  dialogRef: React.RefObject<HTMLElement | null>;
}) {
  if (typeof document === "undefined") return null;
  // 与 keydown 的门同一判据：能按键盘才列键盘。
  const precisePointer =
    typeof window !== "undefined" &&
    window.matchMedia("(hover: hover) and (pointer: fine)").matches;
  const rows: [string, string][] = [
    ["/", "聚焦搜索框"],
    ["r", "重试：焦点所在行；抽屉里是抽屉那颗"],
    ["Shift + R", "重新读取面板"],
    ["j / k", "抽屉内下一部 / 上一部任务"],
    ["?", "打开 / 关闭本帮助"],
    ["Esc", "关闭抽屉或本帮助"],
  ];
  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-foreground/20 p-4"
      role="presentation"
      onClick={onClose}
    >
      <section
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="shortcuts-title"
        onClick={(event) => event.stopPropagation()}
        className="max-h-[85dvh] w-full max-w-sm overflow-y-auto rounded-xl bg-card p-5 ring-1 ring-foreground/10"
      >
        <div className="flex items-center justify-between gap-3">
          <h2 id="shortcuts-title" className="font-heading text-base font-medium">
            快捷键与术语
          </h2>
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            aria-label="关闭帮助"
            className="grid size-8 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
          >
            <X className="size-4" aria-hidden />
          </button>
        </div>

        {precisePointer ? (
          <section aria-labelledby="help-shortcuts" className="mt-4">
            <h3 id="help-shortcuts" className="text-xs font-medium text-muted-foreground">
              快捷键
            </h3>
            <dl className="mt-2 space-y-1.5">
              {rows.map(([keys, desc]) => (
                <div key={keys} className="flex items-baseline justify-between gap-4">
                  <dt className="font-mono text-xs text-foreground">{keys}</dt>
                  <dd className="dense text-right text-muted-foreground">{desc}</dd>
                </div>
              ))}
            </dl>
            <p className="mt-2 text-xs text-muted-foreground">输入框里打字时快捷键不生效。</p>
          </section>
        ) : null}

        <section aria-labelledby="help-status" className="mt-4">
          <h3 id="help-status" className="text-xs font-medium text-muted-foreground">
            状态
          </h3>
          <dl className="mt-2 space-y-2.5">
            {STATUS_ORDER.map((state) => (
              <div key={state}>
                <dt>
                  <Badge
                    variant="outline"
                    className={cn("font-normal", TASK_STATE_LOOK[state].className)}
                  >
                    {STATUS_NAMES[state]}
                  </Badge>
                </dt>
                <dd className="mt-1 text-xs leading-relaxed text-muted-foreground">
                  {STATUS_NOTES[state]}
                </dd>
              </div>
            ))}
          </dl>
        </section>

        <section aria-labelledby="help-confusables" className="mt-4">
          <h3 id="help-confusables" className="text-xs font-medium text-muted-foreground">
            容易混的
          </h3>
          <dl className="mt-2 space-y-2.5">
            {CONFUSABLES.map((entry) => (
              <div key={entry.term}>
                <dt className="dense font-medium">{entry.term}</dt>
                <dd className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
                  {entry.note}
                </dd>
              </div>
            ))}
          </dl>
        </section>

        <p className="mt-4 text-xs text-muted-foreground">完整词表在仓库的 GLOSSARY.md。</p>
      </section>
    </div>,
    document.body,
  );
}

/**
 * useNow 是一分钟一跳的「现在」。
 *
 * 相对时间（3 分钟前）不自己走的话，一屏「3 分钟前」会挂到用户关掉标签页为止，
 * 那比写绝对时间更骗人。间隔取 60s：秒级的「正在跑 43 秒」不是这一跳能兑现的精度，
 * 那一行说的是「醒着」，不是秒表。
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
    // 正在跑是「机器此刻醒着」的最直接证据，优先于「多久以前」。
    const running = health?.running_since ? Date.parse(health.running_since) : null;
    return {
      label,
      text: [
        intervals[key],
        running
          ? `正在跑 ${formatElapsed(running, now)}`
          : health?.last_run_at
            ? formatWhen(health.last_run_at, now)
            : "还没跑过",
        failing ? "失败" : "",
      ]
        .filter(Boolean)
        .join(" · "),
      failing,
    };
  });
}

export { loopLines };

