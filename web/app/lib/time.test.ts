import assert from "node:assert/strict";
import test from "node:test";

import {
  formatAge,
  formatElapsed,
  formatFuture,
  formatInterval,
  formatWhen,
} from "./time.ts";

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

// 值班台问的是「这件事我躲了多久」，不是「这是哪一天」：日期只活在 title 里。
test("台阶一直延到月与年，永不回落绝对时间", () => {
  assert.equal(formatWhen("2026-10-06T14:42:05Z", now), "29 天前");
  assert.equal(formatWhen("2026-10-05T14:42:05Z", now), "1 个月前"); // 30 天换月
  assert.equal(formatWhen("2026-09-05T14:42:05Z", now), "2 个月前");
  assert.equal(formatWhen("2025-11-05T14:42:05Z", now), "12 个月前"); // 364 天还是月
  assert.equal(formatWhen("2025-11-04T14:42:05Z", now), "1 年前"); // 365 天换年
  assert.equal(formatWhen("2025-09-30T14:42:05Z", now), "1 年前");
});

test("任何时间都不上屏绝对日期：年份只活在 title 里", () => {
  for (const at of ["2025-09-30T14:42:05Z", "2027-12-09T14:42:05Z", "2026-10-05T14:42:05Z"]) {
    for (const text of [formatWhen(at, now), formatAge(at, now), formatFuture(at, now)]) {
      assert.ok(!/\d{4}\//.test(text), `「${text}」看起来是绝对日期`);
    }
  }
});

test("空值与坏值都给破折号，不抛异常", () => {
  assert.equal(formatWhen(null, now), "—");
  assert.equal(formatWhen("not-a-date", now), "—");
});

test("进行中的耗时不带「前」：刚刚 / N 秒 / N 分钟 / N 小时", () => {
  assert.equal(formatElapsed("2026-11-04T14:42:01Z", now), "刚刚");
  assert.equal(formatElapsed("2026-11-04T14:41:40Z", now), "25 秒");
  assert.equal(formatElapsed("2026-11-04T14:40:05Z", now), "2 分钟");
  assert.equal(formatElapsed("2026-11-04T12:42:05Z", now), "2 小时");
});

test("未来起点不会算出负耗时：错钟时回落「刚刚」", () => {
  assert.equal(formatElapsed("2026-11-04T14:45:00Z", now), "刚刚");
  assert.equal(formatElapsed(null, now), "—");
});

test("将来也走同一份台阶：分、时、天、月、年", () => {
  assert.equal(formatFuture("2026-11-04T14:47:05Z", now), "5 分钟后");
  assert.equal(formatFuture("2026-11-04T16:42:05Z", now), "2 小时后");
  assert.equal(formatFuture("2026-11-04T14:42:20Z", now), "马上");
  assert.equal(formatFuture("2026-11-05T10:42:05Z", now), "20 小时后");
  assert.equal(formatFuture("2026-11-07T14:42:05Z", now), "3 天后");
  assert.equal(formatFuture("2026-12-19T14:42:05Z", now), "1 个月后");
  assert.equal(formatFuture("2027-12-09T14:42:05Z", now), "1 年后");
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

test("「最早」读的是同一份台阶：天、月、年都认", () => {
  assert.equal(formatAge("2026-10-24T14:42:05Z", now), "11 天前");
  assert.equal(formatAge("2026-10-05T14:42:05Z", now), "1 个月前");
  assert.equal(formatAge("2025-11-04T14:42:05Z", now), "1 年前");
  assert.equal(formatAge("2026-11-03T14:42:05Z", now), "昨天");
  assert.equal(formatAge("2026-11-04T13:42:05Z", now), "1 小时前");
  // 未来的时刻对「已经挂了多久」没有意义：夹成 刚刚，不算负数。
  assert.equal(formatAge("2026-12-01T14:42:05Z", now), "刚刚");
  assert.equal(formatAge(null, now), "—");
});
