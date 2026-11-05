#!/usr/bin/env python3
"""校验仓库自带的启动脚本本身没有被写坏。

历史事故：`start-lan.sh` 曾被 base64 之后的文本覆盖——git 里存的是一行
`QGVjaG8gb2Zm...`，执行只会得到“不是内部或外部命令”。它藏了好几个版本没人
发现，因为打包脚本只做 cp，坏文件原样进了发布包，而没人真的去跑一次。

Windows 的 `start.bat` / `start-lan.bat` 已经下线：入口程序本身就是
`artex-hydra.exe`，双击即起 Web 控制台，自动重启也内置在入口的监护循环里，
外层 bat 只会多出一层「找不到可执行文件 / 版本漂移」的故障面。因此这里只剩
两个 .sh 需要校验。

`.sh` 的最低条件：无 BOM、LF、首行为解释器（`#!`）。base64 文本是一整行、
没有换行，第一条规则就会把它拦下来。
"""


from __future__ import annotations

import sys
from pathlib import Path

# Windows 上 Python 默认 stdout/stderr 是 cp1252（或中文系统上的 GBK），
# 打印中文校验结果会 UnicodeEncodeError。显式切到 UTF-8（Python 3.7+ 的
# TextIOWrapper.reconfigure；不支持时退化为不处理）。
for _stream in (sys.stdout, sys.stderr):
    try:
        _stream.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass

ROOT = Path(__file__).resolve().parents[1]
BOM = b"\xef\xbb\xbf"


def check_sh(path: Path) -> list[str]:
    raw = path.read_bytes()
    errs: list[str] = []
    if raw.startswith(BOM):
        errs.append("带 UTF-8 BOM：内核会找不到解释器（Bad interpreter）")
        raw = raw[len(BOM):]
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError as exc:
        return errs + [f"不是合法 UTF-8：{exc}"]
    if "\r\n" in text:
        errs.append("含 CRLF：CRLF 会让 shebang 变成 /bin/sh\\r，脚本无法执行")
    first = text.split("\n", 1)[0]
    if not first.startswith("#!"):
        errs.append(f"首行必须是 #! 解释器，实际是 {first[:60]!r}")
    return errs


def main() -> int:
    # Windows 不再随包分发 .bat：入口程序自己就是 artex-hydra.exe，双击即起
    # Web 控制台，自动重启也已内置在入口里（见 selfupdate.Handoff 的监护循环），
    # 外层 bat 只会引入「找不到可执行文件 / 版本漂移」这类额外故障面。
    targets = [
        (ROOT / "start.sh", check_sh),
        (ROOT / "start-lan.sh", check_sh),
    ]
    failed = 0
    for path, fn in targets:
        if not path.exists():
            print(f"[start-scripts] 缺少 {path.name}", file=sys.stderr)
            failed += 1
            continue
        errs = fn(path)
        for e in errs:
            print(f"[start-scripts] {path.name}: {e}", file=sys.stderr)
        if errs:
            failed += 1
    if failed:
        print(f"[start-scripts] {failed} 个启动脚本不可用", file=sys.stderr)
        return 1
    print("[start-scripts] 2 个启动脚本格式正常")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
