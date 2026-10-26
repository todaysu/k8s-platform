# FTC 集群运维平台 MySQL 资源配置说明

## 文档目的

本文档面向 DBA、Kubernetes 管理员和平台交付人员，说明 FTC 在不同纳管规模下的 MySQL CPU、内存、存储、连接数、高可用和备份起始配置。文中数值是容量测试的起点，不是不考虑节点数、Pod 数、Event 速率和保留期的固定承诺。

## 生产基线

- 使用 MySQL 8.0 或更高版本，推荐 MySQL 8.4 LTS。
- 使用 InnoDB、`utf8mb4`、`utf8mb4_0900_ai_ci` 和 UTC 时区。
- 生产优先使用企业现有高可用 MySQL 或托管数据库；不使用 `deploy/mysql.yaml` 单副本清单承载 60+ 集群。
- 数据盘使用 SSD 或 NVMe，数据库数据、binlog 和备份分开计算容量。
- MySQL 与 API、Collector、Worker 之间使用低延迟内网，生产不通过 NodePort 对外公开 3306。

## 按纳管规模的起始配置

| 纳管规模 | 用途 | MySQL 架构 | CPU | 内存 | 数据盘 | 建议 `max_connections` |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| 1–10 集群 | 开发、功能测试 | 单副本 | 2 vCPU | 4 GiB | 50 GiB | 100 |
| 10–30 集群 | 小规模正式环境 | 主备或托管 HA | 4 vCPU | 8 GiB | 100–200 GiB | 200 |
| 30–60 集群 | 中等规模生产 | 跨节点 HA | 8 vCPU | 16 GiB | 300–500 GiB | 300 |
| 60–100 集群 | 目标生产规模 | 跨可用区 HA | 8–16 vCPU | 16–32 GiB | 500 GiB–1 TiB | 400–500 |

对 60–100 集群，起始建议是每个主库节点 8 vCPU、16 GiB 内存和 500 GiB SSD。若 Event 写入、资源索引和 binlog 已占用持续 IO，或运行 100 集群长稳负载，升级到 16 vCPU、32 GiB 和 1 TiB 前应先核对慢查询、索引命中和保留策略，避免仅用加资源掩盖无界查询。

## CPU 与内存

### CPU

MySQL CPU 主要消耗在 Event 聚合、资源搜索、大批量索引替换和多页面并发查询。正常负载下建议将长时间 CPU 使用率控制在 60% 以下，为 Collector 冷启动、索引重建和副本接管保留突发容量。

### InnoDB Buffer Pool

| MySQL 内存 | `innodb_buffer_pool_size` 起始值 | 适用说明 |
| ---: | ---: | --- |
| 4 GiB | 2 GiB | 开发或小规模单副本 |
| 8 GiB | 5 GiB | 10–30 集群 |
| 16 GiB | 10–12 GiB | 30–60 集群 |
| 32 GiB | 22–24 GiB | 60–100 集群和大索引 |

专用 MySQL 实例可将 65%–75% 内存用于 buffer pool。容器 limit 不能与 buffer pool 相等，必须为连接级 buffer、performance schema、redo、临时表和备份进程预留内存。若容器出现 OOM，先检查并发连接数和每连接 buffer，不应盲目上调 buffer pool。

## 存储容量与 IO

Event 是主要增长项，但一次 Event 更新不等于一行新记录。实际容量必须用生产数据中的唯一 Event 数、平均行宽、索引、binlog、保留期和备份压缩率计算。

容量预算可按下式起步：

```text
数据盘 = 表数据 + 索引 + 临时空间 + 至少 30% 增长余量
binlog 空间 = 峰值日写入量 × binlog 保留天数
备份空间 = 单次全量备份 × 保留份数 + 恢复临时空间
```

初始容量建议：

- 30 天 Event 保留、60–100 集群：从 500 GiB SSD 起步，在上线后第 1、7、14 天核对实际增长。
- 数据盘尽量保持 30% 可用空间；可用空间低于 20% 告警，低于 10% 立即处理。
- 建议起始持续 IOPS 不低于 3000，且能承受 100 MiB/s 左右的顺序吞吐；最终以峰值索引采集和备份时的 fsync 延迟验收。
- MySQL 数据、binlog 和备份不应共用一个容量较小的 PVC。

## 连接数配置

FTC 每个 MySQL 数据库池在代码中以 20 个最大打开连接、5 个空闲连接和 5 分钟连接生命期为基线。生产角色拆分后，应将 API、Collector、Worker、迁移、备份和 DBA 应急连接一起纳入预算。

| 业务来源 | 60+ 集群起始预算 |
| --- | ---: |
| API 2 副本 | 40 |
| Collector 2 副本 | 40 |
| Worker 1 副本 | 20 |
| 滚动升级临时副本 | 40 |
| 备份、迁移和 DBA 预留 | 40 |
| 安全余量 | 20 |
| 起始总预算 | 200 |

对当前 2 API + 2 Collector + 1 Worker 的起始规模，`max_connections=300` 足以覆盖正常运行与滚动升级；配置为 500 可作为后续扩容预留，但必须同时保证每连接内存和 MySQL 上限，不应把增大连接数当作慢查询的解决方案。

## MySQL 参数起始值

