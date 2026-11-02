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
    needs_attention: number;
  };
  loops: Record<
    "poll" | "reconcile" | "sidefx",
    { last_run_at: string | null; last_ok: boolean | null }
  >;
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

  if (!response.ok) {
    throw new HttpError(
      response.status,
      body?.error?.code ?? `http_${response.status}`,
      body?.error?.message ?? `请求失败（HTTP ${response.status}）`,
    );
  }
  return body as T;
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
