# 安全基线扫描第一版设计

## 目标

在左侧一级导航增加安全扫描入口，帮助运维人员快速回答：哪些集群尚未扫描、哪些控制项存在风险、影响哪些资源、证据是什么、应如何整改。

## 第一版边界

- 管理员在“安全扫描”页面手动选择一套或多套已纳管集群执行扫描，平台复用保存的 Kubeconfig。
- 提交后任务在平台后台排队执行并立即返回，页面轮询台账状态；关闭浏览器不会中断扫描。默认同时执行 2 套集群，由 `SECURITY_SCAN_WORKERS` 控制。
- 页面默认选择“Kubescape 临时 Job 扫描”，在目标集群创建临时 Job、ServiceAccount、ClusterRole 和 ClusterRoleBinding，结束后自动回收；不驻留 Agent、不挂载宿主机目录、不使用特权容器。
- 保留“快速只读检查”作为低权限可选方式，通过 Kubernetes List API 检查，不创建 Job/DaemonSet，不进入节点，不执行 Pod 命令。
- 只读检查覆盖 Pod SecurityContext、host namespace、hostPath、ServiceAccount token、镜像标签、NetworkPolicy 覆盖和 cluster-admin 绑定。
- Pod 每 500 个一页读取并立即聚合；每个控制项只返回 8 个受影响资源样例，适配大集群并避免页面海量渲染。
- NetworkPolicy 或 ClusterRoleBinding 无读取权限时，该项标为人工复核并进入未覆盖范围，不把“看不到”误判为通过。

## 安全决策

快速只读检查只调用只读 API。返回内容不包含 Secret、ConfigMap 正文、环境变量值、容器日志或完整 Pod YAML，只包含控制项统计和有限的资源名称样例。Kubescape 报告同样只提取控制项状态和资源名称，不保存原始报告。手动扫描需要管理员权限；同一集群同时只允许一个后台任务。

节点文件权限、kubelet/etcd/API Server 启动参数无法通过 Kubernetes API 完整验证。完整 CIS 模式使用临时 kube-bench 节点 Job、hostPID 和只读 hostPath 采集，并明确标为高权限扫描；托管控制面或未暴露宿主机配置仍列为“未覆盖”。扫描分数不等同于正式合规认证。

## 后续迭代

- 保存多次扫描历史和例外审批记录，并支持按集群、环境与资源池比较基线漂移（第一版保存每套集群最近一次结果）。
- 增加执行审计、定时任务和数据库任务租约/心跳，支持多副本安全接管；当前启动时立即终止旧扫描并清理残留资源。
