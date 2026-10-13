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

项目目前只有后端配置加载和前端基础骨架，尚不能连接 Kubernetes 集群。

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

详细设计与开发步骤位于 [`docs/plans`](docs/plans)。
