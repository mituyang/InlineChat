# InlineChat CI/CD 使用说明

## 目标
- `CI` 负责基础回归，确保 `main` 直推模式下仍有最小质量检查。
- `CD` 负责镜像构建、发布动作编排和回滚输入，保证版本可追踪、可回滚。

## CI（已启用）
- 工作流：`.github/workflows/ci.yml`
- 关键检查：
  - `test-gate`：`make test`
  - 触发方式：`push main` 与手动触发（`workflow_dispatch`）

## Security（本次补充）
- 工作流：`.github/workflows/security.yml`
- 关键检查：
  - `govulncheck`：对全部 Go module 执行漏洞扫描（每月 + 手动触发）

## CD（本次新增）
- 工作流：`.github/workflows/cd.yml`

### 自动触发
- `push main` 时自动执行 `build-and-push-images`：
  - 先 `make build-local` 产出二进制
  - 构建并推送 5 个服务镜像到 GHCR：
    - `chat-service`
    - `auth-service`
    - `admin-service`
    - `realtime-service`
    - `gateway-service`
  - 镜像标签默认：`sha-<12位commit>`，并额外打 `main`

### 手动触发（workflow_dispatch）
- 输入参数：
  - `release_action`: `deploy | rollback`
  - `environment`: `staging | production`
  - `image_tag`: 指定要部署/回滚的镜像标签
  - `build_images`: `deploy` 时是否先构建并推送镜像

### Webhook 集成（可选）
- 若配置仓库 Secret，workflow 会自动调用外部部署系统：
  - `DEPLOY_WEBHOOK_URL`
  - `DEPLOY_WEBHOOK_TOKEN`（可选）
- 若未配置，会在 Job Summary 输出手动发布指引，不会静默失败。

## 分支保护清单（已补充）
- 详细配置见：`docs/branch-protection-checklist.md`
- 核心原则：
  - 单人开发时仅保留“防误操作”规则（禁止 force-push、禁止删除 `main`）
  - 如后续变为多人协作，再切回严格模式（PR 审批 + 必选检查）

## 协作治理（本次补充）
- `CODEOWNERS`：`.github/CODEOWNERS`
  - 将 `services/`、`packages/`、`infra/`、`.github/`、`docs/` 绑定责任人，配合分支保护可启用 Code Owner 强审。
- PR 模板：`.github/pull_request_template.md`
  - 统一记录变更范围、风险、验证清单，降低评审信息缺失。
- Issue 模板：`.github/ISSUE_TEMPLATE/`
  - 分离 Bug 与 Feature 两类输入，提升问题收敛效率。
- Dependabot：
  - 单人开发模式默认关闭自动依赖 PR（如需恢复，可重新添加 `.github/dependabot.yml`）。

## 回滚约定
- 回滚时使用 `release_action=rollback`，并明确 `image_tag`。
- 目标镜像会先在 GHCR 做存在性校验（`docker manifest inspect`）。
- 回滚后建议立刻执行一轮 `smoke` 验证核心链路。
