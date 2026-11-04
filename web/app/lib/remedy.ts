import type { AttentionItem, SideEffect } from "./api";

/**
 * 「需要人工」每一行该给什么动作。
 *
 * 这个模块的唯一职责：判断这一行的病要靠什么治——流水线重试、改集成、去上游、
 * 上宿主机、还是去 qBittorrent 看一眼。面板据此决定给不给按钮、给哪个按钮，
 * 以及那句「为什么重试不会好」要怎么说。
 *
 * 判据只用两样东西：规格里稳定的错误类（永久错 400/401/403 立即驻留，§4.2），
 * 和上游原话里能认出的少数几种病因（errno、同名清单、token 失效）。认不出来一律
 * 回落到「重试」——猜错方向的提示比没有提示更坏。
 */

export type RemedyKind =
  /** 流水线重试就有意义：失败、停滞、退避耗尽。 */
  | "retry"
  /** 要改集成设置里的某一节，改完再重发。 */
  | "settings"
  /** 要在 JavDB 那边处理（同名清单、清单已删），处理完再重发。 */
  | "upstream"
  /** 宿主机上的权限/空间问题，面板上的按钮解决不了。 */
  | "host"
  /** qBittorrent 里还在下，本项目永不重下。 */
  | "qbt";

export type Remedy = {
  kind: RemedyKind;
  /** 一行话：说清病因与下一步。空串表示失败原因本身已经说完了。 */
  hint: string;
  /** `settings` 时指明是哪一节，用于「去集成设置 → Telegram」这类文案。 */
  settings?: "telegram" | "jellyfin" | "javdb";
  /** 面板行里还给不给重试按钮。宿主机问题不给。 */
  retryable: boolean;
};

const HOST_FAILURE =
  /permission denied|operation not permitted|read-?only file ?system|no space left|disk quota|eacces|erofs|enospc/i;

const SAME_LIST = /同名|already exists|already created|已存在|已有同名/i;

/** 从「Telegram 403：…」这类上游原话里抠出 HTTP 状态码；抠不到返回 null。 */
export function statusCodeOf(message: string | undefined | null): number | null {
  if (!message) return null;
  const match = /\b([45]\d\d)\b/.exec(message);
  return match ? Number(match[1]) : null;
}

/** 永久错按规格是「不重试、立即驻留」，所以这一类绝不能只给一个重发按钮。 */
function isPermanent(status: number | null): boolean {
  return status === 400 || status === 401 || status === 403 || status === 404;
}

/** 失败的任务：宿主机病因让重试变成摆设，其余一律回流水线。 */
export function remedyForFailure(item: AttentionItem): Remedy {
  if (HOST_FAILURE.test(item.reason)) {
    return {
      kind: "host",
      hint: `重试不会变好：这是宿主机上的问题（目录权限、只读挂载或空间）。在宿主上处理 ${
        extractPath(item.reason) ?? "/media"
      } 之后再回来。`,
      retryable: false,
    };
  }
  return { kind: "retry", hint: "", retryable: true };
}

/** 停滞：qbt 里还在下就不会重下，按钮只是「让流水线再对一次账」。 */
export function remedyForStalled(): Remedy {
  return {
    kind: "qbt",
    hint: "先在 qBittorrent 里确认种子还在下；确认丢了种，重试才会重新加种。",
    retryable: true,
  };
}

/** 驻留副作用：按 kind 找它缺的那一样东西。 */
export function remedyForSideEffect(effect: SideEffect): Remedy {
  const status = statusCodeOf(effect.last_error);

  switch (effect.kind) {
    case "notify":
      if (isPermanent(status)) {
        return {
          kind: "settings",
          settings: "telegram",
          hint: "去 Telegram 设置改掉 token 或 chat id，再回来重发。",
          retryable: true,
        };
      }
      return {
        kind: "retry",
        hint: "自动重试已用完；网络恢复后重发即可。",
        retryable: true,
      };

    case "jellyfin_refresh":
      if (isPermanent(status)) {
        return {
          kind: "settings",
          settings: "jellyfin",
          hint: "去 Jellyfin 设置核对 API key 与媒体库路径，再回来重发。",
          retryable: true,
        };
      }
      return {
        kind: "retry",
        hint: "自动重试已用完；Jellyfin 回来后重发即可。",
        retryable: true,
      };

    case "list_removal":
      if (SAME_LIST.test(effect.last_error ?? "")) {
        return {
          kind: "upstream",
          hint: "JavDB 那边有同名清单：在 JavDB App 里删掉或改掉同名清单，再回来重发。",
          retryable: true,
        };
      }
      if (isPermanent(status)) {
        return {
          kind: "settings",
          settings: "javdb",
          hint: "JavDB token 多半已失效：去集成设置换 token，再回来重发。",
          retryable: true,
        };
      }
      return {
        kind: "retry",
        hint: "自动重试已用完；JavDB 回来后重发即可。",
        retryable: true,
      };

    default:
      return { kind: "retry", hint: "", retryable: true };
  }
}

/** 集成设置里那一节的中文名，拼「去集成设置 → Telegram」用。 */
export function settingsLabel(section: Remedy["settings"]): string {
  switch (section) {
    case "telegram":
      return "Telegram";
    case "jellyfin":
      return "Jellyfin";
    case "javdb":
      return "JavDB";
    default:
      return "集成设置";
  }
}

/** 从上游原话里挑出出错的那条路径，拼提示用；认不出就返回 null。 */
function extractPath(message: string): string | null {
  const match = /(\/[^\s:()]+(?:\/[^\s:()]+)*)/.exec(message);
  return match ? match[1] : null;
}
