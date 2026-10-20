# FTC 集群运维平台的 Kubernetes 部署与数据库接入

## 1. 部署目标

生产部署使用一个 MySQL 8.4 保存集群纳管、加密凭据、健康快照、容量基线、Kubernetes Event、账号与登录会话。不引入 Kafka、Elasticsearch 或 ClickHouse。

平台支持两个及以上 API 副本。副本通过 Kubernetes Lease 选出唯一采集 Leader，避免重复扫描 60+ 集群。MySQL 示例默认使用 100 GiB PVC；全部数据表和索引由平台启动时自动创建。

## 2. 部署前检查

1. 确认目标集群存在默认 `StorageClass`：

   ```bash
   kubectl get storageclass
   ```

2. 确认节点至少可提供 4 GiB 内存和 100 GiB 持久化存储，或准备公司已有的高可用 MySQL 8.4。
3. 构建 [`Dockerfile`](../../Dockerfile)，推送镜像并修改 [`deploy/platform.yaml`](../../deploy/platform.yaml) 中的镜像地址。
4. 替换 `deploy/platform.yaml` 与 `deploy/mysql.yaml` 中全部 `REPLACE_WITH_*`。密码不应提交到 Git。
4. 如果密码包含 `@`、`:`、`/` 等字符，`dsn` 中的密码必须进行 URL 编码。

## 3. 部署 MySQL

```bash
kubectl apply -f deploy/platform.yaml --server-side
kubectl apply -f deploy/mysql.yaml
kubectl -n ftc-platform rollout status statefulset/platform-mysql --timeout=5m
kubectl -n ftc-platform get pod,svc,pvc
```

预期结果：

- `platform-mysql-0` 为 `1/1 Running`。
- `data-platform-mysql-0` PVC 为 `Bound`。
- Service 地址为 `platform-mysql.ftc-platform.svc:3306`，不对集群外暴露。

检查 MySQL：

```bash
kubectl -n ftc-platform exec statefulset/platform-mysql -- sh -c \
  'mysqladmin ping -h 127.0.0.1 -u"$MYSQL_USER" -p"$MYSQL_PASSWORD"'
```

## 4. 平台接入 MySQL

平台只需要一个 MySQL DSN，不要把它明文写到 Deployment：

```yaml
env:
  - name: PLATFORM_DATABASE_DSN
    valueFrom:
      secretKeyRef:
        name: ftc-platform
        key: mysql-dsn
```

MySQL 可以稍后添加：不设置 `PLATFORM_DATABASE_DSN` 时平台使用 SQLite 本地开发模式；设置后重启平台即会连接 MySQL，取得迁移锁并自动创建所有表、索引。生产数据不会自动从已有 SQLite 搬迁到 MySQL，正式使用前应确定数据库模式。

平台与 MySQL 位于不同 Namespace 时，Secret 仍须放在平台 Namespace；DSN 使用完整 Service DNS：

```text
platform-mysql.ftc-platform.svc:3306
```

重启平台后检查：

```bash
kubectl -n ftc-platform rollout restart deployment/ftc-platform
kubectl -n ftc-platform rollout status deployment/ftc-platform
kubectl -n ftc-platform logs deployment/ftc-platform --all-containers --tail=100
```

日志中只有一个 Pod 显示 `became leader`。设置 `METRICS_ADDR=:9090` 后，可通过同 Namespace 内的 `ftc-platform:9090/metrics` 抓取采集器、事件 Watch 和 API 指标；该端口不经过登录认证，不要通过 Ingress 暴露。打开任意已纳管集群的“事件”页，采集状态应变为 `watching`。无需在被纳管集群内安装 Agent。

平台会自动建立“集群 + 时间”、“类型 + 时间”、“命名空间 + 时间”和“资源种类 + 时间”组合索引，使常用筛选不需要全表扫描。自由文本搜索使用模糊匹配并受页面时间范围约束。

Event 使用分级保留策略控制数据量：Normal 原始事件保留 7 天，Warning/异常原始事件保留 30 天，按事件指纹生成的小时聚合保留 365 天，按天/对象/类别生成的日聚合保留 31 天。24 小时以上窗口优先读取日聚合，6 小时以内仍读原始表；某集群日聚合为空时回退原始表。日聚合的事件数是“首次观察”口径，累计发生次数仍是精确值。重复的 Watch 更新只把新增次数写入小时聚合，不会重复累计整条 Event 的历史次数。清理任务每小时执行，单批最多删除 5000 行，避免大事务阻塞在线查询。如果原始表仍增长到数千万行，再评估 MySQL 月分区或独立检索系统，第一版不预先引入重型组件。

