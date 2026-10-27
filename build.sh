#!/usr/bin/env bash
# ARTEX-HYDRA cross-platform release builder.
#
# The default mode builds one target and embeds the already-exported frontend.
# `./build.sh --release` builds and packages all supported desktop/server targets.
#
# Environment variables:
#   ARTEX_TARGET_OS=linux             One target OS in single-target mode.
#   ARTEX_TARGET_ARCH=amd64           One target arch in single-target mode.
#   ARTEX_TARGETS=linux/amd64,...     Comma-separated targets for multi-target mode.
#   ARTEX_BUILD_VERSION=v0.3.3        Version embedded in the binary and archive name.
#   ARTEX_OUTPUT=/path/to/artex-rea   Explicit binary path in single-target mode.
#   ARTEX_OUTPUT_DIR=dist             Directory for default binary paths.
#   ARTEX_PACKAGE=1                   Create a zip archive for each target.
#   ARTEX_PACKAGE_DIR=dist            Directory for release archives.
#   ARTEX_COMPRESS=off                UPX mode: off, auto, or required.
#   ARTEX_UPX_ARGS="--best --lzma"    Arguments passed to UPX.
#   ARTEX_SKIP_FRONTEND=1             Reuse server/webui/dist (for CI artifact builds).
#   ARTEX_SKIP_NPM_CI=1               Skip npm ci while rebuilding the frontend.
#   ARTEX_GOSUMDB=sum.golang.org      Go checksum database.
set -euo pipefail

cd "$(cd "$(dirname "$0")" && pwd)"

info() { printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok() { printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[!]\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
用法：
  ./build.sh                         构建当前系统与架构
  ./build.sh --target linux/amd64     构建指定目标
  ./build.sh --release               构建并打包全部支持平台
  ./build.sh --desktop               构建当前系统的 Wails V2 桌面端（原生构建，不支持交叉编译）

选项：
  --release              构建 Linux、macOS、Windows 的受支持 amd64/arm64 目标并生成 zip
  --desktop              构建桌面端 artex-hydra-desktop（CGO_ENABLED=1，仅宿主机目标）
  --target OS/ARCH       指定单一目标，如 windows/amd64
  --upx                  强制 UPX 压缩，可能影响部分 Linux 环境的兼容性
  --no-compress          禁用 UPX，仅通过 Go linker 去符号并压缩 zip
  --help                 显示帮助

可通过 ARTEX_TARGETS 覆盖目标列表，例如：
  ARTEX_TARGETS=linux/amd64,windows/amd64 ./build.sh --release

桌面端（--desktop）走独立 Go 模块 desktop/，产物是**单文件** artex-hydra-desktop
（无 core、无 versions/，数据与它同目录），名字基名经 ldflags 注入 selfupdate；
CGO 依赖平台原生 webview 库，必须原生构建。macOS 目标打包为 .app bundle，
其余平台为 zip。产物名带 -desktop 后缀，与 WEB 版（artex-rea-*）区分。
EOF
}

RELEASE_TARGETS_DEFAULT="linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64"
ARTEX_RELEASE="${ARTEX_RELEASE:-0}"
ARTEX_DESKTOP="${ARTEX_DESKTOP:-0}"
ARTEX_COMPRESS="${ARTEX_COMPRESS:-off}"
ARTEX_PACKAGE="${ARTEX_PACKAGE:-0}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --release)
      ARTEX_RELEASE=1
      ARTEX_PACKAGE=1
      shift
      ;;
    --target)
      [ "$#" -ge 2 ] || die "--target 需要 OS/ARCH 参数"
      target_arg="$2"
      case "$target_arg" in
        */*)
          ARTEX_TARGET_OS="${target_arg%%/*}"
          ARTEX_TARGET_ARCH="${target_arg##*/}"
          ARTEX_TARGETS="$target_arg"
          ;;
        *) die "目标必须为 OS/ARCH，例如 linux/amd64" ;;
      esac
      shift 2
      ;;
    --no-compress)
      ARTEX_COMPRESS=0
      shift
      ;;
    --desktop)
      ARTEX_DESKTOP=1
      shift
      ;;
    --upx)
      ARTEX_COMPRESS=required
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *) die "未知参数：$1，请使用 --help 查看用法" ;;
  esac
