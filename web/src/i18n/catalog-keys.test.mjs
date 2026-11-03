// 词库键覆盖检查：源码里 useTranslations("ns") 之后用到的每个 t("key") 都必须在
// messages/zh.json 里存在。
//
// 为什么要有这条测试：next-intl 在运行时才解析键，缺键只会在浏览器控制台刷
// MISSING_MESSAGE 并渲染出空白/原始键名。它不会让 tsc 或 biome 失败，所以「编译
// 通过」不代表界面文案是对的——第六轮就因此让对话页列表行整行报错。
//
// 只做静态字面量扫描：模板拼出来的键（t(`a.${x}`)）无法静态判定，跳过；
// 条件拼 key 的地方本来就该显式列全。
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const ROOT = fileURLToPath(new URL("../..", import.meta.url));
const SRC = join(ROOT, "src");
const CATALOG = JSON.parse(readFileSync(join(ROOT, "messages/zh.json"), "utf8"));

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (full.endsWith(".tsx") || full.endsWith(".ts")) out.push(full);
  }
  return out;
}

function hasKey(path, key) {
  let node = CATALOG;
  for (const part of [...path.split("."), ...key.split(".")]) {
    if (!part) continue;
    if (typeof node !== "object" || node === null || !(part in node)) return false;
    node = node[part];
  }
  return true;
}

// 收集每个文件里 `const t = useTranslations("...")` 的命名空间绑定。
// 同名变量在不同组件里可能绑不同命名空间，所以按变量名存集合，命中任一即算存在。
function bindings(src) {
  const map = new Map();
  const add = (name, ns) => map.set(name, new Set([...(map.get(name) ?? []), ns]));
  for (const m of src.matchAll(/(\w+)\s*=\s*useTranslations\(\s*"([\w.]+)"\s*\)/g)) add(m[1], m[2]);
  for (const m of src.matchAll(/(\w+)\s*=\s*useTranslations\(\s*\)/g)) add(m[1], "");
  for (const m of src.matchAll(/(\w+)\s*=\s*await\s+getTranslations\(\s*"([\w.]+)"\s*\)/g)) add(m[1], m[2]);
  return map;
}

test('源码里用到的所有 t("字面量键") 都存在于 messages/zh.json', () => {
  const missing = [];
  for (const file of walk(SRC)) {
    const src = readFileSync(file, "utf8");
    const bound = bindings(src);
    if (bound.size === 0) continue;
    for (const m of src.matchAll(/\b(\w+)\(\s*"([\w.-]+)"\s*[,)]/g)) {
      const [, name, key] = m;
      const namespaces = bound.get(name);
      if (!namespaces) continue;
      const ok = [...namespaces].some((ns) => hasKey(ns, key));
      if (!ok) missing.push(`${file.slice(SRC.length + 1)}: t("${key}") [${[...namespaces].join("|")}]`);
    }
  }
  assert.deepEqual(missing, []);
});
