import assert from "node:assert/strict";
import test from "node:test";

import {
  collectedImportItems,
  importIDs,
  importReceipts,
  importResultLabel,
  type ImportOutcome,
} from "./collected.ts";
import type { CollectedActress } from "./api.ts";

/**
 * 「从 App 收藏导入」（#50，规格 §6.4/§8.4）的纯逻辑：谁勾得动、发哪几个 id、
 * 逐条回执怎么说。
 *
 * 名单本身问 `GET /api/javdb/collected-actors`（需 token，`subscribed` 由服务端算）；
 * 这里只处理那份名单到手之后的事。面板没有搜索框（上游没有按名字搜女优的端点，§13），
 * 所以这里的函数里也不该出现任何「按名字过滤」的形状。
 */

/** 一份缩放版的真实收藏：两位女优（一位已订阅）、一位男优。 */
const rows: CollectedActress[] = [
  { id: "D2EdJ", name: "花守夏歩", videos_count: 179, gender: 0, subscribed: false },
  { id: "EvkJ", name: "河北彩花", videos_count: 232, gender: 0, subscribed: true },
  { id: "m1", name: "森林原人", videos_count: 900, gender: 1, subscribed: false },
];

test("已订阅的勾不动、男优也勾不动，其余都能勾", () => {
  const items = collectedImportItems(rows);

  assert.deepEqual(
    items.map((item) => [item.actress.id, item.selectable, item.reason]),
    [
      ["D2EdJ", true, ""],
      // 已订阅：置灰 + 说清为什么（重复导入是幂等的，但让人再勾一遍没有意义）。
      ["EvkJ", false, "已订阅"],
      // gender=1 是男优（上游实测）：这是女优订阅，他是「列出来但勾不动」。
      ["m1", false, "男优，女优订阅不收"],
    ],
  );
});

test("不认识的 gender 不猜：照样能勾", () => {
  // 上游的性别码只实测过 0/1。将来多出别的值，宁可让人自己勾，也不要静默藏起来。
  const items = collectedImportItems([
    { id: "x", name: "未知", videos_count: 1, gender: 2, subscribed: false },
  ]);
  assert.equal(items[0].selectable, true);
  assert.equal(items[0].reason, "");
});

test("只发勾上的，顺序照名单", () => {
  const items = collectedImportItems([
    ...rows,
    { id: "Av2e", name: "桃園憐奈", videos_count: 322, gender: 0, subscribed: false },
  ]);

  assert.deepEqual(importIDs(items, new Set(["Av2e", "D2EdJ"])), ["D2EdJ", "Av2e"]);
  // 一个都没勾就不发：不要发一个空请求去让服务端说「没东西」。
  assert.deepEqual(importIDs(items, new Set()), []);
  // 勾不动的 id 即使出现在集合里也不发（置灰的勾不是意图）。
  assert.deepEqual(importIDs(items, new Set(["EvkJ", "m1"])), []);
});

test("回执把新建、本来就有、失败分开数", () => {
  const results: ImportOutcome[] = [
    { id: "D2EdJ", name: "花守夏歩", outcome: "created" },
    { id: "EvkJ", name: "河北彩花", outcome: "duplicate" },
    { id: "m1", name: "森林原人", outcome: "failed", message: "上游读不到这位女优" },
  ];

  assert.deepEqual(importReceipts(results), {
    tone: "warn",
    text: "导入 3 位：新建 1、本来就有 1、失败 1（森林原人：上游读不到这位女优）",
  });

  // 全成时是 ok 那一档，并且说清「重复导入没造第二行」。
  assert.deepEqual(importReceipts(results.slice(0, 2)), {
    tone: "ok",
    text: "导入 2 位：新建 1、本来就有 1",
  });

  // 一条都没有时不编一句话（那颗按钮本来就该是禁用的）。
  assert.deepEqual(importReceipts([]), { tone: "ok", text: "" });
});

test("逐条回执说清那一行成了没有", () => {
  assert.equal(importResultLabel({ id: "a", name: "A", outcome: "created" }), "已订阅");
  assert.equal(
    importResultLabel({ id: "b", name: "B", outcome: "duplicate" }),
    "本来就有，水位线原样保留",
  );
  assert.equal(
    importResultLabel({ id: "c", name: "C", outcome: "failed", message: "门禁没开：还没初始化" }),
    "失败：门禁没开：还没初始化",
  );
});
