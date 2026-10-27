#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
if [[ -d "${PROJECT_ROOT}/mysql-rpm/packages" ]]; then
  BUNDLE_ROOT="${PROJECT_ROOT}/mysql-rpm"
else
  BUNDLE_ROOT="${PROJECT_ROOT}/dist/ftc-platform-v1.1.0-delivery/mysql-rpm"
fi

DRY_RUN=false
ASSUME_YES=false
BIND_ADDRESS="0.0.0.0"
MYSQL_PORT="3306"
CREDENTIAL_FILE="/root/ftc-mysql-credentials.txt"
OS_MAJOR_OVERRIDE=""
ARCH_OVERRIDE=""

usage() {
  cat <<'EOF'
FTC 平台外部 MySQL 8.4 离线安装入口

用法：
  sudo ./mysql-rpm/install-mysql.sh [选项]

选项：
  --bind-address <地址>   MySQL 监听地址，默认 0.0.0.0
  --port <端口>           MySQL 端口，默认 3306
  --credential-file <路径> 保存随机凭据，默认 /root/ftc-mysql-credentials.txt
  --yes                   跳过资源确认
  --dry-run               只检查系统、RPM和配置，不安装
  --os-major <8|9>        仅配合 --dry-run，用于离线校验指定 EL版本
  --arch <x86_64>         仅配合 --dry-run，用于离线校验架构
  -h, --help              显示帮助

支持：EL8/EL9 兼容系统，x86_64。
注意：脚本拒绝覆盖已安装的 MySQL/MariaDB Server，也不会迁移旧数据库。
EOF
}

die() {
  printf '错误：%s\n' "$*" >&2
  exit 1
}

info() {
  printf '==> %s\n' "$*"
}

