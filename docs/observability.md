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
- `infra/monitoring/alertmanager.yml`：告警路由基础配置
- `infra/monitoring/grafana-datasource.yml`：Grafana 默认 Prometheus 数据源

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
当前 `alertmanager.yml` 只保留了最小路由（receiver=`default`）。  
生产环境建议接入企业通知通道（Webhook、PagerDuty、飞书/钉钉机器人等）并按 `severity` 分级。

## 下一步建议
- 增加业务指标：
  - outbox dead 数量
  - 限流降级次数（Redis 熔断/本地回退）
  - WebSocket 在线连接数与广播失败计数
- 增加数据库与缓存 exporter：
  - MySQL exporter（连接池、慢查询、锁等待）
  - Redis exporter（内存、命中率、连接数）
