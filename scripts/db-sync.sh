#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"
COMPOSE_FILE="${COMPOSE_FILE:-$ROOT_DIR/infra/docker/docker-compose.yml}"
DB_SYNC_MODE="${DB_SYNC_MODE:-sync}"

SERVICE_NAMES=("chat-service" "auth-service" "admin-service")
MIGRATE_SERVICE_NAMES=("chat-migrate" "auth-migrate" "admin-migrate")
MIGRATION_DIRS=(
  "$ROOT_DIR/services/chat-service/migrations"
  "$ROOT_DIR/services/auth-service/migrations"
  "$ROOT_DIR/services/admin-service/migrations"
)
MIGRATE_URL_KEYS=(
  "CHAT_MYSQL_MIGRATE_URL"
  "AUTH_MYSQL_MIGRATE_URL"
  "ADMIN_MYSQL_MIGRATE_URL"
)
APP_SERVICES=(
  "gateway-service"
  "ai-service"
  "realtime-service"
  "admin-service"
  "auth-service"
  "chat-service"
)

TARGET_DB=""
TARGET_DB_READY=0
FINALIZE_STARTED=0

cleanup() {
  local exit_code=$?
  set +e
  if [ "$exit_code" -eq 0 ]; then
    if [ -n "$TARGET_DB" ]; then
      drop_database_if_exists "$TARGET_DB" >/dev/null 2>&1 || true
    fi
    return
  fi

  if [ -n "$TARGET_DB" ] && [ "$FINALIZE_STARTED" = "0" ]; then
    drop_database_if_exists "$TARGET_DB" >/dev/null 2>&1 || true
  fi

  if [ -n "$TARGET_DB" ] && [ "$TARGET_DB_READY" = "1" ]; then
    echo "db-sync 失败：已保留临时数据库 '$TARGET_DB' 供排查。"
  fi
}

trap cleanup EXIT

compose() {
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"
}

read_env_value() {
  local key="$1"
  awk -v target="$key" '
    {
      line = $0
      sub(/\r$/, "", line)
      if (line ~ /^[[:space:]]*#/ || line ~ /^[[:space:]]*$/) {
        next
      }
      sub(/^[[:space:]]+/, "", line)
      if (line ~ /^export[[:space:]]+/) {
        sub(/^export[[:space:]]+/, "", line)
      }
      split(line, parts, "=")
      current = parts[1]
      gsub(/[[:space:]]/, "", current)
      if (current != target) {
        next
      }
      sub(/^[^=]*=/, "", line)
      print line
      found = 1
      exit
    }
    END {
      if (!found) {
        exit 1
      }
    }
  ' "$ENV_FILE"
}

require_env_value() {
  local key="$1"
  local value
  if ! value="$(read_env_value "$key")"; then
    echo "db-sync 缺少环境变量: $key" >&2
    exit 1
  fi
  printf '%s' "$value"
}

parse_db_name_from_url() {
  local url="$1"
  local base="${url%%\?*}"
  printf '%s' "${base##*/}"
}

parse_migrations_table_from_url() {
  local url="$1"
  local query="${url#*\?}"
  local param

  if [ "$query" = "$url" ]; then
    echo "db-sync 失败：迁移连接串缺少 x-migrations-table: $url" >&2
    exit 1
  fi

  IFS='&' read -r -a params <<<"$query"
  for param in "${params[@]}"; do
    if [[ "$param" == x-migrations-table=* ]]; then
      printf '%s' "${param#*=}"
      return
    fi
  done

  echo "db-sync 失败：迁移连接串缺少 x-migrations-table: $url" >&2
  exit 1
}

replace_db_name_in_url() {
  local url="$1"
  local db_name="$2"
  local base="${url%%\?*}"
  local suffix=""
  if [[ "$url" == *\?* ]]; then
    suffix="?${url#*\?}"
  fi
  printf '%s%s' "${base%/*}/$db_name" "$suffix"
}

normalize_dirty_flag() {
  local dirty="${1:-0}"
  case "$dirty" in
    1|true|TRUE|True) printf '1' ;;
    *) printf '0' ;;
  esac
}

mysql_query() {
  local sql="$1"
  compose exec -T mysql sh -lc 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot -N -B' <<<"$sql"
}

mysql_execute_file() {
  local sql_file="$1"
  compose exec -T mysql sh -lc 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot -N -B' <"$sql_file"
}

database_exists() {
  local db_name="$1"
  local result
  result="$(mysql_query "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = '${db_name}';")"
  [ "$result" = "$db_name" ]
}

drop_database_if_exists() {
  local db_name="$1"
  mysql_query "DROP DATABASE IF EXISTS \`${db_name}\`;"
}

