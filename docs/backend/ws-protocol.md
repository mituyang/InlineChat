# InlineChat WebSocket 协议（对外由 gateway-service 暴露，内部由 realtime-service 承接）

## 连接入口
- 外部路径：`gateway-service` 的 `GET /ws/:conversation_id`
- 内部承接：`realtime-service` 的同路径，由网关反向代理
- 参数：
  - `visitor_token`：访客连接必填
  - `access_token`：客服连接必填（JWT，`agent` 角色）
  - `last_message_id`：可选，断线补拉起点
- Origin 校验：由 `WS_ALLOWED_ORIGINS` 控制。

## 鉴权规则
- 带 `access_token` 时按客服链路鉴权，并向 `auth-service` 做二次校验。
- 不带 `access_token` 时按访客链路鉴权，要求 `visitor_token` 与会话绑定值一致。
- 两类连接都会先向 `chat-service` 校验会话存在，再向 `admin-service` 校验会话所属站点仍为 `active`。
- 校验失败时可能返回 `401/403/404/409`。

## 客户端 -> 服务端事件

### `message.send`
```json
{
  "type": "message.send",
  "payload": {
    "sender_type": "visitor",
    "content": "你好",
    "client_msg_id": "c1",
    "visitor_token": "vt_xxx"
  }
}
```

字段说明：
- `sender_type`：`visitor` 或 `agent`；缺省按 `visitor` 处理。
- `content`：必填，最大 2000 字符。
- `client_msg_id`：必填，幂等键。
- `visitor_token`：访客发送建议显式带上；不带时回退为连接上下文的 token。

## 服务端 -> 客户端事件

### `message.ack`
`message.send` 成功调用 `chat-service` 落库后立即回包；真正广播由 `chat -> Redis -> realtime` 异步链路完成。

```json
{
  "type": "message.ack",
  "payload": {
    "client_msg_id": "c1",
    "message_id": 123,
    "status": "sent"
  }
}
```

### `message.nack`
业务校验失败（参数错误、权限错误、会话状态错误等）。

```json
{
  "type": "message.nack",
  "payload": {
    "client_msg_id": "c1",
    "error": "invalid visitor_token"
  }
}
```

### `message.new`
同会话广播消息事件（来自 Redis 订阅或断线回放）。

```json
{
  "type": "message.new",
  "payload": {
    "conversation_id": 1,
    "message": {
      "id": 123,
      "conversation_id": 1,
      "sender_type": "visitor",
      "sender_id": "",
      "content": "你好",
      "client_msg_id": "c1",
      "status": "sent",
      "created_at": "2026-02-24T10:00:00Z",
      "updated_at": "2026-02-24T10:00:00Z"
    }
  }
}
```

### `message.status`
消息状态推进广播，当前用于 `read`。

区间更新：
```json
{
  "type": "message.status",
  "payload": {
    "conversation_id": 1,
    "sender_type": "agent",
    "up_to_message_id": 456,
    "status": "read"
  }
}
```

### `conversation.closed`
会话关闭事件广播。

```json
{
  "type": "conversation.closed",
  "payload": {
    "conversation_id": 1,
    "status": "closed"
  }
}
```

### `replay.end`
断线补拉结束标记。仅在携带 `last_message_id` 且触发回放时返回。

```json
{
  "type": "replay.end",
  "payload": {
    "conversation_id": 1,
    "last_message_id": 456,
    "replayed_count": 20,
    "truncated": false,
    "next_before_id": 0
  }
}
```

### `error`
WebSocket 帧级解析错误（如 JSON 格式错误）时返回：

```json
{
  "type": "error",
  "error": "invalid payload"
}
```

## 回放与补拉规则
- `last_message_id=0` 或缺失时，不触发回放。
- 回放按消息 `id` 升序推送。
- 单次连接最多回放 `500` 条，超出返回 `replay.end.truncated=true`，并给出 `next_before_id` 继续拉取锚点。

## 连接保活
- 服务端周期发送 ping，客户端需正常响应 pong。
- 写队列满会触发连接关闭，客户端应执行指数退避重连。