下列参数是 60–100 集群、16 GiB MySQL 实例的起始参考：

```ini
[mysqld]
character-set-server=utf8mb4
collation-server=utf8mb4_0900_ai_ci
default-time-zone=+00:00
max_connections=300
max_allowed_packet=64M
innodb_buffer_pool_size=10G
innodb_buffer_pool_instances=4
innodb_log_file_size=2G
innodb_log_buffer_size=256M
innodb_flush_method=O_DIRECT
innodb_flush_log_at_trx_commit=1
sync_binlog=1
transaction_isolation=READ-COMMITTED
slow_query_log=ON
long_query_time=1
log_queries_not_using_indexes=OFF
```

`innodb_flush_log_at_trx_commit=1` 和 `sync_binlog=1` 用于保持故障后数据安全边界。如需放宽持久性换取写入性能，必须由 DBA 明确评估 RPO，不在平台默认配置中降低。

## Kubernetes requests 与 limits

MySQL 是有状态服务，生产建议将 requests 接近实际保留资源，避免与其他高 IO 负载过度竞争。

| 规模 | requests | limits | 说明 |
| --- | --- | --- | --- |
| 开发测试 | `cpu: 1` `memory: 2Gi` | `cpu: 4` `memory: 4Gi` | 与 `deploy/mysql.yaml` 一致，不是生产规格 |
| 30–60 集群 | `cpu: 4` `memory: 12Gi` | `cpu: 8` `memory: 16Gi` | buffer pool 建议 10 GiB 以下 |
| 60–100 集群 | `cpu: 8` `memory: 24Gi` | `cpu: 16` `memory: 32Gi` | buffer pool 建议 22–24 GiB，需专用节点和快速盘 |

若采用托管 MySQL，将表中值映射为实例规格和 IOPS 档位。若采用 Kubernetes 内 HA MySQL，每个数据节点都要具备相同的可持续规格，不能只为主节点分配高资源。

## 高可用和灾备

- 生产至少一主一备，建议跨可用区部署，且完成实际故障切换演练。
- 平台使用的连接地址必须指向可切换的主库端点，不得绑定单个 Pod IP。
- RPO 和 RTO 建议起点：RPO 不超过 15 分钟，RTO 不超过 60 分钟；有更高要求时使用同步复制、自动切换和持续 binlog 归档。
- 备份必须与 MySQL 数据盘故障域分离，并定期复制到异地存储。

## 备份容量与周期

| 项目 | 建议 |
| --- | --- |
| 全量备份 | 每日 1 次，保留至少 7 份 |
| binlog | 持续归档，保留 7–14 天 |
| 月度备份 | 保留 6–12 个月，依合规要求调整 |
| 恢复演练 | 至少每季度 1 次 |
| 密钥和 Secret | 与数据库备份同等管理，但与数据分开保存 |

`deploy/mysql-backup.yaml` 中的 20 GiB PVC 只适用于开发或小数据量。生产备份空间应使用“最大单次备份 × 保留份数 × 1.3”计算，并为恢复演练保留额外空间。

## 监控和告警阈值

| 指标 | 预警 | 紧急 | 说明 |
| --- | ---: | ---: | --- |
| CPU 使用率 | 60% 持续 15 分钟 | 80% 持续 10 分钟 | 同时查慢查询和索引采集 |
| 内存使用率 | 75% | 90% | 容器环境同时看 OOMKill |
| 数据盘使用率 | 70% | 85% | 还要看日增长速度 |
| 连接使用率 | 60% | 80% | `Threads_connected / max_connections` |
| 连接等待 | > 0 持续 | 持续增长 | 结合平台 503 和采集延迟 |
| 复制延迟 | 30 秒 | 5 分钟 | 影响故障切换的 RPO |
| fsync P95 | 10 ms | 25 ms | 持续过高表示存储瓶颈 |
| 慢查询 | 出现新指纹 | 数量持续增长 | 长时间查询需保留 EXPLAIN |
| 备份年龄 | 24 小时 | 36 小时 | 同时验证备份文件可解压 |

## 扩容与调参顺序

1. 先确认是 CPU、buffer pool miss、存储延迟、锁等待、连接等待还是单个慢查询。
2. 对慢查询优先补索引、收窄时间范围或减少扫描行数，不先增加连接数。
3. 对 buffer pool miss 增加内存；对 fsync 延迟增加 IOPS 或迁移到更快存储。
4. 对资源索引完整替换导致的锁与 IO 峰值，先放宽采集间隔，再根据实测决定是否开发 UID 增量索引。
5. 一次只修改一类参数，保存变更前后至少 30 分钟的对比数据和回滚值。

## 上线验收清单

- [ ] MySQL 版本、字符集、排序规则和时区正确。
- [ ] 平台账号仅具有目标库需要的 DDL DML 权限，不使用 root。
- [ ] API、Collector、Worker 均通过高可用端点连接。
- [ ] 60+ 集群环境的数据库 CPU、内存、IOPS 和连接数经过混合负载验收。
- [ ] 已开启慢查询、容量、连接、复制和备份年龄告警。
- [ ] 已执行主备切换和全量备份恢复演练。
- [ ] 已实测 7 天以上的数据增长，并按保留期推算全周期容量。