## 5. 开启 Pod 终端

Pod Exec 是高权限功能，默认关闭。只有在平台入口已开启 HTTPS 与管理员认证后，才能在 Deployment 中设置：

```yaml
env:
  - name: ENABLE_POD_EXEC
    value: "true"
```

纳管 Kubeconfig 对应的用户至少需要目标 Namespace 内的以下权限：

```yaml
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get"]
  - apiGroups: [""]
    resources: ["pods/exec"]
    verbs: ["create"]
```

平台只允许选择已存在的容器，并启动 `/bin/sh` 或 `/bin/bash`。不保存终端输入和输出。对生产环境，后续应增加独立的 Exec 审计记录、操作人和会话时间，但不应记录可能包含密码的终端内容。

## 6. 备份与扩容

- 当 PVC 使用率超过 70% 时，先扩容 PVC，不要删除 StatefulSet 的数据卷。
- 每日备份 `ftc_platform` 数据库。该库同时包含纳管凭据、账号、快照和 Event。
- 备份示例见 [`deploy/mysql-backup.yaml`](../../deploy/mysql-backup.yaml)：每天 02:00 用 `mysqldump --single-transaction` 导出并 gzip 到 20 GiB PVC，保留 7 天。备份卷与数据卷分离，避免 StatefulSet 删除时一起丢失：

  ```bash
  kubectl apply -f deploy/mysql-backup.yaml
  kubectl -n ftc-platform create job --from=cronjob/platform-mysql-backup platform-mysql-backup-manual
  ```

  备份仍在同一集群内，生产环境应把 `/backup` 同步到对象存储或异地备份系统，并定期做恢复演练。
- 该 YAML 是单实例第一版，不提供 MySQL 高可用。平台使用单个 DSN，不包含读写分离；如果平台成为生产核心系统，应改用公司已有的高可用 MySQL 服务或 MySQL Operator，并把 `PLATFORM_DATABASE_DSN` 指向其 Service。
- 平台健康采集在 `deploy/platform.yaml` 中默认使用 `HEALTH_INSTANCE_SOURCE=workloads`，不再每分钟全量读取业务 Pod；资源搜索索引由 Leader 每 10 分钟刷新。若需要 Pod 级精确实例口径，可改回 `pods`。

## 7. 配置全局业务 Namespace 与交付基线

在全局“容量”页面点击“业务命名空间配置”，初始化固定的业务 Namespace 清单。每行一个，也支持逗号分隔，名称按 Kubernetes Namespace 规范校验；可以提前填写尚未创建的名称，也可从集群发现结果点击“加入业务”。移除名称后，它在所有集群中自动归为平台组件。保存后全局生效，不创建或修改 Kubernetes Namespace。

- 清单内 Namespace 全部计为业务，其他 Namespace 全部计为平台组件；不再维护公共组件、技术栈组件或未分类类别。
- 相同 Namespace 跨集群、跨技术栈统一规则；技术栈仅保留为容量资源池分组维度。
- 空 Namespace 也会发现和分类，Pod 数和 Requests 为 0；纳管凭据须具备集群级 `list namespaces` 权限。
- 未配置业务清单时全部计为平台，页面提示交付前补齐清单。旧版本中的业务清单继续使用，公共/技术栈映射不再参与统计，历史规则记录保留；`SYSTEM_NAMESPACES` 不再影响容量分类。
- CPU/内存 Requests、Limits、Prometheus 实际用量使用同一业务清单口径；无节点的 Pod 需求仍单独记入待调度，已结束 Pod 不计入容量。

交付前，进入集群容量详情核对业务清单、平台范围和资源申请，手动锁定当前基线。基线记录交付时的平台 Namespace 名单与资源量。交付后创建、且不在原基线平台名单内的非业务 Namespace 自动计入平台并提示“交付后新增”，包括空 Namespace。旧基线因旧分类遗漏的已存在 Namespace 仅提示平台范围变化，不误报交付后新增；这些资源已计入平台开销，不重复扣减。基线不会自动改变，确认变更后由管理员生成新版本，或填写原因暂时沿用旧基线。没有基线时提示尚未建立，不将所有现有 Namespace 误报为新增。

业务清单、交付决策和历史基线保存在平台数据库。Prometheus 原始时序数据不复制到平台数据库。

## 8. 跨集群事件排查

