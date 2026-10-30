#!/usr/bin/env python3
"""生成 Release 更新清单 latest.json。

为什么需要它：客户端的「自动检测新版本」默认开启。如果每次都打 REST API
（api.github.com/repos/<repo>/releases/latest），未认证额度只有 60 次/小时/IP，
公司内网一个出口后面几十台机器就会互相打爆；而
https://github.com/<repo>/releases/latest/download/latest.json 是网页别名，
不计入 API 配额，所以每个 Release 都要挂一份固定名字的清单。

清单字段与 GitHub Release 同构，selfupdate.FetchLatestManifest 直接反序列化：
  tag_name / name / body / draft / prerelease / published_at / html_url / assets[]
assets[] 里带 name / size / browser_download_url（Go 侧缺 URL 时会自己拼）。

用法（见 .github/workflows/release.yml）：
  python3 scripts/gen-update-manifest.py --dist dist --repo owner/name \
      --tag v1.2.3 --notes dist/notes.md --published-at 2026-01-01T00:00:00Z
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

# 客户端 parseVersion 只接受纯净数字段：带 -rc.1 / -ko.1 / -global.1 后缀的 tag
# 会被判成「不可比较」，所有客户端的一键更新都会静默禁用。所以这里直接拒绝。
#
# 段数接受 3 或 4：第四段是「同一个 patch 重发的微修订」（2.4.0 -> 2.4.0.1）。
TAG_RE = re.compile(r"^v\d+\.\d+\.\d+(\.\d+)?$")

# ---------------------------------------------------------------- 产物单一性
#
# 本仓库只发布 **Web 版**（artex-rea-<版本>-<os>-<arch>.zip），因此清单里
# **只允许**出现这六种平台包加 SHA256SUMS。任何别的 zip（历史的桌面端
# artex-hydra-desktop-*、构建残留、手工塞进来的文件）都会被拒绝，而不是
# 悄悄写进 assets[]。
#
# 为什么必须白名单而不是过滤：清单是客户端的**授权来源**——本机该装的包名
# 由 selfupdate.AssetName 按 entryStem 拼出，再到这里 FindAsset 精确匹配。
# 只要 assets[] 里混进别的产物族，就等于承认它「也是本仓库的正式发行物」，
# 下游任何按前缀/模糊匹配的消费方都会被带偏。
WEB_STEM = "artex-rea"
PLATFORMS = (
    ("linux", "amd64"),
    ("linux", "arm64"),
    ("darwin", "amd64"),
    ("darwin", "arm64"),
    ("windows", "amd64"),
    ("windows", "arm64"),
)


def expected_assets(version: str) -> set[str]:
    """返回本版本允许出现在清单里的全部资产名（六端 zip + SHA256SUMS）。"""
    names = {f"{WEB_STEM}-{version}-{os_}-{arch}.zip" for os_, arch in PLATFORMS}
    names.add("SHA256SUMS")
    return names


def sha256_of(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def main() -> int:
    ap = argparse.ArgumentParser(description="生成 Release 更新清单 latest.json")
    ap.add_argument("--dist", required=True, help="存放 *.zip 与 SHA256SUMS 的目录")
    ap.add_argument("--repo", required=True, help="owner/name")
    ap.add_argument("--tag", required=True, help="必须形如 v1.2.3 或 v1.2.3.4")
    ap.add_argument("--notes", default="", help="Release 说明正文文件（可省略）")
    ap.add_argument("--published-at", default="", help="RFC3339 发布时间（可省略）")
    ap.add_argument("--out", default="", help="输出路径，默认 <dist>/latest.json")
    args = ap.parse_args()

    if not TAG_RE.match(args.tag):
        print(
            f"拒绝发布：tag {args.tag!r} 不是 vX.Y.Z / vX.Y.Z.W 形式。\n"
            "客户端只认纯数字段版本号（三或四段），带后缀的 tag 会让所有客户端判定为不可比较并禁用一键更新。",
            file=sys.stderr,
        )
        return 2

    dist = Path(args.dist)
    version = args.tag[1:] if args.tag.startswith("v") else args.tag
    allowed = expected_assets(version)

    # 先看盘上有什么，再按白名单对账。任何"不在名单里"的 zip 都视为污染：
    # 直接失败，而不是过滤后继续——静默丢弃会让人以为发布是干净的，实则
    # Release 上仍挂着别族的包，下次谁去扫 dist 又会把它捡回来。
    present = {p.name for p in dist.glob("*.zip")}
    unexpected = sorted(present - allowed)
    if unexpected:
        print(
            "拒绝发布：以下产物不属于本仓库的 Web 版发布族，请先从 dist 移除：\n"
            + "\n".join(f"  - {n}" for n in unexpected)
            + f"\n\n本仓库只发布 {WEB_STEM}-<版本>-<os>-<arch>.zip（三平台 × amd64/arm64）。",
            file=sys.stderr,
        )
        return 2

    missing = sorted(allowed - present - {"SHA256SUMS"})
    if missing:
        print(
            "拒绝发布：缺少以下平台包，清单会不完整：\n"
            + "\n".join(f"  - {n}" for n in missing),
            file=sys.stderr,
        )
        return 2

    zips = [dist / n for n in sorted(present)]

    # SHA256SUMS 由 release.yml 的 sha256sum 生成；没有就现算，保证两份口径一致。
    # 只接受白名单内的条目：校验和文件若混入别族产物，说明打包环节已经脏了，
    # 这里静默忽略等于把问题藏起来，所以直接拒绝。
    sums: dict[str, str] = {}
    sums_file = dist / "SHA256SUMS"
    if sums_file.exists():
        stray_sums = []
        for line in sums_file.read_text(encoding="utf-8").splitlines():
            parts = line.split()
            if len(parts) != 2:
                continue
            name = parts[1].lstrip("*")
            if name not in allowed:
                stray_sums.append(name)
                continue
            sums[name] = parts[0]
        if stray_sums:
            print(
                "拒绝发布：SHA256SUMS 里含有非 Web 版产物：\n"
                + "\n".join(f"  - {n}" for n in sorted(stray_sums)),
                file=sys.stderr,
            )
            return 2

    assets = []
    for z in zips:
        assets.append(
            {
                "name": z.name,
                "size": z.stat().st_size,
                "sha256": sums.get(z.name) or sha256_of(z),
                "browser_download_url": f"https://github.com/{args.repo}/releases/download/{args.tag}/{z.name}",
            }
        )

    # 客户端「清单优先」的检测通道会拿 assets[] 去找 SHA256SUMS 做完整性校验。
    # 清单只列 zip 的话，升级会在校验阶段报「缺少 SHA256SUMS」并拒绝更新——
    # 文件其实就挂在 Release 上，纯粹是清单没写。这里补进去，两条通道口径一致。
    if sums_file.exists():
        assets.append(
            {
                "name": sums_file.name,
                "size": sums_file.stat().st_size,
                "browser_download_url": f"https://github.com/{args.repo}/releases/download/{args.tag}/{sums_file.name}",
            }
        )

    body = ""
    if args.notes:
        notes_path = Path(args.notes)
        if notes_path.exists():
            body = notes_path.read_text(encoding="utf-8", errors="replace").strip()

    # Go 侧 published_at 是 time.Time，空串会让整个清单解码失败，必须给一个合法值。
    published_at = args.published_at.strip() or datetime.now(timezone.utc).isoformat(timespec="seconds")

    manifest = {
        "tag_name": args.tag,
        "name": args.tag,
        "body": body,
        "draft": False,
        "prerelease": False,
        "published_at": published_at,
        "html_url": f"https://github.com/{args.repo}/releases/tag/{args.tag}",
        "assets": assets,
    }

    # 写盘前的最后一道闸：assets[] 里每一笔都必须是白名单内的名字。
    # 前面已按文件名对过账，这里防的是将来有人改了上面的拼装逻辑而漏掉过滤。
    stray = sorted(a["name"] for a in manifest["assets"] if a["name"] not in allowed)
    if stray:
        print(
            "拒绝发布：清单里出现了非 Web 版资产（内部错误）：\n"
            + "\n".join(f"  - {n}" for n in stray),
            file=sys.stderr,
        )
        return 2

    out = Path(args.out) if args.out else dist / "latest.json"
    out.write_text(json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(
        f"已生成 {out}：tag={args.tag}，{len(assets)} 个资产"
        f"（{len(zips)} 个 {WEB_STEM} 平台包 + SHA256SUMS）"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
