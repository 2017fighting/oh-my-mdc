#!/usr/bin/env node
/**
 * 对比度探针：从 app.css 读 token，用真浏览器采真像素，按 WCAG 算比值。
 *
 * 为什么不用 node 自己算颜色：oklch→sRGB 的数学写错一次就守不住，而「守」正是它存在的
 * 理由。所以颜色交给浏览器（canvas 的 fillStyle 与 CSS 用的是同一个色彩管线），仓库里
 * 只留两件事：**哪些对要测**、**阈值多少**。
 *
 * 契约（见 DESIGN 的 Contrast 一节）：
 * - 文本对 ≥ 4.5，焦点环 ≥ 3；
 * - 状态回按命名自动配对 `--status-<名>-text × --status-<名>-surface`，新增第五个
 *   状态色不会静默逃测；
 * - 边框、发丝线、表面色只打印不判定——它们是装饰性分层，状态靠字（The Signal-Only
 *   Color Rule）。给它们设阈值会逼着状态色为了「边框好看」而牺牲可读性。
 *
 * 颜色一律从 `web/app/app.css` 读：这里不存第二份色值，改 token 探针自动跟上。
 */

import { execFile } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const run = promisify(execFile);
const here = dirname(fileURLToPath(import.meta.url));
const appCSS = join(here, "..", "app", "app.css");

/** 一页里要量的对：前景色 × 若干层背景（按 CSS 的合成顺序，从下往上）。 */
const TEXT_THRESHOLD = 4.5;
const NON_TEXT_THRESHOLD = 3;

/** 找不到浏览器时的候选（本机 macOS / Linux / playwright 缓存都在内）。 */
function chromeCandidates() {
  const home = process.env.HOME ?? "";
  const list = [
    process.env.CHROME,
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
    "/usr/bin/google-chrome",
    "/usr/bin/google-chrome-stable",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
  ].filter(Boolean);
  if (home) {
    for (const version of ["chromium-1248", "chromium-1228", "chromium-1091"]) {
      list.push(join(home, ".cache", "ms-playwright", version, "chrome-linux64", "chrome"));
      list.push(join(home, ".cache", "ms-playwright", version, "chrome-linux", "chrome"));
    }
  }
  return list.filter((path) => path && existsSync(path));
}

/** 从 app.css 里抽出所有 `--token: <颜色>`；只认 oklch()/hex，别的形态不猜。 */
function readTokens(css) {
  const tokens = new Map();
  const pattern = /--([a-z0-9-]+)\s*:\s*(oklch\([^)]*\)|#[0-9a-fA-F]{3,8})\s*;/g;
  for (const [, name, value] of css.matchAll(pattern)) tokens.set(name, value);
  return tokens;
}

function need(tokens, name) {
  const value = tokens.get(name);
  if (!value) throw new Error(`app.css 里找不到 --${name}：探针不猜颜色`);
  return value;
}

/**
 * 构建要量的对。
 *
 * `bg-muted/40` 这种写法在 CSS 里是「40% 的 muted 叠在下面那层之上」，所以这里也照抄
 * 合成顺序：先卡白、再 40% 灰。alpha 由浏览器合成，跟真实渲染一致。
 */
function buildPairs(tokens) {
  const card = need(tokens, "card");
  const background = need(tokens, "background");
  const muted = need(tokens, "muted");
  const mutedFg = need(tokens, "muted-foreground");
  const ring = need(tokens, "ring");

  const pairs = [
    { name: "muted-foreground / 表头 bg-muted/40", threshold: TEXT_THRESHOLD, fg: mutedFg, layers: [{ color: card }, { color: muted, alpha: 0.4 }] },
    { name: "muted-foreground / 回执带 bg-muted/50", threshold: TEXT_THRESHOLD, fg: mutedFg, layers: [{ color: card }, { color: muted, alpha: 0.5 }] },
    { name: "muted-foreground / 卡片白", threshold: TEXT_THRESHOLD, fg: mutedFg, layers: [{ color: card }] },
    { name: "muted-foreground / 页面底 bg-muted/30", threshold: TEXT_THRESHOLD, fg: mutedFg, layers: [{ color: background }, { color: muted, alpha: 0.3 }] },
    { name: "muted-foreground / 实心 bg-muted", threshold: TEXT_THRESHOLD, fg: mutedFg, layers: [{ color: muted }] },
    { name: "焦点环 / 卡片白", threshold: NON_TEXT_THRESHOLD, fg: ring, layers: [{ color: card }] },
  ];

  // 状态色按命名配对：新增 --status-foo-text / --status-foo-surface 会自动进入名单。
  const names = [...tokens.keys()]
    .map((name) => /^status-(.+)-text$/.exec(name)?.[1])
    .filter(Boolean)
    .sort();
  if (names.length === 0) throw new Error("app.css 里没有 --status-*-text：状态三件套没了？");

  for (const name of names) {
    const surface = need(tokens, `status-${name}-surface`);
    pairs.push({
      name: `状态「${name}」字 / 底`,
      threshold: TEXT_THRESHOLD,
      fg: need(tokens, `status-${name}-text`),
      layers: [{ color: card }, { color: surface }],
    });
    // 装饰性：只打印。边框与表面色不承载状态语义，颜色从不单独说话。
    const border = tokens.get(`status-${name}-border`);
    if (border) {
      pairs.push({
        name: `状态「${name}」边 / 底（装饰）`,
        threshold: null,
        fg: border,
        layers: [{ color: card }, { color: surface }],
      });
    }
  }

  const hairline = tokens.get("border");
  if (hairline) {
    pairs.push({ name: "发丝线 / 卡片白（装饰）", threshold: null, fg: hairline, layers: [{ color: card }] });
  }

  return pairs;
}

