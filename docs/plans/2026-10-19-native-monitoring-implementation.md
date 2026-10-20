# 原生资源与关键组件监控

## 已实现

独立 /monitoring 页面支持总览、Pod 资源、API Server、etcd 四个视图。顶部集群、1h/6h/24h/7d 范围、30s/60s 自动刷新（默认关闭）、手动刷新与取消；Pod 视图支持命名空间/Pod，历史资源可手动填写，点击打开 Pod 详情。浏览器 URL 保存筛选条件，切换集群清除资源范围、取消旧查询；页面隐藏时不自动刷新。

曲线使用原生 SVG，支持悬停/键盘滑块取值、图例显示/隐藏、单位格式化和缺口断线。按实际历史样本绘图；零值、NaN/缺样本和查询错误区分展示。源自 Pod 的 CPU 合计不冒充主机 CPU 使用率。未自动依据单个数值判定集群健康。

后端 GET /api/v1/clusters/{id}/monitoring，固定面板查询经已保存纳管凭据连接 API Server Service Proxy，再查询现有 Prometheus query_range。不开放原始 PromQL、任意 URL 或任意代理路径。验证视图、DNS namespace/Pod、最多 7 天时间范围。4 个全局并发请求、每请求 2 个并发面板、25s 请求超时、8s Prometheus 超时、30s 短期缓存（最多 32 项）、每曲线目标约 360 点、最多 40 条曲线/面板、8 MiB 响应上限；支持 limit 的 Prometheus 将返回最多 41 条用于判断超限，不截取前 40 条冒充完整数据。

自动发现已有 Prometheus Service，当前固定使用最高优先级候选，整个请求不混合数据源，返回来源名称。没有找到服务或权限不足时明确失败。多数据源手动绑定、HA 指标去重策略和采集组件部署不在本次实现范围；现有查询按数据源实际指标聚合，重复抓取应在采集端修正。

## 面板

- Pod CPU 核数、内存工作集；CPU/内存 requests/limits（需 kube-state-metrics）。
- API Server 请求速率、5xx 比例、P99 延迟（排除 WATCH/CONNECT）、在途请求、429 速率。
- etcd Leader 可见性、切换速率、失败提案速率、WAL fsync P99、backend commit P99、数据库配额使用率。要求 etcd 自身指标，不用 API Server 的 etcd 客户端指标代替。

## 验证与当前限制

test01 使用实际保存的纳管凭据验证：myapp 有 4 个 Pod 的 CPU/内存历史曲线；API Server 5 个面板中 4 个有样本；429 当前没有样本；etcd 6 个面板均无样本；requests/limits 无 kube-state-metrics 样本。没有安装新组件、修改 Service 或 Prometheus 采集配置。

Go 全套测试；60 项前端测试、构建与 lint。测试覆盖范围/参数边界、Service Proxy query_range 路径、零/NaN、曲线数量限制、按 Pod 过滤、集群切换清空范围和图表缺口。浏览器通过隔离预览入口加载真实查询快照，验证 Pod/API Server/etcd 视图、真实趋势及无数据提示；截图 data/kind-test/pod-monitoring-trends.png。临时预览入口及真实集群测试文件已删除。

后端已更新至 data/kind-test/platform-monitoring 并检查健康；重启使既有内存登录会话失效，需要重新登录。

## 默认集群及故障提示修复

实测四个纳管记录：列表首先返回 test04，test04/test03/test02 都没有发现 Prometheus；test01 查询正常。旧页面默认选第一项，再由 HTTP 层把所有顶层错误归为同一危险告警，导致可用监控看起来也不可用。

改为：无 URL 指定时优先选择 metricsAvailable 且连接可达的集群；显式选中的集群仍保留。未发现数据源、发现权限不足、认证失败、集群连接失败、超时分别提供错误码与安全提示；无数据源是信息提示，带集群名，不能冒充有数据或自动安装组件。面板内部代理权限/认证/超时也区分说明。增加默认选择与无数据源提示、HTTP 错误透传回归测试。61 项前端测试、Go 测试、构建与 lint 通过。

## Pod 磁盘 I/O 与在途请求显示

移除 Pod CPU/内存 requests/limits 独立面板。Pod 页面保留 CPU、内存，增加读取/写入吞吐量（B/s、KiB/s、MiB/s 等自适应）及读取/写入 IOPS，使用 cAdvisor container_fs_* 计数器的 5 分钟 rate，沿用 namespace/Pod 过滤。容器可观测 I/O 不保证覆盖所有 PVC/网络存储，不当作应用逻辑 I/O。

