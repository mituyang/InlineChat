# 网关错误码约定

本文档描述 `gateway-service` 对外 HTTP 错误码约定。  
下游服务（直接访问 `chat/auth/admin` 的 `/v1/*`）可能返回简化格式 `{"error":"..."}`，不在本页范围内。

## 统一错误响应
```json
{
  "error": {
    "code": "invalid_argument",
    "message": "invalid limit"
  },
  "request_id": "f57f0c6b9d6f4d6f"
}
```

## 错误码映射
| HTTP Status | `error.code` | 说明 |
| --- | --- | --- |
| `400` | `invalid_argument` | 参数校验失败、字段非法、必填缺失 |
| `401` | `unauthorized` | 认证失败、token 缺失或无效 |
| `403` | `forbidden` | 权限不足、越权访问 |
| `404` | `not_found` | 资源不存在 |
| `404` | `route_not_found` | 路由不存在（NoRoute） |
| `405` | `method_not_allowed` | 请求方法不允许（NoMethod） |
| `409` | `conflict` | 业务冲突（如站点状态冲突） |
| `409` | `already_exists` | 唯一键冲突等“已存在”语义 |
| `409` | `failed_precondition` | 前置条件不满足（如状态机条件不满足） |
| `429` | `rate_limited` | 触发网关限流 |
| `500` | `internal_error` | 网关或下游返回未知内部错误 |
| `502` | `upstream_unavailable` | 下游服务不可用或不可达 |
| `504` | `upstream_timeout` | 下游调用超时 |

## gRPC 到 HTTP 的映射
- `codes.InvalidArgument -> 400 invalid_argument`
- `codes.NotFound -> 404 not_found`
- `codes.AlreadyExists -> 409 already_exists`
- `codes.Unauthenticated -> 401 unauthorized`
- `codes.PermissionDenied -> 403 forbidden`
- `codes.FailedPrecondition -> 409 failed_precondition`
- `codes.Unavailable -> 502 upstream_unavailable`
- `codes.DeadlineExceeded -> 504 upstream_timeout`
- 其他未枚举 gRPC 错误默认映射 `500 internal_error`

## 使用建议
- 前端逻辑优先基于 `error.code` 分支，不依赖 `message` 文案。
- 链路排查优先使用 `request_id` 关联网关和下游日志。
