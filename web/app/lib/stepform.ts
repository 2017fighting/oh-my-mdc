/**
 * 表单的「脏」判定：这一屏有没有还没写进服务器的输入。
 *
 * 以前向导没有这个概念，于是三件事同时发生：`下一步` 看起来像「保存并继续」却只导航；
 * `测试连接` 测的是服务器上的旧值；屏幕上看不出「我改了但没存」。三者都不是控件问题，
 * 是没有人回答「现在存的是不是屏幕上的这个」。
 *
 * 判据与 `kindHandlers.initial` 对齐：草稿里的值 vs 服务器给这张表的初值。敏感字段的
 * 空串永远是「不改」（掩码只做 placeholder），所以空串不算脏。
 */

export type FieldShape = {
  key: string;
  kind: "text" | "secret" | "number" | "duration" | "bool";
};

/** dirtyKeys 返回这一屏被改过的字段名，顺序与字段表一致。 */
export function dirtyKeys(
  fields: FieldShape[],
  base: Record<string, string | boolean>,
  values: Record<string, string | boolean>,
): string[] {
  return fields.filter((field) => !sameValue(field.kind, base[field.key], values[field.key])).map((field) => field.key);
}

/** sameValue 回答「草稿里的这个值，与服务器上那个值算不算同一件事」。 */
export function sameValue(
  kind: FieldShape["kind"],
  base: string | boolean | undefined,
  value: string | boolean | undefined,
): boolean {
  if (kind === "bool") return boolValue(base) === boolValue(value);

  const before = text(base);
  const now = text(value);
  // 敏感字段：空串＝不改。留空保存与「原样不动」是同一件事，不该亮「未保存」。
  if (kind === "secret") return now.trim() === "" || now === before;
  return now === before;
}

function text(value: string | boolean | undefined): string {
  return value === undefined ? "" : String(value);
}

/** boolValue 连字符串形态一起认：服务端与表单都给布尔，但草稿回放时可能拿到 "false"。 */
function boolValue(value: string | boolean | undefined): boolean {
  if (typeof value === "boolean") return value;
  const raw = text(value).trim().toLowerCase();
  return !(raw === "" || raw === "false" || raw === "0");
}
