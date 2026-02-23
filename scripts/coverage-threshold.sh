#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CACHE_DIR="${CACHE_DIR:-$ROOT_DIR/.cache}"
GO_BUILD_CACHE="${GO_BUILD_CACHE:-${CACHE_DIR}/go-build}"
GO_MOD_CACHE="${GO_MOD_CACHE:-${CACHE_DIR}/go-mod}"
GO_PROXY="${GO_PROXY:-https://proxy.golang.org,direct}"
COVERAGE_THRESHOLD="${COVERAGE_THRESHOLD:-50}"
COVERAGE_THRESHOLD_ALL="${COVERAGE_THRESHOLD_ALL:-12}"
MIN_COVERED_PACKAGES="${MIN_COVERED_PACKAGES:-10}"
MIN_TOTAL_PACKAGES="${MIN_TOTAL_PACKAGES:-20}"

if [ "$#" -gt 0 ]; then
	SERVICES=("$@")
else
		SERVICES=(
			"services/chat-service"
			"services/realtime-service"
			"services/gateway-service"
			"services/auth-service"
			"services/admin-service"
			"packages/discovery"
			"packages/httpmiddleware"
		)
fi

mkdir -p "$GO_BUILD_CACHE" "$GO_MOD_CACHE"

sum_all="0"
count_all=0
sum_covered="0"
count_covered=0

for svc in "${SERVICES[@]}"; do
	svc_dir="$ROOT_DIR/$svc"
	if [ ! -d "$svc_dir" ]; then
		echo "模块目录不存在: $svc"
		exit 1
	fi

	echo "==> go test -cover $svc"
	output="$(
		cd "$svc_dir" && \
			GOPROXY="$GO_PROXY" \
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
		sum_all="$(awk -v a="$sum_all" -v b="$pct" 'BEGIN { printf "%.4f", a + b }')"
		count_all=$((count_all + 1))

		if awk -v x="$pct" 'BEGIN { exit !(x > 0) }'; then
			sum_covered="$(awk -v a="$sum_covered" -v b="$pct" 'BEGIN { printf "%.4f", a + b }')"
			count_covered=$((count_covered + 1))
		fi
	done <<<"$percentages"
done

if [ "$count_all" -lt "$MIN_TOTAL_PACKAGES" ]; then
	echo "覆盖率门禁失败: 覆盖统计包数量不足，当前=${count_all}，要求>=${MIN_TOTAL_PACKAGES}"
	exit 1
fi

if [ "$count_covered" -lt "$MIN_COVERED_PACKAGES" ]; then
	echo "覆盖率门禁失败: 有覆盖率的包数量不足，当前=${count_covered}，要求>=${MIN_COVERED_PACKAGES}"
	exit 1
fi

avg_all="$(awk -v s="$sum_all" -v c="$count_all" 'BEGIN { printf "%.2f", s / c }')"
avg_covered="$(awk -v s="$sum_covered" -v c="$count_covered" 'BEGIN { printf "%.2f", s / c }')"

echo "coverage_packages_total=${count_all} average_coverage_all=${avg_all}% threshold_all=${COVERAGE_THRESHOLD_ALL}% covered_packages=${count_covered} average_coverage_nonzero=${avg_covered}% threshold_nonzero=${COVERAGE_THRESHOLD}%"

if ! awk -v avg="$avg_all" -v threshold="$COVERAGE_THRESHOLD_ALL" 'BEGIN { exit !(avg >= threshold) }'; then
	echo "覆盖率门禁失败: average_coverage_all=${avg_all}% < threshold_all=${COVERAGE_THRESHOLD_ALL}%"
	exit 1
fi

if ! awk -v avg="$avg_covered" -v threshold="$COVERAGE_THRESHOLD" 'BEGIN { exit !(avg >= threshold) }'; then
	echo "覆盖率门禁失败: average_coverage_nonzero=${avg_covered}% < threshold_nonzero=${COVERAGE_THRESHOLD}%"
	exit 1
fi

echo "覆盖率门禁通过"
