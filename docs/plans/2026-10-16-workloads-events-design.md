# 集群工作负载与事件中心设计基线

**状态：** Proposed（确认后分功能实施）  
**范围：** 单集群详情中的“工作负载”和“事件”，以及 Kubernetes Event 的 30 天持久化  
**延续设计：** ACM 风格的集群详情标题栏与二级标签，不新增另一套导航体系

## 1. 目标与边界

### 1.1 本阶段目标

1. 在单集群详情中增加两个一级标签：`工作负载`、`事件`。
2. 让运维人员从工作负载一路下钻到 Pod、容器、日志和事件，快速回答“哪里异常、受影响范围是什么”。
3. 后台持续采集约 60 套集群的 Kubernetes Event，保存到 MySQL 30 天。
4. 为后续“全局事件中心”“业务跨集群全景”“AI 分析”提供统一数据基础。

### 1.2 第一版不做

- 不做完整 Kubernetes 控制台，不提供任意 YAML 编辑。
- 不允许删除 Deployment、StatefulSet、DaemonSet、Pod 等业务资源。
- 不把 Event 当作告警，也不在本阶段发送通知。
- 不保存 Pod 日志到数据库；日志仍按需从目标集群读取。
- 不直接用 Event 计算集群健康分。Event 是辅助诊断数据，可能重复、延迟或丢失。

## 2. 信息架构与页面路由

单集群详情二级导航调整为：

```text
概览 | 节点 | 工作负载 | 事件
```

建议路由：

| 页面 | 路由 |
|---|---|
| 集群概览 | `/clusters/{clusterId}/overview` |
| 节点 | `/clusters/{clusterId}/nodes` |
| 工作负载列表 | `/clusters/{clusterId}/workloads` |
| 工作负载详情 | `/clusters/{clusterId}/workloads/{kind}/{namespace}/{name}` |
| 集群事件 | `/clusters/{clusterId}/events` |

“业务全景”与“工作负载”不重复：

- **工作负载**以单集群为边界，回答某个集群当前运行了什么、哪些实例异常。
- **业务全景**以业务为边界，将同命名空间/同业务标识的工作负载跨集群聚合。

## 3. 工作负载管理设计

### 3.1 第一版管理范围

第一版以只读诊断为主：

| 资源 | P0 | 展示重点 |
|---|---:|---|
| Deployment | 是 | 期望、Ready、Available、镜像、更新时间 |
| StatefulSet | 是 | 期望、Ready、Current、分区滚动状态 |
| DaemonSet | 是 | Desired、Ready、Available、Misscheduled |
| Pod / 容器 | 是 | Phase、Ready、重启数、节点、Pod IP、异常原因 |
| Job / CronJob | P1 | 成功、失败、最近执行 |
| ReplicaSet | 详情关联 | 不作为主列表入口，只展示 Deployment 下的版本关系 |

### 3.2 工作负载列表页

顶部概览卡：

- 工作负载总数；
- 正常、进行中、异常数量；
- Pod Ready / Total；
- 最近 1 小时 Warning Event 数。

筛选区：

- 命名空间：全部命名空间或单选；
- 类型：Deployment / StatefulSet / DaemonSet；
- 状态：正常 / 进行中 / 异常；
- 名称搜索；
- 自动刷新 30 秒，并保留手动刷新。

列表字段：

- 名称、命名空间、类型；
- 状态；
- Ready / Desired；
- Pod 重启总数；
- 镜像；
- CPU / 内存（Prometheus 可用时显示，不可用时明确显示“指标不可用”）；
- 最近更新时间；
- Warning Event 数；
- 进入详情箭头。

### 3.3 工作负载状态规则

避免只按 Pod Phase 判断，使用控制器自身状态：

- **正常：** observedGeneration 已追上 generation，且 Ready/Available 达到期望值。
- **进行中：** 正在发布或扩缩容，暂未达到期望，但没有明确失败条件。
- **异常：** 超时未达到期望、Deployment ProgressDeadlineExceeded、Pod 持续 Pending/Failed、容器 CrashLoopBackOff/ImagePullBackOff，或不可用副本大于 0 且超过宽限时间。
- **未知：** API 无法读取或数据字段不足；未知不能显示成正常。

