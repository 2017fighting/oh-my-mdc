import assert from "node:assert/strict";
import test from "node:test";

import { attentionRowCount, groupByNumber, groupStuck } from "./attention.ts";
import type { Attention, AttentionItem, SideEffect } from "./api";

function item(movieID: string, number: string, infohash: string): AttentionItem {
  return {
    movie_id: movieID,
    number,
    infohash,
    source: "javdb_magnet",
    label: "中文",
    reason: "刮削失败：DMM 超时",
    at: "2026-11-04T09:00:00Z",
  };
}

test("同一番号的版本收成一行", () => {
  const groups = groupByNumber([
    item("mv_a", "SSIS-812", "aa"),
    item("mv_a", "SSIS-812", "bb"),
  ]);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].number, "SSIS-812");
  assert.deepEqual(
    groups[0].items.map((one) => one.infohash),
    ["aa", "bb"],
  );
});

test("两任务的失败版本交错时仍各收一行", () => {
  // 接口按 updated_at DESC 出数据，A/B/A 这种交错是常态而不是边角。
  const groups = groupByNumber([
    item("mv_a", "SSIS-812", "aa"),
    item("mv_b", "PRED-309", "cc"),
    item("mv_a", "SSIS-812", "bb"),
  ]);
  assert.deepEqual(
    groups.map((group) => group.movieID),
    ["mv_a", "mv_b"],
  );
  assert.equal(groups[0].items.length, 2);
  assert.equal(groups[1].items.length, 1);
});

test("保持首次出现的顺序", () => {
  const groups = groupByNumber([
    item("mv_b", "PRED-309", "cc"),
    item("mv_a", "SSIS-812", "aa"),
  ]);
  assert.deepEqual(
    groups.map((group) => group.movieID),
    ["mv_b", "mv_a"],
  );
});

function stuck(id: number, kind: "notify" | "list_removal", lastError: string): SideEffect {
  return {
    id,
    kind,
    target_key: `event:${id}`,
    payload: {},
    attempts: 3,
    last_error: lastError,
    next_attempt_at: null,
    stuck: true,
  };
}

const TELEGRAM_401 = "telegram: 401 Unauthorized";

test("同一病因的驻留待办收成一组", () => {
  const groups = groupStuck([
    stuck(1, "notify", TELEGRAM_401),
    stuck(2, "notify", TELEGRAM_401),
    stuck(3, "notify", TELEGRAM_401),
  ]);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].effects.length, 3);
});

test("kind 不同或错不同就不并：病因不是一回事", () => {
  const groups = groupStuck([
    stuck(1, "notify", TELEGRAM_401),
    stuck(2, "notify", "telegram: chat 未激活"),
    stuck(3, "list_removal", TELEGRAM_401),
  ]);
  assert.equal(groups.length, 3);
  assert.deepEqual(
    groups.map((group) => group.effects.map((effect) => effect.id)),
    [[1], [2], [3]],
  );
});

test("空队列没有组", () => {
  assert.deepEqual(groupStuck([]), []);
});

test("「需要人工」的行数按病因数：三条同因通知算一件事", () => {
  const attention: Attention = {
    ok: true,
    failed: [item("mv_a", "SSIS-812", "aa")],
    stalled: [],
    failed_truncated: false,
    stalled_truncated: false,
  };
  assert.equal(
    attentionRowCount(attention, [
      stuck(1, "notify", TELEGRAM_401),
      stuck(2, "notify", TELEGRAM_401),
      stuck(3, "notify", TELEGRAM_401),
    ]),
    2,
  );
});
