/**
 * 表草稿：一张表一份，键是 formKey（节 + revision）。
 *
 * 抽成纯函数是为了能被测：向导里最容易写错的不是表单控件，而是两件事——
 *
 * 1. 「一次事件里连着改两个字段」（选清单 = id + name）：各自基于旧快照展开会互相覆盖；
 * 2. 「一张表一份」的边界：以前只存一份草稿，改完 A 步再去动 B 步，A 的草稿就被顶掉了，
 *    而屏幕上看不出它没了（回到 A 只看得到服务器上的旧值）。现在按 formKey 存一份表。
 *
 * 钥匙 = 节 + revision：保存成功后 revision 变了，旧草稿自然不再命中，也就不会拿
 * 「保存前的输入」冒充「服务器上的最新值」。
 */

/** FormValues 是一张表的草稿值：文本类都是字符串，开关是布尔。 */
export type FormValues = Record<string, string | boolean>;

/** Drafts 是「一张表一份草稿」：键是 formKey，值是该表的当前输入。 */
export type Drafts = Record<string, FormValues>;

/** draftFor 取一张表的当前值；没有草稿就回落到 base()（只在真的需要时才算）。 */
export function draftFor(drafts: Drafts, key: string, base: () => FormValues): FormValues {
  return drafts[key] ?? base();
}

/**
 * setFields 把一批字段合进这张表的草稿，而不是用一批字段替换掉草稿。
 *
 * patch 一次可以带多个字段，调用方连着调两次也叠得住（React 批处理下「连着两次
 * setState 各自展开旧对象」会丢字段的那个坑）。
 */
export function setFields(
  drafts: Drafts,
  key: string,
  base: () => FormValues,
  patch: FormValues,
): Drafts {
  const values = drafts[key] ?? base();
  return { ...drafts, [key]: { ...values, ...patch } };
}

/**
 * clearDrafts 删掉某一节的所有草稿（保存成功后调用）。
 *
 * 按前缀删而不是只删当前 key：revision 会变，旧 key 从此永远不再命中，
 * 留着只是把「已经写进服务器的输入」在内存里多存一份。
 */
export function clearDrafts(drafts: Drafts, sectionPrefix: string): Drafts {
  const kept: Drafts = {};
  for (const [key, values] of Object.entries(drafts)) {
    if (!key.startsWith(sectionPrefix)) kept[key] = values;
  }
  return kept;
}
