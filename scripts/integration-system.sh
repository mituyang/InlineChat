#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"
COMPOSE_FILE="$ROOT_DIR/infra/docker/docker-compose.yml"

if [ ! -f "$ENV_FILE" ]; then
  echo "缺少环境文件: $ENV_FILE"
  echo "请先执行: cp .env.example .env"
  exit 1
fi

read_env() {
  local key="$1"
  local value
  value="$(grep -E "^${key}=" "$ENV_FILE" | head -n1 | cut -d= -f2- || true)"
  printf "%s" "$value"
}

require_env() {
  local key="$1"
  if [ -z "${!key:-}" ]; then
    echo "缺少必要环境变量: $key"
    exit 1
  fi
}

MYSQL_DATABASE="${MYSQL_DATABASE:-$(read_env MYSQL_DATABASE)}"
MYSQL_USER="${MYSQL_USER:-$(read_env MYSQL_USER)}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:-$(read_env MYSQL_PASSWORD)}"
GATEWAY_HTTP_PORT="${GATEWAY_HTTP_PORT:-$(read_env GATEWAY_HTTP_PORT)}"
GATEWAY_HTTP_PORT="${GATEWAY_HTTP_PORT:-8200}"
GATEWAY_URL="${INTEGRATION_GATEWAY_URL:-http://127.0.0.1:${GATEWAY_HTTP_PORT}}"
DISCOVERY_PREFIX="${DISCOVERY_PREFIX:-$(read_env DISCOVERY_PREFIX)}"
DISCOVERY_PREFIX="${DISCOVERY_PREFIX:-/inlinechat/services}"

require_env MYSQL_DATABASE
require_env MYSQL_USER
require_env MYSQL_PASSWORD

compose_cmd=(docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE")

echo "[1/5] 运行端到端冒烟"
ENV_FILE="$ENV_FILE" "$ROOT_DIR/scripts/smoke-e2e.sh"

echo "[2/5] 校验 etcd 服务注册"
etcd_keys="$("${compose_cmd[@]}" exec -T etcd etcdctl --endpoints=http://127.0.0.1:2379 get "$DISCOVERY_PREFIX" --prefix --keys-only)"
for service in chat-service auth-service admin-service realtime-service; do
  if ! grep -q "$service" <<<"$etcd_keys"; then
    echo "  etcd 缺少服务注册: $service"
    exit 1
  fi
done
echo "  etcd 注册校验通过"

echo "[3/5] 校验 MySQL 迁移结果"
mysql_check_sql="SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='${MYSQL_DATABASE}' AND table_name IN ('conversations','messages','sites','agents');"
mysql_table_count="$("${compose_cmd[@]}" exec -T mysql sh -lc "mysql -u\"$MYSQL_USER\" -p\"$MYSQL_PASSWORD\" -D\"$MYSQL_DATABASE\" -Nse \"$mysql_check_sql\"")"
if [ "$mysql_table_count" != "4" ]; then
  echo "  MySQL 表校验失败，期望 4，实际 ${mysql_table_count}"
  exit 1
fi
echo "  MySQL 表校验通过"

echo "[4/5] 校验 Redis + WebSocket + gRPC 消息链路"
(
  cd "$ROOT_DIR/services/gateway-service"
  GATEWAY_URL="$GATEWAY_URL" \
  GOCACHE="$ROOT_DIR/.cache/go-build" \
  GOMODCACHE="$ROOT_DIR/.cache/go-mod" \
  go run ./cmd/ws-push-check
)

echo "[5/5] 集成测试完成"
echo "integration_ok gateway=${GATEWAY_URL}"
