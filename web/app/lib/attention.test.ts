import assert from "node:assert/strict";
import test from "node:test";

import { groupByNumber } from "./attention.ts";
import type { AttentionItem } from "./api";

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
