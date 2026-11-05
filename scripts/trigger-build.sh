#!/usr/bin/env bash
# 无 tag 手动触发构建（对应 .github/workflows/release.yml 的 workflow_dispatch）。
#
# 用途：不打新版本号也能在 CI 上按需构建指定平台。产物只作为 artifact 保留，
# 不创建 Release —— 发布仍必须打 tag（release.yml 的 push tag 路径）。
#
# 用法：
#   scripts/trigger-build.sh                              全部 6 端，版本 dev
#   scripts/trigger-build.sh -t linux/amd64               只编 linux/amd64
#   scripts/trigger-build.sh -t linux/amd64,darwin/arm64 -v 2.5.0.7
#   scripts/trigger-build.sh --watch                      触发后跟随运行日志
#
# 依赖：gh（已登录，见 `gh auth status`）。仓库默认取当前 git 远端。
set -euo pipefail

# 与 release.yml 中 plan job 的受支持清单保持一致；本地先校验，省一次失败运行。
SUPPORTED="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"
WORKFLOW="release.yml"

info() { printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok() { printf '\033[32m[+]\033[0m %s\n' "$*"; }
die() { printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<EOF
用法：$(basename "$0") [选项]

选项：
  -t, --targets LIST   逗号分隔的构建目标（如 linux/amd64,windows/amd64）；
                       留空 = 全部 6 端。
                       支持：$SUPPORTED
  -v, --version VER    无 tag 构建的版本号，仅用于二进制内嵌与产物命名；默认 dev
  -r, --ref REF        触发所用的分支/tag；默认当前分支
  -R, --repo OWNER/NAME  目标仓库；默认当前 git 远端
  -w, --watch          触发后跟随本次运行的日志
  -h, --help           显示帮助

示例：
  $(basename "$0") -t linux/amd64
  $(basename "$0") -t "linux/amd64 windows/amd64" -v 2.5.0.7 --watch
EOF
}

targets=""
version="dev"
ref=""
repo=""
watch=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    -t|--targets) [ "$#" -ge 2 ] || die "--targets 需要一个参数"; targets="$2"; shift 2 ;;
    -v|--version) [ "$#" -ge 2 ] || die "--version 需要一个参数"; version="$2"; shift 2 ;;
    -r|--ref)     [ "$#" -ge 2 ] || die "--ref 需要一个参数";     ref="$2";     shift 2 ;;
    -R|--repo)    [ "$#" -ge 2 ] || die "--repo 需要一个参数";    repo="$2";    shift 2 ;;
    -w|--watch)   watch=1; shift ;;
    -h|--help)    usage; exit 0 ;;
    *) die "未知参数：$1（用 -h 查看用法）" ;;
  esac
done

command -v gh >/dev/null 2>&1 || die "未找到 gh，请先安装 GitHub CLI"
gh auth status >/dev/null 2>&1 || die "gh 未登录，请先执行：gh auth login"

# 空白与逗号统一成逗号分隔，方便本地校验与传给 workflow。
targets="$(printf '%s' "$targets" | tr -d '[:space:]')"

if [ -n "$targets" ]; then
  old_ifs="$IFS"; IFS=','
  read -r -a want <<< "$targets"
  IFS="$old_ifs"
  for t in "${want[@]}"; do
    [ -n "$t" ] || continue
    case " $SUPPORTED " in
      *" $t "*) ;;
      *) die "不支持的目标：$t（支持：$SUPPORTED）" ;;
    esac
  done
fi

if [ -z "$repo" ]; then
  repo="$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null || true)"
  [ -n "$repo" ] || die "无法推断仓库，请用 -R owner/name 指定"
fi
if [ -z "$ref" ]; then
  ref="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo main)"
fi

if [ -n "$targets" ]; then platform_desc="$targets"; else platform_desc="全部 6 端"; fi
info "触发 $WORKFLOW @ $repo ($ref)  平台：$platform_desc  版本：$version"

gh workflow run "$WORKFLOW" --repo "$repo" --ref "$ref" \
  -f targets="$targets" -f version="$version"
ok "已触发。产物在本次运行的 artifact 中（不建 Release）。"

if [ "$watch" = "1" ]; then
  # workflow_dispatch 刚触发时 run 可能还未登记，稍等再取最近一次运行。
  sleep 5
  run_id="$(gh run list --repo "$repo" --workflow "$WORKFLOW" --limit 1 \
    --json databaseId --jq '.[0].databaseId')"
  [ -n "$run_id" ] || die "取不到运行 ID，请在 Actions 页面查看"
  ok "跟随运行 #$run_id"
  gh run watch "$run_id" --repo "$repo"
else
  info "查看：gh run list --repo $repo --workflow $WORKFLOW --limit 5"
fi
