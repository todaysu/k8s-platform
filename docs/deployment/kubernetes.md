# FTC 集群运维平台：Kubernetes 快速部署指南

本文只解决一件事：把 FTC 集群运维平台和 MySQL 正确部署到 Kubernetes，并完成第一次登录。

安全扫描、Pod 终端、Arthas、备份、高可用和 60+ 集群扩容不属于首次部署的必需步骤，统一放在文末“上线后按需配置”中。

## 1. 推荐部署方式

第一版推荐使用以下组合：

- 平台部署在 Kubernetes 中；
- MySQL 8.4 部署在同一集群的 `ftc-platform` Namespace；
- 第一次打开页面时完成数据库初始化并创建管理员；
- 初始化完成后切回普通运行身份；
- 平台表结构和索引由程序自动创建，不需要手工执行 SQL。

如果公司已经提供 MySQL，可跳过第 4 节，初始化页面中填写公司 MySQL 地址即可。

## 2. 部署前只需准备四项

| 配置 | 用途 | 要求 |
| --- | --- | --- |
| 平台镜像 | 运行 FTC 平台 | 推送到目标集群可访问的镜像仓库 |
| MySQL 密码 | 集群内 MySQL 的 root 和应用账号密码 | 不要提交到 Git |
| 凭据加密密钥 | 加密保存纳管集群的 Kubeconfig | 固定保存，后续不能随意更换 |
| 初始化令牌 | 保护首次初始化页面 | 初始化完成后不再日常使用 |

检查集群是否有默认 StorageClass：

```bash
kubectl get storageclass
```

生成凭据加密密钥和初始化令牌：

```bash
openssl rand -base64 32
openssl rand -hex 16
```

然后修改以下占位符：

1. `deploy/platform.yaml`
   - 平台镜像地址；
   - `REPLACE_WITH_BASE64_32_BYTE_KEY`；
   - `REPLACE_WITH_SETUP_TOKEN`。
2. `deploy/mysql.yaml`
   - `REPLACE_WITH_ROOT_PASSWORD`；
   - `REPLACE_WITH_PASSWORD`。

检查是否还有未替换内容：

```bash
grep -n "REPLACE_WITH\|your-registry.example.com" \
  deploy/platform.yaml deploy/mysql.yaml
```

有输出时不要继续部署。

> `CREDENTIAL_ENCRYPTION_KEY` 一旦用于加密 Kubeconfig，就必须长期保持不变。丢失或更换后，原有集群凭据将无法解密。

## 3. 构建平台镜像

在项目根目录执行：

```bash
docker build -t <公司镜像仓库>/ftc-platform:0.1.0 .
docker push <公司镜像仓库>/ftc-platform:0.1.0
```

把 `deploy/platform.yaml` 中的：

```yaml
image: your-registry.example.com/ftc-platform:0.1.0
```

替换为真实镜像地址。

如果目标环境包含 x86 和 ARM 节点，平台镜像应发布为同时包含 `linux/amd64` 与 `linux/arm64` 的多架构镜像。

## 4. 部署 MySQL

如果使用公司已有 MySQL，直接跳到第 5 节。

```bash
kubectl create namespace ftc-platform \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f deploy/mysql.yaml
kubectl -n ftc-platform rollout status statefulset/platform-mysql --timeout=5m
kubectl -n ftc-platform get pod,svc,pvc
```

正常结果：

- `platform-mysql-0` 为 `1/1 Running`；
- `data-platform-mysql-0` PVC 为 `Bound`；
- MySQL 地址为 `platform-mysql.ftc-platform.svc:3306`。

检查 MySQL：

```bash
kubectl -n ftc-platform exec statefulset/platform-mysql -- sh -c \
  'mysqladmin ping -h 127.0.0.1 -u"$MYSQL_USER" -p"$MYSQL_PASSWORD"'
```

看到 `mysqld is alive` 后继续。

