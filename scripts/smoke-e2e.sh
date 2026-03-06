#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"

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

SUPER_ADMIN_EMAIL="${SUPER_ADMIN_EMAIL:-$(read_env SUPER_ADMIN_EMAIL)}"
SUPER_ADMIN_PASSWORD="${SUPER_ADMIN_PASSWORD:-$(read_env SUPER_ADMIN_PASSWORD)}"
SUPER_ADMIN_DISPLAY_NAME="${SUPER_ADMIN_DISPLAY_NAME:-$(read_env SUPER_ADMIN_DISPLAY_NAME)}"

require_env SUPER_ADMIN_EMAIL
require_env SUPER_ADMIN_PASSWORD
require_env SUPER_ADMIN_DISPLAY_NAME

GATEWAY_HTTP_PORT="${GATEWAY_HTTP_PORT:-$(read_env GATEWAY_HTTP_PORT)}"
GATEWAY_HTTP_PORT="${GATEWAY_HTTP_PORT:-8200}"
GATEWAY_URL="${SMOKE_GATEWAY_URL:-http://127.0.0.1:${GATEWAY_HTTP_PORT}}"

RUN_ID="$(date +%s)"
SITE_NAME="Smoke Site ${RUN_ID}"
SITE_DOMAIN="smoke-${RUN_ID}.local"
SITE_ID="site_smoke_${RUN_ID}"
AGENT_ID="$(printf "%04d" $(( ((RUN_ID + $$ + RANDOM) % 9000) + 1000 )))"
AGENT_EMAIL="smoke_agent_${RUN_ID}@example.com"
AGENT_PASSWORD="${SMOKE_AGENT_PASSWORD:-Agent#Smoke2026!}"
AGENT_DISPLAY_NAME="Smoke Agent ${AGENT_ID}"
VISITOR_TOKEN="smoke_visitor_${RUN_ID}"

extract_json_string() {
  local json="$1"
  local key="$2"

  if command -v jq >/dev/null 2>&1; then
    printf "%s" "$json" | jq -r --arg key "$key" '.[$key] // ""'
    return
  fi

  printf "%s" "$json" | sed -n "s/.*\"${key}\":\"\\([^\"]*\\)\".*/\\1/p" | head -n1
}

extract_json_number() {
  local json="$1"
  local key="$2"

  if command -v jq >/dev/null 2>&1; then
    printf "%s" "$json" | jq -r --arg key "$key" '.[$key] // 0'
    return
  fi

  printf "%s" "$json" | sed -n "s/.*\"${key}\":\\([0-9][0-9]*\\).*/\\1/p" | head -n1
}

http_json() {
  local method="$1"
  local url="$2"
  local body="${3:-}"
  local token="${4:-}"

  local args=(
    -fsS
    -X "$method"
    "$url"
    -H "Content-Type: application/json"
  )

  if [ -n "$token" ]; then
    args+=(-H "Authorization: Bearer ${token}")
  fi
  if [ -n "$body" ]; then
    args+=(-d "$body")
  fi

  curl "${args[@]}"
}

extract_widget_session() {
  local html="$1"
  printf "%s" "$html" | sed -n 's/.*__INLINECHAT_WIDGET_SESSION__="\([^"]*\)".*/\1/p' | head -n1
}

fetch_widget_session() {
  local site_id="$1"
  local site_domain="$2"
  local parent_origin="https://${site_domain}"
  local widget_html
  widget_html="$(curl -fsS \
    -H "Referer: ${parent_origin}/smoke" \
    "${GATEWAY_URL}/app/widget/?site_id=${site_id}&parent_origin=https%3A%2F%2F${site_domain}")"
  extract_widget_session "$widget_html"
}

wait_gateway_ready() {
  local max_retry="${1:-40}"
  local interval_sec="${2:-2}"
  local attempt=1

  while [ "$attempt" -le "$max_retry" ]; do
    if curl -fsS "${GATEWAY_URL}/healthz" >/dev/null 2>&1; then
      return 0
    fi
    echo "  网关未就绪，等待重试 (${attempt}/${max_retry})..."
    sleep "$interval_sec"
    attempt=$((attempt + 1))
  done

  echo "  网关超时未就绪: ${GATEWAY_URL}/healthz"
  return 1
}

wait_gateway_ready 40 2

echo "[1/9] 健康检查: ${GATEWAY_URL}/healthz"
health_resp="$(curl -fsS "${GATEWAY_URL}/healthz")"
echo "  ${health_resp}"

echo "[2/9] 使用环境变量超级管理员登录"
super_admin_login_payload="$(printf '{"email":"%s","password":"%s"}' "$SUPER_ADMIN_EMAIL" "$SUPER_ADMIN_PASSWORD")"
super_admin_login_resp="$(http_json POST "${GATEWAY_URL}/api/auth/v1/auth/login" "$super_admin_login_payload")"
super_admin_token="$(extract_json_string "$super_admin_login_resp" "token")"
if [ -z "$super_admin_token" ]; then
  echo "  超级管理员登录失败: ${super_admin_login_resp}"
  exit 1
fi
echo "  超级管理员登录成功: ${SUPER_ADMIN_EMAIL}"

