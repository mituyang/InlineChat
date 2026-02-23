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
- `apps/widget-sdk/inlinechat-widget.js`: 嵌入式 SDK 脚本源码
- `apps/*`: 前端单一源码目录（gateway 镜像构建时直接打包，无需同步双份目录）
- `infra/docker/docker-compose.yml`: 本地编排
- `docs/production-roadmap.md`: 生产化改造路线图
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
- 说明：
  - 访客端支持匿名会话创建、消息发送、WebSocket 实时收消息、断线自动重连与轮询兜底。
  - 客服与超级管理员通过同一登录页鉴权，登录后按角色进入对应工作台。
  - 客服能力（会话列表、认领、转接、关闭、客服发言）仅 `agent` 可用；`super_admin` 不具备客服能力。
  - 客服工作台与管理后台均支持亮色/暗色主题切换，并使用同一主题偏好存储键进行跨页面保持。
  - 客服端支持会话列表、会话认领/转接/关闭、消息收发、未读统计、快捷语、会话统计面板；客服与访客都可通过 WebSocket 发消息，断连自动重连并降级轮询。
  - WebSocket 支持断线补拉：连接时带 `last_message_id` 可回补该消息之后的历史消息。
  - 消息状态支持 `sent -> delivered -> read`：写入后为 `sent`，对端在线并成功入队后推进到 `delivered`，客户端显式上报已读后推进到 `read`。
  - 超级管理员账号只从 `.env` 读取并由 `auth-service` 启动时确保存在，管理台由超级管理员登录后创建客服账号。

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
- `make migrate`: 执行全部迁移
- `make lint`: 执行格式与静态检查（`fmt-check + vet`，覆盖 `services/*` 与 `packages/*`）
- `make test`: 运行后端模块测试（覆盖 `services/*` 与 `packages/*`）
- `make test-race`: 运行后端模块 race 测试（覆盖 `services/*` 与 `packages/*`）
- `make test-cover`: 校验覆盖率门禁（全量包平均值 + 有覆盖包平均值 + 最小包数）
- `make quality`: 执行完整质量门禁（`lint + test + test-race + test-cover`）
- `make smoke`: 运行端到端冒烟（健康检查、登录、管理接口、会话与消息）
- `make integration`: 运行系统集成检查（`smoke + etcd + mysql + websocket`）
- `make full-regression`: 运行全功能回归（管理、认证、会话、已读、认领、转接确认/拒绝、关闭、自动关闭、审计）
- `make mvp-release`: 执行 MVP 验收流水（`test + integration`）
- `make fmt`: 统一 gofmt
- `make proto`: 重新生成 gRPC 协议代码（chat/auth/admin/gateway/realtime）

## 测试与 CI
- 已补充核心业务单元测试：
  - `services/auth-service/internal/service/auth_service_test.go`
  - `services/admin-service/internal/service/admin_service_test.go`
  - `services/chat-service/internal/service/chat_service_test.go`
- CI 流水线：`.github/workflows/ci.yml`
  - `quality-gate` Job：执行 `make quality`（`lint + test + test-race + test-cover`，覆盖 `services/*` 与 `packages/*`）
  - `smoke-gate` Job：执行 `cp .env.example .env && make up && make smoke`，并在结束后自动 `make down`
  - `required-gate` Job：仅在 PR 触发，汇总校验 `quality-gate + smoke-gate` 结果，建议作为分支保护必选检查项
  - `integration-main` Job：仅在 `main` 分支 push 后触发，执行 `make integration` 进行合并后系统回归

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
- 广播时若检测到至少 1 个“对端角色”在线连接成功入队，`realtime-service` 会回写消息状态到 `delivered`
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

`message.new` 中的 `message` 结构会包含 `status` 字段（`sent`/`delivered`/`read`）。

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
