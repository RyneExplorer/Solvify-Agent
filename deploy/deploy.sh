#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
SOLVIFY_ROOT="${SOLVIFY_ROOT:-/opt/solvify-agent}"
COMPOSE_FILE="$SCRIPT_DIR/compose.prod.yaml"
ENV_FILE="$SOLVIFY_ROOT/shared/.env"
CONFIG_FILE="$SOLVIFY_ROOT/shared/config.yaml"
RELEASE_ENV="$SOLVIFY_ROOT/shared/.release.env"
PREVIOUS_RELEASE_ENV="$SOLVIFY_ROOT/shared/.release.env.previous"
LOCK_FILE="$SOLVIFY_ROOT/deploy.lock"
COMPOSE_PROJECT_NAME="solvify-agent"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-180}"

# 输出部署信息
log() {
  printf '[部署] %s\n' "$*"
}

# 输出错误并终止
fail() {
  printf '[部署错误] %s\n' "$*" >&2
  exit 1
}

# 显示脚本使用方式
usage() {
  printf '用法：%s <后端镜像> <前端镜像> <提交 SHA>\n' "$0"
}

# 检查服务器所需命令
require_command() {
  local command_name="$1"
  command -v "$command_name" >/dev/null 2>&1 || fail "缺少命令：${command_name}"
}

# 执行固定项目和配置文件的 Compose 命令
compose_command() {
  docker compose \
    --project-name "$COMPOSE_PROJECT_NAME" \
    --env-file "$ENV_FILE" \
    --env-file "$RELEASE_ENV" \
    --file "$COMPOSE_FILE" \
    "$@"
}

# 等待指定服务进入健康状态
wait_for_health() {
  local service_name="$1"
  local deadline=$((SECONDS + HEALTH_TIMEOUT_SECONDS))
  local container_id
  local health_status

  while (( SECONDS < deadline )); do
    container_id="$(compose_command ps -q "$service_name" 2>/dev/null || true)"
    if [[ -n "$container_id" ]]; then
      health_status="$(docker inspect \
        --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' \
        "$container_id" 2>/dev/null || true)"
      case "$health_status" in
        healthy)
          log "${service_name} 健康检查通过"
          return 0
          ;;
        exited|dead|unhealthy)
          log "${service_name} 状态异常：${health_status}"
          return 1
          ;;
      esac
    fi
    sleep 3
  done

  log "${service_name} 健康检查超时"
  return 1
}

# 输出有限诊断信息且不读取配置文件
print_diagnostics() {
  compose_command ps || true
  compose_command logs --tail=50 backend frontend || true
}

# 恢复上一组不可变镜像
rollback() {
  if [[ ! -f "$PREVIOUS_RELEASE_ENV" ]]; then
    log "没有可用的上一版本，停止异常应用容器并保留数据服务"
    compose_command stop frontend backend || true
    return 1
  fi

  log "开始恢复上一版本镜像"
  cp "$PREVIOUS_RELEASE_ENV" "$RELEASE_ENV"
  chmod 0600 "$RELEASE_ENV"

  compose_command pull backend frontend || true
  compose_command up -d --no-deps backend || return 1
  wait_for_health backend || return 1
  compose_command up -d --no-deps frontend || return 1
  wait_for_health frontend || return 1

  log "上一版本恢复完成"
}

# 捕获部署错误并触发回滚
handle_error() {
  local exit_code="$?"
  local line_number="${1:-未知}"

  trap - ERR
  set +e
  log "部署在第 ${line_number} 行失败"
  print_diagnostics
  rollback
  local rollback_code="$?"
  if [[ "$rollback_code" -ne 0 ]]; then
    log "自动回滚未完成，请人工检查"
  fi
  exit "$exit_code"
}

# 写入本次发布镜像信息
write_release_env() {
  local backend_image="$1"
  local frontend_image="$2"
  local release_sha="$3"
  local temporary_file="${RELEASE_ENV}.tmp"

  umask 077
  {
    printf 'BACKEND_IMAGE=%s\n' "$backend_image"
    printf 'FRONTEND_IMAGE=%s\n' "$frontend_image"
    printf 'RELEASE_SHA=%s\n' "$release_sha"
    printf 'SOLVIFY_ROOT=%s\n' "$SOLVIFY_ROOT"
  } >"$temporary_file"
  mv "$temporary_file" "$RELEASE_ENV"
}

# 执行生产部署
main() {
  [[ "$#" -eq 3 ]] || {
    usage
    exit 2
  }

  local backend_image="$1"
  local frontend_image="$2"
  local release_sha="$3"
  local app_port

  [[ "$backend_image" == ghcr.io/*:* ]] || fail "后端镜像地址必须来自 ghcr.io"
  [[ "$frontend_image" == ghcr.io/*:* ]] || fail "前端镜像地址必须来自 ghcr.io"
  [[ "$release_sha" =~ ^[0-9a-f]{7,40}$ ]] || fail "提交 SHA 格式无效"

  require_command docker
  require_command flock
  docker compose version >/dev/null 2>&1 || fail "需要安装 Docker Compose v2"

  [[ -f "$COMPOSE_FILE" ]] || fail "Compose 文件不存在：${COMPOSE_FILE}"
  [[ -f "$ENV_FILE" ]] || fail "生产环境变量不存在：${ENV_FILE}"
  [[ -f "$CONFIG_FILE" ]] || fail "生产配置不存在：${CONFIG_FILE}"
  if grep -q "CHANGE_ME" "$ENV_FILE" "$CONFIG_FILE"; then
    fail "生产配置仍包含 CHANGE_ME，请先填写真实值"
  fi

  exec 9>"$LOCK_FILE"
  flock -n 9 || fail "已有部署正在执行"

  rm -f "$PREVIOUS_RELEASE_ENV"
  if [[ -f "$RELEASE_ENV" ]]; then
    cp "$RELEASE_ENV" "$PREVIOUS_RELEASE_ENV"
    chmod 0600 "$PREVIOUS_RELEASE_ENV"
  fi
  write_release_env "$backend_image" "$frontend_image" "$release_sha"

  trap 'handle_error "$LINENO"' ERR

  log "拉取提交 ${release_sha} 对应的应用镜像"
  compose_command pull backend frontend

  log "启动 PostgreSQL 和 Redis"
  compose_command up -d postgres redis
  wait_for_health postgres
  wait_for_health redis

  log "更新后端"
  compose_command up -d --no-deps backend
  wait_for_health backend

  log "更新前端"
  compose_command up -d --no-deps frontend
  wait_for_health frontend

  ln -sfn "$SCRIPT_DIR" "$SOLVIFY_ROOT/current"
  docker image prune -f >/dev/null

  trap - ERR
  app_port="$(sed -n 's/^APP_PORT=//p' "$ENV_FILE" | tail -n 1)"
  app_port="${app_port:-18888}"
  log "部署成功：${release_sha}"
  log "访问地址：http://服务器地址:${app_port}/"
}

main "$@"
