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
4. 生成平台加密密钥和一次性初始化凭证：

   ```bash
   openssl rand -base64 32   # CREDENTIAL_ENCRYPTION_KEY：32 字节密钥的 Base64
   openssl rand -hex 16      # SETUP_TOKEN：首次初始化凭证
   ```

5. 替换 `deploy/platform.yaml` 中的 `REPLACE_WITH_BASE64_32_BYTE_KEY` 和 `REPLACE_WITH_SETUP_TOKEN`。
6. 替换 `deploy/mysql.yaml` 中的 root 密码、应用账号密码。密码不要提交到 Git。

> 平台不再要求预先写入 `ADMIN_PASSWORD_HASH` 或 `PLATFORM_DATABASE_DSN`。首次进入平台时由初始化页面选择数据库方式、填写连接信息并创建管理员。

## 3. 部署 MySQL（可选，仅集群内模式）

```bash
kubectl create namespace ftc-platform
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

如果使用集群外 MySQL，不要应用 `deploy/mysql.yaml`，直接在下一节的初始化页面选择“集群外 MySQL”。

## 4. 部署平台并进入数据库初始化

```bash
kubectl apply -f deploy/platform.yaml
kubectl -n ftc-platform rollout status deployment/ftc-platform --timeout=5m
kubectl -n ftc-platform get pod,svc
```

初始 Pod 运行在 `SETUP_MODE=true`，不会连接数据库；`/api/v1/health` 仍可用，所以就绪探针正常。此时访问平台会显示“初始化平台数据库”。

### 4.1 读取初始化凭证

```bash
kubectl -n ftc-platform get secret ftc-platform \
  -o jsonpath='{.data.setup-token}' | base64 -d; echo
```

集群内 MySQL 的应用账号密码：

```bash
kubectl -n ftc-platform get secret platform-mysql \
  -o jsonpath='{.data.password}' | base64 -d; echo
```

### 4.2 打开初始化页面

生产通过 Ingress/Route 暴露 Service；临时验证可以端口转发：

```bash
kubectl -n ftc-platform port-forward svc/ftc-platform 8080:80
```

浏览器打开 `http://127.0.0.1:8080`。

如果使用 HTTPS Ingress，`SECURE_COOKIES=true` 可以直接保持；端口转发的 HTTP 只用于初始化页面，初始化完成后登录建议改用 HTTPS。

### 4.3 选择数据库方式

页面提供两个选项：

1. **随平台部署在集群内**
   - 地址预填 `platform-mysql.ftc-platform.svc`；
   - 端口 `3306`，数据库 `ftc_platform`；
   - 用户名 `ftc`，密码填写 `platform-mysql` Secret 中的 `password`；
   - 这是默认方式，适用于第 3 节已部署的 MySQL。

2. **连接集群外 MySQL**
   - 填写外部地址、端口、数据库名、用户名、密码；
   - 需要 TLS 时在“TLS”字段填 `true`、`skip-verify` 或团队约定的 TLS 配置；
   - 不需要部署 `deploy/mysql.yaml`。

填写初始化凭证和第一个管理员用户名/密码，先点“测试连接”，再点“初始化数据库”。

平台会：

- 连接目标 MySQL；
- 创建全部表、索引和 `schema_migrations`；
- 创建第一个管理员账号；
- 把数据库 DSN 和管理员密码哈希写入 `ftc-platform-runtime` Secret；
- 自动退出并重启。

重启后 Deployment 从 `ftc-platform-runtime` 读取 `PLATFORM_DATABASE_DSN` 和管理员信息，进入正常登录页。

```bash
kubectl -n ftc-platform rollout status deployment/ftc-platform
kubectl -n ftc-platform get secret ftc-platform-runtime
kubectl -n ftc-platform logs deployment/ftc-platform --tail=100
```

日志中只有一个 Pod 显示 `became leader`。打开任意已纳管集群的“事件”页，采集状态应变为 `watching`。无需在被纳管集群内安装 Agent。

### 4.4 运行配置 Secret

`ftc-platform-runtime` 由平台创建，包含：

- `dsn`：数据库连接串（含密码）；
- `admin-username`；
- `admin-password-hash`。

它和数据库备份一样重要：删除后平台会回到初始化模式，且数据库里已有管理员时初始化会被拒绝。请把它纳入 Secret 备份/密钥管理，不要提交到 Git。

