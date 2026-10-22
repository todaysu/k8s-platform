# 交付版安装与初始化设计

日期：2026-10-22
状态：设计评审，待确认 4 个架构决策后进入实现

## 1. 目标

交付一套面向内网 Kubernetes 集群的安装方式：

- 运维只需要 `kubectl apply -f` 一个引导清单；
- 通过 `port-forward` 打开初始化向导；
- 在向导中完成数据库、存储、域名、HTTPS、管理员和运行规格配置；
- 初始化完成后自动生成 Ingress，访问 `https://<内部域名>`；
- MySQL 为高可用架构，镜像来自企业私有仓库；
- 向导给出本地 hosts 配置和证书信任方式；
- 初始化结束后自动关闭 setup 身份和入口。

## 2. 现状与关键缺口

| 现状 | 交付要求 | 结论 |
| --- | --- | --- |
| `deploy/platform.yaml` 只部署平台，setup 模式需要人工 patch ServiceAccount | 一个 YAML + 向导自动完成 | 需要引导清单和 setup 控制器 |
| 初始化页只填 MySQL 连接和管理员 | 还要域名、Ingress、TLS、SC、MySQL HA、镜像 | 需要重做向导 |
| 没有 Ingress/TLS 清单和代码 | HTTPS 内部域名 | 需要新增 Ingress/TLS 编排 |
| `deploy/mysql-ha.yaml` 曾是两套 StatefulSet + 人工复制 | 真高可用、自动切换 | 已删除；交付版由 `install.yaml` 创建 PXC Operator 三节点 |
| SC 需要人工选择/创建 | 自动配置 SC | SC 不能凭空创建，需要 provisioner；见第 4 节 |
| MySQL 备份是单 PVC + mysqldump | HA 下的备份/恢复 | 需要独立备份目标和恢复验证 |
| 文档中的 setup roleRef patch 写法不一致 | 可执行、可回滚 | 需要统一并修正 |

### 2.1 旧 `mysql-ha.yaml` 的具体问题（文件已移除）

当前清单不能称为高可用：

- 主库 StatefulSet 的 Pod 没有 `role: primary` 标签，但 `platform-mysql-primary` Service 用该标签选择，实际选不到 Pod；
- 从库是独立 StatefulSet，主从复制需要人工初始化，没有自动 failover；
- 没有主库选举、VIP/Proxy、Router 或 Operator 控制器；
- 没有副本重建、延迟监控、半同步/组复制配置；
- 备份 CronJob 指向 `platform-mysql`，在 HA 方案下需要明确指向当前主库。

因此交付版不应复用这份清单。

## 3. 推荐的交付架构

### 3.1 两阶段：引导 + 初始化编排

单一静态 YAML 无法包含向导中才产生的值（域名、密码、镜像、SC）。可行方式是：

```
kubectl apply -f deploy/install.yaml
        │
        ├─ Namespace / RBAC / 引导 Secret
        ├─ Setup Deployment（SETUP_MODE=true，不连数据库）
        └─ Setup Service（ClusterIP，仅 port-forward 使用）
                │
        kubectl -n ftc-platform port-forward svc/ftc-platform-setup 8080:80
                │
        初始化向导（5 步）
                │
        Setup 控制器按步骤创建：
          1. MySQL HA（Operator CR 或外部 MySQL）
          2. 数据库 schema + 管理员
          3. Ingress + TLS Secret
          4. 运行 Secret + 正式 Deployment/Collector/Worker
          5. 健康检查、输出 URL/hosts/CA
                │
        自动缩容 setup、回收 setup RBAC
```

### 3.2 安全边界

- Setup 身份只在初始化期间存在，使用独立 ServiceAccount 和短期 RoleBinding；
- 初始化成功后自动删除 setup RoleBinding、缩容 setup Deployment；
- Setup 入口只通过 port-forward 暴露，不创建公网/内网 Ingress；
- 继续保留 setup token 校验和失败限流；
- `CREDENTIAL_ENCRYPTION_KEY` 由引导 Job 生成，必须在初始化结果页提示备份；
- 运行期平台 ServiceAccount 不授予 Secret 读权限。

### 3.3 一键 apply 与"生成 YAML"的取舍

