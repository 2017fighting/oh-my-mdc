import * as React from "react";
import { AlertTriangle, ChevronRight, Loader2, RefreshCw, RotateCcw, X } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { Notice } from "~/components/form";
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
  onPage: (page: number) => void;
  onOpenWizard: () => void;
  onRefresh: () => Promise<void>;
}) {
  const [selected, setSelected] = React.useState<string | null>(null);
  const [refreshing, setRefreshing] = React.useState(false);
  const contentRef = React.useRef<HTMLDivElement>(null);

  // 抽屉是模态：背景整块 inert，页面不跟着滚，关掉再把焦点还给那一行。
  React.useEffect(() => {
    const content = contentRef.current;
    if (!selected || !content) return;
    content.setAttribute("inert", "");
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      content.removeAttribute("inert");
      document.body.style.overflow = previousOverflow;
    };
  }, [selected]);

  const counts = overview?.counts;
  const stats = [
    { label: "下载中", value: counts?.downloading ?? 0, hint: "已交给 qBittorrent、还没下完" },
    { label: "加工中", value: counts?.processing ?? 0, hint: "刮削与落盘进行中" },
    { label: "已入库", value: counts?.finished ?? 0, hint: "已硬链进媒体库" },
    { label: "需人工", value: counts?.needs_attention ?? 0, hint: "失败待重试与驻留副作用" },
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
      <div ref={contentRef} className="flex flex-col gap-6">
      <section aria-label="任务统计" className="grid grid-cols-2 gap-3 xl:grid-cols-4">
        {stats.map((stat) => (
          <Card key={stat.label} size="sm">
            <CardHeader>
              <CardDescription>{stat.label}</CardDescription>
              <CardTitle className="text-2xl tabular-nums">{stat.value}</CardTitle>
            </CardHeader>
            <CardContent className="text-xs text-muted-foreground">{stat.hint}</CardContent>
          </Card>
        ))}
      </section>

      <NeedsAttention
        attention={attention}
        stuck={sideEffects.filter((effect) => effect.stuck)}
        onOpenTask={setSelected}
        onRefresh={onRefresh}
      />

      <Card>
        <CardHeader>
          <div>
            <CardTitle as="h2">任务</CardTitle>
            <CardDescription>一行一任务（按 movie_id 聚合），版本以小 chip 展示。</CardDescription>
          </div>
          <CardAction>
            <Button
              variant="outline"
              size="sm"
              className="text-xs"
              onClick={() => void refresh()}
              disabled={refreshing}
            >
              <RefreshCw className={cn("size-3.5", refreshing && "animate-spin")} aria-hidden />
              刷新
            </Button>
          </CardAction>
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
                      onClick={() => setSelected(task.movie_id)}
                      aria-label={`打开 ${task.number} 的详情`}
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
              <table className="w-full border-collapse text-[0.8125rem] lg:min-w-[34rem]">
                <thead>
                  <tr className="border-y bg-muted/40 text-left text-xs font-medium tracking-wide text-muted-foreground">
                    <th className="px-4 py-2 font-medium">番号</th>
                    <th className="px-4 py-2 font-medium">状态</th>
                    <th className="px-4 py-2 font-medium">版本</th>
                    <th className="hidden px-4 py-2 font-medium md:table-cell">更新</th>
                    <th className="w-8 px-2 py-2" aria-label="打开抽屉" />
                  </tr>
                </thead>
                <tbody>
                  {tasks.map((task) => (
                    <tr
                      key={task.movie_id}
                      onClick={() => setSelected(task.movie_id)}
                      className="cursor-pointer border-b transition-colors last:border-b-0 hover:bg-muted/40"
                    >
                      <td className="px-4 py-2.5 align-top">
                        <div className="font-medium tabular-nums">
                          <button
                            type="button"
                            onClick={(event) => {
                              event.stopPropagation();
                              setSelected(task.movie_id);
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
                      <td className="hidden px-4 py-2.5 align-top text-xs text-muted-foreground md:table-cell">
                        {formatTime(task.updated_at)}
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
          <div className="flex flex-wrap items-center justify-between gap-2 border-t px-4 py-2.5 text-xs text-muted-foreground">
            <span className="tabular-nums">
              第 {page} 页 · 每页 {pageSize} 条
            </span>
            <div className="flex items-center gap-1.5">
              <Button
                variant="outline"
                size="sm"
                className="text-xs"
                disabled={page <= 1}
                onClick={() => onPage(page - 1)}
              >
                上一页
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="text-xs"
                disabled={!hasMore}
                onClick={() => onPage(page + 1)}
              >
                下一页
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      <SideEffectQueue effects={sideEffects.filter((effect) => !effect.stuck)} onRefresh={onRefresh} />

      <Card>
        <CardHeader>
          <CardTitle as="h2">循环健康</CardTitle>
          <CardDescription>
            三条循环（轮询 / 对账 / 副作用）各自单飞；副作用失败退避重试，耗尽后驻留等人工。
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-2 text-sm">
          {(["poll", "reconcile", "sidefx"] as const).map((loop) => {
            const health = overview?.loops?.[loop];
            const label = loop === "poll" ? "清单轮询" : loop === "reconcile" ? "对账" : "副作用";
            return (
              <div key={loop} className="flex items-center justify-between">
                <span>{label}</span>
                <span className="text-muted-foreground">
                  {health?.last_run_at
                    ? `${formatTime(health.last_run_at)} · ${health.last_ok ? "成功" : "失败"}`
                    : "还没跑过"}
                </span>
              </div>
            );
          })}
          <Separator className="my-1" />
          <Button
            variant="link"
            size="sm"
            onClick={onOpenWizard}
            className="h-7 self-start px-0 text-sm"
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
 * 任务侧两半可以当场「重试」（版本侧结局由接口逐条回），驻留副作用当场「重发」。
 */
function NeedsAttention({
  attention,
  stuck,
  onOpenTask,
  onRefresh,
}: {
  attention: Attention | null;
  stuck: SideEffect[];
  onOpenTask: (movieID: string) => void;
  onRefresh: () => Promise<void>;
}) {
  const failed = attention?.failed ?? [];
  const stalled = attention?.stalled ?? [];
  const total = failed.length + stalled.length + stuck.length;

  const { busy, error, retry } = useSideEffectRetry(onRefresh);
  // 任务侧重试与副作用重发是两件事，各有自己的忙位与提示（规格 §8.2 的行内动作）。
  const taskRetry = useTaskRetry(onRefresh);

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
          <CardDescription>
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
        <CardContent className="space-y-2">
          {error ? <Notice tone="bad">{error}</Notice> : null}
          {taskRetry.error ? <Notice tone="bad">{taskRetry.error}</Notice> : null}
          {taskRetry.notice ? (
            <Notice tone={taskRetry.notice.tone}>{taskRetry.notice.text}</Notice>
          ) : null}

          <div className="divide-y">
          {failed.map((item) => (
            <AttentionRow
              key={`failed-${item.infohash}`}
              title={item.number}
              badge={
                <Badge
                  variant="outline"
                  className="border-transparent bg-status-danger-surface text-status-danger-text"
                >
                  {phaseLabel(item.phase)}失败
                </Badge>
              }
              detail={item.reason}
              action={
                <RetryActions
                  movieID={item.movie_id}
                  busy={taskRetry.busy === taskRetryKey(item.movie_id, false)}
                  onRetry={() => void taskRetry.retry(item.movie_id, false)}
                  onOpen={() => onOpenTask(item.movie_id)}
                />
              }
            />
          ))}

          {stalled.map((item) => (
            <AttentionRow
              key={`stalled-${item.infohash}`}
              title={item.number}
              badge={<Badge variant="outline">下载停滞</Badge>}
              detail={`${item.reason}（状态仍是下载中，qbt 里还在下就不会重下）`}
              action={
                <RetryActions
                  movieID={item.movie_id}
                  busy={taskRetry.busy === taskRetryKey(item.movie_id, false)}
                  onRetry={() => void taskRetry.retry(item.movie_id, false)}
                  onOpen={() => onOpenTask(item.movie_id)}
                />
              }
            />
          ))}

          {stuck.map((effect) => (
            <AttentionRow
              key={`stuck-${effect.id}`}
              title={sideEffectTitle(effect)}
              badge={
                <Badge
                  variant="outline"
                  className="border-transparent bg-status-danger-surface text-status-danger-text"
                >
                  副作用驻留
                </Badge>
              }
              detail={effect.last_error || "自动重试已放弃，等人工重发。"}
              action={
                <Button
                  variant="outline"
                  size="sm"
                  className="text-xs"
                  disabled={busy === effect.id}
                  onClick={() => void retry(effect.id)}
                >
                  <RotateCcw className={cn("size-3.5", busy === effect.id && "animate-spin")} aria-hidden />
                  重发
                </Button>
              }
            />
          ))}
          </div>
        </CardContent>
      ) : null}
    </Card>
  );
}

/** AttentionRow 是「需要人工」区的一行：标题 + 徽章 + 原因 + 行内动作。 */
function AttentionRow({
  title,
  badge,
  detail,
  action,
}: {
  title: React.ReactNode;
  badge?: React.ReactNode;
  detail: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-2 py-3 first:pt-0 last:pb-0">
      <div className="min-w-0 space-y-1">
        <div className="flex items-center gap-2 text-[0.8125rem] font-medium tabular-nums">
          {title}
          {badge}
        </div>
        <p className="text-xs leading-relaxed break-words text-muted-foreground">{detail}</p>
      </div>
      {action}
    </div>
  );
}

/** RetryActions 是「需要人工」一行的行内动作：重试（任务侧）+ 查看任务。 */
function RetryActions({
  movieID,
  busy,
  onRetry,
  onOpen,
}: {
  movieID: string;
  busy: boolean;
  onRetry: () => void;
  onOpen: () => void;
}) {
  return (
    <div className="flex shrink-0 items-center gap-1.5" data-movie-id={movieID}>
      <Button variant="outline" size="sm" className="text-xs" disabled={busy} onClick={onRetry}>
        <RotateCcw className={cn("size-3.5", busy && "animate-spin")} aria-hidden />
        重试
      </Button>
      <Button variant="outline" size="sm" className="text-xs" onClick={onOpen}>
        查看任务
      </Button>
    </div>
  );
}

/** RetryNotice 是一次任务重试的结果提示：成功用 ok，被拒/失败用 warn。 */
type RetryNotice = { tone: "ok" | "warn"; text: string };

/**
 * useTaskRetry 是人工重试（规格 §6.1 / P13）的共用逻辑：忙位、错误、逐版结论。
 *
 * 版本侧的结局是 200 里的逐版结果：被拒（qbt 仍在下载）与加种失败都不该当成
 * 网络错误弹红字，但要原样告诉人为什么没动。
 */
function useTaskRetry(onRefresh: () => Promise<void>) {
  const [busy, setBusy] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<RetryNotice | null>(null);

  const retry = React.useCallback(
    async (movieID: string, force: boolean) => {
      setBusy(taskRetryKey(movieID, force));
      setError(null);
      setNotice(null);
      try {
        const result = await api.retryTask(movieID, force);
        setNotice(summarizeRetry(result.results));
        await onRefresh();
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      } finally {
        setBusy(null);
      }
    },
    [onRefresh],
  );

  return { busy, error, notice, retry };
}

/** taskRetryKey 是忙位的键：同一任务的「重试」与「强制重刮」互不复用。 */
function taskRetryKey(movieID: string, force: boolean): string {
  return `${movieID}:${force ? "force" : "retry"}`;
}

/** summarizeRetry 把逐版结果折成一句话：被拒/失败的排在前面，成功只说做了哪件事。 */
function summarizeRetry(results: TaskRetryOutcome[]): RetryNotice {
  if (results.length === 0) {
    return { tone: "warn", text: "这条任务还没有版本，没有可重试的东西。" };
  }

  const blocked = results.filter((result) => result.action === "skipped" || result.action === "failed");
  if (blocked.length > 0) {
    return {
      tone: "warn",
      text: blocked.map((result) => `${shortHash(result.infohash)}：${result.reason}`).join("；"),
    };
  }

  const done = results.map((result) => `${shortHash(result.infohash)} ${retryActionLabel(result.action)}`);
  return { tone: "ok", text: `已下发：${done.join("；")}` };
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

/** useSideEffectRetry 是「重发 / 提前重试」的共用逻辑：忙位、错误、刷新面板。 */
function useSideEffectRetry(onRefresh: () => Promise<void>) {
  const [busy, setBusy] = React.useState<number | null>(null);
  const [error, setError] = React.useState<string | null>(null);

  const retry = React.useCallback(
    async (id: number) => {
      setBusy(id);
      setError(null);
      try {
        await api.retrySideEffect(id);
        await onRefresh();
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      } finally {
        setBusy(null);
      }
    },
    [onRefresh],
  );

  return { busy, error, retry };
}

/** SideEffectQueue 是底部的副作用队列：排队中的待办与它们的下一次尝试时间。 */
function SideEffectQueue({
  effects,
  onRefresh,
}: {
  effects: SideEffect[];
  onRefresh: () => Promise<void>;
}) {
  const { busy, error, retry } = useSideEffectRetry(onRefresh);

  return (
    <Card>
      <CardHeader>
        <CardTitle as="h2">副作用队列</CardTitle>
        <CardDescription>
          删清单项、刷 Jellyfin、发通知都走这张表：同目标只排一条，失败退避重试，耗尽后移到「需要人工」。
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-2">
        {error ? <Notice tone="bad">{error}</Notice> : null}
        {effects.length === 0 ? (
          <p className="text-xs text-muted-foreground">队列是空的。</p>
        ) : (
          <div className="divide-y">
            {effects.map((effect) => (
              <div
                key={effect.id}
                className="flex flex-wrap items-center justify-between gap-2 py-3 first:pt-0 last:pb-0"
              >
                <div className="min-w-0">
                  <div className="flex items-center gap-2 text-[0.8125rem] font-medium">
                    {sideEffectTitle(effect)}
                    <Badge variant="outline" className="h-5 font-normal text-muted-foreground">
                      第 {effect.attempts + 1} 次
                    </Badge>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    下次尝试 {formatTime(effect.next_attempt_at)}
                    {effect.last_error ? ` · 上次：${effect.last_error}` : ""}
                  </p>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  className="text-xs"
                  disabled={busy === effect.id}
                  onClick={() => void retry(effect.id)}
                >
                  <RotateCcw className={cn("size-3.5", busy === effect.id && "animate-spin")} aria-hidden />
                  提前重试
                </Button>
              </div>
            ))}
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

/** VersionChip 是版本的小 chip：标签（或来源）一眼可见，失败用红色。 */
function VersionChip({ version }: { version: TaskVersion }) {
  const name = version.label || sourceLabel(version.source);
  return (
    <Badge
      variant="outline"
      title={`${name} · ${stateLabel(version.status)} · ${version.infohash}`}
      className={cn(
        "h-5 max-w-28 font-normal",
        version.status === "failed" && "border-status-danger-border text-status-danger-text",
        version.status === "finished" && "border-status-done-border text-status-done-text",
      )}
    >
      <span className="truncate">{name}</span>
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

  // 模态：开时焦点进抽屉，Tab 在内部循环，Esc 关闭，关掉把焦点还给打开它的那行。
  React.useEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeRef.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
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
    return () => {
      window.removeEventListener("keydown", onKey);
      // 等背景的 inert 先撤掉：对 inert 子树里的元素调用 focus() 会被忽略。
      window.setTimeout(() => previous?.focus(), 0);
    };
  }, [onClose]);

  const task = detail?.task;

  return (
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
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            aria-label="关闭抽屉"
            className="grid size-8 shrink-0 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
          >
            <X className="size-4" aria-hidden />
          </button>
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
                    <EventRow key={event.id} event={event} />
                  ))}
                </ol>
              )}
            </section>
          ) : null}
        </div>

        <footer className="space-y-2 border-t px-4 py-3">
          <DrawerActions movieID={movieID} onRetried={reload} onRefresh={onRefresh} />
          <p className="text-xs leading-relaxed text-muted-foreground">
            重试＝回加工开头或按存的 payload 重新加种；已入库的版本只有「强制重刮」才会重跑。
          </p>
        </footer>
      </aside>
    </div>
  );
}

