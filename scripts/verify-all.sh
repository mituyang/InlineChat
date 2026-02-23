#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"

VERIFY_AUTO_UPDOWN="${VERIFY_AUTO_UPDOWN:-1}"
VERIFY_KEEP_ENV_ON_FAIL="${VERIFY_KEEP_ENV_ON_FAIL:-0}"
VERIFY_KEEP_ENV="${VERIFY_KEEP_ENV:-0}"

if [ ! -f "$ENV_FILE" ]; then
  echo "缺少环境文件: $ENV_FILE"
  echo "请先执行: cp .env.example .env"
  exit 1
fi

cleanup_env() {
  local exit_code="$1"

  if [ "$VERIFY_AUTO_UPDOWN" != "1" ]; then
    return
  fi

  if [ "$VERIFY_KEEP_ENV" = "1" ]; then
    echo "[verify-all] VERIFY_KEEP_ENV=1，保留当前环境"
    return
  fi

  if [ "$exit_code" != "0" ] && [ "$VERIFY_KEEP_ENV_ON_FAIL" = "1" ]; then
    echo "[verify-all] 任务失败且 VERIFY_KEEP_ENV_ON_FAIL=1，保留现场环境"
    return
  fi

  echo "[verify-all] 清理环境: make down"
  (
    cd "$ROOT_DIR"
    ENV_FILE="$ENV_FILE" make down
  ) || true
}

on_exit() {
  local exit_code="$?"
  trap - EXIT
  cleanup_env "$exit_code"
  exit "$exit_code"
}
trap on_exit EXIT

run_step() {
  local name="$1"
  shift

  echo
  echo "[verify-all] ==> ${name}"
  (
    cd "$ROOT_DIR"
    ENV_FILE="$ENV_FILE" "$@"
  )
}

if [ "$VERIFY_AUTO_UPDOWN" = "1" ]; then
  echo "[verify-all] 启动前清理旧环境"
  (
    cd "$ROOT_DIR"
    ENV_FILE="$ENV_FILE" make down
  ) || true

  echo "[verify-all] 启动环境: make up"
  (
    cd "$ROOT_DIR"
    ENV_FILE="$ENV_FILE" make up
  )
else
  echo "[verify-all] VERIFY_AUTO_UPDOWN!=1，跳过自动 up/down"
fi

run_step "quality" make quality
run_step "smoke" make smoke
run_step "integration" make integration
run_step "full-regression" make full-regression
run_step "e2e-ui" make e2e-ui

echo
echo "[verify-all] 全量验收通过"