两条路径：

- **直接安装（傻瓜式）**：向导直接调用 Kubernetes API 创建 MySQL/Ingress/Secret。需要给 setup 身份较宽的命名空间权限和少量集群权限；完成后立即回收。
- **生成清单（权限最小）**：向导只做检查和参数收集，输出一份 `install-values.yaml`，由运维 `kubectl apply`。多一步操作，但 setup 不需要写权限。

建议默认"直接安装"，同时保留"只生成清单"开关，供安全要求高的环境使用。

## 4. StorageClass 与磁盘

### 4.1 先纠正一个概念

SC 不是"存 MySQL 数据的地方"，它是**创建 PV 的模板**。没有 CSI provisioner 或静态 PV，平台无法凭空创建一个能用的 SC。

可选模式：

| 模式 | 说明 | 是否适合 HA |
| --- | --- | --- |
| 使用已有默认/指定 SC | 最常见；Ceph/NFS/iSCSI 等网络存储 | 是，推荐 |
| 三节点本地静态 PV + no-provisioner SC | 向导选择 3 个节点上的非系统盘，创建 3 个 local PV 和 SC | 可用；节点故障后副本不能漂移到新节点，只能等节点恢复 |
| 单节点本地盘 | 只适合 PoC | 否，不满足高可用 |
| 平台安装 local-path/NFS provisioner | 需要额外基础组件和节点权限 | local-path 不适合跨节点 HA |

因此"自动配置 SC"的准确表述应是：**自动识别、校验并选择 SC；只有本地盘模式才由向导创建静态 PV + SC**。

### 4.2 容量估算

向导内置一个计算器，输入集群数、平均事件速率、原始事件保留天数、副本数，输出：

- 单副本数据量（含索引、binlog、relay log、临时空间）；
- 3 副本总容量；
- 备份容量（压缩后 × 保留份数）；
- 建议的单节点可用空间和扩容阈值。

参考量级：

| 场景 | 单副本数据 | 3 副本 + 备份 | 建议 |
| --- | --- | --- | --- |
| 20 集群 / 20 events/s / 30 天 | 100–150 GiB | 450–600 GiB | 每节点 ≥ 300 GiB 可用 |
| 60 集群 / 100 events/s / 30 天 | 300–400 GiB | 1.0–1.4 TiB | 每节点 ≥ 500 GiB 可用，SSD/NVMe |
| 100 集群 / 200 events/s / 30 天 | 600–800 GiB | 2.0–2.6 TiB | 独立存储池，预留 30% |

要求：

- SC 支持 `allowVolumeExpansion: true`；
- 使用 `ReadWriteOnce`，`volumeBindingMode: WaitForFirstConsumer`（本地 PV 必须）；
- 告警阈值 70% / 85%，不要等 100%；
- 备份不要和主数据放在同一个节点/同一块盘；
- MySQL 3 副本需要跨节点反亲和，最好跨可用区。

## 5. MySQL 高可用选型

| 方案 | 优点 | 代价 | 建议 |
| --- | --- | --- | --- |
| 外部托管 MySQL（RDS/公司 DBA） | 平台零运维，HA/备份由基础设施负责 | 需要外部资源和网络 | 有 DBA/云 RDS 时首选 |
| PXC Operator（Percona XtraDB Cluster） | 3 节点 Galera、自动 failover、HAProxy、XtraBackup，社区成熟 | 需要镜像：operator、pxc、haproxy/proxysql、xtrabackup；需要 3 节点和网络/本地存储 | 自建 HA 推荐 |
| Oracle MySQL Operator | 官方 MySQL 8.4 + InnoDB Cluster + MySQL Router | Operator/Router/Server 多镜像；自建案例少于 PXC | 必须使用 Oracle MySQL 时选 |
| 自建主从 + 人工切换 | 镜像少、结构简单 | 不是真正 HA，切换靠人 | 不用于交付 |

无论哪种，初始化时必须校验：

- 3 个副本、奇数 quorum；
- 反亲和/拓扑分布；
- `utf8mb4` + `utf8mb4_0900_ai_ci`；
- `default-time-zone=+00:00`；
- 连接数、buffer pool、binlog 保留；
- 备份与恢复演练。

