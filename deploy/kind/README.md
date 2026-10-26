# FTC 集群运维平台 v1.0.1 部署与验收手册

当前版本使用一份完整清单 [`deploy/install.yaml`](../install.yaml) 完成部署：

- 不需要生成私有安装清单；
- 不需要初始化 Token；
- 部署后直接打开初始化页面；
- 清单包含平台初始化服务和必要 RBAC；
- 初始化完成后自动创建 API、Collector、Worker、HTTPS Service 和证书。

> MySQL 是外部必需依赖。部署清单不创建 MySQL、PVC 或 StorageClass。

## 1. 默认资源

| 项目 | 默认值 |
| --- | --- |
| 平台镜像 | `ftc-platform:v1.0.1-amd64` |
| 初始化入口 | `https://<节点IP>:30080` |
| 正式入口 | `https://<初始化时填写的域名>:30443` |
| API | 2 副本 |
| Collector | 2 副本 |
| Worker | 1 副本 |

## 2. x86 普通 Kubernetes 集群部署

### 2.1 检查前置条件

```bash
kubectl get nodes -o wide
```

验收条件：

- 至少一个可调度的 `amd64` 节点；
- 已准备平台 Pod 可访问的外部 MySQL 8.0/8.4；
- 客户端能访问节点的 TCP `30080` 和 `30443`；
- 节点能拉取平台镜像，或镜像已经预加载到节点运行时。

### 2.2 准备平台镜像

如果集群节点可以直接使用本地预加载镜像，确保每个可能调度 Setup Pod 的节点都有：

```text
ftc-platform:v1.0.1-amd64
```

如果使用内部镜像仓库，只需修改 [`deploy/install.yaml`](../install.yaml) 中唯一的平台镜像：

```yaml
image: ftc-platform:v1.1.0-amd64
```

例如改为：

```yaml
image: registry.internal/ftc/ftc-platform:v1.1.0-amd64
```

### 2.3 直接部署

```bash
kubectl apply -f deploy/install.yaml
```

不再执行 `go run ./cmd/installer`，也不需要查找或填写 setup-token。

### 2.4 等待初始化服务

```bash
kubectl -n ftc-platform rollout status deployment/ftc-platform-setup --timeout=5m
kubectl -n ftc-platform get pods,svc
```

预期：

- `ftc-platform-setup-*` 为 `1/1 Running`；
- `ftc-platform-setup` Service 暴露 `30080`。

## 3. 进入初始化页面

先选择一个客户端可访问的节点 IP：

```bash
kubectl get nodes -o wide
```

打开：

```text
https://<节点IP>:30080
```

页面会直接自动检查集群环境，不再显示初始化凭证输入框。

初始化端口在完成配置前没有 Token 保护，只应在管理网络或防火墙白名单范围内开放。确认正式平台可以登录后，应立即点击“完成并关闭初始化入口”。

## 4. 初始化页面填写值

### 4.1 MySQL

| 字段 | 值 |
| --- | --- |
| 地址 | 外部 MySQL 的 DNS 或 IP |
| 端口 | `3306` |
| 数据库 | `ftc_platform` |
| 用户名 | `ftc` |
| 密码 | MySQL 账号密码 |
| TLS | 按公司 MySQL 策略选择 |

先点击“测试数据库连接”，成功后继续。

### 4.2 访问方式

| 字段 | 示例 |
| --- | --- |
| 内部域名 | `ftc-platform.cmb.local` |
| HTTPS NodePort | `30443` |

域名必须解析到可以访问 NodePort 的节点 IP。平台证书只包含初始化时填写的域名，后续必须用完全相同的域名访问。

### 4.3 管理员

- 用户名建议为 `admin`；
- 密码至少 6 个字符，且不能超过 72 字节；
- 密码只在初始化时使用，不写入安装状态或日志。

### 4.4 安装阶段

页面依次完成：

1. 环境预检；
2. 初始化 MySQL 表结构和管理员；
3. 生成平台 CA 与域名证书；
4. 部署 API、Collector 和 Worker；
5. 验证 Deployment、采集分片和 HTTPS 健康状态。

## 5. 配置域名

假设：

- 初始化填写域名：`ftc-platform.cmb.local`；
- 可访问节点 IP：`10.20.30.40`。

本地 hosts 应加入：

```text
10.20.30.40 ftc-platform.cmb.local
```

