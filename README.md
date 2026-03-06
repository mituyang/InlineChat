# InlineChat Backend MVP

## 技术栈
- Go 1.25
- Gin
- WebSocket
- gRPC（服务间通讯）
- MySQL + GORM
- Redis
- etcd（服务注册与发现）
- Zap
- Docker Compose

## 目录
- `services/`: 后端微服务
- `apps/agent-console`: 客服工作台前端源码
- `apps/admin-console`: 管理后台前端源码
- `apps/customer-console`: 访客调试页前端源码
- `apps/widget-chat`: Widget 聊天窗（iframe）前端源码
- `apps/demo-site`: 业务网站示例前端源码（已嵌入客服）
- `apps/api-docs`: Swagger UI 接口文档前端源码
- `apps/widget-sdk/inlinechat-widget.js`: 嵌入式 SDK 脚本源码
- `apps/*`: 前端单一源码目录（gateway 镜像构建时直接打包，无需同步双份目录）
- `tests/e2e`: Playwright 前端主流程 E2E（Chromium，5 场景）
- `infra/docker/docker-compose.yml`: 本地编排
- `docs/production-roadmap.md`: 生产化改造路线图
- `docs/cicd.md`: CI/CD 使用说明（门禁、发布、回滚）
- `docs/branch-protection-checklist.md`: `main` 分支保护配置清单
- `docs/observability.md`: 可观测性与告警接入说明
- `docs/backend/README.md`: 后端文档索引（服务说明、协议、错误码、Runbook）
- `docs/backend/http-api.md`: 网关 HTTP API 约定
- `docs/backend/ws-protocol.md`: WebSocket 协议约定
- `docs/backend/openapi.yaml`: 网关 OpenAPI 草案（chat/auth/admin）
- `docs/backend/grpc-contract.md`: gRPC 服务契约说明
- `docs/backend/conversation-state-machine.md`: 会话与消息状态机
- `docs/backend/error-codes.md`: 网关错误码映射
- `docs/backend/runbook.md`: 后端运维排障手册
- `.github/CODEOWNERS`: 目录责任人配置
- `.github/pull_request_template.md`: PR 标准模板
- `.github/ISSUE_TEMPLATE/*`: Issue 标准模板
- Dependabot：单人开发模式默认关闭（避免自动 PR 噪声）
- `.env.example`: 环境变量模板

前端资源加载规则：
- 容器内优先读取 `./public/*`（由镜像构建时从 `apps/*` 打包）。
- 本地直接运行 `gateway-service` 时可自动回退读取 `apps/*`，无需同步脚本。

## 本地启动
1. `cp .env.example .env`
2. 在 `.env` 设置超级管理员账号（`SUPER_ADMIN_EMAIL`、`SUPER_ADMIN_PASSWORD`、`SUPER_ADMIN_DISPLAY_NAME`）
   - `SUPER_ADMIN_PASSWORD` 必须满足：12-72 位、包含大小写字母/数字/特殊字符、不能包含空白字符、不能使用常见弱口令。
   - 若浏览器出现“密码泄露/检查已保存密码”提示，通常是浏览器密码管理器检测到旧弱口令，不是后端报错；更新为强口令并更新浏览器已保存密码即可。
   - 如需平滑轮换 JWT 密钥，设置 `JWT_PREVIOUS_SECRET` 为上一把密钥（留空表示不启用轮换）。
   - 网关限流参数：`LOGIN_RATE_LIMIT_PER_MIN`、`LOGIN_RATE_LIMIT_BURST`、`VISITOR_RATE_LIMIT_PER_MIN`、`VISITOR_RATE_LIMIT_BURST`、`AGENT_RATE_LIMIT_PER_MIN`、`AGENT_RATE_LIMIT_BURST`、`ADMIN_RATE_LIMIT_PER_MIN`、`ADMIN_RATE_LIMIT_BURST`、`RATE_LIMIT_KEY_TTL_MINS`。
   - 网关分布式限流（可选）参数：`RATE_LIMIT_REDIS_ADDR`、`RATE_LIMIT_REDIS_PASSWORD`、`RATE_LIMIT_REDIS_DB`、`RATE_LIMIT_REDIS_PREFIX`、`RATE_LIMIT_REDIS_TIMEOUT_MS`（Redis 不可用时自动降级为本地限流）。
   - MySQL 连接池与查询超时参数：`MYSQL_MAX_OPEN_CONNS`、`MYSQL_MAX_IDLE_CONNS`、`MYSQL_CONN_MAX_LIFETIME_SEC`、`MYSQL_CONN_MAX_IDLE_TIME_SEC`、`MYSQL_QUERY_TIMEOUT_MS`。
   - WebSocket Origin 白名单：`WS_ALLOWED_ORIGINS` 必须显式配置（逗号分隔），不再建议使用 `*`。
   - outbox 死信参数：`EVENT_OUTBOX_MAX_ATTEMPTS`、`EVENT_OUTBOX_REPLAY_DEAD_ON_START`、`EVENT_OUTBOX_REPLAY_DEAD_BATCH`。
