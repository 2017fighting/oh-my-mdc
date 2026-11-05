/**
 * 测试连接的稳定机器码 → 屏幕上的那句话。
 *
 * 机器码属于 `settings.TestCode`（internal/settings/test.go），是给人排查用的身份，
 * 不是给人读的文案。以前前端只翻了两条 Telegram 专用码，其余原样露出——于是一个
 * 中文界面上写着「失败 · auth」；触屏没有 hover，`title` 里那句「该改什么」等于不存在。
 *
 * 这里只做一件事：给每个码一句人话。细节（哪台机器、哪个路径、上游原话）仍由服务端的
 * detail 承担，界面上单独一行展示，不塞进胶囊。
 */

const TEST_CODE_LABELS: Record<string, string> = {
  timeout: "连接超时",
  auth: "鉴权被拒",
  network: "网络不通",
  protocol: "响应不符合预期",
  business: "上游拒绝",
  token_invalid: "token 无效",
  chat_unavailable: "chat 未激活",
  not_implemented: "未接线",
};

/** 认不出的码一律说「测试失败」：屏幕不该替后端发明一个英文单词。 */
export function testCodeLabel(code: string | undefined): string {
  if (!code) return "测试失败";
  return TEST_CODE_LABELS[code] ?? "测试失败";
}
