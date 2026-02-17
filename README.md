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
- `.env.example`: 环境变量模板

前端资源加载规则：
- 容器内优先读取 `./public/*`（由镜像构建时从 `apps/*` 打包）。
- 本地直接运行 `gateway-service` 时可自动回退读取 `apps/*`，无需同步脚本。

## 本地启动
1. `cp .env.example .env`
2. 在 `.env` 设置超级管理员账号（`SUPER_ADMIN_EMAIL`、`SUPER_ADMIN_PASSWORD`、`SUPER_ADMIN_DISPLAY_NAME`）
3. `docker compose -f infra/docker/docker-compose.yml --env-file .env up --build`
4. 网关健康检查：`GET http://localhost:8200/healthz`
5. 网关会在请求与响应里统一透传 `X-Request-ID`（可用 `GATEWAY_REQUEST_ID_HEADER` 配置）

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
  - 客服端支持会话列表、会话认领/转接/关闭、消息收发、未读统计、快捷语、会话统计面板；活跃会话消息优先走 WebSocket，断连自动重连并降级轮询。
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

可选参数：
- `data-gateway-origin`: 网关地址（默认取脚本 `src` 的域名）
- `data-primary-color`: 右下角图标主色（默认 `#2f343c`）
- `data-bottom` / `data-right`: 图标距底部和右侧距离（默认 `24px`）
- `data-panel-width` / `data-panel-height`: 聊天窗尺寸（默认 `380px` / `620px`）

说明：
- 脚本会在右下角渲染悬浮按钮，点击后打开聊天窗。
- 聊天窗内部使用 `wss/ws` 连接 `/ws/:conversation_id` 实时通道，并带 HTTP 轮询兜底。
- 本地示例宿主页：`apps/widget-sdk/demo-host.html`

## Makefile
- `make build-local`: 本地先编译全部服务二进制（`linux` 目标）
- `make image-build`: 使用本地二进制构建 Docker 镜像
- `make up`: 先本地编译、再构建镜像、最后后台启动全部服务
- `make down`: 停止并删除容器
- `make logs`: 跟随日志
- `make migrate`: 执行全部迁移
- `make test`: 运行后端测试
- `make smoke`: 运行端到端冒烟（健康检查、登录、管理接口、会话与消息）
- `make integration`: 运行系统集成检查（`smoke + etcd + mysql + websocket`）
- `make mvp-release`: 执行 MVP 验收流水（`test + integration`）
- `make fmt`: 统一 gofmt
- `make proto`: 重新生成 gRPC 协议代码（chat/auth/admin/gateway/realtime）

## 测试与 CI
- 已补充核心业务单元测试：
  - `services/auth-service/internal/service/auth_service_test.go`
  - `services/admin-service/internal/service/admin_service_test.go`
  - `services/chat-service/internal/service/chat_service_test.go`
- CI 流水线：`.github/workflows/ci.yml`
  - `test` Job：执行 `make test`
  - `integration` Job：执行 `cp .env.example .env && make up && make integration`，并在结束后自动 `make down`

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
- Auth:
  - `POST /api/auth/v1/auth/login`
  - `GET /api/auth/v1/auth/me`
- Admin:
  - `POST /api/admin/v1/admin/sites`（需要 Bearer Token，且角色为 `admin/super_admin`）
  - `GET /api/admin/v1/admin/sites`（需要 Bearer Token，且角色为 `admin/super_admin`）
  - `POST /api/admin/v1/admin/agents`（需要 Bearer Token，且角色必须为 `super_admin`）
  - `GET /api/admin/v1/admin/agents`（需要 Bearer Token，且角色为 `admin/super_admin`）
- Realtime:
  - `GET /ws/:conversation_id`

## WebSocket 示例
发送：
```json
{"type":"message.send","payload":{"content":"你好","client_msg_id":"c1","visitor_token":"vt_xxx"}}
```

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