3. `docker compose -f infra/docker/docker-compose.yml --env-file .env up --build`
4. 网关健康检查：`GET http://localhost:8200/healthz`（进程存活）
5. 网关就绪检查：`GET http://localhost:8200/readyz`（上游依赖可用）
6. 网关会在请求与响应里统一透传 `X-Request-ID`（可用 `GATEWAY_REQUEST_ID_HEADER` 配置）

基础依赖默认宿主机端口：
- MySQL: `8233 -> 3306`
- Redis: `8236 -> 6379`
- etcd: `8237 -> 2379`

## 前端访问
- 员工统一登录页（客服/超级管理员共用）：`http://localhost:8200/app/staff-login/`
- 访客端：`http://localhost:8200/app/customer/`
- 客服端（未登录会自动跳转登录页）：`http://localhost:8200/app/agent/`
- 管理端（未登录会自动跳转登录页）：`http://localhost:8200/app/admin/`
- 嵌入式 Widget 聊天页（iframe 页面）：`http://localhost:8200/app/widget/`
- 嵌入脚本：`http://localhost:8200/sdk/inlinechat-widget.js`
- 业务网站示例（已嵌入客服）：`http://localhost:8200/app/demo/`
- 在线 API 文档（Swagger UI）：`http://localhost:8200/app/api-docs/`
- OpenAPI 原文：`http://localhost:8200/docs/backend/openapi.yaml`
- 说明：
  - 访客端支持匿名会话创建、消息发送、WebSocket 实时收消息、断线自动重连与轮询兜底。
  - 客服与超级管理员通过同一登录页鉴权，登录后按角色进入对应工作台。
  - 客服能力（会话列表、认领、转接、关闭、客服发言）仅 `agent` 可用；`super_admin` 不具备客服能力。
  - 客服工作台与管理后台均支持亮色/暗色主题切换，并使用同一主题偏好存储键进行跨页面保持。
  - 客服端支持会话列表、会话认领/转接/关闭、消息收发、未读统计、快捷语、会话统计面板；客服与访客都可通过 WebSocket 发消息，断连自动重连并降级轮询。
  - WebSocket 支持断线补拉：连接时带 `last_message_id` 可回补该消息之后的历史消息。
  - 消息状态支持 `sent -> read`：写入后为 `sent`，客户端显式上报已读后推进到 `read`。
  - 超级管理员账号只从 `.env` 读取并由 `auth-service` 启动时确保存在（写入 `super_admins` 表）；客服账号存储在 `agents` 表并由管理台创建。

## Widget 嵌入方式
在任意网站页面底部插入以下脚本（`data-site-id` 必填）：

```html
<script
  src="http://localhost:8200/sdk/inlinechat-widget.js"
  data-site-id="site_demo"
  data-title="在线客服"
  defer
></script>
```

注意：
- `data-site-id` 必须使用管理后台已创建站点的 `site_id`，前端值与后台站点不匹配将无法创建会话。
- 管理后台创建站点时需先填写 `site_id`（可点击“生成ID”按钮），再提交创建。
- 建议直接使用管理后台站点卡片中的“嵌入脚本”复制结果，避免手工输入错误。

