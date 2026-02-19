#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CACHE_DIR="${CACHE_DIR:-$ROOT_DIR/.cache}"
GO_BUILD_CACHE="${CACHE_DIR}/go-build"
GO_MOD_CACHE="${CACHE_DIR}/go-mod"
COVERAGE_THRESHOLD="${COVERAGE_THRESHOLD:-50}"
MIN_COVERED_PACKAGES="${MIN_COVERED_PACKAGES:-10}"

if [ "$#" -gt 0 ]; then
	SERVICES=("$@")
else
	SERVICES=(
		"services/chat-service"
		"services/realtime-service"
		"services/gateway-service"
		"services/auth-service"
		"services/admin-service"
	)
fi

mkdir -p "$GO_BUILD_CACHE" "$GO_MOD_CACHE"

sum="0"
count=0

for svc in "${SERVICES[@]}"; do
	svc_dir="$ROOT_DIR/$svc"
	if [ ! -d "$svc_dir" ]; then
		echo "服务目录不存在: $svc"
		exit 1
	fi

	echo "==> go test -cover $svc"
	output="$(
		cd "$svc_dir" && \
			GOCACHE="$GO_BUILD_CACHE" \
			GOMODCACHE="$GO_MOD_CACHE" \
			go test ./... -cover
	)"
	printf "%s\n" "$output"

	percentages="$(printf "%s\n" "$output" | sed -n 's/.*coverage: \([0-9.][0-9.]*\)%.*/\1/p')"
	if [ -z "$percentages" ]; then
		continue
	fi

	while IFS= read -r pct; do
		if awk -v x="$pct" 'BEGIN { exit !(x > 0) }'; then
			sum="$(awk -v a="$sum" -v b="$pct" 'BEGIN { printf "%.4f", a + b }')"
			count=$((count + 1))
		fi
	done <<<"$percentages"
done

if [ "$count" -lt "$MIN_COVERED_PACKAGES" ]; then
	echo "覆盖率门禁失败: 有覆盖率的包数量不足，当前=${count}，要求>=${MIN_COVERED_PACKAGES}"
	exit 1
fi

avg="$(awk -v s="$sum" -v c="$count" 'BEGIN { printf "%.2f", s / c }')"
echo "covered_packages=${count} average_coverage=${avg}% threshold=${COVERAGE_THRESHOLD}%"

if ! awk -v avg="$avg" -v threshold="$COVERAGE_THRESHOLD" 'BEGIN { exit !(avg >= threshold) }'; then
	echo "覆盖率门禁失败: average_coverage=${avg}% < threshold=${COVERAGE_THRESHOLD}%"
	exit 1
fi

echo "覆盖率门禁通过"
