# InlineChat 分支保护清单（main）

## 目标
- 保证所有代码通过 PR 合并，阻止未过门禁的变更直接进入 `main`。
- 将质量门禁与评审门禁固化为仓库规则，减少“人为放行”。

## 推荐配置入口
- GitHub 仓库路径：`Settings -> Branches -> Branch protection rules`
- 规则分支：`main`

## P0 必选项
1. `Require a pull request before merging`：开启
2. `Require approvals`：至少 `1`
3. `Dismiss stale pull request approvals when new commits are pushed`：开启
4. `Require review from Code Owners`：按团队情况决定（若已维护 `CODEOWNERS`，建议开启）
5. `Require conversation resolution before merging`：开启
6. `Require status checks to pass before merging`：开启
7. Required checks 仅保留稳定聚合门禁：
   - `required-gate`
8. `Require branches to be up to date before merging`：开启
9. `Do not allow bypassing the above settings`：开启
10. `Allow force pushes`：关闭
11. `Allow deletions`：关闭

## 推荐项（P1）
1. `Require signed commits`：开启（团队已完成签名流程后）
2. `Require linear history`：按团队合并策略选择（若只允许 squash/rebase，建议开启）
3. `Restrict who can push to matching branches`：开启并只保留发布机器人或管理员
4. `Lock branch`：仅在发布冻结窗口开启

## 合并策略建议
1. 默认使用 `Squash and merge`，保持主干历史整洁。
2. 若需要保留提交语义，可补充允许 `Rebase and merge`。
3. 不建议默认开启 `Merge commit`，避免主干历史噪声过高。

## 自检清单（合并前）
- PR 状态为 `required-gate` 通过。
- 至少 1 名 reviewer 已批准。
- 所有 review comment 已处理并标记 resolved。
- 无 `force-push` / 无绕过规则合并。
- 合并后 `main` 上 `integration-main` 结果正常（用于合并后回归观察）。
