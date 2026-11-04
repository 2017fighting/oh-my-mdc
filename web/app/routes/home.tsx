import * as React from "react";
import { Link, useLocation, useSearchParams } from "react-router";
import { LayoutDashboard, Loader2, Plug, Sparkles } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Separator } from "~/components/ui/separator";
import { Notice } from "~/components/form";
import { Panel, isTaskState, type PanelQuery } from "~/components/panel";
import { attentionRowCount } from "~/lib/attention";
import { SECTION_ORDER, Wizard, isSectionName } from "~/routes/wizard";
import { formatInterval } from "~/lib/time";
import {
  api,
  isGatewayAuthError,
  type Attention,
  type ConfigSnapshot,
  type Overview,
  type SectionName,
  type SideEffect,
  type TaskRow,
} from "~/lib/api";

/**
 * 整站外壳（规格 §8.2/§8.3）：未初始化时只剩向导，面板入口被门禁挡住；
 * 已初始化时面板是默认视图，集成设置是可寻址的八节。
 *
 * 视图来自 pathname 而不是内存状态：`/` 是面板，`/settings/<section>` 是某一节。
 * 于是「去 Telegram 设置」是一条真链接——落到 Telegram 那一节、可书签、可刷新。
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

export default function Home() {
  const location = useLocation();
  const [params, setParams] = useSearchParams();

  const [snapshot, setSnapshot] = React.useState<ConfigSnapshot | null>(null);
  const [overview, setOverview] = React.useState<Overview | null>(null);
  const [tasks, setTasks] = React.useState<TaskRow[]>([]);
  const [total, setTotal] = React.useState(0);
  const [attention, setAttention] = React.useState<Attention | null>(null);
  const [sideEffects, setSideEffects] = React.useState<SideEffect[]>([]);
  const [pageSize, setPageSize] = React.useState(50);
  // 存错误本身而不是一句话：网关类失败（登录页、401/403）要拿它决定给什么动作。
  const [loadError, setLoadError] = React.useState<unknown | null>(null);
  // 上一次真正读到数据的时刻（客户端时钟）。值班台必须知道自己有多旧，
  // 否则「还没好」与「这是旧快照」在屏幕上长得一模一样。
  const [loadedAt, setLoadedAt] = React.useState<number | null>(null);

  // 面板的筛选与页号住在 URL 里：F5 不丢，还能把「只看需人工」发给自己的另一个标签页。
  const query = readQuery(params);
  const queryKey = `${query.page}\u0000${query.state}\u0000${query.q}`;

  const writeQuery = React.useCallback(
    (next: PanelQuery) => {
      const search = new URLSearchParams();
      if (next.q) search.set("q", next.q);
      if (next.state) search.set("state", next.state);
      if (next.page > 1) search.set("page", String(next.page));
      // replace：边打字边改地址栏，不该给每一击都留一条历史。
      setParams(search, { replace: true });
    },
    [setParams],
  );

  const apply = React.useCallback((loaded: Dashboard) => {
    setSnapshot(loaded.config);
    setOverview(loaded.overview);
    setTasks(loaded.tasks);
    setTotal(loaded.total);
    setAttention(loaded.attention);
    setSideEffects(loaded.sideEffects);
    setPageSize(loaded.pageSize);
    setLoadError(null);
    setLoadedAt(Date.now());
  }, []);

  // 首次进入、翻页、改筛选时拉一次；保存/测试/刷新按钮另有各自的入口。
  //
  // 这里必须用 try/catch 包住 apply：以前是 `.then(loaded => apply(loaded), onError)`，
  // 而 onError 只接**请求**的失败，接不住 apply 自己抛的错（回包形状不对时）。
  // 后果是 snapshot 与 loadError 都停在 null，页面永远“正在读取设置…”。
  React.useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const loaded = await fetchDashboard(query);
        if (!cancelled) apply(loaded);
      } catch (error: unknown) {
        if (!cancelled) setLoadError(error);
      }
    })();
    return () => {
      cancelled = true;
    };
    // query 每次渲染都是新对象，用它的三要素当键。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apply, queryKey]);

  const refresh = React.useCallback(async () => {
    try {
      apply(await fetchDashboard(query));
    } catch (error) {
      setLoadError(error);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apply, queryKey]);

  // 页号越界（删掉了最后一页的全部任务、或筛选后总数变少）时退到最后一页，
  // 不让空页把人留在空白表格上。
  React.useEffect(() => {
    if (tasks.length > 0 || total === 0 || query.page <= 1) return;
    const lastPage = Math.max(1, Math.ceil(total / pageSize));
    if (lastPage !== query.page) writeQuery({ ...query, page: lastPage });
  }, [tasks.length, total, pageSize, query, writeQuery]);

  const initialized = snapshot?.initialized ?? false;

  // 未初始化＝整站只剩向导，不认任何想进面板的意图；已初始化则认地址栏。
  const route = parseRoute(location.pathname);
  const view: View = initialized && route.view === "panel" ? "panel" : "settings";
  const section = route.section ?? SECTION_ORDER[0];

  // 从设置回到面板时重读一次：改完 Telegram 再回来，得看到改完之后的队列，
  // 而不是离开前的快照。首次挂载不重复拉（上面那个 effect 已经拉过了）。
  const previousView = React.useRef<View | null>(null);
  React.useEffect(() => {
    const previous = previousView.current;
    previousView.current = view;
    if (previous === "settings" && view === "panel") void refresh();
  }, [view, refresh]);

  const loop = loopState(snapshot, overview);

  // 失败提示说清是哪一层的事：应用自己说的错，还是前面那层网关拦下的。
  const loadErrorText = loadError instanceof Error ? loadError.message : "";
  const gatewayExpired = isGatewayAuthError(loadError);

  // 三条循环的间隔读自 app_config；读不到就是 null，不编默认值。
  const appConfig = (snapshot?.sections?.app?.data ?? {}) as Record<string, unknown>;
  const intervals = {
    poll: formatInterval(appConfig.poll_interval),
    reconcile: formatInterval(appConfig.reconcile_interval),
    sidefx: formatInterval(appConfig.sidefx_interval),
  };

  // 标题只描述**地址**，不预判门禁：读到 snapshot 之前不说「未初始化」。
  // 标题、导航高亮与加载文案读**地址**（route.view），只有未初始化才被门禁改写：
  // 刚打开 / 的那一刻 snapshot 还没回来，view 会短暂算成 settings，于是面板写着
  // 「集成设置」、导航亮着另一边，而屏幕上其实什么都没有。
  const onPanel = route.view === "panel" && (snapshot ? initialized : true);
  // 面板的标题就是那个数：首屏第一眼要回答的是「还有几件事」，不是一个叫「面板」的词。
  // 全站最大的字留在这里，卡片里的说明只负责把它拆开（失败几部 / 停滞几部 / 驻留几条）。
  const rows = attentionRowCount(attention, sideEffects);
  const cut = Boolean(attention?.failed_truncated || attention?.stalled_truncated);
  const title = onPanel
    ? !snapshot
      ? "面板"
      : rows > 0
        ? `${rows}${cut ? "+" : ""} 件事需要人工`
        : "没有要动手的事"
    : !snapshot
      ? "集成设置"
      : initialized
        ? "集成设置"
        : "初始化向导";

  return (
    <div id="app-shell" className="mx-auto flex min-h-screen w-full max-w-6xl gap-6 p-6 text-foreground">
      <aside className="hidden w-52 shrink-0 flex-col gap-6 md:flex">
        <Link to="/" className="flex items-center gap-2 rounded-lg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
          <Sparkles className="size-5 text-primary" aria-hidden />
          <span className="font-heading text-base font-semibold tracking-tight">oh-my-mdc</span>
        </Link>

        <nav className="flex flex-col gap-1 text-sm" aria-label="主导航">
          <NavItem
            to="/"
            icon={<LayoutDashboard className="size-4" aria-hidden />}
            label="面板"
            current={onPanel}
            disabled={!initialized}
            title={initialized ? undefined : "主线三件套不齐，面板进不去"}
          />
          <NavItem
            to={`/settings/${section}`}
            icon={<Plug className="size-4" aria-hidden />}
            label="集成设置"
            current={!onPanel}
            title={initialized ? "八节各自一个地址" : "初始化前这里是向导"}
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
          <Link to="/" className="flex items-center gap-2">
            <Sparkles className="size-5 text-primary" aria-hidden />
            <span className="font-heading text-base font-semibold tracking-tight">oh-my-mdc</span>
          </Link>
          <nav
            aria-label="视图切换"
            className="grid grid-cols-2 gap-1 rounded-lg bg-background p-1 ring-1 ring-foreground/10"
          >
            <MobileNavItem
              to="/"
              icon={<LayoutDashboard className="size-4" aria-hidden />}
              label="面板"
              current={onPanel}
              disabled={!initialized}
              title={initialized ? undefined : "主线三件套不齐，面板进不去"}
            />
            <MobileNavItem
              to={`/settings/${section}`}
              icon={<Plug className="size-4" aria-hidden />}
              label="集成设置"
              current={!onPanel}
            />
          </nav>
        </div>

        <header>
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <h1 className="font-heading text-2xl font-semibold tracking-tight">{title}</h1>
            {snapshot && view === "settings" ? (
              <Badge variant={initialized ? "secondary" : "outline"}>
                {initialized ? "已初始化" : "未初始化"}
              </Badge>
            ) : null}
            {overview?.paused ? <Badge variant="outline">已暂停</Badge> : null}
          </div>
        </header>

        {loadError ? (
          <Notice tone="bad" onClose={() => setLoadError(null)}>
            {gatewayExpired
              ? `网关拦下了这次请求：${loadErrorText}。登录后再刷新本页。`
              : `${route.view === "panel" ? "读不到面板数据" : "读不到设置"}：${loadErrorText}`}
            <button
              type="button"
              className="ml-2 underline underline-offset-4"
              onClick={() => void refresh()}
            >
              重试
            </button>
            {gatewayExpired ? (
              <button
                type="button"
                className="ml-2 underline underline-offset-4"
                onClick={() => window.location.reload()}
              >
                刷新页面
              </button>
            ) : null}
          </Notice>
        ) : null}

        {snapshot ? (
          view === "panel" ? (
            <Panel
              overview={overview}
              tasks={tasks}
              total={total}
              attention={attention}
              sideEffects={sideEffects}
              query={query}
              pageSize={pageSize}
              loadedAt={loadedAt}
              intervals={intervals}
              onQuery={writeQuery}
              onRefresh={refresh}
            />
          ) : (
            <Wizard
              snapshot={snapshot}
              section={section}
              initialized={initialized}
              onRefresh={refresh}
            />
          )
        ) : loadError ? null : (
          <div role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" aria-hidden />
            {route.view === "panel" ? "正在读取面板数据…" : "正在读取设置…"}
          </div>
        )}
      </main>
    </div>
  );
}

type View = "panel" | "settings";

/**
 * parseRoute 把 pathname 翻成视图与节。
 *
 * 认不出的地址一律回面板（已初始化时）；未初始化时调用方会把它按向导渲染。
 */
