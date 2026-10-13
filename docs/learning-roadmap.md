# Kubernetes 多集群运维平台学习路线

## 1. 这条路线适合谁

这条路线面向代码基础较弱、但了解 Kubernetes 日常运维的人。学习目标不是先成为 Go、React 或 Kubernetes 开发专家，而是在开发一个真实平台的过程中，逐步理解需要的知识。

最终目标是独立理解并维护一个轻量的 Kubernetes 多集群运维平台：

- 一套平台管理 DEV、PRD 两类集群。
- 通过集群名称和 Kubeconfig 纳管集群。
- 查看集群、节点、工作负载、Pod 和 Event。
- 主动检查集群健康状况。
- 在严格审计和权限控制下进入容器执行命令。
- 后续再接入 Prometheus 和 Alertmanager，告警暂不进入第一阶段。

## 2. 学习原则

### 2.1 不按语言目录学习

不要求先学完一本 Go 教程，再学完 React，最后才开始平台。每个阶段只学习当前功能需要的知识，并立即在项目里使用。

### 2.2 每个阶段都要有可见成果

一个阶段只有在能够运行、操作和验证后才算完成。看完视频、读完文章不等于完成。

### 2.3 Headlamp 是参考答案，不是入门教材

先理解我们自己的小功能，再查看 Headlamp 对应模块。不要从头通读 Headlamp 源码，也不要复制整个项目。

### 2.4 暂时跳过与目标无关的内容

第一轮不学习微服务、消息队列、复杂前端状态管理、Operator、CRD、Helm 管理、Service Mesh、多租户和业务发布系统。

## 3. 全局路线图

```text
阶段 0：看懂平台整体结构
   ↓
阶段 1：Go、HTTP、JSON 和测试基础
   ↓
阶段 2：数据库与单管理员登录
   ↓
阶段 3：Kubeconfig 与 client-go
   ↓
阶段 4：完成第一条闭环——集群纳管
   ↓
阶段 5：多集群资源查看
   ↓
阶段 6：集群稳定性与主动巡检
   ↓
阶段 7：Event 分析
   ↓
阶段 8：容器日志与 Exec
   ↓
阶段 9：部署、安全加固和日常维护
   ↓
阶段 10：可选——Prometheus 与 Alertmanager
```

按每周投入 5～7 小时估算，完成阶段 0～4 大约需要 6～10 周；完成第一版平台大约需要 4～6 个月。时间不是考核标准，以每个阶段的验收结果为准。

## 4. 阶段 0：建立整体认识

### 学习目标

能够用自己的话解释下面这条链路：

```text
浏览器页面
   ↓ HTTP/JSON
Go 后端
   ├── SQLite：保存平台数据
   └── client-go：访问各个 Kubernetes API Server
```

需要理解：

- 前端负责显示页面和收集输入。
- 后端负责业务规则、安全检查和集群访问。
- 数据库保存平台自己的数据，不保存 Kubernetes 中的 Pod。
- Kubernetes API Server 是集群实时状态的来源。
- Kubeconfig 是后端连接目标集群的凭据和地址配置。

### 实践任务

- 启动当前 Go 后端。
- 启动当前 React 前端。
- 在浏览器看到“多集群管理平台”页面。
- 找到前端入口和后端入口文件。

### 完成标准

不看文档，也能画出浏览器、Go 后端、数据库和 Kubernetes 集群之间的关系。

### 当前状态

项目骨架已经创建，接下来需要进行一次运行演示和代码导览。

## 5. 阶段 1：Go、HTTP、JSON 和测试基础

### 只学习这些内容

- Go 的变量、函数、结构体、切片、Map。
- `if`、`for` 和错误处理。
- 包和文件之间的关系。
- HTTP 的 GET、POST、状态码。
- JSON 请求和响应。
- 最基本的 Go 测试。

暂时不深入并发、反射、泛型和复杂设计模式。

### 实践任务

开发三个练习接口：

- `GET /api/v1/health`：返回平台是否正常。
- `GET /api/v1/version`：返回平台版本。
- `POST /api/v1/echo`：接收一个名称并以 JSON 返回。

每个接口至少写一个自动测试。

### 完成标准

- 能解释一次浏览器请求如何进入 Go 函数。
- 能看懂简单的 JSON。
- 能运行测试，并理解通过和失败的区别。

### 学习资料

