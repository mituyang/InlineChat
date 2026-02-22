#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"

if [ ! -f "$ENV_FILE" ]; then
  echo "缺少环境文件: $ENV_FILE"
  echo "请先执行: cp .env.example .env"
  exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "缺少依赖: jq"
  echo "请先安装 jq 后再执行全功能回归测试"
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
GATEWAY_HTTP_PORT="${GATEWAY_HTTP_PORT:-$(read_env GATEWAY_HTTP_PORT)}"
GATEWAY_HTTP_PORT="${GATEWAY_HTTP_PORT:-8200}"
GATEWAY_URL="${FULL_REGRESSION_GATEWAY_URL:-http://127.0.0.1:${GATEWAY_HTTP_PORT}}"
AUTO_CLOSE_AFTER_SEC="${AUTO_CLOSE_AFTER_SEC:-$(read_env AUTO_CLOSE_AFTER_SEC)}"
AUTO_CLOSE_AFTER_SEC="${AUTO_CLOSE_AFTER_SEC:-300}"
FULL_REGRESSION_AUTO_CLOSE_BUFFER_SEC="${FULL_REGRESSION_AUTO_CLOSE_BUFFER_SEC:-20}"

require_env SUPER_ADMIN_EMAIL
require_env SUPER_ADMIN_PASSWORD

RUN_ID="$(date +%s)"
SITE_ID="site_full_${RUN_ID}"
SITE_NAME="Full Regression ${RUN_ID}"
SITE_DOMAIN="full-${RUN_ID}.local"
VISITOR_TOKEN_A="vt_full_${RUN_ID}_a"
VISITOR_TOKEN_B="vt_full_${RUN_ID}_b"
VISITOR_TOKEN_C="vt_full_${RUN_ID}_c"

AGENT_A_ID="$(printf "%04d" $(( ((RUN_ID + $$ + RANDOM) % 9000) + 1000 )))"
AGENT_B_ID="$(printf "%04d" $(( ((RUN_ID + $$ + RANDOM + 173) % 9000) + 1000 )))"
if [ "$AGENT_B_ID" = "$AGENT_A_ID" ]; then
  AGENT_B_ID="$(printf "%04d" $(( ((RUN_ID + $$ + RANDOM + 811) % 9000) + 1000 )))"
fi
AGENT_A_EMAIL="full_agent_a_${RUN_ID}@example.com"
AGENT_B_EMAIL="full_agent_b_${RUN_ID}@example.com"
AGENT_A_PASSWORD_OLD="Agent#FullOld2026!"
AGENT_A_PASSWORD_NEW="Agent#FullNew2026!"
AGENT_B_PASSWORD="Agent#FullB2026!"
AGENT_A_DISPLAY_NAME="Full Agent A ${AGENT_A_ID}"
AGENT_B_DISPLAY_NAME="Full Agent B ${AGENT_B_ID}"

HTTP_STATUS=""
HTTP_BODY=""
STEP_NO=0

step() {
  STEP_NO=$((STEP_NO + 1))
  echo
  echo "[${STEP_NO}] $1"
}

fail() {
  echo "FAIL: $1"
  if [ -n "${HTTP_STATUS:-}" ]; then
    echo "HTTP_STATUS=${HTTP_STATUS}"
  fi
  if [ -n "${HTTP_BODY:-}" ]; then
    echo "HTTP_BODY=${HTTP_BODY}"
  fi
  exit 1
}

assert_status() {
  local expected="$1"
  local context="$2"
  if [ "$HTTP_STATUS" != "$expected" ]; then
    fail "${context} 状态码不符合预期，expected=${expected}"
  fi
}

json_string() {
  local expr="$1"
  printf "%s" "$HTTP_BODY" | jq -r "$expr // empty"
}

json_number() {
  local expr="$1"
  printf "%s" "$HTTP_BODY" | jq -r "$expr // 0"
}