done

command -v go >/dev/null 2>&1 || die "未找到 Go，本项目需要 Go 1.26 或以上"

# Windows runner 只有 python.exe（没有 python3 命令）：优先 python3，缺失回退 python。
PYTHON_BIN="${PYTHON_BIN:-python3}"
command -v python3 >/dev/null 2>&1 || PYTHON_BIN="python"

ARTEX_GOSUMDB="${ARTEX_GOSUMDB:-sum.golang.org}"
if [ -z "${ARTEX_BUILD_VERSION:-}" ]; then
  if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    ARTEX_BUILD_VERSION="$(git describe --tags --always --dirty)"
  else
    ARTEX_BUILD_VERSION="dev"
  fi
fi
# Release tags are commonly passed as v0.3.3; keep the binary version consistent.
ARTEX_BUILD_VERSION="${ARTEX_BUILD_VERSION#v}"
ARTEX_OUTPUT_DIR="${ARTEX_OUTPUT_DIR:-dist}"
ARTEX_PACKAGE_DIR="${ARTEX_PACKAGE_DIR:-$ARTEX_OUTPUT_DIR}"
ARTEX_UPX_ARGS="${ARTEX_UPX_ARGS:---best --lzma}"
# 更新源（owner/name）。默认为空 = 用 selfupdate 包里写死的本仓库；发布流水线
# 显式传入本仓库全名，fork 出来的构建就不会去连上游。只允许构建期注入，
# 运行时可配等于给一条 RCE 通道，selfupdate 刻意不提供。
ARTEX_UPDATE_REPO="${ARTEX_UPDATE_REPO:-}"
if [ -n "$ARTEX_UPDATE_REPO" ]; then
  case "$ARTEX_UPDATE_REPO" in
    */*/*) die "ARTEX_UPDATE_REPO 必须是 owner/name 形式：$ARTEX_UPDATE_REPO" ;;
    */*) ;;
    *) die "ARTEX_UPDATE_REPO 必须是 owner/name 形式：$ARTEX_UPDATE_REPO" ;;
  esac
fi

if [ "${ARTEX_DESKTOP}" = "1" ]; then
  # 桌面端必须原生构建（CGO 依赖平台原生 webview 库）：默认取宿主机目标，
  # CI 各 runner 显式传入自己的目标。
  ARTEX_TARGET_OS="${ARTEX_TARGET_OS:-$(GOSUMDB="$ARTEX_GOSUMDB" go env GOOS)}"
  ARTEX_TARGET_ARCH="${ARTEX_TARGET_ARCH:-$(GOSUMDB="$ARTEX_GOSUMDB" go env GOARCH)}"
  ARTEX_TARGETS="${ARTEX_TARGETS:-${ARTEX_TARGET_OS}/${ARTEX_TARGET_ARCH}}"
elif [ "${ARTEX_RELEASE}" = "1" ]; then
  ARTEX_TARGETS="${ARTEX_TARGETS:-$RELEASE_TARGETS_DEFAULT}"
else
  ARTEX_TARGET_OS="${ARTEX_TARGET_OS:-$(GOSUMDB="$ARTEX_GOSUMDB" go env GOOS)}"
  ARTEX_TARGET_ARCH="${ARTEX_TARGET_ARCH:-$(GOSUMDB="$ARTEX_GOSUMDB" go env GOARCH)}"
  ARTEX_TARGETS="${ARTEX_TARGETS:-${ARTEX_TARGET_OS}/${ARTEX_TARGET_ARCH}}"
fi

