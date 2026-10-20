# Kubernetes 多集群管理平台

一个参考 CNCF Headlamp 技术架构、聚焦集群稳定性的轻量多集群管理平台。

当前正在实现第一个纵向功能：使用集群名称和上传的 Kubeconfig 纳管已有 Kubernetes 集群。

## 技术栈

- Go 后端
- React + TypeScript 前端
- PatternFly 6（ACM 风格）
- Kubernetes `client-go` v0.32.x
- SQLite（本地开发）
- MySQL 8.4（生产环境统一存储）

## 当前开发状态

项目已包含真实集群纳管、健康概览、全局资源池容量、单集群容量基线、节点、工作负载、Pod 详情与日志，以及单集群 Event 中心。Event 由平台通过 Kubernetes API `List + Watch` 非侵入式采集，按 UID 更新重复次数；Warning 保留 30 天，Normal 保留 7 天，小时聚合保留 365 天；24 小时以上窗口优先读取按天/对象/类别生成的日聚合。

平台最终以 Web 服务形式部署到 Kubernetes，不开发桌面端，也不要求用户单独运行客户端程序。

### 跨集群资源索引

平台在自身数据库中维护 `cluster_resources` 摘要表，由 Leader 每 10 分钟刷新一套集群。浏览器搜索只查询该索引，不再访问目标集群；索引完全为空时才回退到一次受控的实时加载。可通过 `RESOURCE_INDEX_INTERVAL`、`RESOURCE_INDEX_TIMEOUT` 和 `RESOURCE_INDEX_WORKERS` 调整刷新周期、单集群超时和并发。

### 多集群健康快照

集群列表和全局概览读取平台数据库中的最近健康快照，不会因页面刷新而同时扫描所有集群。后台采集器默认每 60 秒错峰刷新一轮，最多同时检查 5 套集群，单集群检查超时为 20 秒；某套集群失败只会将该集群标记为连接失败，并保留上一次成功指标及检查时间。

可通过 `HEALTH_COLLECTION_INTERVAL`、`HEALTH_COLLECTION_WORKERS` 和 `HEALTH_COLLECTION_TIMEOUT` 调整采集周期、并发上限和单集群超时。设置 `HEALTH_INSTANCE_SOURCE=workloads` 后，实例数来自 Deployment/StatefulSet/DaemonSet 状态，健康采集只列 kube-system Pod，不再每分钟全量读取业务 Pod；`deploy/platform.yaml` 默认启用该模式。60 套集群建议先保持 `1m / 5 / 20s`，经过压测后再调高并发。

集群概览与容量默认读取采集快照，只有显式刷新才实时访问目标集群；节点、工作负载、Pod 日志和 Exec 仍按需实时访问。跨集群事件的问题与对象视图在数据库内完成聚合，只有单个资源的证据查询才读取原始 Event。跨集群搜索读取平台持久化资源索引，由 Leader 每 10 分钟刷新一轮，浏览器搜索不再访问目标集群；索引为空时回退到受控的实时加载。

当前 SQLite 方案面向单副本第一阶段；生产多副本前需迁移到共享 MySQL，并为后台采集器增加 Leader Election。详细决策见 [`docs/adr/0002-cache-fleet-health-snapshots.md`](docs/adr/0002-cache-fleet-health-snapshots.md)。

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

`make dev-backend` 仅在本地开发时默认开启 Pod Exec。如需关闭，使用 `ENABLE_POD_EXEC=false make dev-backend`。直接运行 `go run ./cmd/platform` 时 Pod Exec 仍默认关闭。

不设置 `PLATFORM_DATABASE_DSN` 时全部数据保存到本地 SQLite，便于开发验证。部署到 Kubernetes 时使用统一 MySQL：

```yaml
env:
  - name: PLATFORM_DATABASE_DSN
    valueFrom:
      secretKeyRef: { name: ftc-platform, key: mysql-dsn }
```

配置 MySQL 后，平台启动时自动创建集群、健康、容量、事件、账号和会话所需的全部表及索引；多个平台 Pod 通过数据库迁移锁和 Kubernetes Lease 安全启动。部署示例见 [`deploy/platform.yaml`](deploy/platform.yaml) 与 [`deploy/mysql.yaml`](deploy/mysql.yaml)。

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

Kubernetes 内部署 MySQL、平台数据库接入和 Pod Exec 开启方法见 [`docs/deployment/kubernetes.md`](docs/deployment/kubernetes.md)。

设置 `METRICS_ADDR`（例如 `:9090`）后，平台在独立端口暴露 Prometheus 文本指标：采集轮次/错误/耗时、事件 List/Watch 重连、API 请求量与 5xx、托管集群数。该端口不经过登录认证，生产环境只应通过内网 Service 或 NetworkPolicy 暴露。