http_call() {
  local method="$1"
  local url="$2"
  local body="${3:-}"
  local token="${4:-}"
  local tmp
  tmp="$(mktemp)"

  local args=(
    -sS
    -o "$tmp"
    -w "%{http_code}"
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

  if ! HTTP_STATUS="$(curl "${args[@]}")"; then
    rm -f "$tmp"
    fail "请求失败: ${method} ${url}"
  fi
  HTTP_BODY="$(cat "$tmp")"
  rm -f "$tmp"
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
  return 1
}

step "等待网关就绪"
if ! wait_gateway_ready 40 2; then
  fail "网关未就绪: ${GATEWAY_URL}/healthz"
fi
echo "  网关已就绪: ${GATEWAY_URL}"

step "超级管理员登录 + me 校验"
login_super_payload="$(printf '{"email":"%s","password":"%s"}' "$SUPER_ADMIN_EMAIL" "$SUPER_ADMIN_PASSWORD")"
http_call POST "${GATEWAY_URL}/api/auth/v1/auth/login" "$login_super_payload"
assert_status "200" "超级管理员登录"
SUPER_TOKEN="$(json_string '.token')"
if [ -z "$SUPER_TOKEN" ]; then
  fail "超级管理员登录返回缺少 token"
fi
http_call GET "${GATEWAY_URL}/api/auth/v1/auth/me" "" "$SUPER_TOKEN"
assert_status "200" "超级管理员 me"
SUPER_ROLE="$(json_string '.role')"
if [ "$SUPER_ROLE" != "super_admin" ]; then
  fail "超级管理员角色不正确: ${SUPER_ROLE}"
fi
echo "  登录成功: ${SUPER_ADMIN_EMAIL} role=${SUPER_ROLE}"

step "创建站点 + 列表查询 + 轮换密钥 + 禁用/启用"
create_site_payload="$(printf '{"site_id":"%s","name":"%s","domain":"%s"}' "$SITE_ID" "$SITE_NAME" "$SITE_DOMAIN")"
http_call POST "${GATEWAY_URL}/api/admin/v1/admin/sites" "$create_site_payload" "$SUPER_TOKEN"
assert_status "201" "创建站点"
SITE_WIDGET_KEY_1="$(json_string '.widget_key')"
if [ -z "$SITE_WIDGET_KEY_1" ]; then
  fail "创建站点返回缺少 widget_key"
fi

http_call GET "${GATEWAY_URL}/api/admin/v1/admin/sites?limit=200" "" "$SUPER_TOKEN"
assert_status "200" "查询站点列表"
if ! printf "%s" "$HTTP_BODY" | jq -e --arg sid "$SITE_ID" '.items | any(.site_id == $sid)' >/dev/null; then
  fail "站点列表未找到新建站点: ${SITE_ID}"
fi

http_call POST "${GATEWAY_URL}/api/admin/v1/admin/sites/${SITE_ID}/rotate-widget-key" "" "$SUPER_TOKEN"
assert_status "200" "轮换站点密钥"
SITE_WIDGET_KEY_2="$(json_string '.widget_key')"
if [ -z "$SITE_WIDGET_KEY_2" ] || [ "$SITE_WIDGET_KEY_2" = "$SITE_WIDGET_KEY_1" ]; then
  fail "站点密钥轮换未生效"
fi

http_call PATCH "${GATEWAY_URL}/api/admin/v1/admin/sites/${SITE_ID}/status" '{"status":"disabled"}' "$SUPER_TOKEN"
assert_status "200" "禁用站点"

create_conv_disabled_payload="$(printf '{"site_id":"%s","visitor_token":"%s"}' "$SITE_ID" "$VISITOR_TOKEN_A")"
http_call POST "${GATEWAY_URL}/api/chat/v1/conversations" "$create_conv_disabled_payload"
assert_status "409" "禁用站点创建会话"

http_call PATCH "${GATEWAY_URL}/api/admin/v1/admin/sites/${SITE_ID}/status" '{"status":"active"}' "$SUPER_TOKEN"
assert_status "200" "启用站点"
echo "  站点流程验证通过: ${SITE_ID}"

step "创建双坐席 + 唯一性约束 + 列表查询"
create_agent_a_payload="$(printf '{"agent_id":"%s","email":"%s","password":"%s","display_name":"%s","role":"agent"}' "$AGENT_A_ID" "$AGENT_A_EMAIL" "$AGENT_A_PASSWORD_OLD" "$AGENT_A_DISPLAY_NAME")"
http_call POST "${GATEWAY_URL}/api/admin/v1/admin/agents" "$create_agent_a_payload" "$SUPER_TOKEN"
assert_status "201" "创建坐席A"
AGENT_A_NUMERIC_ID="$(json_number '.id')"
if [ "$AGENT_A_NUMERIC_ID" = "0" ]; then
  fail "创建坐席A未返回有效 id"
fi

create_agent_b_payload="$(printf '{"agent_id":"%s","email":"%s","password":"%s","display_name":"%s","role":"agent"}' "$AGENT_B_ID" "$AGENT_B_EMAIL" "$AGENT_B_PASSWORD" "$AGENT_B_DISPLAY_NAME")"
http_call POST "${GATEWAY_URL}/api/admin/v1/admin/agents" "$create_agent_b_payload" "$SUPER_TOKEN"
assert_status "201" "创建坐席B"
AGENT_B_NUMERIC_ID="$(json_number '.id')"
if [ "$AGENT_B_NUMERIC_ID" = "0" ]; then
  fail "创建坐席B未返回有效 id"
fi

dup_email_payload="$(printf '{"agent_id":"%s","email":"%s","password":"%s","display_name":"%s","role":"agent"}' "9001" "$AGENT_A_EMAIL" "$AGENT_B_PASSWORD" "Dup Email ${RUN_ID}")"
http_call POST "${GATEWAY_URL}/api/admin/v1/admin/agents" "$dup_email_payload" "$SUPER_TOKEN"
assert_status "409" "重复邮箱约束"

dup_name_payload="$(printf '{"agent_id":"%s","email":"%s","password":"%s","display_name":"%s","role":"agent"}' "9002" "dup_name_${RUN_ID}@example.com" "$AGENT_B_PASSWORD" "$AGENT_A_DISPLAY_NAME")"
http_call POST "${GATEWAY_URL}/api/admin/v1/admin/agents" "$dup_name_payload" "$SUPER_TOKEN"
assert_status "409" "重复显示名约束"

http_call GET "${GATEWAY_URL}/api/admin/v1/admin/agents?limit=200" "" "$SUPER_TOKEN"
assert_status "200" "查询坐席列表"
if ! printf "%s" "$HTTP_BODY" | jq -e --argjson aid "$AGENT_A_NUMERIC_ID" '.items | any(.id == $aid)' >/dev/null; then
  fail "坐席列表未找到坐席A"
fi
if ! printf "%s" "$HTTP_BODY" | jq -e --argjson bid "$AGENT_B_NUMERIC_ID" '.items | any(.id == $bid)' >/dev/null; then
  fail "坐席列表未找到坐席B"
fi
echo "  坐席创建与唯一约束验证通过: A=${AGENT_A_ID} B=${AGENT_B_ID}"

step "坐席认证能力：重置密码/强制下线/状态切换"
http_call POST "${GATEWAY_URL}/api/auth/v1/auth/login" "$(printf '{"email":"%s","password":"%s"}' "$AGENT_A_EMAIL" "$AGENT_A_PASSWORD_OLD")"
assert_status "200" "坐席A旧密码登录"
AGENT_A_TOKEN_OLD="$(json_string '.token')"

http_call POST "${GATEWAY_URL}/api/admin/v1/admin/agents/${AGENT_A_NUMERIC_ID}/reset-password" "$(printf '{"new_password":"%s"}' "$AGENT_A_PASSWORD_NEW")" "$SUPER_TOKEN"
assert_status "200" "重置坐席A密码"

http_call POST "${GATEWAY_URL}/api/auth/v1/auth/login" "$(printf '{"email":"%s","password":"%s"}' "$AGENT_A_EMAIL" "$AGENT_A_PASSWORD_OLD")"
assert_status "401" "坐席A旧密码失效"

http_call POST "${GATEWAY_URL}/api/auth/v1/auth/login" "$(printf '{"email":"%s","password":"%s"}' "$AGENT_A_EMAIL" "$AGENT_A_PASSWORD_NEW")"
assert_status "200" "坐席A新密码登录"
AGENT_A_TOKEN="$(json_string '.token')"

http_call GET "${GATEWAY_URL}/api/auth/v1/auth/me" "" "$AGENT_A_TOKEN"
assert_status "200" "坐席A me (force logout 前)"

http_call POST "${GATEWAY_URL}/api/admin/v1/admin/agents/${AGENT_A_NUMERIC_ID}/force-logout" "" "$SUPER_TOKEN"
assert_status "200" "强制下线坐席A"

http_call GET "${GATEWAY_URL}/api/auth/v1/auth/me" "" "$AGENT_A_TOKEN"
assert_status "401" "坐席A token 强制下线后失效"

http_call POST "${GATEWAY_URL}/api/auth/v1/auth/login" "$(printf '{"email":"%s","password":"%s"}' "$AGENT_A_EMAIL" "$AGENT_A_PASSWORD_NEW")"
assert_status "200" "坐席A再次登录"
AGENT_A_TOKEN="$(json_string '.token')"

http_call PATCH "${GATEWAY_URL}/api/admin/v1/admin/agents/${AGENT_B_NUMERIC_ID}/status" '{"status":"inactive"}' "$SUPER_TOKEN"
assert_status "200" "坐席B设为 inactive"
http_call POST "${GATEWAY_URL}/api/auth/v1/auth/login" "$(printf '{"email":"%s","password":"%s"}' "$AGENT_B_EMAIL" "$AGENT_B_PASSWORD")"
if [ "$HTTP_STATUS" != "401" ] && [ "$HTTP_STATUS" != "403" ]; then
  fail "inactive 坐席登录受限 状态码不符合预期，expected=401|403"
fi

http_call PATCH "${GATEWAY_URL}/api/admin/v1/admin/agents/${AGENT_B_NUMERIC_ID}/status" '{"status":"active"}' "$SUPER_TOKEN"
assert_status "200" "坐席B设为 active"
http_call POST "${GATEWAY_URL}/api/auth/v1/auth/login" "$(printf '{"email":"%s","password":"%s"}' "$AGENT_B_EMAIL" "$AGENT_B_PASSWORD")"
assert_status "200" "坐席B登录"
AGENT_B_TOKEN="$(json_string '.token')"
echo "  坐席认证能力验证通过"

step "会话主链路：访客/认领/已读/转接确认/关闭"
http_call POST "${GATEWAY_URL}/api/chat/v1/conversations" "$(printf '{"site_id":"%s","visitor_token":"%s"}' "$SITE_ID" "$VISITOR_TOKEN_A")"
assert_status "201" "创建会话A"
CONV_A_ID="$(json_number '.id')"
if [ "$CONV_A_ID" = "0" ]; then
  fail "会话A创建失败"
fi

http_call GET "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}?visitor_token=wrong_token"
assert_status "403" "会话A visitor_token 防越权(get)"
http_call GET "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/messages?limit=20&visitor_token=wrong_token"
assert_status "403" "会话A visitor_token 防越权(list)"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/messages" "$(printf '{"sender_type":"visitor","content":"%s","client_msg_id":"%s","visitor_token":"%s"}' "visitor message A1" "full_a1_${RUN_ID}" "$VISITOR_TOKEN_A")"
assert_status "201" "会话A访客发首条消息"
MSG_A1_ID="$(json_number '.id')"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/messages" "$(printf '{"sender_type":"agent","content":"%s","client_msg_id":"%s"}' "agent try before claim" "full_agent_preclaim_${RUN_ID}")" "$AGENT_A_TOKEN"
assert_status "409" "会话A未认领禁止客服发消息"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/read" '{"last_read_message_id":1}' "$AGENT_A_TOKEN"
assert_status "403" "会话A未认领禁止客服已读"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/claim" "{}" "$AGENT_A_TOKEN"
assert_status "200" "会话A认领"
if [ "$(json_number '.assigned_agent_id')" != "$AGENT_A_NUMERIC_ID" ]; then
  fail "会话A认领后 assigned_agent_id 不正确"
fi

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/messages" "$(printf '{"sender_type":"agent","content":"%s","client_msg_id":"%s"}' "agent message A2" "full_agent_a2_${RUN_ID}")" "$AGENT_A_TOKEN"
assert_status "201" "会话A认领后客服发消息"
MSG_A2_ID="$(json_number '.id')"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/read" "$(printf '{"last_read_message_id":%s,"visitor_token":"%s"}' "$MSG_A2_ID" "$VISITOR_TOKEN_A")"
assert_status "200" "会话A访客已读"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/transfer" "$(printf '{"to_agent_id":%s}' "$AGENT_B_NUMERIC_ID")" "$AGENT_A_TOKEN"
assert_status "200" "会话A发起转接到B"
if [ "$(json_number '.pending_transfer_to_agent_id')" != "$AGENT_B_NUMERIC_ID" ]; then
  fail "会话A转接后 pending_transfer_to_agent_id 不正确"
fi

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/messages" "$(printf '{"sender_type":"agent","content":"%s","client_msg_id":"%s"}' "agent B before confirm" "full_agent_b_preconfirm_${RUN_ID}")" "$AGENT_B_TOKEN"
assert_status "403" "会话A转接待确认期间新客服禁止发消息"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/transfer/confirm" "{}" "$AGENT_B_TOKEN"
assert_status "200" "会话A转接确认"
if [ "$(json_number '.assigned_agent_id')" != "$AGENT_B_NUMERIC_ID" ]; then
  fail "会话A确认转接后 assigned_agent_id 不正确"
fi
if [ "$(json_number '.pending_transfer_to_agent_id')" != "0" ]; then
  fail "会话A确认转接后 pending_transfer_to_agent_id 未清空"
fi

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/messages" "$(printf '{"sender_type":"agent","content":"%s","client_msg_id":"%s"}' "agent A after transferred" "full_agent_a_after_transfer_${RUN_ID}")" "$AGENT_A_TOKEN"
assert_status "403" "会话A转接后原客服禁止发消息"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/messages" "$(printf '{"sender_type":"agent","content":"%s","client_msg_id":"%s"}' "agent B after confirm" "full_agent_b_after_confirm_${RUN_ID}")" "$AGENT_B_TOKEN"
assert_status "201" "会话A确认后新客服可发消息"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/close" "{}" "$AGENT_B_TOKEN"
assert_status "200" "会话A关闭"
if [ "$(json_string '.status')" != "closed" ]; then
  fail "会话A关闭后状态不为 closed"
fi

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_A_ID}/messages" "$(printf '{"sender_type":"visitor","content":"%s","client_msg_id":"%s","visitor_token":"%s"}' "visitor after closed" "full_visitor_after_close_${RUN_ID}" "$VISITOR_TOKEN_A")"
assert_status "409" "会话A关闭后访客发消息应失败"
echo "  会话主链路验证通过: conv=${CONV_A_ID} msg1=${MSG_A1_ID}"