macOS 临时测试命令：

```bash
echo '10.20.30.40 ftc-platform.cmb.local' | sudo tee -a /etc/hosts
dscacheutil -q host -a name ftc-platform.cmb.local
```

如果域名没有解析，浏览器会在 DNS 阶段失败，不会出现证书不安全提示。

## 6. 验证 30443

先绕过 DNS 和证书检查，验证 Service 本身：

```bash
curl -k --resolve ftc-platform.cmb.local:30443:10.20.30.40 \
  https://ftc-platform.cmb.local:30443/api/v1/health
```

预期：

```json
{"data":{"status":"ok"},"error":null}
```

然后验证域名解析：

```bash
curl -k https://ftc-platform.cmb.local:30443/api/v1/health
```

如果第一条成功、第二条失败，问题是 DNS/hosts；如果两条都失败，检查 Service、节点防火墙和 kube-proxy。

## 7. 导出并信任平台 CA

```bash
mkdir -p data/delivery
kubectl -n ftc-platform get secret ftc-platform-ca \
  -o jsonpath='{.data.ca\.crt}' \
  | base64 -d > data/delivery/ftc-platform-ca.crt
```

使用 CA 验证：

```bash
curl --cacert data/delivery/ftc-platform-ca.crt \
  https://ftc-platform.cmb.local:30443/api/v1/health
```

macOS 信任命令：

```bash
sudo security add-trusted-cert \
  -d -r trustRoot \
  -k /Library/Keychains/System.keychain \
  data/delivery/ftc-platform-ca.crt
```

该操作会要求系统管理员确认。只应信任当前平台导出的 CA。

## 8. 完成初始化

访问：

```text
https://<初始化时填写的域名>:30443
```

使用刚创建的管理员登录。确认登录成功后，回到初始化页面：

1. 勾选“已确认能登录正式平台”；
2. 点击“完成并关闭初始化入口”。

平台会删除 Setup Deployment、Setup Service 和临时高权限 RBAC 绑定。此后 `30080` 不能访问是正常结果。

## 9. 最终验收

```bash
kubectl get nodes
kubectl -n ftc-platform get deployments,pods,svc,pvc
```

验收标准：

- 节点全部 Ready；
- `ftc-platform` 为 `2/2`；
- `ftc-platform-collector` 为 `2/2`；
- `ftc-platform-worker` 为 `1/1`；
- `ftc-platform` Service 暴露 `30443`；
- Setup Deployment 和 Service 已删除；
- 管理员可以登录正式入口。

## 10. 本地 Kind 验收

Apple Silicon 使用 ARM64 镜像：

```bash
make kind-create
make kind-load
make kind-deploy
```

`make kind-deploy` 会将完整清单中的 amd64 镜像临时替换为 `ftc-platform:v1.0.1-arm64` 后提交，不会修改原文件。

Kind 端口映射：

```text
https://<节点 InternalIP>:30080  初始化入口
https://<初始化域名>:30443  正式入口
```

节点 IP 从 `kubectl get nodes -o wide` 的 `INTERNAL-IP` 列获取。初始化证书不再包含 `127.0.0.1`，不应把本地回环地址当作交付访问地址。

## 11. 故障排查

### ImagePullBackOff

```bash
kubectl -n ftc-platform get pods
kubectl -n ftc-platform describe pod <Pod名称>
```

确认镜像地址、架构、仓库凭据和节点网络。

### 30443 无法访问

```bash
kubectl -n ftc-platform get svc ftc-platform
kubectl -n ftc-platform get endpoints ftc-platform
kubectl -n ftc-platform logs deployment/ftc-platform --tail=100
```

同时检查：

- 使用的域名是否与初始化时填写值完全一致；
- 域名是否解析到可访问节点；
- 节点防火墙是否允许 TCP 30443；
- 云平台安全组或物理网络 ACL 是否放行；
- kube-proxy/CNI 是否正常处理 NodePort。

## 12. 回收测试环境

删除平台资源（不会删除外部 MySQL 中的数据）：

```bash
kubectl delete namespace ftc-platform
kubectl delete clusterrolebinding ftc-platform-setup --ignore-not-found
kubectl delete clusterrole ftc-platform-setup --ignore-not-found
```

Kind 环境直接执行：

```bash
kind delete cluster --name ftc-v1
```