create_database() {
  local db_name="$1"
  mysql_query "CREATE DATABASE \`${db_name}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
GRANT ALL PRIVILEGES ON \`${db_name}\`.* TO '${MYSQL_USER}'@'%';
FLUSH PRIVILEGES;"
}

table_exists() {
  local db_name="$1"
  local table_name="$2"
  local result
  result="$(mysql_query "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='${db_name}' AND TABLE_NAME='${table_name}';")"
  [ "${result:-0}" -gt 0 ]
}

list_repo_versions() {
  local migration_dir="$1"
  find "$migration_dir" -maxdepth 1 -type f -name '*.up.sql' -print \
    | sed -E 's#.*/([0-9]+)_.*#\1#' \
    | sort -n
}

latest_repo_version() {
  local migration_dir="$1"
  local latest
  latest="$(list_repo_versions "$migration_dir" | tail -1 || true)"
  if [ -z "$latest" ]; then
    printf '0'
    return
  fi
  printf '%s' "$((10#$latest))"
}

service_state() {
  local db_name="$1"
  local table_name="$2"
  local version=0
  local dirty=0
  local row

  if ! table_exists "$db_name" "$table_name"; then
    printf '0 0'
    return
  fi

  row="$(mysql_query "SELECT version, dirty FROM \`${db_name}\`.\`${table_name}\` LIMIT 1;")"
  if [ -n "$row" ]; then
    version="${row%%$'\t'*}"
    dirty="${row#*$'\t'}"
    if [ "$dirty" = "$row" ]; then
      dirty=0
    fi
  fi

  printf '%s %s' "${version:-0}" "$(normalize_dirty_flag "$dirty")"
}

list_common_tables() {
  local source_db="$1"
  local target_db="$2"
  mysql_query "
SELECT target.TABLE_NAME
FROM information_schema.TABLES AS target
JOIN information_schema.TABLES AS source
  ON source.TABLE_SCHEMA = '${source_db}'
 AND source.TABLE_NAME = target.TABLE_NAME
 AND source.TABLE_TYPE = 'BASE TABLE'
WHERE target.TABLE_SCHEMA = '${target_db}'
  AND target.TABLE_TYPE = 'BASE TABLE'
  AND target.TABLE_NAME NOT IN ('schema_migrations_chat', 'schema_migrations_auth', 'schema_migrations_admin')
ORDER BY target.TABLE_NAME;
"
}

list_common_columns() {
  local source_db="$1"
  local target_db="$2"
  local table_name="$3"
  mysql_query "
SELECT CONCAT('\`', target.COLUMN_NAME, '\`')
FROM information_schema.COLUMNS AS target
JOIN information_schema.COLUMNS AS source
  ON source.TABLE_SCHEMA = '${source_db}'
 AND source.TABLE_NAME = '${table_name}'
 AND source.COLUMN_NAME = target.COLUMN_NAME
WHERE target.TABLE_SCHEMA = '${target_db}'
  AND target.TABLE_NAME = '${table_name}'
ORDER BY target.ORDINAL_POSITION;
"
}

build_copy_sql_file() {
  local source_db="$1"
  local target_db="$2"
  local output_file="$3"
  local copied_tables=0
  local table_name
  local quoted_columns=""

  : >"$output_file"
  printf 'SET FOREIGN_KEY_CHECKS=0;\n' >>"$output_file"

  while IFS= read -r table_name; do
    [ -n "$table_name" ] || continue
    quoted_columns="$(list_common_columns "$source_db" "$target_db" "$table_name" | paste -sd',' -)"
    if [ -z "$quoted_columns" ]; then
      continue
    fi

    printf 'INSERT INTO `%s`.`%s` (%s) SELECT %s FROM `%s`.`%s`;\n' \
      "$target_db" "$table_name" "$quoted_columns" "$quoted_columns" "$source_db" "$table_name" >>"$output_file"
    copied_tables=$((copied_tables + 1))
  done < <(list_common_tables "$source_db" "$target_db")

  printf 'SET FOREIGN_KEY_CHECKS=1;\n' >>"$output_file"
  printf '%s' "$copied_tables"
}

copy_compatible_data() {
  local source_db="$1"
  local target_db="$2"
  local sql_file
  local copied_tables

  sql_file="$(mktemp)"
  copied_tables="$(build_copy_sql_file "$source_db" "$target_db" "$sql_file")"
  if [ "$copied_tables" = "0" ]; then
    rm -f "$sql_file"
    echo "db-sync：未发现可搬运的数据表，跳过数据导入。"
    return
  fi

  mysql_execute_file "$sql_file"
  rm -f "$sql_file"
  echo "db-sync：已按同名表/同名列搬运 ${copied_tables} 张表数据。"
}

run_migration() {
  local migrate_service="$1"
  local migrate_url="$2"
  local db_name="$3"

  compose run --rm --no-deps -T "$migrate_service" -path=/migrations "-database=$(replace_db_name_in_url "$migrate_url" "$db_name")" up >/dev/null
}

apply_all_current_migrations() {
  local db_name="$1"
  local idx

  for idx in "${!MIGRATE_SERVICE_NAMES[@]}"; do
    run_migration "${MIGRATE_SERVICE_NAMES[$idx]}" "${MIGRATE_URLS[$idx]}" "$db_name"
  done
}

stop_app_services() {
  compose stop "${APP_SERVICES[@]}" >/dev/null 2>&1 || true
}

backup_database() {
  local reason="$1"
  BACKUP_REASON="$reason" ENV_FILE="$ENV_FILE" COMPOSE_FILE="$COMPOSE_FILE" "$ROOT_DIR/scripts/db-backup.sh"
}

rebuild_database() {
  local db_name="$1"

  TARGET_DB="${db_name}_sync_target_$$"
  echo "db-sync：检测到数据库版本高于当前代码，开始按当前 migrations 重建 '$db_name'。"
  backup_database "db-sync-rebuild"
  stop_app_services

  drop_database_if_exists "$TARGET_DB"
  create_database "$TARGET_DB"
  apply_all_current_migrations "$TARGET_DB"
  copy_compatible_data "$db_name" "$TARGET_DB"
  TARGET_DB_READY=1

  FINALIZE_STARTED=1
  drop_database_if_exists "$db_name"
  create_database "$db_name"
  apply_all_current_migrations "$db_name"
  copy_compatible_data "$TARGET_DB" "$db_name"
  drop_database_if_exists "$TARGET_DB"
  TARGET_DB=""
  TARGET_DB_READY=0
  FINALIZE_STARTED=0
  echo "db-sync：数据库已按当前 migrations 重建并完成兼容数据回填。"
}

MYSQL_USER="$(require_env_value "MYSQL_USER")"
MIGRATE_URLS=()
MIGRATION_TABLES=()
REPO_VERSIONS=()
DB_VERSIONS=()
DIRTY_FLAGS=()

for key in "${MIGRATE_URL_KEYS[@]}"; do
  MIGRATE_URLS+=("$(require_env_value "$key")")
done

DB_NAME="$(parse_db_name_from_url "${MIGRATE_URLS[0]}")"
for url in "${MIGRATE_URLS[@]}"; do
  current_db_name="$(parse_db_name_from_url "$url")"
  if [ "$current_db_name" != "$DB_NAME" ]; then
    echo "db-sync 仅支持三组 migrations 指向同一个业务数据库。" >&2
    echo "当前配置: ${MIGRATE_URLS[*]}" >&2
    exit 1
  fi
done

if ! database_exists "$DB_NAME"; then
  echo "db-sync：数据库 '$DB_NAME' 不存在，开始创建。"
  create_database "$DB_NAME"
fi

needs_rebuild=0
needs_up=0

for idx in "${!SERVICE_NAMES[@]}"; do
  MIGRATION_TABLES+=("$(parse_migrations_table_from_url "${MIGRATE_URLS[$idx]}")")
  REPO_VERSIONS+=("$(latest_repo_version "${MIGRATION_DIRS[$idx]}")")
  read -r db_version dirty <<<"$(service_state "$DB_NAME" "${MIGRATION_TABLES[$idx]}")"
  DB_VERSIONS+=("${db_version:-0}")
  DIRTY_FLAGS+=("${dirty:-0}")

  echo "db-sync：${SERVICE_NAMES[$idx]} repo=${REPO_VERSIONS[$idx]} db=${DB_VERSIONS[$idx]} dirty=${DIRTY_FLAGS[$idx]}"

  if [ "${DIRTY_FLAGS[$idx]}" = "1" ]; then
    echo "db-sync 失败：${SERVICE_NAMES[$idx]} 的迁移状态为 dirty，请先手动修复。" >&2
    exit 1
  fi

  if (( 10#${DB_VERSIONS[$idx]} > 10#${REPO_VERSIONS[$idx]} )); then
    needs_rebuild=1
  elif (( 10#${DB_VERSIONS[$idx]} < 10#${REPO_VERSIONS[$idx]} )); then
    needs_up=1
  fi
done

if [ "$needs_rebuild" = "1" ]; then
  if [ "$DB_SYNC_MODE" = "up-only" ]; then
    echo "db-sync 失败：当前数据库版本高于仓库 migrations，up-only 模式不会自动回退。" >&2
    exit 1
  fi
  rebuild_database "$DB_NAME"
  exit 0
fi

if [ "$needs_up" = "1" ]; then
  backup_database "db-sync-up"
  for idx in "${!SERVICE_NAMES[@]}"; do
    if (( 10#${DB_VERSIONS[$idx]} < 10#${REPO_VERSIONS[$idx]} )); then
      echo "db-sync：执行 ${SERVICE_NAMES[$idx]} 向前迁移。"
      run_migration "${MIGRATE_SERVICE_NAMES[$idx]}" "${MIGRATE_URLS[$idx]}" "$DB_NAME"
    fi
  done
  echo "db-sync：数据库已同步到当前 migrations。"
  exit 0
fi

echo "db-sync：数据库版本已与当前 migrations 一致。"
