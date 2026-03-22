# InlineChat 后端文档索引

## 目标
- 为后端开发、联调、排障提供统一入口。
- 将接口、协议、状态机、错误码、运维流程拆分维护，避免单文档膨胀。
- 让“代码实现”和“文档说明”能双向校验。

## 推荐阅读顺序
1. `docs/architecture.md`
2. `services/*/README.md`
3. `docs/backend/http-api.md`
4. `docs/backend/ws-protocol.md`
5. `docs/backend/openapi.yaml`
6. `docs/backend/grpc-contract.md`
7. `docs/backend/conversation-state-machine.md`
8. `docs/backend/error-codes.md`
9. `docs/backend/runbook.md`

## 文档范围
- 本目录聚焦后端服务：`gateway-service`、`chat-service`、`realtime-service`、`auth-service`、`admin-service`。
- 前端 UI/交互实现细节不在本目录展开。

## 在线查看
- Swagger UI 页面：`/app/api-docs/`
- 规范原文：`/docs/backend/openapi.yaml`
- 文档索引原文：`/docs/backend/README.md`

## 维护约定
- 路由/字段/状态流转变更时，必须同步更新对应文档。
- 变更指引：
  - 接口行为变更：更新 `http-api.md` / `ws-protocol.md`
  - 对外契约变更：更新 `openapi.yaml` / `grpc-contract.md`
  - 业务流转变更：更新 `conversation-state-machine.md`
  - 错误返回变更：更新 `error-codes.md`
  - 运维流程变更：更新 `runbook.md`

## 代码事实来源
- HTTP 聚合层：`services/gateway-service/internal/handler`
- Widget 会话与嵌入校验：`services/gateway-service/internal/handler/http_widget.go`
- WebSocket 协议：`services/realtime-service/internal/ws/handler.go`
- 会话/消息规则：`services/chat-service/internal/service/chat_service.go`
