import assert from "node:assert/strict";
import test from "node:test";

import { CONFUSABLES, MISSING_FACE_LABELS, STATUS_NAMES, STATUS_NOTES, STATUS_ORDER } from "./copy.ts";
import type { MissingFace } from "./api.ts";

test("四个状态名与说明一一对应，不多不少", () => {
  assert.deepEqual(Object.keys(STATUS_NAMES).sort(), [...STATUS_ORDER].sort());
  assert.deepEqual(Object.keys(STATUS_NOTES).sort(), [...STATUS_ORDER].sort());
});

test("状态名唯一、非空、够短：徽章与 chip 都要装得下", () => {
  const names = Object.values(STATUS_NAMES);
  assert.equal(new Set(names).size, names.length, "状态名不许重复");
  for (const name of names) {
    assert.ok(name.length > 0 && name.length <= 4, `「${name}」不在 1–4 字之间`);
  }
});

test("「需人工」不许回到状态名里：那是地方的名字", () => {
  // 2026-11 那一轮把任务状态从「需人工」改成「失败」，就是为了让一个词只干一件事。
  // 这条测试是那个决定的守卫：谁把词写回去，这里先红。
  for (const name of Object.values(STATUS_NAMES)) {
    assert.ok(!name.includes("人工"), `状态名「${name}」又把地方名借来了`);
  }
});

test("「失败」那条要说出 URL 里的写法：author 会手改地址栏", () => {
  assert.match(STATUS_NOTES.failed, /\?state=failed/);
});

test("容易混的五条都有词有解释，且不复述状态名定义", () => {
  assert.equal(CONFUSABLES.length, 5);
  for (const entry of CONFUSABLES) {
    assert.ok(entry.term.length > 0, "术语缺词");
    assert.ok(entry.note.length >= 20, `「${entry.term}」的解释太短，说不清`);
  }
  // 状态名的定义只住在 STATUS_NOTES：易混区可以**引用**它们（任务级「失败」vs 版本级
  // 「失败」），但不能把一个状态名当成一条独立的词条再定义一遍。
  const bare = new Set(
    Object.values(STATUS_NAMES).flatMap((name) => [name, `「${name}」`]),
  );
  for (const entry of CONFUSABLES) {
    assert.ok(!bare.has(entry.term), `「${entry.term}」是状态名，不该在易混区再定义一遍`);
  }
});

test("待补齐四面文案齐全、非空、够短：chip 要装得下", () => {
  // 机器名与后端 task.missing_faces（internal/task/faces.go）同一套词，
  // 漆一个面就有一面在面板上变成裸英文。
  assert.deepEqual(Object.keys(MISSING_FACE_LABELS).sort(), ["actors", "images", "outline", "title_zh"].sort());
  for (const face of Object.keys(MISSING_FACE_LABELS) as MissingFace[]) {
    const label = MISSING_FACE_LABELS[face];
    assert.ok(label.length > 0 && label.length <= 6, `「${face}」的文案「${label}」不在 1–6 字之间`);
    assert.ok(label.startsWith("缺"), `「${face}」的文案「${label}」应当以「缺」开头（缺的是哪一面，一眼可读）`);
    assert.ok(!label.includes("缺译"), `「${face}」的文案用了 GLOSSARY 的 _Avoid_ 词「缺译」`);
  }
});
