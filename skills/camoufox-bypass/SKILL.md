---
name: camoufox-bypass
description: 'Bypasses Cloudflare Turnstile / Managed Challenge (the orange cloud check) by driving the Camoufox fingerprint browser once to solve the challenge, then extracting the cf_clearance cookie and reusing it across high-concurrency HTTP requests. Use this when a target returns 403 + cf-mitigated header, or when the page title contains "Just a moment...". Do NOT launch a browser for every request - solve once, reuse the cookie.',
domain: cybersecurity
subdomain: evasion
tags:
- cloudflare
- turnstile
- camoufox
- cf_clearance
- waf-bypass
- fingerprint-browser
version: 1.0.0
author: artex
license: Apache-2.0
nist_csf:
- DE.AE-07
mitre_attack:
- T1090
- T1587.003
---

# Camoufox · Cloudflare Turnstile Bypass

## When to Use

Only trigger this skill when the target shows **one of the following**:

- HTTP 403 / 503 with a `cf-mitigated: challenge` header
- HTML `<title>` is exactly `Just a moment...` or `Attention Required! | Cloudflare`
- HTML contains `<div id="challenge-running">` or `<div class="cf-turnstile">`
- A `Set-Cookie: cf_clearance=...` is present in the response, meaning a prior solve was successful

**Do NOT use** for:

- Targets that are simply down (5xx without CF markers)
- WAFs that are not Cloudflare (Akamai, DataDome, Imperva — different solve flow)
- Every single HTTP request. Launch Camoufox **once**, extract the cookie, then fan out with `curl_cffi` / `requests`.

## Prerequisites

- `python -m pip install -U "camoufox[geoip]" pylcaptcha curl_cffi`
- `python -m camoufox fetch` (downloads the Firefox-based browser binary)
- The user has already installed Camoufox via Settings → "自动化浏览器 · Camoufox"
- **Global egress proxy** is configured in Settings → "全局代理". Turnstile validates that
  the browser's IP matches the eventual API caller's IP. If proxy is set, install AND solve
  must both use it. This skill assumes that inheritance is already handled by ARTEX.

## Workflow

### Step 1: Confirm It Is Actually Cloudflare

Before launching a browser, verify the challenge signature with one cheap request:

```bash
curl -sI -A "Mozilla/5.0 (Windows NT 10.0; Win64; x64)" https://target.example.com/
```

Look for `cf-mitigated: challenge` or `server: cloudflare` + `HTTP/2 403`.

### Step 2: Solve Once, Get the Cookie

Run the provided solver script:

```bash
python skills/camoufox-bypass/scripts/solve_cf.py \
  --url "https://target.example.com/" \
  --out "/path/to/task/workspace/cf_jar.json" \
  --proxy "$ARTEX_GLOBAL_PROXY"   # optional, inherits from settings
```

The script does:

1. Launches Camoufox with `geoip=True` (auto-aligns timezone/locale to proxy IP).
2. Navigates to the URL and waits for either the Turnstile iframe to disappear
   or `cf_clearance` to appear in the cookie jar.
3. Writes the full cookie jar (JSON) to `--out` for downstream reuse.
4. Prints the `cf_clearance` value on stdout so the agent can log it.

### Step 3: Reuse the Cookie at High Concurrency

Once you have the cookie, **stop using the browser**. Use `curl_cffi` which mimics
the same TLS/JA3 fingerprint as Chrome, so Cloudflare sees a consistent client:

```python
from curl_cffi import requests as creq
import json

jar = json.load(open("/path/to/cf_jar.json"))
cookies = {c["name"]: c["value"] for c in jar if c.get("domain", "").endswith(".example.com")}

r = creq.get("https://target.example.com/api/foo",
             cookies=cookies,
             impersonate="chrome124",
             proxies={"https": "http://user:pass@proxy:8080"},  # same as solver
             timeout=15)
print(r.status_code, r.text[:200])
```

You can now fire hundreds of concurrent requests through `asyncio` or a thread pool
using the **same cookie** — Turnstile does not re-challenge per request, only per IP
at the CDN edge with a TTL of ~30 minutes.

### Step 4: Refresh When Expired

If requests start returning 403 + `cf-mitigated` again, the cookie expired. Re-run
`solve_cf.py` to get a fresh one. Typical TTL is 30 minutes, sometimes shorter under
aggressive bot scoring.

## Failure Modes & Escalation

| Symptom | Likely cause | Next step |
|---|---|---|
| Solver times out after 60s | IP is datacenter-flagged by Cloudflare | Switch to residential proxy in Settings → 全局代理, retry |
| Solver returns but 403 persists | Target re-challenges on every request (aggressive mode) | Slow down requests; add human-like delays; rotate UA per request |
| Solver returns different cookie than requests use | Proxy mismatch between solver and caller | Verify both inherit the same `ARTEX_GLOBAL_PROXY` |
| Turnstile iframe never disappears | Shadow-DOM managed challenge | Camoufox by itself may not handle this; escalate to human |

## Reference

See `references/bypass_guide.md` for:

- The full Cloudflare challenge taxonomy (JS challenge vs Turnstile vs Managed)
- How `cf_clearance` is validated (IP + UA + TLS + time window)
- When to escalate to manual solving
- Concrete payload examples for `curl_cffi` impersonation presets
