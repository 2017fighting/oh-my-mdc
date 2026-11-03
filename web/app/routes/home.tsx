import * as React from "react";
import { LayoutDashboard, Loader2, Plug, Sparkles } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Separator } from "~/components/ui/separator";
import { Notice } from "~/components/form";
import { Panel } from "~/components/panel";
import { Wizard } from "~/routes/wizard";
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
  // 一进站还不知道门禁状态：null 表示「随初始化状态走」（已初始化就进面板）。
  // 用户点过导航之后才由他决定，免得保存完 Jellyfin 就被拽出向导。
  const [requestedView, setRequestedView] = React.useState<View | null>(null);

  const apply = React.useCallback(
    ([config, nextOverview, taskList, nextAttention, nextSideEffects]: Dashboard) => {
      setSnapshot(config);
      setOverview(nextOverview);
      setTasks(taskList);
      setAttention(nextAttention);
      setSideEffects(nextSideEffects);
      setLoadError(null);
      // 第一次读到门禁状态时定下默认视图；之后一律认用户点过什么，
      // 免得在向导里保存完 Jellyfin（门禁刚打开）就被拽去面板。
      setRequestedView((current) => current ?? (config.initialized ? "panel" : "wizard"));
    },
    [],
  );

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
                : "表格为主体，要动手的事置顶；点一行任务打开抽屉（重试 / 强制重刮）。"}
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
            attention={attention}
            sideEffects={sideEffects}
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

/** Dashboard 是一次面板读取的全部输入（设置、概览、任务、「需要人工」区、副作用队列）。 */
type Dashboard = [ConfigSnapshot, Overview, TaskRow[], Attention, SideEffect[]];

async function fetchDashboard(): Promise<Dashboard> {
  const [config, overview, taskList, attention, sideEffects] = await Promise.all([
    api.config(),
    api.overview(),
    api.tasks(),
    api.attention(),
    api.sideEffects(),
  ]);
  return [config, overview, taskList.tasks, attention, sideEffects.side_effects];
}

function loopLabel(snapshot: ConfigSnapshot | null, overview: Overview | null): string {
  if (!snapshot?.initialized) return "未启动";
  if (overview?.paused) return "已暂停";
  const loops = overview ? Object.values(overview.loops) : [];
  return loops.some((loop) => loop?.last_run_at) ? "运行中" : "未启动";
}
