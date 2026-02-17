SHELL := /bin/bash

COMPOSE_FILE := infra/docker/docker-compose.yml
ENV_FILE ?= .env
CACHE_DIR := $(CURDIR)/.cache
GO_TEST_SERVICES := services/chat-service services/realtime-service services/gateway-service services/auth-service services/admin-service
GO_BUILD_OS ?= linux
GO_BUILD_ARCH ?= $(shell go env GOARCH)
GO_BUILD_OUTPUT := .bin/server
GO_PROXY ?= https://proxy.golang.org,direct

.PHONY: help ensure-env config sync-frontend up up-fg down restart logs ps migrate migrate-chat migrate-auth migrate-admin fmt test proto build-local image-build smoke mvp-release

help:
	@echo "可用命令:"
	@echo "  make up             使用 .env 后台启动全部服务"
	@echo "  make build-local    本地编译全部服务二进制（Linux）"
	@echo "  make image-build    基于本地二进制构建 Docker 镜像"
	@echo "  make sync-frontend  同步 apps 前端到 gateway 静态目录"
	@echo "  make down           停止并删除容器"
	@echo "  make logs           查看服务日志"
	@echo "  make ps             查看服务状态"
	@echo "  make config         校验 docker compose 配置"
	@echo "  make migrate        执行全部迁移任务"
	@echo "  make test           运行后端 Go 测试"
	@echo "  make smoke          运行端到端冒烟（登录/管理/会话/消息）"
	@echo "  make mvp-release    执行 MVP 验收流水（sync-frontend + test + smoke）"
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

sync-frontend:
	./scripts/sync-frontends.sh

build-local:
	@mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	@for svc in $(GO_TEST_SERVICES); do \
		echo "==> go build $$svc ($(GO_BUILD_OS)/$(GO_BUILD_ARCH))"; \
		( \
			cd $$svc && \
			mkdir -p .bin && \
			GOPROXY=$(GO_PROXY) \
			GOCACHE=$(CACHE_DIR)/go-build \
			GOMODCACHE=$(CACHE_DIR)/go-mod \
			CGO_ENABLED=0 GOOS=$(GO_BUILD_OS) GOARCH=$(GO_BUILD_ARCH) \
			go build -trimpath -o $(GO_BUILD_OUTPUT) ./cmd/server \
		) || exit 1; \
	done

image-build: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) build

up: ensure-env sync-frontend build-local image-build
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) up -d

up-fg: ensure-env sync-frontend build-local image-build
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) up

down: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) down

restart: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) restart

logs: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) logs -f --tail=200

ps: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) ps

migrate: ensure-env migrate-chat migrate-auth migrate-admin
	@echo "迁移已执行完成"

migrate-chat: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) run --rm chat-migrate

migrate-auth: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) run --rm auth-migrate

migrate-admin: ensure-env
	docker compose -f $(COMPOSE_FILE) --env-file $(ENV_FILE) run --rm admin-migrate

fmt:
	@for svc in $(GO_TEST_SERVICES); do \
		echo "==> gofmt $$svc"; \
		find $$svc -name '*.go' -print0 | xargs -0 gofmt -w; \
	done

test:
	@mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	@for svc in $(GO_TEST_SERVICES); do \
		echo "==> go test $$svc"; \
		( \
			cd $$svc && \
			GOCACHE=$(CACHE_DIR)/go-build \
			GOMODCACHE=$(CACHE_DIR)/go-mod \
			go test ./... \
		) || exit 1; \
	done

smoke: ensure-env
	ENV_FILE=$(ENV_FILE) ./scripts/smoke-e2e.sh

mvp-release: ensure-env sync-frontend test smoke
	@echo "MVP 验收通过（sync-frontend + test + smoke）"

proto:
	./scripts/gen-proto.sh
