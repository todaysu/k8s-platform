# FTC 集群运维平台日常排障手册

## 文档目的

本手册供日常值班、故障应急和发布验证使用。它先说明每个组件的作用与故障影响，再按“前端打不开、后端无响应、登录失败、页面转圈、单集群失败、429、报告与安全扫描异常”给出检查顺序、判断标准和恢复动作。

排障的基本原则是：先确认故障范围，再从入口逐层向内检查；重启之前保存日志和现场；一次只恢复一个组件，确认稳定后再继续。

## 一 组件与作用

### 1. 浏览器和访问入口

| 组件 | 作用 | 故障现象 | 主要检查 |
| --- | --- | --- | --- |
| DNS | 将平台域名解析到入口 IP | 域名找不到、访问到旧环境 | `nslookup` `dig` |
| TLS 证书 | 保护 HTTPS，并绑定平台域名 | 证书过期、SAN 不匹配、浏览器阻止 | `openssl s_client` `curl -vk` |
| NodePort Ingress LB | 将外部请求转发到平台 Service | 连接超时、502、503 | 入口状态、端口、后端 Endpoint |
| 浏览器前端 | 加载 React PatternFly 页面并调用 API | 白屏、按钮无反应、旧版页面 | Network、Console、静态资源缓存 |

### 2. `ftc-platform` API 组件

API 角色的 `PLATFORM_ROLE=api`，主要负责：

- 提供前端静态文件、REST API 和 WebSocket。
- 处理登录会话、RBAC、Origin 检查和请求准入。
- 读取健康、容量、Event、告警和资源索引。
- 对用户指定的单集群执行节点、Pod、日志、监控和终端查询。
- 执行运维报告的持久化后台生成器。

API 失效时，前端、登录和所有用户操作会受影响，但独立 Collector 仍可继续采集。

### 3. `ftc-platform-collector` 采集组件

Collector 角色的 `PLATFORM_ROLE=collector`，不提供业务页面，负责：

- 周期采集多集群健康快照。
- 周期采集容量快照和命名空间分类观测。
- 维护跨集群资源索引。
- 使用 Kubernetes Event List Watch 保存事件和聚合。
- 采集 Alertmanager 告警并执行保留清理。

Collector 通过 MySQL shard lease 和 epoch fencing 分配所有权。它失效时，页面可能仍能打开，但采集时间会逐渐过期，新 Event、告警和索引不再更新。

Collector 和 Worker 的内部健康端口提供：

```text
GET /healthz  进程存活
GET /readyz   MySQL 在 2 秒内可访问
```

### 4. `ftc-platform-worker` 任务组件

Worker 角色的 `PLATFORM_ROLE=worker`，负责从 MySQL 持久化任务队列中取出安全扫描任务，在目标集群上创建临时 kube-bench Job，保存结果并回收临时资源。

Worker 失效时，普通页面仍可使用，但安全扫描会排队、超时或失败。运维报告生成器当前运行在 API 角色，不应将报告失败误判为 Worker 故障。

### 5. MySQL

MySQL 是生产环境的共享状态中心，保存：

- 用户、会话、集群元数据和加密凭据。
- 健康、容量和告警快照。
- Event 原始记录与聚合。
- 跨集群资源索引。
- shard 租约、请求配额、安全扫描任务和报告。

MySQL 失效时影响最大：API 就绪检查失败，登录与页面读取失败，Collector 无法提交快照，Worker 无法取得任务。处理平台全局故障时应优先排除 MySQL。

### 6. 被纳管 Kubernetes API

平台通过保存的 kubeconfig 访问目标 API Server。单集群证书过期、Token 失效、RBAC 收紧、路由中断或 API 429 不会直接导致其他集群失败。

如果只有一套集群异常，优先检查该集群的网络、证书、凭据和 RBAC，不要重启整个平台。

### 7. Prometheus 和 Alertmanager

- Prometheus 为指标监控页提供 CPU、内存、磁盘、网络和工作负载时序数据。
- Alertmanager 为告警中心提供当前告警与标签。

