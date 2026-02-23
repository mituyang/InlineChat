# InlineChat 生产化路线图

本文档用于把项目从当前 Demo 形态，持续收敛为可上线、可运维、可扩展的生产级系统。

## 目标
- 可用性：关键链路可观测、可恢复、可回滚。
- 安全性：默认安全、最小权限、可审计。
- 稳定性：高并发与故障场景下可控降级。
- 工程性：发布流程、测试门禁、配置治理标准化。

## 已完成（第一阶段基线）
- 全服务新增 `readyz` 就绪探针（依赖探活）。
- 全服务 HTTP Server 增加读写与空闲超时。
- 全服务统一注入基础安全响应头。
- `docker-compose` 业务服务启用 `readyz` healthcheck。
- 关键依赖条件从 `service_started` 升级为 `service_healthy`。
- CI/CD 基线完善：`quality + smoke + required-gate + integration-main + CD`。
- 可观测性基线完善：Prometheus/Alertmanager/Grafana/Blackbox 叠加编排与默认告警规则。
- 工程治理基线补齐：`CODEOWNERS`、PR/Issue 模板、Dependabot、Security workflow（dependency-review + govulncheck）。

## 下一阶段（高优先级）
1. 安全与访问控制
- 网关登录接口限流（防撞库/暴力破解）。
- 访客接口按 `site_id` + `visitor_token` 做速率限制。
- JWT 轮换策略（双密钥窗口）与失效策略（黑名单/短时 token）。
- 敏感配置改为 Secret 管理（至少支持 docker secret / env 文件分级）。

2. 可观测性
- 引入 Prometheus 指标（QPS、延迟、错误率、WS 在线数、消息堆积）。
- 增加结构化审计日志（登录、建会话、认领、转接、关闭）。
- 统一告警规则（5xx 比例、readyz 失败、outbox 重试飙升）。

3. 数据与一致性
- 为核心表补齐唯一索引/组合索引回顾与压测验证。
- Outbox 增加死信/人工补偿通道。
- 关键操作补充幂等键与冲突错误码规范。

4. 发布与运维
- 增加 `staging -> production` 双环境流水线。
- 镜像签名与 SBOM（供应链安全）。
- 滚动发布与回滚脚本（零停机升级）。

5. 测试体系
- 补充契约测试（gateway 与下游 gRPC 协议兼容）。
- 增加关键路径压测脚本（会话列表、消息发送、WS 广播）。
- 引入故障注入测试（redis/mysql/etcd 短暂不可用）。

## 验收标准（生产就绪）
- `P95 API latency`、`Error Rate`、`Readyz SLO` 有明确阈值与报警。
- 版本发布可追踪（变更单、镜像、迁移、回滚点完整）。
- 核心业务链路具备自动化回归与压测报告。
- 安全检查项（认证、限流、输入校验、依赖漏洞）有固定流水线门禁。
