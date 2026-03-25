# InlineChat CI/CD 使用说明

## 目标
- `CI` 在 `push main` 时执行全量验证，失败立即红灯。
- `CD` 只在 `CI` 成功后才构建镜像，阻断带病构建。
- 手动发布/回滚能力保留，用于环境操作与应急恢复。

## CI（全量门禁）
- 工作流：`.github/workflows/ci.yml`
- 触发方式：
  - `pull_request -> main`
  - `push main`
  - `workflow_dispatch`
- 核心步骤：
  - 检出代码
  - 安装 Go
  - 安装 `protoc` 与 Go gRPC 插件
  - `cp .env.example .env`
  - 执行 `make config`
  - 执行 `make env-lint`
  - 执行 `make proto-check`
  - 安装 Node
  - 使用 `npm ci` 安装 `tests/e2e` 依赖（命中 lockfile + cache）
  - 安装 Playwright Chromium
  - 执行 `make verify-all`
- 前置门禁：
  - `make config`
  - `make env-lint`
  - `make proto-check`
- `make verify-all` 覆盖：
  - `env-lint`
  - `quality`
  - `smoke`
  - `integration`
  - `full-regression`
  - `e2e-ui`（Playwright 5 场景）
- 前端 E2E 关键变量：
  - `E2E_BASE_URL`（默认 `http://127.0.0.1:8200`）
  - `E2E_SUPER_ADMIN_EMAIL` / `E2E_SUPER_ADMIN_PASSWORD`（默认回退 `.env` 中超级管理员账号）
- 失败产物：
  - Playwright 报告与测试结果
  - 关键容器日志

## CD（受 CI 成功门禁）
- 工作流：`.github/workflows/cd.yml`
- 自动触发：
  - 监听 `CI` 的 `workflow_run`
  - 仅当 `conclusion == success`
  - 且该次 `CI` 来自 `main`
  - 才执行自动镜像构建
- 自动构建行为：
  - `build-and-push-images` 构建并推送 6 个服务镜像到 GHCR
  - `workflow_run` 场景下：
    - checkout 使用 `github.event.workflow_run.head_sha`
    - 默认标签使用该 `head_sha` 生成 `sha-<12位commit>`
    - 仅 `main` 分支额外推送 `main` 标签
- 当前纳入 CD 的服务：
  - `chat-service`
  - `auth-service`
  - `admin-service`
  - `realtime-service`
  - `gateway-service`
  - `ai-service`
- 手动触发（`workflow_dispatch`）保留：
  - `release_action`: `deploy | rollback`
  - `environment`: `staging | production`
  - `image_tag`: 指定镜像标签
  - `build_images`: deploy 时是否先构建镜像

## Security
- 工作流：`.github/workflows/security.yml`
- `govulncheck`：扫描全部 Go module 已知漏洞（每月 + 手动）

## 分支保护清单
- 配置参考：`docs/branch-protection-checklist.md`
- 单人开发默认：
  - 禁止 force-push
  - 禁止删除 `main`

## 协作治理
- `CODEOWNERS`：`.github/CODEOWNERS`
- PR 模板：`.github/pull_request_template.md`
- Issue 模板：`.github/ISSUE_TEMPLATE/`
- Dependabot：单人开发默认关闭自动依赖 PR

## 回滚约定
- 回滚使用 `workflow_dispatch` + `release_action=rollback`
- `image_tag` 必填
- CD 会先做 GHCR 镜像存在性校验（`docker manifest inspect`）
- 回滚后建议执行一轮 `make smoke` 验证核心链路
