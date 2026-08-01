#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
SOLVIFY_ROOT="${SOLVIFY_ROOT:-/opt/solvify-agent}"
DEPLOY_USER="${DEPLOY_USER:-${SUDO_USER:-}}"

# 输出普通信息
log() {
  printf '[初始化] %s\n' "$*"
}

# 输出错误并终止
fail() {
  printf '[初始化错误] %s\n' "$*" >&2
  exit 1
}

# 检查服务器所需命令
require_command() {
  local command_name="$1"
  command -v "$command_name" >/dev/null 2>&1 || fail "缺少命令：${command_name}"
}

# 复制首次配置且不覆盖已有文件
install_if_missing() {
  local source_file="$1"
  local target_file="$2"
  local mode="$3"

  if [[ -e "$target_file" ]]; then
    log "保留已有配置：${target_file}"
    return
  fi

  install -m "$mode" "$source_file" "$target_file"
  log "已创建配置：${target_file}"
}

# 执行服务器初始化
main() {
  [[ "$EUID" -eq 0 ]] || fail "请使用 sudo 执行此脚本"
  [[ -n "$DEPLOY_USER" ]] || fail "请通过 DEPLOY_USER 指定服务器部署用户"
  id "$DEPLOY_USER" >/dev/null 2>&1 || fail "部署用户不存在：${DEPLOY_USER}"

  require_command docker
  require_command flock
  docker compose version >/dev/null 2>&1 || fail "需要安装 Docker Compose v2"
  docker info >/dev/null 2>&1 || fail "Docker Engine 当前不可用"

  local deploy_uid
  local deploy_gid
  deploy_uid="$(id -u "$DEPLOY_USER")"
  deploy_gid="$(id -g "$DEPLOY_USER")"

  install -d -m 0755 -o "$deploy_uid" -g "$deploy_gid" "$SOLVIFY_ROOT"
  install -d -m 0755 -o "$deploy_uid" -g "$deploy_gid" "$SOLVIFY_ROOT/releases"
  install -d -m 0750 -o "$deploy_uid" -g "$deploy_gid" "$SOLVIFY_ROOT/shared"
  install -d -m 0750 -o 10001 -g 10001 "$SOLVIFY_ROOT/shared/data"
  install -d -m 0750 -o 10001 -g 10001 "$SOLVIFY_ROOT/shared/logs"

  install_if_missing \
    "$SCRIPT_DIR/.env.production.example" \
    "$SOLVIFY_ROOT/shared/.env" \
    0600
  chown "$deploy_uid:$deploy_gid" "$SOLVIFY_ROOT/shared/.env"

  install_if_missing \
    "$SCRIPT_DIR/config.production.yaml.example" \
    "$SOLVIFY_ROOT/shared/config.yaml" \
    0640
  chown "$deploy_uid:10001" "$SOLVIFY_ROOT/shared/config.yaml"

  if getent group docker >/dev/null 2>&1 && ! id -nG "$DEPLOY_USER" | tr ' ' '\n' | grep -qx docker; then
    usermod -aG docker "$DEPLOY_USER"
    log "已将 ${DEPLOY_USER} 加入 docker 用户组，请重新登录服务器后再部署"
  fi

  log "服务器目录初始化完成：${SOLVIFY_ROOT}"
  log "请编辑 ${SOLVIFY_ROOT}/shared/.env"
  log "请编辑 ${SOLVIFY_ROOT}/shared/config.yaml"
}

main "$@"

