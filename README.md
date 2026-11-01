# ARTEX-HYDRA 简体中文版

**由 LLM 多 Agent 自主执行渗透测试与自适应编排的系统**（Go 后端 + Next.js 前端）

界面、报告、摘要、对话及后端消息**统一使用简体中文**，本仓库不再提供其他语言界面。

[![release](https://github.com/j5tv9nfzhn-hue/ARTEX-HYDRA/actions/workflows/release.yml/badge.svg)](https://github.com/j5tv9nfzhn-hue/ARTEX-HYDRA/actions/workflows/release.yml) [![detections](https://github.com/j5tv9nfzhn-hue/ARTEX-HYDRA/actions/workflows/detections.yml/badge.svg?branch=main)](https://github.com/j5tv9nfzhn-hue/ARTEX-HYDRA/actions/workflows/detections.yml) [![license: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue.svg)](LICENSE)

---

## 🚨 先读这一节

**仅允许在自有或明确授权的环境中，以学习、防御和检测为目的使用。**

ARTEX 可自主组织侦察、工具执行和多阶段测试，滥用可能造成严重损害。

- **未经授权的使用可能构成犯罪。** 在中华人民共和国大陆地区，未经授权侵入、非法控制计算机信息系统，可能触犯《刑法》第二百八十五条、第二百八十六条；同时涉及《网络安全法》《数据安全法》《个人信息保护法》。跨境使用还需遵守行为地与数据主体所在地的法律。
- **不要将他人的真实服务作为实验目标。** 请使用自有、本地隔离的 OWASP Juice Shop、DVWA 等有意设计的测试环境；真实目标必须事先取得书面许可，并约定范围、时间窗口与数据处理方式。
- **从防御视角阅读。** 本仓库提供[自主 AI 防御与检测指南](docs/04-防御与检测.md)、指标、规则与加固清单。

### 关于来源与真实滥用事件

ARTEX 由国内开发者 **Autumn-27** 创建并开源（[Autumn-27/ARTEX](https://github.com/Autumn-27/ARTEX)），Agent 能力依赖其 [norma](https://github.com/Autumn-27/norma) SDK。本仓库是该项目的韩语本地化分支 `jiwoochris/artex-ko` 的简体中文分支。

2026 年 10 月，韩国多家媒体（经由本项目韩语分支的安全告知记录）报道称，ARTEX 被用于针对韩国境内金融机构的个人信息窃取攻击，相关刑事调查在进行中。**本仓库仅转述并保留该记录，不代表对其细节的独立核实。** 保留这段记录的唯一目的，是让防御方理解自主 AI 攻击的真实后果，并据此建立检测与阻断能力。

本仓库不提供任何攻击方法、目标清单或利用建议，只保留技术说明、防御指南与检测规则。若不同意[使用限制与免责声明](#许可证与免责声明)，请勿下载或使用。

---

## 60 秒了解

系统将目标分解为意图，执行真实工具，将资产与发现写入图结构。Next.js 静态前端内嵌到 Go 单二进制，数据存储于 SQLite（单文件，无需数据库服务）。

**访问形态只有一种：浏览器**（默认 `http://127.0.0.1:8787`）。

三个关键设计：**双图数据底座**（探索图 + 资产图，经锚点互连）、**事件驱动闭环**（图变化唤醒协调器）、**成本与可靠性控制面**（缓存 / 预算 / 通知 / 记账）。详见[系统架构](docs/02-系统架构.md)。

## 快速开始

本项目只发布 **Web 版**：纯 Go 单二进制（`CGO_ENABLED=0`）+ 内嵌 Next.js 静态前端，覆盖 Linux / macOS / Windows。

**方式一：下载发行包**

下载 `artex-rea-<版本>-<平台>-<架构>.zip`，解压后得到程序本体（前端已内嵌）、启动脚本、`skills/`、`config.example.json`。

```bat
copy config.example.json config.json   & rem 可选：指定数据库文件位置，留空自动创建 artex.db
start.bat                              & rem → http://localhost:8787
```

**方式二：源码构建**（Go 1.26 + Node.js 22 LTS）

```bash
cd web && npm ci && npm run build:static && cd ..
rm -rf server/webui/dist && mkdir -p server/webui/dist
cp -a web/out/. server/webui/dist/
CGO_ENABLED=0 go build -tags embedui -o artex-rea ./cmd/artex
copy config.example.json config.json
./start.sh
```

或一次产出全部发布物：`./build.sh --release`

首次访问 `http://localhost:8787/setup` 初始化管理员密码；设置令牌打印在启动日志中。管理员用户名固定为 `ARTEX-HYDRA`。

完整安装、配置、反向代理与故障排查见 [docs/01-快速开始](docs/01-快速开始.md)。

## 文档

| | |
| --- | --- |
| [01-快速开始](docs/01-快速开始.md) | 安装、配置、首次运行、反向代理 |
| [02-系统架构](docs/02-系统架构.md) | 六层架构、双图、闭环、成本控制 |
| [03-外观与主题](docs/03-外观与主题.md) | 皮肤与九套预设 |
| [04-防御与检测](docs/04-防御与检测.md) | 防御指南与检测规则 |
| [05-开发与贡献](docs/05-开发与贡献.md) | 本地化原则、提交约定 |
| [06-维护与发布](docs/06-维护与发布.md) | 上游同步、发布流程 |
| [07-安全与行为准则](docs/07-安全与行为准则.md) | 漏洞报告、行为准则 |
| [08-变更日志](docs/08-变更日志.md) | 按版本记录 |
| [09-子模块](docs/09-子模块.md) | SDK、插件、官网等 |
| [文档索引](docs/README.md) | 全部文档 |

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
- **防回归**：源码文本、词库、脚本、文档及静态 UI 通过残留语言检查。

详细边界见 [docs/05-开发与贡献.md](docs/05-开发与贡献.md#本地化原则)，上游同步见 [docs/06-维护与发布.md](docs/06-维护与发布.md)。

## 许可证与免责声明

本项目采用 **AGPL-3.0**（见 [LICENSE](LICENSE)）。内置技能库来自 [usestrix/strix](https://github.com/usestrix/strix)，Apache-2.0，见 [`skills/NOTICE.md`](skills/NOTICE.md)。

本工具按"原样"提供，不含任何明示或默示的担保。使用本工具即表示已阅读并接受本文档的[使用范围与法律告知](#-先读这一节)。任何以本仓库内容为依据对未授权目标实施扫描、探测或利用的行为，由行为人自行承担全部法律责任。
