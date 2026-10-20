# Audit 资源链名称回退设计

## 问题

Kubernetes audit 策略为 `Metadata` 时，记录通常只有 `objectRef`，没有请求体或响应体中的 `metadata.ownerReferences`。如果节点导出命令又只保留 `deployments` 类型，ReplicaSet 和 Pod 行会在导入前丢失，平台无法建立 Deployment 变更链。

## 设计

1. **精确证据优先**：若 owner UID 能在导入文件中找到资源，继续按 `ownerReferences` 建立边。
2. **名称回退**：owner 缺失或 owner UID 不在本批文件时，仅在同一 Namespace 和合法父子类型内，按最长资源名前缀匹配 Deployment → ReplicaSet → Pod。
3. **证据分级**：owner UID 关联标记为“创建链”，名称回退标记为“按命名推断”，不把推测伪装成确定事实。
4. **导出保留上下文**：节点命令选择 Deployment 时，资源范围自动扩展为 Deployment、ReplicaSet 和 Pod；名称条件仍作用于所有三类对象。

## 安全边界

- 不跨 Namespace 推断。
- 不用业务日志正文或 Secret 内容做关联。
- 同名不同 UID 的已知资源不视为同一对象；缺 UID 的记录必须显示人工核对提示。
- 导出中额外使用 Source IP、操作人、动作或结果过滤仍可能排除控制器生成的子资源事件，页面应保留这一可见提示。

## 验收

- 全部为 Metadata 级别、无 ownerReferences 的 Deployment/ReplicaSet/Pod 可形成推测链。
- owner UID 指向本批未导入对象时，可回退到名称链。
- 不同 Namespace 的同名资源不会串联。
- Deployment 链导出命令保留 `deployments` / `replicasets` / `pods` 三类记录。