step "转接拒绝链路：发起/拒绝后原客服继续接待"
http_call POST "${GATEWAY_URL}/api/chat/v1/conversations" "$(printf '{"site_id":"%s","visitor_token":"%s"}' "$SITE_ID" "$VISITOR_TOKEN_B")"
assert_status "201" "创建会话B"
CONV_B_ID="$(json_number '.id')"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_B_ID}/messages" "$(printf '{"sender_type":"visitor","content":"%s","client_msg_id":"%s","visitor_token":"%s"}' "visitor message B1" "full_b1_${RUN_ID}" "$VISITOR_TOKEN_B")"
assert_status "201" "会话B访客发消息"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_B_ID}/claim" "{}" "$AGENT_A_TOKEN"
assert_status "200" "会话B认领"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_B_ID}/transfer" "$(printf '{"to_agent_id":%s}' "$AGENT_B_NUMERIC_ID")" "$AGENT_A_TOKEN"
assert_status "200" "会话B发起转接"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_B_ID}/transfer/reject" "{}" "$AGENT_B_TOKEN"
assert_status "200" "会话B拒绝转接"
if [ "$(json_number '.assigned_agent_id')" != "$AGENT_A_NUMERIC_ID" ]; then
  fail "会话B拒绝转接后 assigned_agent_id 不正确"
