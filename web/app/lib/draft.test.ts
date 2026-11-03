import assert from "node:assert/strict";
import test from "node:test";

import { setFields } from "./draft.ts";

const empty = () => ({ list_id: "", name: "" });

test("patch 里的多个字段一起落进草稿", () => {
  const draft = setFields(null, "list_registry:3", empty, { list_id: "k4EVE4", name: "想看清单" });

  assert.deepEqual(draft.values, { list_id: "k4EVE4", name: "想看清单" });
});

test("同一事件里连着写两个字段不会互相覆盖", () => {
  // 向导的「选清单」就是这么写的：先 list_id，再 name。各自展开旧快照的话，
  // 第二次 setFields 会把第一次的 list_id 抹掉（截图里那个 id 空、名字有值的 bug）。
  const first = setFields(null, "list_registry:3", empty, { list_id: "k4EVE4" });
  const second = setFields(first, "list_registry:3", empty, { name: "想看清单" });

  assert.equal(second.key, "list_registry:3");
  assert.deepEqual(second.values, { list_id: "k4EVE4", name: "想看清单" });
});

test("钥匙变了就回落到服务器最新值，不带着上一节的草稿", () => {
  const draft = setFields({ key: "qbittorrent:7", values: { url: "旧的" } }, "list_registry:3", empty, {
    list_id: "k4EVE4",
  });

  assert.equal(draft.key, "list_registry:3");
  assert.deepEqual(draft.values, { list_id: "k4EVE4", name: "" });
});

test("只有真的要回落时才去算 base", () => {
  let calls = 0;
  const base = () => {
    calls += 1;
    return empty();
  };

  setFields({ key: "list_registry:3", values: { list_id: "k4EVE4" } }, "list_registry:3", base, {
    name: "想看清单",
  });

  assert.equal(calls, 0);
});
