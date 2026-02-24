# realtime-service

## 服务职责
- 管理 WebSocket 连接（访客与客服）。
- 将客户端 `message.send` 转换为 `chat-service` gRPC 写入请求。
- 订阅 Redis 事件并向同会话连接广播。
- 在“对端在线且成功入队”时，回写 `delivered` 状态到 `chat-service`。

## 端口与探针
- 默认 HTTP 端口：`8203`（`HTTP_PORT`）
- 健康检查：`GET /healthz`
- 就绪检查：`GET /readyz`（检查 Redis + chat/auth 发现结果）
- 指标：`GET /metrics`
- WebSocket：`GET /ws/:conversation_id`

## 协议要点
- 连接参数：
  - 访客：`visitor_token`
  - 客服：`access_token`
  - 可选：`last_message_id`（断线补拉）
- 客户端事件：`message.send`
- 服务端事件：`message.ack`、`message.nack`、`message.new`、`message.status`、`conversation.closed`、`replay.end`

详细协议见：`docs/backend/ws-protocol.md`

## 依赖关系
- 必需依赖：
  - `Redis`（Pub/Sub）
  - `etcd`（发现 `chat/auth`）
  - `chat-service`（gRPC）
  - `auth-service`（gRPC）

## 关键环境变量
| 变量名 | 默认值 | 必填 | 说明 |
| --- | --- | --- | --- |
| `HTTP_PORT` | `8203` | 否 | HTTP/WS 端口 |
| `REDIS_ADDR` | - | 是 | Redis 地址 |
| `REDIS_PASSWORD` | 空 | 否 | Redis 密码 |
| `REDIS_DB` | `0` | 否 | Redis DB |
| `JWT_SECRET` | - | 是 | JWT 主密钥 |
| `JWT_PREVIOUS_SECRET` | 空 | 否 | JWT 旧密钥（轮换窗口） |
| `JWT_ISSUER` | `inlinechat-auth` | 否 | JWT 发行者 |
| `WS_ALLOWED_ORIGINS` | - | 是 | Origin 白名单（逗号分隔） |
| `CHAT_GRPC_DIAL_TIMEOUT_SEC` | `8` | 否 | chat gRPC 建连超时 |
| `CHAT_GRPC_CALL_TIMEOUT_SEC` | `8` | 否 | chat gRPC 调用超时 |
| `ETCD_ENDPOINTS` | - | 是 | etcd 地址列表 |
| `DISCOVERY_PREFIX` | `/inlinechat/services` | 否 | 服务发现前缀 |
| `CHAT_SERVICE_NAME` | `chat-service` | 否 | Chat 服务名 |
| `AUTH_SERVICE_NAME` | `auth-service` | 否 | Auth 服务名 |
| `SERVICE_NAME` | `realtime-service` | 否 | 注册服务名 |
| `SERVICE_ADVERTISE_HTTP_ENDPOINT` | - | 是 | 注册到 etcd 的 HTTP 地址 |

完整字段见：`services/realtime-service/internal/config/config.go`

## 本地运行
```bash
go run ./services/realtime-service/cmd/server
```

## 常见排障
- WS 升级失败：优先检查 `WS_ALLOWED_ORIGINS`。
- 只能收到 `ack` 收不到 `message.new`：优先检查 Redis 订阅链路与对端是否在线。
