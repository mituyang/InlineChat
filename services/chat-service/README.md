# chat-service

## 服务职责
- 管理会话与消息持久化（MySQL + GORM）。
- 提供会话认领、转接、关闭、已读、自动关闭等核心业务规则。
- 发布消息事件到 Redis（`message.new` / `message.status` / `conversation.closed`）。
- 通过 gRPC 向 `gateway-service`、`realtime-service` 提供能力。

## 端口与探针
- 默认 HTTP 端口：`8202`（`HTTP_PORT`）
- 默认 gRPC 端口：`8212`（`GRPC_PORT`）
- 健康检查：`GET /healthz`
- 就绪检查：`GET /readyz`（检查 MySQL + Redis）
- 指标：`GET /metrics`

## 对外能力
- gRPC：
  - `ChatGatewayService`
  - `ChatInternalService`
- HTTP（服务内调试入口，前缀 `/v1`）：
  - `POST /v1/conversations`
  - `GET /v1/conversations/:id`
  - `POST /v1/conversations/:id/messages`
  - `GET /v1/conversations/:id/messages`

## 依赖关系
- 必需依赖：
  - `MySQL`
  - `Redis`
  - `etcd`（注册 gRPC 地址）

## 关键环境变量
| 变量名 | 默认值 | 必填 | 说明 |
| --- | --- | --- | --- |
| `HTTP_PORT` | `8202` | 否 | HTTP 端口 |
| `GRPC_PORT` | `8212` | 否 | gRPC 端口 |
| `MYSQL_DSN` | - | 是 | MySQL 连接串 |
| `MYSQL_MAX_OPEN_CONNS` | `80` | 否 | 连接池上限 |
| `MYSQL_MAX_IDLE_CONNS` | `20` | 否 | 空闲连接数 |
| `MYSQL_QUERY_TIMEOUT_MS` | `1500` | 否 | 查询超时 |
| `REDIS_ADDR` | - | 是 | Redis 地址 |
| `REDIS_PASSWORD` | 空 | 否 | Redis 密码 |
| `REDIS_DB` | `0` | 否 | Redis DB |
| `AUTO_CLOSE_AFTER_SEC` | `300` | 否 | 自动关闭阈值 |
| `EVENT_OUTBOX_ENABLED` | `true` | 否 | 是否启用 outbox |
| `EVENT_OUTBOX_MAX_ATTEMPTS` | `8` | 否 | outbox 最大重试次数 |
| `EVENT_OUTBOX_REPLAY_DEAD_ON_START` | `false` | 否 | 启动回放死信 |
| `ETCD_ENDPOINTS` | - | 是 | etcd 地址列表 |
| `ETCD_REGISTER_TTL_SEC` | `15` | 否 | 注册租约 TTL |
| `DISCOVERY_PREFIX` | `/inlinechat/services` | 否 | 服务发现前缀 |
| `SERVICE_NAME` | `chat-service` | 否 | 注册服务名 |
| `SERVICE_ADVERTISE_GRPC_ENDPOINT` | - | 是 | 注册到 etcd 的 gRPC 地址 |

完整字段见：`services/chat-service/internal/config/config.go`

## 本地运行
```bash
go run ./services/chat-service/cmd/server
```

## 状态与一致性说明
- 消息状态：`sent -> read`
- 消息幂等键：`client_msg_id`（会话内唯一）
- 会话创建使用锁避免并发重复建会话
- Outbox 开启时，业务写入与事件落库同事务提交