fi
if [ "$(json_number '.pending_transfer_to_agent_id')" != "0" ]; then
  fail "会话B拒绝转接后 pending_transfer_to_agent_id 未清空"
fi

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_B_ID}/messages" "$(printf '{"sender_type":"agent","content":"%s","client_msg_id":"%s"}' "agent A after reject" "full_agent_a_after_reject_${RUN_ID}")" "$AGENT_A_TOKEN"
assert_status "201" "会话B拒绝转接后原客服仍可发送"
echo "  转接拒绝链路验证通过: conv=${CONV_B_ID}"

step "自动关闭链路：客服发消息后访客超时未回复"
http_call POST "${GATEWAY_URL}/api/chat/v1/conversations" "$(printf '{"site_id":"%s","visitor_token":"%s"}' "$SITE_ID" "$VISITOR_TOKEN_C")"
assert_status "201" "创建会话C"
CONV_C_ID="$(json_number '.id')"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_C_ID}/messages" "$(printf '{"sender_type":"visitor","content":"%s","client_msg_id":"%s","visitor_token":"%s"}' "visitor message C1" "full_c1_${RUN_ID}" "$VISITOR_TOKEN_C")"
assert_status "201" "会话C访客发消息"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_C_ID}/claim" "{}" "$AGENT_A_TOKEN"
assert_status "200" "会话C认领"

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_C_ID}/messages" "$(printf '{"sender_type":"agent","content":"%s","client_msg_id":"%s"}' "agent message C2 for auto close" "full_agent_c2_${RUN_ID}")" "$AGENT_A_TOKEN"
assert_status "201" "会话C客服发消息触发自动关闭计时"