### 3.4 工作负载详情页

详情页分为：

1. **概况：** 类型、命名空间、标签、镜像、创建时间、发布状态。
2. **实例：** 该控制器拥有的 Pod 列表，展示容器 Ready、重启数、节点和 IP。
3. **事件：** 按 `regarding.uid` 关联该工作负载及其下属 ReplicaSet/Pod 的最近事件。
4. **日志：** 按 Pod、容器、时间范围读取实时日志；第一版不持久化日志。

### 3.5 操作权限

建议分阶段开放：

- **P0：** 只读查看；容器 Exec 作为单独的高风险能力实现，需要二次确认、会话超时和审计。
- **P1：** 仅为无状态 Deployment 增加“滚动重启”“调整副本数”，每次操作二次确认并写审计日志。
- **暂不开放：** 删除资源、修改镜像、任意 YAML 编辑、强制删除 Pod。

## 4. 事件页面设计

### 4.1 单集群事件页

默认显示最近 24 小时，数据来自平台 MySQL，而不是每次打开页面时直接读取 Kubernetes API。

顶部摘要：

- 最近 1 小时 / 24 小时事件数；
- Warning 数；
- 受影响对象数；
- 事件采集状态及最后成功时间。

筛选条件：

- 时间：1 小时、6 小时、24 小时、7 天、30 天；
- 类型：Warning、Normal、其他；
- 命名空间；
- 对象类型；
- Reason；
- 对象名称或消息关键字。

列表字段：

- 最后发生时间；
- 类型；
- Reason；
- 对象（Kind / Namespace / Name）；
- 消息；
- 次数；
- 首次发生时间；
- 来源组件。

交互规则：

- 默认按最后发生时间倒序；
- 同一个 Kubernetes Event UID 更新同一行的次数与最后时间，不重复插入多行；
- Warning 红/橙色高亮，Normal 使用中性色；
- 点击对象可下钻到对应工作负载、Pod 或节点；
- 点击行打开详情抽屉，显示完整消息、来源、首次/最后时间和关联对象。

### 4.2 全局事件中心（后续迭代）

左侧“事件中心”后续读取同一张事件表，增加集群、环境筛选和跨集群聚合。不再重新建设第二套采集链路。

## 5. 事件采集与持久化架构

```text
┌──────────────────────── 目标 Kubernetes 集群（约 60 套） ────────────────────────┐
│ events.k8s.io/v1 API                                                           │
└───────────────────────────────┬────────────────────────────────────────────────┘
                                │ List + Watch
                                ▼
┌────────────────────── KubeScope 后端 ──────────────────────┐
│ 集群采集协调器                                              │
│  ├─ 每个纳管集群一个 Event Watch Worker                    │
│  ├─ 断线重连 / 指数退避 / 410 Gone 后重新 List             │
│  └─ 规范化、去重、批量 Upsert                               │
└───────────────────────────────┬────────────────────────────┘
                                │
                                ▼
┌────────────────────────────── MySQL ───────────────────────┐
│ cluster_events               event_collector_checkpoints   │
│ 30 天事件数据                 每集群采集状态/最后成功时间     │
└───────────────────────────────┬────────────────────────────┘
                                │ 查询/游标分页
                                ▼
                  单集群事件页 / 全局事件中心 / AI 分析
```

### 5.1 采集流程

1. 平台启动后读取已纳管集群。
2. 每个集群启动一个受控 Worker；先 List 当前事件，再从 List 返回的 `resourceVersion` 开始 Watch。
3. 收到 ADDED / MODIFIED 时规范化并 Upsert；DELETED 不删除历史记录。
4. Watch 正常超时后续接；网络失败使用带抖动的指数退避。
5. 若 API 返回 `410 Gone`，说明旧 `resourceVersion` 已不可用，重新 List 后继续。
6. 集群被解除纳管时停止 Worker；该集群历史事件是否立即删除由产品策略决定，推荐随集群删除以避免孤儿数据。

