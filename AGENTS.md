# AGENTS.md

本仓库对 AI 助手 / 自动化代理的**硬性约定**。完整版见 [`README.md` 的「开发与发布约定」](README.md#开发与发布约定长期生效--最高优先级)，冲突时以该节为准。

## 一、本机只读 + 只写，不构建

在用户本机的工作副本上，**禁止**执行任何测试、编译、构建、打包命令：

```text
go build / go vet / go test / go run / gofmt / golangci-lint
npm ci|install|test|run build|run build:static、npx tsc、npx biome、next build
build.sh、dev.sh、任何打包脚本
```

含间接方式：MCP shell 转发、写 `.bat`/`.ps1` 去跑、`docker run` 跑等价命令。

**允许**：读文件、写文件、列目录、`git status|diff|log|show`、`git add|commit|push|tag`、`gh` 只读查询。

构建与验证一律由 GitHub Actions 承担（`release.yml` 构建与发布，`detections.yml` 检测规则）。

`desktop/` 是独立 Go 模块（Wails V2 桌面壳，CGO 依赖）：本机同样不构建；它的编译验证由 `release.yml` 的 `desktop` job 承担六端（三平台 × amd64/arm64）：Windows 与 Linux **每端一个 job、各自原生 runner**（`windows-latest`、`windows-11-arm`、`ubuntu-24.04`、`ubuntu-24.04-arm`）；macOS 的两个架构合并为**一个 job**（`macos-26`，先 arm64 原生、再 amd64 经 `build.sh` 同机 clang `-arch` 交叉），因为每个 macOS job 都要单独拿机器，合并后只有一次获取机会。webview 库是平台原生库，除 macOS 的同机跨架构这一条例外外不做交叉编译。排障提示：某个 job **0 步骤、0 日志**、停在「waiting for a hosted runner to come online」后被取消，是那个标签的池子给不出机器（runner 拾取早于任何仓库代码执行），改构建脚本没有用，换标签。`desktop/go.sum` 不入库，首次构建由 `go mod tidy` 生成。

## 二、门禁分层

- **编译 / 静态检查失败 = 必须拦住**，不得出包（`release.yml` 的 `gate` job：build / vet / embedui-vet / gofmt / golangci-lint）。
- **测试失败 = 只是健康度信号**，不得阻塞打包与发版（`release.yml` 的 `signals` job，`continue-on-error`）。
- **发布流程失败 = 必须失败并清理**，不留半成品（`release.yml` 失败即删草稿、保 tag）。

"测试不拦"不等于"什么都不拦"：编译类确定性失败必须拦死。

## 三、网络必须走代理 10808

裸直连 GitHub 会稳定超时（~20 秒）。一律：

```bash
git -c http.proxy=http://127.0.0.1:10808 -c https.proxy=http://127.0.0.1:10808 <子命令>
```

`gh` 用 `HTTP_PROXY` / `HTTPS_PROXY` 环境变量。按次传参，不写全局 `git config`。
单次 20 秒无响应即判不可达，最多重试 2 次；仍失败就停止并报告，不裸连硬刷。

## 四、推送即返回

不轮询 CI、不盯 Actions 日志、不为确认成功反复查询。唯一串行要求：**tag 必须在新 commit 已到远端之后创建**。

## 五、静态自检代替编译

写完回读改动文件，核对 import、符号名、类型签名、调用点参数、结构体字段、i18n 键（`web/messages/zh.json`）。
不凭记忆写符号名，不把"让 CI 去发现"当作检查手段。

## 六、版本与发布

- 每版必发 GitHub Release，tag 与 Release 同名。
- 版本删除后重发沿用同一版本号，不递增。
- 更新源白名单只有 `j5tv9nfzhn-hue/ARTEX-HYDRA`，保留白名单 + SHA256 双闸门。
- 自动更新（安装）默认关，自动检测默认开，两者都有开关。

## 七、提交

一次语义完整的改动合成单个提交，中文提交信息，说明"改了什么 / 为什么"。
不提交构建产物（`dist/`、`server/webui/dist/`、`web/out/`）、不提交密钥与本地配置。