## 5. 部署平台

### 5.1 应用平台清单

```bash
kubectl apply -f deploy/platform.yaml
```

首次初始化需要临时使用 `ftc-platform-setup` ServiceAccount 写入运行配置 Secret。执行：

```bash
# 确保初始化身份已经绑定初始化 Role；重复执行是安全的。
kubectl -n ftc-platform create rolebinding ftc-platform-setup \
  --role=ftc-platform-setup \
  --serviceaccount=ftc-platform:ftc-platform-setup \
  --dry-run=client -o yaml | kubectl apply -f -

# 仅初始化阶段使用 setup 身份。
kubectl -n ftc-platform patch deployment ftc-platform \
  --type=merge \
  -p '{"spec":{"template":{"spec":{"serviceAccountName":"ftc-platform-setup"}}}}'

kubectl -n ftc-platform rollout status deployment/ftc-platform --timeout=5m
```

> 不要执行 `kubectl patch serviceaccount ... roleRef`。ServiceAccount 没有 `roleRef` 字段；权限关系属于 RoleBinding。

### 5.2 打开初始化页面

临时验证可使用端口转发：

```bash
kubectl -n ftc-platform port-forward svc/ftc-platform 8080:80
```

浏览器打开：

```text
http://127.0.0.1:8080
```

端口转发只用于初始化和健康检查。清单默认设置 `SECURE_COOKIES=true`，正式登录应通过 HTTPS Ingress/Route 访问。

读取初始化令牌：

```bash
kubectl -n ftc-platform get secret ftc-platform \
  -o jsonpath='{.data.setup-token}' | base64 -d; echo
```

如果使用集群内 MySQL，再读取应用账号密码：

```bash
kubectl -n ftc-platform get secret platform-mysql \
  -o jsonpath='{.data.password}' | base64 -d; echo
```

### 5.3 在页面完成初始化

集群内 MySQL 填写：

| 字段 | 值 |
| --- | --- |
| 地址 | `platform-mysql.ftc-platform.svc` |
| 端口 | `3306` |
| 数据库 | `ftc_platform` |
| 用户名 | `ftc` |
| 密码 | `platform-mysql` Secret 中的 `password` |

外部 MySQL 填写公司提供的地址、端口、数据库名、账号、密码和 TLS 配置。

然后：

1. 填写初始化令牌；
2. 设置第一个管理员用户名和密码；
3. 点击“测试连接”；
4. 测试成功后点击“初始化数据库”。

平台会自动：

- 创建全部表和索引；
- 创建管理员账号；
- 生成 `ftc-platform-runtime` Secret；
- 重启平台 Pod。

不需要手工导入 SQL。

### 5.4 初始化完成后立即切回普通身份

看到登录页后执行：

```bash
kubectl -n ftc-platform patch deployment ftc-platform \
  --type=merge \
  -p '{"spec":{"template":{"spec":{"serviceAccountName":"ftc-platform"}}}}'

kubectl -n ftc-platform rollout status deployment/ftc-platform --timeout=5m
```

这一步不能省略。`ftc-platform-setup` 可以写运行配置 Secret，只应在首次初始化时短暂使用。

## 6. 部署验收

依次执行：

```bash
kubectl -n ftc-platform get pod,svc,pvc
kubectl -n ftc-platform get secret ftc-platform-runtime
kubectl -n ftc-platform logs deployment/ftc-platform --tail=100
```

验收标准：

- 平台 Pod 为 `Running` 且 Ready；
- MySQL Pod 为 `Running`，PVC 为 `Bound`；
- `ftc-platform-runtime` Secret 存在；
- 浏览器显示正常登录页；
- 管理员可以登录；
- 日志中没有持续出现数据库连接或迁移错误。

检查健康接口：

```bash
kubectl -n ftc-platform port-forward svc/ftc-platform 8080:80
curl http://127.0.0.1:8080/api/v1/health
```

