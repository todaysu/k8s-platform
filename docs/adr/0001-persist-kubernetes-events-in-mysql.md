# ADR-0001：使用 MySQL 持久化 Kubernetes Event

## 状态

Accepted

## 背景

平台需要管理约 60 套 Kubernetes 集群，并保留 30 天事件历史。Kubernetes Event 自身保留时间有限，平台现有 SQLite 主要承担轻量集群元数据存储，不适合作为多集群持续高频写入的生产事件库。公司已有 MySQL 使用基础。

## 决策

1. 通过平台中央后台采集器持续 List + Watch `events.k8s.io/v1`，兼容回退 `core/v1`；不在被纳管集群部署 Agent。
2. 将规范化事件 Upsert 到 MySQL，以 `(cluster_id, event_uid)` 保证幂等。
3. 重复发生次数不逐条展开，而是根据 Event Series 的 count 增量写入 5 分钟聚合表，供趋势和 AI 分析。
4. 按 `last_observed_at` 保留 30 天，第一版采用索引、游标分页和分批删除，不立即启用表分区。
5. 工作负载状态仍实时读取 Kubernetes API；MySQL 只承担事件历史，不作为工作负载真实状态源。
6. 只有在真实数据证明 MySQL 无法满足查询或分析目标后，才增加 ClickHouse 或 OpenSearch，避免过早引入重型组件。

## 结果

### 正面

- 可以稳定查询超过 Kubernetes API Server 默认事件 TTL 的历史；
- 复用现有 MySQL 运维能力；
- 单集群事件页、全局事件中心和后续 AI 分析共享同一数据源；
- 唯一键 Upsert 可承受 Watch 重连和重复投递。
- 5 分钟聚合保留事件暴增趋势，同时避免逐次展开造成行数膨胀。

### 负面

- 平台增加 MySQL 依赖、迁移和容量治理工作；
- 平台采集器未运行期间的过期事件无法补回；
- 需要实现清理任务、采集状态和重连监控。
- 深度全文搜索或上亿行大范围分析仍可能需要专用分析存储。

### 中性

- 现有 SQLite 可暂时继续保存集群元数据；后续可评估统一迁移到 MySQL。
- 如果真实事件量远高于预计，需要在容量数据充分后重新评估分区或专用事件存储。

## 备选方案

### 直接查询 Kubernetes Event

不采用。Event 保留时间有限，无法满足 30 天历史，也无法支持跨集群统一查询。

### 继续使用 SQLite

不采用为生产事件库。它适合当前单进程轻量元数据，但不利于持续写入、高可用副本和后续查询扩展。

### Elasticsearch / OpenSearch

暂不采用。搜索能力强，但部署和运维成本与轻量化目标不符；当前筛选查询 MySQL 索引足够。

### Prometheus

不采用。Prometheus 适合数值型时序指标，不适合保存带消息和对象维度的事件明细。

### 每条重复事件都保存为独立行

不采用。Kubernetes Event Series 已提供累计次数；逐条展开会显著放大行数和索引写入，却很少增加诊断价值。保留唯一事件明细加 5 分钟增量聚合即可支持页面查询和 AI 趋势分析。

## 参考

- Kubernetes Event API: https://kubernetes.io/docs/reference/kubernetes-api/events/event-v1/
- Kubernetes API watch semantics: https://kubernetes.io/docs/reference/using-api/api-concepts/
- kube-apiserver event TTL: https://kubernetes.io/docs/reference/command-line-tools-reference/kube-apiserver/
- MySQL partition unique-key restrictions: https://dev.mysql.com/doc/refman/8.4/en/partitioning-limitations-partitioning-keys-unique-keys.html
