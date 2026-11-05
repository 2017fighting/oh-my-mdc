import assert from "node:assert/strict";
import test from "node:test";

import { testCodeLabel } from "./testcodes.ts";

test("每个稳定码都有人话", () => {
  for (const code of [
    "timeout",
    "auth",
    "network",
    "protocol",
    "business",
    "token_invalid",
    "chat_unavailable",
    "not_implemented",
  ]) {
    const label = testCodeLabel(code);
    assert.notEqual(label, "测试失败", `${code} 应该有专属文案`);
    assert.match(label, /[\u4e00-\u9fa5]/, `${code} 的文案应该是中文`);
  }
});

test("认不出的码不把英文原样搬上屏幕", () => {
  assert.equal(testCodeLabel("host_path_invisible"), "测试失败");
  assert.equal(testCodeLabel(""), "测试失败");
  assert.equal(testCodeLabel(undefined), "测试失败");
});

test("两条 Telegram 专用码保留原有的说法", () => {
  assert.equal(testCodeLabel("token_invalid"), "token 无效");
  assert.equal(testCodeLabel("chat_unavailable"), "chat 未激活");
});