它们失效不代表 Kubernetes API 失效。常见现象是集群概览正常，但监控面板为空或告警覆盖过期。

### 8. kube-bench 临时 Job

安全扫描时，Worker 在被纳管集群的 `SECURITY_SCANNER_NAMESPACE` 中创建临时高权限 Job。它依赖正确架构的 kube-bench 镜像、镜像拉取权限、Pod Security 放行和节点调度。

## 二 每日值班检查

### 建议顺序

1. 检查平台入口和 `/api/v1/health`。
2. 检查 API、Collector、Worker 和 MySQL Pod 状态。
3. 检查最近 24 小时重启、OOM、调度失败和存储事件。
4. 检查健康、容量、资源索引和告警采集时间。
5. 检查 MySQL 磁盘、连接数、慢查询和备份年龄。
6. 检查平台 429、5xx 与后台任务失败。

### 基础命令

```bash
kubectl -n ftc-platform get pods -o wide
kubectl -n ftc-platform get deployment,statefulset,service,endpoints,pvc
kubectl -n ftc-platform get events --sort-by=.lastTimestamp
kubectl -n ftc-platform top pods
kubectl -n ftc-platform top nodes
```

健康状态应满足：

- API 至少一个 Ready，生产建议两个 Ready。
- Collector 副本 Ready，采集时间持续推进。
- Worker Ready，安全扫描任务不长期停在 pending/running。
- Service 有 Endpoint，PVC 为 Bound。
- MySQL 可用且数据盘保留至少 30% 余量。

## 三 统一排障流程

### 步骤 1 记录现象

在修改任何状态前记录：

- 故障开始时间、首次发现时间和时区。
- 影响页面、用户、集群和操作。
- HTTP 状态码、页面错误文案和 request URL。
- 最近发布、参数、证书、网络和数据库变更。

### 步骤 2 判断范围

| 范围 | 优先怀疑 |
| --- | --- |
| 所有用户完全无法访问 | DNS TLS 入口 Service API Pod |
| 登录页可打开，所有业务请求失败 | API MySQL 会话 Cookie |
| 页面可用，数据全部停止更新 | Collector MySQL shard lease |
| 只有安全扫描失败 | Worker kube-bench Job 镜像 Pod Security |
| 只有运维报告失败 | API 内报告任务 Event 数据库 模型配置 |
| 只有单一集群失败 | 目标 API 网络 证书 凭据 RBAC |
| 概览正常，监控面板失败 | 目标 Prometheus 或监控查询配额 |
| 大量 429 | API 准入槽位、单集群槽位、慢请求 |

### 步骤 3 从外到内验证

```bash
# 1. DNS
nslookup <平台域名>

# 2. HTTPS 和证书
curl -vk --connect-timeout 5 https://<平台域名>/api/v1/health

# 3. 绕过外部入口验证 Service
kubectl -n ftc-platform port-forward service/ftc-platform 18080:8080
curl -v http://127.0.0.1:18080/api/v1/health

# 4. 检查 Endpoint
kubectl -n ftc-platform get service ftc-platform -o yaml
kubectl -n ftc-platform get endpoints ftc-platform -o yaml
```

判断：

- port-forward 正常，外部域名失败：检查 DNS、TLS、LB、Ingress、NodePort 或防火墙。
- Service 无 Endpoint：检查 selector、API Pod Ready 和就绪探针。
- port-forward 也失败：进入 API Pod 和 MySQL 排查。

## 四 按现象排障

### 1. 前端完全打不开

```bash
curl -vkI https://<平台域名>/
curl -vk https://<平台域名>/api/v1/health
kubectl -n ftc-platform get pods,svc,endpoints -o wide
```

