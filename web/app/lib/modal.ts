/**
 * 弹层共用的两件小事：外壳 inert 的计数，与「能 Tab 到哪些元素」。
 *
 * 原本长在 panel.tsx 里。向导也要开一个浮层（术语），而两处各写一份 inert 计数会
 * 立刻出事：抽屉与帮助各持一只把手，谁先放手就会把另一半的 inert 撤掉。所以计数是
 * 模块级唯一的一份，抽屉、帮助、向导的三只把手都从这里拿。
 */

let shellInertHolds = 0;

/** holdShellInert 把 #app-shell 置 inert，返回一只必须调用的释放把手。 */
export function holdShellInert(): () => void {
  if (shellInertHolds === 0) document.getElementById("app-shell")?.setAttribute("inert", "");
  shellInertHolds += 1;
  let released = false;
  return () => {
    if (released) return;
    released = true;
    shellInertHolds = Math.max(0, shellInertHolds - 1);
    if (shellInertHolds === 0) document.getElementById("app-shell")?.removeAttribute("inert");
  };
}

/** focusablesIn 返回弹层里可 Tab 到的元素，按文档顺序；隐藏的不算。 */
export function focusablesIn(root: HTMLElement): HTMLElement[] {
  return Array.from(
    root.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((element) => element.offsetParent !== null);
}
