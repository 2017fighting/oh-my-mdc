import * as React from "react";
import { AlertTriangle, ChevronRight, Loader2, RefreshCw, RotateCcw, X } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { Notice } from "~/components/form";
import {
  api,
  type Attention,
  type AttentionItem,
  type Overview,
  type SideEffect,
  type TaskDetail,
  type TaskEvent,
  type TaskRow,
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
  onOpenWizard,
  onRefresh,
}: {
  overview: Overview | null;
  tasks: TaskRow[];
  attention: Attention | null;
  sideEffects: SideEffect[];
  onOpenWizard: () => void;
  onRefresh: () => Promise<void>;
}) {
  const [selected, setSelected] = React.useState<string | null>(null);
  const [refreshing, setRefreshing] = React.useState(false);

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

  return (
    <>
      <section aria-label="任务统计" className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
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
        <CardHeader className="flex-row items-start justify-between gap-3">
          <div>
            <CardTitle>任务</CardTitle>
            <CardDescription>一行一任务（按 movie_id 聚合），版本以小 chip 展示。</CardDescription>
          </div>
          <button
            type="button"
            onClick={() => void refresh()}
            disabled={refreshing}
            className="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-lg border border-input px-2.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted disabled:opacity-60"
          >
            <RefreshCw className={cn("size-3.5", refreshing && "animate-spin")} aria-hidden />
            刷新
          </button>
        </CardHeader>
        <CardContent className="px-0">
          {tasks.length === 0 ? (
            <EmptyTasks />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-[13px]">
                <thead>
                  <tr className="border-y bg-muted/40 text-left text-[11px] font-medium tracking-wide text-muted-foreground">
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
                      tabIndex={0}
                      role="button"
                      aria-label={`打开 ${task.number} 的详情`}
                      onClick={() => setSelected(task.movie_id)}
                      onKeyDown={(event) => {
                        if (event.key === "Enter" || event.key === " ") {
                          event.preventDefault();
                          setSelected(task.movie_id);
                        }
                      }}
                      className="cursor-pointer border-b transition-colors last:border-b-0 hover:bg-muted/40 focus-visible:bg-muted/40 focus-visible:outline-none"
                    >
                      <td className="px-4 py-2.5 align-top">
                        <div className="font-medium tabular-nums">{task.number}</div>
                        <div className="font-mono text-[11px] text-muted-foreground">{task.movie_id}</div>
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
                      <td className="px-4 py-2.5 align-top text-xs text-muted-foreground">
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
          )}
        </CardContent>
      </Card>

      <SideEffectQueue effects={sideEffects.filter((effect) => !effect.stuck)} onRefresh={onRefresh} />

      <Card>
        <CardHeader>
          <CardTitle>循环健康</CardTitle>
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
          <button
            type="button"
            onClick={onOpenWizard}
            className="self-start text-sm font-medium text-primary underline-offset-4 hover:underline"
          >
            去集成设置
          </button>
        </CardContent>
      </Card>

      {selected ? (
        <TaskDrawer key={selected} movieID={selected} onClose={() => setSelected(null)} />
      ) : null}
    </>
  );
}

