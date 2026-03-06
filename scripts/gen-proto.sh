#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PROTO_DIR="$ROOT_DIR/packages/shared-types/proto"

CHAT_OUT="$ROOT_DIR/services/chat-service/internal/gen/chatv1"
REALTIME_OUT="$ROOT_DIR/services/realtime-service/internal/gen/chatv1"
GATEWAY_CHAT_OUT="$ROOT_DIR/services/gateway-service/internal/gen/chatv1"

AUTH_OUT="$ROOT_DIR/services/auth-service/internal/gen/authv1"
REALTIME_AUTH_OUT="$ROOT_DIR/services/realtime-service/internal/gen/authv1"
GATEWAY_AUTH_OUT="$ROOT_DIR/services/gateway-service/internal/gen/authv1"

ADMIN_OUT="$ROOT_DIR/services/admin-service/internal/gen/adminv1"
REALTIME_ADMIN_OUT="$ROOT_DIR/services/realtime-service/internal/gen/adminv1"
GATEWAY_ADMIN_OUT="$ROOT_DIR/services/gateway-service/internal/gen/adminv1"

if ! command -v protoc >/dev/null 2>&1; then
	echo "未找到 protoc，请先安装 Protocol Buffers 编译器。"
	exit 1
fi

if ! command -v protoc-gen-go >/dev/null 2>&1; then
	echo "未找到 protoc-gen-go，请先安装：go install google.golang.org/protobuf/cmd/protoc-gen-go@latest"
	exit 1
fi

if ! command -v protoc-gen-go-grpc >/dev/null 2>&1; then
	echo "未找到 protoc-gen-go-grpc，请先安装：go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest"
	exit 1
fi

mkdir -p "$CHAT_OUT" "$REALTIME_OUT"
mkdir -p "$GATEWAY_CHAT_OUT" "$AUTH_OUT" "$REALTIME_AUTH_OUT" "$GATEWAY_AUTH_OUT" "$ADMIN_OUT" "$REALTIME_ADMIN_OUT" "$GATEWAY_ADMIN_OUT"

generate_proto() {
	local proto_file="$1"
	local out_dir="$2"
	local go_package="$3"

	protoc \
		-I "$PROTO_DIR" \
		--go_out=paths=source_relative,M${proto_file}=${go_package}:"$out_dir" \
		--go-grpc_out=paths=source_relative,M${proto_file}=${go_package}:"$out_dir" \
		"$proto_file"

	local nested_dir="$out_dir/inlinechat"
	if [ -d "$nested_dir" ]; then
		find "$nested_dir" -maxdepth 1 -type f -name '*.go' -exec mv {} "$out_dir"/ \;
		rm -rf "$nested_dir"
	fi
}

echo "生成 chat-service gRPC 代码..."
generate_proto "inlinechat/chat.proto" "$CHAT_OUT" "inlinechat/services/chat-service/internal/gen/chatv1"

echo "生成 realtime-service gRPC 代码..."
generate_proto "inlinechat/chat.proto" "$REALTIME_OUT" "inlinechat/services/realtime-service/internal/gen/chatv1"

echo "生成 gateway-service chat gRPC 代码..."
generate_proto "inlinechat/chat.proto" "$GATEWAY_CHAT_OUT" "inlinechat/services/gateway-service/internal/gen/chatv1"

echo "生成 auth-service gRPC 代码..."
generate_proto "inlinechat/auth.proto" "$AUTH_OUT" "inlinechat/services/auth-service/internal/gen/authv1"

echo "生成 realtime-service auth gRPC 代码..."
generate_proto "inlinechat/auth.proto" "$REALTIME_AUTH_OUT" "inlinechat/services/realtime-service/internal/gen/authv1"

echo "生成 gateway-service auth gRPC 代码..."
generate_proto "inlinechat/auth.proto" "$GATEWAY_AUTH_OUT" "inlinechat/services/gateway-service/internal/gen/authv1"

echo "生成 admin-service gRPC 代码..."
generate_proto "inlinechat/admin.proto" "$ADMIN_OUT" "inlinechat/services/admin-service/internal/gen/adminv1"

echo "生成 realtime-service admin gRPC 代码..."
generate_proto "inlinechat/admin.proto" "$REALTIME_ADMIN_OUT" "inlinechat/services/realtime-service/internal/gen/adminv1"

echo "生成 gateway-service admin gRPC 代码..."
generate_proto "inlinechat/admin.proto" "$GATEWAY_ADMIN_OUT" "inlinechat/services/gateway-service/internal/gen/adminv1"

echo "完成。"