可选参数：
- `data-gateway-origin`: 网关地址（默认取脚本 `src` 的域名）
- `data-primary-color`: 右下角图标主色（默认 `#2f343c`）
- `data-bottom` / `data-right`: 图标距底部和右侧距离（默认 `24px`）
- `data-panel-width` / `data-panel-height`: 聊天窗尺寸（默认 `380px` / `620px`）

说明：
- 脚本会在右下角渲染悬浮按钮，点击后打开聊天窗。
- 聊天窗内部使用 `wss/ws` 连接 `/ws/:conversation_id` 实时通道，并带 HTTP 轮询兜底。
  - 客服端连接 WS 需追加 `access_token`：`/ws/:conversation_id?access_token=...`。
- 本地示例宿主页：`apps/widget-sdk/demo-host.html`
- 业务网站示例请在 `apps/demo-site/config.js` 手动配置 `siteID`，然后访问 `http://localhost:8200/app/demo/` 验证嵌入效果。

## Makefile
- `make build-local`: 本地先编译全部服务二进制（`linux` 目标）
- `make image-build`: 使用本地二进制构建 Docker 镜像
- `make up`: 先本地编译、再构建镜像、最后后台启动全部服务
- `make down`: 停止并删除容器
- `make logs`: 跟随日志
- `make monitoring-up`: 叠加启动监控组件（Prometheus/Alertmanager/Grafana/Blackbox）
- `make monitoring-down`: 停止监控组件
- `make monitoring-logs`: 查看监控组件日志
- `make migrate`: 执行全部迁移
- `make lint`: 执行格式与静态检查（`fmt-check + vet`，覆盖 `services/*` 与 `packages/*`）
- `make test`: 运行后端模块测试（覆盖 `services/*` 与 `packages/*`）
- `make test-race`: 运行后端模块 race 测试（覆盖 `services/*` 与 `packages/*`）
- `make test-cover`: 校验覆盖率门禁（全量包平均值 + 有覆盖包平均值 + 最小包数）
- `make env-lint`: 校验 `.env` 与 `.env.example` 键一致性（防配置漂移）
- `make quality`: 执行完整质量门禁（`lint + test + test-race + test-cover`）
- `make smoke`: 运行端到端冒烟（健康检查、登录、管理接口、会话与消息）
- `make integration`: 运行系统集成检查（`smoke + etcd + mysql + websocket`）
- `make full-regression`: 运行全功能回归（管理、认证、会话、已读、认领、转接确认/拒绝、关闭、自动关闭、审计）
- `make e2e-ui`: 运行前端 Playwright E2E（5 个主流程场景，Chromium）
- `make verify-all`: 一键全量验证（`env-lint + quality + smoke + integration + full-regression + e2e-ui`，默认自动 `up/down`）
- `make mvp-release`: 执行 MVP 验收流水（`test + integration`）
- `make fmt`: 统一 gofmt
- `make proto`: 重新生成 gRPC 协议代码（chat/auth/admin/gateway/realtime）

## 全量验证（verify-all）
- 本地执行：
  - `make verify-all`
- `verify-all` 默认行为：
  - 启动前自动 `make down`（容错清理）
  - 自动 `make up`
  - 依次执行 `env-lint -> quality -> smoke -> integration -> full-regression -> e2e-ui`
  - 结束后自动 `make down`
- 可用控制变量：
  - `VERIFY_AUTO_UPDOWN=1|0`：是否自动管理环境（默认 `1`）
  - `VERIFY_KEEP_ENV_ON_FAIL=1|0`：失败时保留现场（默认 `0`）
  - `VERIFY_KEEP_ENV=1|0`：无论成功失败都保留环境（默认 `0`）
- 前端 E2E 依赖（首次）：
  - `npm --prefix tests/e2e install`
  - `npx --prefix tests/e2e playwright install --with-deps chromium`
