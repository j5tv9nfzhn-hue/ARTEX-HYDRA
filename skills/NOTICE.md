# 内置技能包来源与许可

本目录下全部 75 个技能包来自上游开源仓库 **usestrix/strix**，未做手写改写：

- 仓库：https://github.com/usestrix/strix
- 上游路径：`strix/skills/`（上游的内部 agent 知识包）
- 固定提交：`6b7ae2ebe4d47037a927aade23265ff4e4f27e84`（2026-10-29）
- 许可证：Apache License 2.0（全文见同目录 `LICENSE-APACHE-2.0`）
- 同步方式：按上述提交整包拷贝，仅做格式转换，**未修改技能正文**

## 格式转换说明

上游是「一个技能 = 一个 `.md` 文件」，按类别分目录存放：

```
strix/skills/vulnerabilities/authentication_jwt.md
```

本仓库的加载器（`github.com/Autumn-27/norma/skill` 的 `LoadDir`）按
`skills/<id>/SKILL.md` 扫描，因此落地时转换为：

```
skills/authentication-jwt/SKILL.md
```

转换只改路径，不动内容：YAML frontmatter 的 `name` / `description` 与正文均逐字节
保留。**目录名取 frontmatter 的 `name` 字段**——因为上游按文件名选择技能，而本仓库
按目录名选择，取 `name` 可让两者指向同一个技能。上游的类别信息不再体现在目录结构上，
完整的类别归属见下节。

## 类别归属

| 上游类别 | 技能数 | 内容 |
| --- | --- | --- |
| `vulnerabilities` | 29 | 漏洞类的进阶测试手法 |
| `tooling` | 13 | 沙箱内核心命令行工具的操作手册 |
| `technologies` | 7 | 第三方服务与平台的专项技术 |
| `cloud` | 4 | 云厂商环境的安全测试 |
| `frameworks` | 4 | 主流 Web 框架的特定测试方法 |
| `analysis` | 4 | 研判、定级与修复验证的判断纪律 |
| `custom` | 4 | 社区贡献的专项场景 |
| `scan_modes` | 4 | 扫描模式（quick/standard/deep/diff） |
| `protocols` | 2 | 协议层的测试范式 |
| `coordination` | 2 | 多 agent 编排与白盒协调 |
| `reconnaissance` | 2 | 信息收集与攻击面测绘 |

## 历史

M15 起本目录曾整包内嵌 `mukul975/Anthropic-Cybersecurity-Skills` 的 818 个技能包，
外加 12 个任务沉淀的 `case-web-*` 技能。这批内容已**全部下线**，内置技能库整体切换为
usestrix/strix 的技能库。旧目录名登记在 `skills/embed.go` 的 `retiredSkills` 里，升级
安装时会被主动删除；用户自建或改过的技能不在该表内，不受影响。

## 许可合规

Apache-2.0 要求随分发提供许可证副本与变更说明：本文件即变更说明（仅改路径、未改正文）。
若需再分发，请保留 `LICENSE-APACHE-2.0` 与本文件。
