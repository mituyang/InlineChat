# InlineChat

InlineChat 是一个即插即用的在线实时客服系统。业务站点只需引入一段 JS，即可弹出聊天窗口；客服和管理员通过后台完成会话接待、站点管理、账号管理与 AI 配置。

## 核心能力

- 访客匿名接入，支持 Widget 嵌入与独立调试页
- WebSocket 实时消息链路，断线自动重连与轮询兜底
- 客服工作台，支持会话认领、转接、关闭、快捷语和 AI 协作
- 管理后台，支持站点管理、客服账号管理、审计与 AI 配置
- 微服务后端拆分，便于独立扩展与部署
- Docker Compose 一键启动本地完整环境

## 技术栈

- 后端：Go 1.25、Gin、gRPC、GORM、MySQL、Redis、WebSocket、etcd、Zap
- 前端：Vue 3、Vite
- 基础设施：Docker Compose、Prometheus、Grafana、Alertmanager

## 服务架构

- `gateway-service`：网关入口、静态资源托管、鉴权透传、路由聚合
- `auth-service`：员工登录、JWT、超级管理员初始化
- `chat-service`：会话、消息、状态流转
- `realtime-service`：WebSocket 实时链路
- `admin-service`：站点、客服账号、后台管理接口
- `ai-service`：AI 配置、知识库重载、辅助回复与摘要能力

## 目录结构

```text
.
├── apps/
│   ├── api-docs/          # Swagger UI
│   ├── console-web/       # Vue3 客服台 / 管理台
│   ├── customer-console/  # 访客调试页
│   ├── demo-site/         # 嵌入客服的示例业务站点
│   ├── staff-login/       # 员工统一登录页
│   ├── widget-chat/       # Widget iframe 聊天页
│   └── widget-sdk/        # 嵌入式 SDK
├── docs/                  # 架构、协议、运维文档
├── infra/                 # Docker 与监控配置
├── packages/              # 共享包
├── services/              # Go 微服务
└── tests/                 # 集成测试与 E2E
```

## 快速开始

### 1. 准备环境

```bash
cp .env.example .env
```

至少补全以下配置：

- `SUPER_ADMIN_EMAIL`
- `SUPER_ADMIN_PASSWORD`
- `SUPER_ADMIN_DISPLAY_NAME`

如需启用 AI，还需要在 `.env` 中配置模型与向量服务相关参数。

### 2. 启动服务

推荐直接使用 `Makefile`：

```bash
make up
```

等价命令：

```bash
docker compose -f infra/docker/docker-compose.yml --env-file .env up --build
```

### 3. 验证服务

```bash
curl http://localhost:8200/healthz
curl http://localhost:8200/readyz
```

### 4. 访问入口

- 员工登录页：`http://localhost:8200/app/staff-login/`
- 客服工作台：`http://localhost:8200/app/agent/`
- 管理后台：`http://localhost:8200/app/admin/`
- 访客调试页：`http://localhost:8200/app/customer/`
- Widget 聊天页：`http://localhost:8200/app/widget/`
- 示例站点：`http://localhost:8200/app/demo/`
- Swagger UI：`http://localhost:8200/app/api-docs/`
- Widget SDK：`http://localhost:8200/sdk/inlinechat-widget.js`

## Widget 接入

在业务页面中插入：

```html
<script
  src="http://localhost:8200/sdk/inlinechat-widget.js"
  data-site-id="site_demo"
  data-title="在线客服"
  defer
></script>
```

说明：

- `data-site-id` 必须与管理后台中的站点 `site_id` 一致
- 脚本会在页面右下角渲染悬浮入口，点击后打开聊天窗口
- 聊天窗口内部通过 WebSocket 连接实时通道，并带 HTTP 轮询兜底

## 前端开发

客服台与管理台位于 `apps/console-web`：

```bash
npm --prefix apps/console-web install
npm --prefix apps/console-web run dev
npm --prefix apps/console-web run build
```

说明：

- Docker 构建 `gateway-service` 镜像时会自动打包 `apps/console-web`
- 如果本地直接运行 `gateway-service`，访问 `/app/agent/` 和 `/app/admin/` 前先执行一次 `npm --prefix apps/console-web run build`

## 常用命令

```bash
make up             # 启动全部服务
make down           # 停止并清理容器
make logs           # 查看日志
make ps             # 查看容器状态
make migrate        # 执行全部数据库迁移
make lint           # Go 静态检查
make test           # 后端测试
make quality        # 质量门禁
make e2e-ui         # 前端 Playwright E2E
make verify-all     # 一键全量验证
```

## 测试与验证

- 后端质量检查：`make quality`
- 前端 E2E：`make e2e-ui`
- 全量回归：`make verify-all`

首次运行前端 E2E 时，需要先安装依赖与浏览器：

```bash
npm --prefix tests/e2e install
npx --prefix tests/e2e playwright install --with-deps chromium
```

## 相关文档

- 架构说明：`docs/architecture.md`
- 后端文档索引：`docs/backend/README.md`
- HTTP API：`docs/backend/http-api.md`
- WebSocket 协议：`docs/backend/ws-protocol.md`
- OpenAPI：`docs/backend/openapi.yaml`
- 可观测性：`docs/observability.md`
- CI/CD：`docs/cicd.md`
- 生产化路线图：`docs/production-roadmap.md`