echo "[3/9] 创建站点"
site_payload="$(printf '{"site_id":"%s","name":"%s","domain":"%s"}' "$SITE_ID" "$SITE_NAME" "$SITE_DOMAIN")"
site_resp="$(http_json POST "${GATEWAY_URL}/api/admin/v1/admin/sites" "$site_payload" "$super_admin_token")"
site_id="$(extract_json_string "$site_resp" "site_id")"
if [ -z "$site_id" ]; then
  echo "  创建站点失败: ${site_resp}"
  exit 1
fi
echo "  site_id=${site_id}"

echo "[4/9] 创建坐席账号"
agent_payload="$(printf '{"agent_id":"%s","email":"%s","password":"%s","display_name":"%s","role":"agent","site_id":"%s"}' "$AGENT_ID" "$AGENT_EMAIL" "$AGENT_PASSWORD" "$AGENT_DISPLAY_NAME" "$site_id")"
agent_resp="$(http_json POST "${GATEWAY_URL}/api/admin/v1/admin/agents" "$agent_payload" "$super_admin_token")"
agent_id="$(extract_json_number "$agent_resp" "id")"
if [ -z "$agent_id" ] || [ "$agent_id" = "0" ]; then
  echo "  创建坐席失败: ${agent_resp}"
  exit 1
fi
echo "  agent_id=${agent_id} (${AGENT_ID}), email=${AGENT_EMAIL}"

echo "[5/9] 坐席登录"
login_payload="$(printf '{"email":"%s","password":"%s"}' "$AGENT_EMAIL" "$AGENT_PASSWORD")"
login_resp="$(http_json POST "${GATEWAY_URL}/api/auth/v1/auth/login" "$login_payload")"
agent_token="$(extract_json_string "$login_resp" "token")"
if [ -z "$agent_token" ]; then
  echo "  登录失败: ${login_resp}"
  exit 1
fi
echo "  登录成功"

echo "[6/9] 匿名访客创建会话"
widget_session="$(fetch_widget_session "$site_id" "$SITE_DOMAIN")"
if [ -z "$widget_session" ]; then
  echo "  获取 widget session 失败"
  exit 1
fi
conversation_payload="$(printf '{"site_id":"%s","visitor_token":"%s"}' "$site_id" "$VISITOR_TOKEN")"
conversation_resp="$(curl -fsS \
  -X POST "${GATEWAY_URL}/api/chat/v1/conversations" \
  -H "Content-Type: application/json" \
  -H "X-InlineChat-Widget-Session: ${widget_session}" \
  -d "$conversation_payload")"
conversation_id="$(extract_json_number "$conversation_resp" "id")"
if [ -z "$conversation_id" ] || [ "$conversation_id" = "0" ]; then
  echo "  创建会话失败: ${conversation_resp}"
  exit 1
fi
echo "  conversation_id=${conversation_id}"

echo "[7/9] 访客发消息并拉取消息"
message_payload="$(printf '{"sender_type":"visitor","content":"%s","client_msg_id":"%s","visitor_token":"%s"}' "hello from smoke" "smoke_${RUN_ID}" "$VISITOR_TOKEN")"
message_resp="$(http_json POST "${GATEWAY_URL}/api/chat/v1/conversations/${conversation_id}/messages" "$message_payload")"
message_id="$(extract_json_number "$message_resp" "id")"
if [ -z "$message_id" ] || [ "$message_id" = "0" ]; then
  echo "  发消息失败: ${message_resp}"
  exit 1
fi
list_message_resp="$(http_json GET "${GATEWAY_URL}/api/chat/v1/conversations/${conversation_id}/messages?limit=20&visitor_token=${VISITOR_TOKEN}")"
echo "  message_id=${message_id}"

echo "[8/9] 坐席认领并拉取会话列表"
claim_resp="$(http_json POST "${GATEWAY_URL}/api/chat/v1/conversations/${conversation_id}/claim" "{}" "$agent_token")"
claimed_agent_id="$(extract_json_number "$claim_resp" "assigned_agent_id")"
if [ -z "$claimed_agent_id" ] || [ "$claimed_agent_id" = "0" ]; then
  echo "  认领失败: ${claim_resp}"
  exit 1
fi
list_conv_resp="$(http_json GET "${GATEWAY_URL}/api/chat/v1/conversations?limit=20" "" "$agent_token")"
echo "  assigned_agent_id=${claimed_agent_id}"

echo "[9/9] 坐席关闭会话"
close_resp="$(http_json POST "${GATEWAY_URL}/api/chat/v1/conversations/${conversation_id}/close" "{}" "$agent_token")"
final_status="$(extract_json_string "$close_resp" "status")"
if [ "$final_status" != "closed" ]; then
  echo "  关闭失败: ${close_resp}"
  exit 1
fi
echo "  会话已关闭"

echo
echo "冒烟通过。"
echo "site_id=${site_id}"
echo "site_domain=${SITE_DOMAIN}"
echo "agent_id=${agent_id}"
echo "conversation_id=${conversation_id}"
echo "message_id=${message_id}"
echo "messages_snapshot=${list_message_resp}"
echo "conversations_snapshot=${list_conv_resp}"
