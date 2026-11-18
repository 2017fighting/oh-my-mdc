import assert from "node:assert/strict";
import test from "node:test";

import {
  skipAcceptText,
  skipReasonLabel,
  skipRowLine,
  skipsBadge,
} from "./skips.ts";

/**
 * 「最近被排掉的」那块折叠格（规格 §8.2/§8.4，GLOSSARY「跳过 / 收下」）。
 *
 * 三条判据的名字是上游与规格共用的词，面板只负责把它们说成人话；
 * 「收下」的回执文案也在这里——`outcome` 是服务端给的，别在面板里另编一套。
 */

test("三条判据各有一句人话", () => {
  assert.equal(skipReasonLabel("not_single"), "多人一起的");
  assert.equal(skipReasonLabel("compilation"), "精选合集");
  assert.equal(skipReasonLabel("long_form"), "长合集");
});

test("不认识的判据原样回出来，不编一个更像样的词", () => {
  // 上游/规格将来多一条判据时，面板该显示那个词本身（可见的怪话好过一句编出来的假话）。
  assert.equal(skipReasonLabel("unknown_reason"), "unknown_reason");
  assert.equal(skipReasonLabel(""), "");
});

test("计数徽标：0 也要说出来，别让一个空格子看起来像还没读", () => {
  assert.equal(skipsBadge(0), "0");
  assert.equal(skipsBadge(3), "3");
  assert.equal(skipsBadge(120), "99+");
});

test("一行说清是谁：番号 + 标题 + 发行日（没有标题就不留空括号）", () => {
  assert.equal(
    skipRowLine({ number: "OFJE-186", title: "初ベスト 6 タイトル 8 時間", release_date: "2026-11-14" }),
    "OFJE-186 · 初ベスト 6 タイトル 8 時間 · 2026-11-14",
  );
  // 发行日上游可能没给：那一天就不说话，不编一个今天。
  assert.equal(
    skipRowLine({ number: "REBD-725", title: "两位女优共演", release_date: null }),
    "REBD-725 · 两位女优共演",
  );
  // 标题也可能是空串（详情没给标题）。
  assert.equal(
    skipRowLine({ number: "ABP-001", title: "", release_date: "2026-11-02" }),
    "ABP-001 · 2026-11-02",
  );
  assert.equal(skipRowLine({ number: "ABP-001", title: "", release_date: null }), "ABP-001");
});

test("「收下」的回执照 outcome 说话：收下了 / 还没出水", () => {
  assert.equal(skipAcceptText("accepted"), "已收下，任务已建、开始加种");
  assert.equal(skipAcceptText("no_magnet"), "还没有磁力，先留着——出水了再收下");
  // 不认识的结果也不许说「成功」。
  assert.equal(skipAcceptText("weird"), "已收下");
});
