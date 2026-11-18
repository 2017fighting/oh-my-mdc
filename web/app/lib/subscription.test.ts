import assert from "node:assert/strict";
import test from "node:test";

import {
  subscriptionAddBody,
  subscriptionSubtitle,
  subscriptionWatermark,
} from "./subscription.ts";

/**
 * 订阅区（规格 §6.4/§8.2）的纯逻辑：面板一行的那句话，与「添加」那颗按钮发出去的形状。
 *
 * 上游没有按名字搜女优的端点（§13 实测），所以入口只有「贴 id / 女优页链接」两种——
 * 判定哪一种由这里做，别让面板里那颗按钮自己猜。
 */

test("贴的是链接就发 javdb_url，贴的是 id 就发 actress_id", () => {
  assert.deepEqual(subscriptionAddBody("EvkJ"), { actress_id: "EvkJ" });
  assert.deepEqual(subscriptionAddBody("  EvkJ  "), { actress_id: "EvkJ" });
  assert.deepEqual(subscriptionAddBody("https://javdb.com/actors/Av2e?locale=zh"), {
    javdb_url: "https://javdb.com/actors/Av2e?locale=zh",
  });
  // 镜像域名一堆，判定只看「像不像一条链接」，不白名单主机。
  assert.deepEqual(subscriptionAddBody("jdforrepam.com/actors/Av2e"), {
    javdb_url: "jdforrepam.com/actors/Av2e",
  });
});

test("空输入不编一个请求体：那颗按钮该是禁用的", () => {
  assert.equal(subscriptionAddBody(""), null);
  assert.equal(subscriptionAddBody("   "), null);
});

test("水位线说成日期，不知道就说不知道", () => {
  assert.equal(subscriptionWatermark("2026-11-01"), "2026-11-01");
  assert.equal(subscriptionWatermark(""), "还没跑过");
});

test("一行说清「带来 N 部」与回灌窗口", () => {
  const row = {
    id: 1,
    actor: { javdb_actor_id: "EvkJ", name: "河北彩花", avatar_url: "", gender: "" },
    watermark_date: "2026-11-01",
    lookback_days: 30,
    backfill_from: null,
    enabled: true,
    tasks_count: 12,
    pending_count: 0,
  };

  assert.equal(subscriptionSubtitle(row), "水位线 2026-11-01 · 带来 12 部");

  // 回灌窗口开着时把下界说在行上：它是「为什么这一轮在翻旧账」的答案。
  assert.equal(
    subscriptionSubtitle({ ...row, backfill_from: "2026-10-02" }),
    "水位线 2026-11-01 · 带来 12 部 · 回灌自 2026-10-02",
  );

  // 还有版本在等加种时说个数，别让人以为「带来 12 部」等于「12 部都动起来了」。
  assert.equal(
    subscriptionSubtitle({ ...row, tasks_count: 0, pending_count: 3 }),
    "水位线 2026-11-01 · 带来 0 部 · 3 部待加种",
  );

  // 停用的那一行要自己说出来：它不再追新，但已建任务照走完。
  assert.equal(
    subscriptionSubtitle({ ...row, enabled: false }),
    "水位线 2026-11-01 · 带来 12 部 · 已停用",
  );
});