Kubernetes 的 `resourceVersion` 只能作为续接位置，不是永久游标。数据库唯一键负责最终幂等，避免重连后产生重复行。

### 5.1.1 为什么选择中央 API Watch

| 方案 | 优点 | 问题 | 结论 |
|---|---|---|---|
| 每 30 秒轮询 Event API | 实现最简单 | 重复拉取整批数据，容易漏掉已经过期的事件，60 套集群会产生不必要请求 | 不采用 |
| 中央平台 List + Watch | 不在业务集群安装组件，只接收增量变化 | 平台必须能访问每个 API Server，需要处理断线和 resourceVersion 过期 | **推荐** |
| 每集群部署 Agent / Event Exporter | 可在集群内就近采集 | 需要在约 60 套集群部署、升级和授权，不符合非侵入要求 | 暂不采用 |

这里的“非侵入”是指不在被纳管集群安装工作负载、不修改 kube-apiserver 配置。它并非完全零开销：每套集群仍会存在一条长期 Watch 连接，以及平台重启或重连时的一次 List 请求。因此必须限制并发 List、启用超时与退避，并监控 API Server 返回的 429、403 和 410。

### 5.2 API 兼容策略

- 优先使用 `events.k8s.io/v1`；
- 若目标集群不支持，再回退到 `core/v1 Events` 并映射到统一模型；
- 事件的 `series.count` 与 `series.lastObservedTime` 用于连续重复事件；旧接口使用 `count/firstTimestamp/lastTimestamp` 兼容。

### 5.3 部署副本策略

- P0 平台后端先以 1 个副本运行，避免多个实例重复建立全部 Watch。
- 后续需要高可用时，通过 Kubernetes Lease 做 leader election；只有 Leader 运行采集器，所有副本均可提供只读 API。
- MySQL 唯一键仍作为最后一道重复保护。

## 6. MySQL 数据模型

### 6.1 `cluster_events`

| 字段 | 类型建议 | 说明 |
|---|---|---|
| id | BIGINT UNSIGNED | 平台内部主键 |
| cluster_id | VARCHAR(64) | 纳管集群 ID |
| event_uid | VARCHAR(64) | Kubernetes Event metadata.uid |
| namespace | VARCHAR(253) | 事件命名空间，集群级对象可为空 |
| event_type | VARCHAR(32) | Normal / Warning / 其他 |
| reason | VARCHAR(128) | 事件原因 |
| action | VARCHAR(128) | 执行动作 |
| message | TEXT | events.k8s.io/v1 的 note 或 core/v1 的 message |
| regarding_api_version | VARCHAR(64) | 关联对象 API 版本 |
| regarding_kind | VARCHAR(64) | 关联对象类型 |
| regarding_namespace | VARCHAR(253) | 关联对象命名空间 |
| regarding_name | VARCHAR(253) | 关联对象名称 |
| regarding_uid | VARCHAR(64) | 关联对象 UID，用于稳定关联 |
| related_kind/name/uid | VARCHAR | 可选的次要关联对象 |
| reporting_controller | VARCHAR(253) | 上报控制器 |
| reporting_instance | VARCHAR(253) | 上报实例 |
| occurrence_count | BIGINT UNSIGNED | 累计次数 |
| first_observed_at | DATETIME(6) | 首次观察时间 |
| last_observed_at | DATETIME(6) | 最后观察时间，也是 30 天保留依据 |
| resource_version | VARCHAR(64) | 最近一次处理的资源版本 |
| created_at / updated_at | DATETIME(6) | 平台写入时间 |

关键约束与索引：

```text
UNIQUE (cluster_id, event_uid)
INDEX  (cluster_id, last_observed_at)
INDEX  (cluster_id, namespace, last_observed_at)
INDEX  (cluster_id, event_type, last_observed_at)
INDEX  (cluster_id, regarding_uid, last_observed_at)
INDEX  (last_observed_at)
```