/** NeedsAttention 是「需要人工」区（规格 §8.2：置顶，行内直接动手）。
 *
 * 三半拼在一起：失败的任务（phase + reason）、停滞的下载、驻留的副作用。
 * 前两半只能「看任务」（重试归人工重试票），驻留副作用可以当场「重发」。
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

  return (
    <Card className={cn(total > 0 && "ring-destructive/30")}>
      <CardHeader className="flex-row items-start justify-between gap-3">
        <div>
          <CardTitle className="flex items-center gap-2">
            <AlertTriangle
              className={cn("size-4", total > 0 ? "text-destructive" : "text-muted-foreground")}
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
        <Badge variant={total > 0 ? "destructive" : "secondary"}>{total}</Badge>
      </CardHeader>
      {total > 0 ? (
        <CardContent className="space-y-2">
          {error ? <Notice tone="bad">{error}</Notice> : null}

          {failed.map((item) => (
            <AttentionRow
              key={`failed-${item.infohash}`}
              title={item.number}
              badge={<Badge variant="destructive">{phaseLabel(item.phase)}失败</Badge>}
              detail={item.reason}
              action={
                <button type="button" className={MiniActionClass} onClick={() => onOpenTask(item.movie_id)}>
                  查看任务
                </button>
              }
            />
          ))}

          {stalled.map((item) => (
            <AttentionRow
              key={`stalled-${item.infohash}`}
              title={item.number}
              badge={<Badge variant="outline">下载停滞</Badge>}
              detail={`${item.reason}（状态仍是下载中，确认后可就地处理）`}
              action={
                <button type="button" className={MiniActionClass} onClick={() => onOpenTask(item.movie_id)}>
                  查看任务
                </button>
              }
            />
          ))}

          {stuck.map((effect) => (
            <AttentionRow
              key={`stuck-${effect.id}`}
              title={sideEffectTitle(effect)}
              badge={<Badge variant="destructive">副作用驻留</Badge>}
              detail={effect.last_error || "自动重试已放弃，等人工重发。"}
              action={
                <button
                  type="button"
                  className={MiniActionClass}
                  disabled={busy === effect.id}
                  onClick={() => void retry(effect.id)}
                >
                  <RotateCcw className={cn("size-3.5", busy === effect.id && "animate-spin")} aria-hidden />
                  重发
                </button>
              }
            />
          ))}
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
    <div className="flex flex-wrap items-start justify-between gap-2 rounded-lg border p-3">
      <div className="min-w-0 space-y-1">
        <div className="flex items-center gap-2 text-[13px] font-medium tabular-nums">
          {title}
          {badge}
        </div>
        <p className="text-[11px] leading-relaxed break-words text-muted-foreground">{detail}</p>
      </div>
      {action}
    </div>
  );
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
        <CardTitle>副作用队列</CardTitle>
        <CardDescription>
          删清单项、刷 Jellyfin、发通知都走这张表：同目标只排一条，失败退避重试，耗尽后移到「需要人工」。
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-2">
        {error ? <Notice tone="bad">{error}</Notice> : null}
        {effects.length === 0 ? (
          <p className="text-xs text-muted-foreground">队列是空的。</p>
        ) : (
          effects.map((effect) => (
            <div
              key={effect.id}
              className="flex flex-wrap items-center justify-between gap-2 rounded-lg border px-3 py-2"
            >
              <div className="min-w-0">
                <div className="flex items-center gap-2 text-[13px] font-medium">
                  {sideEffectTitle(effect)}
                  <Badge variant="outline" className="h-5 font-normal text-muted-foreground">
                    第 {effect.attempts + 1} 次
                  </Badge>
                </div>
                <p className="text-[11px] text-muted-foreground">
                  下次尝试 {formatTime(effect.next_attempt_at)}
                  {effect.last_error ? ` · 上次：${effect.last_error}` : ""}
                </p>
              </div>
              <button
                type="button"
                className={MiniActionClass}
                disabled={busy === effect.id}
                onClick={() => void retry(effect.id)}
              >
                <RotateCcw className={cn("size-3.5", busy === effect.id && "animate-spin")} aria-hidden />
                提前重试
              </button>
            </div>
          ))
        )}
      </CardContent>
    </Card>
  );
}

/** 行内小按钮的唯一 class（重发/提前重试/查看任务共用）。 */
const MiniActionClass =
  "inline-flex h-7 shrink-0 items-center gap-1 rounded-lg border border-input px-2 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted disabled:opacity-60";

/** 一条待办的人话标题：番号（有的话）+ 这一条在做什么。 */
function sideEffectTitle(effect: SideEffect): string {
  const number = typeof effect.payload.number === "string" ? effect.payload.number : "";
  const label = sideEffectKindLabel(effect.kind);
  return number ? `${number} · ${label}` : label;
}

function sideEffectKindLabel(kind: SideEffect["kind"]): string {
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
  downloading: { label: "下载中", className: "bg-sky-50 text-sky-800 dark:bg-sky-950 dark:text-sky-300" },
  processing: { label: "加工中", className: "bg-amber-50 text-amber-900 dark:bg-amber-950 dark:text-amber-300" },
  finished: { label: "已入库", className: "bg-emerald-50 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300" },
  failed: { label: "需人工", className: "bg-destructive/10 text-destructive" },
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
    <Badge variant="outline" className={cn("border-transparent", badge.className)}>
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
        version.status === "failed" && "border-destructive/30 text-destructive",
        version.status === "finished" && "border-emerald-300 text-emerald-800 dark:border-emerald-800 dark:text-emerald-300",
      )}
    >
      <span className="truncate">{name}</span>
    </Badge>
  );
}