confirm() {
  local prompt="$1"
  if [[ "$ASSUME_YES" == true ]]; then
    return 0
  fi
  [[ -t 0 ]] || die "当前不是交互终端；确认风险后使用 --yes"
  local answer
  read -r -p "${prompt} [y/N] " answer
  case "$answer" in
    y|Y|yes|YES) return 0 ;;
    *) return 1 ;;
  esac
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bind-address)
      [[ $# -ge 2 ]] || die "--bind-address 缺少参数"
      BIND_ADDRESS="$2"
      shift 2
      ;;
    --port)
      [[ $# -ge 2 ]] || die "--port 缺少参数"
      MYSQL_PORT="$2"
      shift 2
      ;;
    --credential-file)
      [[ $# -ge 2 ]] || die "--credential-file 缺少参数"
      CREDENTIAL_FILE="$2"
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
    --os-major)
      [[ $# -ge 2 ]] || die "--os-major 缺少参数"
      OS_MAJOR_OVERRIDE="$2"
      shift 2
      ;;
    --arch)
      [[ $# -ge 2 ]] || die "--arch 缺少参数"
      ARCH_OVERRIDE="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *) die "未知参数：$1（使用 --help 查看帮助）" ;;
  esac
done

[[ "$MYSQL_PORT" =~ ^[0-9]+$ ]] || die "端口必须是数字"
(( MYSQL_PORT >= 1 && MYSQL_PORT <= 65535 )) || die "端口必须在 1–65535 之间"
[[ "$BIND_ADDRESS" != *$'\n'* && -n "$BIND_ADDRESS" ]] || die "监听地址无效"

if [[ -n "$OS_MAJOR_OVERRIDE" || -n "$ARCH_OVERRIDE" ]]; then
  [[ "$DRY_RUN" == true ]] || die "--os-major 和 --arch 只能配合 --dry-run 使用"
fi

if [[ "$DRY_RUN" != true && "$EUID" -ne 0 ]]; then
  die "必须使用 root 权限运行：sudo $0"
fi

ARCH="${ARCH_OVERRIDE:-$(uname -m)}"
[[ "$ARCH" == "x86_64" ]] || die "本交付包仅支持 x86_64，当前架构为 ${ARCH}"

if [[ -n "$OS_MAJOR_OVERRIDE" ]]; then
  OS_MAJOR="$OS_MAJOR_OVERRIDE"
  OS_NAME="dry-run override"
else
  [[ -r /etc/os-release ]] || die "无法读取 /etc/os-release"
  # shellcheck disable=SC1091
  source /etc/os-release
  OS_MAJOR="${VERSION_ID%%.*}"
  OS_NAME="${PRETTY_NAME:-${NAME:-unknown}}"
fi
case "$OS_MAJOR" in
  8|9) ;;
  *) die "仅支持 EL8/EL9 兼容系统，当前系统为 ${OS_NAME}（大版本 ${OS_MAJOR}）" ;;
esac

RPM_DIR="${BUNDLE_ROOT}/packages/el${OS_MAJOR}/x86_64"
GPG_KEY="${BUNDLE_ROOT}/RPM-GPG-KEY-mysql-2025"
[[ -d "$RPM_DIR" ]] || die "找不到 RPM目录：$RPM_DIR"
[[ -f "$GPG_KEY" ]] || die "找不到 MySQL官方 GPG公钥：$GPG_KEY"

required_packages=(server client client-plugins common libs icu-data-files libs-compat)
for package_name in "${required_packages[@]}"; do
  package_file="${RPM_DIR}/mysql-community-${package_name}-8.4.9-1.el${OS_MAJOR}.x86_64.rpm"
  [[ -f "$package_file" ]] || die "RPM缺失：$package_file"
done

info "目标系统：${OS_NAME} / x86_64"
info "RPM目录：${RPM_DIR}"
info "MySQL版本：8.4.9"

if [[ "$DRY_RUN" == true ]]; then
  printf '\n将生成的核心配置：\n'
  printf '  bind-address=%s\n' "$BIND_ADDRESS"
  printf '  port=%s\n' "$MYSQL_PORT"
  printf '  character-set-server=utf8mb4\n'
  printf '  collation-server=utf8mb4_0900_ai_ci\n'
  printf 'dry-run 完成，没有安装 RPM、修改配置或启动服务。\n'
  exit 0
fi

command -v rpm >/dev/null 2>&1 || die "未找到 rpm"
if command -v dnf >/dev/null 2>&1; then
  PACKAGE_MANAGER=dnf
elif command -v yum >/dev/null 2>&1; then
  PACKAGE_MANAGER=yum
else
  die "未找到 dnf 或 yum"
fi
command -v systemctl >/dev/null 2>&1 || die "目标系统未使用 systemd"
command -v openssl >/dev/null 2>&1 || die "未找到 openssl"

installed_server="$(rpm -qa | grep -Ei '^(mysql-community-server|mariadb-server|MariaDB-server)(-|$)' || true)"
if [[ -n "$installed_server" ]]; then
  printf '检测到已安装数据库服务端软件包：\n%s\n' "$installed_server" >&2
  die "为防止覆盖数据，脚本不会自动删除或升级已有 MySQL/MariaDB"
fi
if [[ -d /var/lib/mysql ]] && find /var/lib/mysql -mindepth 1 -maxdepth 1 ! -name lost+found -print -quit | grep -q .; then
  die "/var/lib/mysql 已存在数据；请先完成备份和迁移评审"
fi
CONFIG_DIR=/etc/my.cnf.d
CONFIG_FILE="${CONFIG_DIR}/ftc-platform.cnf"
[[ ! -e "$CONFIG_FILE" ]] || die "配置文件已存在：$CONFIG_FILE"

MEMORY_MB="$(awk '/^MemTotal:/ {print int($2/1024)}' /proc/meminfo)"
[[ "$MEMORY_MB" =~ ^[0-9]+$ ]] || die "无法读取服务器内存"
if (( MEMORY_MB < 8192 )); then
  die "服务器内存只有 ${MEMORY_MB} MiB，低于生产最低要求 8 GiB"
fi
if (( MEMORY_MB < 16384 )); then
  printf '警告：服务器内存为 %s MiB，承载 60+ 集群建议至少 16 GiB。\n' "$MEMORY_MB"
  confirm "仍要继续安装吗？" || { printf '已取消安装。\n'; exit 0; }
fi
BUFFER_POOL_MB=$(( MEMORY_MB * 60 / 100 ))
if (( BUFFER_POOL_MB > 24576 )); then
  BUFFER_POOL_MB=24576
fi

printf '\n即将执行：\n'
printf '  1. 安装 MySQL Community 8.4.9 官方 RPM。\n'
printf '  2. 写入 FTC 生产参数并监听 %s:%s。\n' "$BIND_ADDRESS" "$MYSQL_PORT"
printf '  3. 随机生成 root 与 ftc 应用密码。\n'
printf '  4. 创建 ftc_platform 数据库和 ftc@%% 最小权限账号。\n'
confirm "确认这是一台新的外部 MySQL服务器并继续吗？" || { printf '已取消安装。\n'; exit 0; }

info "导入 MySQL官方 GPG公钥并检查 RPM签名"
rpm --import "$GPG_KEY"
rpm --checksig "${RPM_DIR}"/*.rpm >/dev/null

info "离线安装 MySQL RPM"
"$PACKAGE_MANAGER" install -y --disablerepo='*' "${RPM_DIR}"/*.rpm

mkdir -p "$CONFIG_DIR"
cat > "$CONFIG_FILE" <<EOF
[mysqld]
bind-address=${BIND_ADDRESS}
port=${MYSQL_PORT}
character-set-server=utf8mb4
collation-server=utf8mb4_0900_ai_ci
default-time-zone=+00:00
skip-name-resolve=ON

max_connections=300
max_allowed_packet=64M
innodb_buffer_pool_size=${BUFFER_POOL_MB}M
innodb_redo_log_capacity=4G
innodb_flush_log_at_trx_commit=1
sync_binlog=1

log_bin=mysql-bin
binlog_format=ROW
binlog_expire_logs_seconds=604800
slow_query_log=ON
long_query_time=1
EOF
chmod 0644 "$CONFIG_FILE"
mysqld --validate-config

info "启动并启用 mysqld"
systemctl enable --now mysqld
for _ in $(seq 1 60); do
  if systemctl is-active --quiet mysqld; then
    break
  fi
  sleep 2
done
systemctl is-active --quiet mysqld || die "mysqld 未能启动，请检查 journalctl -u mysqld"

TEMP_ROOT_PASSWORD="$(sed -n 's/.*temporary password is generated for root@localhost: //p' /var/log/mysqld.log | tail -1)"
[[ -n "$TEMP_ROOT_PASSWORD" ]] || die "无法从 /var/log/mysqld.log 读取一次性 root 密码"

random_password() {
  printf 'Ftc!9%s' "$(openssl rand -hex 14)"
}

ROOT_PASSWORD="$(random_password)"
FTC_PASSWORD="$(random_password)"

umask 077
CREDENTIAL_DIR="$(dirname "$CREDENTIAL_FILE")"
if [[ ! -d "$CREDENTIAL_DIR" ]]; then
  mkdir -p -m 0700 "$CREDENTIAL_DIR"
fi
SERVER_IPS="$(hostname -I 2>/dev/null | xargs || true)"

write_credentials() {
  local initialization_status="$1"
  cat > "$CREDENTIAL_FILE" <<EOF
FTC 平台外部 MySQL 初始化结果
状态：${initialization_status}
生成时间：$(date -u '+%Y-%m-%dT%H:%M:%SZ')
服务器地址候选：${SERVER_IPS:-请填写数据库服务器可达 IP 或 DNS}
端口：${MYSQL_PORT}
数据库：ftc_platform
平台用户名：ftc
平台密码：${FTC_PASSWORD}
TLS：按企业策略配置；当前脚本未签发企业 TLS证书
MySQL root 密码：${ROOT_PASSWORD}
EOF
  chmod 0600 "$CREDENTIAL_FILE"
}

# 先保存随机密码，避免 ALTER USER 成功后因后续步骤失败而丢失凭据。
write_credentials "初始化进行中；如脚本失败，请先检查 MySQL实际状态，不要重复执行"

info "初始化 root 密码和 FTC 平台数据库"
mysql --connect-expired-password --user=root --password="$TEMP_ROOT_PASSWORD" <<SQL
ALTER USER 'root'@'localhost' IDENTIFIED BY '${ROOT_PASSWORD}';
CREATE DATABASE IF NOT EXISTS ftc_platform CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE USER IF NOT EXISTS 'ftc'@'%' IDENTIFIED BY '${FTC_PASSWORD}';
ALTER USER 'ftc'@'%' IDENTIFIED BY '${FTC_PASSWORD}';
GRANT ALL PRIVILEGES ON ftc_platform.* TO 'ftc'@'%';
FLUSH PRIVILEGES;
SQL
write_credentials "初始化完成"

VERIFY_HOST="$BIND_ADDRESS"
if [[ "$VERIFY_HOST" == "0.0.0.0" || "$VERIFY_HOST" == "::" ]]; then
  VERIFY_HOST=127.0.0.1
fi
MYSQL_PWD="$FTC_PASSWORD" mysql --host="$VERIFY_HOST" --port="$MYSQL_PORT" --user=ftc \
  --database=ftc_platform --execute='SELECT VERSION() AS version, @@character_set_server AS charset, @@collation_server AS collation;'

printf '\nMySQL 安装与 FTC 数据库初始化完成。\n'
printf '凭据文件：%s（权限 0600，请立即纳入密码保管系统）\n' "$CREDENTIAL_FILE"
printf '平台初始化页面填写：<数据库服务器可达IP>:%s / ftc_platform / ftc / 凭据文件中的平台密码。\n' "$MYSQL_PORT"
printf '脚本未自动开放防火墙。仅允许 Kubernetes 节点或平台 Pod 网段访问 TCP %s。\n' "$MYSQL_PORT"
printf '此安装为单机外部 MySQL；必须另行配置主备、高可用或经过恢复演练的备份方案。\n'