if [ "${ARTEX_SKIP_FRONTEND:-0}" = "1" ]; then
  [ -d server/webui/dist ] || die "ARTEX_SKIP_FRONTEND=1，但 server/webui/dist 不存在"
else
  command -v npm >/dev/null 2>&1 || die "未找到 npm，前端静态构建需要 Node.js/npm"
  command -v rsync >/dev/null 2>&1 || die "未找到 rsync"
  info "正在构建前端静态资源"
  if [ "${ARTEX_SKIP_NPM_CI:-0}" != "1" ]; then
    (cd web && npm ci)
  fi
  # 版本号内嵌进静态包：与 -X main.version 同源（ARTEX_BUILD_VERSION），
  # 这样桌面端窗口内的「关于」与 /api/health 报的是同一个版本，四段（0.0.0.1）
  # 也原样落地——package.json 的 semver 只允许三段，不能当来源。
  (cd web && NEXT_PUBLIC_APP_VERSION="$ARTEX_BUILD_VERSION" npm run build:static)
  info "正在将前端资源同步到 server/webui/dist"
  mkdir -p server/webui/dist
  rsync -a --delete web/out/ server/webui/dist/
fi

compress_binary() {
  binary="$1"
  goos="$2"
  case "$ARTEX_COMPRESS" in
    0|off|false|none)
      info "跳过 UPX：$binary"
      return 0
      ;;
    auto|required|true|1) ;;
    *) die "ARTEX_COMPRESS 必须为 off、auto 或 required" ;;
  esac

  if ! command -v upx >/dev/null 2>&1; then
    if [ "$ARTEX_COMPRESS" = "required" ]; then
      die "ARTEX_COMPRESS=required，但未找到 upx"
    fi
    warn "未找到 upx，保留 linker 去符号后的结果：$binary"
    return 0
  fi

  before=$(wc -c < "$binary" | tr -d ' ')
  upx_args="$ARTEX_UPX_ARGS"
  [ "$goos" = "darwin" ] && upx_args="$upx_args --force-macos"
  # shellcheck disable=SC2086
  if ! upx $upx_args -- "$binary"; then
    if [ "$ARTEX_COMPRESS" = "required" ]; then
      die "UPX 压缩失败：$binary"
    fi
    warn "UPX 不支持该目标格式，保留未压缩二进制：$binary"
    return 0
  fi
  after=$(wc -c < "$binary" | tr -d ' ')
  ok "UPX 压缩完成：$binary (${before} -> ${after} bytes)"
}

# make_zip 打 zip 包：Linux/macOS 用 zip；Windows runner 的 Git Bash 没有
# zip 命令，依次回退 7z（部分镜像在 PATH）、Windows 自带的 bsdtar
# （System32\tar.exe，Windows 10+ 必有）。bsdtar 的 zip 条目名用 / 分隔、
# 顶层含文件夹，与 zip -r 结构一致——selfupdate 的 path.Base 提取依赖
# 这一点（Compress-Archive 用 \ 分隔会导致 exe 提取匹配失败，故不采用）。
make_zip() {
  pkgdir="$1"
  folder="$2"
  out="$3"
  if command -v zip >/dev/null 2>&1; then
    (cd "$pkgdir" && zip -q -r -9 "$out" "$folder")
  elif command -v 7z >/dev/null 2>&1; then
    (cd "$pkgdir" && 7z a -tzip -mx=9 "$out" "$folder" >/dev/null)
  elif [ -x /c/Windows/System32/tar.exe ]; then
    /c/Windows/System32/tar.exe -a -c -f "$pkgdir/$out" -C "$pkgdir" "$folder"
  else
    die "打包需要 zip/7z/bsdtar，均不可用"
  fi
}

