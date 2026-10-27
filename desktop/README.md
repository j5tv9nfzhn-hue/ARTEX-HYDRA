# artex-hydra-desktop 桌面端（Wails V2）

ARTEX-HYDRA 的桌面发行形态：Wails V2 原生窗口（Windows=WebView2、macOS=WebKit、
Linux=webkit2gtk）渲染与服务器版**同一套** Next.js 前端，后端在进程内运行
（与 `artex-rea` 完全同构：SQLite 双图库、agent 引擎、HTTP API）。

- Web 入口保留：桌面端默认监听 `127.0.0.1:8787`，浏览器访问同地址即同一界面；
  端口被服务器版占用时窗口照常可用（窗口渲染走进程内 Handler，不依赖回环端口）。
- 发行物是**单文件**（`singleFile=1`）：包内只有一个可执行文件，没有 core、没有
  `versions/`、没有 `pointer.json`；`config/`、`artex.db`、`data/`、`skills/` 都在
  它旁边，整个目录拷走即完成迁移。与 WEB 版 `artex-rea-*` 用 `-desktop` 后缀区分。
- 更新：`selfupdate` 的 GitHub 查询与 SHA256 校验照旧可用（资产名
  `artex-hydra-desktop-<版本>-<os>-<arch>.zip`）；桌面端不跑版本化换装，页面
  「系统设置 → 更新」只做检查，装新版本＝关闭窗口后替换那一个可执行文件。
- **界面的「系统设置 → 数据迁移」**：新增的 `migrate` 包同时被服务器版与桌面端使用，
  前端也只有一份，所以这张卡片两边都能用（桌面端导入旧 WEB 目录、WEB 端导入桌面端
  目录）。体检列出体积/行数/表数/密钥/工作空间与技能项数，并探测 8787/8788 判断
  对方是否还在跑——探测只算「除本程序之外」的占用（本进程自己的监听端口通过
  `migrate.Options.OwnPorts` 排除，否则跑着的时候永远判「旧版还在跑」）；导入支持
  **合并**（保留当前库，按两边共同列逐表插入，冲突跳过）与**替换**（清空后灌入，
  动手前 `VACUUM INTO` 备份），并连旧版落在安装根目录的 `i<意图ID>` 产物目录一起
  搬进 `data/`。CLI `-import-old` 只留给全新空目录，目标已有 artex.db 时会提示改用卡片。
- **关闭窗口 = 收进托盘（Windows）**：`desktop/tray_windows.go` 用 `fyne.io/systray`
  （纯 syscall，不加 cgo 依赖）。托盘就绪后 `OnBeforeClose` 隐藏窗口而不退出，后端
  与在跑的回合继续；退出走托盘菜单（先置 `trayQuitting` 再 `Quit`）。托盘起不来就
  退回"关闭即退出"。macOS/Linux 目前 `tray_stub.go` 提供空实现（要接
  `RunWithExternalLoop` / StatusNotifier，需真机验证后才开）。
- **窗口尺寸**：默认 1440×900、最小 1024×640；小屏按「屏宽 88% / 屏高 94%」收缩
  （1366×768 → 1202×722，与实测参考窗口 1200×728 一致）。
- **图标**：标题栏 / 任务栏 / Alt-Tab 的图标来自 exe 资源。`build.sh --desktop` 会先跑
  `rsrc` 生成 `desktop/rsrc_windows_<arch>.syso`（`.ico` + DPI manifest）；手工 `go build`
  时这一步要自己做，否则 exe 是系统默认图标。
- **窗口内不需要登录**：窗口直连进程内后端（`boot.Options.DisableAuth`），没有登录页、没有初始化流程，也不生成 `setup-info.txt`。免登录的边界只有一条：请求必须来自窗口本身（`server.authExempt` 认的进程内标记）。所以本机浏览器 `127.0.0.1:8787` 与 `-lan` 之后的局域网设备一样要账号密码，密码在窗口内「系统设置 → 登录密码」设一次；没设密码时「在局域网开放」会被拒绝（409）。

## 旧版 WEB 端数据导入（-import-old）

桌面端为解压即用的发行包（Windows=zip、macOS=.app、Linux=zip），不提供安装引导程序；
从旧版 WEB 端迁移统一走命令行导入：

