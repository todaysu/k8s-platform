# 普通 Kubernetes 单一交付方案与实施计划

**状态：方案已收敛，待实现；现有 deploy/install.yaml 仍是旧 Ingress 安装器，不能按本文宣称已可用。**

**Goal:** 运维只修改平台镜像地址，执行一次 apply，打开初始化页填写配置，随后通过 HTTPS 使用平台。

**Architecture:** 一个安装清单、一个平台镜像、平台原生 TLS + NodePort。初始化自动编排 API、Collector、Worker，共享外部 MySQL；不安装 ingress-nginx，不提供 OCP/Route、PXC 或其他首次交付分支。

**Tech Stack:** Go net/http/crypto/x509、client-go、Kubernetes Secret/Service/Deployment、React、现有 MySQL 存储。

## 1. 唯一用户流程

1. 修改交付文件 `install.yaml` 中唯一的 `image:` 地址。镜像已由发布方构建并可拉取，不要求客户本地构建或另找 kubectl 镜像。
2. 执行 `kubectl apply -f install.yaml`。
3. 等待初始化实例启动，打开 `https://<可达节点IP>:30080`。
4. 在初始化页填写安装凭证、外部 MySQL、正式域名和管理员，点击安装。自动检查、迁移、生成证书、部署、验证；失败在页面显示原因并支持恢复。
5. 结果页完成证书信任／域名解析引导，打开 `https://<自定义域名>:30443` 登录使用。正式入口验证可用后，在页面完成安装收尾。

用户不需要 Helm、port-forward、手工 patch SA、手工创建 TLS Secret、单独安装 Ingress、分别部署三个运行角色或执行常规收尾清理命令。排障命令只放附录。

## 2. 前置条件与无法由网页代办的工作

- 已有可用的 Kubernetes 与 apply 权限、可拉取镜像的仓库、足够的节点资源；默认 NodePort 范围允许 30080/30443，客户端到节点端口可达。
- 使用已有外部 MySQL 8.0+ 的专用数据库／账号和授权，向导负责建表，不要求用户提供 root 账号，不自动搭建数据库。
- 自定义域名需要内网 DNS 或客户端 hosts。已有解析可直接使用；没有解析时结果页提供可复制的配置，不能宣称平台自动修改客户 DNS。
- 自签证书首次需要客户端信任。网页不能静默修改系统证书库；文档／页面必须说明首次证书核验与 CA 导入流程，不能承诺任何客户端首次打开均无提示。
- 私有镜像仓库若要求认证，应使用集群已有的镜像拉取配置。没有凭据时必须提前提供，初始化页尚未启动，不能靠后续向导修复自身 ImagePullBackOff。

因此“五步”是服务器安装主流程，并不表示可以跳过数据库、网络、DNS、镜像凭据和客户端信任条件。

## 3. 单一清单与镜像地址只改一次

- 唯一交付入口是 `deploy/install.yaml`，正式交付不再要求用户在 install-bootstrap.yaml、平台 ConfigMap 和多个 Deployment 间重复替换镜像。
- 引导改为一个 setup Deployment（平台镜像），程序启动时自动初始化 bootstrap Secret、加密密钥、初始化证书与持久化状态；不依赖 shell Job 调 kubectl 打印密钥。
- Setup 从自身 Pod 获取运行镜像及 ImagePullSecrets，用于生成正式三个 Deployment，无需在页面再填写同一个平台镜像。
- 引导 Service 固定使用 HTTPS NodePort 30080，setup 监听 8443；初始化证书包含可用节点访问 IP，保存并复用 Secret，Pod 重启不重建 CA。
- 去掉 nginx 命名空间、IngressClass、admission webhook、Ingress 及相应 RBAC。仅保留平台自身资源与必要预检权限。

## 4. 初始化鉴权

不能为了省掉查日志，把 setup token 放进未认证接口或内置公开默认口令。

交付阶段生成独立、随机的安装凭证，写入本次交付的私有 install.yaml 中的专用 Secret；用户直接从交付文件取得，在“配置”步骤填写，不额外运行 logs/get secret。交付文件必须私密保管，不能把实际凭证提交到公开源码。仓库保留模板／生成工具，发布方交付已经完成凭证生成的安装文件。

如果坚持直接 apply 公开通用模板、运行时才生成 token，就必须保留一次可信的 token 获取动作；不能同时实现公开 NodePort、无额外凭证动作、且避免未授权首次接管。此限制不能靠前端自动填 token 解决。

## 5. 向导最小字段

- 安装凭证。
- MySQL 地址、端口、库名、账号、密码，连接测试；TLS 作为高级配置。
- 正式域名；正式 NodePort 默认 30443，在高级配置可改并校验冲突。
- 管理员用户名、密码与确认密码。

无 IngressClass、证书上传、OCP、PXC、存储盘、镜像重复填写。运行副本、资源规格和采集参数使用经过测试的默认值；单节点资源不足时预检提示，必要时允许选择精简规格。

## 6. 证书与服务