package_binary() {
  binary="$1"
  goos="$2"
  goarch="$3"
  package_name="artex-rea-${ARTEX_BUILD_VERSION}-${goos}-${goarch}"
  package_root="${ARTEX_PACKAGE_DIR}/${package_name}"
  archive="${ARTEX_PACKAGE_DIR}/${package_name}.zip"

  rm -rf "$package_root" "$archive"
  mkdir -p "$package_root"
  cp "$binary" "$package_root/"
  # 版本化安装的"入口 + 核心"两件套（字节相同，只是名字不同）：
  #   artex-rea[.exe]       入口，永远待在根目录，只按 pointer.json 转发；
  #   artex-rea-core[.exe]  真正干活的核心，首次启动被收进 versions/<ver>/。
  # 升级因此只新增 versions/<新版本>/ 并原子改指针，不再替换任何正在运行的程序
  # ——Windows 上"删除/覆盖运行中的 exe"必然失败，旧方案就是死在这里。
  case "$goos" in
    windows) core_ext=".exe" ;;
    *) core_ext="" ;;
  esac
  cp "$binary" "$package_root/artex-rea-core${core_ext}"
  # 启动监护脚本是正式入口；页面一键更新需要进程退出后由脚本重启。
  # 直接运行 artex-rea 不会自动重启，发布包仅包含对应系统的启动脚本。
  # 启动脚本必须真的能执行：start-lan.* 曾被 base64 文本覆盖，双击只会报
  # “不是内部或外部命令”，而且一路进了发布包。坏脚本不允许打包。
  "$PYTHON_BIN" -I scripts/check-start-scripts.py || exit 1
  if [ "$goos" = "windows" ]; then
    cp start.bat start-lan.bat "$package_root/"
  else
    cp start.sh start-lan.sh "$package_root/"
    chmod +x "$package_root/start.sh" "$package_root/start-lan.sh"
  fi
  cp -R skills "$package_root/"
  rm -f "$package_root/skills/embed.go"
  cp config.example.json "$package_root/"
  if [ -f README.md ]; then cp README.md "$package_root/"; fi
  make_zip "$ARTEX_PACKAGE_DIR" "$(basename "$package_root")" "$(basename "$archive")"
  rm -rf "$package_root"
  ok "Release 压缩包：$archive"
}

# package_desktop 打包桌面发行物：Windows/Linux 为 zip，macOS 为 .app bundle。
#
# 与服务器版的关键差别：桌面端是**单文件**发行——没有 artex-hydra-core、没有
# versions/、没有 pointer.json。一个可执行文件就地运行，config/artex.db/skills
# 就在它旁边，整目录拷走即完成迁移（新旧合并也不必对齐核心副本）。服务器版仍走
# 「入口 + 核心两件套」，因为它换装时不能覆盖正在运行的 exe。
#
# 产物名带 -desktop 后缀，与 WEB 版（artex-rea-*）在同一个 Release 里一眼可分。
package_desktop() {
  binary="$1"
  goos="$2"
  goarch="$3"
  stem="artex-hydra-desktop"
  package_name="${stem}-${ARTEX_BUILD_VERSION}-${goos}-${goarch}"
  package_root="${ARTEX_PACKAGE_DIR}/${package_name}"
  archive="${ARTEX_PACKAGE_DIR}/${package_name}.zip"

  rm -rf "$package_root" "$archive"
  mkdir -p "$package_root"

  case "$goos" in
    windows)
      core_ext=".exe"
      cp "$binary" "$package_root/${stem}${core_ext}"
      "$PYTHON_BIN" -I scripts/check-start-scripts.py || exit 1
      # 启动脚本按桌面包改名：根目录脚本写的是 artex-rea，直接拷会找不到入口
      sed 's/artex-rea/artex-hydra-desktop/g' start.bat > "$package_root/start.bat"
      sed 's/artex-rea/artex-hydra-desktop/g' start-lan.bat > "$package_root/start-lan.bat"
      ;;
    darwin)
      app_bundle="$package_root/${stem}.app"
      mkdir -p "$app_bundle/Contents/MacOS" "$app_bundle/Contents/Resources"
      cp "$binary" "$app_bundle/Contents/MacOS/${stem}"
      chmod +x "$app_bundle/Contents/MacOS/${stem}"
      sed 's/artex-rea/artex-hydra-desktop/g' start.sh > "$app_bundle/Contents/MacOS/start.sh"
      chmod +x "$app_bundle/Contents/MacOS/start.sh"
      cp -R skills "$app_bundle/Contents/MacOS/skills"
      rm -f "$app_bundle/Contents/MacOS/skills/embed.go"
      cp config.example.json "$app_bundle/Contents/MacOS/"
      if [ -f README.md ]; then cp README.md "$app_bundle/Contents/MacOS/"; fi
      cat > "$app_bundle/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key>
  <string>ARTEX-HYDRA</string>
  <key>CFBundleDisplayName</key>
  <string>ARTEX-HYDRA</string>
  <key>CFBundleExecutable</key>
  <string>${stem}</string>
  <key>CFBundleIdentifier</key>
  <string>io.github.j5tv9nfzhn-hue.artex-hydra</string>
  <key>CFBundleVersion</key>
  <string>1</string>
  <key>CFBundleShortVersionString</key>
  <string>${ARTEX_BUILD_VERSION}</string>
  <key>LSMinimumSystemVersion</key>
  <string>10.15</string>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