/** 生成采样页：canvas 里按 CSS 的规则合成，然后算 WCAG 比值。 */
function buildPage(pairs) {
  return `<!doctype html><meta charset="utf-8"><body><script>
const pairs = ${JSON.stringify(pairs)};
const cv = document.createElement("canvas");
cv.width = cv.height = 2;
const ctx = cv.getContext("2d", { willReadFrequently: true });

function sample(color, layers) {
  ctx.globalAlpha = 1;
  ctx.fillStyle = "#fff";
  ctx.fillRect(0, 0, 2, 2);
  for (const layer of layers) {
    ctx.globalAlpha = layer.alpha ?? 1;
    ctx.fillStyle = layer.color;
    ctx.fillRect(0, 0, 2, 2);
  }
  const bg = ctx.getImageData(1, 1, 1, 1).data;

  ctx.globalAlpha = 1;
  ctx.fillStyle = color;
  ctx.fillRect(0, 0, 2, 2);
  const fg = ctx.getImageData(1, 1, 1, 1).data;

  return [[fg[0], fg[1], fg[2]], [bg[0], bg[1], bg[2]]];
}

const linear = (value) => {
  const s = value / 255;
  return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
};
const luminance = ([r, g, b]) => 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);

const results = pairs.map((pair) => {
  const [fg, bg] = sample(pair.fg, pair.layers);
  const a = luminance(fg);
  const b = luminance(bg);
  const [hi, lo] = a > b ? [a, b] : [b, a];
  return { name: pair.name, threshold: pair.threshold, ratio: (hi + 0.05) / (lo + 0.05) };
});

document.body.textContent = JSON.stringify({ results });
</script></body>`;
}

function unescapeHTML(text) {
  return text
    .replaceAll("&quot;", '"')
    .replaceAll("&lt;", "<")
    .replaceAll("&gt;", ">")
    .replaceAll("&amp;", "&");
}

async function main() {
  const chrome = chromeCandidates();
  if (chrome.length === 0) {
    console.error("找不到 Chromium：设 CHROME=/path/to/chrome 再跑（探针不装作通过）");
    process.exit(1);
  }

  const css = await readFile(appCSS, "utf8");
  const pairs = buildPairs(readTokens(css));
  const dir = await mkdtemp(join(tmpdir(), "mdc-contrast-"));
  const page = join(dir, "probe.html");
  await writeFile(page, buildPage(pairs), "utf8");

  try {
    const { stdout } = await run(chrome[0], [
      "--headless=new",
      "--no-sandbox",
      "--disable-gpu",
      "--virtual-time-budget=3000",
      "--dump-dom",
      `file://${page}`,
    ], { maxBuffer: 8 * 1024 * 1024 });

    const body = /<body[^>]*>([\s\S]*?)<\/body>/.exec(stdout)?.[1];
    if (!body) throw new Error("拿不到采样页的 DOM：浏览器没跑起来？");
    const { results } = JSON.parse(unescapeHTML(body.trim()));

    let failed = 0;
    console.log("对比度探针（颜色读自 web/app/app.css，像素来自 canvas）\n");
    for (const row of results) {
      const ratio = row.ratio.toFixed(2);
      if (row.threshold === null) {
        console.log(`   ${ratio.padStart(6)}:1      ${row.name}`);
        continue;
      }
      const ok = row.ratio >= row.threshold;
      if (!ok) failed += 1;
      console.log(`${ok ? " ✓" : " ✗"} ${ratio.padStart(6)}:1 ≥ ${row.threshold}  ${row.name}`);
    }

    console.log("");
    if (failed > 0) {
      console.error(`${failed} 对不达阈值。改 token，不要在组件上打补丁（DESIGN · Contrast）。`);
      process.exit(2);
    }
    console.log(`全部达标（${results.filter((row) => row.threshold !== null).length} 对文本/焦点，其余为装饰性打印）。`);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

main().catch((error) => {
  console.error(`探针没跑成：${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
});
