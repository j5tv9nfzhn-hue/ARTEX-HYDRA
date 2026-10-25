# Cloudflare Bypass Reference

## 挑战类型概览

Cloudflare 会对可疑请求返回三种不同的人机验证，处理方式略有差异：

| 类型 | 触发条件 | 页面特征 | 我们能否自动过 |
|---|---|---|---|
| **JS Challenge** | 低风险但指纹可疑 | `<title>Just a moment...</title>` | Camoufox 指纹到位时**自动通过**，无需交互 |
| **Turnstile (Interactive)** | 中高风险 | 页面内嵌 `<div class="cf-turnstile">` | Camoufox + `pylcaptcha` 通常可过 |
| **Managed Challenge** | 高风险 / 数据中心 IP | Shadow DOM 中的不可见挑战 | **最顽固**，经常需要人工或住宅代理 |

## cf_clearance 是怎么被验证的？

Cloudflare 签发 `cf_clearance` 时会绑定以下上下文：

- **出口 IP**：换代理 → 立即失效（这也是为什么安装、求解、发包必须**同一个代理**）
- **User-Agent**：浏览器的 UA 必须与签发时一致
- **TLS 指纹（JA3/JA4）**：`curl_cffi` 的 `impersonate` 就是为对齐这个而设计的
- **时间窗口**：通常 30 分钟有效，激进场景下可能缩短到 5 分钟

**因此**：任何一个维度的不一致都会导致 CF 认为你「伪造 cookie」，直接重挑战。

## 标准战术流程（ARTEX 推荐）

```
┌─────────────┐     ┌──────────────────┐     ┌────────────────────┐
│ 1. 一次探测 │ ──▶ │ 2. Camoufox 求解 │ ──▶ │ 3. curl_cffi 并发  │
│  (curl -I)  │     │  (拿 cf_clearance)│     │  (复用 cookie)      │
└─────────────┘     └──────────────────┘     └────────────────────┘
                                                    │
                                                    ▼
                                           ┌────────────────┐
                                           │ 30 分钟后刷新  │
                                           └────────────────┘
```

### 步骤 1：廉价探测

不要一上来就开浏览器。先发一个 `curl -I`：

```bash
curl -sI -A "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 \
  (KHTML, like Gecko) Chrome/124.0 Safari/537.36" https://target/
```

关注响应头：

- `server: cloudflare`
- `cf-mitigated: challenge`
- `cf-ray: <id>`

如果没看到这几个，说明根本不是 Cloudflare 的锅，别走这条路。

### 步骤 2：Camoufox 求解

```bash
python skills/camoufox-bypass/scripts/solve_cf.py \
  --url "https://target/" \
  --out "$WORKDIR/cf_jar.json"
```

成功时 stdout 输出 `cf_clearance` 的值，exit code 0。

### 步骤 3：curl_cffi 并发

```python
import asyncio, json
from curl_cffi.requests import AsyncSession

async def probe(session, url, cookies):
    async with session.get(url, cookies=cookies, impersonate="chrome124") as r:
        return r.status, url

async def main():
    jar = json.load(open("cf_jar.json"))
    cookies = {c["name"]: c["value"] for c in jar}
    urls = [f"https://target/api/item/{i}" for i in range(100)]
    async with AsyncSession(impersonate="chrome124") as s:
        results = await asyncio.gather(*[probe(s, u, cookies) for u in urls])
    print(results)

asyncio.run(main())
```

## 常见坑

### 坑 1：代理不一致

求解时用了 A 代理，发包时忘了带代理（走直连）。CF 看到 cookie 的 IP 与请求 IP 不符，直接重挑战。**解决**：整个任务里 `ARTEX_GLOBAL_PROXY` 保持不变。

### 坑 2：TLS 指纹泄露

用 `requests` 库带 cookie 试试？大概率还是 403。因为 `requests` 的 TLS 指纹不是 Chrome。**必须用 `curl_cffi` + `impersonate="chrome124"`**。

### 坑 3：UA 不一致

Camoufox 默认用 Firefox 的 UA。如果你在 `curl_cffi` 里用 `impersonate="chrome124"`，CF 会看到 UA=Chrome 但 cookie 签发时 UA=Firefox。**解决**：在求解脚本里显式设置 `userAgent` 覆盖为 Chrome UA；或全程用 Firefox 风格（`impersonate="firefox133"`）。

### 坑 4：住宅 vs 机房

机房 IP（AWS / Vultr / DigitalOcean）被 CF 标记的概率极高。即使是 Camoufox 也可能被 Managed Challenge。**解决**：住宅代理是终极解。

## 何时该放弃自动化

出现以下任一信号，应停止尝试并记录到证据库，由人类介入：

- 连续 3 次求解超时
- 求解成功但 cookie 立即失效（30 秒内）
- Shadow DOM 中的 Managed Challenge（Camoufox 尚不支持）
- 需要短信 / 邮箱二次验证

这些都应该作为「受阻」事实写回，让 Planner 决策是换目标、换代理、还是人工介入。
