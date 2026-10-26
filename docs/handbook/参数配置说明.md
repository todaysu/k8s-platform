# FTC 集群运维平台参数说明

## 调参顺序

调整前先观察 API 延迟、429、Collector 采集年龄、目标 API Server 429 和 MySQL 连接等待。优先收窄页面查询和放宽采集周期，最后才增加并发。更改 shard 数不是普通调参，必须执行停机迁移。

## 基础与数据库

| 参数 | 默认值 | 说明 | 调整建议 |
| --- | --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | HTTP 监听地址 | 通常不改，由 Service 暴露 |
| `METRICS_ADDR` | 空 | Prometheus 指标地址 | 生产设为独立内网端口，不对公网开放 |
| `PLATFORM_DATABASE_DSN` | 空 | MySQL DSN | 60+ 集群必须配置 |
| `DATABASE_PATH` | `./data/platform.db` | SQLite 文件 | 仅用于开发和单进程测试 |
| `CREDENTIAL_ENCRYPTION_KEY` | 无 | 凭据加密密钥 | 使用 32 字节强随机值的 Base64，安全备份 |
| `SECURE_COOKIES` | 生产 true | Cookie Secure 标记 | HTTPS 环境不得关闭 |
| `SESSION_TTL` | `8h` | 会话有效期 | 缩短会提高安全性，但增加登录频率 |

## 采集参数

| 参数 | 默认值 | 60+ 集群建议 | 影响 |
| --- | --- | --- | --- |
| `HEALTH_COLLECTION_INTERVAL` | `1m` | `1m` 起步 | 越短越新鲜，对目标 API 压力越高 |
| `HEALTH_COLLECTION_TIMEOUT` | `20s` | `20s` | 不应超过轮次间隔 |
| `HEALTH_COLLECTION_WORKERS` | `5` | `5` | 增加前先检查 API Server 429 和中央 CPU |
| `HEALTH_INSTANCE_SOURCE` | `pods` | `workloads` | `workloads` 避免每分钟列出全部业务 Pod |
| `CAPACITY_COLLECTION_INTERVAL` | `5m` | `5m` | 容量变化通常无需分钟级 |
| `CAPACITY_COLLECTION_TIMEOUT` | `45s` | `45s` | 大集群可依实测调整 |
| `CAPACITY_COLLECTION_WORKERS` | `3` | `3` | 容量列表开销大于健康快照 |
| `RESOURCE_INDEX_INTERVAL` | `10m` | `10m` | 越短写放大越明显 |
| `RESOURCE_INDEX_TIMEOUT` | `30s` | 先实测 | 每个集群索引的执行超时 |
| `RESOURCE_INDEX_WORKERS` | `2` | `2` | 受共享大 List 配额约束 |

## 运行角色和分片

| 参数 | 可选值 | 说明 |
| --- | --- | --- |
| `PLATFORM_ROLE` | `all api collector worker` | 本地可用 `all`；生产 60+ 集群使用独立 Deployment |
| `COLLECTION_SHARDS` | `0` 或固定正整数 | `0` 是旧的单 Leader 模式；生产建议首次设为 `16` |
| `LEADER_ELECTION_ENABLED` | `true false` | 旧模式采集器选主；分片模式依赖 MySQL 租约 |

`COLLECTION_SHARDS` 首次启用后会持久化。修改时必须停止全部 Collector、备份 MySQL、执行明确的映射迁移并同步更新所有副本；不得直接改回 0。

## API 准入参数

| 参数 | 默认值 | 通道 | 调整条件 |
| --- | ---: | --- | --- |
| `HTTP_QUERY_CONCURRENCY` | 64 | 快照和数据库查询 | 依 API CPU、MySQL 连接等待和 P95 |
| `HTTP_REALTIME_CONCURRENCY` | 20 | 节点、Pod、工作负载、日志和刷新 | 依目标 API Server 429 和网络 RTT |
| `HTTP_MONITORING_CONCURRENCY` | 8 | Prometheus 面板 | 大序列响应时优先减少面板或时间范围 |
| `HTTP_SESSION_CONCURRENCY` | 10 | Exec kubectl TTY Arthas | 按同时值班人员和长连接资源定义 |
| `HTTP_CLUSTER_CONCURRENCY` | 2 | 单一目标集群上限 | 不建议为解决用户重复点击而调高 |

上述值为每进程上限，MySQL 配额还会在 API 副本之间控制实时、监控和会话通道。扩容 API 副本时必须重新计算总数据库连接和远端请求预算。

## 安全扫描

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `SECURITY_SCAN_WORKERS` | 2 | 同时扫描的集群数 |
| `SECURITY_SCAN_TIMEOUT` | `30m` | 单次扫描总超时 |
| `SECURITY_POD_STARTUP_TIMEOUT` | `5m` | kube-bench Job 进入 Running 的等待上限 |
| `SECURITY_KUBE_BENCH_IMAGE` | `aquasec/kube-bench:v0.16.0` | 默认多架构镜像 |
| `SECURITY_KUBE_BENCH_IMAGE_AMD64` | 空 | 内网 amd64 镜像 | 设置后优先于默认镜像 |
| `SECURITY_KUBE_BENCH_IMAGE_ARM64` | 空 | 内网 arm64 镜像 | 设置后优先于默认镜像 |
| `SECURITY_HOST_SCAN_WORKERS` | 5 | 单集群内节点扫描并发 |

## 推荐调参流程

1. 保存变更前后 30 分钟的平台、MySQL 和目标 API Server 指标。
2. 一次只改一类参数，变化不超过 25%。
3. 同时观察新鲜度、P95 P99、429、错误率、MySQL 等待和目标 API Server 429。
4. 若改善一项但破坏另一项 SLO，回滚并优先改查询或采集策略。
5. 在升级清单中记录参数、理由、实测结果和回滚值。
