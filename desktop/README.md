# artex-hydra 桌面端（Wails V2）

ARTEX-HYDRA 的桌面发行形态：Wails V2 原生窗口（Windows=WebView2、macOS=WebKit、
Linux=webkit2gtk）渲染与服务器版**同一套** Next.js 前端，后端在进程内运行
（与 `artex-rea` 完全同构：SQLite 双图库、agent 引擎、HTTP API）。

- Web 入口保留：桌面端默认监听 `127.0.0.1:8787`，浏览器访问同地址即同一界面；
  端口被服务器版占用时窗口照常可用（窗口渲染走进程内 Handler，不依赖回环端口）。
- 更新系统复用 `selfupdate`：版本化安装布局（入口 + 核心两件套、pointer.json、
  journal 状态机、SHA256 三闸门）与服务器版完全相同，仅资产名参数化为
  `artex-hydra` / `artex-hydra-core`。
- **窗口内不需要登录**：窗口直连进程内后端（`boot.Options.DisableAuth`），没有登录页、没有初始化流程，也不生成 `setup-info.txt`。免登录的边界只有一条：请求必须来自窗口本身（`server.authExempt` 认的进程内标记）。所以本机浏览器 `127.0.0.1:8787` 与 `-lan` 之后的局域网设备一样要账号密码，密码在窗口内「系统设置 → 登录密码」设一次；没设密码时「在局域网开放」会被拒绝（409）。

## 旧版 WEB 端数据导入（-import-old）

桌面端为解压即用的发行包（Windows=zip、macOS=.app、Linux=zip），不提供安装引导程序；
从旧版 WEB 端迁移统一走命令行导入：

```bat
artex-hydra.exe -import-old "C:\path\to\旧版安装目录"
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
    -X github.com/Autumn-27/artex/selfupdate.entryStem=artex-hydra \
    -X github.com/Autumn-27/artex/selfupdate.coreStem=artex-hydra-core" \
  -o artex-hydra .
```

**构建标签必须带 `desktop,production`**：wails 运行时的 CreateApp 按标签三选一
（production 实现 / dev 实现 / 无标签时的 MessageBox 报错兜底）——缺了会编译
成功但启动只弹「Wails applications will not build without the correct build tags」。

CGO 不能交叉编译（webview 库是平台原生库）：桌面端必须在目标平台上原生构建，
CI 由 `desktop` job 在三个原生 runner 上各自完成。

## 已知限制

- 窗口内为 webview 环境：`window.open` 等浏览器专属行为受限；SSE 流式正常。
- macOS 发行包未签名/未公证，首次打开需在「系统设置 → 隐私与安全性」放行。
- Linux 需要 webkit2gtk-4.1 运行时；部分发行版需手动安装。
- 桌面端不做端口接管（不会杀另一个实例的进程），单实例由安装目录锁保证。
- 三平台桌面端均为解压即用的发行包，不提供安装引导程序；快捷方式需自行创建
  （Windows 包内附 `artex-hydra.ico`，exe 本身也已内嵌品牌图标与 DPI manifest）。