AUTO_CLOSE_TIMEOUT_SEC=$((AUTO_CLOSE_AFTER_SEC + FULL_REGRESSION_AUTO_CLOSE_BUFFER_SEC))
echo "  等待自动关闭: conversation=${CONV_C_ID} timeout=${AUTO_CLOSE_TIMEOUT_SEC}s"
closed="0"
for ((i=0; i<AUTO_CLOSE_TIMEOUT_SEC; i+=2)); do
  sleep 2
  http_call GET "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_C_ID}?visitor_token=${VISITOR_TOKEN_C}"
  if [ "$HTTP_STATUS" != "200" ]; then
    continue
  fi
  status_text="$(json_string '.status')"
  if [ "$status_text" = "closed" ]; then
    closed="1"
    break
  fi
done
if [ "$closed" != "1" ]; then
  fail "会话C未在预期时间内自动关闭"
fi

http_call POST "${GATEWAY_URL}/api/chat/v1/conversations/${CONV_C_ID}/messages" "$(printf '{"sender_type":"visitor","content":"%s","client_msg_id":"%s","visitor_token":"%s"}' "visitor after auto close" "full_visitor_after_autoclose_${RUN_ID}" "$VISITOR_TOKEN_C")"
assert_status "409" "会话C自动关闭后访客发送受限"
echo "  自动关闭链路验证通过: conv=${CONV_C_ID}"

