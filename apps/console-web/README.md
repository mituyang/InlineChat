# console-web

`Vue3 + Vite` 的客服台/管理台实现。

当前目标：
- 提供 `agent`、`admin` 两个独立入口。
- 复用共享的鉴权、API、主题和基础布局。
- 直接作为新的客服台/管理台实现，替换旧静态页。

## 本地运行

1. 安装依赖

```bash
npm --prefix apps/console-web install
```

2. 启动开发服务器

```bash
npm --prefix apps/console-web run dev
```

默认通过 Vite 代理转发到 `http://127.0.0.1:8200`。

## 构建

```bash
npm --prefix apps/console-web run build
```

构建产物输出到：

- `apps/console-web/dist/agent`
- `apps/console-web/dist/admin`

## Docker / 网关访问

`gateway-service` Docker 镜像会在构建阶段自动执行 `console-web` 的前端构建，并把产物复制到：

- `/app/public/agent`
- `/app/public/admin`

部署后可通过以下地址访问：

- `http://localhost:8200/app/agent/`
- `http://localhost:8200/app/admin/`
- `http://localhost:8200/app/agent-vue`
- `http://localhost:8200/app/admin-vue`