function parseRoute(pathname: string): { view: View; section: SectionName | null } {
  const match = /^\/settings(?:\/([a-z_]+))?\/?$/.exec(pathname);
  if (!match) return { view: "panel", section: null };
  const candidate = match[1];
  return { view: "settings", section: isSectionName(candidate) ? candidate : null };
}

/**
 * readQuery 把地址栏里的筛选读成面板查询。认不出的值一律回落默认，
 * 不因为一条手改的 URL 就把整页打成错误。
 */
function readQuery(params: URLSearchParams): PanelQuery {
  const page = Number(params.get("page") ?? "1");
  const state = params.get("state") ?? "";
  return {
    page: Number.isInteger(page) && page > 0 ? page : 1,
    q: params.get("q") ?? "",
    state: isTaskState(state) ? state : "",
  };
}

function NavItem({
  to,
  icon,
  label,
  current,
  disabled,
  title,
}: {
  to: string;
  icon: React.ReactNode;
  label: string;
  current: boolean;
  disabled?: boolean;
  title?: string;
}) {
  const className = cn(
    "flex items-center gap-2 rounded-lg px-3 py-2 text-left transition-colors",
    "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
    current
      ? "bg-background font-medium ring-1 ring-foreground/10"
      : "text-muted-foreground hover:bg-background/60",
    disabled && "cursor-not-allowed opacity-60",
  );

  // 禁用态不装成链接：读屏与键盘都不该走进去，但 title 仍说清为什么进不去。
  if (disabled) {
    return (
      <span aria-disabled="true" title={title} className={className}>
        {icon}
        {label}
      </span>
    );
  }
  return (
    <Link to={to} title={title} aria-current={current ? "page" : undefined} className={className}>
      {icon}
      {label}
    </Link>
  );
}

