# InlineChat

InlineChat 是一个即插即用的在线实时客服系统。业务站点只需引入一段 JS，即可弹出聊天窗口；客服和管理员通过后台完成会话接待、站点管理、账号管理与 AI 配置。

## 核心能力

- 访客匿名接入，支持 Widget 嵌入与独立调试页
- WebSocket 实时消息链路，断线自动重连与轮询兜底
- 客服工作台，支持会话认领、转接、关闭、快捷语和 AI 协作
- 管理后台，支持站点管理、客服账号管理、审计与 AI 配置
- AI 客服支持站点知识库、向量检索 + rerank、仅未分配会话自动回复
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
- `ai-service`：站点级 AI 配置、知识库索引、RAG 检索与自动回复

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

如需启用 AI，`.env.example` 默认使用宿主机上的 `Hermes + llama-server Embedding + llama-server Reranker`。

### 2. 启动本机 AI 服务（可选，`.env.example` 默认值）

当前默认组合：

- Chat：Hermes，监听 `http://0.0.0.0:8642/v1`
- Embedding：`Qwen3-Embedding-0.6B`，监听 `http://0.0.0.0:8298/v1`
- Reranker：`Qwen3-Reranker-0.6B-Q4_K_M`，监听 `http://0.0.0.0:8299`
- 向量库：Qdrant，由 `docker compose` 内置启动

启动 Embedding 服务：

```bash
llama-server \
  -m "/Users/yqw/.cache/huggingface/hub/models--Qwen--Qwen3-Embedding-0.6B-GGUF/snapshots/370f27d7550e0def9b39c1f16d3fbaa13aa67728/Qwen3-Embedding-0.6B-Q8_0.gguf" \
  -a "Qwen3-Embedding-0.6B" \
  --host 0.0.0.0 \
  --port 8298 \
  --embeddings \
  --pooling last \
  -ngl 0 \
  -np 1 \
  -c 4096
```

启动 Reranker 服务：

```bash
llama-server \
  -hf giladgd/Qwen3-Reranker-0.6B-GGUF:Q4_K_M \
  -a "Qwen3-Reranker-0.6B-Q4_K_M" \
  --host 0.0.0.0 \
  --port 8299 \
  --rerank \
  -ngl all \
  -c 2048
```

`.env.example` 中与之对应的配置如下：

```env
AI_CHAT_BASE_URL=http://host.docker.internal:8642/v1
AI_CHAT_MODEL=hermes-agent
AI_CHAT_API_KEY=inlinechat-local-hermes-20260416
AI_EMBEDDING_BASE_URL=http://host.docker.internal:8298/v1
AI_EMBEDDING_MODEL=Qwen3-Embedding-0.6B
AI_RERANKER_BASE_URL=http://host.docker.internal:8299
AI_QDRANT_URL=http://qdrant:6333
```

说明：

- `AI_CHAT_*` 是当前 `ai-service` 实际使用的聊天模型配置；代码仍兼容旧名 `AI_LLM_*`
- `AI_CHAT_API_KEY` 需要与本机 Hermes 服务配置保持一致
- Hermes 需提供 OpenAI-compatible 的 `/v1/models` 与 `/v1/chat/completions`
- Embedding 服务需提供 `/v1/embeddings`，Reranker 服务需提供 `/rerank`（可选 `/health`）
- `host.docker.internal` 用于让容器内的 `ai-service` 访问宿主机上的 Hermes / `llama-server`
- `qdrant` 由本项目 `docker compose` 启动，无需额外手动拉起

### 2.1 AI 客服实际工作方式

当前代码里的 AI 客服逻辑是：

- 仅对 `open` 且尚未分配人工坐席的会话生效
- 仅处理访客消息
- 站点 AI 配置需显式开启
- 当前唯一回复模式为 `unassigned_auto_reply`
- 先检索站点知识库，优先做 FAQ / fact 直答，不足时再调用聊天模型生成
- 找不到依据时返回兜底文案，不会在无知识依据时自由编造

对应代码链路位于：

- `services/ai-service/internal/service/auto_reply_service.go`
- `services/ai-service/internal/knowledgebase/manager.go`
- `services/admin-service/internal/service/admin_service.go`

### 3. 启动服务

推荐直接使用 `Makefile`：

```bash
make up
```

默认行为：

```bash
make up        # 自动判断数据库升级或回退重建，再构建并启动
make up-strict # 在 make up 基础上额外执行 schema-check
```

说明：

- `make up` 会先把数据库自动同步到当前仓库里的 migrations
- 只要检测到需要迁移或回退重建，都会先自动备份当前业务数据库
- 当代码前进导致 migrations 版本变大时，会自动执行向前迁移
- 当代码回退导致数据库版本高于当前 migrations 时，会按当前 migrations 重建数据库，并按同名表/同名列自动回填数据
- 自动备份文件默认保存在 `output/db-backups/`

### 4. 验证服务

```bash
curl http://localhost:8200/healthz
curl http://localhost:8200/readyz
```

### 5. 访问入口

- 员工登录页：`http://localhost:8200/app/staff-login/`
- 客服工作台：`http://localhost:8200/app/agent/`
- 管理后台：`http://localhost:8200/app/admin/`
- 访客调试页：`http://localhost:8200/app/customer/`
- Widget 聊天页：`http://localhost:8200/app/widget/`
- 示例站点：`http://localhost:8200/app/demo/`
- Swagger UI：`http://localhost:8200/app/api-docs/`
- Widget SDK：`http://localhost:8200/sdk/inlinechat-widget.js`

### 6. 启用站点 AI 客服

1. 创建站点，例如 `site_test`
2. 在本地知识库目录写入站点资料：

```text
data/knowledgebases/<site_id>/knowledge.md
```

示例：

```text
data/knowledgebases/site_test/knowledge.md
```

3. 启动全部服务后，进入管理后台 `http://localhost:8200/app/admin/`
4. 在 “AI 控制台” 中选择站点，打开 AI 开关
5. 回复模式保持为 `仅未分配会话自动回复`
6. 点击 “重建知识库索引”，等待状态变为 `ready`
7. 用访客端或 Widget 发起新会话，在无人认领时即可看到 AI 自动回复

说明：

- 知识库目录会被挂载进 `ai-service` 容器：`/app/data/knowledgebases`
- 管理后台会展示 `knowledge_dir`、`index_status`、`indexed_chunks`、`active_job_id`
- 若人工认领会话，AI 自动回复会立即停止

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
make up-strict      # 严格校验 schema 后启动
make db-backup      # 手动备份当前业务数据库
make db-sync        # 自动同步数据库到当前 migrations
make down           # 停止并清理容器
make logs           # 查看日志
make ps             # 查看容器状态
make prepare-db     # 同步数据库并执行 schema-check
make migrate        # 仅执行向前迁移
make lint           # Go 静态检查
make test           # 后端测试
make quality        # 质量门禁
make proto-check    # 校验 proto 生成代码与仓库一致
make smoke          # 冒烟回归
make integration    # 系统集成回归
make full-regression # 全功能回归
make e2e-ui         # 前端 Playwright E2E
make verify-all     # CI 全量验收入口（本地需显式开启）
```

## 测试与验证

- 后端质量检查：`make quality`
- 前端 E2E：`make e2e-ui`
- 冒烟回归：`make smoke`
- 系统集成回归：`make integration`
- 全功能回归：`make full-regression`
- 全量回归：`VERIFY_ALLOW_LOCAL=1 make verify-all`

首次运行前端 E2E 时，需要先安装依赖与浏览器：

```bash
npm --prefix tests/e2e ci
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