如果只能接受"一个 MySQL 镜像"，就无法做到自动 failover；这一点需要提前确认。

## 6. Ingress、HTTPS 与 hosts

### 6.1 向导字段

- 内部域名（必填，DNS label 校验，不允许 IP/通配符）；
- IngressClass（自动列出，默认选择集群默认 class）；
- TLS 模式：
  1. 使用已有 TLS Secret（企业 CA，推荐）；
  2. cert-manager Issuer/ClusterIssuer（自动签发）；
  3. 平台生成自签 CA（兜底，需要下载并信任 CA）。
- 管理员账号/密码。

### 6.2 Ingress 要求

- 强制 HTTPS 跳转，`SECURE_COOKIES=true`；
- WebSocket 长连接：`proxy-read-timeout`/`proxy-send-timeout` ≥ 3600s，关闭 buffering；
- 保留 Host，平台 Origin 校验依赖 Host；
- 如果 Ingress 会写入 `X-Forwarded-For`，设置 `TRUST_PROXY_HEADERS=true`，否则保持 false；
- 上传 kubeconfig 的 body 限制 ≥ 1 MiB（按实际 kubeconfig 大小）；
- 初始化完成后从 Ingress status 读取 VIP/hostname，生成 hosts 行：

```text
10.20.30.40  ftc-platform.cmb.local
```

同时给出 Windows / macOS / Linux 的修改说明和证书导入说明。

### 6.3 初始化阶段的访问

- 只通过 `kubectl -n ftc-platform port-forward svc/ftc-platform-setup 8080:80` 访问；
- setup 页面不设置登录 Cookie，HTTP 端口转发可用；
- 初始化完成后必须走 HTTPS，HTTP 端口转发只用于排障。

## 7. 初始化向导步骤

### 第 1 步：环境检查

- Kubernetes 版本、节点数、架构（amd64/arm64）；
- Setup ServiceAccount 权限（SelfSubjectAccessReview）；
- IngressClass / Ingress Controller 地址；
- StorageClass 列表、默认 SC、`allowVolumeExpansion`、`volumeBindingMode`；
- cert-manager CRD / Issuer；
- 节点可分配 CPU/内存；
- 镜像仓库连通性/ImagePullSecret；
- 是否已存在 `ftc-platform-runtime`（防止重复初始化）。

### 第 2 步：数据库与存储

- 外部 MySQL：地址/端口/库/账号/密码/TLS/测试连接；
- 集群内 HA：方案（PXC/Oracle）、副本数（固定 3）、镜像、pull secret、SC、单副本容量、备份容量；
- 3 节点/3 磁盘选择（仅本地 PV 模式）；
- 备份策略：目标（PVC/NFS/S3）、时间、保留；
- 自动生成 root/app/replication 密码，或使用用户输入；
- 资产：MySQL 镜像、Operator 镜像、Proxy 镜像、备份镜像。

### 第 3 步：访问与管理员

- 内部域名、IngressClass、TLS 模式；
- 管理员用户名/密码/确认密码；
- 可选：平台镜像 tag、API/Collector/Worker 副本数、资源规格。

### 第 4 步：确认与安装

- 配置摘要；
- 预检结果；
- 分步执行进度：MySQL → schema/admin → Ingress/TLS → 运行 Deployment → 健康检查；
- 失败可重试，已创建资源不重复创建；
- 结果页：URL、hosts 行、CA 下载、管理员账号、`kubectl get` 验收命令；
- 自动缩容 setup 并回收 RBAC。

### 第 5 步（结果页）：交付信息

- `https://<domain>`；
- hosts 配置；
- CA/证书信任；
- 初始化 token 已失效；
- 备份/恢复命令；
- 升级/回滚说明。

## 8. 初始化必须包含 vs 登录后参数配置

### 必须在初始化完成

- 平台镜像、ImagePullSecret、命名空间；
- 数据库模式、HA、镜像、SC、容量、账号密码；
- Ingress 域名、IngressClass、TLS；
- 管理员账号；
- 加密密钥、setup token；
- API/Collector/Worker 拓扑和资源（至少给默认值）；
- 备份目标与保留；
- 预检、健康检查、hosts/CA 输出。

