import assert from "node:assert/strict";
import test from "node:test";

import {
  BACKFILL_BATCH,
  backfillBatchNote,
  backfillConfirmText,
  backfillGroups,
  backfillPrefix,
  backfillSummary,
  filterBackfill,
  nextBackfillBatch,
} from "./backfill.ts";
import type { BackfillDir } from "./api.ts";

/**
 * 补录目录那一节的纯逻辑（#43）：分组、筛选、成批、文案。
 *
 * 这一节以前是 502 行全量铺开（26,533px，占整页 85%）；分组与批量是把它收回一屏的
 * 两件事，所以判据必须有测试钉住——尤其是「按钮印多少就补多少」这一条。
 */

const dir = (number: string, videos = 1): BackfillDir => ({ number, videos, bytes: 0 });

const sample = [
  dir("ABF-017"),
  dir("ABF-029"),
  dir("ABF-032", 2),
  dir("FSDSS-033"),
  dir("FSDSS-047"),
  dir("FSDSS-059"),
  dir("MIDV-002", 2),
  dir("9MIDA-198"),
  dir("T28313"),
];

test("前缀取第一个连字符之前，没有连字符就是整串", () => {
  assert.equal(backfillPrefix("ABF-017"), "ABF");
  assert.equal(backfillPrefix("9MIDA-198"), "9MIDA");
  assert.equal(backfillPrefix("T28313"), "T28313");
  assert.equal(backfillPrefix("abf-017"), "ABF");
  // 连字符打头这种脏数据不能变成空前缀：整串兜底。
  assert.equal(backfillPrefix("-017"), "017");
});

test("分组按条数从多到少，同数按字母序", () => {
  assert.deepEqual(backfillGroups(sample), [
    { prefix: "ABF", count: 3 },
    { prefix: "FSDSS", count: 3 },
    { prefix: "9MIDA", count: 1 },
    { prefix: "MIDV", count: 1 },
    { prefix: "T28313", count: 1 },
  ]);
});

test("搜索吃掉连字符与大小写：abf017 与 ABF-017 是同一个目录", () => {
  const all = filterBackfill(sample, { q: "", prefix: "" });
  assert.equal(all.length, sample.length);

  for (const q of ["abf017", "ABF-017", " abf 017 "]) {
    assert.deepEqual(
      filterBackfill(sample, { q, prefix: "" }).map((item) => item.number),
      ["ABF-017"],
      `搜索词 ${JSON.stringify(q)}`,
    );
  }

  assert.deepEqual(
    filterBackfill(sample, { q: "fsdss", prefix: "" }).map((item) => item.number),
    ["FSDSS-033", "FSDSS-047", "FSDSS-059"],
  );
});

test("前缀筛选与搜索词一起用，且保持后端给的顺序", () => {
  assert.deepEqual(
    filterBackfill(sample, { q: "", prefix: "ABF" }).map((item) => item.number),
    ["ABF-017", "ABF-029", "ABF-032"],
  );
  assert.deepEqual(
    filterBackfill(sample, { q: "03", prefix: "FSDSS" }).map((item) => item.number),
    ["FSDSS-033"],
  );
  assert.deepEqual(filterBackfill(sample, { q: "", prefix: "NOPE" }), []);
});

test("一批就是当前筛选的前 N 个：按钮印多少就补多少", () => {
  const filtered = filterBackfill(sample, { q: "", prefix: "ABF" });
  assert.equal(nextBackfillBatch(filtered).length, 3);
  assert.equal(nextBackfillBatch(filtered, 2).length, 2);
  assert.deepEqual(
    nextBackfillBatch(filtered, 2).map((item) => item.number),
    ["ABF-017", "ABF-029"],
  );
  // 空筛选批次是空数组，不是「第一个」。
  assert.deepEqual(nextBackfillBatch([]), []);
});

test("默认批量是 50：一节 502 行不能按一下就全动", () => {
  assert.equal(BACKFILL_BATCH, 50);
  const many = Array.from({ length: 120 }, (_, index) => dir(`ABF-${String(index).padStart(3, "0")}`));
  assert.equal(nextBackfillBatch(many).length, 50);
});

test("确认条同时说范围、后果与不做什么", () => {
  const text = backfillConfirmText(12);
  assert.match(text, /12 个目录/);
  assert.match(text, /\.nfo 会被删掉/);
  assert.match(text, /不过 qBittorrent/);
});

test("一批的结局：全成、全败、成败混合各说各的", () => {
  assert.match(backfillBatchNote([]), /没有目录/);
  assert.match(
    backfillBatchNote([
      { number: "ABF-017", ok: true },
      { number: "ABF-029", ok: true },
    ]),
    /^2 个目录已补录/,
  );
  assert.match(
    backfillBatchNote([
      { number: "ABF-017", ok: false, message: "目录没了" },
      { number: "ABF-029", ok: false, message: "目录没了" },
    ]),
    /^2 个目录都没成：目录没了$/,
  );
  const mixed = backfillBatchNote([
    { number: "ABF-017", ok: true },
    { number: "ABF-029", ok: false, message: "没有视频" },
  ]);
  assert.match(mixed, /^1 个已补录，1 个没成/);
  assert.match(mixed, /第一个是 ABF-029：没有视频/);
});

test("面板摘要：没有目录就没有那一行", () => {
  assert.equal(backfillSummary(null), null);
  assert.equal(backfillSummary({ dirs: [] }), null);
  assert.equal(backfillSummary({ dirs: sample }), "9 个旧目录待补录 · 5 个番号前缀");
});
