# Kubernetes 多集群管理平台

一个参考 CNCF Headlamp 技术架构、聚焦集群稳定性的轻量多集群管理平台。

当前正在实现第一个纵向功能：使用集群名称和 Token 类型的 Kubeconfig 纳管已有 Kubernetes 集群。

## 技术栈

- Go 后端
- React + TypeScript 前端
- Material UI
- Kubernetes `client-go`（后续任务接入）
- SQLite（本地开发）

## 当前开发状态

项目目前包含后端健康检查接口和集群管理 UI 原型。UI 使用模拟数据，尚不能连接 Kubernetes 集群。

平台最终以 Web 服务形式部署到 Kubernetes，不开发桌面端，也不要求用户单独运行客户端程序。

## 本地工具

当前开发机使用 Homebrew 安装的 macOS ARM 版 Go。若终端优先找到旧的 `/usr/local/go/bin/go`，先执行：

```bash
export PATH="/opt/homebrew/bin:$PATH"
```

运行基础验证：

```bash
make test
cd web && npm run build && npm run lint
```

启动后端前，需要提供开发用配置：

```bash
export ADMIN_PASSWORD_HASH="仅限本地学习的占位值"
export CREDENTIAL_ENCRYPTION_KEY="仅限本地学习的占位值"
make dev-backend
```

后端启动后，可访问健康检查接口：

```text
GET http://127.0.0.1:8080/api/v1/health
GET http://127.0.0.1:8080/api/v1/version
```

启动前端：

```bash
make dev-frontend
```

整体产品与架构基线见 [`docs/product/2026-10-15-multi-cluster-operations-platform-requirements.md`](docs/product/2026-10-15-multi-cluster-operations-platform-requirements.md)。

历史模块设计与开发步骤位于 [`docs/plans`](docs/plans)。
