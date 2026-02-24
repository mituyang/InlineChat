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

### 2) WebSocket 连接失败（401/403）
现象：
- 访客连接报 `visitor_token is required` 或 `invalid visitor_token`。
- 客服连接报 `invalid access_token` / `agent role required`。

排查步骤：
1. 访客端确认 URL 包含 `visitor_token`，且与会话绑定值一致。
2. 客服端确认 URL 包含 `access_token`，token 未过期且角色为 `agent`。
3. 检查 `WS_ALLOWED_ORIGINS` 是否覆盖当前页面 Origin。

### 3) 发消息后没有实时推送
现象：
- HTTP 发消息成功，但对端 WebSocket 收不到 `message.new`。

排查步骤：
1. 检查 `chat-service` 是否成功发布消息事件（查看日志）。
2. 检查 `realtime-service` Redis 订阅循环是否有错误重试日志。
3. 检查 Redis 连通性与配置：`REDIS_ADDR`、`REDIS_PASSWORD`、`REDIS_DB`。
4. 若消息状态始终停留 `sent`，重点看对端连接是否在线。

### 4) API 频繁返回 429
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

### 5) MySQL/Redis 异常导致服务不就绪
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
