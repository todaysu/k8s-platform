# 容量分类完整发现 Namespace

问题：分类清单此前由已调度且未结束的 Pod 反推，导致 test01 中空的 myapp 未出现。

采用 Kubernetes 原生 Namespace 资源作为发现来源（参考 https://kubernetes.io/docs/concepts/overview/working-with-objects/namespaces/），在现有容量采集链路中读取全部 Namespace，并预先建立带分类的零值统计，再关联现有 Pod 资源统计。保留 Pod 反推作为两次 API 读取之间资源变化的补充。

不增加新的 UI 或分类概念。空 Namespace、仅有待调度 Pod 或已结束 Pod 的 Namespace 均可被分类；待调度资源仍单独统计，已结束 Pod 仍不计入容量。Namespace 列表读取失败沿用现有容量采集错误处理，禁止将错误当作空列表。

验证：回归测试覆盖空 Namespace 分类、待调度及已结束 Pod、不变的业务/平台/未分类 Requests，以及 Namespace 权限不足；后端全量测试；只读连接 test01，确认 myapp 出现且统计为零。
