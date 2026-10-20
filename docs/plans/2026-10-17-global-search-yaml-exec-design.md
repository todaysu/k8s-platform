# 跨集群搜索、YAML 查看与 Pod Exec 设计

## 1. 背景

全部纳管集群组成一个资源池，应用的落点集群不固定。运维人员需要从平台入口直接定位应用，而不是先猜测集群再逐个查找。

## 2. 成熟产品模式

- 参考 ACM 的跨集群搜索：按名称、命名空间和资源属性检索，结果明确显示所在集群。
- 参考 OpenShift/Headlamp 详情页：在资源详情内使用“详情 / YAML”页签，YAML 默认只读。
- 参考 Kubernetes Dashboard/Headlamp 终端：Exec 必须先进入具体 Pod，再选择容器和 shell，不提供跨 Pod 的模糊执行。

## 3. 本期范围

### 3.1 跨集群搜索

- 顶部导航提供固定入口。
- 搜索 Deployment、StatefulSet、DaemonSet 和 Pod。
- 匹配名称、命名空间、类型、镜像、Pod Owner 与节点名。
- 结果显示集群名、环境、命名空间、类型和状态，点击直达详情。
- 最多返回 200 条；部分集群失败时保留其他集群的结果并明确提示。
- 使用 30 秒内存快照降低对各 API Server 的读取压力，第一版不引入 Elasticsearch 等重型索引。

### 3.2 工作负载 YAML

- 读取 Kubernetes API 中的实时对象。
- 保留 `apiVersion`、`kind`、`metadata`、`spec` 和 `status`，去除冗长的 `managedFields`。
- 支持复制，不支持在页面中修改或提交。

### 3.3 Pod Exec

- 仅支持 Running Pod，先选容器，再选 `/bin/sh` 或 `/bin/bash`。
- 默认禁用，通过 `ENABLE_POD_EXEC=true` 显式开启。
- 生产环境必须先完成 HTTPS、管理员认证、Kubernetes RBAC 最小权限和审计日志。

## 4. 后续迭代

- 当集群数或资源数显著增长时，改为后台 informer 索引，避免用户搜索时才查询全部集群。
- 扩展 Service、Ingress、Job/CronJob 与自定义资源，类型来自发现结果而非前端写死。
- 在完成授权、审批和变更审计后，再评估 YAML 编辑能力。