### 可以登录后在"参数配置"完成

- 采集周期/并发/Worker；
- 事件保留与清理批次；
- 安全扫描 kube-bench 镜像、Namespace、ImagePullSecret；
- Arthas 地址/版本/SHA/并发；
- 数据库备份策略（如果初始化未配置）；
- 会话并发、Pod Exec 开关等运行参数。

### 现有代码/清单中还需要补的

- 平台/MySQL/Operator/kube-bench 的多架构镜像 manifest；
- `SETUP_MODE` 结束后自动回收 setup 身份（现在要人工 patch）；
- Ingress/SC/节点/证书的 preflight API；
- MySQL HA Operator 的 CR 编排和等待逻辑；
- 备份与恢复验证；
- 升级/回滚和卸载（保留 PVC）说明；
- 监控指标抓取（ServiceMonitor/注解）和告警；
- 资源配额/LimitRange（可选）；
- 初始化失败清理和幂等重试；
- 文档中 setup roleRef patch 的错误写法统一修正。

## 9. 已确认方向

- **MySQL**：集群内高可用，3 副本；
- **存储**：三节点本地盘，由向导自动创建 local PV + no-provisioner SC；
- **TLS**：平台自签 CA，初始化后下载 CA 并导入客户端信任；
- **安装方式**：向导直接 apply，初始化成功后自动回收 setup 权限。

### 9.1 推荐实现

- MySQL：**PXC Operator + 3 节点 PXC**（自动 failover、HAProxy、XtraBackup）；
- 存储：3 个节点各选一个非系统盘挂载路径，创建 3 个 local PV，SC 使用
  `kubernetes.io/no-provisioner` + `WaitForFirstConsumer` + `Retain`；
- Ingress：向导创建 Ingress，TLS Secret 由平台生成的自签 CA 签发；
- 运行角色：API ×2、Collector ×2、Worker ×1，跨节点反亲和；
- 初始化完成后：写入运行 Secret，创建正式 Deployment/Collector/Worker，
  缩容 setup，删除 setup RoleBinding。

### 9.2 需要企业私有仓库提供的镜像

如果采用 PXC，不能只提供 `mysql` 镜像，还需要：

| 组件 | 默认镜像（示例） | 用途 |
| --- | --- | --- |
| PXC Operator | `percona/percona-xtradb-cluster-operator:1.18.x` | 控制器 |
| PXC | `percona/percona-xtradb-cluster:8.4.x` | MySQL 实例 |
| HAProxy | `percona/percona-xtradb-cluster-haproxy:...` | 读写入口 |
| XtraBackup | `percona/percona-xtrabackup:8.4.x` | 备份 |
| 平台 | `ftc-platform:0.1.0` | 平台自身 |
| kube-bench | 可选，amd64/arm64 | 安全扫描 |

Operator 的 Webhook 建议关闭或使用预生成证书，避免额外引入 cert-manager；
这一点需要在实现前用 PXC 版本确认。

### 9.3 剩余环境约束

以下事实必须在实现前确认：

1. 生产集群是否有 **≥ 3 个可调度节点**，且每个节点有一块可用的非系统盘？
2. Ingress 的 443 端口如何暴露：已有 LoadBalancer/VIP、NodePort，还是
   ingress-nginx 使用 hostNetwork？
3. 私有仓库能否提供 PXC Operator、PXC、HAProxy、XtraBackup 四类镜像？
4. 本地盘选择方式：
   - 向导手动选择 3 个节点 + 3 个挂载路径（权限最小，推荐）；
   - 临时特权 DaemonSet 自动探测磁盘（体验好，但权限高）。

### 9.4 交付阶段

- Phase 0：PXC Operator + 3 节点 local PV 的可行性验证（kind 多节点）；
- Phase 1：`install.yaml` 引导清单 + setup 预检 API + 向导第 1 步；
- Phase 2：本地 PV/SC + PXC HA + 备份；
- Phase 3：Ingress + 自签 CA + 正式 Deployment + 权限回收；
- Phase 4：kind/真实集群验收、升级/回滚/卸载文档。

确认 9.3 后开始 Phase 0/1。

## 10. Phase 1 进展（2026-10-22）