- 自动生成私有 CA 及正式域名服务证书。私钥只保存在 Secret，挂载给 API；不返回页面、不写日志。
- API 原生 HTTPS 8443，正式 Service NodePort 30443。页面/API/终端共用入口，终端使用 wss。
- Bootstrap 与正式证书隔离；持久保存 CA，重启不重建。证书续期／域名变更用原 CA 补签，热加载前验证证书、私钥、有效期与 SAN。
- 健康探针使用单独的内部健康端口，不将业务 API 经明文 NodePort 暴露。collector/worker 不建对外入口。
- 多副本共享证书，登录 Cookie 保持 Secure/HttpOnly，保留 Origin 校验；直连 TLS 模式不盲目信任客户端转发头。
- NodePort 入口根据实际 kube-proxy、节点网络与防火墙检查可用地址。域名只指向单一节点不能宣称节点级入口高可用。

## 7. 安装状态、恢复与完成判定

- 在 Kubernetes 中保存非敏感安装阶段、配置摘要和错误，使用 Lease/资源版本协调单个安装执行者；不要仅靠进程互斥锁。
- 不持久化明文管理员／数据库密码到状态对象。已完成数据库步骤后依赖 Secret 和账号一致性检查恢复；未完成且需要凭据时提示用户重新输入。
- runtime Secret 存在不等于整个安装完成。中断后允许认证用户继续未完成阶段。
- CA Secret 已存在但服务证书缺失时，通过已保存 CA 私钥补签，避免一次 API 写入失败导致永久卡住。
- 默认 API 2／Collector 2／Worker 1，固定 COLLECTION_SHARDS=16。恢复已有库前读取持久化分片映射，不得自动改取模或清空数据；不兼容时在变更前停止并提示。
- 等待三个 Deployment 就绪、分片认领、API/数据库基本功能和平台 HTTPS 服务检查；结果页另引导用户验证客户端到正式 NodePort 的实际可达性，不能用集群内 health 代替外部连通性。
- 用户在正式入口登录验证后通过认证的完成动作触发 setup 关闭。先停后台安装任务／标记完成，再回收 RBAC、关闭 setup Service/Deployment，顺序需保证自身仍有权限执行清理；清理失败由可重试任务处理。

## 8. 本地现状核对

| 当前本地实现 | 不满足之处 | 调整 |
| --- | --- | --- |
| deploy/install.yaml 捆绑 ingress-nginx v1.11.3 | 额外组件与已知漏洞版本 | 去掉整套 Ingress 依赖 |
| install-bootstrap.yaml 的三处镜像替换与 shell Job | 镜像需重复修改，日志输出密钥 | setup 自动引导与镜像继承 |
| bootstrap 文档要求 logs / port-forward | 用户多执行命令 | 唯一 HTTPS NodePort 入口；安装凭证随私有交付文件提供 |
| cmd/platform/main.go 只启动 HTTP | NodePort 不会自动提供 TLS | 增加原生 HTTPS 与内部健康端口 |
| preflight/install 强制 IngressClass | 无 Ingress 时不能初始化 | 改为端口、资源、网络与数据库预检 |
| runtime ConfigMap 没有 COLLECTION_SHARDS | 回退单 Leader，恢复分片库失败 | 显式分片与映射校验 |
| install 状态只保存在内存 | Pod 重启后不能续装 | 持久状态与幂等执行 |
| verify 仅等待 API | Collector/Worker 失败也宣布成功 | 验证三个角色及实际 TLS |
| 仅删除 setup RBAC | NodePort/Deployment 留存，仍需人工清理 | 正式入口确认后的自动收尾 |
| install.md 为 Step 0–12，多份指南互相冲突 | 不符合单一简化交付 | 主手册只列五步，排障另附 |

## 9. 实施与验收顺序

### 任务一：TLS 服务与证书

修改 cmd/platform/main.go、internal/config/config.go；提取／复用 internal/setup/tls.go，新增证书加载与热更新模块。测试 HTTPS 登录、SAN、私钥匹配、过期拒绝、热更新失败保留旧证书、wss 握手及断线。

### 任务二：引导与唯一清单

改造 deploy/install.yaml、引导命令／setup 启动逻辑与发布文件生成工具。验收“仅一个镜像字段”、无 nginx/PXC/额外工具镜像依赖、启动无手工 patch、凭证与私钥不经日志或公开接口泄露。

### 任务三：持久初始化与运行资源

修改 internal/setup/setup.go、install.go、preflight.go、runtime.go、证书资源编排。覆盖每个阶段的故障注入、进程重启恢复、并发初始化、NodePort 冲突、已有分片映射与清理重试。

### 任务四：页面与手册

修改 web/src/setup/SetupPage.tsx 及相关测试，去掉 Ingress 字段，增加正式入口／CA／域名配置引导及安装恢复。统一 docs/deployment/install.md、install-beginner.md、kubernetes.md 和 README；历史方案明确标注废弃，不作为另一套交付入口。

### 任务五：真实首次安装验收

使用独立、可清理的 Kubernetes 测试环境，按客户五步从零安装；无预装 Ingress、无 port-forward、无手工 patch。验证 HTTPS 初始化与登录、外部 MySQL、三个角色、CA 信任、三类终端、安装中断恢复及 setup 关闭。已有环境只做只读检查，不删除用户现有 Ingress／Service／Secret。

交付记录必须区分单元测试、模拟接口、真实集群和浏览器操作；实现并验收之前，不修改文档声称新流程已经可用。
