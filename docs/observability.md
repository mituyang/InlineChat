# InlineChat 可观测性与告警草案

## 目标
- 统一采集 5 个核心服务的 `/metrics` 指标。
- 补齐 `readyz` 探测告警，第一时间感知依赖故障。
- 提供最小可用告警规则（可按业务再细化）。

## 文件结构
- `infra/docker/docker-compose.monitoring.yml`：Prometheus / Alertmanager / Blackbox / Grafana 叠加编排
- `infra/monitoring/prometheus.yml`：Prometheus 抓取配置
- `infra/monitoring/alert_rules.yml`：告警规则
- `infra/monitoring/blackbox.yml`：HTTP 探测模块
- `infra/monitoring/alertmanager.yml`：告警路由基础配置（本地最小可用）
- `infra/monitoring/alertmanager.channels.example.yml`：告警通知通道模板（按 severity 分路）
- `infra/monitoring/grafana-datasource.yml`：Grafana 默认 Prometheus 数据源
- `infra/monitoring/grafana-dashboards.yml`：Grafana dashboard provider
- `infra/monitoring/dashboards/inlinechat-overview.json`：默认总览面板模板

## 启动方式
基于现有 compose 叠加启动监控组件：

```bash
docker compose \
  -f infra/docker/docker-compose.yml \
  -f infra/docker/docker-compose.monitoring.yml \
  --env-file .env \
  up -d
```

常用入口：
- Prometheus: `http://localhost:${PROMETHEUS_HOST_PORT:-9090}`
- Alertmanager: `http://localhost:${ALERTMANAGER_HOST_PORT:-9093}`
- Grafana: `http://localhost:${GRAFANA_HOST_PORT:-3000}`
- Blackbox Exporter: `http://localhost:${BLACKBOX_EXPORTER_HOST_PORT:-9115}`

## 当前告警覆盖
- 可用性：
  - `InlineChatMetricsEndpointDown`
  - `InlineChatReadyzProbeFailed`
- 网关稳定性：
  - `InlineChatGateway5xxRatioHigh`
  - `InlineChatGatewayP95LatencyHigh`
  - `InlineChatGatewayInflightHigh`
- 核心服务错误率：
  - `InlineChatChatService5xxRatioHigh`
  - `InlineChatAuthService5xxRatioHigh`
  - `InlineChatAdminService5xxRatioHigh`
  - `InlineChatRealtimeService5xxRatioHigh`

## 告警通知建议
当前 `alertmanager.yml` 保持本地最小配置。  
若要接入企业通知通道（Webhook、Email、PagerDuty、飞书/钉钉机器人），按下面步骤启用模板：

1. 复制模板文件：
   - `cp infra/monitoring/alertmanager.channels.example.yml infra/monitoring/alertmanager.yml`
2. 修改占位配置：
   - `smtp_*`：SMTP 网关与鉴权
   - `webhook_configs.url`：告警机器人或告警平台地址
   - `email_configs.to`：oncall 邮件组
3. 重启告警组件：
   - `make monitoring-down && make monitoring-up`

模板默认已支持：
- `critical` 与 `warning` 按 `severity` 分路
- `critical` 可同时走 webhook + email
- 抑制规则（同一告警 `critical` 触发时抑制对应 `warning`）

## Grafana 面板模板
- Grafana 启动后会自动加载 `InlineChat Overview`（UID：`inlinechat-overview`）。
- 面板覆盖：
  - 各服务请求速率（RPS）
  - 各服务 5xx 比例
  - gateway P95 延迟
  - 各服务 inflight 请求数
  - `readyz` 成功率
  - firing 告警统计与告警明细

## 下一步建议
- 增加业务指标：
  - outbox dead 数量
  - 限流降级次数（Redis 熔断/本地回退）
  - WebSocket 在线连接数与广播失败计数
- 增加数据库与缓存 exporter：
  - MySQL exporter（连接池、慢查询、锁等待）
  - Redis exporter（内存、命中率、连接数）
