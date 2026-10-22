# Arthas 故障排查向导

面向不熟悉命令的运维人员，在现有目标选择与 TTY 会话上增加故障现象入口：CPU、内存/GC、慢接口、异常、线程等待。

每个场景按步骤提供命令预览、输出阅读说明、下一步建议和明确标识的示意数据。场景和步骤切换不会执行命令，只有已连接并附加 JVM 后才可点击执行。高级预设折叠保留。

trace/watch 使用完整 Java 类和方法标识符，拒绝通配符、表达式和命令分隔符；限制 5 次输出，trace 增加耗时阈值。异常预设只查看异常对象。补充 Ctrl+C 中断采样。向导不自动分析根因、不自动 heapdump、不部署测试 JVM。

修正原“堆直方图”实际为堆快照、“thread -b 检测死锁”的误导说明，以及启动前未确认却显示“已确认”的文案。

验证：构建通过；4 项测试覆盖首次进入、连接前禁用、场景切换不执行、步骤切换、参数生成与非法输入。浏览器独立向导预览确认布局与交互，不代表真实 JVM 故障演练。

参考：https://arthas.aliyun.com/doc/thread.html ，https://arthas.aliyun.com/doc/trace.html

## 按需使用与案例扩展

目标选择、首次使用说明前置；故障排查向导以原生 details/summary 标签呈现，默认折叠，支持键盘展开与收起。原有会话终端保持可独立使用。

在原 5 类现象上新增 4 类：发布代码未生效（sc/jad）、类加载与依赖冲突（sc/classloader）、短时方法调用统计（monitor，5 秒周期、3 次）、trace/watch 无输出（sc/sm 核对匹配、方法签名、请求落点）。按场景附官方文档来源和官方仓库案例，均整理为观察、判断、下一步，而非堆叠命令。不会自动执行热更新或重放业务请求。

来源补充：
- https://arthas.aliyun.com/doc/sc.html
- https://arthas.aliyun.com/doc/sm.html
- https://arthas.aliyun.com/doc/classloader.html
- https://arthas.aliyun.com/doc/monitor.html
- https://github.com/alibaba/arthas/issues/537

验证：5 项相关测试通过（包含默认折叠、阅读顺序、展开收起和新增命令生成），前端构建通过。独立页面预览确认默认折叠与展开后 9 类场景；预览未连接 JVM。

## 社区案例、三级选择、离线包与测试应用

按用户要求撤下官方案例，内置 6 个社区用户案例，区分线上记录、性能调优记录与复现实验，保留作者、原文、现象、方法、定位证据、本平台操作映射。社区文章只作为案例素材，不自动执行其中的脚本或热更新命令。

来源：George_sz（多次数据库请求累计网络延迟）、西街二叔（Zabbix 图表排查）、楼下小黑哥（同名类 jar 冲突）、itxiaoshen（忙线程/锁/方法观测复现）。原始文章地址见 web/src/tools/arthas/cases.ts。

目标选择级联为集群→命名空间→Pod→容器。命名空间来自该集群运行中的 Pod，先选命名空间后才能选 Pod；切换集群或命名空间清空旧 Pod/容器/风险确认，防止误连旧目标。

2026-10-20 核对最新 Arthas 为 4.3.5，完整 Maven Central 发行 zip 固定于 internal/arthasdist 并校验上游 SHA-256；go:embed 分发，不需要项目外路径或公网下载。包内同时带 Linux ARM64 / x86_64 库。会话先用非 TTY Exec 二进制传包，再以 TTY 启动指定本地 arthas-home。仅监听容器 loopback，关闭 HTTP 端口。需要 JDK8+、sh/tar、可写 /tmp，同用户 Attach。移除 ARTHAS_BOOT_URL 配置。

已在 test01 / myapp 部署 Deployment arthas-demo，镜像 ftc-arthas-demo:2，容器 java，ARM64/JDK8，UID/GID10001，有明确 passwd 用户以支持 JVM 进程发现。无外部 Service，健康探针与资源限制。测试源代码、镜像构建、YAML、使用方法保存在 deploy/examples/arthas-demo/。

