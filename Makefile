SHELL := /bin/bash

COMPOSE_FILE := infra/docker/docker-compose.yml
MONITORING_COMPOSE_FILE := infra/docker/docker-compose.monitoring.yml
ENV_FILE ?= .env
CACHE_DIR ?= $(CURDIR)/.cache
GO_BUILD_CACHE ?= $(CACHE_DIR)/go-build
GO_MOD_CACHE ?= $(CACHE_DIR)/go-mod
GO_SERVICE_MODULES := services/chat-service services/realtime-service services/gateway-service services/auth-service services/admin-service
GO_SHARED_MODULES := packages/discovery packages/httpmiddleware
GO_TEST_MODULES := $(GO_SERVICE_MODULES) $(GO_SHARED_MODULES)
GO_BUILD_OS ?= linux
GO_BUILD_ARCH ?= $(shell go env GOARCH)
GO_BUILD_OUTPUT := .bin/server
GO_PROXY ?= https://proxy.golang.org,direct
E2E_DIR := tests/e2e
COVERAGE_THRESHOLD ?= 45
COVERAGE_THRESHOLD_ALL ?= 12
MIN_COVERED_PACKAGES ?= 10
MIN_TOTAL_PACKAGES ?= 20

.PHONY: help ensure-env config up up-fg down restart logs ps monitoring-up monitoring-down monitoring-logs migrate migrate-chat migrate-auth migrate-admin fmt fmt-check vet lint test test-race test-cover env-lint quality e2e-ui verify-all proto build-local image-build smoke integration full-regression mvp-release

help:
	@echo "可用命令:"
	@echo "  make up             使用 .env 后台启动全部服务"
	@echo "  make build-local    本地编译全部服务二进制（Linux）"
	@echo "  make image-build    基于本地二进制构建 Docker 镜像"
	@echo "  make down           停止并删除容器"
	@echo "  make logs           查看服务日志"
	@echo "  make ps             查看服务状态"
	@echo "  make monitoring-up  在现有服务上叠加启动 Prometheus/Alertmanager/Grafana"
	@echo "  make monitoring-down 停止监控组件"
	@echo "  make monitoring-logs 查看监控组件日志"
	@echo "  make config         校验 docker compose 配置"
	@echo "  make migrate        执行全部迁移任务"
	@echo "  make lint           执行格式与静态检查（fmt-check + vet）"
	@echo "  make test           运行后端 Go 测试"
	@echo "  make test-race      运行后端 Go race 测试"
	@echo "  make test-cover     校验覆盖率门禁（有覆盖包平均值 + 最小包数）"
	@echo "  make env-lint       校验 .env 与 .env.example 配置键一致性"
	@echo "  make quality        执行完整质量门禁（lint + test + test-race + test-cover）"
	@echo "  make e2e-ui         执行前端 Playwright E2E（需先安装 tests/e2e 依赖）"
	@echo "  make verify-all     CI 全量验收入口（本地默认禁用，可用 VERIFY_ALLOW_LOCAL=1 临时开启）"
	@echo "  make smoke          运行端到端冒烟（登录/管理/会话/消息）"
	@echo "  make integration    运行系统集成检查（smoke + etcd + mysql + websocket）"
	@echo "  make full-regression 运行全功能回归（覆盖管理/认证/会话/转接/自动关闭/审计）"
	@echo "  make mvp-release    执行 MVP 验收流水（test + integration）"
	@echo "  make fmt            对后端 Go 代码执行 gofmt"
	@echo "  make proto          基于 proto 定义生成 gRPC 代码"

ensure-env:
	@if [ ! -f "$(ENV_FILE)" ]; then \
		echo "缺少 $(ENV_FILE)，请先执行: cp .env.example .env"; \
		exit 1; \
	fi

config:
	docker compose -f $(COMPOSE_FILE) --env-file .env.example config >/tmp/inlinechat-compose-check.txt
	@echo "compose 配置校验通过"

build-local:
	@mkdir -p $(GO_BUILD_CACHE) $(GO_MOD_CACHE)
	@for svc in $(GO_SERVICE_MODULES); do \
		echo "==> go build $$svc ($(GO_BUILD_OS)/$(GO_BUILD_ARCH))"; \
		( \
			cd $$svc && \
			mkdir -p .bin && \
			GOPROXY=$(GO_PROXY) \
			GOCACHE=$(GO_BUILD_CACHE) \
			GOMODCACHE=$(GO_MOD_CACHE) \
			CGO_ENABLED=0 GOOS=$(GO_BUILD_OS) GOARCH=$(GO_BUILD_ARCH) \
			go build -trimpath -o $(GO_BUILD_OUTPUT) ./cmd/server \
		) || exit 1; \
	done

image-build: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) build

up: ensure-env build-local image-build
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) up -d

up-fg: ensure-env build-local image-build
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) up

down: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) down

restart: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) restart

logs: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) logs -f --tail=200

ps: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) ps

monitoring-up: ensure-env
	docker compose -f $(COMPOSE_FILE) -f $(MONITORING_COMPOSE_FILE) --env-file $(ENV_FILE) up -d prometheus alertmanager grafana blackbox-exporter

