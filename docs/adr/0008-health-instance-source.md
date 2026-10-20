# ADR-0008：健康采集支持工作负载实例口径

- 日期：2026-10-20
- 状态：已接受

## 背景

健康采集原先每套集群每分钟 `List Pods` 全量，200 节点集群约 6,000 个 Pod。60 套集群即每分钟 36 万个对象，是平台最大的重复读取来源。

## 决策

1. 新增配置 `HEALTH_INSTANCE_SOURCE`，取值 `pods`（默认）或 `workloads`。
2. `workloads` 模式下，实例总数/就绪数来自 Deployment、StatefulSet、DaemonSet 的期望副本和 Ready 副本；核心组件只列 `kube-system` 的 Pod。
3. 节点容量、Prometheus 使用率、Worker 核数和 API 延迟的采集逻辑不变。
4. `deploy/platform.yaml` 默认使用 `workloads`；需要 Pod 级精确实例口径时改回 `pods`。

## 后果

### 正面

- 健康轮次不再全量读取业务 Pod，60 套集群每分钟减少约 36 万个对象。
- 工作负载对象数量通常比 Pod 少一个数量级，API Server 和平台解码压力显著下降。

### 负面

- 裸 Pod、Job、CronJob 产生的实例不再计入健康实例数。
- “实例”口径从 Pod 数变为工作负载期望副本数，与容量页的 Pod 口径略有差异。
- DaemonSet 使用 `desiredNumberScheduled/numberReady`，与 Pod 计数在节点滚动时可能短暂不同。
