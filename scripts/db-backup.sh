#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"
COMPOSE_FILE="${COMPOSE_FILE:-$ROOT_DIR/infra/docker/docker-compose.yml}"
DB_BACKUP_DIR="${DB_BACKUP_DIR:-$ROOT_DIR/output/db-backups}"
BACKUP_REASON="${BACKUP_REASON:-manual}"

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
    echo "db-backup 缺少环境变量: $key" >&2
    exit 1
  fi
  printf '%s' "$value"
}

slugify() {
  printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9' '-'
}

MYSQL_DATABASE="$(require_env_value "MYSQL_DATABASE")"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
REASON_SLUG="$(slugify "$BACKUP_REASON")"

mkdir -p "$DB_BACKUP_DIR"

BACKUP_PATH="${DB_BACKUP_DIR}/${TIMESTAMP}-${MYSQL_DATABASE}-${REASON_SLUG}-$$.sql.gz"

docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" exec -T \
  -e DB_BACKUP_NAME="$MYSQL_DATABASE" \
  mysql \
  sh -lc 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqldump -uroot --single-transaction --quick --set-gtid-purged=OFF --routines --events --triggers --databases "$DB_BACKUP_NAME"' \
  | gzip -c >"$BACKUP_PATH"

echo "db-backup：已生成备份 $BACKUP_PATH"
