import { ArrowUpRight, ListChecks, Settings, Sparkles, Terminal } from "lucide-react";

import { Badge } from "~/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";

/**
 * 骨架壳页（#18）。
 *
 * 这一步只交付「打开浏览器看得见中文界面」：导航、状态位、统计卡都按规格 §8.2 的形状摆好，
 * 数据留给向导（#19）与面板（#23 起）填。所有文案 zh-CN。
 */

const stats = [
  { label: "下载中", value: "—", hint: "已交给 qBittorrent、还没下完" },
  { label: "加工中", value: "—", hint: "刮削与落盘进行中" },
  { label: "已入库", value: "—", hint: "已硬链进媒体库" },
  { label: "需人工", value: "—", hint: "失败待重试与待重发" },
];

const navItems = [
  { label: "概览", icon: ListChecks, current: true },
  { label: "集成设置", icon: Settings, current: false },
];

const roadmap = [
  { title: "配置向导", text: "填 JavDB、qBittorrent、Jellyfin，配齐才算初始化" },
  { title: "自动下载", text: "轮询想看清单，有磁力的番号自动交给 qBittorrent" },
  { title: "入库与通知", text: "刮削、硬链、刷 Jellyfin、发 Telegram，各自独立重试" },
];

export function meta() {
  return [
    { title: "oh-my-mdc · 概览" },
    { name: "description", content: "把 JavDB 清单里的番号变成 Jellyfin 媒体库条目" },
  ];
}

export default function Overview() {
  return (
    <div className="mx-auto flex min-h-screen w-full max-w-6xl gap-6 p-6 text-foreground">
      <aside className="hidden w-52 shrink-0 flex-col gap-6 md:flex">
        <div className="flex items-center gap-2">
          <Sparkles className="size-5 text-primary" aria-hidden />
          <span className="font-heading text-base font-semibold tracking-tight">
            oh-my-mdc
          </span>
        </div>

        <nav className="flex flex-col gap-1 text-sm">
          {navItems.map((item) => (
            <span
              key={item.label}
              aria-current={item.current ? "page" : undefined}
              className={
                item.current
                  ? "flex items-center gap-2 rounded-lg bg-background px-3 py-2 font-medium ring-1 ring-foreground/10"
                  : "flex items-center gap-2 rounded-lg px-3 py-2 text-muted-foreground"
              }
            >
              <item.icon className="size-4" aria-hidden />
              {item.label}
            </span>
          ))}
        </nav>

        <p className="mt-auto text-xs leading-relaxed text-muted-foreground">
          未初始化时整站只剩向导；主线三件套配齐后循环才会启动。
        </p>
      </aside>

      <main className="flex min-w-0 flex-1 flex-col gap-6">
        <header className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h1 className="font-heading text-2xl font-semibold tracking-tight">概览</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              现在只有骨架：数据库已连上、迁移已跑过，业务循环还没有接线。
            </p>
          </div>
          <Badge variant="outline" className="gap-1">
            <Terminal className="size-3" aria-hidden />
            骨架版本
          </Badge>
        </header>

        <section aria-label="任务统计" className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {stats.map((stat) => (
            <Card key={stat.label} size="sm">
              <CardHeader>
                <CardDescription>{stat.label}</CardDescription>
                <CardTitle className="text-2xl tabular-nums">{stat.value}</CardTitle>
              </CardHeader>
              <CardContent className="text-xs text-muted-foreground">{stat.hint}</CardContent>
            </Card>
          ))}
        </section>

        <Card>
          <CardHeader>
            <CardTitle>接下来会有什么</CardTitle>
            <CardDescription>
              这一页由 Go 二进制内嵌交付；下面的统计卡与告警区会在后续版本接入真实数据。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <Separator />
            <ul className="flex flex-col gap-2 text-sm">
              {roadmap.map((item) => (
                <li key={item.title} className="flex items-start gap-2">
                  <Badge variant="secondary" className="mt-0.5 shrink-0">
                    {item.title}
                  </Badge>
                  <span className="text-muted-foreground">{item.text}</span>
                </li>
              ))}
            </ul>
            <a
              className="inline-flex items-center gap-1 text-sm font-medium text-primary underline-offset-4 hover:underline"
              href="https://github.com/2017fighting/oh-my-mdc/issues"
            >
              开发进度与问题都在 GitHub Issues
              <ArrowUpRight className="size-3.5" aria-hidden />
            </a>
          </CardContent>
        </Card>
      </main>
    </div>
  );
}
