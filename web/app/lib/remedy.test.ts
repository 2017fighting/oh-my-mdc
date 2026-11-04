import assert from "node:assert/strict";
import test from "node:test";

import { remedyForFailure, remedyForSideEffect, remedyForStalled, statusCodeOf } from "./remedy.ts";
import type { AttentionItem, SideEffect } from "./api";

const failed = (reason: string, phase: AttentionItem["phase"] = "organize"): AttentionItem => ({
  movie_id: "Smk1Q4",
  number: "MIDE-872",
  infohash: "1".repeat(40),
  source: "javdb_magnet",
  label: "中文",
  phase,
  reason,
  at: "2026-11-04T14:00:00Z",
});

const effect = (over: Partial<SideEffect>): SideEffect => ({
  id: 1,
  kind: "notify",
  target_key: "filed:abc",
  payload: {},
  attempts: 5,
  next_attempt_at: null,
  stuck: true,
  ...over,
});

test("上游原话里的状态码能被认出来", () => {
  assert.equal(statusCodeOf("Telegram 403：bot 不在该 chat 里"), 403);
  assert.equal(statusCodeOf("超时"), null);
  assert.equal(statusCodeOf(undefined), null);
});

test("宿主机权限失败不给重试：这个按钮注定再失败一次", () => {
  const remedy = remedyForFailure(failed("媒体库目录不可写：permission denied (/media/movies/MIDE-872)"));
  assert.equal(remedy.kind, "host");
  assert.equal(remedy.retryable, false);
  assert.match(remedy.hint, /\/media\/movies\/MIDE-872/);
});

test("认不出的失败回落到重试，不瞎指方向", () => {
  const remedy = remedyForFailure(failed("取数时 DMM 返回了空响应"));
  assert.equal(remedy.kind, "retry");
  assert.equal(remedy.retryable, true);
  assert.equal(remedy.hint, "");
});

test("通知被 Telegram 永久拒收 → 去 Telegram 设置", () => {
  const remedy = remedyForSideEffect(effect({ kind: "notify", last_error: "Telegram 403：bot 不在该 chat 里" }));
  assert.equal(remedy.kind, "settings");
  assert.equal(remedy.settings, "telegram");
});

test("通知是退避耗尽（超时）→ 只该重发", () => {
  const remedy = remedyForSideEffect(effect({ kind: "notify", last_error: "上下文超时" }));
  assert.equal(remedy.kind, "retry");
});

test("JavDB 同名清单是上游业务错：要去 JavDB 处理，不是改设置", () => {
  const remedy = remedyForSideEffect(
    effect({ kind: "list_removal", target_key: "wAnT01:Smk1Q5", last_error: "JavDB「您已創建了同名清單」" }),
  );
  assert.equal(remedy.kind, "upstream");
  assert.equal(remedy.retryable, true);
});

test("删清单项 401 → token 失效，去 JavDB 设置", () => {
  const remedy = remedyForSideEffect(
    effect({ kind: "list_removal", target_key: "wAnT01:Smk1Q5", last_error: "JavDB 401 unauthorized" }),
  );
  assert.equal(remedy.kind, "settings");
  assert.equal(remedy.settings, "javdb");
});

test("刷 Jellyfin 403 → 去 Jellyfin 设置", () => {
  const remedy = remedyForSideEffect(effect({ kind: "jellyfin_refresh", last_error: "Jellyfin 403 Forbidden" }));
  assert.equal(remedy.kind, "settings");
  assert.equal(remedy.settings, "jellyfin");
});

test("停滞提示 qbt 里还在下就不会重下", () => {
  const remedy = remedyForStalled();
  assert.equal(remedy.kind, "qbt");
  assert.match(remedy.hint, /qBittorrent/);
});
