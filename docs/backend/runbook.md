# 后端运维 Runbook（MVP）

## 目标
- 在本地和预发布环境快速定位“不可用、消息异常、鉴权异常、限流异常”。
- 提供统一排障路径，减少口口相传的隐性知识。

## 快速体检
1. 检查进程存活：
   - `curl -sS http://127.0.0.1:8200/healthz`
2. 检查链路就绪：
   - `curl -sS http://127.0.0.1:8200/readyz`
3. 观察容器状态与日志：
   - `make ps`
   - `make logs`

## 常见故障场景

### 1) 网关 `readyz` 失败
现象：
- `GET /readyz` 返回 `503`，并带 `errors.chat_grpc / auth_grpc / admin_grpc / realtime_http`。

排查步骤：
1. `make ps` 确认对应服务容器是否运行。
2. 查看故障服务日志：`docker compose -f infra/docker/docker-compose.yml logs <service-name>`
3. 检查 `ETCD_ENDPOINTS` 与服务注册配置：
   - `DISCOVERY_PREFIX`
   - `*_SERVICE_NAME`
   - `*_SERVICE_ADVERTISE_*_ENDPOINT`

### 2) 创建匿名会话失败（403/409）
现象：
- `POST /api/chat/v1/conversations` 返回 `widget session is required` / `invalid widget session`
- 或返回 `site is not active`

排查步骤：
1. 确认请求经过 `gateway-service`，不是直接调用 `chat-service` 的调试路由。
2. 确认调用方已先访问 `/app/widget/?site_id=...&parent_origin=...`，并携带 `X-InlineChat-Widget-Session`。
3. 检查管理后台对应 `site_id` 是否存在且状态为 `active`。
4. 检查 `parent_origin`、`Referer`、`Origin` 是否匹配站点域名。

### 3) WebSocket 连接失败（401/403/409）
现象：
- 访客连接报 `visitor_token is required` 或 `invalid visitor_token`。
- 客服连接报 `invalid access_token` / `agent role required`。
- 也可能报 `site is unavailable` / `site is not active`。

排查步骤：
1. 确认客户端连接的是网关入口 `GET /ws/:conversation_id`，而不是绕过网关直连其它端口。
2. 访客端确认 URL 包含 `visitor_token`，且与会话绑定值一致。
3. 客服端确认 URL 包含 `access_token`，token 未过期且角色为 `agent`。
4. 检查 `WS_ALLOWED_ORIGINS` 是否覆盖当前页面 Origin。
5. 若提示站点不可用，检查 `admin-service` 可用性及站点状态。

### 4) 发消息后没有实时推送
现象：
- HTTP / WS 发消息成功，但对端 WebSocket 收不到 `message.new`。

排查步骤：
1. 确认消息发送方和接收方都已连接到同一个 `conversation_id`，且连接未被服务端主动关闭。
2. 检查 `chat-service` 是否成功发布消息事件（查看日志）。
3. 若开启 outbox，检查 `OutboxDispatcher` 是否存在重试/死信日志。
4. 检查 `realtime-service` Redis 订阅循环是否有错误重试日志。
5. 检查 Redis 连通性与配置：`REDIS_ADDR`、`REDIS_PASSWORD`、`REDIS_DB`。
6. 若消息状态始终停留 `sent`，重点看对端连接是否在线。

### 5) API 频繁返回 429
现象：
- 返回 `error.code=rate_limited`。

排查步骤：
1. 检查限流阈值配置：
   - `LOGIN_RATE_LIMIT_*`
   - `VISITOR_RATE_LIMIT_*`
   - `AGENT_RATE_LIMIT_*`
   - `ADMIN_RATE_LIMIT_*`
2. 若启用了 Redis 分布式限流，检查：
   - `RATE_LIMIT_REDIS_ADDR` 可达
   - `RATE_LIMIT_REDIS_TIMEOUT_MS`
   - 是否出现熔断降级日志（fallback to local limiter）

### 6) MySQL/Redis 异常导致服务不就绪
现象：
- `auth/admin/chat/realtime` 的 `readyz` 报依赖不可达。

排查步骤：
1. 检查依赖容器状态：
   - `mysql` / `redis` / `etcd`
2. 校验环境变量：
   - `MYSQL_DSN`
   - `REDIS_ADDR`
   - `ETCD_ENDPOINTS`
3. 必要时重建环境：
   - `make down`
   - `make up`

## 回归验证
- 最小链路验证：
  - `make smoke`
- 集成验证：
  - `make integration`
- 全量验证：
  - `make verify-all`

## 回滚建议
- 代码回滚后至少执行：
  - `make smoke`
- 若涉及协议变更（HTTP/WS/gRPC），建议追加：
  - `make integration`
