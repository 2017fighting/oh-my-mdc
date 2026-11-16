import {
  isRouteErrorResponse,
  Links,
  Meta,
  Outlet,
  Scripts,
  ScrollRestoration,
} from "react-router";

import type { Route } from "./+types/root";
import "./app.css";

export const links: Route.LinksFunction = () => [
  { rel: "icon", href: "/favicon.ico" },
];

export function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN">
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        {/* 浏览器 chrome 随色：无 JS 时靠 media 跟系统；钉住后由 ThemeToggle 补一颗
            不带 media 的覆盖（浏览器取最后一颗）。 */}
        <meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)" />
        <meta name="theme-color" content="#141414" media="(prefers-color-scheme: dark)" />
        {/* 双主题的当前态（跟随系统 = light dark，钉住 = light/dark），ThemeToggle 负责写。
            下面这行内联脚本在首绘前把存的偏好写进 meta——位置必须在它后面、body 之前
            （防闪，见 DESIGN · Dark）；存取失败就跟随系统，不拦页面。 */}
        <meta name="color-scheme" content="light dark" />
        <script
          dangerouslySetInnerHTML={{
            __html:
              'try{var s=localStorage.getItem("color-scheme");document.querySelector(\'meta[name="color-scheme"]\').content=s||"light dark";}catch(e){}',
          }}
        />
        <Meta />
        <Links />
      </head>
      <body className="min-h-screen bg-muted/30 antialiased">
        <a
          href="#main"
          className="sr-only focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:z-60 focus:rounded-lg focus:bg-background focus:px-3 focus:py-2 focus:text-sm focus:ring-1 focus:ring-foreground/20"
        >
          跳到主内容
        </a>
        {children}
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}

export default function App() {
  return <Outlet />;
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  let title = "出了点问题";
  let detail = "页面没能正常加载，刷新试试。";
  let stack: string | undefined;

  if (isRouteErrorResponse(error)) {
    title = error.status === 404 ? "页面不存在" : `错误 ${error.status}`;
    detail =
      error.status === 404
        ? "这个地址没有对应的页面。"
        : error.statusText || detail;
  } else if (import.meta.env.DEV && error instanceof Error) {
    detail = error.message;
    stack = error.stack;
  }

  return (
    <main id="main" tabIndex={-1} className="mx-auto max-w-2xl p-6 outline-none">
      <h1 className="text-xl font-semibold">{title}</h1>
      <p className="mt-2 text-muted-foreground">{detail}</p>
      {stack ? (
        <pre className="mt-4 overflow-x-auto rounded-lg bg-muted p-4 text-xs">
          <code>{stack}</code>
        </pre>
      ) : null}
    </main>
  );
}
