# FTC 平台交付：普通 Kubernetes

标准交付入口是 [`scripts/deploy-platform.sh`](../../scripts/deploy-platform.sh)，底层使用 [`deploy/install.yaml`](../../deploy/install.yaml) 渲染临时清单。安装清单只部署平台初始化服务与必要 RBAC，不部署 MySQL；脚本会根据节点架构选择 `ftc-platform:v1.1.0-amd64` 或 `ftc-platform:v1.1.0-arm64`。

## 1. 前置条件

- 至少一个可调度的 `amd64` 节点；
- 已准备 MySQL 8.0/8.4 或企业内部兼容 MySQL 的 HA 数据库；
- 平台 Pod 能访问 MySQL TCP 端口；
- 客户端能访问节点 TCP `30080` 和 `30443`；
- 对应 CPU 架构的平台镜像已经导入每个可能调度平台 Pod 的节点运行时。

MySQL 管理员先创建库和账号：

```sql
CREATE DATABASE ftc_platform CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE USER 'ftc'@'%' IDENTIFIED BY '<强密码>';
GRANT ALL PRIVILEGES ON ftc_platform.* TO 'ftc'@'%';
FLUSH PRIVILEGES;
```

平台会自动初始化表结构，不会在 Kubernetes 集群中创建 MySQL、PVC 或 StorageClass。

## 2. 部署

推荐使用自动架构部署入口。脚本只面向普通 Kubernetes，不处理 kind，也不会把本地镜像自动导入远程节点：

```bash
chmod +x scripts/deploy-platform.sh
./scripts/deploy-platform.sh
```

脚本会读取 Ready 节点的 `kubernetes.io/arch`：

- `amd64` 集群使用 `ftc-platform:v1.1.0-amd64`；
- `arm64` 集群使用 `ftc-platform:v1.1.0-arm64`；
- 混合架构集群默认停止，避免单架构镜像被调度到错误节点；
- 原始 `deploy/install.yaml` 不会被修改，部署使用退出后自动清理的临时清单。

运行脚本前，必须把对应镜像导入所有可能调度平台 Pod 的节点。脚本只会用中文显示所需镜像和本地镜像包位置，无法通过 Kubernetes API 验证节点运行时内的镜像是否完整。非交互环境确认导入完成后使用：

```bash
./scripts/deploy-platform.sh --yes
```

仅检查架构和渲染结果、不部署：

```bash
./scripts/deploy-platform.sh --dry-run
```

指定其他 Context 或版本：

```bash
./scripts/deploy-platform.sh --context <context名称> --version v1.1.0
```

部署完成后，脚本会列出节点地址。初始化页面必须通过客户端可达的节点 IP 和 NodePort 访问：

```text
https://<可达节点IP>:30080
```

普通 Kubernetes 部署不要使用 `https://127.0.0.1:30080`。`127.0.0.1` 指向访问者自己的主机，不是 Kubernetes 节点。

### 2.1 手工部署

如果不使用部署脚本，也可以先根据节点 CPU 架构修改 `deploy/install.yaml` 中唯一的平台镜像标签，然后执行：

```bash
kubectl get nodes -o wide
kubectl apply -f deploy/install.yaml
kubectl -n ftc-platform rollout status deployment/ftc-platform-setup --timeout=5m
kubectl -n ftc-platform get pod,svc
```

## 3. 初始化

浏览器打开：

```text
https://<可达节点IP>:30080
```

节点 IP 从 `kubectl get nodes -o wide` 的 `INTERNAL-IP` 或经网络映射后可达的节点 IP 中选择。不使用 `127.0.0.1`，初始化证书也不包含回环地址。

页面直接进入初始化并自动预检，不需要初始化 Token。填写外部 MySQL 的真实参数：

| 字段 | 说明 |
| --- | --- |
| 地址 | 平台 Pod 可访问的 MySQL DNS 或 IP |
| 端口 | 默认 `3306` |
| 数据库 | 默认 `ftc_platform` |
| 用户名 | 例如 `ftc` |
| 密码 | MySQL 账号密码 |
| TLS | 按公司 MySQL 策略选择 |

先点击“测试数据库连接”。成功后填写内部域名、NodePort `30443` 和第一个管理员。管理员密码至少 6 个字符，且不能超过 72 字节。

初始化期间 `30080` 是未认证的一次性管理入口，只能在管理网或防火墙白名单内开放。验证正式入口可登录后，立即在页面完成收尾，平台会删除 Setup Service、Deployment 和高权限 RBAC 绑定。

## 4. 正式入口

将初始化时填写的域名解析到可访问 NodePort 的节点 IP，再访问：

```text
https://<初始化时填写的域名>:30443
```

证书 SAN 只包含初始化时填写的域名。域名未解析时，浏览器会在 DNS 阶段失败，不会出现证书提示。