- 前端 E2E 运行参数：
  - `E2E_BASE_URL`（默认 `http://127.0.0.1:8200`）
  - `E2E_SUPER_ADMIN_EMAIL`（默认回退 `.env` 的 `SUPER_ADMIN_EMAIL`）
  - `E2E_SUPER_ADMIN_PASSWORD`（默认回退 `.env` 的 `SUPER_ADMIN_PASSWORD`）
- 常见失败排查：
  - `tests/e2e/node_modules` 缺失：先执行依赖安装
  - Playwright 浏览器未安装：执行 `playwright install --with-deps chromium`
  - 端口冲突：先执行 `make down` 再重跑

## 测试与 CI
- 已补充核心业务单元测试：
  - `services/auth-service/internal/service/auth_service_test.go`
  - `services/admin-service/internal/service/admin_service_test.go`
  - `services/chat-service/internal/service/chat_service_test.go`
- CI 流水线：`.github/workflows/ci.yml`
  - `verify-all` Job：每次 `push main` 执行 `make verify-all`（全量门禁）
  - 失败时上传 Playwright 报告、测试结果与关键容器日志
- 安全流水线：`.github/workflows/security.yml`
  - `govulncheck` Job：扫描所有 Go 模块已知漏洞（每月 + 手动）
- CD 流水线：`.github/workflows/cd.yml`
  - 自动构建触发改为监听 `CI` 成功（`workflow_run`），CI 失败时阻断镜像构建
  - `build-and-push-images` Job：构建并推送 5 个服务镜像到 GHCR（`sha-<commit>`，`workflow_run` 场景附加 `main` 标签）
  - `release-or-rollback` Job：手动触发，支持 `deploy/rollback` 与环境选择，支持可选 Webhook 集成
- 分支保护清单：`docs/branch-protection-checklist.md`
  - 单人开发模式：仅保留“禁止 force-push + 禁止删除 main”
- 协作治理配置：
  - `CODEOWNERS`：限定核心目录责任人
  - `PR/Issue` 模板：统一评审与缺陷输入质量
  - 单人开发模式默认关闭 Dependabot 自动 PR（避免噪声）

## 监控与告警
- 监控编排文件：`infra/docker/docker-compose.monitoring.yml`
- 监控配置目录：`infra/monitoring`
- 启动方式（叠加基础服务）：
  - `make monitoring-up`
  - 或执行：
    - `docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.monitoring.yml --env-file .env up -d`
- 默认访问地址：
  - Prometheus：`http://localhost:${PROMETHEUS_HOST_PORT:-9090}`
  - Alertmanager：`http://localhost:${ALERTMANAGER_HOST_PORT:-9093}`
  - Grafana：`http://localhost:${GRAFANA_HOST_PORT:-3000}`
- Grafana 默认会自动加载总览面板：`InlineChat Overview`
- 告警通知通道模板：`infra/monitoring/alertmanager.channels.example.yml`
  - 启用方式：复制为 `infra/monitoring/alertmanager.yml` 并填写 webhook / smtp 配置后重启监控组件

## 生产化基线（已启用）
- 全服务提供双探针：
  - `GET /healthz`：仅表示进程存活（liveness）
  - `GET /readyz`：表示依赖可用（readiness，含 MySQL/Redis/etcd 上游检查）
- 所有 HTTP 服务启用基础超时：
  - `ReadHeaderTimeout=5s`
  - `ReadTimeout=15s`
  - `WriteTimeout=20s`
  - `IdleTimeout=60s`
- 所有 HTTP 服务启用统一安全响应头中间件：
  - `X-Content-Type-Options: nosniff`
  - `Referrer-Policy`
  - `Permissions-Policy`
- 所有 HTTP 服务暴露 `GET /metrics`（Prometheus 格式），包含请求总量、延迟分布、在途请求数。
- `gateway-service` 启用匿名侧与登录侧限流（支持 Redis 分布式限流，Redis 异常自动降级本地限流），超限返回 `429 rate_limited`。
- `chat-service` outbox 支持最大重试、死信（`dead`）和启动回放（可选）。
- `auth-service`、`admin-service`、`realtime-service` 支持 JWT 双密钥验签（`JWT_SECRET` + `JWT_PREVIOUS_SECRET`），用于无损密钥轮换。
- `docker compose` 已对业务服务启用 `readyz` 健康检查，并将关键依赖切换为 `service_healthy`，避免“已启动但不可用”的级联故障。

