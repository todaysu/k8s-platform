# 无 Token 单 YAML 初始化设计

## 目标

平台交付只使用 `deploy/install.yaml`。运维人员执行一次 `kubectl apply` 后直接进入初始化向导，不再生成私有安装清单，也不再读取或填写 setup-token。

## 设计

- Setup 后端在 `SETUP_TOKEN` 为空时进入无 Token 模式；保留非空 Token 的兼容能力，但标准交付清单不再配置 Token。
- 初始化页面加载后自动执行环境预检，移除凭证输入框和所有 Token 请求参数。
- Kubernetes 清单保留空的 `ftc-platform-bootstrap` Secret，由 Setup Pod 首次启动时写入随机凭据加密密钥；密钥不会出现在 YAML、页面或日志中。
- 清单包含单副本 MySQL、20Gi PVC、Setup Deployment、NodePort Service 和 RBAC，x86 测试集群可直接应用。
- Setup Pod 从自身 PodSpec 读取平台镜像，将同一镜像用于 API、Collector 和 Worker，清单只出现一次平台镜像地址。
- 初始化完成后，用户先验证正式入口，再触发收尾；Setup Service、Deployment 和高权限绑定被自动删除。

## 安全边界

取消 Token 意味着初始化期间 `30080` 是未认证的一次性管理入口。部署者必须通过管理网、主机防火墙或外部 ACL 限制访问范围，并在验证正式入口后立即关闭初始化入口。数据库中已有管理员、运行 Secret、安装 Lease 和持久安装状态继续防止重复及并发初始化。

## 数据库与存储

单 YAML 中的 MySQL 仅用于测试，依赖目标集群默认 StorageClass。通用清单无法自动推断物理机本地磁盘路径或安全地选择承载节点；没有默认 StorageClass 时，集群管理员必须先提供存储类或显式填写 `storageClassName`。生产环境使用外部或 HA MySQL。

## 验收

- Setup Pod 在没有 `SETUP_TOKEN` 环境变量时正常启动。
- 初始化页面不显示凭证字段，并自动请求预检。
- MySQL、PVC 和 Setup 可由单个 YAML 创建。
- 正式平台仍使用自签 CA、域名证书和 HTTPS NodePort 30443。
- 完成安装后 Setup 入口与高权限 RBAC 被删除。
