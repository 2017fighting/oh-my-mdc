import * as React from "react";
import { ChevronRight, LayoutDashboard, Loader2, Plug, RefreshCw, Sparkles, X } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { Notice } from "~/components/form";
import { Wizard } from "~/routes/wizard";
import {
  api,
  type ConfigSnapshot,
  type Overview,
  type TaskDetail,
  type TaskEvent,
  type TaskRow,
  type TaskState,
  type TaskVersion,
} from "~/lib/api";

/**
 * 整站外壳（规格 §8.2/§8.3）：未初始化时只剩向导，面板入口被门禁挡住；
 * 已初始化时面板是默认视图，「集成设置」回到同一份向导（编辑态）。
 *
 * 门禁判据来自 GET /api/config 的 initialized，跟前端本地状态无关：
 * 主线三件套被删掉，下一次刷新就回到向导。
 */

export function meta() {
  return [
    { title: "oh-my-mdc" },
    { name: "description", content: "把 JavDB 清单里的番号变成 Jellyfin 媒体库条目" },
  ];
}

type View = "panel" | "wizard";

export default function Home() {
  const [snapshot, setSnapshot] = React.useState<ConfigSnapshot | null>(null);
  const [overview, setOverview] = React.useState<Overview | null>(null);
  const [tasks, setTasks] = React.useState<TaskRow[]>([]);
  const [loadError, setLoadError] = React.useState<string | null>(null);
  // 一进站还不知道门禁状态：null 表示「随初始化状态走」（已初始化就进面板）。
  // 用户点过导航之后才由他决定，免得保存完 Jellyfin 就被拽出向导。
  const [requestedView, setRequestedView] = React.useState<View | null>(null);

  const apply = React.useCallback(([config, nextOverview, taskList]: [ConfigSnapshot, Overview, TaskRow[]]) => {
    setSnapshot(config);
    setOverview(nextOverview);
    setTasks(taskList);
    setLoadError(null);
    // 第一次读到门禁状态时定下默认视图；之后一律认用户点过什么，
    // 免得在向导里保存完 Jellyfin（门禁刚打开）就被拽去面板。
    setRequestedView((current) => current ?? (config.initialized ? "panel" : "wizard"));
  }, []);

  const refresh = React.useCallback(async () => {
    try {
      apply(await fetchDashboard());
    } catch (error) {
      setLoadError(error instanceof Error ? error.message : String(error));
    }
  }, [apply]);

  // 首次进入时把设置、概览与任务表拉一次；之后的刷新都由保存/测试/刷新按钮触发。
  React.useEffect(() => {
    let cancelled = false;
    fetchDashboard().then(
      (loaded) => {
        if (!cancelled) apply(loaded);
      },
      (error: unknown) => {
        if (!cancelled) setLoadError(error instanceof Error ? error.message : String(error));
      },
    );
    return () => {
      cancelled = true;
    };
  }, [apply]);

  const initialized = snapshot?.initialized ?? false;
  // 未初始化＝整站只剩向导，不认任何想进面板的意图；已初始化则认用户的选择。
  const view: View = initialized && requestedView === "panel" ? "panel" : "wizard";

  return (
    <div className="mx-auto flex min-h-screen w-full max-w-6xl gap-6 p-6 text-foreground">
      <aside className="hidden w-52 shrink-0 flex-col gap-6 md:flex">
        <div className="flex items-center gap-2">
          <Sparkles className="size-5 text-primary" aria-hidden />
          <span className="font-heading text-base font-semibold tracking-tight">oh-my-mdc</span>
        </div>

        <nav className="flex flex-col gap-1 text-sm" aria-label="主导航">
          <NavItem
            icon={<LayoutDashboard className="size-4" aria-hidden />}
            label="面板"
            current={view === "panel"}
            disabled={!initialized}
            title={initialized ? undefined : "主线三件套不齐，面板进不去"}
            onClick={() => setRequestedView("panel")}
          />
          <NavItem
            icon={<Plug className="size-4" aria-hidden />}
            label="集成设置"
            current={view === "wizard"}
            onClick={() => setRequestedView("wizard")}
          />
        </nav>

        <div className="mt-auto space-y-2 text-xs leading-relaxed text-muted-foreground">
          <Separator />
          <div className="flex items-center justify-between">
            <span>循环</span>
            <span>{loopLabel(snapshot, overview)}</span>
          </div>
          <p>未初始化时整站只剩向导；主线三件套配齐后循环才会启动。</p>
        </div>
      </aside>

      <main className="flex min-w-0 flex-1 flex-col gap-6">
        <header className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h1 className="font-heading text-2xl font-semibold tracking-tight">
              {view === "wizard" ? (initialized ? "集成设置" : "初始化向导") : "面板"}
            </h1>
            <p className="mt-1 text-sm text-muted-foreground">
              {view === "wizard"
                ? "一步一张表：每步只 PATCH 这一节，敏感字段留空就是不改。"
                : "表格为主体，要动手的事置顶；点一行任务打开只读抽屉。"}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Badge variant={initialized ? "secondary" : "outline"}>
              {initialized ? "已初始化" : "未初始化"}
            </Badge>
            {overview?.paused ? <Badge variant="outline">已暂停</Badge> : null}
          </div>
        </header>

        {loadError ? (
          <Notice tone="bad">
            读不到设置：{loadError}
            <button
              type="button"
              className="ml-2 underline underline-offset-4"
              onClick={() => void refresh()}
            >
              重试
            </button>
          </Notice>
        ) : null}

        {!snapshot ? (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" aria-hidden />
            正在读取设置…
          </div>
        ) : view === "wizard" ? (
          <Wizard
            snapshot={snapshot}
            onRefresh={refresh}
            onDone={() => setRequestedView("panel")}
          />
        ) : (
          <Panel
            overview={overview}
            tasks={tasks}
            onOpenWizard={() => setRequestedView("wizard")}
            onRefresh={refresh}
          />
        )}
      </main>
    </div>
  );
}

function NavItem({
  icon,
  label,
  current,
  disabled,
  title,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  current: boolean;
  disabled?: boolean;
  title?: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      aria-current={current ? "page" : undefined}
      className={cn(
        "flex items-center gap-2 rounded-lg px-3 py-2 text-left transition-colors",
        current
          ? "bg-background font-medium ring-1 ring-foreground/10"
          : "text-muted-foreground hover:bg-background/60",
        disabled && "cursor-not-allowed opacity-40",
      )}
    >
      {icon}
      {label}
    </button>
  );
}

/** Panel 是面板：统计卡 → 稠密任务表 → 循环健康（规格 §8.2）。 */
function Panel({
  overview,
  tasks,
  onOpenWizard,
  onRefresh,
}: {
  overview: Overview | null;
  tasks: TaskRow[];
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

      <Card>
        <CardHeader>
          <CardTitle>循环健康</CardTitle>
          <CardDescription>
            三条循环（轮询 / 对账 / 副作用）各自单飞；对账与副作用引擎随后续版本接线。
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

function phaseLabel(phase: TaskVersion["error_phase"] | undefined): string {
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

async function fetchDashboard(): Promise<[ConfigSnapshot, Overview, TaskRow[]]> {
  const [config, overview, taskList] = await Promise.all([api.config(), api.overview(), api.tasks()]);
  return [config, overview, taskList.tasks];
}

function loopLabel(snapshot: ConfigSnapshot | null, overview: Overview | null): string {
  if (!snapshot?.initialized) return "未启动";
  if (overview?.paused) return "已暂停";
  const loops = overview ? Object.values(overview.loops) : [];
  return loops.some((loop) => loop?.last_run_at) ? "运行中" : "未启动";
}
