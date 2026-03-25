# InlineChat 后端 MVP 架构

## 目标
- 先完成匿名访客客服链路。
- 采用微服务结构，保证后续认证和业务扩展。
- 所有配置通过环境变量注入，避免硬编码。
- 数据库采用迁移脚本管理，不在服务启动时自动改表。

## 服务拆分
- `gateway-service`: 外部统一入口，托管 `apps/*` 静态资源；创建匿名会话前校验站点状态与 widget session；WebSocket 走反向代理到 realtime，其余 API 通过 gRPC 调 chat/auth/admin，并注入请求追踪 ID。
- `chat-service`: 会话与消息持久化（MySQL + GORM），并提供内部 gRPC 接口（给 gateway/realtime）。
- `realtime-service`: WebSocket 连接管理、断线补拉、Redis Pub/Sub 广播；通过 gRPC 调用 chat-service 完成会话查询/消息落库，通过 auth-service 做客服 token 二次校验，通过 admin-service 校验站点状态。
- `auth-service`: 启动时根据环境变量确保超级管理员账号存在（`super_admins` 表）、登录签发 JWT、会话自检接口，并提供 gRPC 接口给 gateway/realtime。
- `admin-service`: 站点管理、客服账号管理（客服账号使用 `agents` 表；JWT 管理员鉴权；创建客服账号仅允许 super_admin），并提供 gRPC 接口给 gateway/realtime。
- `etcd`: 服务注册与发现中心，gateway/realtime 通过它解析上游地址。

## 前端目录
- `apps/console-web`: 客服工作台与管理后台的 `Vue3 + Vite` 源码目录。
- `apps/customer-console`: 访客调试页源码目录。
- `apps/staff-login`: 员工统一登录页源码目录。
- `apps/widget-chat`: Widget 聊天窗（iframe）源码目录。
- `apps/demo-site`: 业务网站示例源码目录。
- `apps/api-docs`: Swagger UI 前端源码目录。
- `apps/widget-sdk/inlinechat-widget.js`: 嵌入脚本源码。
- `apps/*` 是唯一前端源码来源。`gateway-service` 镜像构建时直接打包这些目录到容器内 `public/*`，不再维护仓库内的前端副本目录。

## MVP 数据流
0. `chat-migrate`、`auth-migrate`、`admin-migrate` 在服务启动前执行 SQL 迁移。
1. 客户端先通过 `gateway-service` 创建会话；网关会校验站点状态与 `X-InlineChat-Widget-Session`，再通过 gRPC 调 `chat-service` 创建或复用会话。
2. 客户端连接 `gateway-service` 的 `GET /ws/:conversation_id`，由网关反向代理到 `realtime-service`。
3. `realtime-service` 在 WS 握手阶段通过 gRPC 调 `chat-service` 校验会话、调 `auth-service` 二次校验客服 token、调 `admin-service` 校验站点状态。
4. 发送 `message.send` 后，`realtime-service` 通过 gRPC 调用 `chat-service` 写入消息，并立即回 `message.ack`。
5. 消息写入成功后发到 Redis 频道，由所有 `realtime-service` 实例订阅并广播给同会话连接。
6. 业务 HTTP API 统一在 `gateway-service` 处理，再通过 gRPC 调 `chat-service`、`auth-service`、`admin-service`。
7. `chat/auth/admin/realtime` 启动后将自身地址注册到 `etcd`，由租约续期维持在线状态。

## 扩展预留
- gRPC 协议：
  - `packages/shared-types/proto/inlinechat/chat.proto`
  - `packages/shared-types/proto/inlinechat/auth.proto`
  - `packages/shared-types/proto/inlinechat/admin.proto`
- 访客模式：匿名优先，消息接口已保留 `visitor_token` 与 `sender_id`。
- 追踪字段：`X-Request-ID`（可通过 `GATEWAY_REQUEST_ID_HEADER` 配置）。
- 网关错误响应统一为 JSON，并携带 `request_id` 便于追踪。
