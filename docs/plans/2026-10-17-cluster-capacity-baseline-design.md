# 集群容量基线设计

## 1. 目标

在单集群详情中增加只读的容量视图，将可调度容量拆分为平台固定开销、业务申请、Pending 需求、安全预留和剩余容量。管理员可以把当前平台资源配置锁定为交付基线，后续直接识别固定开销漂移。

## 2. 口径

- 总量使用所有可调度节点的 `status.allocatable`，不使用物理 `capacity`。
- 平台资源由统一的系统命名空间白名单识别；当前默认 `kube-system,cmb-monitoring`，部署时可通过 `SYSTEM_NAMESPACES` 覆盖。
- 非系统命名空间归为业务资源。
- 已调度且未终止 Pod 的有效 requests/limits 进入当前占用；未调度 Pending Pod 单独作为待满足需求展示，避免把尚未占用节点的需求算成已分配。
- CPU 与内存始终独立计算。Limits 只表示超配风险，不参与剩余可调度容量计算。
- 实际使用通过集群内 Prometheus Service Proxy 查询；不可用时容量申请数据仍可展示。

## 3. 基线

每次“锁定当前为基线”创建一个不可变版本，保存：采集时间、系统命名空间、Worker 数量、CPU/内存 Allocatable、平台 requests 和 limits。页面默认与最新版本比较。

漂移状态：

- `not_set`：尚未建立基线；
- `stable`：平台 CPU 与内存 requests 相对基线变化均不超过 5%，且 Allocatable 变化不超过 5%；
- `drift`：平台 requests 任一变化超过 5%；
- `rebaseline`：Allocatable 任一变化超过 5%，提示扩缩容后重新建立基线。

## 4. 页面

集群详情新增“容量”页签：

1. 顶部显示基线状态、采集时间和系统命名空间；
2. CPU、内存分别显示平台 requests、业务 requests、安全预留和剩余容量；
3. 显示平台/业务当前实际使用以及 requests 使用效率；
4. 显示与交付基线的差异；
5. 按命名空间列出 requests、limits 和 Pod 数，支持从最大占用开始排查。

## 5. 数据与失败处理

Kubernetes API 是 Allocatable 和资源声明的事实来源；Prometheus 是实际使用的事实来源；SQLite 只保存小体量基线版本，不复制时序数据。Prometheus 不可用时返回 `metrics_available=false`，Kubernetes API 失败时整个容量请求失败并给出可重试提示。

