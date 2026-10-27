#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -f "${SCRIPT_DIR}/manifests/install.yaml" ]]; then
  PROJECT_ROOT="${SCRIPT_DIR}"
  MANIFEST="${PROJECT_ROOT}/manifests/install.yaml"
else
  PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
fi
if [[ -z "${MANIFEST:-}" && -f "${PROJECT_ROOT}/deploy/install.yaml" ]]; then
  MANIFEST="${PROJECT_ROOT}/deploy/install.yaml"
elif [[ -z "${MANIFEST:-}" ]]; then
  MANIFEST="${PROJECT_ROOT}/manifests/install.yaml"
fi
VERSION="v1.1.0"
ARCH="auto"
KUBE_CONTEXT=""
ASSUME_YES=false
DRY_RUN=false

usage() {
  cat <<'EOF'
FTC 集群运维平台部署入口（普通 Kubernetes）

用法：
  交付包：./deploy-platform.sh [选项]
  源码仓库：./scripts/deploy-platform.sh [选项]

选项：
  --context <名称>       使用指定的 kubectl Context；默认使用当前 Context
  --version <版本>       平台版本；默认 v1.1.0
  --arch <架构>          auto、amd64 或 arm64；默认 auto
  --manifest <路径>      安装清单；默认自动识别 deploy/ 或 manifests/install.yaml
  --yes                  确认镜像已导入且 MySQL 已就绪，不再交互询问
  --dry-run              只检测和校验，不向集群部署
  -h, --help             显示帮助

说明：
  1. 本脚本不处理 kind，也不负责将镜像导入 Kubernetes 节点。
  2. 请先把对应架构的平台镜像导入所有可能运行平台 Pod 的节点。
  3. 本脚本不部署 MySQL；请提前准备平台 Pod 可以访问的 MySQL 8.0/8.4。
  4. 初始化页面必须通过节点 IP 和 NodePort 访问，不能使用 127.0.0.1。
EOF
}

die() {
  printf '错误：%s\n' "$*" >&2
  exit 1
}

info() {
  printf '==> %s\n' "$*"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --context)
      [[ $# -ge 2 ]] || die "--context 缺少参数"
      KUBE_CONTEXT="$2"
      shift 2
      ;;
    --version)
      [[ $# -ge 2 ]] || die "--version 缺少参数"
      VERSION="$2"
      shift 2
      ;;
    --arch)
      [[ $# -ge 2 ]] || die "--arch 缺少参数"
      ARCH="$2"
      shift 2
      ;;
    --manifest)
      [[ $# -ge 2 ]] || die "--manifest 缺少参数"
      MANIFEST="$2"
      shift 2
      ;;
    --yes)
      ASSUME_YES=true
      shift
      ;;
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "未知参数：$1（使用 --help 查看帮助）"
      ;;
  esac
done

command -v kubectl >/dev/null 2>&1 || die "未找到 kubectl"
[[ -f "$MANIFEST" ]] || die "安装清单不存在：$MANIFEST"

if [[ "$VERSION" != v* ]]; then
  VERSION="v${VERSION}"
fi

case "$ARCH" in
  auto|amd64|arm64) ;;
  *) die "不支持的架构：${ARCH}；只支持 auto、amd64、arm64" ;;
esac

KUBECTL=(kubectl)
if [[ -n "$KUBE_CONTEXT" ]]; then
  KUBECTL+=(--context "$KUBE_CONTEXT")
else
  KUBE_CONTEXT="$(kubectl config current-context 2>/dev/null)" || die "无法读取当前 kubectl Context"
fi

info "检查 Kubernetes 集群：${KUBE_CONTEXT}"
"${KUBECTL[@]}" cluster-info >/dev/null 2>&1 || die "无法连接 Kubernetes 集群"

NODE_ARCH_ROWS="$("${KUBECTL[@]}" get nodes --no-headers \
  -o 'custom-columns=ARCH:.metadata.labels.kubernetes\.io/arch,READY:.status.conditions[?(@.type=="Ready")].status')"
READY_ARCHES="$(printf '%s\n' "$NODE_ARCH_ROWS" | awk '$2 == "True" {print $1}' | sort -u)"
[[ -n "$READY_ARCHES" ]] || die "没有发现 Ready 节点"

if [[ "$ARCH" == "auto" ]]; then
  ARCH_COUNT="$(printf '%s\n' "$READY_ARCHES" | awk 'NF {count++} END {print count+0}')"
  if [[ "$ARCH_COUNT" -ne 1 ]]; then
    printf '检测到多个节点架构：\n%s\n' "$READY_ARCHES" >&2
    die "离线单架构镜像不能安全部署到混合架构集群；请隔离目标节点后使用 --arch 明确指定"
  fi
  ARCH="$READY_ARCHES"
fi

case "$ARCH" in
  amd64|arm64) ;;
  *) die "集群架构不受支持：$ARCH" ;;
esac

IMAGE="ftc-platform:${VERSION}-${ARCH}"
ARCHIVE_NAME="ftc-platform-${VERSION}-${ARCH}.tar"

