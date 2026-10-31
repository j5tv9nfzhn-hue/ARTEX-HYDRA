#!/usr/bin/env node
/**
 * 编辑排版层（方案 A · 纸张 · 编年）的闭环守卫。
 *
 * 它守的是这个项目里最容易悄悄破掉的一条承诺：
 *   「新增的纸本主题不能影响其余 8 套主题。」
 *
 * 六个不变量：
 *   1. editorial.css 里的每一条规则都必须挂在 [data-theme-preset="paper"] 之下
 *      （唯二的例外是「默认态」声明，它们必须显式列出）。
 *   2. presets/paper.css 必须覆盖完整的令牌契约 —— 少一个令牌，
 *      组件就会回落到默认主题的颜色，在深色模式下尤其明显。
 *   3. 页面要么继承全局壳的 body 锚点，要么自己挂一个；
 *      否则拿不到表格语法（这是阶段二踩过的坑）。
 *   4. skin-editorial.css 的每一条规则都必须挂在 [data-skin="editorial"] 之下，
 *      且双主题的七处接线齐全。
 *   5. skin-editorial.css 里不得出现硬编码色值：颜色只能来自 paper.css 的令牌，
 *      或本文件令牌块里那四档中性暗（抬升与遮罩）。
 *   6. 主题预设必须注册进 THEME_PRESET_OPTIONS（外观下拉与命令面板的唯一来源）。
 *
 * 用法：node scripts/audit-editorial.mjs
 * 退出码非 0 即失败，可直接接进 CI。零依赖，不需要先构建。
 */