/**
 * DrawerActions 是抽屉底部的两个按钮（规格 §8.3 文案：重试 / 强制重刮）。
 *
 * 点完先重读抽屉（版本列表与时间线要立刻反映新状态），再刷新面板（统计卡与需人工区）。
 */
function DrawerActions({
  movieID,
  onRetried,
  onRefresh,
}: {
  movieID: string;
  onRetried: () => Promise<void>;
  onRefresh: () => Promise<void>;
}) {
  const taskRetry = useTaskRetry(onRefresh);

  const retry = async (force: boolean) => {
    await taskRetry.retry(movieID, force);
    await onRetried();
  };

  return (
    <>
      {taskRetry.error ? <Notice tone="bad">{taskRetry.error}</Notice> : null}
      {taskRetry.notice ? (
        <Notice tone={taskRetry.notice.tone}>{taskRetry.notice.text}</Notice>
      ) : null}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          className="text-xs"
          disabled={taskRetry.busy !== null}
          onClick={() => void retry(false)}
        >
          <RotateCcw className={cn("size-3.5", taskRetry.busy === taskRetryKey(movieID, false) && "animate-spin")} aria-hidden />
          重试
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="text-xs"
          disabled={taskRetry.busy !== null}
          onClick={() => void retry(true)}
        >
          <RotateCcw className={cn("size-3.5", taskRetry.busy === taskRetryKey(movieID, true) && "animate-spin")} aria-hidden />
          强制重刮
        </Button>
      </div>
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

function EventRow({ event }: { event: TaskEvent }) {
  return (
    <li className="relative pl-4">
      <span className="absolute top-1.5 left-0 size-1.5 rounded-full bg-border" aria-hidden />
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-xs font-medium">{eventKindLabel(event.kind)}</span>
        <time className="shrink-0 text-xs text-muted-foreground">{formatTime(event.at)}</time>
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

function formatTime(value: string | null | undefined): string {
  if (!value) return "—";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString("zh-CN", { hour12: false });
}

