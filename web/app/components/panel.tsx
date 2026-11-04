import * as React from "react";
import { createPortal } from "react-dom";
import { AlertTriangle, ChevronRight, Loader2, RefreshCw, RotateCcw, X } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { Notice } from "~/components/form";
import { formatAbsolute, formatClock, formatFuture, formatWhen } from "~/lib/time";
import {
  remedyForFailure,
  remedyForSideEffect,
  remedyForStalled,
  settingsLabel,
  type Remedy,
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
 * 面板（规格 §8.2）：统计卡 →「需要人工」区 → 稠密任务表 → 副作用队列 → 循环健康。
 *
 * 这里只放面板自己的东西；整站外壳（导航、门禁、视图切换）在 routes/home.tsx。
 */

/** Panel 是面板：统计卡 → 「需要人工」区 → 稠密任务表 → 副作用队列 → 循环健康（规格 §8.2）。 */
export function Panel({
  overview,
  tasks,
  attention,
  sideEffects,
  page,
  pageSize,
  loadedAt,
  intervals,
  onPage,
  onOpenWizard,
  onRefresh,
}: {
  overview: Overview | null;
  tasks: TaskRow[];
  attention: Attention | null;
  sideEffects: SideEffect[];
  page: number;
  pageSize: number;
  /** 上一次读到数据的时刻（客户端时钟）：值班台必须知道自己有多旧。 */
  loadedAt: number | null;
  /** 三条循环的间隔，读自 app_config；读不到就是 null，不编默认值。 */
  intervals: { poll: string | null; reconcile: string | null; sidefx: string | null };
  onPage: (page: number) => void;
  onOpenWizard: () => void;
  onRefresh: () => Promise<void>;
}) {
  const [selected, setSelected] = React.useState<string | null>(null);
  const [refreshing, setRefreshing] = React.useState(false);
  // 相对时间要自己走，否则一屏「3 分钟前」会挂到用户关掉标签页为止。
  const now = useNow();

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
  const stats = [
    { label: "下载中", value: counts?.downloading ?? 0, tone: "" },
    { label: "加工中", value: counts?.processing ?? 0, tone: "" },
    { label: "已入库", value: counts?.finished ?? 0, tone: "" },
    {
      label: "需人工",
      value: counts?.needs_attention ?? 0,
      tone: (counts?.needs_attention ?? 0) > 0 ? "text-status-danger-text" : "",
    },
  ];

  const refresh = async () => {
    setRefreshing(true);
    try {
      await onRefresh();
    } finally {
      setRefreshing(false);
    }
  };

  const hasMore = tasks.length >= pageSize;

  return (
    <>
      <div className="flex flex-col gap-6">
      {/* 统计走一行，把首屏第一块让给「需要人工」；新鲜度与刷新贴在这一行的右端。 */}
      <div className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-2">
        <section
          aria-label="任务统计"
          className="flex flex-wrap items-baseline gap-x-4 gap-y-1 text-[0.8125rem] text-muted-foreground"
        >
          {stats.map((stat) => (
            <span key={stat.label} className="flex items-baseline gap-1.5">
              <span>{stat.label}</span>
              <span className={cn("font-medium tabular-nums text-foreground", stat.tone)}>
                {stat.value}
              </span>
            </span>
          ))}
        </section>

        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          <span className="tabular-nums">
            {loadedAt ? (
              <>
                上次读取 <time dateTime={new Date(loadedAt).toISOString()}>{formatClock(loadedAt)}</time>
                <span className="text-muted-foreground/80">（{formatWhen(loadedAt, now)}）</span>
              </>
            ) : (
              "还没读到过"
            )}
          </span>
          <Button
            variant="outline"
            className="h-9 px-2.5 text-[0.8125rem] md:h-7 md:text-xs"
            onClick={() => void refresh()}
            disabled={refreshing}
          >
            <RefreshCw className={cn("size-3.5", refreshing && "animate-spin")} aria-hidden />
            刷新
          </Button>
        </div>
      </div>

      <NeedsAttention
        attention={attention}
        stuck={sideEffects.filter((effect) => effect.stuck)}
        now={now}
        onOpenTask={openTask}
        onOpenWizard={onOpenWizard}
        onRefresh={onRefresh}
      />

      <Card>
        <CardHeader>
          <CardTitle as="h2" title="一行一任务，按 movie_id 聚合；版本以小 chip 展示。">
            任务
          </CardTitle>
        </CardHeader>
        <CardContent className="px-0">
          {tasks.length === 0 ? (
            <EmptyTasks />
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
                        <span className="block text-[0.8125rem] font-medium tabular-nums">
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
              <table className="w-full table-fixed border-collapse text-[0.8125rem] lg:min-w-[34rem]">
                <colgroup>
                  <col className="w-[11rem]" />
                  <col className="w-[6.5rem]" />
                  <col />
                  <col className="w-[8rem]" />
                  <col className="w-8" />
                </colgroup>
                <thead>
                  <tr className="border-y bg-muted/40 text-left text-xs font-medium tracking-wide text-muted-foreground">
                    <th className="px-4 py-2 font-medium">番号</th>
                    <th className="px-4 py-2 font-medium">状态</th>
                    <th className="px-4 py-2 font-medium">版本</th>
                    <th className="px-4 py-2 font-medium">更新</th>
                    <th className="w-8 px-2 py-2" aria-label="打开抽屉" />
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
          {tasks.length > 0 ? (
            <div className="flex flex-wrap items-center justify-between gap-2 border-t px-4 py-2.5 text-xs text-muted-foreground">
              <span className="tabular-nums">
                第 {page} 页 · 每页 {pageSize} 条
              </span>
              <div className="flex items-center gap-1.5">
                <Button
                  variant="outline"
                  className="h-9 px-2.5 text-[0.8125rem] md:h-7 md:text-xs"
                  disabled={page <= 1}
                  onClick={() => onPage(page - 1)}
                >
                  上一页
                </Button>
                <Button
                  variant="outline"
                  className="h-9 px-2.5 text-[0.8125rem] md:h-7 md:text-xs"
                  disabled={!hasMore}
                  onClick={() => onPage(page + 1)}
                >
                  下一页
                </Button>
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
        <CardContent className="flex flex-col gap-1.5 text-[0.8125rem]">
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
          <Button
            variant="link"
            size="sm"
            onClick={onOpenWizard}
            className="h-9 self-start px-0 text-sm md:h-7"
          >
            去集成设置
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
 * 三半拼在一起：失败的任务（phase + reason）、停滞的下载、驻留的副作用。
 * 每一行先回答「病因是什么」，再决定给哪个动作（`lib/remedy.ts`）：
 * 宿主机问题不给重试、设置问题先指向设置、上游业务错指向上游；只在流水线
 * 自己就有救的时候，才让重试做主角。
 */
function NeedsAttention({
  attention,
  stuck,
  now,
  onOpenTask,
  onOpenWizard,
  onRefresh,
}: {
  attention: Attention | null;
  stuck: SideEffect[];
  now: number;
  onOpenTask: (movieID: string) => void;
  onOpenWizard: () => void;
  onRefresh: () => Promise<void>;
}) {
  const failed = attention?.failed ?? [];
  const stalled = attention?.stalled ?? [];
  const total = failed.length + stalled.length + stuck.length;

  const { busy, error, notices, retry, clearError } = useTaskRetry(onRefresh);
  // 任务侧重试与副作用重发是两件事，各有自己的忙位与提示（规格 §8.2 的行内动作）。
  const sideEffect = useSideEffectRetry(onRefresh);

  return (
    <Card className={cn(total > 0 && "ring-status-danger-border")}>
      <CardHeader>
        <div>
          <CardTitle as="h2" className="flex items-center gap-2">
            <AlertTriangle
              className={cn("size-4", total > 0 ? "text-status-danger-text" : "text-muted-foreground")}
              aria-hidden
            />
            需要人工
          </CardTitle>
          <CardDescription aria-live="polite">
            {total === 0
              ? "没有要动手的事：失败、停滞与驻留副作用都会出现在这里。"
              : `${total} 件要动手的事：失败重试、停滞确认、驻留副作用重发。`}
          </CardDescription>
        </div>
        <CardAction>
          <Badge
            variant="outline"
            className={cn(
              "border-transparent tabular-nums",
              total > 0
                ? "bg-status-danger-surface text-status-danger-text"
                : "bg-secondary text-secondary-foreground",
            )}
          >
            {total}
          </Badge>
        </CardAction>
      </CardHeader>
      {total > 0 ? (
        <CardContent>
          {error ? (
            <div className="mb-2">
              <Notice tone="bad" onClose={clearError}>
                {error}
              </Notice>
            </div>
          ) : null}

          <div className="divide-y">
          {failed.map((item) => {
            const remedy = remedyForFailure(item);
            return (
              <AttentionRow
                key={`failed-${item.infohash}`}
                title={item.number}
                time={item.at}
                now={now}
                badge={
                  <Badge
                    variant="outline"
                    className="border-transparent bg-status-danger-surface text-status-danger-text"
                  >
                    {phaseLabel(item.phase)}失败
                  </Badge>
                }
                detail={item.reason}
                remedy={remedy}
                notice={notices[item.movie_id]}
                actions={
                  <>
                    {remedy.retryable ? (
                      <Button
                        variant="outline"
                        className="h-9 px-2.5 text-[0.8125rem] md:h-7 md:text-xs"
                        disabled={busy.has(taskRetryKey(item.movie_id, false))}
                        onClick={() => void retry(item.movie_id, false)}
                      >
                        <RotateCcw
                          className={cn(
                            "size-3.5",
                            busy.has(taskRetryKey(item.movie_id, false)) && "animate-spin",
                          )}
                          aria-hidden
                        />
                        重试
                      </Button>
                    ) : null}
                    <Button
                      variant="ghost"
                      className="h-9 px-2.5 text-[0.8125rem] md:h-7 md:text-xs"
                      onClick={() => onOpenTask(item.movie_id)}
                    >
                      查看任务
                    </Button>
                  </>
                }
              />
            );
          })}

          {stalled.map((item) => (
            <AttentionRow
              key={`stalled-${item.infohash}`}
              title={item.number}
              time={item.at}
              now={now}
              badge={<Badge variant="outline">下载停滞</Badge>}
              detail={item.reason}
              remedy={remedyForStalled()}
              notice={notices[item.movie_id]}
              actions={
                <>
                  <Button
                    variant="outline"
                    className="h-9 px-2.5 text-[0.8125rem] md:h-7 md:text-xs"
                    disabled={busy.has(taskRetryKey(item.movie_id, false))}
                    onClick={() => void retry(item.movie_id, false)}
                  >
                    <RotateCcw
                      className={cn(
                        "size-3.5",
                        busy.has(taskRetryKey(item.movie_id, false)) && "animate-spin",
                      )}
                      aria-hidden
                    />
                    重试
                  </Button>
                  <Button
                    variant="ghost"
                    className="h-9 px-2.5 text-[0.8125rem] md:h-7 md:text-xs"
                    onClick={() => onOpenTask(item.movie_id)}
                  >
                    查看任务
                  </Button>
                </>
              }
            />
          ))}

          {stuck.map((effect) => {
            const remedy = remedyForSideEffect(effect);
            return (
              <AttentionRow
                key={`stuck-${effect.id}`}
                title={sideEffectTitle(effect)}
                meta={effect.attempts > 0 ? `已试 ${effect.attempts} 次` : undefined}
                now={now}
                badge={
                  <Badge
                    variant="outline"
                    className="border-transparent bg-status-danger-surface text-status-danger-text"
                  >
                    副作用驻留
                  </Badge>
                }
                detail={effect.last_error || "自动重试已放弃，等人工重发。"}
                remedy={remedy}
                notice={sideEffect.errors[effect.id]}
                actions={
                  <>
                    {remedy.settings ? (
                      <Button
                        variant="outline"
                        className="h-9 px-2.5 text-[0.8125rem] md:h-7 md:text-xs"
                        onClick={onOpenWizard}
                      >
                        去 {settingsLabel(remedy.settings)} 设置
                      </Button>
                    ) : null}
                    <Button
                      variant={remedy.settings ? "ghost" : "outline"}
                      className="h-9 px-2.5 text-[0.8125rem] md:h-7 md:text-xs"
                      disabled={sideEffect.busy === effect.id}
                      onClick={() => void sideEffect.retry(effect.id)}
                    >
                      <RotateCcw
                        className={cn("size-3.5", sideEffect.busy === effect.id && "animate-spin")}
                        aria-hidden
                      />
                      重发
                    </Button>
                  </>
                }
              />
            );
          })}
          </div>
        </CardContent>
      ) : null}
    </Card>
  );
}

/**
 * AttentionRow 是「需要人工」区的一行：标题 + 徽章 + 多久了 / 原因 / 补法 / 动作。
 *
 * 原因用正文色 13px——它是这一屏最决定动作的一句话，不该是全页最灰最小的字。
 * 补法（remedy.hint）只在能说清「为什么重试没用」时才出现。
 */
function AttentionRow({
  title,
  badge,
  detail,
  remedy,
  time,
  meta,
  now,
  notice,
  actions,
}: {
  title: React.ReactNode;
  badge?: React.ReactNode;
  detail: string;
  remedy: Remedy;
  /** 事件发生的时刻（任务侧）。 */
  time?: string;
  /** 时刻之外的次要元信息，如驻留副作用「已试 3 次」。 */
  meta?: string;
  now: number;
  notice?: RetryNotice | string;
  actions?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:flex-wrap sm:items-start sm:justify-between sm:gap-x-4">
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[0.8125rem] font-medium tabular-nums">
          {title}
          {badge}
          {time ? (
            <span
              className="text-xs font-normal text-muted-foreground"
              title={formatAbsolute(time)}
            >
              {formatWhen(time, now)}
            </span>
          ) : null}
          {meta ? (
            <span className="text-xs font-normal text-muted-foreground">{meta}</span>
          ) : null}
        </div>
        <p className="text-[0.8125rem] leading-relaxed break-words text-foreground">{detail}</p>
        {remedy.hint ? (
          <p className="text-xs leading-relaxed break-words text-muted-foreground">{remedy.hint}</p>
        ) : null}
        {notice ? (
          <div className="pt-0.5">
            <Notice tone={typeof notice === "string" ? "bad" : notice.tone}>
              {typeof notice === "string" ? notice : notice.text}
            </Notice>
          </div>
        ) : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-1.5">{actions}</div> : null}
    </div>
  );
}

/** RetryNotice 是一次任务重试的结果提示：成功用 ok，被拒/失败用 warn。 */
type RetryNotice = { tone: "ok" | "warn"; text: string };

/**
 * useTaskRetry 是人工重试（规格 §6.1 / P13）的共用逻辑：忙位、错误、逐版结论。
 *
 * 忙位按行记（键 = movie_id + 是否强制），所以别人在看行 B 时行 A 不会被重新
 * 放开；回执也按 movie_id 记，落在出事的那一行里，不往卡片顶部挤。
 */
function useTaskRetry(onRefresh: () => Promise<void>) {
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
    async (movieID: string, force: boolean) => {
      const key = taskRetryKey(movieID, force);
      // 同一行连点不二次下发：两个 POST 会让后到的那张回执顶掉前一张。
      if (busyRef.current.has(key)) return;
      busyRef.current = new Set(busyRef.current).add(key);
      setBusy(busyRef.current);
      setError(null);
      setNotices((current) => {
        if (!(movieID in current)) return current;
        const next = { ...current };
        delete next[movieID];
        return next;
      });
      try {
        const result = await api.retryTask(movieID, force);
        setNotices((current) => ({ ...current, [movieID]: summarizeRetry(result.results) }));
        await onRefresh();
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      } finally {
        const next = new Set(busyRef.current);
        next.delete(key);
        busyRef.current = next;
        setBusy(next);
      }
    },
    [onRefresh],
  );

  return { busy, error, notices, retry, clearError, clearNotice };
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
function summarizeRetry(results: TaskRetryOutcome[]): RetryNotice {
  if (results.length === 0) {
    return { tone: "warn", text: "这条任务还没有版本，没有可重试的东西。" };
  }

  const blocked = results.filter((result) => result.action === "skipped" || result.action === "failed");
  if (blocked.length > 0) {
    return {
      tone: "warn",
      text: blocked.map((result) => labelResult(result)).join("；"),
    };
  }

  const done = results.map((result) => labelResult(result));
  return { tone: "ok", text: `已下发：${done.join("；")}` };
}

/** labelResult 拼一条版本结局：只有多于一条版本时才前缀哈希。 */
function labelResult(result: TaskRetryOutcome, showHash = false): string {
  const body =
    result.action === "skipped" || result.action === "failed"
      ? result.reason
      : retryActionLabel(result.action);
  return showHash ? `${shortHash(result.infohash)}：${body}` : body;
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

/** useSideEffectRetry 是「重发」的共用逻辑：忙位、逐条错误、刷新面板。 */
function useSideEffectRetry(onRefresh: () => Promise<void>) {
  const [busy, setBusy] = React.useState<number | null>(null);
  const [errors, setErrors] = React.useState<Record<number, string>>({});

  const retry = React.useCallback(
    async (id: number) => {
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
        await onRefresh();
      } catch (cause) {
        setErrors((current) => ({
          ...current,
          [id]: cause instanceof Error ? cause.message : String(cause),
        }));
      } finally {
        setBusy(null);
      }
    },
    [busy, onRefresh],
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

  return (
    <Card>
      <CardHeader>
        <CardTitle
          as="h2"
          title="删清单项、刷 Jellyfin、发通知都走这张表：同目标只排一条，失败退避重试，耗尽后移到「需要人工」。"
        >
          副作用队列
        </CardTitle>
        {effects.length === 0 ? <CardDescription>队列是空的。</CardDescription> : null}
      </CardHeader>
      <CardContent className="space-y-2">
        {effects.length === 0 ? null : (
          <div className="divide-y">
            {effects.map((effect) => {
              return (
                <div
                  key={effect.id}
                  className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:flex-wrap sm:items-start sm:justify-between sm:gap-x-4"
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2 text-[0.8125rem] font-medium">
                      {sideEffectTitle(effect)}
                      <Badge variant="outline" className="h-5 font-normal text-muted-foreground">
                        {effect.attempts > 0 ? `已试 ${effect.attempts} 次` : "还没试过"}
                      </Badge>
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
                    <Button
                      variant="outline"
                      className="h-9 px-2.5 text-[0.8125rem] md:h-7 md:text-xs"
                      disabled={busy === effect.id}
                      onClick={() => void retry(effect.id)}
                    >
                      <RotateCcw
                        className={cn("size-3.5", busy === effect.id && "animate-spin")}
                        aria-hidden
                      />
                      立即重发
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
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

function EmptyTasks() {
  return (
    <div className="px-4 py-10 text-center text-sm text-muted-foreground">
      <p>还没有任务。</p>
      <p className="mt-1 text-xs">
        在 JavDB 的想看清单里加一部有磁力的片，下一轮轮询就会出现在这里。
      </p>
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

function isTaskState(value: string | undefined): value is TaskState {
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
  const name = version.label || sourceLabel(version.source);
  const failed = version.status === "failed";
  return (
    <Badge
      variant="outline"
      title={`${name} · ${stateLabel(version.status)} · ${version.infohash}`}
      aria-label={`${name}，${stateLabel(version.status)}`}
      className={cn(
        "h-5 max-w-28 font-normal",
        failed && "border-status-danger-border text-status-danger-text",
        version.status === "finished" && "border-status-done-border text-status-done-text",
      )}
    >
      <span className="truncate">{name}</span>
      {failed ? <span className="shrink-0">失败</span> : null}
    </Badge>
  );
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
      onClick={onClose}
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
              className="grid size-8 shrink-0 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
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
              <h3 className="text-[0.8125rem] font-medium">版本（{task.versions.length}）</h3>
              <div className="divide-y">
                {task.versions.map((version) => (
                  <VersionCard key={version.infohash} version={version} />
                ))}
              </div>
            </section>
          ) : null}

          {detail ? (
            <section aria-label="事件时间线" className="space-y-2">
              <h3 className="text-[0.8125rem] font-medium">最近事件</h3>
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
            finishedCount={finishedCount}
            onRetried={reload}
            onRefresh={onRefresh}
          />
          <p className="text-xs leading-relaxed text-muted-foreground">
            重试＝回加工开头，或按存的交付材料重新加种；已入库的版本只有「强制重刮」才会重跑。
          </p>
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
  finishedCount,
  onRetried,
  onRefresh,
}: {
  movieID: string;
  finishedCount: number;
  onRetried: () => Promise<void>;
  onRefresh: () => Promise<void>;
}) {
  const taskRetry = useTaskRetry(onRefresh);
  const [confirming, setConfirming] = React.useState(false);
  const notice = taskRetry.notices[movieID];
  const busy = taskRetry.busy.size > 0;

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
        <div className="space-y-2 rounded-lg border border-status-progress-border bg-status-progress-surface px-3 py-2">
          <p className="text-xs leading-relaxed text-status-progress-text">
            确认把 {finishedCount} 条已入库版本拉回加工开头？重刮会重新取数并重写落盘，原来的取数快照留在任务事件里。
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              className="h-9 px-2.5 text-[0.8125rem] md:h-8 md:text-sm"
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
              className="h-9 px-2.5 text-[0.8125rem] md:h-8 md:text-sm"
              onClick={() => setConfirming(false)}
            >
              取消
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <Button
            className="h-9 px-2.5 text-[0.8125rem] md:h-8 md:text-sm"
            disabled={busy}
            onClick={() => void retry(false)}
          >
            <RotateCcw
              className={cn("size-3.5", taskRetry.busy.has(taskRetryKey(movieID, false)) && "animate-spin")}
              aria-hidden
            />
            重试
          </Button>
          {finishedCount > 0 ? (
            <Button
              variant="outline"
              className="h-9 px-2.5 text-[0.8125rem] md:h-8 md:text-sm"
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
  return (
    <div className="space-y-1.5 py-3 first:pt-0 last:pb-0">
      <div className="flex items-center justify-between gap-2">
        <span className="break-all font-mono text-xs leading-relaxed">{version.infohash}</span>
        <StateBadge state={version.status} />
      </div>
      <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
        <span>{sourceLabel(version.source)}</span>
        <span aria-hidden>·</span>
        <span>{version.label || "无标签"}</span>
        {version.attempts > 1 ? <span aria-hidden>·</span> : null}
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
          失败于{phaseLabel(version.error_phase)}：{version.error_reason}
        </p>
      ) : null}
    </div>
  );
}

function EventRow({ event, now }: { event: TaskEvent; now: number }) {
  return (
    <li className="relative pl-4">
      <span className="absolute top-1.5 left-0 size-1.5 rounded-full bg-border" aria-hidden />
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-xs font-medium">{eventKindLabel(event.kind)}</span>
        <time
          className="shrink-0 text-xs tabular-nums text-muted-foreground"
          dateTime={event.at}
          title={formatAbsolute(event.at)}
        >
          {formatWhen(event.at, now)}
        </time>
      </div>
      <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">{event.reason}</p>
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