import { readdirSync, readFileSync, existsSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const web = join(root, "web");

const errors = [];
const warns = [];

function read(p) {
  return existsSync(p) ? readFileSync(p, "utf8") : null;
}

// ── 1. editorial.css 的规则作用域 ────────────────────────────────────────────
// 允许无作用域的「默认态」选择器：它们定义的是所有主题下的基线外观。
const DEFAULT_STATE_OK = new Set(['[data-editorial="masthead-meta"]', '[data-editorial="secnum"]']);

const editorial = read(join(web, "src/styles/editorial.css"));
if (!editorial) {
  errors.push("缺少 web/src/styles/editorial.css");
} else {
  // 粗粒度但足够：按顶层 `选择器 {` 切块，跳过注释、@media、@import。
  const stripped = editorial.replace(/\/\*[\s\S]*?\*\//g, "");
  const selectors = [];
  let depth = 0;
  let buf = "";
  for (const ch of stripped) {
    if (ch === "{") {
      if (depth === 0) selectors.push(buf.trim());
      depth++;
      buf = "";
    } else if (ch === "}") {
      depth = Math.max(0, depth - 1);
      buf = "";
    } else if (depth === 0) {
      buf += ch;
    }
  }
  let unscoped = 0;
  for (const sel of selectors) {
    if (!sel || sel.startsWith("@")) continue;
    const parts = sel.split(",").map((s) => s.trim()).filter(Boolean);
    for (const part of parts) {
      if (part.includes('[data-theme-preset="paper"]')) continue;
      if (DEFAULT_STATE_OK.has(part)) continue;
      unscoped++;
      errors.push(`editorial.css 存在未限定主题的选择器：${part}`);
    }
  }
  if (unscoped === 0) console.log(`  ✓ editorial.css 全部规则限定在 paper 作用域内（${selectors.length} 块）`);
}

// ── 2. paper.css 的令牌契约 ──────────────────────────────────────────────────
const REQUIRED_TOKENS = [
  "--radius",
  "--background",
  "--foreground",
  "--card",
  "--card-foreground",
  "--popover",
  "--popover-foreground",
  "--primary",
  "--primary-foreground",
  "--secondary",
  "--secondary-foreground",
  "--muted",
  "--muted-foreground",
  "--accent",
  "--accent-foreground",
  "--destructive",
  "--border",
  "--input",
  "--ring",
  "--chart-1",
  "--chart-2",
  "--chart-3",
  "--chart-4",
  "--chart-5",
  "--sidebar",
  "--sidebar-foreground",
  "--sidebar-primary",
  "--sidebar-accent",
  "--sidebar-border",
  "--sidebar-ring",
  "--shadow-2xs",
  "--shadow-xs",
  "--shadow-sm",
  "--shadow-md",
  "--shadow-lg",
  "--shadow-xl",
  "--shadow-2xl",
];

const paper = read(join(web, "src/styles/presets/paper.css"));
if (!paper) {
  errors.push("缺少 web/src/styles/presets/paper.css");
} else {
  const declared = new Set([...paper.matchAll(/(--[a-z0-9-]+)\s*:/g)].map((m) => m[1]));
  const missing = REQUIRED_TOKENS.filter((t) => !declared.has(t));
  if (missing.length) errors.push(`paper.css 缺少令牌：${missing.join(", ")}`);
  else console.log(`  ✓ paper.css 令牌契约完整（${REQUIRED_TOKENS.length} 项）`);

  // 浅色 + 深色两套都必须存在
  if (!/:root\[data-theme-preset="paper"\]/.test(paper)) errors.push('paper.css 缺少浅色作用域 :root[data-theme-preset="paper"]');
  if (!/\.dark:root\[data-theme-preset="paper"\]/.test(paper)) errors.push("paper.css 缺少深色作用域 .dark:root[...]");
}

// ── 3. 主题注册与 body 锚点 ──────────────────────────────────────────────────
const themeTs = read(join(web, "src/lib/preferences/theme.ts"));
if (!themeTs) {
  errors.push("缺少 web/src/lib/preferences/theme.ts");
} else {
  // 只取 THEME_PRESET_OPTIONS 块内的 value，避免把浅色/深色/跟随系统也算进来
  const block = themeTs.slice(
    themeTs.indexOf("THEME_PRESET_OPTIONS"),
    themeTs.indexOf("THEME_PRESET_VALUES"),
  );
  const values = [...block.matchAll(/value:\s*"([^"]+)"/g)].map((m) => m[1]);
  if (!values.includes("paper")) errors.push("theme.ts 未注册 paper");
  const presets = readdirSync(join(web, "src/styles/presets")).filter((f) => f.endsWith(".css"));
  const unregistered = presets
    .map((f) => f.replace(/\.css$/, ""))
    .filter((n) => !values.includes(n));
  if (unregistered.length) warns.push(`有预设文件未注册到 theme.ts：${unregistered.join(", ")}`);
  console.log(`  ✓ 主题已注册：共 ${values.length} 套（含 paper）`);
}

const shell = read(join(web, "src/app/(main)/_components/main-content.tsx"));
const shellAnchored = Boolean(shell && shell.includes('data-editorial="body"'));
if (!shellAnchored) errors.push("全局壳 main-content.tsx 没有 body 锚点，普通页面拿不到表格语法");

function walk(dir, out = []) {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (e.name === "page.tsx") out.push(p);
  }
  return out;
}

const pages = walk(join(web, "src/app/(main)"));
let mastheads = 0;
for (const p of pages) {
  const src = readFileSync(p, "utf8");
  if (src.includes("<Masthead")) mastheads++;
  const ownAnchor = src.includes('data-editorial="body"');
  if (!ownAnchor && !shellAnchored) {
    errors.push(`${relative(web, p)} 既没有自己的 body 锚点，全局壳也没有`);
  }
  if (src.includes("<Masthead") && !src.includes('from "@/components/editorial"')) {
    errors.push(`${relative(web, p)} 用了 Masthead 但没 import`);
  }
}
console.log(`  ✓ 页面 ${pages.length} 个，其中 ${mastheads} 个使用报头原语`);

// ── 4. 双主题「外观」层的作用域 ──────────────────────────────────────────────
// 与第 1 条同一条纪律：skin-editorial.css 里每一条规则都必须挂在
// :root[data-skin="editorial"] 之下，否则切回老主题时会有样式漏出去。

const skin = read(join(web, "src/styles/skin-editorial.css"));
if (!skin) {
  errors.push("缺少 web/src/styles/skin-editorial.css");
} else {
  const stripped = skin.replace(/\/\*[\s\S]*?\*\//g, "");
  const selectors = [];
  let depth = 0;
  let buf = "";
  for (const ch of stripped) {
    if (ch === "{") {
      if (depth === 0) selectors.push(buf.trim());
      depth++;
      buf = "";
    } else if (ch === "}") {
      depth = Math.max(0, depth - 1);
      buf = "";
    } else if (depth === 0) {
      buf += ch;
    }
  }
  let unscoped = 0;
  for (const sel of selectors) {
    if (!sel || sel.startsWith("@")) continue;
    for (const part of sel.split(",").map((x) => x.trim()).filter(Boolean)) {
      if (part.includes('[data-skin="editorial"]')) continue;
      unscoped++;
      errors.push(`skin-editorial.css 存在未限定外观的选择器：${part}`);
    }
  }
  if (unscoped === 0) console.log(`  ✓ skin-editorial.css 全部规则限定在外观作用域内（${selectors.length} 块）`);
}

// ── 4b. 皮肤层不得硬编码配色 ──────────────────────────────────────────────────
// 颜色只有两个合法出处：presets/paper.css 的令牌，或本文件令牌块里那四档
// 「中性暗」（抬升与遮罩）。规则里一旦写死色值，换预设 / 换主题时它不再跟着动，
// 而且往往只在深色模式下才现形——评审时最容易漏，所以交给脚本守。

{
  const lines = skin.split("\n");
  let inComment = false;
  const codeLines = [];
  for (let i = 0; i < lines.length; i++) {
    let cur = lines[i];
    let out = "";
    while (cur.length) {
      if (inComment) {
        const end = cur.indexOf("*/");
        if (end === -1) { cur = ""; } else { inComment = false; cur = cur.slice(end + 2); }
      } else {
        const start = cur.indexOf("/*");
        if (start === -1) { out += cur; cur = ""; } else { out += cur.slice(0, start); inComment = true; cur = cur.slice(start + 2); }
      }
    }
    codeLines.push([i + 1, out]);
  }

  // 令牌块 = 第一个 :root[data-skin="editorial"] { ... } 的闭合区间
  let tokenStart = -1;
  let tokenEnd = -1;
  for (let i = 0; i < codeLines.length; i++) {
    if (codeLines[i][1].includes(':root[data-skin="editorial"] {')) { tokenStart = i; break; }
  }
  if (tokenStart >= 0) {
    let depth = 0;
    for (let i = tokenStart; i < codeLines.length && tokenEnd < 0; i++) {
      for (const ch of codeLines[i][1]) {
        if (ch === "{") depth++;
        else if (ch === "}") { depth--; if (depth === 0) { tokenEnd = i; break; } }
      }
    }
  }

  const COLOR = /#[0-9a-fA-F]{3,8}\b|oklch\(|rgba?\(|hsla?\(/;
  let hardCoded = 0;
  for (let i = 0; i < codeLines.length; i++) {
    if (tokenStart >= 0 && i >= tokenStart && i <= tokenEnd) continue;
    if (COLOR.test(codeLines[i][1])) {
      hardCoded++;
      errors.push(
        `skin-editorial.css 第 ${codeLines[i][0]} 行出现硬编码色值（令牌块之外只能引用 var()）：${codeLines[i][1].trim().slice(0, 60)}`
      );
    }
  }
  if (hardCoded === 0) {
    console.log(`  ✓ skin-editorial.css 无硬编码配色（令牌块第 ${tokenStart + 1}–${tokenEnd + 1} 行除外）`);
  }
}


// ── 5. 双主题的接线必须齐全 ──────────────────────────────────────────────────
// 少任何一处，都会出现「切了没生效」或者「刷新后变回老主题」的幽灵 bug：
// 首帧脚本负责不闪烁，layout 负责 SSR 默认值，provider 负责运行时同步。

const wiring = [
  ["web/src/lib/preferences/theme.ts", "SKIN_OPTIONS", "外观选项未定义"],
  ["web/src/lib/preferences/preferences-config.ts", "skin:", "偏好表里没有 skin 键"],
  ["web/src/lib/preferences/theme-utils.ts", "applySkin", "缺少 applySkin"],
  ["web/src/scripts/theme-boot.tsx", '"data-skin"', "首帧脚本没有写 data-skin（会闪烁 / 刷新后掉回老主题）"],
  ["web/src/app/layout.tsx", "data-skin={skin}", "<html> 上没有 data-skin（SSR 默认值缺失）"],
  ["web/src/stores/preferences/preferences-provider.tsx", "applySkin(", "provider 没有同步外观变化"],
  ["web/src/app/globals.css", "skin-editorial.css", "globals.css 没有引入外观层"],
];
// 用计数判断，不要用「错误文本里是否含 skin」判断：缺文件时的消息是
// 「缺少 web/src/lib/preferences/theme.ts」，里面一个 skin 都没有，
// 按文本匹配会在这七处全缺的情况下照样打印「接线完整」——守卫自己撒谎。
let wiringBroken = 0;
for (const [file, needle, msg] of wiring) {
  const src = read(join(root, file));
  if (!src) {
    errors.push(`缺少 ${file}`);
    wiringBroken++;
  } else if (!src.includes(needle)) {
    errors.push(`${msg}（${file}）`);
    wiringBroken++;
  }
}
if (wiringBroken === 0) {
  console.log("  ✓ 双主题接线完整（首帧 / SSR / provider / 样式层）");
}

// ── 汇总 ────────────────────────────────────────────────────────────────────
console.log("");
for (const w of warns) console.log(`  ! ${w}`);
if (errors.length) {
  for (const e of errors) console.error(`  ✗ ${e}`);
  console.error(`\n校验失败：${errors.length} 项`);
  process.exit(1);
}
console.log("校验通过：新增主题未越界，其余主题不受影响。");
