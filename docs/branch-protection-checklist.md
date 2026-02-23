# InlineChat 分支保护清单（main）

## 目标
- 在不同协作规模下，使用合适强度的分支保护。
- 单人开发优先“防误操作”，多人协作再切换到“强门禁”。

## 推荐配置入口
- GitHub 仓库路径：`Settings -> Branches -> Branch protection rules`
- 规则分支：`main`

## 当前模式（单人开发，轻量）
1. `Require a pull request before merging`：关闭
2. `Require status checks to pass before merging`：关闭
3. `Require conversation resolution before merging`：关闭
4. `Do not allow bypassing the above settings`：关闭
5. `Allow force pushes`：关闭
6. `Allow deletions`：关闭

说明：
- 该模式允许直接 push 到 `main`，减少流程摩擦。
- 仍保留“禁止 force-push / 禁止删除分支”，避免误操作。

## 团队模式（多人协作，严格）
1. `Require a pull request before merging`：开启
2. `Require approvals`：至少 `1`
3. `Dismiss stale pull request approvals when new commits are pushed`：开启
4. `Require review from Code Owners`：建议开启
5. `Require conversation resolution before merging`：开启
6. `Require status checks to pass before merging`：开启
7. Required checks：
   - `required-gate`（或当前 CI 聚合门禁）
   - `dependency-review`（或当前安全门禁）
8. `Require branches to be up to date before merging`：开启
9. `Do not allow bypassing the above settings`：开启
10. `Allow force pushes`：关闭
11. `Allow deletions`：关闭

## 何时从轻量切换到严格
- 开始多人协作（>=2 人并行开发）
- 需要合规审计或外部交付验收
- 线上事故需要更强变更约束
