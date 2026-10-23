# FTC 平台交付：普通 Kubernetes

这套交付使用平台自身的 HTTPS 和两个 NodePort。无需安装 Ingress Controller；数据库使用已有的 MySQL 8.0+。初始化入口为 `30080`，正式入口默认 `30443`。平台会生成私有 CA 与服务证书，客户端首次访问需信任 CA。

## 五步安装

1. 准备一个 Kubernetes 集群、一套从集群 Pod 可访问的 MySQL 8.0+ 数据库，以及一个能解析到可访问 NodePort 的节点 IP 的域名。MySQL 账号需有目标库的建表、改表和索引权限。平台不会创建 MySQL 服务、数据库或账号。
2. 在已生成的私有清单 `data/delivery/install.yaml` 中，将唯一的 `REPLACE_WITH_PLATFORM_IMAGE` 改成目标集群可拉取的平台镜像。此文件包含随机初始化凭证，权限为 `0600`，不要提交到 Git 或发送给无关人员。私有仓库的 `imagePullSecrets` 可加在 `ftc-platform-setup` Pod 中；初始化器会把同一凭据传给运行实例和证书续签任务。
3. 执行 `kubectl apply -f data/delivery/install.yaml`，再执行 `kubectl -n ftc-platform rollout status deployment/ftc-platform-setup --timeout=5m`。浏览器打开 `https://<节点IP>:30080`。首次打开会提示未受信任证书；这是初始化入口自动生成的自签证书。
4. 从私有清单的 `ftc-platform-bootstrap` Secret `setup-token` 字段读取初始化凭证，在页面输入。填写 MySQL 地址、库名和账号、正式域名、HTTPS NodePort（默认 `30443`）、管理员密码，点击安装。平台会迁移表结构、检查已有采集分片配置、生成正式 CA/证书，并创建 API、Collector、Worker 和续签任务。刷新浏览器后可凭初始化凭证继续查看进度；失败后修复原因并重新提交，不会清空已有数据或更换有效 CA。
5. 安装完成后下载 CA 证书并导入客户端信任库。确认域名指向可访问 `30443` 的节点，然后访问 `https://<域名>:30443` 登录。返回初始化页勾选已登录，点击“完成并关闭初始化入口”；安装器删除初始化 Service/Deployment 并回收初始化权限。

制作后续交付包时，发布方运行 `go run ./cmd/installer`，从公开模板生成一份随机凭证的私有清单；当前工作区已生成该文件。生成器不会覆盖现有私有清单。重试安装时使用同一份清单，不要重新生成凭证。若数据库已经存有与本版 `16` 个采集分片不同的配置，安装器会停止并报告，不会自动改写分片映射。

## 网络与证书

`30080/TCP` 仅用于首次初始化；`30443/TCP` 是正式平台入口。NodePort 需要目标网络和防火墙放行。安装页面会列出节点内网 IP；请选择从客户端实际可达的节点。集群内的正式 Service 使用 HTTPS `443`，API Pod 监听 `8443`，健康探针使用 Pod 内部 HTTP `8081`。Collector 与 Worker 不对外开放业务端口。

正式证书的 SAN 包含配置的域名，浏览器通过域名访问。`hosts` 文件或企业 DNS 都可提供解析，安装器不会修改客户端 DNS。安装器在集群 Secret 中保存原 CA 和私钥；每周定期检查证书，在到期前使用同一 CA 续签服务证书。API 从 Secret 挂载文件热加载新证书。若 CA 丢失，续签会失败并保留现状，需要从备份恢复原 CA。

## 排查

- `kubectl -n ftc-platform get pod,svc,deploy,cronjob`：查看初始化入口或运行组件状态。
- `kubectl -n ftc-platform logs deploy/ftc-platform-setup`：查看初始化器错误，日志不包含初始化凭证或数据库密码。
- 若浏览器无法打开初始化页，检查节点 `30080` 可达性及 Pod 就绪；若正式页不可达，检查 `30443`、域名解析、API Pod 和 TLS Secret。
- 若 MySQL 失败，确认 Pod 到 MySQL 的路由、账号权限、数据库已存在及 TLS 模式。安装进度页会保留具体失败步骤。
- 若续签失败，检查 `kubectl -n ftc-platform get jobs` 和证书任务日志，并确认 `ftc-platform-ca` Secret 还在。

旧的 `deploy/platform.yaml`、`deploy/install-bootstrap.yaml`、Ingress 和集群内 MySQL 清单只供已有环境维护或本地开发；新交付入口统一使用本指南和生成的私有清单。升级前备份 MySQL、`ftc-platform-bootstrap` 与 `ftc-platform-ca` Secret。
