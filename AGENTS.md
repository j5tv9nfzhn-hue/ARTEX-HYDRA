# AGENTS.md

本仓库对 AI 助手 / 自动化代理的**硬性约定**。完整背景见 [README.md](README.md)。

## 一、本机可读、可写、可构建

在用户本机的工作副本上，**允许**执行编译、静态检查、测试与构建：

```text
go build ./... / go vet ./... / go test ./... / go run / gofmt / golangci-lint
npm ci|install|test|run build|run build:static、npx tsc、npx biome、next build
build.sh、dev.sh、任何打包脚本
```

允许通过间接方式执行（MCP shell 转发、写 `.bat`/`.ps1` 去跑、`docker run` 跑等价命令）。

**允许**：读文件、写文件、列目录、`git status|diff|log|show`、`git add|commit|push|tag`、`gh` 查询。

**仍然禁止**：

- 把构建产物提交进仓库（`dist/`、`server/webui/dist/`、`web/out/`、`node_modules/`）。
- 把密钥、令牌、本地配置（`config.json`、`artex.db`）带进提交。
- 对**外部或未授权的真实目标**运行本仓库的任何工具做扫描/探测（见 README 的使用范围与法律告知）。

本机执行构建/测试只用于**快速自查**，不作为发布的唯一凭据；发布仍以 GitHub Actions 的产物为准（`release.yml` 构建与发布，`detections.yml` 检测规则）。

## 二、门禁分层

- **编译 / 静态检查失败 = 必须拦住**，不得出包（`release.yml` 的 `gate` job：build / vet / embedui-vet / gofmt / golangci-lint）。
- **测试失败 = 只是健康度信号**，不得阻塞打包与发版（`release.yml` 的 `signals` job，`continue-on-error`）。本机测试失败同样不构成停止理由，但要在提交信息或回复中说明。
- **发布流程失败 = 必须失败并清理**，不留半成品（失败即删草稿、保 tag）。

"测试不拦"不等于"什么都不拦"：编译类确定性失败必须拦死。

## 三、网络必须走代理 10808

裸直连 GitHub 会稳定超时（~20 秒）。一律：

```bash
git -c http.proxy=http://127.0.0.1:10808 -c https.proxy=http://127.0.0.1:10808 <子命令>
```

`gh` 用 `HTTP_PROXY` / `HTTPS_PROXY` 环境变量。按次传参，不写全局 `git config`。
单次 20 秒无响应即判不可达，最多重试 2 次；仍失败就停止并报告，不裸连硬刷。

> 例外：`go build` / `go test` / `npm ci` 等需要拉取依赖时，同样按上述代理环境变量执行；若本机已有模块缓存（`GOMODCACHE`、`node_modules`）则可离线执行。

## 四、推送即返回

不轮询 CI、不盯 Actions 日志、不为确认成功反复查询。唯一串行要求：**tag 必须在新 commit 已到远端之后创建**。

本机构建/测试通过不等于可以跳过推送后的 CI 检查；只是不再由代理反复轮询结果。

## 五、静态自检 + 本机验证

写完回读改动文件，核对 import、符号名、类型签名、调用点参数、结构体字段、i18n 键（`web/messages/zh.json`）。
不凭记忆写符号名。

本机可跑（建议顺序，失败即定位后再继续）：

```bash
gofmt -l . && go vet ./... && go build ./cmd/artex
go test ./db/ ./server/ ./agent/
cd web && npm run check && npm test
```

`server/webui/dist` 为空时 `-tags embedui` 会构建失败，属预期：本地验证用不带 tag 的 `go build ./...`，或先完成静态导出再打 tag。

## 六、版本与发布

- 每版必发 GitHub Release，tag 与 Release 同名。
- 版本删除后重发沿用同一版本号，不递增。
- 更新源白名单只有 `j5tv9nfzhn-hue/ARTEX-HYDRA`，保留白名单 + SHA256 双闸门。
- 自动更新（安装）默认关，自动检测默认开，两者都有开关。

## 七、提交

一次语义完整的改动合成单个提交，中文提交信息，说明"改了什么 / 为什么"。
不提交构建产物（`dist/`、`server/webui/dist/`、`web/out/`）、不提交密钥与本地配置。
