#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

sync_dir() {
	local src="$1"
	local dst="$2"
	if [ ! -d "$src" ]; then
		echo "前端目录不存在: $src"
		exit 1
	fi

	rm -rf "$dst"
	mkdir -p "$dst"
	cp -R "$src"/. "$dst"/
	rm -f "$dst/.gitkeep"
}

sync_file() {
	local src="$1"
	local dst="$2"
	local parent

	if [ ! -f "$src" ]; then
		echo "文件不存在: $src"
		exit 1
	fi

	parent="$(dirname "$dst")"
	mkdir -p "$parent"
	cp "$src" "$dst"
}

sync_dir "$ROOT_DIR/apps/agent-console" "$ROOT_DIR/services/gateway-service/public/agent"
sync_dir "$ROOT_DIR/apps/admin-console" "$ROOT_DIR/services/gateway-service/public/admin"
sync_dir "$ROOT_DIR/apps/staff-login" "$ROOT_DIR/services/gateway-service/public/staff-login"
sync_dir "$ROOT_DIR/apps/customer-console" "$ROOT_DIR/services/gateway-service/public/customer"
sync_dir "$ROOT_DIR/apps/widget-chat" "$ROOT_DIR/services/gateway-service/public/widget"
sync_dir "$ROOT_DIR/apps/demo-site" "$ROOT_DIR/services/gateway-service/public/demo"
sync_file "$ROOT_DIR/apps/widget-sdk/inlinechat-widget.js" "$ROOT_DIR/services/gateway-service/public/sdk/inlinechat-widget.js"

echo "前端静态资源同步完成"
