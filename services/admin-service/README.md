# admin-service

## 服务职责
- 管理站点（site）与客服账号（agent）。
- 提供审计日志查询。
- 处理高风险管理动作（状态变更、密码重置、强制下线等）。
- 通过 gRPC 向网关提供管理域能力，并向 `realtime-service` 提供站点查询能力。

## 端口与探针
- 默认 HTTP 端口：`8204`（`HTTP_PORT`）
- 默认 gRPC 端口：`8214`（`GRPC_PORT`）
- 健康检查：`GET /healthz`
- 就绪检查：`GET /readyz`（检查 MySQL）
- 指标：`GET /metrics`

## 对外能力
- gRPC：
  - `AdminGatewayService`（管理域写操作 + `GetSiteBySiteID` / `GetSiteByDomain` 查询）
- HTTP（服务内调试入口，前缀 `/v1/admin`）：
  - 站点管理：创建、列表、状态变更、轮换 `widget_key`
  - 客服管理：创建、列表、状态变更、重置密码、强制下线
  - 审计日志：查询

## 依赖关系
- 必需依赖：
  - `MySQL`
  - `etcd`（注册 gRPC 地址）

## 关键环境变量
| 变量名 | 默认值 | 必填 | 说明 |
| --- | --- | --- | --- |
| `HTTP_PORT` | `8204` | 否 | HTTP 端口 |
| `GRPC_PORT` | `8214` | 否 | gRPC 端口 |
| `MYSQL_DSN` | - | 是 | MySQL 连接串 |
| `MYSQL_MAX_OPEN_CONNS` | `80` | 否 | 连接池上限 |
| `MYSQL_MAX_IDLE_CONNS` | `20` | 否 | 空闲连接数 |
| `MYSQL_QUERY_TIMEOUT_MS` | `1500` | 否 | 查询超时 |
| `JWT_SECRET` | - | 是 | JWT 主密钥 |
| `JWT_PREVIOUS_SECRET` | 空 | 否 | JWT 旧密钥（轮换窗口） |
| `JWT_ISSUER` | `inlinechat-auth` | 否 | JWT 发行者 |
| `BCRYPT_COST` | `12` | 否 | 密码哈希强度（10-14） |
| `ETCD_ENDPOINTS` | - | 是 | etcd 地址列表 |
| `DISCOVERY_PREFIX` | `/inlinechat/services` | 否 | 服务发现前缀 |
| `SERVICE_NAME` | `admin-service` | 否 | 注册服务名 |
| `SERVICE_ADVERTISE_GRPC_ENDPOINT` | - | 是 | 注册到 etcd 的 gRPC 地址 |

完整字段见：`services/admin-service/internal/config/config.go`

## 本地运行
```bash
go run ./services/admin-service/cmd/server
```

## 权限说明
- 管理入口要求 `admin/super_admin` 鉴权。
- 部分敏感动作（如创建客服、状态变更、重置密码）由服务内进一步收敛到 `super_admin`。