第一版不保存完整原始 JSON，避免无界字段和重复存储；发现确实需要排障字段时再增加受控 JSON 列。

### 6.2 `event_collector_checkpoints`

| 字段 | 说明 |
|---|---|
| cluster_id | 主键 |
| resource_version | 最近观察到的版本，仅用于最佳努力续接 |
| state | running / retrying / forbidden / stopped |
| last_success_at | 最近成功收到或同步数据的时间 |
| last_error | 最近错误摘要，不保存凭据 |
| updated_at | 状态更新时间 |

### 6.3 `event_occurrence_5m`：AI 与趋势分析用的压缩表

不建议把 Event 每一次重复发生都写成一条明细。Kubernetes Event Series 已经提供累计 `count`；平台每次收到更新时计算：

```text
本次新增次数 = 新 count - 数据库中旧 count
```

然后按 5 分钟窗口 Upsert 到压缩表：

| 字段 | 说明 |
|---|---|
| bucket_time | 5 分钟窗口开始时间 |
| cluster_id | 集群 |
| namespace | 命名空间 |
| regarding_uid/kind/name | 受影响对象 |
| event_type/reason | 类型与原因 |
| occurrence_delta | 此窗口新增发生次数 |
| first_seen_at/last_seen_at | 窗口内首末时间 |

唯一键建议为：

```text
(bucket_time, cluster_id, regarding_uid, event_type, reason)
```

这样同时保留两种视角：

- `cluster_events`：用于页面看事件内容、首次/末次时间、累计次数；
- `event_occurrence_5m`：用于看波峰、趋势、异常窗口和 AI 特征，不保存每一次重复明细。

这比“每发生一次就插入一行”大幅降低行数，同时不会丢失某个时间段突然暴增这一关键信号。

## 7. 30 天保留与容量控制

### 7.1 保留定义

按 `last_observed_at` 保留 30 天：

- 每天运行一次清理任务；
- 分批删除 `last_observed_at < UTC_TIMESTAMP() - INTERVAL 30 DAY` 的记录；
- 每批 5,000～10,000 行，批次间短暂停顿，避免长事务和大范围锁；
- 清理结果写入平台指标和日志。

连续重复且近期仍发生的事件会更新 `last_observed_at`，因此不会在仍活跃时被清理。

### 7.2 为什么 P0 不直接按时间分区

MySQL 要求分区列出现在每个唯一键中。如果按 `last_observed_at` 分区，会与稳定去重键 `(cluster_id, event_uid)` 冲突或迫使唯一键包含可变时间，从而削弱幂等 Upsert。第一版先使用正确索引和分批删除；当真实数据量证明需要分区时，再根据日增量和查询计划专门迁移。

### 7.3 容量保护

- Worker 使用有界队列，建议 1,000 条；
- 100 条或 1 秒一次批量 Upsert；
- 初始 List 同时最多 10 个集群，避免平台重启时冲击 API Server；
- 单条消息设置长度上限并安全截断；
- 页面必须使用游标分页，默认每页 50 条，禁止一次返回 30 天全部事件。

### 7.4 数据量估算与升级门槛

先通过真实数据测量，不凭集群数量猜容量。上线后至少记录 7 天：

```text
30 天事件行数 ≈ 每日新增唯一 Event UID × 30
30 天窗口行数 ≈ 每日有事件的“对象 + Reason + 5分钟窗口”组合 × 30
```

示例仅用于容量理解：如果 60 个集群合计每天产生 60 万个唯一 Event UID，30 天约为 1,800 万行。假设数据与索引合计平均占用 2～3 KB，可能需要约 36～54 GB。实际值必须用 `information_schema` 和真实平均行长测量，因为 Kubernetes 会把部分重复事件合并为 Event Series。

满足以下任一条件时再评估 ClickHouse 或 OpenSearch，而不是第一版直接引入：

