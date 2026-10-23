# Changelog

## v1.0.0 - 2026-10-24

FTC 集群运维平台首个完整发布版本。

### 主要能力

- 多集群纳管、健康概览、节点、工作负载、Pod 日志与 Exec。
- 跨集群 Event 采集、聚合、搜索和持久化。
- 按环境、资源池和技术栈划分的容量管理。
- kube-bench 手动安全扫描、结果保留和两次扫描差异对比。
- 内置 Arthas 4.3.5 的 Java 应用排障会话。
- 自然周、月、季运行报告，支持内网模型总结和独立 HTML 运维态势页。
- Kubernetes 初始化向导、HTTPS 证书、MySQL 自动建表与管理员初始化。
- API、Collector、Worker 角色拆分，采集分片与并发限制。

### 交付

- AMD64 和 ARM64 离线 Docker 镜像包与 SHA-256 校验。
- 三节点 Kind 本地验收环境。
- 普通 Kubernetes 初始化安装清单和升级文档。
