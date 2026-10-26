# FTC 集群运维平台架构与技术设计

## 文档目的

本文档面向平台架构师、交付人员和运维管理员，说明 FTC 平台如何纳管 Kubernetes 集群、如何在 60 套以上集群规模下控制访问压力，以及各技术组件的边界。平台参考 Red Hat Advanced Cluster Management 的“中央视图、集群生命周期、可观测性、治理”信息架构，但保持无 Agent、轻量、聚焦稳定性的产品边界。

## 设计结论

FTC 是一个以共享 MySQL 为状态中心的模块化单体，通过三种运行角色实现资源隔离：API 服务页面和按需实时操作，Collector 负责持续采集，Worker 负责安全扫描和报告等耗时任务。页面的跨集群读路径优先读数据库快照和索引，不在浏览器请求中同步扫描全部集群。当查看节点、Pod、日志、终端或监控面板时，才对单一目标集群执行有界实时访问。

## 高层架构

```text
运维浏览器
      |
      v
API 副本 x2  ---- 身份认证 RBAC ---- 请求准入与跨副本配额
      |                                  |
      |                                  +---- 单集群实时 Kubernetes Prometheus
      v
MySQL 共享平台库
  |       |       |       |       |
  |       |       |       |       +---- 报告、安全扫描任务
  |       |       |       +------------ 资源索引
  |       |       +-------------------- Event 与日聚合
  |       +---------------------------- 容量快照
  +------------------------------------ 健康和告警快照
      ^
      |
Collector 副本 x2 ---- 16 固定 shard lease epoch fencing
      |
      +---- 60+ 套被纳管 Kubernetes API Alertmanager Prometheus

Worker 副本 x1 ---- 持久化任务队列 ---- kube-bench Job 与报告生成
```

## 组件与语言

| 层次 | 技术 | 责任 | 规模化措施 |
| --- | --- | --- | --- |
| Web | React 19 TypeScript PatternFly 6 | ACM 风格导航、多集群页面和操作界面 | 页面分页、请求取消、定时轮询受控 |
| API | Go 1.27 `net/http` | REST WebSocket 身份认证、RBAC、输入校验 | 查询、实时、监控、会话四通道准入 |
| Kubernetes 连接 | client-go 0.32 | List Watch Exec Logs | 分页 500、8 MiB 单页、25000 单类资源上限 |
| 数据层 | MySQL 8.4 | 生产共享状态、任务和索引 | 连接池隔离、集合聚合、事务 fencing |
| 本地数据层 | SQLite | 单进程开发和功能测试 | 不用于多副本和容量验收 |
| 交付 | Docker Kubernetes YAML | 单清单安装、离线镜像 | amd64 arm64 独立镜像归档 |

## 核心调用逻辑

1. 集群健康、容量、资源索引、Event 和告警由 Collector 错峰更新。
2. 多集群页面从 MySQL 读取最近成功快照，并显示采集时间、过期和失败；失败不会被当作零。
3. 用户显式刷新或打开单集群资源时，API 在进程内和 MySQL 共享配额中取得槽位后才访问目标。
4. 长时间安全扫描和报告生成先入库，由 Worker 执行，不占用 API 请求生命周期。
5. Collector 用固定 shard 和租约分配所有权，epoch 阻止旧 owner 的迟到写入。

## 60 套以上集群的发布基线

- 生产必须使用 MySQL，并将 `PLATFORM_ROLE` 拆分为 API、Collector 和 Worker。
- 初始建议为 API 2 副本、Collector 2 副本、Worker 1 副本、16 个 shard。
- 启用 `HEALTH_INSTANCE_SOURCE=workloads`，避免健康轮询全量业务 Pod。
- 默认保持健康 5 并发、容量 3 并发、索引 2 并发，不因集群数增长直接线性放大。
- 发布前必须补充 60 集群长稳、10% 离线、MySQL 慢查询和不可用、Collector kill 与 10 用户浏览器混合负载验收。

## 故障语义

| 故障 | 用户可见结果 | 保护方式 |
| --- | --- | --- |
| 单集群离线 | 显示连接失败和最后成功值 | 超时与错峰采集，不阻塞其他集群 |
| 采集失败 | 快照标记 stale，不发布部分索引 | 完整批次成功后才替换 |
| API 压力过高 | 429 并带 `Retry-After` | 有界准入，不进入无界队列 |
| Collector 退出 | 租约到期后其他副本接管 | shard lease 和 epoch fencing |
| MySQL 故障 | 读写失败或服务降级 | 有界 context、任务持久化、恢复后受控补采 |

## 安全设计

- 会话 Cookie 使用 HttpOnly、SameSite Strict，HTTPS 生产模式启用 Secure。
- 所有非 GET 请求校验 Origin，WebSocket 终端也强制同源。
- 状态修改路由必须在权限表声明；启动时检测遗漏。
- 集群凭据和模型密钥加密保存，日志不应输出明文。
- kubectl、Arthas、安全扫描为高权限能力，使用管理员权限和受限并发。

## 与 ACM 的对照与取舍

ACM 使用 hub cluster 和 managed-cluster agent 完成集群生命周期、治理与可观测；FTC 保留中央视图、搜索、健康、告警、安全与钻取体验，但通过 kubeconfig 直连，不安装 managed-cluster agent，不承担集群创建、升级、应用下发和策略编排。

参考：[ACM 2.14 多集群架构](https://docs.redhat.com/en/documentation/red_hat_advanced_cluster_management_for_kubernetes/2.14/html/about/welcome-to-red-hat-advanced-cluster-management-for-kubernetes)、[ACM 2.14 可观测服务](https://docs.redhat.com/en/documentation/red_hat_advanced_cluster_management_for_kubernetes/2.14/html/observability/observing-environments-intro)。