step "会话列表与审计日志查询"
http_call GET "${GATEWAY_URL}/api/chat/v1/conversations?limit=200&site_id=${SITE_ID}" "" "$AGENT_A_TOKEN"
assert_status "200" "按站点过滤会话列表"
if ! printf "%s" "$HTTP_BODY" | jq -e --argjson cid "$CONV_B_ID" '.items | any(.id == $cid)' >/dev/null; then
  fail "会话列表缺少会话B"
fi

http_call GET "${GATEWAY_URL}/api/admin/v1/admin/audit-logs?limit=200" "" "$SUPER_TOKEN"
assert_status "200" "查询审计日志"
if ! printf "%s" "$HTTP_BODY" | jq -e --arg sid "$SITE_ID" '.items | any(.action == "site.create" and .resource_id == $sid)' >/dev/null; then
  fail "审计日志缺少当前回归创建站点记录"
fi
echo "  会话列表与审计日志验证通过"

echo
echo "full_regression_ok gateway=${GATEWAY_URL}"
echo "site_id=${SITE_ID}"
echo "agent_a_id=${AGENT_A_ID} numeric=${AGENT_A_NUMERIC_ID}"
echo "agent_b_id=${AGENT_B_ID} numeric=${AGENT_B_NUMERIC_ID}"
echo "conversation_a=${CONV_A_ID}"
echo "conversation_b=${CONV_B_ID}"
echo "conversation_c=${CONV_C_ID}"