PLIST
      ;;
    linux)
      cp "$binary" "$package_root/${stem}"
      chmod +x "$package_root/${stem}"
      "$PYTHON_BIN" -I scripts/check-start-scripts.py || exit 1
      sed 's/artex-rea/artex-hydra-desktop/g' start.sh > "$package_root/start.sh"
      sed 's/artex-rea/artex-hydra-desktop/g' start-lan.sh > "$package_root/start-lan.sh"
      chmod +x "$package_root/start.sh" "$package_root/start-lan.sh"
      ;;
    *) die "不支持的桌面目标系统：$goos" ;;
  esac

  # skills / config / README / 图标在 zip 模式下的统一收尾（.app 模式已在分支内完成）。
  if [ "$goos" != "darwin" ]; then
    cp -R skills "$package_root/"
    rm -f "$package_root/skills/embed.go"
    cp config.example.json "$package_root/"
    if [ -f README.md ]; then cp README.md "$package_root/"; fi
    # Windows 桌面端附带应用图标（exe 已内嵌品牌图标；解压后自建快捷方式时同样可用）
    if [ "$goos" = "windows" ] && [ -f desktop/assets/artex-hydra.ico ]; then
      cp desktop/assets/artex-hydra.ico "$package_root/${stem}.ico"
    fi
  fi
  make_zip "$ARTEX_PACKAGE_DIR" "$(basename "$package_root")" "$(basename "$archive")"
  rm -rf "$package_root"
  ok "桌面端 Release 包：$archive"
}

build_target() {
  target="$1"
  case "$target" in
    */*) ;;
    *) die "无效目标：$target，应为 OS/ARCH 格式" ;;
  esac
  goos="${target%%/*}"
  goarch="${target##*/}"
  case "$goos" in
    linux|darwin|windows) ;;
    *) die "不支持的系统：$goos，支持 linux、darwin、windows" ;;
  esac

  binary_name="artex-rea"
  [ "$goos" = "windows" ] && binary_name="artex-rea.exe"
  if [ -n "${ARTEX_OUTPUT:-}" ] && [ "$ARTEX_RELEASE" != "1" ]; then
    output="$ARTEX_OUTPUT"
  else
    output="${ARTEX_OUTPUT_DIR}/artex-rea-${goos}-${goarch}/${binary_name}"
  fi
  mkdir -p "$(dirname "$output")"

  # 版本号决定客户端能不能比对（纯数字三或四段才可比），更新源决定从哪拿包。
  ldflags="-s -w -buildid= -X main.version=${ARTEX_BUILD_VERSION}"
  if [ -n "$ARTEX_UPDATE_REPO" ]; then
    ldflags="$ldflags -X github.com/Autumn-27/artex/selfupdate.Repo=${ARTEX_UPDATE_REPO}"
  fi

  info "编译 ${goos}/${goarch}，版本 ${ARTEX_BUILD_VERSION}"
  GOSUMDB="$ARTEX_GOSUMDB" \
  CGO_ENABLED=0 \
  GOOS="$goos" \
  GOARCH="$goarch" \
  go build \
    -tags embedui \
    -trimpath \
    -ldflags "$ldflags" \
    -o "$output" \
    ./cmd/artex

  compress_binary "$output" "$goos"
  if [ -d "skills" ]; then
    out_dir="$(dirname "$output")"
    rm -rf "$out_dir/skills"
    cp -R skills "$out_dir/skills"
    rm -f "$out_dir/skills/embed.go"
  fi
  if command -v file >/dev/null 2>&1; then file "$output"; fi
  if [ "$ARTEX_PACKAGE" = "1" ]; then package_binary "$output" "$goos" "$goarch"; fi
  ok "编译完成：$output"
}

