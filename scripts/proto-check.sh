#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

tracked_paths=(
  "packages/shared-types/proto"
  "services/auth-service/internal/gen"
  "services/chat-service/internal/gen"
  "services/realtime-service/internal/gen"
  "services/gateway-service/internal/gen"
  "services/admin-service/internal/gen"
  "services/ai-service/internal/gen"
)

cd "$ROOT_DIR"

./scripts/gen-proto.sh

if git diff --quiet -- "${tracked_paths[@]}" && [ -z "$(git status --short -- "${tracked_paths[@]}")" ]; then
  echo "proto 生成结果一致"
  exit 0
fi

echo "检测到 proto 生成结果未提交，请先执行: make proto"
git status --short -- "${tracked_paths[@]}" || true
git diff -- "${tracked_paths[@]}" || true
exit 1
