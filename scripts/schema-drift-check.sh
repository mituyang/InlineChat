#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"
COMPOSE_FILE="${COMPOSE_FILE:-$ROOT_DIR/infra/docker/docker-compose.yml}"

TMP_DB="inlinechat_schema_check_tmp_$$"
TMP_EXPECTED="$(mktemp)"
TMP_ACTUAL="$(mktemp)"
TMP_DIFF="$(mktemp)"
TMP_DB_CREATED=0

cleanup() {
  set +e
  if [ "$TMP_DB_CREATED" = "1" ]; then
    drop_database_if_exists "$TMP_DB" >/dev/null 2>&1 || true
  fi
  rm -f "$TMP_EXPECTED" "$TMP_ACTUAL" "$TMP_DIFF"
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
    echo "schema-check 缺少环境变量: $key" >&2
    exit 1
  fi
  printf '%s' "$value"
}

parse_db_name_from_url() {
  local url="$1"
  local base="${url%%\?*}"
  printf '%s' "${base##*/}"
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

mysql_query() {
  local sql="$1"
  compose exec -T mysql sh -lc 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot -N -B' <<<"$sql"
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

create_temp_database() {
  local db_name="$1"
  mysql_query "DROP DATABASE IF EXISTS \`${db_name}\`;
CREATE DATABASE \`${db_name}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
GRANT ALL PRIVILEGES ON \`${db_name}\`.* TO '${MYSQL_USER}'@'%';
FLUSH PRIVILEGES;"
}

list_tables() {
  local db_name="$1"
  mysql_query "
SELECT TABLE_NAME
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = '${db_name}'
  AND TABLE_TYPE = 'BASE TABLE'
  AND TABLE_NAME NOT IN ('schema_migrations_chat', 'schema_migrations_auth', 'schema_migrations_admin')
ORDER BY TABLE_NAME;
"
}

normalize_create_table() {
  local raw_line="$1"
  local create_stmt="${raw_line#*$'\t'}"
  printf '%b\n' "$create_stmt" | sed -E \
    -e 's/ AUTO_INCREMENT=[0-9]+//g' \
    -e 's/[[:space:]]+$//'
}

dump_schema() {
  local db_name="$1"
  local output_file="$2"
  : >"$output_file"
  while IFS= read -r table_name; do
    [ -n "$table_name" ] || continue
    printf -- "-- table:%s\n" "$table_name" >>"$output_file"
    local create_row
    create_row="$(mysql_query "SHOW CREATE TABLE \`${db_name}\`.\`${table_name}\`;")"
    normalize_create_table "$create_row" >>"$output_file"
    printf '\n' >>"$output_file"
  done < <(list_tables "$db_name")
}

run_service_migrations() {
  local service_name="$1"
  local migrate_url="$2"
  compose run --rm --no-deps -T "$service_name" -path=/migrations "-database=${migrate_url}" up >/dev/null
}

MYSQL_DATABASE="$(require_env_value "MYSQL_DATABASE")"
MYSQL_USER="$(require_env_value "MYSQL_USER")"
CHAT_MIGRATE_URL="$(require_env_value "CHAT_MYSQL_MIGRATE_URL")"
AUTH_MIGRATE_URL="$(require_env_value "AUTH_MYSQL_MIGRATE_URL")"
ADMIN_MIGRATE_URL="$(require_env_value "ADMIN_MYSQL_MIGRATE_URL")"

CHAT_DB_NAME="$(parse_db_name_from_url "$CHAT_MIGRATE_URL")"
AUTH_DB_NAME="$(parse_db_name_from_url "$AUTH_MIGRATE_URL")"
ADMIN_DB_NAME="$(parse_db_name_from_url "$ADMIN_MIGRATE_URL")"

if [ "$CHAT_DB_NAME" != "$MYSQL_DATABASE" ] || [ "$AUTH_DB_NAME" != "$MYSQL_DATABASE" ] || [ "$ADMIN_DB_NAME" != "$MYSQL_DATABASE" ]; then
  echo "schema-check 仅支持三组 migrations 指向同一个业务数据库" >&2
  echo "MYSQL_DATABASE=$MYSQL_DATABASE chat=$CHAT_DB_NAME auth=$AUTH_DB_NAME admin=$ADMIN_DB_NAME" >&2
  exit 1
fi

if ! database_exists "$MYSQL_DATABASE"; then
  echo "schema-check 失败：当前数据库 '$MYSQL_DATABASE' 不存在" >&2
  echo "请先确认 mysql 初始化完成，或手动创建数据库后重试。" >&2
  exit 1
fi

ACTUAL_TABLE_COUNT="$(list_tables "$MYSQL_DATABASE" | awk 'NF > 0 { count++ } END { print count + 0 }')"
if [ "$ACTUAL_TABLE_COUNT" -eq 0 ]; then
  echo "schema-check 跳过结构对比：数据库 '$MYSQL_DATABASE' 当前为空，后续启动会按当前 migrations 建库。"
  exit 0
fi

create_temp_database "$TMP_DB" >/dev/null
TMP_DB_CREATED=1

run_service_migrations "chat-migrate" "$(replace_db_name_in_url "$CHAT_MIGRATE_URL" "$TMP_DB")"
run_service_migrations "auth-migrate" "$(replace_db_name_in_url "$AUTH_MIGRATE_URL" "$TMP_DB")"
run_service_migrations "admin-migrate" "$(replace_db_name_in_url "$ADMIN_MIGRATE_URL" "$TMP_DB")"

dump_schema "$TMP_DB" "$TMP_EXPECTED"
dump_schema "$MYSQL_DATABASE" "$TMP_ACTUAL"

if ! diff -u "$TMP_EXPECTED" "$TMP_ACTUAL" >"$TMP_DIFF"; then
  echo "schema-check 失败：当前数据库 '$MYSQL_DATABASE' 与仓库 migrations 生成的结构不一致。" >&2
  echo "建议处理方式：新增 migration 修正结构，或手动重建本地数据库。" >&2
  echo >&2
  cat "$TMP_DIFF" >&2
  exit 1
fi

echo "schema-check 通过：当前数据库 '$MYSQL_DATABASE' 的结构与仓库 migrations 一致。"
