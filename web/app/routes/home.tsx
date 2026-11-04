import * as React from "react";
import { LayoutDashboard, Loader2, Plug, Sparkles } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Separator } from "~/components/ui/separator";
import { Notice } from "~/components/form";
import { Panel } from "~/components/panel";
import { Wizard } from "~/routes/wizard";
import { formatInterval } from "~/lib/time";
import {
  api,
  type Attention,
  type ConfigSnapshot,
  type Overview,
  type SideEffect,
  type TaskRow,
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
  const [attention, setAttention] = React.useState<Attention | null>(null);
  const [sideEffects, setSideEffects] = React.useState<SideEffect[]>([]);
  const [loadError, setLoadError] = React.useState<string | null>(null);
  const [page, setPage] = React.useState(1);
  const [pageSize, setPageSize] = React.useState(50);
  // 上一次真正读到数据的时刻（客户端时钟）。值班台必须知道自己有多旧，
  // 否则「还没好」与「这是旧快照」在屏幕上长得一模一样。
  const [loadedAt, setLoadedAt] = React.useState<number | null>(null);
  // 一进站还不知道门禁状态：null 表示「随初始化状态走」（已初始化就进面板）。
  // 用户点过导航之后才由他决定，免得保存完 Jellyfin 就被拽出向导。
  const [requestedView, setRequestedView] = React.useState<View | null>(null);

  const apply = React.useCallback(
    ([config, nextOverview, taskList, nextAttention, nextSideEffects, listPage, listPageSize]: Dashboard) => {
      setSnapshot(config);
      setOverview(nextOverview);
      setTasks(taskList);
      setAttention(nextAttention);
      setSideEffects(nextSideEffects);
      setPageSize(listPageSize);
      setLoadError(null);
      setLoadedAt(Date.now());
      // 删完最后一页的全部任务时退一页，不让空页把人留在空白表格上。
      if (listPage > 1 && taskList.length === 0) {
        setPage(listPage - 1);
      }
      // 第一次读到门禁状态时定下默认视图；之后一律认用户点过什么，
      // 免得在向导里保存完 Jellyfin（门禁刚打开）就被拽去面板。
      setRequestedView((current) => current ?? (config.initialized ? "panel" : "wizard"));
    },
    [],
  );

  const refresh = React.useCallback(async () => {
    try {
      apply(await fetchDashboard(page));
    } catch (error) {
      setLoadError(error instanceof Error ? error.message : String(error));
    }
  }, [apply, page]);

  // 首次进入与翻页时把设置、概览与任务表拉一次；其余刷新由保存/测试/刷新按钮触发。
  React.useEffect(() => {
    let cancelled = false;
    fetchDashboard(page).then(
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
  }, [apply, page]);

  const initialized = snapshot?.initialized ?? false;
  // 未初始化＝整站只剩向导，不认任何想进面板的意图；已初始化则认用户的选择。
  const view: View = initialized && requestedView === "panel" ? "panel" : "wizard";

  // 从向导回到面板时重读一次：改完 Telegram 再回来，得看到改完之后的队列，
  // 而不是离开前的快照。首次挂载不重复拉（上面那个 effect 已经拉过了）。
  const previousView = React.useRef<View | null>(null);
  React.useEffect(() => {
    const previous = previousView.current;
    previousView.current = view;
    // 首次挂载、翻页都不走这里；只有「从向导回到面板」补一次读取。
    if (previous === "wizard" && view === "panel") void refresh();
  }, [view, refresh]);

  const loop = loopState(snapshot, overview);

  // 三条循环的间隔读自 app_config；读不到就是 null，不编默认值。
  const appConfig = (snapshot?.sections?.app?.data ?? {}) as Record<string, unknown>;
  const intervals = {
    poll: formatInterval(appConfig.poll_interval),
    reconcile: formatInterval(appConfig.reconcile_interval),
    sidefx: formatInterval(appConfig.sidefx_interval),
  };

  return (
    <div id="app-shell" className="mx-auto flex min-h-screen w-full max-w-6xl gap-6 p-6 text-foreground">
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
          <div className="flex items-center justify-between gap-2">
            <span>循环</span>
            <span className={cn(loop.failing && "text-status-danger-text")}>{loop.text}</span>
          </div>
          <p>未初始化时整站只剩向导；主线三件套配齐后循环才会启动。</p>
        </div>
      </aside>

      <main id="main" tabIndex={-1} className="flex min-w-0 flex-1 flex-col gap-6 outline-none">
        <div className="flex flex-col gap-3 md:hidden">
          <div className="flex items-center gap-2">
            <Sparkles className="size-5 text-primary" aria-hidden />
            <span className="font-heading text-base font-semibold tracking-tight">oh-my-mdc</span>
          </div>
          <nav
            aria-label="视图切换"
            className="grid grid-cols-2 gap-1 rounded-lg bg-background p-1 ring-1 ring-foreground/10"
          >
            <MobileNavItem
              icon={<LayoutDashboard className="size-4" aria-hidden />}
              label="面板"
              current={view === "panel"}
              disabled={!initialized}
              onClick={() => setRequestedView("panel")}
            />
            <MobileNavItem
              icon={<Plug className="size-4" aria-hidden />}
              label="集成设置"
              current={view === "wizard"}
              onClick={() => setRequestedView("wizard")}
            />
          </nav>
        </div>

        <header>
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <h1 className="font-heading text-2xl font-semibold tracking-tight">
              {view === "wizard" ? (initialized ? "集成设置" : "初始化向导") : "面板"}
            </h1>
            {view === "wizard" ? (
              <Badge variant={initialized ? "secondary" : "outline"}>
                {initialized ? "已初始化" : "未初始化"}
              </Badge>
            ) : null}
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
          <div role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
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
            attention={attention}
            sideEffects={sideEffects}
            page={page}
            pageSize={pageSize}
            loadedAt={loadedAt}
            intervals={intervals}
            onPage={setPage}
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
        "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
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

/** MobileNavItem 是窄屏的两格视图切换：44px 高，拇指够得着，不和桌面侧栏共用一套尺寸。 */
function MobileNavItem({
  icon,
  label,
  current,
  disabled,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  current: boolean;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-current={current ? "page" : undefined}
      className={cn(
        "flex h-11 items-center justify-center gap-2 rounded-md text-sm transition-colors",
        "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
        current ? "bg-secondary font-medium text-secondary-foreground" : "text-muted-foreground",
        disabled && "cursor-not-allowed opacity-40",
      )}
    >
      {icon}
      {label}
    </button>
  );
}

/** Dashboard 是一次面板读取的全部输入（设置、概览、任务、「需要人工」区、副作用队列、分页）。 */
type Dashboard = [ConfigSnapshot, Overview, TaskRow[], Attention, SideEffect[], number, number];

async function fetchDashboard(page: number): Promise<Dashboard> {
  const [config, overview, taskList, attention, sideEffects] = await Promise.all([
    api.config(),
    api.overview(),
    api.tasks({ page }),
    api.attention(),
    api.sideEffects(),
  ]);
  return [
    config,
    overview,
    taskList.tasks,
    attention,
    sideEffects.side_effects,
    taskList.page,
    taskList.page_size,
  ];
}

/**
 * loopState 是侧栏那一行。
 *
 * 以前只要有一条循环跑过就说「运行中」，于是侧栏「运行中」与两屏下的「副作用 失败」
 * 同时在屏。失败必须进这句话，因为那才是回来处理东西的理由。
 */
function loopState(
  snapshot: ConfigSnapshot | null,
  overview: Overview | null,
): { text: string; failing: boolean } {
  if (!snapshot?.initialized) return { text: "未启动", failing: false };
  if (overview?.paused) return { text: "已暂停", failing: false };
  const loops = overview ? Object.values(overview.loops) : [];
  if (!loops.some((item) => item?.last_run_at)) return { text: "未启动", failing: false };
  const failed = loops.filter((item) => item?.last_run_at && item.last_ok === false).length;
  return failed > 0
    ? { text: `运行中 · ${failed} 条失败`, failing: true }
    : { text: "运行中", failing: false };
}
