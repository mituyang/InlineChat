# gateway-service

## 服务职责
- 对外统一 HTTP 入口。
- 聚合并转发 `chat/auth/admin` gRPC 接口。
- 反向代理 `/ws/*` 到 `realtime-service`。
- 提供请求追踪、统一错误格式、限流、安全响应头。
- 托管前端静态资源（`/app/*`、`/sdk/*`）。

## 端口与探针
- 默认 HTTP 端口：`8200`（`HTTP_PORT`）
- 健康检查：`GET /healthz`
- 就绪检查：`GET /readyz`
- 指标：`GET /metrics`

## 主要路由
- `Chat API`: `/api/chat/v1/*`
- `Auth API`: `/api/auth/v1/auth/*`
- `Admin API`: `/api/admin/v1/admin/*`
- `WebSocket`: `/ws/*`（代理到 `realtime-service`）
- `前端静态资源`: `/app/*`、`/sdk/*`

## 依赖关系
- 必需依赖：
  - `etcd`（服务发现）
  - `chat-service`（gRPC）
  - `auth-service`（gRPC）
  - `admin-service`（gRPC）
  - `realtime-service`（HTTP）
- 可选依赖：
  - `Redis`（分布式限流；不可用时降级本地限流）

## 关键环境变量
| 变量名 | 默认值 | 必填 | 说明 |
| --- | --- | --- | --- |
| `HTTP_PORT` | `8200` | 否 | 网关监听端口 |
| `LOG_LEVEL` | `info` | 否 | 日志级别 |
| `REQUEST_ID_HEADER` | `X-Request-ID` | 否 | 请求追踪头 |
| `ETCD_ENDPOINTS` | - | 是 | etcd 地址列表 |
| `ETCD_DIAL_TIMEOUT_SEC` | `5` | 否 | etcd 连接超时 |
| `DISCOVERY_PREFIX` | `/inlinechat/services` | 否 | 服务发现前缀 |
| `CHAT_SERVICE_NAME` | `chat-service` | 否 | Chat 服务名 |
| `AUTH_SERVICE_NAME` | `auth-service` | 否 | Auth 服务名 |
| `ADMIN_SERVICE_NAME` | `admin-service` | 否 | Admin 服务名 |
| `REALTIME_SERVICE_NAME` | `realtime-service` | 否 | Realtime 服务名 |
| `GRPC_DIAL_TIMEOUT_SEC` | `8` | 否 | gRPC 建连超时 |
| `GRPC_CALL_TIMEOUT_SEC` | `8` | 否 | gRPC 调用超时 |
| `LOGIN_RATE_LIMIT_PER_MIN` | `60` | 否 | 登录限流每分钟额度 |
| `VISITOR_RATE_LIMIT_PER_MIN` | `180` | 否 | 访客限流每分钟额度 |
| `AGENT_RATE_LIMIT_PER_MIN` | `240` | 否 | 客服限流每分钟额度 |
| `ADMIN_RATE_LIMIT_PER_MIN` | `120` | 否 | 管理限流每分钟额度 |
| `RATE_LIMIT_KEY_TTL_MINS` | `30` | 否 | 限流键 TTL（分钟） |
| `RATE_LIMIT_REDIS_ADDR` | 继承 `REDIS_ADDR` | 否 | 分布式限流 Redis 地址 |

完整字段见：`services/gateway-service/internal/config/config.go`

## 本地运行
```bash
go run ./services/gateway-service/cmd/server
```

## 常见排障
- `GET /readyz` 返回 `503`：优先检查 etcd 与 4 个上游服务注册状态。
- 频繁 `429`：检查限流阈值与 Redis 降级日志。