在途请求面板改为整行宽度，图例优先显示“只读请求”“写入请求”，完整换行显示实例；值以“个”显示，坐标轴不重复长单位，避免裁切。

只读现场核对：Prometheus scrape job 只有 prometheus/cadvisor/kube-apiserver；etcd 指标端点仅监听 http://127.0.0.1:2381，没有 etcd scrape job，所以 etcd 面板无数据不是页面遗漏。未修改 etcd 监听或部署/采集配置；补齐时需先设计仅内网可达的采集或同节点采集代理，不要求对外 Service。

myapp 4 个 Pod 的四个 I/O 指标即时与 1h range 查询均成功，目前速率为 0，有效零值按零展示。Go 全套、62 项前端测试、构建与 lint；浏览器真实快照预览验证 I/O 面板和完整在途图例，截图 data/kind-test/pod-disk-io.png、apiserver-inflight-full.png。临时预览文件已删除。

## 紧凑图表、绝对时间与全屏

撤销在途请求占满整行的布局；所有常规图表双列显示、绘图高度固定 190px，缩小标题/底部说明间距，保留可换行的只读/写入图例。新增每图全屏和整个看板全屏，Portal 避免页面祖先裁切；支持 Esc、键盘焦点循环、退出恢复焦点以及嵌套全屏只退出最上层。

时间范围新增自定义：本地开始/结束时间（秒级），“按时间查询”后发起真实 query_range；验证完整输入、先后顺序、最少 30 秒/最多 7 天、结束不在未来。绝对范围写入 URL，并暂停自动刷新。图表拖选至少 30 秒的区间会更新全局范围并重新查询，所有图表同步；刷新保留同一作用域图表实例，因此全屏内拖选不会退出全屏。切换集群/资源则清除旧作用域数据。

验证：65 项前端测试、构建、lint；浏览器以真实采样快照验证绝对时间提交、单图全屏、拖选后 URL/起止时间/曲线更新且保持全屏、Esc 退出、看板全屏与紧凑双列。截图 data/kind-test/monitoring-compact-time.png。临时预览文件删除。无后端改动，无需重启服务。

## 图表文字与资源选择修正

移除固定 viewBox + preserveAspectRatio=none 的非等比拉伸。SVG 通过 ResizeObserver 读取自身实际像素宽高，绘图坐标与容器匹配，坐标文字固定 10/11px；全屏/退出及尺寸变化后重算，拖选比例也使用实际边距。窄图只保留首尾时间刻度避免重叠。

替换浏览器原生 datalist 为 ResourceCombobox：输入框获得焦点/点击或箭头即展开，显示实际资源选项；支持输入过滤、任意名称、上下键/Enter、Esc/Tab、外部点击收起、选择全部。切换命名空间清空 Pod，点击应用后才查询，手动填写历史资源仍可使用。

70 项前端测试与构建通过；本次改动文件 lint 无警告。全局 lint 在非本次修改的 OperationsToolsPage.tsx 中已有新增的热更新/转义警告，保留其他工作中的修改。浏览器预览使用真实 Pod 列表与历史快照，确认 namespace/Pod 点击展开与选中后曲线由 4 条变 1 条，文字无形变。截图 data/kind-test/monitoring-resource-picker.png，临时预览已删除。无需后端重启。

## test01 etcd 采集已配置

用户明确要求后，仅在 kind-k8s-test/test01 完成：静态 etcd metrics 保留 127.0.0.1:2381 并增加节点内部 Docker IP 172.18.0.2:2381；Prometheus ConfigMap 增加 job etcd（15 秒采集），不新增 Service 或宿主机端口映射。etcd 短暂重启后 /health 与 API /readyz 通过。Prometheus 使用 ConfigMap subPath，等待 kubelet 投射更新后仅重启原有容器，保留原 Pod UID/emptyDir，并实际查询确认 20 分钟前历史仍在。

原始配置、更新配置、回滚步骤和采样结果保存 data/kind-test/etcd-monitoring/；平台服务实际凭据查询六面板均 available，Leader=1、失败提案/Leader 切换速率=0、P99 WAL fsync 约 6.3ms、commit 约 6.8ms、配额约 3.79%。节点/etcd/Prometheus 最终 Ready；采集目标 UP。该内部 HTTP 采集仅用于本地测试，不把它作为生产 TLS/认证隔离方案。历史从采集启动时开始积累。

图表数字轴对 changes/s、proposals/s 仅显示数值，完整单位保留在图例，避免长单位裁切；图表专项测试及构建通过。