预期返回：

```json
{"data":{"status":"ok"},"error":null}
```

## 7. 常见问题

| 现象 | 优先检查 |
| --- | --- |
| MySQL Pod 一直 Pending | StorageClass、PVC 和节点磁盘容量 |
| 初始化页面无法保存 | Deployment 是否正在使用 `ftc-platform-setup` ServiceAccount |
| 数据库测试失败 | 地址、端口、账号密码、NetworkPolicy 和 MySQL 授权范围 |
| 初始化后又回到初始化页 | `ftc-platform-runtime` Secret 是否存在、Pod 是否已重启 |
| 原有集群全部连接失败 | `CREDENTIAL_ENCRYPTION_KEY` 是否被更换 |
| 登录后立即失效 | HTTPS 场景检查 `SECURE_COOKIES=true`；纯 HTTP 临时调试需明确处理 Cookie 配置 |
| 平台升级后表缺失 | 查看启动日志中的数据库迁移错误，不要手工创建部分表 |

查看 Pod 事件和日志：

```bash
kubectl -n ftc-platform describe pod -l app.kubernetes.io/name=ftc-platform
kubectl -n ftc-platform logs deployment/ftc-platform --tail=200
kubectl -n ftc-platform get events --sort-by=.lastTimestamp
```

## 8. 必须备份的内容

必须同时备份：

1. MySQL 数据库；
2. `ftc-platform-runtime` Secret；
3. `ftc-platform` Secret 中的凭据加密密钥。

只备份数据库是不够的。缺少原来的凭据加密密钥时，数据库中的 Kubeconfig 密文无法恢复。

导出 Secret 备份时，应进入公司 Secret 管理系统，不要提交到 Git：

```bash
kubectl -n ftc-platform get secret ftc-platform-runtime -o yaml
kubectl -n ftc-platform get secret ftc-platform -o yaml
```

## 9. 上线后按需配置

以下功能不影响平台第一次启动，确认基础平台稳定后再配置。

### 9.1 安全扫描

- 把 kube-bench 的 amd64/arm64 镜像同步到公司 Harbor；
- 为目标集群准备创建 Job、读取 Pod 日志和只读检查集群资源的权限；
- OCP 需要单独审批 SCC；
- 扫描 Pod 使用高权限但不常驻，结束后自动回收。

详细设计见：

- [`../plans/2026-10-20-security-baseline-design.md`](../plans/2026-10-20-security-baseline-design.md)
- [`../plans/2026-10-21-kube-bench-v1-history-design.md`](../plans/2026-10-21-kube-bench-v1-history-design.md)

### 9.2 Pod 终端与 Arthas

Pod Exec 默认关闭。平台入口完成 HTTPS 和权限控制后，再设置：

```yaml
ENABLE_POD_EXEC: "true"
```

目标 Kubeconfig 还需要 `pods/get` 和 `pods/exec/create` 权限。Arthas 应使用公司内网制品地址，不应让生产 Pod 从互联网下载工具。

### 9.3 容量分类

登录平台后，在“容量 → 业务命名空间配置”中维护业务 Namespace。该清单全局生效；未列入的 Namespace 计入平台组件容量。

### 9.4 生产高可用、备份和 60+ 集群扩容

生产环境建议使用公司高可用 MySQL，并拆分 API、Collector、Worker。详细配置、事件保留策略、采集分片、备份恢复和性能参数见：

- [`production-deployment.md`](production-deployment.md)
- [`../adr/0009-fleet-collection-architecture-at-scale.md`](../adr/0009-fleet-collection-architecture-at-scale.md)

## 10. 卸载提示

删除平台前先备份数据库和 Secret。

```bash
kubectl delete -f deploy/platform.yaml
kubectl delete -f deploy/mysql.yaml
```

删除 StatefulSet 不一定会删除 PVC。除非已经确认备份可用，否则不要手工删除 MySQL PVC。