```bat
artex-hydra-desktop.exe -import-old "C:\path\to\旧版安装目录"
```

导入内容与规则：

- **必须成套**：`artex.db`（含残留 -wal/-shm）、`credential.key`——没有
  credential.key，LLM 配置里的加密 API key 全部解不开；
- **同时拷贝**：`jwt.key`（保留登录会话）、`config.json`（只迁移
  listen/language；指向旧目录的 database.path/skill_dir 不会带过来）、
  `data/` 工作空间树（排除 .delete-staging）、`skills/` 整树（旧覆盖新——
  用户改过的技能文件全保留，被覆盖的内置技能可在 UI「恢复出厂」升到新版）、
  根目录 `i<意图ID>` 产物目录 → 新 `data/`；
- **前置条件**：旧版程序必须完全退出（导入器探测 8787/8788 端口，仍在监听
  会拒绝）；新安装目录已存在 artex.db 时拒绝（防重复导入覆盖新数据）；
- 旧目录全程只读（拷贝不是移动），随时可以切回旧版。

## 独立模块的原因

`wails` 是 CGO 依赖。放进根模块会污染服务器版的纯静态构建（`CGO_ENABLED=0`）
与 CI 门禁，因此桌面壳是独立 Go 模块，通过 `replace` 复用根模块的后端包。

## 本地构建

前置：Go 1.26、Node.js/npm（前端静态导出）、目标平台的 webview 运行时
（Windows 需 WebView2、Linux 需 `libwebkit2gtk-4.1-dev` 等、macOS 自带 WebKit）。

```bash
# 根目录（先构建前端，desktop 构建依赖 server/webui/dist）：
./build.sh --desktop --target <os>/<arch>

# 或不经 build.sh 直接构建（需先备好 server/webui/dist）：
cd desktop
go mod tidy          # 首次：生成 go.sum（拉取 wails 依赖）
CGO_ENABLED=1 go build -tags "embedui,desktop,production" -trimpath \
  -ldflags "-s -w -buildid= -X main.version=dev \
    -X github.com/Autumn-27/artex/selfupdate.entryStem=artex-hydra-desktop \
    -X github.com/Autumn-27/artex/selfupdate.singleFile=1" \
  -o artex-hydra-desktop .
```

**构建标签必须带 `desktop,production`**：wails 运行时的 CreateApp 按标签三选一
（production 实现 / dev 实现 / 无标签时的 MessageBox 报错兜底）——缺了会编译
成功但启动只弹「Wails applications will not build without the correct build tags」。

CGO 依赖平台原生 webview 库，桌面端六端（Windows / macOS / Linux 各 amd64 + arm64）
全部由 CI 的 `desktop` job 产出：Windows 与 Linux 跑在对应架构的原生 runner 上
（`windows-latest`、`windows-11-arm`、`ubuntu-latest`、`ubuntu-24.04-arm`）；macOS
两个架构都在同一台 Apple Silicon（`macos-15`）上出，x86_64 目标靠 clang 的双 slice
交叉（`-arch x86_64`，同时进 CGO_CFLAGS 与 CGO_LDFLAGS），不依赖已退役的 Intel
runner。`build.sh --desktop` 对 macOS 之外的跨架构请求直接拒绝，并在构建后用
`scripts/check-binary-headers.py` 校验产物的机器类型码——「`-arch` 没生效、包其实是
宿主架构」这种失败构建期是绿灯，只能靠文件头兜住。

## 已知限制

- 窗口内为 webview 环境：`window.open` 等浏览器专属行为受限；SSE 流式正常。
- macOS 发行包未签名/未公证（开源项目常规做法：直接打包分发），首次打开需在「系统设置 → 隐私与安全性」放行；两个架构都要求 macOS 11+（Go 1.26 的 darwin 与 wails 用到的 UTType 都从 11 起才有）。
- Linux 需要 webkit2gtk-4.1 运行时；部分发行版需手动安装。
- 桌面端不做端口接管（不会杀另一个实例的进程），单实例由安装目录锁保证。
- 三平台桌面端均为解压即用的发行包，不提供安装引导程序；快捷方式需自行创建
  （Windows 包内附 `artex-hydra-desktop.ico`，exe 本身也已内嵌品牌图标与 DPI manifest）。
