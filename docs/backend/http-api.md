# InlineChat HTTP API（后端）

## 入口与约定
- 网关入口：`http://localhost:8200`
- 统一 API 前缀：
  - `Chat`: `/api/chat/v1`
  - `Auth`: `/api/auth/v1/auth`
  - `Admin`: `/api/admin/v1/admin`
- WebSocket 外部入口见：`docs/backend/ws-protocol.md`（`gateway-service` 暴露 `GET /ws/:conversation_id`）
- 统一错误格式（经 `gateway-service` 返回）：

```json
{
  "error": {
    "code": "invalid_argument",
    "message": "xxx"
  },
  "request_id": "..."
}
```

## 鉴权模式
- 访客模式：依赖 `visitor_token`（query 或 body，按接口定义）。
- 匿名建会话额外要求 `X-InlineChat-Widget-Session`，用于校验站点来源与 widget 会话。
- 员工模式：`Authorization: Bearer <token>`。
- 角色约束：
  - 客服接口由 `agent` 访问（例如会话认领/转接/关闭）。
  - 管理接口由 `admin/super_admin` 访问；部分高风险操作仅 `super_admin` 可执行（由下游服务校验）。

## Widget Session 约定
- 请求头：`X-InlineChat-Widget-Session`
- 适用接口：`POST /api/chat/v1/conversations`
- 生成方式：由 `gateway-service` 的 `/app/widget/?site_id=...&parent_origin=...` 初始化页面注入；官方 `widget-chat` 与 `customer-console` 已自动处理
- 校验内容：站点 `site_id`、`widget_key`、`site_domain`、过期时间

## Chat API
| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| `POST` | `/api/chat/v1/conversations` | 访客 + Widget Session | 创建匿名会话（需 `site_id` + `visitor_token` + `X-InlineChat-Widget-Session`；网关会先校验站点状态） |
| `GET` | `/api/chat/v1/conversations` | `agent` | 会话列表（支持 `status/site_id/assigned_agent_id/unassigned_only/limit/offset`） |
| `GET` | `/api/chat/v1/conversations/:id` | 访客或 `agent` | 会话详情 |
| `POST` | `/api/chat/v1/conversations/:id/messages` | 访客或 `agent` | 发消息（`client_msg_id` 幂等） |
| `GET` | `/api/chat/v1/conversations/:id/messages` | 访客或 `agent` | 消息列表（支持 `limit/before_id`） |
| `POST` | `/api/chat/v1/conversations/:id/read` | 访客或 `agent` | 标记已读（需 `last_read_message_id`） |
| `POST` | `/api/chat/v1/conversations/:id/claim` | `agent` | 认领会话 |
| `POST` | `/api/chat/v1/conversations/:id/transfer` | `agent` | 发起转接（需 `to_agent_id`） |
| `POST` | `/api/chat/v1/conversations/:id/transfer/confirm` | `agent` | 接收方确认转接 |
| `POST` | `/api/chat/v1/conversations/:id/transfer/reject` | `agent` | 接收方拒绝转接 |
| `POST` | `/api/chat/v1/conversations/:id/close` | `agent` | 关闭会话 |

## Auth API
| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| `POST` | `/api/auth/v1/auth/login` | 无 | 员工登录，返回 JWT |
| `GET` | `/api/auth/v1/auth/me` | Bearer Token | 读取当前 token 对应身份 |

## Admin API
| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| `POST` | `/api/admin/v1/admin/sites` | `admin/super_admin` | 创建站点 |
| `GET` | `/api/admin/v1/admin/sites` | `admin/super_admin` | 站点列表 |
| `PATCH` | `/api/admin/v1/admin/sites/:site_id/status` | `admin/super_admin`（下游可收紧） | 更新站点状态 |
| `POST` | `/api/admin/v1/admin/sites/:site_id/rotate-widget-key` | `admin/super_admin`（下游可收紧） | 轮换站点 `widget_key` |
| `POST` | `/api/admin/v1/admin/agents` | `admin/super_admin`（下游可收紧） | 创建客服账号 |
| `GET` | `/api/admin/v1/admin/agents` | `admin/super_admin` | 客服列表 |
| `PATCH` | `/api/admin/v1/admin/agents/:id/status` | `admin/super_admin`（下游可收紧） | 更新客服状态 |
| `POST` | `/api/admin/v1/admin/agents/:id/reset-password` | `admin/super_admin`（下游可收紧） | 重置客服密码 |
| `POST` | `/api/admin/v1/admin/agents/:id/force-logout` | `admin/super_admin`（下游可收紧） | 强制下线客服 |
| `GET` | `/api/admin/v1/admin/audit-logs` | `admin/super_admin` | 审计日志查询 |

## 分页与限制约定
- 列表接口默认：`limit=50`、`offset=0`。
- 统一上限：`limit <= 200`。
- 消息内容上限：`2000` 个字符。

## 速率限制约定
- 网关默认启用登录、访客、客服、管理多维限流。
- 超限返回：`HTTP 429`，`error.code=rate_limited`。
