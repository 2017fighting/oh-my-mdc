/**
 * 设置域与概览的 API 客户端（规格 §8.4）。
 *
 * 全部 JSON；错误统一是 {ok:false, error:{code, message}}。这里只做一件事：
 * 把 HTTP 形状翻成 TypeScript 类型，把非 2xx 变成 HttpError，别处不再手工拼 URL。
 */

export type SectionName =
  | "javdb"
  | "list_registry"
  | "qbittorrent"
  | "jellyfin"
  | "mteam"
  | "ai_translation"
  | "telegram"
  | "app";

/** 敏感字段对外的唯一形态：有没有值 + 一段掩码，原文永不出现在前端。 */
export type Masked = {
  configured: boolean;
  masked: string;
};

export type LastTest = {
  at: string;
  ok: boolean;
  error?: string;
};

export type ListEntry = {
  /** JavDB 清单 id：实测是 6 字符 nanoid 字符串（#16），不是整数。 */
  list_id: string;
  purpose: "want_watch" | "want_watch_zh";
  name: string;
};

export type SectionState = {
  configured: boolean;
  enabled?: boolean;
  revision: string;
  data: Record<string, unknown>;
  last_test?: LastTest | null;
};

export type ConfigSnapshot = {
  ok: boolean;
  initialized: boolean;
  /** 门禁的第二半：媒体库目录在这台机器上可见（P4-2）。 */
  media_root_visible: boolean;
  sections: Record<SectionName, SectionState>;
};

export type TestResult = {
  /** 测试结论，不是 HTTP 信封：false 时看 code。 */
  ok: boolean;
  code?: string;
  detail?: string;
  duration_ms: number;
};

/**
 * 通知事件的键（规格 §5.6）。设置里的四个布尔开关与它对上：
 * 事件名同时是 side_effect 的 target_key 前缀（"<event>:<infohash>"）。
 */
export type NotifyEvent =
  | "download_started"
  | "filed"
  | "processing_failed"
  | "download_stalled";

/** 事件 → 开关列：侧效应队列与「需要人工」区据此说清「哪个通知没发出去」。 */
export const notifyEventLabels: Record<NotifyEvent, string> = {
  download_started: "下载开始",
  filed: "入库完成",
  processing_failed: "加工失败",
  download_stalled: "下载停滞",
};

/** target_key 的第一段就是事件名（filed:<infohash>）；不认识的事件名回空串。 */
export function notifyEventOf(targetKey: string): NotifyEvent | "" {
  const event = targetKey.split(":")[0];
  // Object.hasOwn 而不是 `in`：后者会认 "constructor" / "toString" 这类原型键，
  // 于是一个脏 target_key 会渲染成「发 Telegram · undefined」。
  return Object.hasOwn(notifyEventLabels, event) ? (event as NotifyEvent) : "";
}

/** JavDB 清单探测结果里的一张清单：id 是身份，name 只是快照。 */
export type JavDBList = {
  id: string;
  name: string;
  is_default: boolean;
  movies_count: number;
};

export type Overview = {
  ok: boolean;
  initialized: boolean;
  paused: boolean;
  counts: {
    downloading: number;
    processing: number;
    finished: number;
    /** 全部版本都失败的任务数：与 `?state=failed` 同一口径，不等于「需要人工」的行数。 */
    failed: number;
  };
  loops: Record<
    "poll" | "reconcile" | "sidefx",
    {
      last_run_at: string | null;
      last_ok: boolean | null;
      /** 最近一次成功的时间：连败判据（LoopStalled 看它，不看 last_run）。 */
      last_success_at: string | null;
      /** 本轮起跑时间；null = 没在跑。进程内状态，重启即「还没跑过」。 */
      running_since: string | null;
      /** 本进程累计迭代；门禁没开、抢锁抢输的轮次不算。 */
      iterations: { ok: number; error: number };
    }
  >;
};

/** 版本状态：四个值是规格 §3 的完整集合（任务级状态由视图聚合，名字相同）。 */
export type TaskState = "downloading" | "processing" | "finished" | "failed";

/** 一条版本（面板 chip 与抽屉列表）。 */
export type TaskVersion = {
  infohash: string;
  source: "javdb_magnet" | "mteam_torrent";
  delivery: "magnet" | "torrent";
  /** best-effort 展示标签（如「中文」），空串表示没有可展示的标签。 */
  label: string;
  status: TaskState;
  /** 失败发生在哪一步（仅 failed 时有值）。 */
  error_phase?: "downloading" | "scrape" | "organize";
  error_reason?: string;
  /** 已落库、还没加种（崩溃自愈的中间态）。 */
  add_pending: boolean;
  /** 加种成功后置起，等旁路循环删清单项。 */
  list_removal_pending: boolean;
  /** 下一次取数不许复用元数据（人工「强制重刮」置起，取数成功后清掉）。 */
  force_rescrape: boolean;
  attempts: number;
  created_at: string;
  updated_at: string;
};

