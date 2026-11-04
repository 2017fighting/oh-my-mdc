import assert from "node:assert/strict";
import test from "node:test";

import { formatInterval, formatWhen, formatFuture, formatAbsolute } from "./time.ts";

const at = (iso: string) => new Date(iso).getTime();
const now = at("2026-11-04T14:42:05Z");

test("一分钟以内说刚刚，不给「0 分钟前」", () => {
  assert.equal(formatWhen("2026-11-04T14:41:50Z", now), "刚刚");
});

test("分钟、小时、天各用自己的单位", () => {
  assert.equal(formatWhen("2026-11-04T14:39:05Z", now), "3 分钟前");
  assert.equal(formatWhen("2026-11-04T11:42:05Z", now), "3 小时前");
  assert.equal(formatWhen("2026-11-02T14:42:05Z", now), "2 天前");
});

test("昨天单独说昨天，不数「1 天前」", () => {
  assert.equal(formatWhen("2026-11-03T14:42:05Z", now), "昨天");
});

test("超过一周回落到绝对时间：那时候精确的日期比「9 天前」有用", () => {
  assert.equal(formatWhen("2026-10-01T14:42:05Z", now), formatAbsolute("2026-10-01T14:42:05Z"));
});

test("空值与坏值都给破折号，不抛异常", () => {
  assert.equal(formatWhen(null, now), "—");
  assert.equal(formatWhen("not-a-date", now), "—");
});

test("将来的时间说「N 分钟后」：副作用的下次尝试总在未来", () => {
  assert.equal(formatFuture("2026-11-04T14:47:05Z", now), "5 分钟后");
  assert.equal(formatFuture("2026-11-04T16:42:05Z", now), "2 小时后");
  assert.equal(formatFuture("2026-11-04T14:42:20Z", now), "马上");
});

test("间隔说人话：秒、分、整小时", () => {
  assert.equal(formatInterval(45), "每 45 秒");
  assert.equal(formatInterval(900), "每 15 分钟");
  assert.equal(formatInterval(3600), "每小时");
  assert.equal(formatInterval("120"), "每 2 分钟");
});

test("app_config 存的是 Go duration 串，不是秒数", () => {
  assert.equal(formatInterval("15m0s"), "每 15 分钟");
  assert.equal(formatInterval("5m0s"), "每 5 分钟");
  assert.equal(formatInterval("1m0s"), "每分钟");
  assert.equal(formatInterval("1h0m0s"), "每小时");
  assert.equal(formatInterval("90m0s"), "每 90 分钟");
});

test("读不到间隔就不说，绝不编一个默认值", () => {
  assert.equal(formatInterval(undefined), null);
  assert.equal(formatInterval(0), null);
  assert.equal(formatInterval("abc"), null);
});
