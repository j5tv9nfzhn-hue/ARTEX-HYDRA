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
  --desktop              构建桌面端 artex-hydra（CGO_ENABLED=1，仅宿主机目标）
  --target OS/ARCH       指定单一目标，如 windows/amd64
  --upx                  强制 UPX 压缩，可能影响部分 Linux 环境的兼容性
  --no-compress          禁用 UPX，仅通过 Go linker 去符号并压缩 zip
  --help                 显示帮助

可通过 ARTEX_TARGETS 覆盖目标列表，例如：
  ARTEX_TARGETS=linux/amd64,windows/amd64 ./build.sh --release

桌面端（--desktop）走独立 Go 模块 desktop/，资产名 artex-hydra / artex-hydra-core
经 ldflags 注入 selfupdate；CGO 依赖平台原生 webview 库，必须原生构建。
macOS 目标打包为 .app bundle，其余平台为 zip。
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
  (cd web && npm run build:static)
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

package_binary() {
  binary="$1"
  goos="$2"
  goarch="$3"
  package_name="artex-rea-${ARTEX_BUILD_VERSION}-${goos}-${goarch}"
  package_root="${ARTEX_PACKAGE_DIR}/${package_name}"
  archive="${ARTEX_PACKAGE_DIR}/${package_name}.zip"

  command -v zip >/dev/null 2>&1 || die "打包需要 zip"
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
  (cd "$ARTEX_PACKAGE_DIR" && zip -q -r -9 "$(basename "$archive")" "$(basename "$package_root")")
  rm -rf "$package_root"
  ok "Release 压缩包：$archive"
}

# package_desktop 打包桌面发行物：Windows/Linux 为 zip，macOS 为 .app bundle。
#
# 布局与服务器版一致（入口 + 核心两件套 + skills + config.example.json + README），
# 只是文件名是 artex-hydra / artex-hydra-core。macOS 的安装根目录即 .app 内
# Contents/MacOS（versions/ 也在其下），与 Windows 布局同构。
package_desktop() {
  binary="$1"
  goos="$2"
  goarch="$3"
  stem="artex-hydra"
  package_name="${stem}-${ARTEX_BUILD_VERSION}-${goos}-${goarch}"
  package_root="${ARTEX_PACKAGE_DIR}/${package_name}"
  archive="${ARTEX_PACKAGE_DIR}/${package_name}.zip"

  command -v zip >/dev/null 2>&1 || die "打包需要 zip"
  rm -rf "$package_root" "$archive"
  mkdir -p "$package_root"

  case "$goos" in
    windows)
      core_ext=".exe"
      cp "$binary" "$package_root/${stem}${core_ext}"
      cp "$binary" "$package_root/${stem}-core${core_ext}"
      "$PYTHON_BIN" -I scripts/check-start-scripts.py || exit 1
      cp start.bat start-lan.bat "$package_root/"
      ;;
    darwin)
      app_bundle="$package_root/${stem}.app"
      mkdir -p "$app_bundle/Contents/MacOS" "$app_bundle/Contents/Resources"
      cp "$binary" "$app_bundle/Contents/MacOS/${stem}"
      cp "$binary" "$app_bundle/Contents/MacOS/${stem}-core"
      chmod +x "$app_bundle/Contents/MacOS/${stem}" "$app_bundle/Contents/MacOS/${stem}-core"
      cp start.sh "$app_bundle/Contents/MacOS/"
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
      cp "$binary" "$package_root/${stem}-core"
      chmod +x "$package_root/${stem}" "$package_root/${stem}-core"
      "$PYTHON_BIN" -I scripts/check-start-scripts.py || exit 1
      cp start.sh start-lan.sh "$package_root/"
      chmod +x "$package_root/start.sh" "$package_root/start-lan.sh"
      ;;
    *) die "不支持的桌面目标系统：$goos" ;;
  esac

  # skills / config / README 在 zip 模式下的统一收尾（.app 模式已在分支内完成）。
  if [ "$goos" != "darwin" ]; then
    cp -R skills "$package_root/"
    rm -f "$package_root/skills/embed.go"
    cp config.example.json "$package_root/"
    if [ -f README.md ]; then cp README.md "$package_root/"; fi
  fi
  (cd "$ARTEX_PACKAGE_DIR" && zip -q -r -9 "$(basename "$archive")" "$(basename "$package_root")")
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
#   - 资产名 stem 经 ldflags 注入 selfupdate（artex-hydra / artex-hydra-core），
#     更新系统因此可以原样复用版本化布局与 SHA256 闸门；
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

  binary_name="artex-hydra"
  [ "$goos" = "windows" ] && binary_name="artex-hydra.exe"
  if [ -n "${ARTEX_OUTPUT:-}" ] && [ "$ARTEX_RELEASE" != "1" ]; then
    output="$ARTEX_OUTPUT"
  else
    output="${ARTEX_OUTPUT_DIR}/artex-hydra-${goos}-${goarch}/${binary_name}"
  fi
  mkdir -p "$(dirname "$output")"

  ldflags="-s -w -buildid= -X main.version=${ARTEX_BUILD_VERSION}"
  ldflags="$ldflags -X github.com/Autumn-27/artex/selfupdate.entryStem=artex-hydra"
  ldflags="$ldflags -X github.com/Autumn-27/artex/selfupdate.coreStem=artex-hydra-core"
  if [ -n "$ARTEX_UPDATE_REPO" ]; then
    ldflags="$ldflags -X github.com/Autumn-27/artex/selfupdate.Repo=${ARTEX_UPDATE_REPO}"
  fi

  info "编译桌面端 ${goos}/${goarch}（CGO），版本 ${ARTEX_BUILD_VERSION}"
  # go.sum 不在仓库里（依赖拉取交给 CI 与首次构建）；已存在则跳过 tidy。
  if [ ! -f desktop/go.sum ]; then
    info "生成 desktop 模块 go.sum（首次构建需要拉取 wails 依赖）"
    (cd desktop && GOSUMDB="$ARTEX_GOSUMDB" go mod tidy)
  fi
  (cd desktop && GOSUMDB="$ARTEX_GOSUMDB" CGO_ENABLED=1 go build \
    -tags embedui \
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
    (cd "$ARTEX_PACKAGE_DIR" && for archive in *.zip; do sha256sum "$archive"; done > "$(basename "$checksum_file")")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$ARTEX_PACKAGE_DIR" && for archive in *.zip; do shasum -a 256 "$archive"; done > "$(basename "$checksum_file")")
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