- [Go 官方入门教程](https://go.dev/doc/tutorial/)
- [Go 官方 REST API 教程](https://go.dev/doc/tutorial/web-service-gin)
- [Go Tour](https://go.dev/tour/)

## 6. 阶段 2：数据库与单管理员登录

### 只学习这些内容

- 表、行、主键、唯一约束。
- `INSERT`、`SELECT`、`UPDATE`、`DELETE`。
- 数据库迁移是什么。
- 密码为什么只能保存哈希，不能保存明文。
- Cookie 和登录会话的基本作用。

### 实践任务

- 创建 SQLite 数据库。
- 建立集群、临时纳管记录和操作日志三张表。
- 完成单管理员登录和退出。
- 未登录时不能访问平台业务页面。

### 完成标准

- 重启平台后数据仍然存在。
- 数据库中没有管理员明文密码。
- 未登录调用业务接口会被拒绝。
- 能说清楚“平台账号”和“Kubernetes Token”不是同一种凭据。

### 学习资料

- [Go 官方数据库教程](https://go.dev/doc/tutorial/database-access)
- [SQLite 官方文档](https://www.sqlite.org/docs.html)

## 7. 阶段 3：Kubeconfig 与 client-go

这是整条路线最关键的 Kubernetes 开发阶段。

### 只学习这些内容

- Kubeconfig 中 `clusters`、`users`、`contexts` 和 `current-context` 的关系。
- API Server、CA、Bearer Token 分别有什么作用。
- TLS 校验为什么不能随意关闭。
- `client-go` 如何使用 Kubeconfig 创建 Kubernetes 客户端。
- Kubernetes API 的 List、Get 和 Watch 有什么区别。

### 第一个最小实验

先不连接页面和数据库，写一个小程序：

```text
读取一份测试 Kubeconfig
   ↓
使用 current-context
   ↓
连接 API Server
   ↓
输出 Kubernetes 版本
   ↓
输出节点名称
```

### Kubeconfig 解析实验

准备包含多个 Context 的脱敏测试文件，验证：

- 自动选择 `current-context`。
- 缺少默认 Context 时明确失败。
- Token 错误、CA 错误、地址错误时返回不同错误。
- 不支持的认证方式被拒绝。
- 日志中不会出现 Token。

### 完成标准

- 能画出 Cluster、User、Context 的引用关系。
- 能用 Go 程序连接一套测试集群并读取版本。
- 能解释为什么平台不长期保存原始 Kubeconfig。

### 学习资料

- [Kubernetes 官方：访问集群](https://kubernetes.io/docs/tasks/access-application-cluster/access-cluster/)
- [client-go 官方集群外认证示例](https://github.com/kubernetes/client-go/tree/master/examples/out-of-cluster-client-configuration)
- [client-go 官方示例目录](https://github.com/kubernetes/client-go/tree/master/examples)

## 8. 阶段 4：完成集群纳管闭环

这是第一个真正可交付的平台功能。

### 学习目标

把已经学过的页面、API、数据库和 Kubernetes 客户端连接起来。

### 功能拆分

1. 集群列表空状态。
2. 输入集群名称。
3. 上传或粘贴 Kubeconfig。
4. 后端解析默认 Context。
5. 测试连接并读取 Kubernetes 版本。
6. 页面显示不含敏感信息的预览。
7. 管理员确认后加密保存连接凭据。
8. 集群出现在列表中。
9. 查看基础详情并重新检查连接。
10. 修改名称、更新凭据和取消纳管。

### 必须理解的安全知识

- Token 加密与密码哈希的区别。
- 为什么加密密钥不能放入代码和 Git。
- 为什么错误信息和日志不能输出 Kubeconfig。
- 为什么“测试连接”和“确认保存”要分成两步。

### 完成标准

- 文件上传和文本粘贴都能纳管测试集群。
- 多 Context 文件使用默认 Context。
- 平台重启后仍能重新连接集群。
- 数据库、页面和日志中都没有明文 Token。
- 取消纳管不会删除真实 Kubernetes 集群。

### 参考项目

- [Headlamp 架构说明](https://headlamp.dev/docs/latest/development/architecture/)
- [Headlamp 开发指南](https://headlamp.dev/docs/latest/development/)
- [Headlamp Go 后端入口](https://github.com/kubernetes-sigs/headlamp/blob/main/backend/cmd/headlamp.go)

完成本阶段后，再阅读 Headlamp 对应代码。阅读目标是比较设计，不是照抄。

## 9. 阶段 5：多集群资源查看

### 学习顺序

按以下顺序逐个增加，只做只读功能：

1. 集群基础信息。
2. 节点列表与详情。
3. Namespace 列表。
4. Deployment、StatefulSet、DaemonSet 列表。
5. Pod 列表与详情。
6. 按集群、Namespace、状态进行筛选。

### 需要学习

- Kubernetes 常用资源对象的结构。
- 标签和选择器。
- 分页、超时和请求取消。
- 多集群客户端如何按集群 ID 选择。
- 浏览器不能直接拿 Kubernetes Token 访问集群。

### 完成标准

- 可以在页面切换集群。
- 能查看节点、工作负载和 Pod。
- 一个集群不可用不会拖垮其他集群页面。
- 所有操作保持只读。

## 10. 阶段 6：集群稳定性与主动巡检

### 学习目标

把“查看资源”升级为“主动发现异常”。

### 第一批检查项

- API Server 是否可连接。
- 节点是否 Ready。
- 节点是否存在 MemoryPressure、DiskPressure、PIDPressure。
- 系统 Namespace 中的关键 Pod 是否正常。
- Pod 是否频繁重启、Pending 或处于异常状态。
- Deployment 可用副本是否达到期望值。
- 集群证书是否接近过期。

### 设计原则

- 每个检查项必须显示事实和原因，不能只给一个模糊分数。
- 一套集群检查失败不能阻塞其他集群。
- 检查结果要有时间，避免把旧结果当成当前状态。
- 第一版可以按需检查，稳定后再增加定时任务。

### 完成标准

- 首页能够汇总所有集群的正常、警告、异常和不可达状态。
- 点击异常可以看到具体集群、资源和原因。
- 检查逻辑具有自动测试。

## 11. 阶段 7：Event 分析

### 学习目标

不再只是把 Event 存下来，而是把大量重复事件变成可以处理的问题。

### 实践顺序

1. 查看单个集群最近 Event。
2. 按 Warning、Namespace、资源类型筛选。
3. 将重复 Event 按集群、资源、Reason 和 Message 归并。
4. 显示首次发生、最近发生和累计次数。
5. 为常见 Reason 提供中文解释和检查建议。
6. 展示一段时间内的新问题、持续问题和已消失问题。

### 完成标准

- 同类重复事件不会淹没页面。
- 可以从异常集群进入对应 Event。
- 能快速识别 ImagePullBackOff、FailedScheduling、Unhealthy 等常见问题。

## 12. 阶段 8：容器日志与 Exec

Exec 是高风险功能，必须晚于登录、审计和集群权限设计。

### 先做日志

- 选择集群、Namespace、Pod 和容器。
- 查看最近日志。
- 支持简单的实时跟随和下载限制。
- 日志请求有超时和大小限制。

### 再做 Exec

- 只能从 Pod 详情进入。
- 必须明确显示当前集群、Namespace、Pod 和容器。
- PRD 集群增加醒目标识和再次确认。
- 后端建立 WebSocket 或流式连接，浏览器不接触 Token。
- 记录操作者、目标、开始时间、结束时间和结果。
- 不在普通日志中记录用户输入的命令内容和敏感输出。
- 使用专用 ServiceAccount 和最小 RBAC 权限。

### 完成标准

- DEV、PRD 环境不可能因为页面切换而混淆。
- 无权限时明确拒绝。
- Exec 会话关闭后连接及时释放。
- 每次 Exec 都有审计记录。

## 13. 阶段 9：部署、安全加固和维护

### 需要学习

- 多阶段容器镜像构建。
- Kubernetes Deployment、Service、Ingress、Secret 和 PVC。
- 健康检查、资源限制和优雅退出。
- SQLite 备份；后续需要高可用时再评估 PostgreSQL。
- 凭据加密密钥和管理员密码的轮换。
- 依赖漏洞检查和升级策略。

### 完成标准

- 平台以一个容器部署到管理集群。
- 前后端同源访问。
- 数据和密钥有备份恢复说明。
- 平台重启不丢失纳管集群。
- 有一份升级、回滚和故障排查手册。

## 14. 阶段 10：可选的监控与告警

这一阶段暂不开发，等核心运维功能稳定后再进行。

建议顺序：

1. 为每个集群配置 Prometheus 数据源。
2. 显示 CPU、内存和关键组件趋势。
3. 接入 Alertmanager 当前告警。
4. 在平台内查看、筛选和关联告警。
5. 将告警关联到集群、节点、Namespace 和工作负载。

第一版只展示，不负责向外发送通知。

## 15. 每个学习单元的固定流程

以后每开发一个小功能，都按下面七步进行：

1. **先讲业务**：这个功能解决什么问题。
2. **画数据流**：页面、后端、数据库和 Kubernetes 如何协作。
3. **学最少概念**：只学习本功能需要的知识。
4. **先写测试**：明确怎样才算正确。
5. **实现最小版本**：一次只增加一个能力。
6. **实际操作**：在浏览器和测试集群中亲手验证。
7. **回看代码**：只阅读本次新增的关键文件，并形成笔记。

建议每次学习控制在 60～120 分钟：

- 15～25 分钟理解概念和流程。
- 30～70 分钟跟随实现。
- 15～25 分钟运行、故意制造错误并记录结果。

## 16. 学习记录模板

每完成一个单元，记录以下内容：

```text
本次功能：
它解决的问题：
浏览器发出了什么请求：
Go 后端做了什么：
是否访问了数据库：
是否访问了 Kubernetes：
成功时看到了什么：
失败时看到了什么：
我还不理解的地方：
```

不要求背代码。重点是逐渐能够解释数据怎样流动、错误在哪里产生，以及怎样验证功能正确。

## 17. 当前下一步

当前已经完成项目骨架，对应阶段 0 的一部分。下一次学习建议先完成：

1. 启动前端和后端，认识项目目录。
2. 画出一次 HTTP 请求的数据流。
3. 实现并测试 `GET /api/v1/health`。
4. 再进入 SQLite 数据库和集群数据模型。

不要直接跳到多集群健康大盘，也不要马上阅读完整 Headlamp 源码。先把第一条小链路跑通，再逐步扩大。
