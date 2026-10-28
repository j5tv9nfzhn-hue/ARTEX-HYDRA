#!/usr/bin/env node
/**
 * audit-density.mjs —— 前端「密度 / 科技感」体检
 *
 * 背景：这套控制台的阅读疲劳不是主观感受，可以量化。
 * 本脚本统计 web/src 下五类信号，输出一张分数表与最重的 Top-N 文件。
 *
 * 用法：
 *   node scripts/audit-density.mjs            # 默认扫 web/src
 *   node scripts/audit-density.mjs web/src    # 指定目录
 *   node scripts/audit-density.mjs web/src --top 20
 *
 * 各指标的健康阈值（本脚本据此给分）：
 *   text-xs 占比      每个 text-xs 都是"正文用 12px"的信号 → 越少越好
 *   uppercase/tracking 中文界面里几乎无正当用途 → 应为 0
 *   shadow-*          层级应由描边与留白承担 → 趋近 0
 *   animate-*         循环动效是焦虑源 → 只允许功能性动效
 *   硬编码色相         应走语义令牌 → 趋近 0
 */
import fs from "node:fs";
import path from "node:path";

const argv = process.argv.slice(2);
const root = path.resolve(argv.find((a) => !a.startsWith("--")) ?? "web/src");
const topN = Number(
  (() => {
    const i = argv.indexOf("--top");
    return i >= 0 ? argv[i + 1] : 12;
  })(),
);

const files = [];
(function walk(dir) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (["node_modules", ".next", "out", "dist"].includes(e.name)) continue;
      walk(p);
    } else if (/\.(tsx?|jsx?)$/.test(e.name)) files.push(p);
  }
})(root);

const RULES = {
  "text-xs": /text-xs\b/g,
  "text-[10px]|11px": /text-\[(10|11)px\]/g,
  uppercase: /uppercase/g,
  "tracking-wide+": /tracking-(widest|wider|wide)/g,
  "shadow-*": /shadow-(sm|md|lg|xl|2xl|xs)\b/g,
  "animate-*": /animate-/g,
  LiveDot: /LiveDot/g,
  "硬编码色相": /-(emerald|amber|rose|orange|red|blue|violet|indigo|teal|cyan|sky|green|yellow|slate|zinc)-(400|500|600)\b/g,
  "chart-*": /chart-[1-5]/g,
};

const totals = {};
for (const k of Object.keys(RULES)) totals[k] = { n: 0, files: new Set() };
const perFile = {};

for (const f of files) {
  const src = fs.readFileSync(f, "utf8");
  for (const [k, re] of Object.entries(RULES)) {
    const m = src.match(re);
    if (!m) continue;
    totals[k].n += m.length;
    totals[k].files.add(f);
    (perFile[f] ??= {})[k] = m.length;
  }
}

const pad = (s, n) => String(s).padEnd(n);
const padL = (s, n) => String(s).padStart(n);

console.log(`\n扫描 ${files.length} 个文件 @ ${path.relative(process.cwd(), root) || "."}\n`);
console.log(pad("指标", 18) + padL("处数", 7) + padL("文件数", 8) + "   读法");
console.log("-".repeat(78));
const HINT = {
  "text-xs": "正文正在用 12px",
  "text-[10px]|11px": "更极端的微型字",
  uppercase: "中文界面无正当用途",
  "tracking-wide+": "同上，装饰性字距",
  "shadow-*": "层级应交给描边/留白",
  "animate-*": "只该留功能性动效",
  LiveDot: "常驻脉冲 = 焦虑源",
  "硬编码色相": "应走语义令牌",
  "chart-*": "数据系列，可保留灰阶",
};
for (const [k, v] of Object.entries(totals)) {
  console.log(pad(k, 18) + padL(v.n, 7) + padL(v.files.size, 8) + "   " + (HINT[k] ?? ""));
}

const rows = Object.entries(perFile)
  .map(([f, o]) => [f, Object.values(o).reduce((a, b) => a + b, 0), o])
  .sort((a, b) => b[1] - a[1])
  .slice(0, topN);

console.log(`\n最重的 ${topN} 个文件`);
console.log("-".repeat(78));
for (const [f, sum, o] of rows) {
  console.log(`${padL(sum, 5)}  ${path.relative(root, f).replace(/\\/g, "/")}`);
  console.log(`       ${Object.entries(o).map(([k, n]) => `${k}×${n}`).join("  ")}`);
}
console.log();