- 活跃数据持续达到数千万至上亿行，常用查询 p95 超过 500 ms；
- 主要需求从结构化筛选变成跨 30 天大范围全文检索；
- AI 特征计算需要频繁扫描大范围原始数据；
- MySQL 写入、清理任务已经明显影响平台元数据业务。

升级时保留 MySQL 作为平台元数据和近期事件查询库，将事件变化流异步复制到分析存储，避免一次性替换整个平台数据库。

### 7.5 为什么筛选不一定会慢

数据量大不等于筛选一定慢，关键是让查询始终先缩小到“集群 + 时间范围”，再应用其他条件：

```sql
WHERE cluster_id = ?
  AND last_observed_at < ?
  AND last_observed_at >= ?
  AND event_type = ?
ORDER BY last_observed_at DESC, id DESC
LIMIT 50
```

对应索引以 `(cluster_id, last_observed_at, id)` 为基础；高频固定筛选再增加少量复合索引。禁止：

- 使用深层 `OFFSET 100000` 翻页；
- 不带集群和时间范围扫描 30 天全表；
- 对 `message` 使用无约束的 `LIKE '%关键词%'`；
- 为每个可筛选字段都创建索引，导致写入成本失控。

自由文本搜索第一版可以限定在单集群和较短时间范围；确实需要全局全文搜索时再增加 MySQL FULLTEXT 或独立搜索引擎。每个核心查询在真实规模数据上使用 `EXPLAIN ANALYZE` 验证，而不是仅凭“已经建了索引”判断性能。

## 8. 后端 API 草案

### 8.1 工作负载

```http
GET /api/v1/clusters/{id}/namespaces
GET /api/v1/clusters/{id}/workloads?namespace=&kind=&status=&q=
GET /api/v1/clusters/{id}/workloads/{kind}/{namespace}/{name}
GET /api/v1/clusters/{id}/workloads/{kind}/{namespace}/{name}/pods
GET /api/v1/clusters/{id}/pods/{namespace}/{name}/logs?container=&tailLines=
```

### 8.2 事件

```http
GET /api/v1/clusters/{id}/events?since=24h&type=&namespace=&kind=&reason=&q=&cursor=&limit=50
GET /api/v1/clusters/{id}/events/{eventId}
GET /api/v1/clusters/{id}/event-collector/status
```

返回列表时附带 `next_cursor`，游标由 `last_observed_at + id` 组成，保证相同时间下稳定翻页。

## 9. 权限与安全

建议为平台准备最小权限 ServiceAccount；如果继续上传管理员 kubeconfig，也应明确其权限风险。

只读工作负载与事件所需权限：

```yaml
apiGroups: ["", "apps", "batch", "events.k8s.io"]
resources: ["namespaces", "pods", "pods/log", "events", "deployments", "statefulsets", "daemonsets", "replicasets", "jobs", "cronjobs"]
verbs: ["get", "list", "watch"]
```

`pods/exec` 使用单独权限，通常为 `create`，不要与基础只读权限混为一组。所有 Exec 会话必须记录操作者、集群、命名空间、Pod、容器、开始/结束时间和结果，但不记录交互命令内容，避免把可能包含秘密的终端内容写入日志。

前端展示事件消息时只作为纯文本渲染，禁止执行 HTML；API 和日志不得输出 kubeconfig、Token 或客户端私钥。

## 10. 非功能要求与可观测性

| 项目 | 第一版目标 |
|---|---|
| 集群规模 | 60 套，可扩展至 100 套 |
| 页面查询 | 常规筛选 p95 小于 500 ms |
| 事件可见延迟 | 正常网络下 10 秒内入库 |
| 数据保留 | 30 天，按最后发生时间 |
| API 故障隔离 | 单个集群异常不阻塞其他集群采集 |
| 重连 | 指数退避 + 抖动，遵守 Retry-After |
| 时间标准 | 后端与数据库统一 UTC，前端按浏览器时区显示 |

平台自身 Prometheus 指标：