“跨集群事件”默认打开异常排查列表。先选择异常类别，再点击“查看影响对象”，选择集群中的具体资源进行核验。类别是症状索引，不是共同根因结论；一个镜像拉取类别可包含重试、认证、网络等不同证据。影响对象按集群、Kind、Namespace 与资源 UID 区分，不凭 Pod 名称猜测 Deployment。

对象核验按需读取 Kubernetes API，显示当前状态、Pod 所在节点、真实 ownerReference 链、容器退出/重启信息。资源 UID 变化时标记“同名资源已替换”，不展示替换资源的日志来代表旧对象；权限不足或连接超时标记未核验，不判定恢复。Pod、Node、Deployment、StatefulSet、DaemonSet、ReplicaSet 支持当前状态核验，其他资源保留历史证据入口。

关联事件默认查询该对象最近发生前后 15 分钟，包含 Normal 前后文，可扩展至 1/6/24 小时。容器尚未启动时优先查看拉取、挂载与调度事件；已有日志的 Pod 可直接进入当前或上一次容器日志页。没有资源 UID 的旧事件仅按名称关联，并明确提示可能包含同名资源。

原始记录位于次级“事件检索”，支持时间、集群、业务/平台、Namespace、资源类型、事件类型与关键字筛选，默认不显示普通 Normal 生命周期事件。“包含 Normal 前后文”可显式开启。查询参数、页码和对象上下文保存在 URL；进入资源详情后浏览器返回可恢复检索范围。

业务/平台复用全局业务 Namespace 清单，Node 和没有业务 Namespace 的事件属于平台。汇总与列表应用相同服务端筛选。每次查询在时间索引范围内流式读取持久化 Event，服务端完整聚合并返回当前页，不再截取每集群 200 条/全局 500 条作为汇总。查询有 30 秒上限，超时或部分集群存储读取失败需显示数据缺失，不能显示“没有异常”。当前实现仍需扫描所选时间段，超大规模环境可在此契约下增加索引化聚合缓存，不应将全部明细传给浏览器。

Event 记录以 UID 更新，次数是该 Event 的累计发生次数，不是所选窗口新增次数；时间筛选以最近发生时间为准。并非每次发生都保存为独立行，因此不展示虚假的逐次发生时间线。页面首页每 30 秒刷新，详情和翻页暂停自动刷新，但存储中的 Watch 更新仍持续进行。

## 登录与平台权限

所有业务 API 必须登录。首次启动：若 `ADMIN_PASSWORD_HASH` 为有效 bcrypt 哈希，则初始化 `ADMIN_USERNAME` 指定的管理员；已有账号时不覆盖密码。Kubernetes 生产部署必须提供有效 bcrypt，避免多个副本各自产生初始化令牌。密码为 12–72 字节，bcrypt cost=12。账号与会话保存在统一平台数据库。

管理员可添加账号、修改角色、禁用和重置密码；不允许禁用自己或降低自己的管理员角色。角色覆盖全部已纳管集群：admin 查看、配置和管理账号；operator 查看与 Pod 终端；viewer 只读资源、日志和事件。API 权限独立于页面按钮控制，不能通过直接请求绕过。此处是平台权限，不会为集群创建 Kubernetes RoleBinding。

会话使用 HttpOnly、SameSite=Strict Cookie，`SESSION_TTL` 默认 8 小时。会话保存在共享数据库，因此滚动升级或请求切换 Pod 不会退出；禁用、修改角色或密码后旧会话立即撤销。写请求及 WebSocket 终端校验 Origin 与 Host。生产 HTTPS 保持 `SECURE_COOKIES=true`；本地 HTTP 使用 false。登录限速保留在各副本内存中，外部入口还应配置速率限制。

## 集群 kubectl 终端

服务端 PATH 需要存在与目标集群兼容的 kubectl 可执行文件（容器部署时应加入运行镜像）。集群详情 → 终端使用平台已保存的凭据，只有平台管理员可以调用。支持常用 get/describe/logs/top/rollout/scale/apply/create/delete/patch/label/annotate 等命令，输入可带 kubectl 前缀。应用 YAML 请使用 kubectl apply -f - 并打开 YAML 输入；本机文件路径、切换 context、插件和本机 Shell 不开放。单命令最多 60 秒，输出最多 1MiB；停止或离开当前集群会取消请求。命令输出仅保留在当前页面内。该界面是逐条 kubectl 命令执行器，交互式 Pod Shell 使用资源详情中的原有终端。
