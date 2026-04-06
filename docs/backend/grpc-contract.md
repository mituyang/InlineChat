# InlineChat gRPC 合约说明

## 目标
- 统一说明当前 gRPC 服务边界、方法语义、错误码与兼容约束。
- 作为网关聚合层和微服务内部调用的“代码同源文档”。

## Proto 文件位置
- `packages/shared-types/proto/inlinechat/chat.proto`
- `packages/shared-types/proto/inlinechat/auth.proto`
- `packages/shared-types/proto/inlinechat/admin.proto`

## 服务矩阵
| 服务 | Proto Service | 主要调用方 | 说明 |
| --- | --- | --- | --- |
| `chat-service` | `ChatGatewayService` | `gateway-service` | 会话与消息主业务 |
| `chat-service` | `ChatInternalService` | `realtime-service` | 实时链路内部写消息 |
| `auth-service` | `AuthGatewayService` | `gateway-service`、`realtime-service` | 登录与 token 身份解析 |
| `admin-service` | `AdminGatewayService` | `gateway-service`、`realtime-service` | 站点、客服与审计管理；站点查询也用于 WS 握手校验 |

## ChatGatewayService
### 方法列表
- `CreateConversation`
- `ListConversations`
- `GetConversation`
- `CreateMessage`
- `ListMessages`
- `MarkMessagesRead`
- `ClaimConversation`
- `TransferConversation`
- `ConfirmTransferConversation`
- `RejectTransferConversation`
- `CloseConversation`

### 关键语义
- `CreateConversation`：
  - `site_id`、`visitor_token` 必填。
  - 同站点同访客存在 open 会话时复用旧会话。
- `CreateMessage`：
  - `sender_type` 允许 `visitor/agent/system`（网关侧仅暴露 `visitor/agent`）。
  - `client_msg_id` 在会话内幂等。
- `MarkMessagesRead`：
  - 需区分 `actor_type=visitor|agent`。
  - visitor 读的是 `agent` 消息，agent 读的是 `visitor` 消息。
- 会话流转：
  - 认领、转接、确认/拒绝、关闭遵循状态机约束。
  - 违反状态约束通常返回 `FailedPrecondition`。

### 常见错误码（服务端映射）
- `InvalidArgument`：字段缺失、字段非法。
- `NotFound`：会话或消息不存在。
- `PermissionDenied`：越权操作、token 与会话不匹配。
- `AlreadyExists`：重复冲突（如已被他人认领、唯一键冲突）。
- `FailedPrecondition`：会话状态不允许当前操作（如已关闭、待转接）。
- `Internal`：未分类内部异常。

## ChatInternalService
### 方法列表
- `CreateMessage`
### 关键语义
- `CreateMessage`：用于实时链路写消息，参数校验与 Gateway 版本一致。

## AuthGatewayService
### 方法列表
- `Login`
- `Me`

### 关键语义
- `Login`：邮箱+密码登录，返回 JWT 与员工信息。
- `Me`：从 `authorization`（Bearer Token）解析身份。

### 常见错误码
- `InvalidArgument`：参数缺失或格式错误。
- `Unauthenticated`：token 缺失或无效、凭证错误。
- `PermissionDenied`：角色不允许。
- `AlreadyExists`：冲突类错误（极少见，保留映射）。
- `Internal`：内部异常。

## AdminGatewayService
### 方法列表
- 站点：
  - `CreateSite` / `UpdateSite` / `ListSites` / `GetSiteBySiteID` / `GetSiteByDomain`
  - `UpdateSiteStatus` / `RotateSiteWidgetKey`
- 客服：
  - `CreateAgent` / `ListAgents`
  - `UpdateAgentStatus` / `ResetAgentPassword` / `ForceAgentLogout`
- 审计：
  - `ListAuditLogs`

### 关键语义
- 所有管理写操作基于 `authorization` 做 JWT 校验。
- 管理入口要求 `admin/super_admin`。
- 高风险动作（如创建客服、状态变更、重置密码、强制下线）要求 `super_admin`。
- `ListSites` / `ListAgents` 默认 `limit=50`，`limit>200` 视为非法。
- `GetSiteBySiteID` / `GetSiteByDomain` 除网关建会话外，也被 `realtime-service` 用于 WS 握手时校验站点是否仍然 `active`。

### 常见错误码
- `Unauthenticated`：缺失或无效 token。
- `PermissionDenied`：角色不足（包括非 `super_admin`）。
- `InvalidArgument`：字段格式或业务参数非法。
- `NotFound`：资源不存在。
- `AlreadyExists`：冲突（唯一键/重复创建等）。
- `Internal`：内部异常。

## 发现与调用超时
- 服务发现基于 `etcd`：
  - `ETCD_ENDPOINTS`
  - `DISCOVERY_PREFIX`
- 网关侧 gRPC 超时：
  - `GRPC_DIAL_TIMEOUT_SEC`
  - `GRPC_CALL_TIMEOUT_SEC`
- 实时侧 gRPC 超时（chat/auth/admin 客户端共享）：
  - `CHAT_GRPC_DIAL_TIMEOUT_SEC`
  - `CHAT_GRPC_CALL_TIMEOUT_SEC`

## 兼容性约束（演进建议）
1. 保持 `field number` 不变，不复用已删除 tag。
2. 新增字段优先采用“可选语义”（不破坏旧客户端）。
3. 避免修改既有字段类型与语义；需要变更时新增字段替代。
4. 错误码应保持稳定；新增错误优先映射到已定义语义集合。
5. 变更 proto 后执行 `make proto`，并同步更新：
   - `docs/backend/http-api.md`（若影响网关行为）
   - `docs/backend/error-codes.md`（若影响错误映射）
   - 本文档（`grpc-contract.md`）