/** 任务行：一行一任务，版本以小 chip 展示。 */
export type TaskRow = {
  movie_id: string;
  number: string;
  list_id: string;
  /** 空串表示这个任务还没有版本（正常流程不会出现）。 */
  state: TaskState | "";
  versions: TaskVersion[];
  created_at: string;
  updated_at: string;
};

export type TaskEvent = {
  id: number;
  movie_id: string;
  infohash?: string;
  kind: "state_change" | "side_effect" | "alert" | "manual_retry";
  from_state?: string;
  to_state?: string;
  phase?: string;
  reason: string;
  /** 结构化附注：人工重试把旧 raw_snapshot 的对比基准留在这里（规格 P13-3）。 */
  detail?: Record<string, unknown>;
  at: string;
};

export type TaskList = {
  ok: boolean;
  tasks: TaskRow[];
  page: number;
  /** 同筛选条件下的总条数：面板说「共 N 条」并关掉最后一页的「下一页」。 */
  total: number;
  page_size: number;
};

export type TaskDetail = {
  ok: boolean;
  task: TaskRow;
  events: TaskEvent[];
};

/** 一次重试对一条版本的结局（规格 §6.1 人工重试行）。 */
export type TaskRetryAction = "reset_processing" | "readded" | "skipped" | "failed";

export type TaskRetryOutcome = {
  infohash: string;
  action: TaskRetryAction;
  from_state: TaskState;
  to_state: TaskState;
  /** 给人看的一句话：被拒、加种失败、重新加种成功都在这里说清。 */
  reason: string;
  /** 下一次取数不复用元数据（强制重刮）。 */
  force_rescrape: boolean;
};

/** 逐版结果：一条任务可以有多条版本，各走各的分支。 */
export type TaskRetryResult = {
  ok: boolean;
  movie_id: string;
  force: boolean;
  results: TaskRetryOutcome[];
};

/** 副作用的三种 kind（规格 §4.2）。 */
export type SideEffectKind = "list_removal" | "jellyfin_refresh" | "notify";

/** 副作用队列的两个筛选（规格 §8.4）：pending = 排队中，stuck = 驻留（需人工）。 */
export type SideEffectState = "pending" | "stuck";

/** 一条待办：面板的「需要人工」区与底部副作用队列读它。 */
export type SideEffect = {
  id: number;
  kind: SideEffectKind;
  /** list_removal 是 "<list_id>:<movie_id>"，jellyfin_refresh 是 movie_id，notify 是 "<event>:<infohash>"。 */
  target_key: string;
  /** 入箱时写的上下文（番号/标签/清单 id）：面板用它说清这一条在做什么。 */
  payload: Record<string, unknown>;
  attempts: number;
  last_error?: string;
  next_attempt_at: string | null;
  /** 驻留＝需人工（done_at IS NULL 且 next_attempt_at 为 NULL）。 */
  stuck: boolean;
};

export type SideEffectList = {
  ok: boolean;
  side_effects: SideEffect[];
};

/** 「需要人工」区的一行（任务侧）：失败（phase + reason）或停滞。 */
export type AttentionItem = {
  movie_id: string;
  number: string;
  infohash: string;
  /** 版本的来源与标签：一部作品两个版本同时出事时，行上要能分辨是哪一条。 */
  source: TaskVersion["source"];
  label: string;
  /** 仅失败行有值：失败发生在哪一步。 */
  phase?: "downloading" | "scrape" | "organize";
  reason: string;
  at: string;
};

export type Attention = {
  ok: boolean;
  failed: AttentionItem[];
  stalled: AttentionItem[];
  /** 每半上限 50 行；被截断时面板写「50+」，不把 50 说成总量。 */
  failed_truncated: boolean;
  stalled_truncated: boolean;
};

/** autorun 改写的前后对比：GET 只读预览，POST 是人工确认后的落地。 */
export type AutorunPlan = {
  ok: boolean;
  /** 完整命令串，不截断：向导要原样给人对比。 */
  before: string;
  after: string;
  /** 现值已含本项目回调 → 跳过不改（幂等）。 */
  skip: boolean;
  changed: boolean;
  /** qbt 的 autorun 开关现值：关着的话命令写了也不会跑。 */
  enabled: boolean;
  warning?: string;
  applied: boolean;
};

export class HttpError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "HttpError";
  }

  get isRevisionConflict(): boolean {
    return this.code === "revision_conflict";
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });

  const body = (await response.json().catch(() => null)) as
    | { error?: { code?: string; message?: string } }
    | null;

  // 非 JSON 只可能是中间层接的：但「哪一种病」要分开诊——
  // 200 + 登录页是 authelia 的会话过期，治法是重登后刷新；
  // 非 200（502/503）是网关或上游暂时没回话，重登没用，等一等再刷新。
  // 以前两者同诊（gateway_html），于是上游 502 被教去登录，白白重登一次。
  if (!body) {
    throw new HttpError(
      response.status,
      response.ok ? "gateway_auth" : "gateway_bad",
      response.ok
        ? "网关返回的是登录页，不是数据"
        : `网关返回了 HTTP ${response.status}，不是数据`,
    );
  }

  if (!response.ok) {
    throw new HttpError(
      response.status,
      body.error?.code ?? `http_${response.status}`,
      body.error?.message ?? `请求失败（HTTP ${response.status}）`,
    );
  }
  return body as T;
}

