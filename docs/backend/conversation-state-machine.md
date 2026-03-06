# 会话与消息状态机

## 会话状态字段
- `status`：`open` / `closed`
- `assigned_agent_id`：当前接待客服
- `pending_transfer_to_agent_id`：待确认转接目标客服
- `pending_transfer_from_agent_id`：转接发起方客服
- `pending_transfer_requested_at`：转接申请时间

## 会话状态模型
- `open_unassigned`
  - `status=open`
  - `assigned_agent_id` 为空
  - 无 pending transfer 字段
- `open_assigned`
  - `status=open`
  - `assigned_agent_id` 非空
  - 无 pending transfer 字段
- `open_transfer_pending`
  - `status=open`
  - `assigned_agent_id` 非空
  - `pending_transfer_to_agent_id` 非空
- `closed`
  - `status=closed`
  - `closed_at` 非空

## 会话状态迁移
| 触发动作 | 触发方 | 前置条件 | 状态变化 | 关键副作用 |
| --- | --- | --- | --- | --- |
| 创建会话 `CreateConversation` | 访客 | `site_id` 有效，`visitor_token` 非空 | `none -> open_unassigned` | 若存在同站点同访客 `open` 会话则复用 |
| 认领会话 `ClaimConversation` | `agent` | 会话 `open` 且未被认领 | `open_unassigned -> open_assigned` | 清理 pending transfer 字段 |
| 发起转接 `TransferConversation` | 当前接待客服或管理员 | 会话 `open_assigned` 且无 pending transfer | `open_assigned -> open_transfer_pending` | 发送系统消息“等待确认” |
| 确认转接 `ConfirmTransferConversation` | 目标客服或管理员 | 会话为 `open_transfer_pending` | `open_transfer_pending -> open_assigned` | 更新 `assigned_agent_id`，发送系统消息“转接成功” |
| 拒绝转接 `RejectTransferConversation` | 目标客服或管理员 | 会话为 `open_transfer_pending` | `open_transfer_pending -> open_assigned` | 清理 pending transfer，发送系统消息“拒绝转接” |
| 关闭会话 `CloseConversation` | 接待客服或管理员 | 会话未关闭 | `open_* -> closed` | 发布 `conversation.closed` |
| 自动关闭 `AutoCloseInactiveConversations` | 系统定时任务 | 会话最后一条非系统消息来自 `agent` 且超过阈值 | `open_* -> closed` | 发布 `conversation.closed` |

## 会话权限规则
- 访客只能访问/操作自己 `visitor_token` 绑定的会话。
- 客服访问会话时：
  - 会话未分配：允许查看，不允许以客服身份发消息。
  - 会话已分配给他人：仅当自己是 `pending_transfer_to_agent_id` 时可参与确认/拒绝。
- 普通客服关闭会话时，必须是当前接待客服；管理员可越权关闭。

## 消息状态机
- `sent`：消息已持久化。
- `read`：客户端显式调用 `mark read` 推进。

状态流转：
- `sent -> read`
- 允许幂等重复推进：
  - `MarkMessagesRead` 在无可更新行时返回 `updated_count=0`。

## 幂等与一致性规则
- `client_msg_id` 在同一会话内唯一，用于消息写入幂等。
- 会话创建使用 `(site_id, visitor_token)` 维度锁避免并发重复建会话。
- Outbox 开启时，消息事件与业务写操作在同事务中落库；异步派发到 Redis。
