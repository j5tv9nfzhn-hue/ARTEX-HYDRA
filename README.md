# ARTEX-HYDRA 简体中文版

**由 LLM 多 Agent 自主执行渗透测试与自适应编排的系统**（Go 后端 + Next.js 前端）

界面、报告、摘要、对话及后端消息**统一使用简体中文**，本仓库不再提供其他语言界面。

[![release](https://github.com/j5tv9nfzhn-hue/ARTEX-HYDRA/actions/workflows/release.yml/badge.svg)](https://github.com/j5tv9nfzhn-hue/ARTEX-HYDRA/actions/workflows/release.yml) [![detections](https://github.com/j5tv9nfzhn-hue/ARTEX-HYDRA/actions/workflows/detections.yml/badge.svg?branch=main)](https://github.com/j5tv9nfzhn-hue/ARTEX-HYDRA/actions/workflows/detections.yml) [![license: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue.svg)](LICENSE)

---

## 🚨 使用范围与法律告知

**仅允许在自有或明确授权的环境中，以学习、防御和检测为目的使用。**

ARTEX 可自主组织侦察、工具执行和多阶段测试，滥用可能造成严重损害。

- **未经授权的使用可能构成犯罪。** 在中华人民共和国大陆地区，未经授权侵入、非法控制计算机信息系统，可能触犯《刑法》第二百八十五条、第二百八十六条；同时涉及《网络安全法》《数据安全法》《个人信息保护法》。跨境使用还需遵守行为地与数据主体所在地的法律。
- **不要将他人的真实服务作为实验目标。** 请使用自有、本地隔离的 OWASP Juice Shop、DVWA 等有意设计的测试环境；真实目标必须事先取得书面许可，并约定范围、时间窗口与数据处理方式。
- **从防御视角阅读。** 见[防御与检测](#防御与检测)：随仓库发布的 [`detections/`](detections) 提供 Sigma、Suricata、ATT&CK 映射与指标，可直接部署到 SIEM。

### 关于来源与真实滥用事件

ARTEX 由国内开发者 **Autumn-27** 创建并开源（[Autumn-27/ARTEX](https://github.com/Autumn-27/ARTEX)），Agent 能力依赖其 [norma](https://github.com/Autumn-27/norma) SDK。本仓库是该项目的韩语本地化分支 `jiwoochris/artex-ko` 的简体中文分支。

2026 年 10 月，韩国多家媒体（经由本项目韩语分支的安全告知记录）报道称，ARTEX 被用于针对韩国境内金融机构的个人信息窃取攻击，相关刑事调查在进行中。**本仓库仅转述并保留该记录，不代表对其细节的独立核实。** 保留这段记录的唯一目的，是让防御方理解自主 AI 攻击的真实后果，并据此建立检测与阻断能力。

本仓库不提供任何攻击方法、目标清单或利用建议，只保留技术说明、防御规则与检测能力。若不同意[许可证与免责声明](#许可证与免责声明)，请勿下载或使用。

---

## 防御与检测

本仓库面向防御方发布检测能力。以下内容用于**防御与检测自有或获得明确书面授权的系统**。检测指标均基于本仓库源码实际存在的字符串或行为，不是猜测；也不得把泛化行为误称为 ARTEX 专属指纹。

### 目标侧：流量指纹与行为特征

| 指标 | 来源 | 说明 |
| --- | --- | --- |
| `artex-enrich/1.0` | [`enrich/enrich.go`](enrich/enrich.go) | 资产补全直接发送 GET，非 LLM 生成；多资产短连接、单 GET、读取标题即断开。UA 可改，缺失不代表安全 |
| `norma/0.4` | norma SDK `WebFetch` | SDK 默认 UA，非 Bash 外部工具。明文或 TLS 终结处可见；其他 SDK 使用者同样存在，仅辅助线索 |

静态指纹有限，关键在**行为**：同一（或少量轮换）来源在短间隔内跨侦察 → 枚举 → 参数探测 → 认证/注入阶段；请求按状态码与响应长度**系统性调整**而非随机变异；在 401/403/429 之后仍持续变换尝试；非工作时段长时连续。轮间可以暂停，只看短时请求量不足。

### 运营者与取证侧：运行本系统的主机

用于调查疑似安装或执行过本系统的主机，**不可当作目标侧指标**：

- **默认 `127.0.0.1:8787`**：管理 UI 端口（`cmd/artex/main.go`）。
- **默认 `127.0.0.1:8788`**：记录代理端口，会生成 `mitmproxy-ca-cert.pem`。代理与 CA 同时存在比单端口更特异。
- **`artex-selfupdate`**：自更新查询 GitHub Release 的出站 UA（`selfupdate/`）。
- **SQLite 单文件 `artex.db`**：`exploration_nodes`、`assets`、`companies`、`activity` 等表，另有 `traffic/` 下的 CA、索引与记录存储。
- **`【ARTEX 平台管控·非目标防御】`**：`guard/guard.go` 写入审计日志的 framing，被阻止的工具调用带此标记。
- **破坏性命令狩猎**：`rm -rf`、`DROP DATABASE`、`FLUSHALL` 等通用线索，正常管理员也会执行，不单凭此定论。

端口、UA、标记都可修改或清除，**缺失不等于安全**；结合目标侧行为与主机侧证据才是研判核心。

### 加固优先级

1. **缩减暴露面并迅速修补**：优先边界、Web、应用与中间件的已知漏洞，以 [CISA KEV](https://www.cisa.gov/known-exploited-vulnerabilities-catalog) 与 [CNCERT/CC](https://www.cert.org.cn/) 公告为优先级输入，不把某个 CVE 写死。
2. **统一认证强度**：辅助、合作、招聘等路径与主服务一致；服务器重新验证状态转换，不信任客户端 cookie/header 标记。
3. **逐对象鉴权**：所有对象访问强制服务器所有权与权限检查（IDOR、越权）。
4. **凭据与会话**：登录与核验接口自适应限速，敏感操作强制 MFA，会话与令牌最小化，响应与日志不暴露秘密。
5. **行为检测优先于固定签名**：WAF/SIEM 按同来源多阶段关联，而非精确字符串匹配。

> IP 阻断只是较弱的第一道防线：来源不明的"攻击 IP 清单"难以验证、很快过期，且有误封共享网段/CDN 的风险。持续有效的是行为检测与上述加固。

### 随仓库发布的检测规则

[`detections/`](detections) 可直接部署到 SIEM：

| 目录 | 内容 |
| --- | --- |
| [`detections/sigma/`](detections/sigma) | 原子规则：资产补全 UA、自更新 UA、guard 审计 framing、记录代理 CA、破坏性命令狩猎 |
| [`detections/sigma/correlation/`](detections/sigma/correlation) | 行为关联规则：补全速率、补全扇出、guard 突发、guard 与破坏命令同主机共现 |
| [`detections/suricata/`](detections/suricata) | 网络规则：补全 UA 存在性（sid 1000001）、速率（sid 1000002）、`norma/` UA（sid 1000003） |
| [`detections/indicators/`](detections/indicators) | 机器可读威胁指标（CSV / MISP） |
| [`detections/triage/`](detections/triage) | 主机侧只读 triage 脚本 |
| [`detections/attack/`](detections/attack) | 攻击面导航数据 |

阈值与窗口是保守起点，须按本地日志基线调优。纯 Web 多阶段行为没有独有 UA，需在本地 SIEM 中自行建立通用行为关联。

### 事件响应要点

- **预备自动处置**：对高置信信号按既定策略隔离来源、失效会话、降低速率，不把所有动作都置于人工等待；评估误封并留回滚。
- **事先定义日志**：认证、访问、出站、DNS，为事后链路重建保留证据。
- **按资产追踪范围**：重建入口到横向移动与权限提升的完整路径，不只修补首次入口。

### 官方通报渠道

中国大陆地区：国家互联网应急中心 CNCERT/CC（[cert.org.cn](https://www.cert.org.cn/)）、[12321 网络安全举报中心](https://www.12321.cn/)、[12386 威胁情报共享](https://www.12386.cn/)、[公安部网络安全保卫局](https://www.mps.gov.cn/)。

涉及个人信息泄露时，依《个人信息保护法》第五十七条、《网络安全法》第二十五条、《数据安全法》向主管部门报告；金融机构另有行业监管报告义务；涉嫌犯罪的向公安机关报案。其他国家和地区按所在地法律处理。本节不构成法律意见。

---

## 60 秒了解

系统将目标分解为意图，执行真实工具，把资产与发现写入图结构。Next.js 静态前端内嵌到 Go 单二进制，数据存于 SQLite（单文件，无需数据库服务）。

**访问形态只有一种：浏览器**，默认 `http://127.0.0.1:8787`。

三个关键设计：

| 设计 | 含义 |
| --- | --- |
| **双图数据底座** | 探索图（做了什么）+ 资产图（发现了什么），经锚点互连 |
| **事件驱动闭环** | 图发生变化即唤醒协调器，不需要人工推进每一步 |
| **成本与可靠性控制面** | 缓存、预算、通知、记账，控制自主执行的开销与失控 |

## 快速开始

只发布一个纯 Go 单二进制（`CGO_ENABLED=0`），覆盖 Linux / macOS / Windows。同一份程序提供两种入口：

| 入口 | 形态 | 适用场景 |
| --- | --- | --- |
| Web 控制台（默认） | 内嵌 Next.js 前端 + HTTP API，浏览器操作 | 看资产图、发现、审批、完整转录 |
| 终端 HARNESS（`artex cli`） | 进程内直接驱动 coord 根 agent，不占端口、不起浏览器 | 绿色启动、脚本/CI、终端里连续下指令 |

两种入口共用同一个 `data/`、`config.json` 与 LLM 配置：先用任一入口配置一次，另一个入口直接复用。

**方式一：下载发行包**

下载 `artex-rea-<版本>-<平台>-<架构>.zip`，解压后得到程序本体（前端已内嵌）、启动脚本、`skills/`、`config.example.json`。

```bat
copy config.example.json config.json   & rem 可选：指定数据库文件位置，留空自动创建 artex.db
start.bat                              & rem → http://localhost:8787
artex-rea.exe cli -t https://example.com
```

Linux / macOS 用 `./start.sh`；终端 HARNESS 直接 `./artex-rea cli -t https://example.com`。

终端 HARNESS 支持环境变量快捷启动（CI 不需要任何交互）：

```bash
export ARTEX_TARGET=https://example.com
export ARTEX_INSTRUCTION="重点测试登录、越权与文件上传"
export ARTEX_NONINTERACTIVE=1
./artex-rea cli
```

LLM 可沿用 Web 控制台里已保存的配置；纯环境变量启动时设置 `ANTHROPIC_API_KEY` 或 `OPENAI_API_KEY` 加 `ARTEX_LLM_MODEL`（可选 `ARTEX_LLM_PROVIDER` / `ARTEX_LLM_BASE_URL`）。交互模式下直接输入文字即给根 agent 下指令，`/help` 查看命令，退出码 `0` 无漏洞、`2` 有漏洞、`1` 失败。

**方式二：源码构建**（Go 1.26 + Node.js 22 LTS）

```bash
cd web && npm ci && npm run build:static && cd ..
rm -rf server/webui/dist && mkdir -p server/webui/dist
cp -a web/out/. server/webui/dist/
CGO_ENABLED=0 go build -tags embedui -o artex-rea ./cmd/artex
./start.sh
```

一次产出全部发布物：`./build.sh --release`

首次访问 `http://127.0.0.1:8787/setup` 初始化管理员密码；设置令牌打印在启动日志中。管理员用户名固定为 `ARTEX-HYDRA`。

## 目录结构

| 目录 | 内容 |
| --- | --- |
| [`cmd/artex`](cmd/artex) | 程序入口与启动引导 |
| [`agent`](agent) | Agent 循环、工具调用、编排与收尾 |
| [`server`](server) | HTTP / API 服务，内嵌前端 |
| [`web`](web) | Next.js 前端（静态导出后内嵌进 Go 二进制） |
| [`db`](db) | SQLite 存储与迁移 |
| [`skills`](skills) | **运行时资产**：75 个技能包，`<技能>/SKILL.md` 由 `skill_search` 全文检索 |
| [`detections`](detections) | Sigma / Suricata / ATT&CK 规则、指标与八类测试 |
| [`guard`](guard) | 越权与危险操作拦截 |
| [`enrich`](enrich) [`evidence`](evidence) [`report`](report) | 富化、证据留存、报告生成 |
| [`traffic`](traffic) [`intercept`](intercept) | 流量记录与拦截代理 |
| [`mcpserver`](mcpserver) [`mcpbuiltin`](mcpbuiltin) [`integrations`](integrations) | MCP 服务、内置工具与外部集成 |
| [`sdk`](sdk) | norma SDK 适配层 |
| [`scripts`](scripts) | 文档链接、语言残留等仓库自检脚本 |

## 开发

```bash
go build ./... && go vet ./... && go test ./...   # Go
cd web && npm test && npm run check               # Web 单元测试 + biome
```

- 涉及数据库的包：`go test ./db/ ./server/ ./agent/`（各包使用独立临时 SQLite 库）。
- 检测规则：`detections/tests/run-all.sh`（仅需 Docker）。
- 文档链接与锚点：`python3 -I scripts/check-doc-links.py`。
- 中文残留检查：`python3 -I scripts/check-language.py --source`，构建后加 `--web`。

前端开发服务器：`cd web && npm run dev`。主题预设由 `npm run generate:presets` 生成，界面文案在 [`web/messages/zh.json`](web/messages/zh.json)。

提交前请阅读 [`.github/PULL_REQUEST_TEMPLATE.md`](.github/PULL_REQUEST_TEMPLATE.md) 的验证清单。

## 分支沿革

```
Autumn-27/ARTEX（国内原作者，中文原创）
        └─ jiwoochris/artex-ko（韩语本地化分支）
                └─ j5tv9nfzhn-hue/ARTEX-HYDRA（本仓库 · 简体中文分支）
```

本仓库 fork 自韩语分支，并在其基础上完成中文重构：移除全部韩文内容，界面与产物统一为简体中文。

- **中文唯一**：界面、报告、事实摘要、对话、系统消息与后端日志均为简体中文；界面不再提供语言选择器。
- **技术语义保留**：保留内部推理提示词的角色、判断与工具行为。
- **协议不变**：API/JSON 字段、工具名、枚举、SQL、命令、payload、URL、原始日志及用户证据不因翻译改变。
- **语言回退**：历史遗留的其他语言代码（`ko`/`en`/`es`）与未知值统一回退为简体中文。

## 安全与漏洞报告

本节针对 **ARTEX 自身代码**的安全问题（认证绕过、权限提升、凭据明文存储、绕过人工审批等），不针对工具在外部系统发现的漏洞。

**不要把漏洞细节提交为公开 Issue。** 请使用仓库 Security 页的 **Report a vulnerability**（GitHub 私密漏洞报告），报告仅对维护者可见。若该功能尚未启用，请只提交「希望建立私密联系渠道」的最小请求，不含敏感细节。

有助于快速处理的信息：受影响组件与版本（Release tag 或提交哈希）、复现步骤与影响范围、可选的 PoC 与缓解建议。

修复以最新默认分支为基准，不保证回补旧版本；与本地化无关的上游问题也建议同步报告给 [Autumn-27/ARTEX](https://github.com/Autumn-27/ARTEX)。实行协调披露，修复前不公开细节。

## 许可证与免责声明

本项目采用 **AGPL-3.0**（见 [LICENSE](LICENSE)）。内置技能库来自 [usestrix/strix](https://github.com/usestrix/strix)，Apache-2.0，见 [`skills/NOTICE.md`](skills/NOTICE.md)。

本工具按「原样」提供，不含任何明示或默示的担保。使用本工具即表示已阅读并接受本文的[使用范围与法律告知](#-使用范围与法律告知)。任何以本仓库内容为依据对未授权目标实施扫描、探测或利用的行为，由行为人自行承担全部法律责任。