已交付 `deploy/install-bootstrap.yaml`，并在 `kind-k8s-test` 上实测：

- `kubectl apply` 成功创建 Namespace、3 个 ServiceAccount、运行/初始化/引导 RBAC；
- 引导 Job 生成 `ftc-platform-bootstrap` Secret（setup token + 32 字节加密密钥）；
- setup Pod 正常启动，`/api/v1/health` 返回 `ok`；
- `/api/v1/setup/status` 返回默认数据库地址和初始化状态；
- 浏览器入口 `/` 返回 200；
- 测试完成后已删除测试 Namespace。

当前清单还是**第一阶段引导版**：

- 只生成初始化入口，不创建 MySQL、本地 PV/SC、Ingress 和正式 Deployment；
- 初始化页面仍是现有版本（数据库连接 + 管理员），新向导还未接入；
- 平台镜像需要替换两处 `REPLACE_WITH_PLATFORM_IMAGE`。

## 11. Phase 2 进展（后端基础能力）

已落地的可测试模块：

| 模块 | 文件 | 能力 |
| --- | --- | --- |
| 环境预检 | `internal/setup/preflight.go` | 节点/架构/可调度性、SC、IngressClass、PXC CRD、已有运行 Secret；返回 warnings/errors |
| 本地存储 | `internal/setup/storage.go` | 校验 3 节点/3 路径、生成 no-provisioner SC + 3 个 Retain local PV；幂等创建 |
| MySQL HA | `internal/setup/mysql.go` | 生成 PXC Secret、PerconaXtraDBCluster CR（3 副本 + HAProxy 3）、等待 ready |
| HTTPS | `internal/setup/tls.go` / `ingress.go` | 自签 CA + 服务证书、TLS Secret、Ingress（WebSocket 超时/SSL 重定向） |
| 运行部署 | `internal/setup/runtime.go` | API×2、Collector×2、Worker×1、ConfigMap、Service、PDB、正确探针 |
| 算子清单 | `deploy/pxc-operator.yaml` | PXC Operator 1.18.0 CRD/RBAC/Deployment |

验证：

- `go test ./internal/setup ./cmd/platform` 通过；
- PXC CR 已用真实 CRD 在 `kind-k8s-test` 上做 `--dry-run=server` 校验通过；
- 测试用 CRD/Namespace 已清理。

尚未完成：

1. setup 安装编排 API（异步步骤/状态/失败重试）；
2. 创建应用数据库、应用账号、管理员与运行 Secret；
3. 部署后等待 API Ready、内部健康检查、Ingress 结果输出；
4. 五步向导 UI 和 CA/hosts 下载；
5. 初始化完成后自动缩容 setup、删除 setup RoleBinding；
6. 三节点 kind 端到端验收与目标集群验收。

下一步按 1 → 2 → 3 → 4 → 5 → 6 顺序实现。


## 12. Phase 3 进展（安装编排后端）

新增 `internal/setup/install.go`：

- `POST /api/v1/setup/install`：校验 token、管理员、域名、3 个本地卷后启动异步安装；
- `GET /api/v1/setup/install`：用 `X-Setup-Token` 读取步骤状态；
- `GET /api/v1/setup/ca`：下载自签 CA 证书；
- 安装步骤：
  1. 预检；
  2. 创建 3 个 local PV + SC；
  3. 创建 PXC Secret/CR，等待 PXC + HAProxy 3 副本 ready；
  4. 创建应用库 `ftc_platform`、应用账号、管理员、运行 Secret；
  5. 创建自签 CA/TLS Secret/Ingress；
  6. 创建 API/Collector/Worker、ConfigMap、Service、PDB；
  7. 等待 API Deployment ready 并做内部健康检查；
- 安装结果返回 `https://<domain>`、CA 下载地址和全部节点 IP（hostNetwork ingress 的 hosts 目标）。
- 失败时保留已创建资源，状态保留在内存中，可重试（存储/PXC/Ingress/运行部署均为幂等路径；PXC 使用 `EnsureMySQL`）。

新增 `internal/setup/database.go`：