info "检测到平台目标架构：${ARCH}"
info "本次部署使用镜像：${IMAGE}"

FOUND_ARCHIVE=""
for candidate in \
  "${PWD}/${ARCHIVE_NAME}" \
  "${PWD}/images/${ARCHIVE_NAME}" \
  "${PROJECT_ROOT}/images/${ARCHIVE_NAME}" \
  "${PROJECT_ROOT}/dist/ftc-platform-${VERSION}-delivery/images/${ARCHIVE_NAME}"; do
  if [[ -f "$candidate" ]]; then
    FOUND_ARCHIVE="$candidate"
    break
  fi
done

printf '\n部署前镜像检查：\n'
if [[ -n "$FOUND_ARCHIVE" ]]; then
  printf '  已在本地找到镜像包：%s\n' "$FOUND_ARCHIVE"
else
  printf '  当前目录未找到 %s。\n' "$ARCHIVE_NAME"
fi
printf '  请确认镜像 %s 已导入所有可能调度平台 Pod 的 Kubernetes 节点。\n' "$IMAGE"
printf '  本脚本不会上传或导入镜像；请按节点容器运行时的方式完成导入。\n\n'

printf '部署前 MySQL 检查：\n'
printf '  本脚本不会部署 MySQL。\n'
printf '  请确认已经准备 MySQL 8.0/8.4、ftc_platform 数据库和最小权限账号。\n'
printf '  请确认平台 Pod 网络能够访问 MySQL 地址和 TCP 3306（或实际配置端口）。\n'
printf '  MySQL 地址、数据库、用户名、密码和 TLS 设置将在初始化页面中填写。\n\n'
printf '  如果尚未准备数据库，可在 EL8/EL9 x86_64 独立服务器运行交付包 mysql-rpm/install-mysql.sh。\n\n'

if [[ "$DRY_RUN" != true && "$ASSUME_YES" != true ]]; then
  [[ -t 0 ]] || die "当前不是交互终端；确认镜像已导入且 MySQL 已就绪后使用 --yes"
  read -r -p "镜像已导入全部目标节点，是否继续部署？[y/N] " answer
  case "$answer" in
    y|Y|yes|YES) ;;
    *) printf '已取消部署。\n'; exit 0 ;;
  esac
  read -r -p "MySQL 已部署且平台 Pod 可以访问，是否继续部署？[y/N] " answer
  case "$answer" in
    y|Y|yes|YES) ;;
    *) printf 'MySQL 尚未就绪，已取消部署。请准备数据库后重新运行脚本。\n'; exit 0 ;;
  esac
elif [[ "$DRY_RUN" != true ]]; then
  printf '已通过 --yes 确认：镜像已导入，MySQL 已部署且网络可达。\n\n'
fi

IMAGE_LINES="$(grep -Ec '^[[:space:]]*image:[[:space:]]+ftc-platform:' "$MANIFEST" || true)"
[[ "$IMAGE_LINES" -eq 1 ]] || die "安装清单必须且只能包含一个 ftc-platform 镜像引用，当前为 ${IMAGE_LINES} 个"

TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ftc-platform-install.XXXXXX")"
trap 'rm -rf "$TEMP_DIR"' EXIT
RENDERED_MANIFEST="${TEMP_DIR}/install.yaml"
ESCAPED_IMAGE="${IMAGE//&/\\&}"

sed -E "s|^([[:space:]]*image:[[:space:]]*)ftc-platform:[^[:space:]]+|\\1${ESCAPED_IMAGE}|" \
  "$MANIFEST" > "$RENDERED_MANIFEST"

grep -Fq "image: ${IMAGE}" "$RENDERED_MANIFEST" || die "临时清单镜像替换失败"
"${KUBECTL[@]}" apply --dry-run=client -f "$RENDERED_MANIFEST" >/dev/null
info "临时清单客户端校验通过"

if [[ "$DRY_RUN" == true ]]; then
  info "dry-run 完成，没有修改集群"
  printf '最终镜像：%s\n' "$IMAGE"
  exit 0
fi

info "应用平台初始化清单"
if ! "${KUBECTL[@]}" apply -f "$RENDERED_MANIFEST"; then
  die "应用安装清单失败"
fi

info "等待初始化服务就绪"
if ! "${KUBECTL[@]}" -n ftc-platform rollout status deployment/ftc-platform-setup --timeout=5m; then
  printf '\n初始化服务未能就绪，现场信息如下：\n' >&2
  "${KUBECTL[@]}" -n ftc-platform get pods,svc -o wide >&2 || true
  "${KUBECTL[@]}" -n ftc-platform get events --sort-by=.lastTimestamp | tail -30 >&2 || true
  die "请重点检查镜像是否已导入全部目标节点，以及节点是否能够调度"
fi

printf '\n平台初始化服务已经就绪。\n'
printf '请从下面节点地址中选择一个客户端可达的 IP：\n'
"${KUBECTL[@]}" get nodes -o wide
printf '\n初始化入口：https://<可达节点IP>:30080\n'
printf '禁止使用 https://127.0.0.1:30080；127.0.0.1 只表示访问者自己的主机。\n'