/**
 * isGatewayAuthError 认出「不是应用说的，是前面那层网关说的」且要重登的那一类失败。
 *
 * 应用层没有鉴权（长期约束），登录页（200 + HTML）与 401/403 只可能来自网关。这类失败
 * 唯一有意义的动作是重登，不能靠重试同一个请求。
 */
export function isGatewayAuthError(error: unknown): boolean {
  if (!(error instanceof HttpError)) return false;
  return error.code === "gateway_auth" || error.status === 401 || error.status === 403;
}

/**
 * isGatewayBadError 认出网关的另一种死法：上游没回话（502/503/504）或网关回了非 JSON。
 *
 * 应用自己只会说 200/400/429/500（JSON 信封），这三档状态码只可能出自中间层。
 * 治法与登录页相反：会话没失效，重登是白跑一趟，稍等再刷新才有意义。
 */
export function isGatewayBadError(error: unknown): boolean {
  if (!(error instanceof HttpError)) return false;
  return (
    error.code === "gateway_bad" ||
    error.status === 502 ||
    error.status === 503 ||
    error.status === 504
  );
}

export const api = {
  config: () => request<ConfigSnapshot>("/api/config"),

  /** 部分字段 + revision：省略＝不改，敏感字段空串＝不改。 */
  save: (name: SectionName, patch: Record<string, unknown>) =>
    request<SectionState>(`/api/config/${name}`, {
      method: "PATCH",
      body: JSON.stringify(patch),
    }),

  test: (name: SectionName) =>
    request<TestResult>(`/api/config/${name}/test`, { method: "POST" }),

  overview: () => request<Overview>("/api/overview"),

  /** 面板任务表：一行一任务，带版本摘要（规格 §8.4）。 */
  tasks: (params: { state?: TaskState; q?: string; page?: number } = {}) => {
    const search = new URLSearchParams();
    if (params.state) search.set("state", params.state);
    if (params.q) search.set("q", params.q);
    if (params.page) search.set("page", String(params.page));
    const query = search.toString();
    return request<TaskList>(`/api/tasks${query ? `?${query}` : ""}`);
  },

  /** 抽屉数据：任务 + 版本列表 + 最近事件。 */
  task: (movieID: string) => request<TaskDetail>(`/api/tasks/${encodeURIComponent(movieID)}`),

  /**
   * 人工重试（规格 §6.1 人工重试行）：force=false 是「重试」，force=true 是「强制重刮」。
   * 版本侧的结局（被拒、加种失败）是 200 里的逐版结果，不是 HTTP 错误。
   */
  /**
   * 重试 / 强制重刮。infohashes 为空就是整个任务（抽屉用），
   * 给了就只动这几条（面板的行是版本组，按钮写几条就只动几条）。
   */
  retryTask: (movieID: string, force = false, infohashes?: string[]) =>
    request<TaskRetryResult>(`/api/tasks/${encodeURIComponent(movieID)}/retry`, {
      method: "POST",
      body: JSON.stringify(infohashes?.length ? { force, infohashes } : { force }),
    }),

  /** 「需要人工」区的任务侧两半：失败版本与停滞下载（规格 §8.2）。 */
  attention: () => request<Attention>("/api/attention"),

  /** 副作用队列；不传 state 即全部未完成（规格 §8.4）。 */
  sideEffects: (state?: SideEffectState) =>
    request<SideEffectList>(`/api/side-effects${state ? `?state=${state}` : ""}`),

  /** 人工重发：清驻留、重排；执行仍归 sidefx 循环。 */
  retrySideEffect: (id: number) =>
    request<{ ok: boolean; side_effect: SideEffect }>(`/api/side-effects/${id}/retry`, {
      method: "POST",
    }),

  /** 探测用户自建的清单（向导「读取已有清单」）。 */
  javdbLists: () => request<{ ok: boolean; lists: JavDBList[] }>("/api/javdb/lists"),

  /** 读 qbt 的 autorun 现值并算出共存改写（不改上游）。 */
  qbtAutorunPlan: () => request<AutorunPlan>("/api/qbittorrent/autorun"),

  /** 确认后把改写落到 qbt：只写 autorun_program。 */
  qbtAutorunApply: () =>
    request<AutorunPlan>("/api/qbittorrent/autorun", { method: "POST" }),

  /** 代建一张清单（向导「一键创建」），返回新清单 id。 */
  javdbCreateList: (name: string, description?: string) =>
    request<{ ok: boolean; id: string; name: string }>("/api/javdb/lists", {
      method: "POST",
      body: JSON.stringify({ name, description }),
    }),
};
