import * as React from "react";
import { Link, useLocation, useSearchParams } from "react-router";
import { LayoutDashboard, Loader2, Plug, Sparkles } from "lucide-react";
import { cn } from "cn";

import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Separator } from "~/components/ui/separator";
import { Notice } from "~/components/form";
import { Panel, isTaskState, loopLines, useNow, ROW_ACTION_SIZE, PANEL_ACTION_SIZE, type PanelQuery } from "~/components/panel";
import { attentionRowCount } from "~/lib/attention";
import { SECTION_ORDER, Wizard, isSectionName } from "~/routes/wizard";
import { durationSeconds, formatInterval } from "~/lib/time";
import {
  api,
  isGatewayAuthError,
  isGatewayBadError,
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
  // 侧栏与手机统计行都要相对时间，而它们挂在外壳上（不是 Panel 里）。
  const now = useNow();
  // 帮助浮层由外壳持有：侧栏那颗入口在 aside 里，浮层本身 portal 到 body（Panel 里渲染），
  // 两边都得够得着同一份开关。
  //
  // 开关存的是「开着时的地址」而不是布尔：换视图地址就变，浮层自然收起，不需要一个
  // 在 effect 里改状态的补丁（那会让渲染多跑一轮，也会把「地址是真相」这条破掉）。
  const [helpRoute, setHelpRoute] = React.useState<string | null>(null);
  const helpOpen = helpRoute === location.pathname;
  const setHelpOpen = React.useCallback(
    (open: boolean) => setHelpRoute(open ? location.pathname : null),
    [location.pathname],
  );

  // 面板的筛选与页号住在 URL 里：F5 不丢，还能把「只看失败」发给自己的另一个标签页。
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

  // 返回刷新后的快照：向导要靠它判断门禁（「完成初始化」按下去之后到底齐了没有）。
  // 读不到就返回 null，界面据此说「已保存但没读回来」，而不是假装成功。
  const refresh = React.useCallback(async (): Promise<ConfigSnapshot | null> => {
    try {
      const loaded = await fetchDashboard(query);
      apply(loaded);
      return loaded.config;
    } catch (error) {
      setLoadError(error);
      return null;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apply, queryKey]);

  // 面板与抽屉只关心「刷完了没有」：把返回值吞掉，别让它们跟着向导的契约走。
  const refreshOnly = React.useCallback(async () => {
    await refresh();
  }, [refresh]);

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
  // 比较的是**地址**（route.view），不是门禁加工后的 view：后者在 snapshot 为 null
  // 时把「/」算成 settings，数据一到又翻回 panel——一次多余的 refresh，
  // 冷加载变成完整两波请求（Playwright 网络轨迹实测过）。
  const previousRouteView = React.useRef<View | null>(null);
  React.useEffect(() => {
    const previous = previousRouteView.current;
    previousRouteView.current = route.view;
    if (previous === "settings" && route.view === "panel") void refresh();
  }, [route.view, refresh]);

  const loop = loopState(snapshot, overview);

  // 失败提示说清是哪一层的事，而且是哪一种病：应用自己说的错、网关要重登、
  // 还是网关/上游暂时没回话（后者重登没用，等一等才有意义）。
  const loadErrorText = loadError instanceof Error ? loadError.message : "";
  const gatewayExpired = isGatewayAuthError(loadError);
  const gatewayBad = !gatewayExpired && isGatewayBadError(loadError);

  // 三条循环的间隔读自 app_config；读不到就是 null，不编默认值。
  const appConfig = (snapshot?.sections?.app?.data ?? {}) as Record<string, unknown>;
  const intervals = {
    poll: formatInterval(appConfig.poll_interval),
    reconcile: formatInterval(appConfig.reconcile_interval),
    sidefx: formatInterval(appConfig.sidefx_interval),
  };
  // 自动复读的节奏跟清单轮询同拍：那是上游真正会变的频率，比它快只会读到同一份数据。
  // 读不到间隔就不自动读（宁可让人按 Shift+R）；下限 15s，免得一个写错的 app_config
  // 把面板变成连点器。
  const pollSeconds = durationSeconds(appConfig.poll_interval);
  const autoRefreshMs = pollSeconds === null ? null : Math.max(pollSeconds * 1000, 15_000);

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
      ? loadError
        ? "值班台暂时读不到"
        : "正在读取面板"
      : rows > 0
        ? `${rows}${cut ? "+" : ""} 件事需要人工`
        : "没有要动手的事"
    : !snapshot
      ? "正在读取设置"
      : initialized
        ? "集成设置"
        : "初始化向导";

  // 那个数是全页的标题，也是「刚才那一按成了没有」最直接的答案：由它自己开口。
  // 以前被播报的是卡片里那句 13px 构成，数反而静默。
  const announced = useAnnouncement(title, snapshot !== null);

  return (
    <div id="app-shell" className="mx-auto flex min-h-screen w-full max-w-6xl gap-6 p-6 text-foreground">
      <aside className="hidden w-52 shrink-0 md:flex">
        {/* 内层粘住、外层只占位：滚过第一屏之后导航与心跳还在，左栏不会变成一片白。 */}
        <div className="sticky top-6 flex h-[calc(100dvh-3rem)] w-full flex-col gap-6">
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

          {/* 心跳就在导航下面：它是「我要不要再等等」的答案，不是脚注。 */}
          <div className="mt-auto space-y-1.5 text-xs leading-relaxed text-muted-foreground">
            <Separator className="mb-2" />
            <div className="flex items-baseline justify-between gap-2">
              <span>循环</span>
              <span className={cn("tabular-nums", loop.failing && "text-status-danger-text")}>
                {loop.text}
              </span>
            </div>
            {/* 失败的那条循环排最前：侧栏这两行是「要不要再等」的答案，
                不能让恰好会失败的那条循环永远被 slice 掉。 */}
            {[...loopLines(overview, intervals, now)]
              .sort((a, b) => Number(b.failing) - Number(a.failing))
              .slice(0, 2)
              .map((line) => (
                <div key={line.label} className="flex items-baseline justify-between gap-2">
                  <span>{line.label}</span>
                  <span className={cn("tabular-nums", line.failing && "text-status-danger-text")}>
                    {line.text}
                  </span>
                </div>
              ))}
            {/* 帮助的一个入口（另一个在循环健康卡底部，手机就靠那颗）：
                侧栏本身 md 起才显示，所以这一颗仍然只是桌面。 */}
            {onPanel && initialized ? (
              <button
                type="button"
                onClick={() => setHelpOpen(true)}
                className="rounded-sm pt-1 text-left text-xs text-muted-foreground underline underline-offset-4 hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              >
                按 <span className="font-mono">?</span> 看快捷键与术语
              </button>
            ) : null}
          </div>
        </div>
      </aside>

      <main id="main" tabIndex={-1} className="flex min-w-0 flex-1 flex-col gap-6 outline-none">
        <div className="flex flex-col gap-3 md:hidden">
          <Link to="/" className="flex min-h-11 items-center gap-2">
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
            {/* 那个数自己开口：常驻活区，只在它真的变了的时候换字（首载不抢话）。 */}
            <p role="status" aria-live="polite" aria-atomic="true" className="sr-only">
              {announced}
            </p>
            {snapshot && view === "settings" ? (
              <Badge variant={initialized ? "secondary" : "outline"}>
                {initialized ? "已初始化" : "未初始化"}
              </Badge>
            ) : null}
            {/* 暂停是集成设置第 8 步里的一个开关：徽章自己不给按钮，但要给一条真链接，
                否则「已暂停」就是一句没有下文的坏消息。 */}
            {overview?.paused ? (
              <>
                <Badge variant="outline">已暂停</Badge>
                <Link
                  to="/settings/app"
                  title="暂停/恢复是集成设置里的一个开关"
                  className="rounded-sm text-sm text-muted-foreground underline underline-offset-4 hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                >
                  恢复循环
                </Link>
              </>
            ) : null}
          </div>
        </header>

        {/* 条子只在「手里已经有一份旧数据」时出现：没有数据的时候，正文那块
            「读不到」才是全部内容，不需要另一个人把同一句话说第二遍。 */}
        {loadError && snapshot ? (
          <Notice tone="bad" onClose={() => setLoadError(null)}>
            {gatewayExpired
              ? `网关拦下了这次请求：${loadErrorText}。登录后再刷新本页。`
              : gatewayBad
                ? `网关或上游暂时没回话：${loadErrorText}。屏幕上还是上一次的数据。稍等片刻，然后刷新页面。`
                : `${route.view === "panel" ? "刷新没成功" : "读不到设置"}：${loadErrorText}`}
            <span className="mt-1 flex flex-wrap items-center gap-2">
              {gatewayExpired || gatewayBad ? (
                <Button className={ROW_ACTION_SIZE} onClick={() => window.location.reload()}>
                  刷新页面
                </Button>
              ) : (
                <Button
                  variant="outline"
                  className={ROW_ACTION_SIZE}
                  onClick={() => void refresh()}
                >
                  重试
                </Button>
              )}
            </span>
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
              autoRefreshMs={autoRefreshMs}
              helpOpen={helpOpen}
              onHelpOpen={setHelpOpen}
              onQuery={writeQuery}
              onRefresh={refreshOnly}
            />
          ) : (
            <Wizard
              snapshot={snapshot}
              section={section}
              initialized={initialized}
              onRefresh={refresh}
            />
          )
        ) : loadError ? (
          <Unavailable
            scope={route.view === "panel" ? "面板" : "设置"}
            errorText={loadErrorText}
            gatewayKind={gatewayExpired ? "auth" : gatewayBad ? "bad" : null}
            onRetry={() => void refresh()}
          />
        ) : (
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
 * useAnnouncement 把「那个数变了」交给一个常驻的活区。
 *
 * 首载不出声（读屏已经会把标题念一遍），之后只在标题真的变了时换字。返回值始终是
 * 最后一条播报，活区就常驻在树上——带内容挂载的活区不会播报，这是这套东西的老坑。
 */
function useAnnouncement(text: string, ready: boolean): string {
  const [spoken, setSpoken] = React.useState("");
  const previous = React.useRef(text);
  React.useEffect(() => {
    if (!ready) return;
    if (previous.current === text) return;
    previous.current = text;
    setSpoken(text);
  }, [text, ready]);
  return spoken;
}

/**
 * Unavailable 是「这一屏读不到」的正式态，不是一条可以关掉的提示。
 *
 * 从前这里渲染 `null`：错误提示跟一个 `×`，关掉之后就只剩那句「正在读取…」，
 * 而取数 effect 不会再跑——人就被锁在永久的转圈里。现在它给完整的一句话、
 * 为什么，以及按病给的第一个动作。
 *
 * 网关的两种死法不再同诊：会话过期（登录页/401/403）教人重登；上游没回话
 * （502/503）教人稍等再刷新——重登是白跑一趟。安抚句也按面给：面板只读是事实，
 * 设置面恰恰可写，不能把同一句硬编码到两处。
 */
function Unavailable({
  scope,
  errorText,
  gatewayKind,
  onRetry,
}: {
  scope: string;
  errorText: string;
  gatewayKind: "auth" | "bad" | null;
  onRetry: () => void;
}) {
  const gateway = gatewayKind !== null;
  return (
    <section
      aria-labelledby="unavailable-title"
      className="rounded-xl bg-card p-6 ring-1 ring-foreground/10"
    >
      <h2 id="unavailable-title" className="font-heading text-base font-medium">
        读不到{scope}数据
      </h2>
      <p className="mt-1.5 dense leading-relaxed text-foreground">{errorText}</p>
      <p className="mt-1 dense leading-relaxed text-muted-foreground">
        {gatewayKind === "auth"
          ? "这个应用自己没有登录页：登录页与 401/403 只可能来自前面那层网关。登录然后刷新，本页会自己读到数据。"
          : gatewayKind === "bad"
            ? `网关或上游暂时没回话。稍等一下再刷新页面；会话没有失效，不需要重新登录。`
            : `数据库或上游暂时没回话。重试是安全的：${
                scope === "面板"
                  ? "面板只读，不会重复下发任何动作。"
                  : "这一步只是重新读取，不会改任何设置。"
              }`}
      </p>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        {gateway ? (
          <>
            <Button className={PANEL_ACTION_SIZE} onClick={() => window.location.reload()}>
              刷新页面
            </Button>
            <Button variant="outline" className={PANEL_ACTION_SIZE} onClick={onRetry}>
              再试一次
            </Button>
          </>
        ) : (
          <Button className={PANEL_ACTION_SIZE} onClick={onRetry}>
            重试
          </Button>
        )}
      </div>
    </section>
  );
}

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
  // 读不到 ≠ 没在跑。侧栏以前在 snapshot 为 null 时写「未启动」，把一次网关超时
  // 说成了「循环没开」，而值班台最不能做的就是说谎。
  if (!snapshot) return { text: "还没读到过", failing: false };
  if (!snapshot.initialized) return { text: "未启动", failing: false };
  if (overview?.paused) return { text: "已暂停", failing: false };
  const loops = overview ? Object.values(overview.loops) : [];
  if (!loops.some((item) => item?.last_run_at)) return { text: "未启动", failing: false };
  const failed = loops.filter((item) => item?.last_run_at && item.last_ok === false).length;
  return failed > 0
    ? { text: `运行中 · ${failed} 条失败`, failing: true }
    : { text: "运行中", failing: false };
}