| 结果 | 下一步 |
| --- | --- |
| 连接拒绝 | 检查监听端口、Service、NodePort 和防火墙 |
| 连接超时 | 检查 DNS 目标 IP、LB、路由和安全组 |
| TLS 失败 | 检查证书过期、证书链、SAN 和域名 |
| 502 503 | 检查 Service Endpoint、API Pod 和就绪探针 |
| `/api/v1/health` 200，但 `/` 失败 | 检查 `WEB_DIST_DIR`、镜像中 `/app/web` 和静态资源 |

### 2. 页面白屏或界面是旧版

在浏览器开发者工具中检查：

- `index.html` 是否 200，是否带 `Cache-Control: no-cache`。
- `assets/*.js` 和 `assets/*.css` 是否 200，是否因旧哈希返回 404。
- Console 是否有 JavaScript 运行异常。
- Network 中 API 是否返回 401、429、500 或 503。

```bash
curl -kI https://<平台域名>/
curl -k https://<平台域名>/ | head
```

先用无痕窗口比较。只有单个浏览器异常时，清除该站点缓存；所有用户都异常时，核对部署镜像和静态文件。

### 3. API 无响应、500 或 503

```bash
kubectl -n ftc-platform get pods -l app.kubernetes.io/name=ftc-platform -o wide
kubectl -n ftc-platform describe pod <API-POD>
kubectl -n ftc-platform logs <API-POD> --since=30m --timestamps
kubectl -n ftc-platform logs <API-POD> --previous --timestamps
kubectl -n ftc-platform top pod <API-POD>
```

检索关键错误：

```bash
kubectl -n ftc-platform logs <API-POD> --since=1h |
  rg -i 'fatal|panic|error|timeout|deadline|connection refused|too many connections|oom|killed'
```

常见判断：

- `OOMKilled`：保存内存、大响应和并发数据，检查大日志、Prometheus 序列和请求堆积。
- `database unavailable`：先恢复 MySQL，不要反复重启 API。
- `too many connections`：查慢查询、长事务和连接堆积，不直接无限调高 `max_connections`。
- context deadline：判断是 MySQL 超时还是单一目标集群超时。

### 4. 登录页可打开，但无法登录

```bash
curl -vk https://<平台域名>/api/v1/auth/session
kubectl -n ftc-platform logs <API-POD> --since=15m |
  rg -i 'login|auth|session|origin|cookie|database'
```

检查：

- 是否通过 HTTPS 访问；生产 `SECURE_COOKIES=true` 时，HTTP 不会发送会话 Cookie。
- 代理是否正确传递 Host；Origin 与 Host 不一致会被拒绝。
- MySQL 中用户是否禁用、会话表是否可写。
- 是否连续失败触发登录限流。

不得在日志、工单或命令输出中暴露管理员密码、Cookie 或集群 Token。

### 5. 页面一直加载或返回 429

429 是有界准入保护生效，并不一定是程序故障。平台分为普通查询、实时操作、监控和终端四个通道，并限制单集群并发。

先检查：

- 是否有多人同时打开同一集群的监控面板。
- 是否有长日志、Exec、kubectl TTY 或 Arthas 会话长期占用槽位。
- MySQL 请求配额租约是否能续租和释放。
- 目标 API Server 或 Prometheus 是否响应过慢。

处理顺序：关闭闲置会话，减少监控时间范围，修复慢请求，最后才以不超过 25% 的幅度调整并发参数。

### 6. 页面能打开，但数据长时间不更新

```bash
kubectl -n ftc-platform get deployment ftc-platform-collector
kubectl -n ftc-platform get pods -l app.kubernetes.io/name=ftc-platform-collector -o wide
kubectl -n ftc-platform logs deployment/ftc-platform-collector --since=30m --timestamps
```

关注日志中的：

- `collector mode check failed`：实际 `COLLECTION_SHARDS` 与 MySQL 已持久化模式不一致。
- lease、epoch、fencing 错误：检查 MySQL 延迟、时钟和 Collector 频繁重启。
- 大量 `CONNECTION_FAILED`：检查中央到目标集群的网络和凭据是否发生全局变更。
- 索引超额、分页失败或 410：核对单集群对象规模和 API Server 状态。