# build_desktop_target 编译 Wails V2 桌面端（desktop/ 独立模块）。
#
# 与服务器版构建的差异：
#   - CGO_ENABLED=1 且不设 GOOS/GOARCH（webview 库是平台原生库，交叉编译必失败）；
#   - 资产名 stem 经 ldflags 注入 selfupdate（artex-hydra-desktop），并置 singleFile=1：
#     不做 versions/ 安装、不转交核心，GitHub 查询与 SHA256 闸门照旧可用；
#   - 单文件发行：包内只有一个可执行文件，数据与它同目录，整目录即迁移单位。
#   - 不跑 UPX（CGO 二进制压缩兼容性差，Release 优先可移植性）。
build_desktop_target() {
  target="$1"
  goos="${target%%/*}"
  goarch="${target##*/}"
  host_goos="$(GOSUMDB="$ARTEX_GOSUMDB" go env GOOS)"
  host_goarch="$(GOSUMDB="$ARTEX_GOSUMDB" go env GOARCH)"
  if [ "$goos" != "$host_goos" ] || [ "$goarch" != "$host_goarch" ]; then
    die "桌面端必须原生构建（CGO 依赖平台 webview 库）：目标 $goos/$goarch 与宿主机 $host_goos/$host_goarch 不一致"
  fi
  case "$goos" in
    linux|darwin|windows) ;;
    *) die "不支持的桌面目标系统：$goos（支持 linux、darwin、windows）" ;;
  esac

  binary_name="artex-hydra-desktop"
  [ "$goos" = "windows" ] && binary_name="artex-hydra-desktop.exe"
  if [ -n "${ARTEX_OUTPUT:-}" ] && [ "$ARTEX_RELEASE" != "1" ]; then
    output="$ARTEX_OUTPUT"
  else
    output="${ARTEX_OUTPUT_DIR}/artex-hydra-desktop-${goos}-${goarch}/${binary_name}"
  fi
  mkdir -p "$(dirname "$output")"

  ldflags="-s -w -buildid= -X main.version=${ARTEX_BUILD_VERSION}"
  # Windows 必须链接 GUI 子系统。Go 的默认 PE 子系统是 CONSOLE：那样双击
  # exe 会先分配一个控制台窗口，而且那个黑窗在进程整个生命周期里常驻
  # （关掉黑窗等于杀掉应用）——对桌面端来说这就是"WEB 端残留"，正规桌面
  # 应用不该有。wails 自己的 CLI 在 Windows 上恒定追加 -H windowsgui，本
  # 脚本自建 ldflags 覆盖了 CLI 的默认值，所以必须显式补回来。
  # 代价是进程没有控制台：stdout/stderr 无效，日志落盘与错误弹窗由
  # desktop/main.go 的 setupDesktopLog / messageBox 负责。
  if [ "$goos" = "windows" ]; then
    ldflags="$ldflags -H windowsgui"
  fi
  ldflags="$ldflags -X github.com/Autumn-27/artex/selfupdate.entryStem=artex-hydra-desktop"
  ldflags="$ldflags -X github.com/Autumn-27/artex/selfupdate.coreStem=artex-hydra-desktop"
  # 单文件发行：不做 versions/ 安装、不转交核心，一键更新改为下载新版替换单文件。
  ldflags="$ldflags -X github.com/Autumn-27/artex/selfupdate.singleFile=1"
  if [ -n "$ARTEX_UPDATE_REPO" ]; then
    ldflags="$ldflags -X github.com/Autumn-27/artex/selfupdate.Repo=${ARTEX_UPDATE_REPO}"
  fi

  info "编译桌面端 ${goos}/${goarch}（CGO），版本 ${ARTEX_BUILD_VERSION}"
  # go.mod/go.sum 可能被 CI 缓存恢复成不一致的组合（缓存里的 go.sum 是
  # tidy 后的，而 workspace 的 go.mod 是未 tidy 的——go.mod 不在缓存路径
  # 里时尤甚）：每次都跑 tidy 保证两者一致再交给 go build。模块缓存
  # 预热后 tidy 开销很小；tidy 一致后也是 no-op。
  (cd desktop && GOSUMDB="$ARTEX_GOSUMDB" go mod tidy)

  # Windows 的窗口图标（标题栏 / 任务栏 / Alt-Tab）来自 exe 里的资源，不是 webview
  # 画出来的：rsrc 把品牌 .ico 与 DPI manifest 编成 .syso，go build 只链接
  # desktop/ 目录下、文件名带 _windows_<arch> 的 syso。这套步骤原先只写在
  # release.yml 里，于是 build.sh 本地打出来的 exe 没有图标——双击后标题栏与
  # 任务栏都是系统默认图标。现在 CI 与本地共用这一段。
  if [ "$goos" = "windows" ]; then
    syso="desktop/rsrc_windows_${goarch}.syso"
    if [ ! -f "$syso" ]; then
      rsrc_bin="$(command -v rsrc 2>/dev/null || true)"
      if [ -z "$rsrc_bin" ]; then
        info "未找到 rsrc：尝试 go install github.com/akavel/rsrc@v0.10.2"
        if go install github.com/akavel/rsrc@v0.10.2 >/dev/null 2>&1; then
          rsrc_bin="$(go env GOPATH)/bin/rsrc"
        fi
      fi
      if [ -n "$rsrc_bin" ] && [ -x "$rsrc_bin" ]; then
        "$rsrc_bin" -ico desktop/assets/artex-hydra.ico \
          -manifest desktop/assets/app.manifest \
          -arch "$goarch" \
          -o "$syso"
        ok "图标资源已生成：$syso"
      else
        warn "rsrc 不可用：本次 exe 不带品牌图标（标题栏/任务栏为系统默认图标）"
        warn "  补法：go install github.com/akavel/rsrc@v0.10.2 后重新构建"
      fi
    fi
  fi

  # 构建标签必须与官方 wails build 对齐（wails 的 CompileProject 恒定追加
  # OutputType 与 production 两个标签）：wails 运行时的 CreateApp 按标签三选一——
  # production 实现 / dev 实现 / 无标签时的 MessageBox 报错兜底。缺
  # desktop,production 时启动只会弹 "Wails applications will not build
  # without the correct build tags"。embedui 是服务器包的前端内嵌标签，两者叠加。
  build_tags="embedui,desktop,production"
  # 下面两项补的是 Wails V2 自身的平台缺口（不是本仓库代码的问题）：
  #
  # Linux：wails 默认 #cgo pkg-config webkit2gtk-4.0。Ubuntu 24.04+ / Debian 13
  #   只提供 webkit2gtk-4.1，必须加 webkit2_41 标签才会改成链接 4.1，否则
  #   pkg-config 直接报 "Package 'webkit2gtk-4.0' ... not found"。但 RHEL 系与
  #   旧版 Debian 仍然只有 4.0，无条件加标签会把本来能过的构建弄挂——所以按
  #   pkg-config 的实际存在情况选，而不是照抄 CI 的环境假设。
  #
  # macOS：wails v2.17 的 darwin 前端在 SDK 带 UniformTypeIdentifiers 头时会用到
  #   UTType，但它自己的 #cgo LDFLAGS 没有链接该 framework，于是链接期报
  #   "Undefined symbols for architecture arm64: _OBJC_CLASS_$_UTType"。
  #   cgo LDFLAGS 在最终链接时跨包累加，这里补一条即解（wails 官方
  #   troubleshooting 给的正是这个写法）。注意 UTType 只在 macOS 11+ 存在，
  #   打包的 Info.plist 仍写 10.15 只是沿用旧值。
  extra_cgo_ldflags=""
  case "$goos" in
    linux)
      if command -v pkg-config >/dev/null 2>&1; then
        if pkg-config --exists webkit2gtk-4.1 && ! pkg-config --exists webkit2gtk-4.0; then
          build_tags="$build_tags,webkit2_41"
          info "检测到 webkit2gtk-4.1（无 4.0）：附加构建标签 webkit2_41"
        fi
      fi
      ;;
    darwin)
      extra_cgo_ldflags="-framework UniformTypeIdentifiers"
      ;;
  esac

  (cd desktop && GOSUMDB="$ARTEX_GOSUMDB" CGO_ENABLED=1 \
    CGO_LDFLAGS="${CGO_LDFLAGS:-} $extra_cgo_ldflags" \
    go build \
    -tags "$build_tags" \
    -trimpath \
    -ldflags "$ldflags" \
    -o "../$output" \
    .)

  if [ -d "skills" ]; then
    out_dir="$(dirname "$output")"
    rm -rf "$out_dir/skills"
    cp -R skills "$out_dir/skills"
    rm -f "$out_dir/skills/embed.go"
  fi
  if command -v file >/dev/null 2>&1; then file "$output"; fi
  if [ "$ARTEX_PACKAGE" = "1" ]; then package_desktop "$output" "$goos" "$goarch"; fi
  ok "编译完成：$output"
}