/** TaskDrawer 是只读抽屉：版本列表（infohash/来源/标签/失败原因）+ 事件时间线。 */
function TaskDrawer({ movieID, onClose }: { movieID: string; onClose: () => void }) {
  const [detail, setDetail] = React.useState<TaskDetail | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const closeRef = React.useRef<HTMLButtonElement>(null);

  React.useEffect(() => {
    let cancelled = false;
    api.task(movieID).then(
      (loaded) => {
        if (!cancelled) setDetail(loaded);
      },
      (cause: unknown) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : String(cause));
      },
    );
    return () => {
      cancelled = true;
    };
  }, [movieID]);

  React.useEffect(() => {
    closeRef.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const task = detail?.task;

  return (
    <div
      className="fixed inset-0 z-50 flex justify-end bg-foreground/20"
      role="presentation"
      onClick={onClose}
    >
      <aside
        role="dialog"
        aria-modal="true"
        aria-label="任务详情"
        onClick={(event) => event.stopPropagation()}
        className="flex h-full w-full max-w-lg flex-col overflow-hidden border-l border-border bg-background shadow-2xl"
      >
        <header className="flex items-start justify-between gap-3 border-b px-4 py-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <h2 className="font-heading text-lg font-semibold tabular-nums">
                {task?.number ?? movieID}
              </h2>
              {task ? <StateBadge state={task.state} /> : null}
            </div>
            <p className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">
              {task?.movie_id ?? movieID}
            </p>
          </div>
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            aria-label="关闭抽屉"
            className="rounded-lg p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <X className="size-4" aria-hidden />
          </button>
        </header>

        <div className="flex-1 space-y-6 overflow-y-auto px-4 py-4">
          {error ? <Notice tone="bad">读不到任务详情：{error}</Notice> : null}
          {!detail && !error ? (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden />
              正在读取…
            </div>
          ) : null}

          {task ? (
            <section aria-label="版本列表" className="space-y-2">
              <h3 className="text-[13px] font-medium">版本（{task.versions.length}）</h3>
              {task.versions.map((version) => (
                <VersionCard key={version.infohash} version={version} />
              ))}
            </section>
          ) : null}

          {detail ? (
            <section aria-label="事件时间线" className="space-y-2">
              <h3 className="text-[13px] font-medium">最近事件</h3>
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

        <footer className="border-t px-4 py-2.5 text-[11px] text-muted-foreground">
          v1 抽屉只读：重试 / 强制重刮在人工重试票落地；这里不看刮削字段与 provenance。
        </footer>
      </aside>
    </div>
  );
}

function VersionCard({ version }: { version: TaskVersion }) {
  return (
    <div className="space-y-1.5 rounded-lg border p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="break-all font-mono text-[11px] leading-relaxed">{version.infohash}</span>
        <StateBadge state={version.status} />
      </div>
      <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
        <span>{sourceLabel(version.source)}</span>
        <span aria-hidden>·</span>
        <span>{version.label || "无标签"}</span>
        {version.attempts > 1 ? <span aria-hidden>·</span> : null}
        {version.attempts > 1 ? <span>尝试 {version.attempts} 次</span> : null}
        {version.add_pending ? (
          <Badge variant="outline" className="h-5 border-amber-300 font-normal text-amber-800 dark:border-amber-800 dark:text-amber-300">
            待加种
          </Badge>
        ) : null}
        {version.list_removal_pending ? (
          <Badge variant="outline" className="h-5 font-normal text-muted-foreground">
            待删清单项
          </Badge>
        ) : null}
      </div>
      {version.error_reason ? (
        <p className="text-[11px] text-destructive">
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
        <span className="text-[12px] font-medium">{eventKindLabel(event.kind)}</span>
        <time className="shrink-0 text-[11px] text-muted-foreground">{formatTime(event.at)}</time>
      </div>
      <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">{event.reason}</p>
      {event.from_state || event.to_state ? (
        <p className="mt-0.5 text-[11px] text-muted-foreground">
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