如果只有一个 Collector 异常，先观察其他副本是否接管 shard。不得在不停止全部 Collector 和不执行迁移的情况下修改 `COLLECTION_SHARDS`。

### 7. 单一被纳管集群失败

从 API 或 Collector Pod 中测试网络：

```bash
kubectl -n ftc-platform exec <POD> -- \
  curl -vk --connect-timeout 5 https://<目标-API-地址>/version
```

然后检查：

- API Server 地址、DNS 和路由。
- CA 和服务端证书是否过期或轮换。
- kubeconfig 中的 Token、客户端证书或 exec plugin 是否仍有效。
- 平台 ServiceAccount 对 Node、Pod、Workload、Event 和日志是否有所需 RBAC。
- API Server 是否返回 401、403 或 429。

401 通常是凭据失效，403 是 RBAC 不足，429 是目标 API Server 限流。更新凭据后用平台纳管验证功能复测，不要在日志中输出 kubeconfig。

### 8. 监控面板为空或加载慢

确认集群概览和 Kubernetes API 是否正常。若只有监控失败：

- 检查 Prometheus Service 发现结果和端口。
- 检查 Prometheus `/api/v1/query_range` 是否可访问。
- 收窄时间范围、节点和面板组后重试。
- 检查是否有大序列响应、超时或 API 监控通道 429。
- 不要因为 Prometheus 不可用就将集群资源判定为零。

### 9. MySQL 异常

```bash
kubectl -n ftc-platform get pods,pvc | rg -i 'mysql|NAME'
kubectl -n ftc-platform logs <MYSQL-POD> --since=30m --timestamps
kubectl -n ftc-platform top pod <MYSQL-POD>
```

在 MySQL 执行：

```sql
SELECT NOW();
SHOW GLOBAL STATUS LIKE 'Threads_connected';
SHOW GLOBAL STATUS LIKE 'Threads_running';
SHOW GLOBAL STATUS LIKE 'Slow_queries';
SHOW VARIABLES LIKE 'max_connections';
SHOW PROCESSLIST;
SHOW ENGINE INNODB STATUS;
```

磁盘和表容量：

```sql
SELECT table_schema,
       ROUND(SUM(data_length + index_length) / 1024 / 1024 / 1024, 2) AS size_gib
FROM information_schema.tables
WHERE table_schema = 'ftc_platform'
GROUP BY table_schema;
```

处理原则：

- 磁盘接近满：先保护数据库可写性，扩容并检查 Event、binlog 和备份保留。
- 连接耗尽：定位长事务和慢查询，不先增大上限。
- 锁等待：检查资源索引替换、清理任务和大查询。
- 主库失效：按 DBA 预案切换高可用端点，再确认 API、Collector 和 Worker 的 `/readyz`。

### 10. 安全扫描排队或失败

```bash
kubectl -n ftc-platform get deployment ftc-platform-worker
kubectl -n ftc-platform logs deployment/ftc-platform-worker --since=30m --timestamps

# 在被纳管集群查看扫描 Job
kubectl -n <SECURITY_SCANNER_NAMESPACE> get jobs,pods
kubectl -n <SECURITY_SCANNER_NAMESPACE> get events --sort-by=.lastTimestamp
```

根据 Pod 状态判断：

| 状态 | 原因 |
| --- | --- |
| `ImagePullBackOff` | kube-bench 镜像地址、架构或 imagePullSecret 错误 |
| `Pending` | 节点选择、资源不足、污点或 Pod Security |
| `CreateContainerConfigError` | Secret、ConfigMap 或挂载配置错误 |
| `Error` | kube-bench 执行、主机目录挂载或权限失败 |
| 已完成但平台无结果 | Worker 日志、MySQL 写入和任务 lease |

### 11. 运维报告失败

报告后台生成器在 API 角色运行。检查：

