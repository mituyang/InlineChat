# auth-service

## 服务职责
- 员工登录鉴权（客服/管理员）。
- 签发并校验 JWT。
- 启动时确保 `super_admin` 账号存在（来自环境变量）。
- 通过 gRPC 向网关和实时服务提供身份能力。

## 端口与探针
- 默认 HTTP 端口：`8201`（`HTTP_PORT`）
- 默认 gRPC 端口：`8211`（`GRPC_PORT`）
- 健康检查：`GET /healthz`
- 就绪检查：`GET /readyz`（检查 MySQL）
- 指标：`GET /metrics`

## 对外能力
- gRPC：
  - `AuthGatewayService`（`Login`、`Me`）
- HTTP（服务内调试入口，前缀 `/v1`）：
  - `POST /v1/auth/login`
  - `GET /v1/auth/me`

## 依赖关系
- 必需依赖：
  - `MySQL`
  - `etcd`（注册 gRPC 地址）

## 关键环境变量
| 变量名 | 默认值 | 必填 | 说明 |
| --- | --- | --- | --- |
| `HTTP_PORT` | `8201` | 否 | HTTP 端口 |
| `GRPC_PORT` | `8211` | 否 | gRPC 端口 |
| `MYSQL_DSN` | - | 是 | MySQL 连接串 |
| `MYSQL_MAX_OPEN_CONNS` | `80` | 否 | 连接池上限 |
| `MYSQL_MAX_IDLE_CONNS` | `20` | 否 | 空闲连接数 |
| `MYSQL_QUERY_TIMEOUT_MS` | `1500` | 否 | 查询超时 |
| `JWT_SECRET` | - | 是 | JWT 主密钥 |
| `JWT_PREVIOUS_SECRET` | 空 | 否 | JWT 旧密钥（轮换窗口） |
| `JWT_ISSUER` | `inlinechat-auth` | 否 | JWT 发行者 |
| `JWT_EXPIRE` | `12h` | 否 | JWT 过期时长 |
| `BCRYPT_COST` | `12` | 否 | 密码哈希强度（10-14） |
| `SUPER_ADMIN_EMAIL` | - | 是 | 超级管理员邮箱 |
| `SUPER_ADMIN_PASSWORD` | - | 是 | 超级管理员密码 |
| `SUPER_ADMIN_DISPLAY_NAME` | - | 是 | 超级管理员显示名 |
| `ETCD_ENDPOINTS` | - | 是 | etcd 地址列表 |
| `DISCOVERY_PREFIX` | `/inlinechat/services` | 否 | 服务发现前缀 |
| `SERVICE_NAME` | `auth-service` | 否 | 注册服务名 |
| `SERVICE_ADVERTISE_GRPC_ENDPOINT` | - | 是 | 注册到 etcd 的 gRPC 地址 |

完整字段见：`services/auth-service/internal/config/config.go`

## 本地运行
```bash
go run ./services/auth-service/cmd/server
```

## 安全建议
- 生产环境务必设置高强度 `JWT_SECRET`。
- 密钥轮换时使用 `JWT_SECRET + JWT_PREVIOUS_SECRET` 双窗口。
- `SUPER_ADMIN_PASSWORD` 必须满足强口令策略。