验证：
- 前端 7 个相关测试通过，覆盖级联清空、命名空间隔离、社区原文链接与限次命令。
- 离线包摘要、核心文件、双架构库测试通过。
- 实测 production KubernetesConnector 的二进制传包和 JVM 信息读取。
- 实测 HTTP WebSocket→Kubernetes TTY→Arthas：trace slowOperation 输出 350.55ms；watch failingOperation 捕获预设 IllegalStateException；stop 正常退出，Pod Ready 且零重启。输出位于 data/kind-test/arthas-demo-tty.txt（本地忽略目录）。
- 集成测试显式 FTC_ARTHAS_LIVE_TEST=1 才运行，默认测试不连接真实集群。
- x86_64 包内容已核对，本次没有 x86 节点运行验证；ARM profiler 尚未执行性能采样，不将包存在等同于 profiler 实测。

## 生产 Java 用户约定补充

用户明确生产 Java 容器通常采用 UID2000/GID5000。测试镜像升级为 ftc-arthas-demo:3，同时设置镜像默认用户、passwd/group 条目和 Kubernetes runAsUser/runAsGroup 为 2000/5000。保留非 root、禁止提权与 capabilities 全部移除。工具不对其他容器强制切换用户，Exec 跟随目标容器的用户，保证与 JVM 同 UID；目录权限也需允许 Attach 与离线包写入。

已确认测试容器 id 输出 uid=2000(app) gid=5000(app)，Java 进程列表可发现 PID1 demo.DiagnosticApp，/tmp 可写。

UID2000/GID5000 实测 WebSocket/TTY 集成测试通过（16.39s）：离线传包、Attach、trace、watch、stop 全流程完成。测试 Pod 1/1 Ready、重启0次。

## 页面边框与终端生命周期修复

- 统一单层卡片与控件边框，消除 PatternFly 控件 wrapper、原生 select 和伪元素的边框叠加。
- 故障向导恢复默认收起，按需展开，保留社区案例。
- 修复 React StrictMode 重复启动远端会话、旧连接 onclose 清空新连接引用、延迟 stop 关闭后续连接，以及提示符跨消息分段导致无法识别已 Attach。
- 断线后保留输出、禁用发送并提供重新连接。区分通道连接与 JVM Attach 状态；主动结束期间不显示误导性的连接错误。
- 全屏通过移动同一个 portal host 到 body 实现，避免平台 Page 容器裁切；不重建终端或 WebSocket。退出全屏与展开向导保留输出、连接。
- 每个 Arthas 启动进程记录独立会话 token/PID，WebSocket 退出时只清理匹配 token 的启动进程，不按进程名批量杀 Java，不误杀业务 JVM。

验证：9 个前端相关测试通过，覆盖 StrictMode 单连接、旧 close 回调和延迟 stop、分段提示符、目标级联与可选向导。Go httpapi/cluster 测试与前端构建通过。test01/myapp（UID2000/GID5000）实测 WebSocket/TTY trace/watch/stop 成功；实际浏览器验证打开→选择 JVM→memory→全屏→退出全屏→展开向导不断线，并验证中断连接后重连、未 Attach 时退出。退出后进程列表仅剩业务 Java 和本次 ps，无 Arthas 启动进程。实际全屏截图 data/kind-test/arthas-terminal-fullscreen-fixed.png。

## 2026-10-21 对齐与固定步骤区

目标选择与启动确认卡片使用 stretch 和一致高度，底边对齐。Arthas 标题、步骤条及会话开始后的目标摘要留在固定区；下方表单、终端、向导使用独立滚动区，避免吸顶元素覆盖按钮。根据平台主内容区域和标题栏的实时尺寸计算可用高度，ResizeObserver 处理侧栏、窗口和会话状态变化；全屏 portal 不受影响。

浏览器隔离布局预览确认两张卡片底边坐标相同；下方滚动216px后，顶部坐标保持156.20px、平台主区 scrollTop=0，下方操作仍可点击。截图在 data/kind-test/arthas-aligned-header-preview.png 与 arthas-aligned-pinned-preview.png。正式页面登录会话已过期，因此最后一轮仅验证使用真实组件和相同页面外壳的隔离布局，不涉及真实集群操作。
