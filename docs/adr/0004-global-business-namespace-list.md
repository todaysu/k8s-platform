# ADR-0004：仅维护全局业务 Namespace 清单

- 日期：2026-10-19
- 状态：已接受，取代 ADR-0002

业务 Namespace 固定且跨技术栈一致，原公共组件加技术栈组件分类增加重复操作，不符合轻量运维定位。保留 OpenShift/Kubernetes 以 Namespace 汇总资源的模型，仅配置一份全局业务清单；业务名单内算业务，其余全部为平台。技术栈仍用于容量资源池隔离，但不参与 Namespace 分类。

保留旧业务名单与历史版本，旧公共/技术栈映射不再生效。新清单支持提前配置尚未创建的 Namespace。按 Namespace 发现与 Pod 统计关联计算，空 Namespace 也保留。实际用量使用同一清单，不与 Requests/Limits 混淆。

交付基线需手动建立和更新。交付后新增非业务 Namespace 自动计入平台并独立提示，不形成第三种分类，也不自动改写基线。旧基线因口径变化可能需要人工确认。

参考：https://docs.redhat.com/en/documentation/openshift_container_platform/4.20/html/building_applications/quotas
