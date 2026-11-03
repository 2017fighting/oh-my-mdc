/**
 * 表草稿的最小状态机：一张表一屏，草稿带着「它属于哪一节哪个 revision」的钥匙。
 *
 * 抽成纯函数是为了能被测：向导里最容易写错的不是表单控件，而是「一次事件里连着改
 * 两个字段」（选清单 = id + name）——各自基于旧快照展开就会互相覆盖。
 */

/** FormValues 是一张表的草稿值：文本类都是字符串，开关是布尔。 */
export type FormValues = Record<string, string | boolean>;

/** Draft 是一份草稿，key 是它属于的表（节 + revision）。 */
export type Draft = { key: string; values: FormValues };

/**
 * setFields 把一批字段合进草稿，而不是用一批字段替换掉草稿。
 *
 * 三个约定：
 *
 * 1. 钥匙对上就接着原来的值改，对不上（换步、保存后 revision 变了）就回落到
 *    base()——即服务器上的最新值，敏感字段永远是空串；
 * 2. base 是函数而不是值：只有真的要回落时才去算它；
 * 3. patch 一次可以带多个字段，调用方连着调两次也叠得住（这正是 React 批处理
 *    下「连着两次 setState 各自展开旧对象」会丢字段的那个坑）。
 */
export function setFields(
  draft: Draft | null,
  key: string,
  base: () => FormValues,
  patch: FormValues,
): Draft {
  const values = draft?.key === key ? draft.values : base();
  return { key, values: { ...values, ...patch } };
}