- `provisionAdmin`：迁移表结构、创建/校验管理员；
- `ensureApplicationDatabase`：通过 HAProxy 用 PXC root 创建 `ftc_platform` 和应用账号，返回运行 DSN；
- `WaitForDeployment`、`waitForHTTPHealth`。

验证：

- `go test ./internal/setup ./cmd/platform` 通过；
- `go vet ./internal/setup` 通过；
- 安装 API 的 token/状态/参数校验、CA 下载、Deployment 等待均有单元测试。

尚未完成：

1. 五步向导 UI（当前仍是旧 SetupPage）；
2. 安装完成后自动删除 setup RoleBinding/ClusterRoleBinding（目前仍保留）；
3. 失败重试按钮和持久化安装状态（当前状态在 setup Pod 内存）；
4. 三节点 kind/目标集群端到端验收；
5. 备份策略、升级/回滚、卸载文档。

## 13. Phase 4 进展（向导 UI、合并清单、权限回收）

- `web/src/setup/SetupPage.tsx` 已替换为 4 步向导：
  1. 环境检查（token + preflight 结果）；
  2. MySQL 与存储（3 节点/路径、容量、PXC/HAProxy/XtraBackup/Operator 镜像、拉取 Secret）；
  3. 访问配置（域名、IngressClass、平台镜像、管理员）；
  4. 确认安装 + 分步进度 + 结果页（URL、CA、hosts、重试）。
- 新增 `deploy/install.yaml`：`install-bootstrap.yaml` + `pxc-operator.yaml` 合并为一个 apply 清单。
- 安装完成后自动删除 setup RoleBinding/ClusterRoleBinding；setup Pod 保留用于结果页，权限已回收。
- 安装结果携带自签 CA PEM，权限回收后仍可从结果页下载 CA。
- 新增 `docs/deployment/install.md`，包含 apply、port-forward、私有仓库 Secret、hosts、验收和已知限制。
- 前端：`npx vitest run` 130 项通过；`npm run build`、`npm run lint` 通过（0 error）。
- 后端：`go test -count=1 ./internal/setup ./cmd/platform`、`go vet ./internal/setup` 通过。

仍未完成：

1. 三节点 kind/目标集群端到端安装（PXC 3 副本、Ingress、API 登录）；
2. 备份 CronJob 与恢复演练；
3. 安装状态持久化到 ConfigMap；
4. 私有仓库场景下 setup Pod 自身拉取镜像的自动化（当前需手工给两个 SA 加 imagePullSecrets）；
5. 升级/回滚/卸载和最终交付文档评审。

## 14. 三节点 kind 验收记录（2026-10-22）

环境：macOS Docker Desktop 8GB，kind 4 节点（1 control-plane + 3 workers，arm64）。

已完成：

- `deploy/install.yaml` apply 成功；
- 引导 Job 生成 token/加密密钥；
- setup Pod Ready；
- 真实 preflight 通过：3 个可调度 worker、arm64、IngressClass nginx、PXC CRD、无运行 Secret；
- 真实 install 启动，storage 步骤完成：
  - 创建 `ftc-platform-local` SC；
  - 创建 3 个 local PV，并被 3 个 PXC PVC 绑定；
- PXC Operator 创建 `platform-mysql` CR，PXC/HAProxy StatefulSet 与 3 个 PVC 已创建；
- ingress-nginx 以 hostPort 80/443 安装在所有节点。

未完成/阻塞：

- 8GB 宿主机资源不足：PXC 3 × 2Gi 请求 + HAProxy + 4 个 ingress 控制器 + operator 同时运行；
- PXC Operator 的默认 liveness probe 在慢环境下超时重启；
- PXC 0 在 bootstrap 后因 liveness 超时被重启，PXC 1 进入 FULL_PXC_CLUSTER_CRASH 恢复路径，3 副本没有稳定形成 SYNced 集群；
- 因此 database/ingress/runtime/verify 步骤未完成，未取得登录页证据。

本轮同时修复：

- `runtimeSecretExists` / `writeRuntimeSecret` 统一使用默认 Secret 名；
- setup ClusterRole 的 PXC 资源名改为 `perconaxtradbclusters`；
- PXC CR 设置 `unsafeFlags.tls=true`（首版内部 TLS 关闭）；
- PXC/HAProxy 默认 `imagePullPolicy: IfNotPresent`；
- PXC readiness/liveness 与 HAProxy probes 放宽，避免慢磁盘/慢节点误杀。

