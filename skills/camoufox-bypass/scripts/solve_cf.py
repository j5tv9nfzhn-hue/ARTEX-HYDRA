#!/usr/bin/env python3
"""Cloudflare Turnstile solver using Camoufox. Prints cf_clearance on stdout and
writes the full cookie jar to --out for downstream reuse.

Usage:
    python solve_cf.py --url https://target.example.com/ --out /path/cf_jar.json
    python solve_cf.py --url ... --out ... --proxy http://user:pass@host:port
    python solve_cf.py --url ... --out ... --headless   # default is headless

Exit codes:
    0 = solved
    2 = challenge present but not solved before timeout
    3 = Camoufox not installed
    4 = navigation error
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
from urllib.parse import urlparse


def parse_proxy(raw: str | None):
    """Convert a proxy URL to Camoufox (Playwright) proxy dict."""
    if not raw:
        return None
    u = urlparse(raw if "://" in raw else f"http://{raw}")
    if not u.hostname:
        return None
    entry = {"server": f"{u.scheme}://{u.hostname}:{u.port or 8080}"}
    if u.username:
        entry["username"] = u.username
    if u.password:
        entry["password"] = u.password
    return entry


def find_cf_clearance(cookies):
    for c in cookies:
        if c.get("name") == "cf_clearance":
            return c.get("value", "")
    return ""


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", required=True)
    ap.add_argument("--out", required=True, help="path to write cookie jar JSON")
    ap.add_argument("--proxy", default=os.environ.get("ARTEX_GLOBAL_PROXY", ""))
    ap.add_argument("--timeout", type=int, default=90)
    ap.add_argument("--headless", action="store_true", default=True)
    ap.add_argument("--headed", dest="headless", action="store_false")
    args = ap.parse_args()

    try:
        from camoufox.sync_api import Camoufox
        from camoufox.addons import DefaultAddons
    except ImportError:
        print("error: camoufox not installed. Run: pip install -U \"camoufox[geoip]\"",
              file=sys.stderr)
        return 3

    proxy = parse_proxy(args.proxy)
    # exclude_addons=[DefaultAddons.UBO] 禁用默认 uBlock Origin 下载：
    # Camoufox 默认会从 addons.mozilla.org 拉取 ~5MB UBO 插件，首次启动在网络
    # 受限环境会卡死。过盾不需要 uBlock，用 exclude_addons 显式排除。
    # 注意：不能传 addons=[]（那是「追加自定义插件」，不影响默认插件下载）。
    kwargs = {"headless": args.headless, "geoip": True,
              "exclude_addons": [DefaultAddons.UBO]}
    if proxy:
        kwargs["proxy"] = proxy

    try:
        with Camoufox(**kwargs) as browser:
            ctx = browser.contexts[0] if browser.contexts else browser.new_context()
            page = ctx.new_page()
            try:
                page.goto(args.url, timeout=60000, wait_until="domcontentloaded")
            except Exception as e:
                print(f"error: navigation failed: {e}", file=sys.stderr)
                return 4

            deadline = time.time() + args.timeout
            cf = ""
            while time.time() < deadline:
                cookies = ctx.cookies()
                cf = find_cf_clearance(cookies)
                if cf:
                    break
                # Also check if the page has settled (no more challenge markers).
                try:
                    title = page.title() or ""
                except Exception:
                    title = ""
                if title and "Just a moment" not in title and "Attention Required" not in title:
                    # No CF challenge on the page and no cookie either — might be a non-CF
                    # target, or the challenge was already passed via UA fingerprint.
                    time.sleep(2)
                    cookies = ctx.cookies()
                    cf = find_cf_clearance(cookies)
                    break
                time.sleep(1)

            cookies = ctx.cookies()
            if not cf:
                cf = find_cf_clearance(cookies)

            os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
            with open(args.out, "w", encoding="utf-8") as f:
                json.dump(cookies, f, ensure_ascii=False, indent=2)

            if cf:
                print(cf)
                print(f"info: cookie jar written to {args.out}", file=sys.stderr)
                return 0
            print("error: challenge not solved before timeout", file=sys.stderr)
            return 2
    except Exception as e:
        print(f"error: {e}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    sys.exit(main())
