# InlineChat CI/CD 使用说明

## 目标
- `CI` 负责 PR 门禁，阻止不满足质量标准的变更进入 `main`。
- `CD` 负责镜像构建、发布动作编排和回滚输入，保证版本可追踪、可回滚。

## CI（已启用）
- 工作流：`.github/workflows/ci.yml`
- 关键检查：
  - `quality-gate`：`make quality`
  - `smoke-gate`：`make up && make smoke`
  - `required-gate`：PR 汇总门禁（建议设置为分支保护必选）
  - `integration-main`：`main` 合并后执行系统集成回归

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
  - 所有变更通过 PR 合并，不允许直接推送到 `main`
  - 必选检查只保留稳定聚合门禁 `required-gate`
  - 强制评审与对话解决，避免“带风险合并”

## 回滚约定
- 回滚时使用 `release_action=rollback`，并明确 `image_tag`。
- 目标镜像会先在 GHCR 做存在性校验（`docker manifest inspect`）。
- 回滚后建议立刻执行一轮 `smoke` 验证核心链路。