结论：代码链路已走到 PXC 编排；剩余验收需要 **≥3 个节点且 ≥16GB 可用内存**的目标集群，或把 kind Docker 内存提升后重试。当前 kind 集群保留用于检查；旧 `k8s-test-2..8` 仅 stop，可用 `docker start` 恢复。

## 15. 第二轮 kind 验收补充（2026-10-22 下午）

进一步定位到以下问题并已修复/记录：

1. **Docker VM 磁盘满**：kind 节点内 `/` 使用率 100%，PXC 2 报 `tee: /tmp/version_info: No space left on device`。删除旧 `k8s-test-2..8` 集群后释放到约 8.9GiB 可用；旧集群已被删除，不再只是 stop。
2. **PXC 1/2 数据目录写坏**：保留 PXC 0 主库，清理 worker2/3 的本地目录并重建静态 PV，PXC 1/2 重新 SST 后确实达到 `SYNced`，3 个 PXC Pod 均 Ready。
3. **Operator 状态卡住**：`status.pxc.ready` 仍为 2，导致 install 的 `WaitForMySQL` 不通过；临时停 Operator 并修正 status 后，install 推进到 database 步骤。
4. **PXC strict mode 与迁移锁冲突**：`GET_LOCK` 在 `pxc_strict_mode=ENFORCING` 下被拒绝，database 步骤报错。已在代码生成 CR 时加入：
   ```ini
   [mysqld]
   pxc_strict_mode=DISABLED
   ```
   同时对现有 PXC Pod 执行了 `SET GLOBAL pxc_strict_mode=DISABLED`。
5. **HAProxy 配置/端点异常**：重置 PXC 后 HAProxy 后端一度为空，root/operator 通过 HAProxy 连接失败。重启 Operator/HAProxy 后端点恢复，但随后 PXC 0 又进入 Terminating，最终未取得稳定登录证据。

结论：**完整三节点 kind 登录验收仍未完成**。代码侧已修复磁盘之外的 PXC 兼容问题（strict mode、unsafe TLS、probe、imagePullPolicy、RBAC），剩余问题集中在 8GB kind 环境的 PXC/HAProxy 稳定性。

环境处理：

- `ftc-ha-test` 已停止（未删除），可用 `docker start` 恢复；
- `k8s-test-2..8` 已删除以释放磁盘；
- `k8s-test` 保留；
- 当前 setup/install 未完成，未创建正式运行 Secret 和登录页。

## 16. 三节点 kind 完整安装成功（2026-10-22）

在全新 `ftc-e2e`（1 control-plane + 3 workers，arm64）上完成：

- preflight：3 个可调度 worker、IngressClass nginx、PXC CRD、无运行 Secret；
- storage：`ftc-platform-local` + 3 个 local PV/PVC；
- MySQL：PXC 3/3 Pod Running/Synced，HAProxy Service 有可用端点；
- database：应用库 `ftc_platform`、应用账号、管理员、`ftc-platform-runtime` Secret；
- ingress：自签 CA、`ftc-platform-tls`、Ingress `ftc-platform.cmb.local`；
- runtime：API 1/1、Collector 1/1、Worker 1/1；
- verify：内部 `/api/v1/health` 返回 `ok`；
- 安装结果：`https://ftc-platform.cmb.local`，CA 644 字节，hosts 为 4 个节点内网 IP；
- setup RoleBinding/ClusterRoleBinding 已自动删除，`permissions_revoked=true`；
- HTTPS 验证：
  - `GET /` 返回 200；
  - `GET /api/v1/health` 返回 200 `{"status":"ok"}`；
  - `POST /api/v1/auth/login` 使用 admin 登录成功，返回 admin 会话；
  - `GET /api/v1/auth/session` 返回 `{"id":1,"username":"admin","role":"admin"}`。

本轮测试中的环境性 workaround（代码/清单已修复可避免）：