monitoring-down: ensure-env
	docker compose -f $(COMPOSE_FILE) -f $(MONITORING_COMPOSE_FILE) --env-file $(ENV_FILE) stop prometheus alertmanager grafana blackbox-exporter
	docker compose -f $(COMPOSE_FILE) -f $(MONITORING_COMPOSE_FILE) --env-file $(ENV_FILE) rm -f prometheus alertmanager grafana blackbox-exporter

monitoring-logs: ensure-env
	docker compose -f $(COMPOSE_FILE) -f $(MONITORING_COMPOSE_FILE) --env-file $(ENV_FILE) logs -f --tail=200 prometheus alertmanager grafana blackbox-exporter

migrate: ensure-env migrate-chat migrate-auth migrate-admin
	@echo "迁移已执行完成"

migrate-chat: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) run --rm chat-migrate

migrate-auth: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) run --rm auth-migrate

migrate-admin: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) run --rm admin-migrate

fmt:
	@for svc in $(GO_TEST_MODULES); do \
		echo "==> gofmt $$svc"; \
		find $$svc -name '*.go' -print0 | xargs -0 gofmt -w; \
	done

fmt-check:
	@unformatted="$$(for svc in $(GO_TEST_MODULES); do find $$svc -name '*.go' -print0 | xargs -0 gofmt -l; done)"; \
	if [ -n "$$unformatted" ]; then \
		echo "以下文件未执行 gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vet:
	@mkdir -p $(GO_BUILD_CACHE) $(GO_MOD_CACHE)
	@for svc in $(GO_TEST_MODULES); do \
		echo "==> go vet $$svc"; \
		( \
			cd $$svc && \
			GOPROXY=$(GO_PROXY) \
			GOCACHE=$(GO_BUILD_CACHE) \
			GOMODCACHE=$(GO_MOD_CACHE) \
			go vet ./... \
		) || exit 1; \
	done

lint: fmt-check vet

test:
	@mkdir -p $(GO_BUILD_CACHE) $(GO_MOD_CACHE)
	@for svc in $(GO_TEST_MODULES); do \
		echo "==> go test $$svc"; \
		( \
			cd $$svc && \
			GOPROXY=$(GO_PROXY) \
			GOCACHE=$(GO_BUILD_CACHE) \
			GOMODCACHE=$(GO_MOD_CACHE) \
			go test ./... \
		) || exit 1; \
	done

test-race:
	@mkdir -p $(GO_BUILD_CACHE) $(GO_MOD_CACHE)
	@for svc in $(GO_TEST_MODULES); do \
		echo "==> go test -race $$svc"; \
		( \
			cd $$svc && \
			GOPROXY=$(GO_PROXY) \
			GOCACHE=$(GO_BUILD_CACHE) \
			GOMODCACHE=$(GO_MOD_CACHE) \
			go test -race ./... \
		) || exit 1; \
	done

test-cover:
	GO_PROXY=$(GO_PROXY) CACHE_DIR=$(CACHE_DIR) GO_BUILD_CACHE=$(GO_BUILD_CACHE) GO_MOD_CACHE=$(GO_MOD_CACHE) COVERAGE_THRESHOLD=$(COVERAGE_THRESHOLD) COVERAGE_THRESHOLD_ALL=$(COVERAGE_THRESHOLD_ALL) MIN_COVERED_PACKAGES=$(MIN_COVERED_PACKAGES) MIN_TOTAL_PACKAGES=$(MIN_TOTAL_PACKAGES) ./scripts/coverage-threshold.sh $(GO_TEST_MODULES)

env-lint:
	ENV_FILE=$(ENV_FILE) EXAMPLE_ENV_FILE=$(CURDIR)/.env.example ./scripts/env-lint.sh

quality: lint test test-race test-cover

e2e-ui:
	@if [ ! -d "$(E2E_DIR)/node_modules" ]; then \
		echo "缺少 $(E2E_DIR)/node_modules，请先执行: npm --prefix $(E2E_DIR) install"; \
		exit 1; \
	fi
	npm --prefix $(E2E_DIR) run test

verify-all: ensure-env
	@if [ "$${CI:-}" != "true" ] && [ "$${VERIFY_ALLOW_LOCAL:-0}" != "1" ]; then \
		echo "本地默认禁用 make verify-all（避免影响本地容器状态）"; \
		echo "如需本地执行，请显式运行: VERIFY_ALLOW_LOCAL=1 make verify-all"; \
		exit 1; \
	fi
	ENV_FILE=$(ENV_FILE) ./scripts/verify-all.sh

smoke: ensure-env
	ENV_FILE=$(ENV_FILE) ./scripts/smoke-e2e.sh

integration: ensure-env
	ENV_FILE=$(ENV_FILE) ./scripts/integration-system.sh

full-regression: ensure-env
	ENV_FILE=$(ENV_FILE) ./scripts/full-regression.sh

mvp-release: ensure-env test integration
	@echo "MVP 验收通过（test + integration）"

proto:
	./scripts/gen-proto.sh
