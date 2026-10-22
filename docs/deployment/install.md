# FTC 平台交付版安装与初始化

本文对应交付版 `deploy/install.yaml`：一个 apply 清单 + 浏览器初始化向导。

## 1. 前置条件

| 项 | 要求 |
| --- | --- |
| 节点 | ≥ 3 个可调度节点，且每个节点有一块用于 MySQL 的非系统盘 |
| 本地路径 | 例如 `/data/ftc-platform/mysql`，允许三个节点使用相同路径 |
| Ingress | ingress-nginx 已安装，并以 hostNetwork 方式在节点 443 监听 |
| IngressClass | 至少存在一个，默认 `nginx` |
| PXC Operator | 已包含在 `install.yaml` 中；镜像可来自私有仓库 |
| 镜像 | 平台、PXC Operator、PXC、HAProxy、XtraBackup 镜像 |
| 私有仓库 | 平台/MySQL 镜像拉取 Secret；Setup Pod 自身也需要能拉到平台镜像 |
| 域名 | 内部域名，例如 `ftc-platform.cmb.local` |
| hosts | 客户端把域名指向任意一个节点内网 IP |

> `deploy/mysql-ha.yaml` 是旧的实验清单，不是高可用实现，不要用于交付。

## 2. 替换镜像并 apply

`install.yaml` 中有三处 `REPLACE_WITH_PLATFORM_IMAGE`（安装 ConfigMap、引导 Job、setup Pod）。一次性替换：

```bash
sed -i 's|REPLACE_WITH_PLATFORM_IMAGE|registry.example.com/ftc/ftc-platform:0.1.0|g' deploy/install.yaml
```

如果平台镜像来自私有仓库，需要让 setup Pod 和引导 Job 能拉取镜像。推荐先把拉取 Secret 放到默认 ServiceAccount，或给 bootstrap/setup 两个 ServiceAccount 增加 `imagePullSecrets`：

```bash
kubectl apply -n ftc-platform -f deploy/install.yaml
kubectl -n ftc-platform create secret docker-registry ftc-platform-registry \
  --docker-server=registry.example.com \
  --docker-username='<user>' \
  --docker-password='<password>'
kubectl -n ftc-platform patch serviceaccount ftc-platform-bootstrap \
  -p '{"imagePullSecrets":[{"name":"ftc-platform-registry"}]}'
kubectl -n ftc-platform patch serviceaccount ftc-platform-setup \
  -p '{"imagePullSecrets":[{"name":"ftc-platform-registry"}]}'
kubectl -n ftc-platform delete pod -l app.kubernetes.io/name=ftc-platform-setup --ignore-not-found
kubectl -n ftc-platform delete job ftc-platform-bootstrap --ignore-not-found
```

> 如果镜像已预加载到节点，或仓库允许匿名拉取，可以跳过 Secret 步骤。

等待引导 Job 完成并读取初始化凭证：

```bash
kubectl -n ftc-platform wait --for=condition=complete job/ftc-platform-bootstrap --timeout=5m
kubectl -n ftc-platform logs job/ftc-platform-bootstrap
```

打开初始化页面：

```bash
kubectl -n ftc-platform port-forward svc/ftc-platform-setup 8080:80
# 浏览器打开 http://127.0.0.1:8080
```

## 3. 初始化向导

1. **环境检查**
   - 填写引导 Job 输出的 setup token；
   - 检查节点、StorageClass、IngressClass、PXC CRD、已有运行 Secret。
2. **数据库与存储**
   - 选择 3 个节点和 3 个本地盘路径；
   - 单卷容量建议 200 GiB 起；
   - 填写 PXC Operator/PXC/HAProxy/XtraBackup 私有镜像和拉取 Secret。
3. **访问配置**
   - 内部域名、IngressClass；
   - 管理员用户名/密码；
   - 可选平台运行镜像和拉取 Secret。
4. **确认安装**
   - 依次创建 local PV/SC → PXC → 应用库/管理员 → 自签 CA/Ingress → 平台 Deployment；
   - 过程约 5–15 分钟，页面显示每一步状态；
   - 完成后显示 `https://<域名>`、节点 IP 列表和 CA 下载。

初始化完成后约 30 秒，平台会自动删除：

- `RoleBinding/ftc-platform-setup`
- `ClusterRoleBinding/ftc-platform-setup`

setup 身份不能再修改集群；setup Pod 仍保留用于结果页，确认无误后可删除：

```bash
kubectl -n ftc-platform delete deployment ftc-platform-setup
kubectl -n ftc-platform delete service ftc-platform-setup
```

## 4. 客户端信任与 hosts

把结果页的 CA 保存为 `ftc-platform-ca.crt`，导入系统/浏览器受信任根证书。

hosts 示例（任选一个节点内网 IP）：

```text
10.20.30.11  ftc-platform.cmb.local
```

然后访问：

```text
https://ftc-platform.cmb.local
```

Ingress 使用 hostNetwork，节点 443 必须是 ingress-nginx 在监听；如果节点 IP 变了，hosts 也要更新。

## 5. 验收

```bash
kubectl -n ftc-platform get pvc,pv,statefulset,pod,ingress,secret
kubectl -n ftc-platform get perconaxtradbcluster platform-mysql
kubectl -n ftc-platform rollout status deployment/ftc-platform
kubectl -n ftc-platform logs deployment/ftc-platform --tail=100
```

预期：

- 3 个 `data-platform-mysql-pxc-*` PVC Bound；
- PXC 3/3、HAProxy 3/3；
- `platform-mysql-haproxy` Service 可用；
- `ftc-platform` API Deployment Ready；
- Ingress 有 TLS；
- `ftc-platform-ca` 和 `ftc-platform-tls` Secret 存在；
- 浏览器能打开登录页。

## 6. 已知限制

- 本地 PV 使用 `Retain`，不支持在线扩容；容量不足需要换盘/迁移。
- 本地盘绑定节点；节点故障时 PXC 依靠剩余 2 副本维持仲裁，但故障副本不会漂移到新节点。
- 初始化完成后 setup Pod 仍保留（权限已回收），需要手工删除。
- 备份 CronJob 尚未包含在初始化流程中；上线前需补充备份目标和恢复演练。
- 安装状态保存在 setup Pod 内存中，Pod 重启后状态页丢失；已创建的集群资源不受影响。
- 私有仓库 Secret 需要让 setup Pod 和引导 Job 都能拉取平台镜像，否则 setup 页面无法启动。