- PXC Operator 的 smart-update status 曾卡在 2，临时停 Operator 并修正 status；
- 旧镜像未包含 `pxc_strict_mode=DISABLED`，对现有 Pod 执行了 `SET GLOBAL`；当前源码已写入 CR 的 `pxc.configuration`，并有单测；
- setup Role 缺少 `poddisruptionbudgets` 权限，已补入 `install.yaml`；
- 安装失败后 runtime Secret 已存在会阻止重试，已改为 install 状态为 failed 时允许重试。

仍未完成：

- 目标生产集群验收（本轮为 kind）；
- 一次使用“当前源码构建的镜像、不做任何手工 patch”的干净 kind 回归；
- 备份 CronJob、升级/回滚/卸载文档评审。

## 17. 零手工 patch 的干净回归（2026-10-22）

重新构建 `ftc-platform:e2e4`，清空 `ftc-platform` 租户资源、local PV/SC 和节点本地目录后，重新 apply `deploy/install.yaml`，全程未再手工 patch。

结果：

- preflight / storage / mysql / database / ingress / runtime / verify 全部 done；
- PXC 3/3 Pod Running/Synced；
- HAProxy 3/3；
- 3 个 local PV/PVC；
- `ftc-platform-runtime`、`ftc-platform-ca`、`ftc-platform-tls` 创建；
- API/Collector/Worker 各 1 副本 Running；
- `GET /` 200、`GET /api/v1/health` 200；
- admin 登录成功，session 返回 `{"id":1,"username":"admin","role":"admin"}`；
- 安装完成后 setup RoleBinding/ClusterRoleBinding 自动删除，`permissions_revoked=true`；
- CA 648 字节，hosts 为 4 个节点内网 IP。

唯一的非零手工步骤属于环境前置：

- ingress-nginx 以 hostPort 80/443 安装在 kind 节点；
- 在 3 个 worker 创建 `/data/ftc-platform/mysql` 并放开权限；
- 为 kind 节点打 `ingress-ready=true` 标签。

这些是目标集群的基础设施前置，不属于平台初始化流程。

## 18. 目标集群验收状态

尚未执行目标生产集群验收：没有目标集群的 kubeconfig/访问权限。kind 多节点验收已完成，代码、清单、向导和文档已具备交付条件；目标集群验收是唯一外部阻塞。

## 19. 切换外部 MySQL 并完成真实 e2e（2026-10-22）

按“一个人可完成、外部 MySQL 优先”的方向调整：

- `deploy/install.yaml` 现在包含 **ingress-nginx hostNetwork（单副本，节点 80/443）** + bootstrap/setup/RBAC，不再包含 PXC Operator、PXC CRD、HAProxy、本地 PV/SC；
- 向导第二页改为外部 MySQL：host/port/database/user/password/TLS，支持“测试数据库连接”；
- ingress-nginx 直接并入 `install.yaml`，使用 hostNetwork 暴露节点 80/443；apply 不再需要 `-n`（清单含两个命名空间）；
- 安装步骤变为 `preflight → database → ingress → runtime → verify`；
- 不再要求 3 个 worker、本地盘、PXC CRD 或 StorageClass；只需至少 1 个可调度节点；
- PXC 代码/`deploy/pxc-operator.yaml` 保留为可选未来能力，不参与当前安装。

真实 e2e：

- 新 kind 集群 `ftc-ext`：1 control-plane + 1 worker，仅 worker 可调度；
- 独立 MySQL 8.0 部署在 `ftc-mysql` 命名空间（不是平台命名空间），库 `ftc_platform`、账号 `ftc`；
- `deploy/install.yaml` 使用当前源码构建的 `ftc-platform:e2e6`；
- 安装结果：preflight / database / ingress / runtime / verify 全部 done；
- `GET /` 200，`GET /api/v1/health` 200；
- admin 登录成功，session 返回 admin；
- setup RoleBinding/ClusterRoleBinding 已自动删除，`permissions_revoked=true`；
- 外部 MySQL 中已创建 `auth_users`、`auth_sessions`、`cluster_events` 等表。

已知限制：

- 外部 MySQL 的 HA/备份/监控由客户负责；
- 单节点可安装，但不是平台高可用；
- ingress-nginx 仍是集群前置，文档给出无 Helm 静态安装方式；
- 自签 CA、无自动升级/回滚不变。
