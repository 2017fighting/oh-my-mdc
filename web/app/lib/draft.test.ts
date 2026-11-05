import assert from "node:assert/strict";
import test from "node:test";

import { clearDrafts, draftFor, setFields } from "./draft.ts";

const empty = () => ({ list_id: "", name: "" });

test("patch 里的多个字段一起落进草稿", () => {
  const drafts = setFields({}, "list_registry:3", empty, { list_id: "k4EVE4", name: "想看清单" });

  assert.deepEqual(drafts["list_registry:3"], { list_id: "k4EVE4", name: "想看清单" });
});

test("同一事件里连着写两个字段不会互相覆盖", () => {
  // 向导的「选清单」就是这么写的：先 list_id，再 name。各自展开旧快照的话，
  // 第二次 setFields 会把第一次的 list_id 抹掉（截图里那个 id 空、名字有值的 bug）。
  const first = setFields({}, "list_registry:3", empty, { list_id: "k4EVE4" });
  const second = setFields(first, "list_registry:3", empty, { name: "想看清单" });

  assert.deepEqual(second["list_registry:3"], { list_id: "k4EVE4", name: "想看清单" });
});

test("两张表的草稿各存一份：动了 B 步，A 步的输入还在", () => {
  // 真实回归：以前只有一份草稿，改完 JavDB 的 token 去填清单 id 再回来，
  // token 输入框是空的——比丢一次点击更糟的是它看起来从来没被输入过。
  const withToken = setFields({}, "javdb:1", () => ({ token: "" }), { token: "eyJ-typed" });
  const withList = setFields(withToken, "list_registry:2", empty, { list_id: "k4EVE4" });

  assert.deepEqual(withList["javdb:1"], { token: "eyJ-typed" });
  assert.deepEqual(withList["list_registry:2"], { list_id: "k4EVE4", name: "" });
});

test("钥匙变了就回落到服务器最新值，不带着上一节的草稿", () => {
  const drafts = setFields({ "qbittorrent:7": { url: "旧的" } }, "list_registry:3", empty, {
    list_id: "k4EVE4",
  });

  assert.deepEqual(draftFor(drafts, "list_registry:3", empty), { list_id: "k4EVE4", name: "" });
});

test("只有真的要回落时才去算 base", () => {
  let calls = 0;
  const base = () => {
    calls += 1;
    return empty();
  };

  setFields({ "list_registry:3": { list_id: "k4EVE4" } }, "list_registry:3", base, {
    name: "想看清单",
  });

  assert.equal(calls, 0);
});

test("clearDrafts 只清这一节，别的节的草稿原样留着", () => {
  const drafts = {
    "javdb:1": { token: "eyJ" },
    "javdb:2": { token: "eyJ" },
    "qbittorrent:1": { url: "http://qbt" },
  };

  const kept = clearDrafts(drafts, "javdb:");

  assert.deepEqual(Object.keys(kept), ["qbittorrent:1"]);
  assert.deepEqual(kept["qbittorrent:1"], { url: "http://qbt" });
});

test("clearDrafts 不按前缀误伤：javdb 不会带走 javdb2", () => {
  const drafts = { "javdb:1": { token: "a" }, "javdb2:1": { token: "b" } };

  assert.deepEqual(Object.keys(clearDrafts(drafts, "javdb:")), ["javdb2:1"]);
});
