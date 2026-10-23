# FTC v1 Kind 本地验收环境

该目录提供一个 control-plane + 两个 worker 的三节点 Kind 环境，仅用于本地功能验收。MySQL 使用单副本和 Kind `standard` StorageClass，不代表生产 HA 方案。

默认使用开发机已缓存的 `kindest/node:v1.24.17`；需要验证其他 Kubernetes 版本时，执行 `make kind-create KIND_NODE_IMAGE=kindest/node:<tag>`。

## 创建与部署

```bash
make package-images
make kind-create
make kind-load
go run ./cmd/installer
make kind-deploy
```

`cmd/installer` 生成含随机初始化令牌的 `data/delivery/install.yaml`；该文件只存在本机，不会进入 Git。

## 进入初始化向导

1. 打开 `https://127.0.0.1:30080`。
2. 从 `data/delivery/install.yaml` 的 `setup-token` 字段取初始化令牌。
3. 数据库地址填写：
   - 主机：`platform-mysql.ftc-platform.svc`
   - 端口：`3306`
   - 数据库：`ftc_platform`
   - 用户名：`ftc`
   - 密码：`ftc-kind-v1`
   - TLS：关闭（仅本地 Kind）
4. 内部域名可使用 `ftc.kind.local`，正式入口 NodePort 使用 `30443`。

初始化完成后，在本机 `/etc/hosts` 加入：

```text
127.0.0.1 ftc.kind.local
```

然后访问 `https://ftc.kind.local:30443`。

## 验收命令

```bash
kubectl --context kind-ftc-v1 get nodes
kubectl --context kind-ftc-v1 -n ftc-platform get pods,svc,pvc
kubectl --context kind-ftc-v1 -n ftc-platform logs deployment/ftc-platform-setup
```