write_checksums() {
  [ "$ARTEX_PACKAGE" = "1" ] || return 0
  checksum_file="$ARTEX_PACKAGE_DIR/SHA256SUMS"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$ARTEX_PACKAGE_DIR" && for archive in *.zip; do [ -e "$archive" ] && sha256sum "$archive"; done > "$(basename "$checksum_file")")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$ARTEX_PACKAGE_DIR" && for archive in *.zip; do [ -e "$archive" ] && shasum -a 256 "$archive"; done > "$(basename "$checksum_file")")
  else
    warn "未找到 sha256sum 或 shasum，跳过 SHA256SUMS"
    return 0
  fi
  ok "校验和文件：$checksum_file"
}

mkdir -p "$ARTEX_OUTPUT_DIR"
if [ "$ARTEX_PACKAGE" = "1" ]; then mkdir -p "$ARTEX_PACKAGE_DIR"; fi

old_ifs="$IFS"
IFS=','
read -r -a targets <<< "$ARTEX_TARGETS"
IFS="$old_ifs"
[ "${#targets[@]}" -gt 0 ] || die "ARTEX_TARGETS 不能为空"
for target in "${targets[@]}"; do
  target="${target//[[:space:]]/}"
  [ -n "$target" ] || continue
  if [ "${ARTEX_DESKTOP}" = "1" ]; then
    build_desktop_target "$target"
  else
    build_target "$target"
  fi
done

if [ "$ARTEX_PACKAGE" = "1" ]; then
  write_checksums
  info "Release 包已生成：$ARTEX_PACKAGE_DIR"
fi