## 数据库迁移
- 迁移目录：`services/chat-service/migrations`
- 编排会自动执行 `chat-migrate`、`auth-migrate`、`admin-migrate`
- 对应服务只会在迁移成功后启动

## 服务间通信
- 所有服务通过 `etcd` 做服务注册与发现，键前缀由 `DISCOVERY_PREFIX` 控制（默认 `/inlinechat/services`）。
- `gateway-service` 通过 gRPC 调用 `chat-service`、`auth-service`、`admin-service`
- `realtime-service` 通过 gRPC 调用 `chat-service`
- `chat-service` 在消息写入成功后会发布 `message.new` 到 Redis 频道，`realtime-service` 订阅后广播给 WebSocket 客户端（访客与客服均可实时收到）
- gRPC 协议定义：
  - `packages/shared-types/proto/inlinechat/chat.proto`
  - `packages/shared-types/proto/inlinechat/auth.proto`
  - `packages/shared-types/proto/inlinechat/admin.proto`
- gRPC 超时配置：`CHAT_GRPC_DIAL_TIMEOUT_SEC`、`CHAT_GRPC_CALL_TIMEOUT_SEC`
- 网关 gRPC 超时配置：`GRPC_DIAL_TIMEOUT_SEC`、`GRPC_CALL_TIMEOUT_SEC`
- etcd 配置：`ETCD_ENDPOINTS`、`ETCD_DIAL_TIMEOUT_SEC`、`ETCD_REGISTER_TTL_SEC`

## MVP 接口
- Chat:
  - `POST /api/chat/v1/conversations`
  - `GET /api/chat/v1/conversations`（需要 Bearer Token）
  - `POST /api/chat/v1/conversations/:id/claim`（需要 Bearer Token）
  - `POST /api/chat/v1/conversations/:id/transfer`（需要 Bearer Token）
  - `POST /api/chat/v1/conversations/:id/close`（需要 Bearer Token）
  - `POST /api/chat/v1/conversations/:id/messages`
  - `GET /api/chat/v1/conversations/:id/messages`
  - `POST /api/chat/v1/conversations/:id/read`
- Auth:
  - `POST /api/auth/v1/auth/login`
  - `GET /api/auth/v1/auth/me`
- Admin:
  - `POST /api/admin/v1/admin/sites`（需要 Bearer Token，且角色为 `admin/super_admin`；请求体需包含 `site_id`、`name`、`domain`）
  - `GET /api/admin/v1/admin/sites`（需要 Bearer Token，且角色为 `admin/super_admin`）
  - `POST /api/admin/v1/admin/agents`（需要 Bearer Token，且角色必须为 `super_admin`；请求体需包含 `agent_id`（4位数字，不能为`0000`）、`email`、`password`、`display_name`；`email` 与 `display_name` 全局唯一）
  - `GET /api/admin/v1/admin/agents`（需要 Bearer Token，且角色为 `admin/super_admin`）
- Realtime:
 - `GET /ws/:conversation_id`（访客需 `visitor_token`，客服需 `access_token`，可选 `last_message_id` 用于断线补拉）

## WebSocket 示例
发送：
```json
{"type":"message.send","payload":{"sender_type":"visitor","content":"你好","client_msg_id":"c1","visitor_token":"vt_xxx"}}
```

客服发送（需 `access_token`）：
```json
{"type":"message.send","payload":{"sender_type":"agent","content":"您好，请问有什么可以帮您？","client_msg_id":"a1"}}
```

`message.new` 中的 `message` 结构会包含 `status` 字段（`sent`/`read`）。

断线补拉示例（从 `message_id=120` 之后继续）：
- `GET /ws/:conversation_id?visitor_token=vt_xxx&last_message_id=120`

## 网关错误响应
```json
{
  "error": {
    "code": "upstream_unavailable",
    "message": "upstream service unavailable"
  },
  "request_id": "..."
}
```
