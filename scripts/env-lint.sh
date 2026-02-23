#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"
EXAMPLE_ENV_FILE="${EXAMPLE_ENV_FILE:-$ROOT_DIR/.env.example}"

if [ ! -f "$EXAMPLE_ENV_FILE" ]; then
  echo "缺少环境模板文件: $EXAMPLE_ENV_FILE"
  exit 1
fi

if [ ! -f "$ENV_FILE" ]; then
  echo "缺少环境文件: $ENV_FILE"
  echo "请先执行: cp .env.example .env"
  exit 1
fi

extract_keys() {
  local file="$1"
  awk '
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
      key = parts[1]
      gsub(/[[:space:]]/, "", key)
      if (key ~ /^[A-Za-z_][A-Za-z0-9_]*$/) {
        print key
      }
    }
  ' "$file" | LC_ALL=C sort -u
}

extract_duplicate_keys() {
  local file="$1"
  awk '
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
      key = parts[1]
      gsub(/[[:space:]]/, "", key)
      if (key ~ /^[A-Za-z_][A-Za-z0-9_]*$/) {
        count[key]++
      }
    }
    END {
      for (k in count) {
        if (count[k] > 1) {
          print k
        }
      }
    }
  ' "$file" | LC_ALL=C sort
}

env_keys_file="$(mktemp)"
example_keys_file="$(mktemp)"
trap 'rm -f "$env_keys_file" "$example_keys_file"' EXIT

extract_keys "$ENV_FILE" >"$env_keys_file"
extract_keys "$EXAMPLE_ENV_FILE" >"$example_keys_file"

env_dup_keys="$(extract_duplicate_keys "$ENV_FILE")"
if [ -n "$env_dup_keys" ]; then
  echo ".env 中存在重复键："
  echo "$env_dup_keys"
  exit 1
fi

example_dup_keys="$(extract_duplicate_keys "$EXAMPLE_ENV_FILE")"
if [ -n "$example_dup_keys" ]; then
  echo ".env.example 中存在重复键："
  echo "$example_dup_keys"
  exit 1
fi

missing_in_env="$(comm -23 "$example_keys_file" "$env_keys_file")"
extra_in_env="$(comm -13 "$example_keys_file" "$env_keys_file")"

if [ -n "$missing_in_env" ]; then
  echo ".env 缺少以下键（相较 .env.example）："
  echo "$missing_in_env"
  exit 1
fi

if [ -n "$extra_in_env" ]; then
  echo ".env 多出以下键（相较 .env.example）："
  echo "$extra_in_env"
  exit 1
fi

key_count="$(wc -l < "$env_keys_file" | tr -d ' ')"
echo "env_lint_ok env=$ENV_FILE example=$EXAMPLE_ENV_FILE key_count=$key_count"
