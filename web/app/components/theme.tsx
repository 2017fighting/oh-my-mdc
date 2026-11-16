import * as React from "react";
import { cn } from "cn";

/**
 * 界面配色三态（跟随系统 / 浅色 / 深色），规格见 DESIGN · Dark 与 ADR-0013。
 *
 * 机制是 meta 驱动的：`<meta name="color-scheme">` 承载当前态（"light dark" /
 * "light" / "dark"），CSS 侧 `:root:has(> head > meta[...])` 把它映射回
 * color-scheme，token 的 light-dark() 自然选边。这里只负责写 meta 与记忆：
 *
 * - 偏好存 localStorage；与系统一致时删除条目——「钉住」只在真的偏离系统时成立，
 *   否则系统以后自己换色（按时段自动切换很常见）会被一条恰好相同的旧值永久压住。
 * - 跨标签页靠 storage 事件回读；同页第二个实例（侧栏 + 窄屏顶行各一颗）
 *   靠自派事件回读；系统偏好变化监听 matchMedia 的 change，只在跟随系统时同步。
 */

const STORAGE_KEY = "color-scheme";
const CHANGE_EVENT = "oh-my-mdc:color-scheme-change";
/** 窄屏任务栏/浏览器 chrome 的随色。深色取 oklch(0.19 0 0) 的 sRGB 近似。 */
const THEME_COLOR = { light: "#ffffff", dark: "#141414" } as const;

type Choice = "light" | "dark" | null;
type Scheme = "light" | "dark";

function readStored(): Choice {
  try {
    const value = localStorage.getItem(STORAGE_KEY);
    return value === "light" || value === "dark" ? value : null;
  } catch {
    return null;
  }
}

function systemScheme(): Scheme {
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function writeMeta(choice: Choice) {
  const meta = document.querySelector('meta[name="color-scheme"]');
  meta?.setAttribute("content", choice ?? "light dark");
}

/**
 * 浏览器 chrome 的 theme-color：无 JS 时靠两颗带 media 的 meta 跟系统；
 * 一旦有偏好（或要显示当前解出的值），补一颗不带 media 的——浏览器取最后一颗。
 */
function syncThemeColor(scheme: Scheme) {
  let override = document.querySelector<HTMLMetaElement>('meta[data-theme-color-override]');
  if (!override) {
    override = document.createElement("meta");
    override.setAttribute("name", "theme-color");
    override.setAttribute("data-theme-color-override", "");
    document.head.appendChild(override);
  }
  override.setAttribute("content", THEME_COLOR[scheme]);
}

function pin(choice: "system" | "light" | "dark") {
  const system = systemScheme();
  // 「跟随系统」与「恰好和系统一致」都不留条目：钉住只在真的偏离系统时成立，
  // 否则系统以后自己换色会被一条恰好相同的旧值永久压住。
  const stored: Choice = choice === "system" || choice === system ? null : choice;
  writeMeta(stored);
  try {
    if (stored) localStorage.setItem(STORAGE_KEY, stored);
    else localStorage.removeItem(STORAGE_KEY);
  } catch {
    // 存不进去就让它只在当前页生效；meta 已写，配色不受影响。
  }
  syncThemeColor(stored ?? system);
  window.dispatchEvent(new Event(CHANGE_EVENT));
}

export function ThemeToggle({ className, touch = false }: { className?: string; touch?: boolean }) {
  const [choice, setChoice] = React.useState<Choice>(null);
  const [system, setSystem] = React.useState<Scheme>("light");

  React.useEffect(() => {
    const resync = () => {
      setChoice(readStored());
      setSystem(systemScheme());
      syncThemeColor(readStored() ?? systemScheme());
    };
    resync();
    // storage 事件的 key 为 null 表示 clear()，也要回读。
    const onStorage = (event: StorageEvent) => {
      if (event.key === STORAGE_KEY || event.key === null) resync();
    };
    window.addEventListener("storage", onStorage);
    window.addEventListener(CHANGE_EVENT, resync);
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const onSystem = () => resync();
    media.addEventListener("change", onSystem);
    return () => {
      window.removeEventListener("storage", onStorage);
      window.removeEventListener(CHANGE_EVENT, resync);
      media.removeEventListener("change", onSystem);
    };
  }, []);

  const options: { value: "system" | "light" | "dark"; label: string }[] = [
    { value: "system", label: "跟随系统" },
    { value: "light", label: "浅色" },
    { value: "dark", label: "深色" },
  ];

  return (
    <div
      role="radiogroup"
      aria-label="界面配色"
      className={cn(
        "grid grid-cols-3 gap-0.5 rounded-lg bg-card p-1 ring-1 ring-foreground/10",
        touch ? "text-[13px]" : "h-7 p-0.5 text-xs",
        className,
      )}
    >
      {options.map((option) => {
        const active = choice === null ? option.value === "system" : choice === option.value;
        const label = option.value === "system" && touch ? "系统" : option.label;
        const title =
          option.value === "system"
            ? `跟随系统（当前${system === "dark" ? "深" : "浅"}色）`
            : active
              ? `已钉住${option.label}`
              : `钉住${option.label}`;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={active}
            title={title}
            onClick={() => pin(option.value)}
            className={cn(
              "flex h-full items-center justify-center whitespace-nowrap rounded-md px-1.5 font-medium transition-colors",
              "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
              touch && "h-11",
              active ? "bg-secondary text-secondary-foreground" : "text-muted-foreground hover:text-foreground",
            )}
          >
            {label}
          </button>
        );
      })}
    </div>
  );
}
