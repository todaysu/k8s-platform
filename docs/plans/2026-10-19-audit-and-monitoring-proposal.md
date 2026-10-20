# 资源部署排障与监控页面建议

账号区域修正：移除 PatternFly 侧栏的底部 padding、内部玻璃容器外边距、圆角；导航占满剩余高度，账号区域不伸展。独立组件预览确认底部贴齐视口，构建与 lint 验证。

以下为设计建议，尚未开发：

资源部署排障页：导入 Kubernetes audit JSON Lines 文件，绑定集群、展示日志覆盖时间、解析失败行及日志级别；按 namespace/kind/name/UID 查找资源。请求按 auditID 合并阶段，按真实 UID 与 ownerReferences 关联 Deployment/ReplicaSet/Pod。首屏是摘要和部署时间线，展开可见操作者、API 操作、返回码、可用字段变更和脱敏原始证据。名称相同但 UID 不同视为不同资源；缺 owner 证据时不猜测。Metadata 不支持镜像/字段差异还原；API 请求成功不等于 Pod Ready，后者需同期事件、状态或监控证据。导入默认隐藏 Secrets、令牌等敏感字段。第一期按需导入，不建实时审计采集系统。

独立监控页：参考 OpenShift 的预置 Dashboard 与高级查询分层。顶部为集群、namespace、workload、时间段、刷新；全局比较指标完整度与健康，不混算利用率；选集群后支持节点及工作负载钻取。预置 CPU/内存使用与 requests/limits、Ready 副本、Pod 重启、节点压力曲线；数据源、最后采样时间、缺失与过期显式显示。高级 PromQL 折叠。点击波峰或异常跳转同资源同时间段排障页。已有代码只有 Prometheus 即时查询与当前快照；历史图需新增 range query 接口，并依赖现存 Prometheus 保留期及 kube-state-metrics/cAdvisor 指标。不能将当前快照伪装成历史。

参考：Kubernetes Auditing https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/ ; OpenShift Monitoring https://docs.openshift.com/en/documentation/monitoring_stack_for_red_hat_openshift/4.22/html/accessing_metrics/index