如果希望完全跳过初始化页面，也可以在 Deployment 中预先提供 `PLATFORM_DATABASE_DSN`（或 `PLATFORM_DB_HOST`/`PLATFORM_DB_USER`/`PLATFORM_DB_PASSWORD`/`PLATFORM_DB_NAME`）和 `ADMIN_PASSWORD_HASH`；只要 DSN 存在，平台会跳过 `SETUP_MODE` 直接启动。这种方式适合 GitOps/自动化，但需要自行管理管理员哈希。

### 4.5 初始化后检查

```bash
kubectl -n ftc-platform exec statefulset/platform-mysql -- sh -c \
  'mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" ftc_platform -e "SHOW TABLES;"'
```

平台会自动建立集群、容量、事件、`event_daily`、`cluster_resources`、账号会话等全部表。设置 `METRICS_ADDR=:9090` 后，可通过同 Namespace 内的 `ftc-platform:9090/metrics` 抓取采集器、事件 Watch 和 API 指标；该端口不经过登录认证，不要通过 Ingress 暴露。

Event 使用分级保留策略控制数据量：Normal 原始事件保留 7 天，Warning/异常原始事件保留 30 天，按事件指纹生成的小时聚合保留 365 天，按天/对象/类别生成的日聚合保留 31 天。24 小时以上窗口优先读取日聚合，6 小时以内仍读原始表；某集群日聚合为空时回退原始表。日聚合的事件数是“首次观察”口径，累计发生次数仍是精确值。重复的 Watch 更新只把新增次数写入小时聚合，不会重复累计整条 Event 的历史次数。清理任务每小时执行，单批最多删除 5000 行，避免大事务阻塞在线查询。

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

业务/平台复用全局业务 Namespace 清单，Node 和没有业务 Namespace 的事件属于平台。汇总与列表应用相同服务端筛选。6 小时以内读取原始 Event，24 小时以上优先读取按天/对象/类别生成的日聚合；服务端完整聚合并返回当前页，不再截取每集群 200 条/全局 500 条作为汇总。查询有 30 秒上限，超时或部分集群存储读取失败需显示数据缺失，不能显示“没有异常”。

Event 记录以 UID 更新，次数是该 Event 的累计发生次数，不是所选窗口新增次数；时间筛选以最近发生时间为准。并非每次发生都保存为独立行，因此不展示虚假的逐次发生时间线。页面首页每 30 秒刷新，详情和翻页暂停自动刷新，但存储中的 Watch 更新仍持续进行。

## 登录与平台权限

所有业务 API 必须登录。首次启动通过初始化页面创建第一个管理员；初始化完成后平台把管理员密码哈希写入 `ftc-platform-runtime` Secret。若采用预配置 DSN 跳过初始化页，则必须提供有效的 `ADMIN_PASSWORD_HASH`（bcrypt cost=12），否则会退回一次性令牌流程。密码为 12–72 字节。账号与会话保存在统一平台数据库。

管理员可添加账号、修改角色、禁用和重置密码；不允许禁用自己或降低自己的管理员角色。角色覆盖全部已纳管集群：admin 查看、配置和管理账号；operator 查看与 Pod 终端；viewer 只读资源、日志和事件。API 权限独立于页面按钮控制，不能通过直接请求绕过。此处是平台权限，不会为集群创建 Kubernetes RoleBinding。

会话使用 HttpOnly、SameSite=Strict Cookie，`SESSION_TTL` 默认 8 小时。会话保存在共享数据库，因此滚动升级或请求切换 Pod 不会退出；禁用、修改角色或密码后旧会话立即撤销。写请求及 WebSocket 终端校验 Origin 与 Host。生产 HTTPS 保持 `SECURE_COOKIES=true`；本地 HTTP 使用 false。登录限速保留在各副本内存中，外部入口还应配置速率限制。

## 集群 kubectl 终端

服务端 PATH 需要存在与目标集群兼容的 kubectl 可执行文件（容器部署时应加入运行镜像）。集群详情 → 终端使用平台已保存的凭据，只有平台管理员可以调用。支持常用 get/describe/logs/top/rollout/scale/apply/create/delete/patch/label/annotate 等命令，输入可带 kubectl 前缀。应用 YAML 请使用 kubectl apply -f - 并打开 YAML 输入；本机文件路径、切换 context、插件和本机 Shell 不开放。单命令最多 60 秒，输出最多 1MiB；停止或离开当前集群会取消请求。命令输出仅保留在当前页面内。该界面是逐条 kubectl 命令执行器，交互式 Pod Shell 使用资源详情中的原有终端。
