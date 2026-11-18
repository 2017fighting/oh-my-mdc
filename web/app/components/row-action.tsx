import * as React from "react";
import { cn } from "cn";

import { Button } from "~/components/ui/button";

/**
 * 行内动作与主按钮的尺寸（DESIGN 的「md 以下 44px 热区」那一条）。
 *
 * 以前这串类名在 panel.tsx 与 routes/home.tsx 里各抄了十几遍，「44px 是拇指热区」这条
 * 规则只活在散文里，于是最先漂掉的是空状态里的两颗「清除筛选」（28px）。收成常量之后，
 * 改规则就是改这一行，漏抄就不可能再发生。
 *
 * 它们与 RowAction 一起住在面板之外：订阅区（subscriptions.tsx）已成一块独立的组件文件，
 * 再从 panel.tsx 拿这几个词就要把两个文件连成环。
 */

/** 行内动作：md 以下 44px 热区 + 13px 字，md 起 28px + 12px。 */
export const ROW_ACTION_SIZE = "h-11 px-3 dense md:h-7 md:px-2.5 md:text-xs";

/** 面板与错误块里的主按钮：比行内动作高 4px（md 起 32px），字号同族。 */
export const PANEL_ACTION_SIZE = "h-11 px-3 dense md:h-8 md:px-2.5 md:text-sm";

/**
 * RowAction 是行内动作的唯一尺寸：手机上一律 44px 热区（值班时是单手刷屏，36px 的
 * 行内动作实测要瞄），md 起收回 28px。
 */
export function RowAction({ className, variant = "outline", ...props }: React.ComponentProps<typeof Button>) {
  return (
    <Button
      variant={variant}
      className={cn(ROW_ACTION_SIZE, className)}
      {...props}
    />
  );
}
