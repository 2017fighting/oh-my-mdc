import * as React from "react";
import { LayoutDashboard, Loader2, Plug, Sparkles } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { Notice } from "~/components/form";
import { Wizard } from "~/routes/wizard";
import { api, type ConfigSnapshot, type Overview } from "~/lib/api";

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
  const [loadError, setLoadError] = React.useState<string | null>(null);
  // 一进站还不知道门禁状态：null 表示「随初始化状态走」（已初始化就进面板）。
  // 用户点过导航之后才由他决定，免得保存完 Jellyfin 就被拽出向导。
  const [requestedView, setRequestedView] = React.useState<View | null>(null);

  const apply = React.useCallback(([config, nextOverview]: [ConfigSnapshot, Overview]) => {
    setSnapshot(config);
    setOverview(nextOverview);
    setLoadError(null);
    // 第一次读到门禁状态时定下默认视图；之后一律认用户点过什么，
    // 免得在向导里保存完 Jellyfin（门禁刚打开）就被拽去面板。
    setRequestedView((current) => current ?? (config.initialized ? "panel" : "wizard"));
  }, []);

  const refresh = React.useCallback(async () => {
    try {
      apply(await fetchSettings());
    } catch (error) {
      setLoadError(error instanceof Error ? error.message : String(error));
    }
  }, [apply]);

  // 首次进入时把设置与概览拉一次；之后的刷新都由保存/测试按钮触发。
  React.useEffect(() => {
    let cancelled = false;
    fetchSettings().then(
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
                : "表格为主体，要动手的事置顶（任务域与告警区随后续版本点亮）。"}
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
          <Panel overview={overview} onOpenWizard={() => setRequestedView("wizard")} />
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

/** Panel 是面板骨架：统计卡与循环健康先照 §8.2 摆位，数据来自 /api/overview。 */
function Panel({ overview, onOpenWizard }: { overview: Overview | null; onOpenWizard: () => void }) {
  const counts = overview?.counts;
  const stats = [
    { label: "下载中", value: counts?.downloading ?? 0, hint: "已交给 qBittorrent、还没下完" },
    { label: "加工中", value: counts?.processing ?? 0, hint: "刮削与落盘进行中" },
    { label: "已入库", value: counts?.finished ?? 0, hint: "已硬链进媒体库" },
    { label: "需人工", value: counts?.needs_attention ?? 0, hint: "失败待重试与待重发" },
  ];

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
        <CardHeader>
          <CardTitle>循环健康</CardTitle>
          <CardDescription>
            三条循环（轮询 / 对账 / 副作用）各自单飞。任务表与「需要人工」告警区在任务域落地后点亮。
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
                    ? `${new Date(health.last_run_at).toLocaleString("zh-CN", { hour12: false })} · ${health.last_ok ? "成功" : "失败"}`
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
    </>
  );
}

async function fetchSettings(): Promise<[ConfigSnapshot, Overview]> {
  return Promise.all([api.config(), api.overview()]);
}

function loopLabel(snapshot: ConfigSnapshot | null, overview: Overview | null): string {
  if (!snapshot?.initialized) return "未启动";
  if (overview?.paused) return "已暂停";
  // 三条循环要到 #26 才接线：没有 last_run_at 就不假装「运行中」。
  const loops = overview ? Object.values(overview.loops) : [];
  return loops.some((loop) => loop?.last_run_at) ? "运行中" : "未启动";
}
