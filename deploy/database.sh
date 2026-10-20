#!/usr/bin/env bash

# 由deploy.sh加载，复用compose_command及部署锁。备份包含敏感数据，只保留在服务器。
DATABASE_BACKUP_FILE=""

database_psql() {
  compose_command exec -T postgres sh -ec \
    'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
}

backup_database() {
  local release_sha="$1"
  local backup_dir="$SOLVIFY_ROOT/shared/backups"
  local partial_file backup_file
  mkdir -p "$backup_dir"
  chmod 0700 "$backup_dir"
  partial_file="$(mktemp "$backup_dir/$(date -u +%Y%m%dT%H%M%SZ)-${release_sha}.XXXXXX.dump.partial")"
  chmod 0600 "$partial_file"

  log "备份当前 PostgreSQL 数据库"
  if ! compose_command exec -T postgres sh -ec \
    'exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom' >"$partial_file"; then
    rm -f -- "$partial_file"
    log "数据库备份失败，停止发布"
    return 1
  fi
  # 不只读取TOC：解码全部归档，发现截断/数据块损坏后不得继续迁移。
  if [[ ! -s "$partial_file" ]] ||
    ! compose_command exec -T postgres pg_restore --list <"$partial_file" >/dev/null ||
    ! compose_command exec -T postgres pg_restore --file=/dev/null <"$partial_file"; then
    rm -f -- "$partial_file"
    log "数据库备份校验失败，停止发布"
    return 1
  fi
  backup_file="${partial_file%.partial}"
  mv -- "$partial_file" "$backup_file"
  (cd "$backup_dir" && sha256sum "$(basename -- "$backup_file")") >"${backup_file}.sha256"
  chmod 0600 "${backup_file}.sha256"
  DATABASE_BACKUP_FILE="$backup_file"
  log "数据库备份校验完成：${backup_file}"
}

validate_migration_package() {
  local name count=0
  [[ -s "$SCRIPT_DIR/migrations.list" ]] || { log "数据库迁移清单缺失"; return 1; }
  while IFS= read -r name || [[ -n "$name" ]]; do
    name="${name%$'\r'}"
    [[ "$name" =~ ^[0-9]{8}_[a-z0-9_]+\.sql$ ]] || { log "数据库迁移文件名无效"; return 1; }
    [[ -s "$SCRIPT_DIR/migrations/$name" ]] || { log "数据库迁移文件缺失：${name}"; return 1; }
    # 清单中的SQL只允许独立的外层事务，执行时统一封装版本写入。
    [[ "$(grep -Ec '^[[:space:]]*BEGIN;[[:space:]]*$' "$SCRIPT_DIR/migrations/$name")" -eq 1 ]] &&
      [[ "$(grep -Ec '^[[:space:]]*COMMIT;[[:space:]]*$' "$SCRIPT_DIR/migrations/$name")" -eq 1 ]] ||
      { log "数据库迁移外层事务格式无效：${name}"; return 1; }
    count=$((count + 1))
  done <"$SCRIPT_DIR/migrations.list"
  [[ "$count" -gt 0 ]]
}

write_migration_batch() {
  local name version
  cat <<'SQL'
\set ON_ERROR_STOP on
SET lock_timeout = '30s';
SET statement_timeout = '5min';
SET search_path TO public;
SELECT pg_advisory_lock(hashtextextended('solvify-agent-schema-upgrade', 0));
CREATE TABLE IF NOT EXISTS app_schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
SQL
  while IFS= read -r name || [[ -n "$name" ]]; do
    name="${name%$'\r'}"
    version="${name%.sql}"
    printf "SELECT EXISTS(SELECT 1 FROM app_schema_migrations WHERE version='%s') AS applied \\gset\n" "$version"
    printf '\\if :applied\n\\echo 已完成：%s\n\\else\nBEGIN;\n' "$version"
    sed -E '/^[[:space:]]*(BEGIN|COMMIT);[[:space:]]*$/d' "$SCRIPT_DIR/migrations/$name" || return 1
    printf '\n%s\n' "INSERT INTO app_schema_migrations(version) VALUES ('$version') ON CONFLICT DO NOTHING;" 'COMMIT;' '\endif'
  done <"$SCRIPT_DIR/migrations.list"
  cat <<'SQL'
DO $$ BEGIN
 IF (SELECT count(*) FROM pg_attribute WHERE attrelid=to_regclass('chat_messages') AND NOT attisdropped
     AND attname IN ('embedding_status','embedding_attempts','embedding_available_at','embedding_lease_until','embedding_lease_token','embedding_error')) <> 6
    OR to_regclass('context_artifacts') IS NULL OR to_regclass('mcp_connections') IS NULL OR to_regclass('mcp_templates') IS NULL THEN
  RAISE EXCEPTION '升级后必需的业务表或消息向量字段仍缺失';
 END IF;
END $$;
SELECT pg_advisory_unlock(hashtextextended('solvify-agent-schema-upgrade', 0));
SQL
}

migrate_database() {
  local batch_file migration_log
  [[ -n "$DATABASE_BACKUP_FILE" && -s "$DATABASE_BACKUP_FILE" ]] || { log "缺少本次已校验备份，禁止迁移"; return 1; }
  batch_file="$(mktemp "$SOLVIFY_ROOT/shared/backups/migration.XXXXXX.sql")"
  chmod 0600 "$batch_file"
  migration_log="${DATABASE_BACKUP_FILE}.migration.log"
  (umask 077; : >"$migration_log")
  if ! write_migration_batch >"$batch_file"; then
    rm -f -- "$batch_file"
    return 1
  fi
  log "执行已有数据库增量迁移，已应用版本保持不变"
  # 单连接会话锁覆盖全部迁移；每个版本及其记录共用事务。失败只记录在本机私有日志。
  if ! database_psql <"$batch_file" >"$migration_log" 2>&1; then
    rm -f -- "$batch_file"
    log "数据库迁移失败，停止更新应用；请检查私有日志：${migration_log}"
    return 1
  fi
  rm -f -- "$batch_file"
  log "数据库迁移和必需结构检查通过"
}