- `event_collector_connected{cluster_id}`；
- `event_collector_ingested_total{cluster_id,type}`；
- `event_collector_reconnect_total{cluster_id,reason}`；
- `event_collector_last_success_timestamp_seconds{cluster_id}`；
- `event_store_upsert_duration_seconds`；
- `event_retention_deleted_total`；
- `event_collector_queue_dropped_total`（正常目标为 0）。

## 11. 失败模式

| 失败场景 | 页面表现 | 后端处理 |
|---|---|---|
| 集群网络中断 | 显示历史数据和“采集中断”时间 | 单集群退避重连，不影响其他集群 |
| RBAC 禁止 watch events | 显示“权限不足” | checkpoint 标记 forbidden，周期重试 |
| resourceVersion 过期 | 用户无感或短暂显示重连 | 重新 List，再继续 Watch |
| MySQL 暂时不可用 | 显示已有历史，采集状态异常 | 有界队列短暂缓冲，退避重试并暴露指标 |
| 平台重启 | 页面短暂显示采集恢复中 | List 当前事件并靠唯一键 Upsert 去重 |
| Event 已在 API Server 过期 | 无法恢复缺口 | 明确显示采集起始时间，不伪造历史 |
| 消息量突增 | 查询保持分页 | 批量写入、有界队列、背压和丢弃指标 |

## 12. 推荐迭代顺序

### 迭代 2A：工作负载只读页

1. 增加命名空间、工作负载、Pod 后端模型与 API；
2. 增加“工作负载”标签、筛选列表和详情页；
3. 支持下钻到实例和按需日志；
4. 暂不开放扩缩容、重启、删除。

### 迭代 2B：事件数据底座

1. 增加 MySQL 配置、事件 Repository 与迁移；
2. 实现每集群 List + Watch Worker；
3. 实现 Upsert、checkpoint、重连和 30 天清理；
4. 暴露采集器自身指标。

### 迭代 2C：单集群事件页

1. 增加事件查询 API；
2. 增加“事件”标签、摘要、筛选、游标分页和详情抽屉；
3. 支持从工作负载/Pod 跳转到带筛选条件的事件页。

### 迭代 2D：受控操作

1. 容器 Exec：二次确认、短会话、审计；
2. 无状态 Deployment 的滚动重启和副本调整；
3. 再根据实际需求评估全局事件中心和 AI 分析。

### 迭代 2E：AI 事件分析

AI 不直接读取 30 天全部 Event，也不把整张表一次性发送给模型。建议使用“规则预处理 + 定向检索 + AI 解释”：

1. 先根据集群、时间窗口、对象和 Reason 从 MySQL 精确检索；
2. 使用 `event_occurrence_5m` 找出突然上升、持续时间过长或跨对象扩散的窗口；
3. 补充工作负载状态、Pod 重启、节点状态和 Prometheus 指标；
4. 生成一个受控的“故障上下文包”，包含少量原始事件证据和聚合特征；
5. AI 输出可能原因、影响范围、排查顺序和证据引用；
6. 保存分析结果、证据 Event ID、模型版本和生成时间，方便复核。

第一版 AI 分析重点应是“解释已筛出的异常窗口”，而不是让模型从海量原始事件中盲找问题。Event 只是一类证据，必须与 Kubernetes 当前状态和 Prometheus 指标交叉验证。资源名称、节点 IP、镜像地址等敏感字段在发送到外部模型前需要按公司策略脱敏。

## 13. 验收标准

- 单集群可以查看 Deployment、StatefulSet、DaemonSet 和所属 Pod 的真实数据；
- API 失败时明确显示错误，不将“未知”伪装成“0”或“正常”；
- 事件采集器覆盖所有纳管集群，单集群失败不影响其他集群；
- 同一 Event UID 重复更新不会生成重复记录；
- 平台重启、Watch 超时和 `410 Gone` 后可恢复采集；
- 可以按集群、时间、类型、命名空间、对象和 Reason 查询；
- 超过 30 天且近期未再发生的事件会被自动清理；
- 所有数据为真实 Kubernetes/MySQL 数据，不使用模拟数据填充页面。

