import assert from "node:assert/strict";
import test from "node:test";

import { dirtyKeys, sameValue } from "./stepform.ts";

const fields = [
  { key: "token", kind: "secret" as const },
  { key: "url", kind: "text" as const },
  { key: "poll_interval", kind: "duration" as const },
  { key: "enabled", kind: "bool" as const },
];

const base = { token: "", url: "http://qbt", poll_interval: "15", enabled: false };

test("没动过的表是干净的", () => {
  assert.deepEqual(dirtyKeys(fields, base, { ...base }), []);
});

test("敏感字段留空不算脏：空串就是「不改」", () => {
  assert.equal(sameValue("secret", "", ""), true);
  assert.equal(sameValue("secret", "", "   "), true);
});

test("敏感字段填了内容才算脏", () => {
  assert.deepEqual(dirtyKeys(fields, base, { ...base, token: "eyJ" }), ["token"]);
});

test("文本改了才算脏，数字/时长按输入串比较", () => {
  assert.deepEqual(dirtyKeys(fields, base, { ...base, url: "http://qbt2" }), ["url"]);
  assert.deepEqual(dirtyKeys(fields, base, { ...base, poll_interval: "30" }), ["poll_interval"]);
  assert.deepEqual(dirtyKeys(fields, base, { ...base, poll_interval: "15" }), []);
});

test("开关按布尔比较：字符串 \"false\" 不能被当成真", () => {
  assert.equal(sameValue("bool", false, false), true);
  assert.equal(sameValue("bool", false, "false"), true);
  assert.equal(sameValue("bool", false, true), false);
});

test("多个字段一起改时全部列出，顺序跟字段表走", () => {
  const values = { token: "eyJ", url: "http://new", poll_interval: "30", enabled: true };
  assert.deepEqual(dirtyKeys(fields, base, values), ["token", "url", "poll_interval", "enabled"]);
});
