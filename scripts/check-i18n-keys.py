#!/usr/bin/env python3
"""校验页面里 t("key") 用到的键在 web/messages/zh.json 里真实存在。

为什么需要它：next-intl 在键缺失时不会抛错，而是把**键名本身**渲染到界面上，
并在控制台刷一条 MISSING_MESSAGE。这类缺陷编译期看不见、类型检查也看不见，
只在真机翻到那一页时才暴露——而新增页面往往一次加几十个键，人肉比对一定会漏。

它怎么判定：
  - 绑定点：`const t = useTranslations("taskDetail")` / `useTranslations("taskDetail.llm")`，
    按**变量名**记录命名空间，点号命名空间按路径下钻。
  - 调用点：`t("title")` / `tk("kpi.ports")`，取调用行之前、同名变量的**最近一次绑定**。
  - 变量名解析（不是「最近的绑定」）：同一页里 `t` 与 `tk` 常常是两个命名空间，
    只按行号取最近的一个会把所有调用都算到后绑定的那个头上。
  - `t` 作为 prop 传进子组件时（`function Foo({ t }: { t: Translator })`）无法静态判定
    归属，计入 skipped 并列出文件，**不报错**——宁可漏报，不可误报。

用法（与 CI 的其它 check-*.py 一致）：
    python3 -I scripts/check-i18n-keys.py            # 仓库根目录
    python3 -I scripts/check-i18n-keys.py --root DIR # 指定根目录（本地自测用）

退出码：0 = 无缺失；1 = 有缺失（CI 里属于 signals，只报告不拦发布）。
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

# Windows 上 Python 默认 stdout/stderr 是 cp1252（或中文系统上的 GBK），
# 打印中文校验结果会 UnicodeEncodeError。显式切到 UTF-8。
for _stream in (sys.stdout, sys.stderr):
    try:
        _stream.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass

# 绑定：const tk = useTranslations("assetDetail.kind")
BIND_RE = re.compile(
    r"""(?:const|let|var)\s+(\w+)\s*=\s*useTranslations\(\s*["']([A-Za-z0-9_.]+)["']\s*\)"""
)
# 调用：任意 `标识符("key")`，随后按标识符是否在绑定表里过滤。
CALL_RE = re.compile(r"""\b(\w+)\(\s*["']([A-Za-z0-9_.]+)["']""")
SKIP_DIRS = {"node_modules", ".next", "dist", "build", "out", ".git"}
SCAN_SUFFIXES = (".tsx", ".ts")


def iter_source_files(src: Path):
    for path in sorted(src.rglob("*")):
        if not path.is_file() or path.suffix not in SCAN_SUFFIXES:
            continue
        if any(part in SKIP_DIRS for part in path.relative_to(src).parts):
            continue
        yield path


def load_messages(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def dig(tree: dict, dotted: str):
    """按下钻路径取值；路径上任何一环不存在或不是对象都返回 None。"""
    cur = tree
    for part in dotted.split("."):
        if not isinstance(cur, dict) or part not in cur:
            return None
        cur = cur[part]
    return cur


def has_key(tree: dict, dotted: str) -> bool:
    """`a.b.c` 存在，或 `a.b` 本身就是一条消息（叶节点）都算命中。"""
    cur = tree
    parts = dotted.split(".")
    for i, part in enumerate(parts):
        if not isinstance(cur, dict) or part not in cur:
            return False
        cur = cur[part]
        if not isinstance(cur, dict) and i < len(parts) - 1:
            return False
    return True


def check_file(path: Path, messages: dict, display: Path):
    missing: list[tuple[int, str, str]] = []
    unresolved: list[tuple[int, str, str]] = []
    calls = 0

    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeDecodeError) as exc:
        return [], [(0, str(path), f"读取失败：{exc}")], 0

    bindings: list[tuple[int, str, str]] = []  # (行号, 变量名, 命名空间)
    for lineno, text in enumerate(lines, start=1):
        for var, ns in BIND_RE.findall(text):
            bindings.append((lineno, var, ns))
    if not bindings:
        return [], [], 0
    bound_names = {var for _, var, _ in bindings}

    for lineno, text in enumerate(lines, start=1):
        for var, key in CALL_RE.findall(text):
            if var not in bound_names:
                continue
            calls += 1
            owner = None
            for bline, bvar, ns in bindings:
                if bvar == var and bline <= lineno:
                    owner = ns
            if owner is None:
                # 变量来自 prop / 解构，静态定不了归属
                unresolved.append((lineno, var, key))
                continue
            tree = dig(messages, owner)
            if tree is None:
                missing.append((lineno, f"{owner}（整个命名空间缺失）", key))
            elif not has_key(tree, key):
                missing.append((lineno, owner, key))

    return missing, [(ln, var, key) for ln, var, key in unresolved], calls


def main() -> int:
    parser = argparse.ArgumentParser(description="校验 i18n 键是否齐备")
    parser.add_argument("--root", default=None, help="仓库根目录，默认为本文件上级目录")
    args = parser.parse_args()

    root = Path(args.root).resolve() if args.root else Path(__file__).resolve().parents[1]
    messages_path = root / "web" / "messages" / "zh.json"
    src = root / "web" / "src"

    if not messages_path.is_file():
        print(f"找不到词库：{messages_path}")
        return 1
    messages = load_messages(messages_path)

    files = list(iter_source_files(src))
    total_missing: list[tuple[Path, int, str, str]] = []
    total_unresolved: list[tuple[Path, int, str, str]] = []
    total_calls = 0
    checked = 0

    for path in files:
        missing, unresolved, calls = check_file(path, messages, path)
        if calls or missing:
            checked += 1
        total_calls += calls
        for lineno, ns, key in missing:
            total_missing.append((path, lineno, ns, key))
        for lineno, var, key in unresolved:
            total_unresolved.append((path, lineno, var, key))

    rel = lambda p: p.relative_to(root).as_posix()  # noqa: E731
    print(f"扫描 {len(files)} 个源文件，其中 {checked} 个含翻译调用，共 {total_calls} 处")

    if total_missing:
        print(f"\n缺少 {len(total_missing)} 个键：")
        for path, lineno, ns, key in total_missing:
            print(f"  {rel(path)}:{lineno}  {ns}.{key}")

    if total_unresolved:
        by_file: dict[Path, int] = {}
        for path, _, _, _ in total_unresolved:
            by_file[path] = by_file.get(path, 0) + 1
        print(f"\n跳过 {len(total_unresolved)} 处（变量由 prop 传入，静态无法判定归属）：")
        for path, n in sorted(by_file.items(), key=lambda kv: -kv[1]):
            print(f"  {rel(path)}  {n} 处")

    if total_missing:
        print("\n结论：存在缺失键。补齐 web/messages/zh.json 后重跑。")
        return 1
    print("\n结论：未发现缺失键。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