```bash
kubectl -n ftc-platform logs deployment/ftc-platform --since=30m |
  rg -i 'report|model|ai|event|timeout|database'
```

判断：

- 事实报告也失败：检查 MySQL、Event 聚合、任务表和时间范围。
- 事实图表成功，只有 AI 未完成：检查模型 endpoint、模型名、API Key、超时和结构化 JSON 输出。
- 模型连接测试通过不代表长报告必然成功，还需检查 token 预算和输出是否被截断。

## 五 日志与现场保存

在重启前保存以下信息：

```bash
incident_dir="/tmp/ftc-incident-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$incident_dir"

kubectl -n ftc-platform get pods,svc,endpoints,pvc -o wide \
  > "$incident_dir/resources.txt"
kubectl -n ftc-platform get events --sort-by=.lastTimestamp \
  > "$incident_dir/events.txt"
kubectl -n ftc-platform top pods \
  > "$incident_dir/top-pods.txt" 2>&1
kubectl -n ftc-platform logs deployment/ftc-platform --since=60m --timestamps \
  > "$incident_dir/api.log" 2>&1
kubectl -n ftc-platform logs deployment/ftc-platform-collector --since=60m --timestamps \
  > "$incident_dir/collector.log" 2>&1
kubectl -n ftc-platform logs deployment/ftc-platform-worker --since=60m --timestamps \
  > "$incident_dir/worker.log" 2>&1
```

保存文件前必须检查和脱敏：

- kubeconfig、Bearer Token、Cookie、API Key、MySQL DSN 和密码不得进入工单。
- 内部 IP、域名、用户名和业务命名空间按组织安全要求脱敏。
- 不得把 `.env`、Secret YAML 或整个数据库导出直接附到普通工单。

## 六 重启和恢复规则

### 什么时候可以重启

- 已保存当前和 `--previous` 日志。
- 已确认只有单个无状态副本异常。
- 配置修改明确需要重启生效。
- 进程已无法提供服务，且没有可恢复的进程内操作。

### 滚动重启

```bash
kubectl -n ftc-platform rollout restart deployment/ftc-platform
kubectl -n ftc-platform rollout status deployment/ftc-platform --timeout=5m

kubectl -n ftc-platform rollout restart deployment/ftc-platform-collector
kubectl -n ftc-platform rollout status deployment/ftc-platform-collector --timeout=5m

kubectl -n ftc-platform rollout restart deployment/ftc-platform-worker
kubectl -n ftc-platform rollout status deployment/ftc-platform-worker --timeout=5m
```

一次只重启一种角色。不要同时重启 API、Collector、Worker 和 MySQL。MySQL 主备切换和恢复必须按 DBA 预案执行，不使用删除 Pod 代替数据库故障分析。

## 七 故障升级标准

出现以下任一情况立即升级：

- MySQL 无法写入、数据盘即将耗尽或疑似数据损坏。
- 全部 API 副本不可用超过 10 分钟。
- 多个 Collector 同时持有同一 shard，或出现 fencing 正确性疑问。
- 凭据、Token、API Key 或数据库备份疑似泄露。
- 多套被纳管集群同时被高频请求或出现 API Server 压力。
- 数据恢复、shard 数迁移、表结构手工修改或回滚需求。

## 八 故障复盘模板

```text
故障标题：
开始时间：
恢复时间：
影响页面：
影响用户与集群：
首个可观测信号：
时间线：
直接原因：
根本原因：
为什么现有告警没有更早发现：
临时恢复动作：
永久整改：
负责人：
计划完成日期：
验证方法：
```

## 九 快速判断口诀

```text
入口不通看 DNS TLS Service 和 Endpoint
白屏看 index.html 静态资源和浏览器 Console
全站异常先看 API 和 MySQL
页面正常数据过期看 Collector
安全扫描失败看 Worker 和 kube-bench Job
报告失败看 API 中的报告任务
单集群异常看该集群网络 证书 凭据 RBAC
大量 429 看慢请求 会话和准入槽位
重启之前保存现场
```