/** MobileNavItem 是窄屏的两格视图切换：44px 高，拇指够得着，不和桌面侧栏共用一套尺寸。 */
function MobileNavItem({
  to,
  icon,
  label,
  current,
  disabled,
  title,
}: {
  to: string;
  icon: React.ReactNode;
  label: string;
  current: boolean;
  disabled?: boolean;
  title?: string;
}) {
  const className = cn(
    "flex h-11 items-center justify-center gap-2 rounded-md text-sm transition-colors",
    "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
    current ? "bg-secondary font-medium text-secondary-foreground" : "text-muted-foreground",
    disabled && "cursor-not-allowed opacity-60",
  );

  if (disabled) {
    return (
      <span aria-disabled="true" title={title} className={className}>
        {icon}
        {label}
      </span>
    );
  }
  return (
    <Link to={to} title={title} aria-current={current ? "page" : undefined} className={className}>
      {icon}
      {label}
    </Link>
  );
}

/** Dashboard 是一次面板读取的全部输入（设置、概览、任务、「需要人工」区、副作用队列、分页）。 */
type Dashboard = {
  config: ConfigSnapshot;
  overview: Overview;
  tasks: TaskRow[];
  total: number;
  attention: Attention;
  sideEffects: SideEffect[];
  page: number;
  pageSize: number;
};

async function fetchDashboard(query: PanelQuery): Promise<Dashboard> {
  const [config, overview, taskList, attention, sideEffects] = await Promise.all([
    api.config(),
    api.overview(),
    api.tasks({ page: query.page, q: query.q || undefined, state: query.state || undefined }),
    api.attention(),
    api.sideEffects(),
  ]);
  return {
    config,
    overview,
    tasks: taskList.tasks,
    total: taskList.total,
    attention,
    sideEffects: sideEffects.side_effects,
    page: taskList.page,
    pageSize: taskList.page_size,
  };
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
