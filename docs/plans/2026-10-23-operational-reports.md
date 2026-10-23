# 运行报告与工具边界调整实施计划

**Goal:** 删除 HTTP 文件服务；保留并接通 Arthas 内置发行包和离线更新；提供周报、月报、季报的持久化 HTML 图表报告。

**Architecture:** 在现有 Go 单体中增加独立 reports 模块，复用数据库聚合和现有认证。报告保存为不可变 JSON 快照，通过服务端 HTML 模板输出内嵌 SVG/CSS；AI 只接收有界统计证据，使用内网 OpenAI-compatible 模型接口，密钥沿用平台加密能力。

**Tech Stack:** Go database/sql、html/template、React、PatternFly、现有 MySQL/SQLite。

## 1. 成熟产品参考与边界

- ACM 的多集群可观测性看板：按集群/资源池查看指标并下钻；Grafana 的报告周期与快照模式。保留周期、范围、固定链接，交付为 HTML 页面，省去渲染服务、PDF 和邮件。
- 参考：https://docs.redhat.com/en/documentation/red_hat_advanced_cluster_management_for_kubernetes/2.15/html/multicluster_engine_operator_with_red_hat_advanced_cluster_management/mce-acm-observability
- 参考：https://grafana.com/docs/grafana/latest/visualizations/dashboards/create-reports/

## 2. 工具清理

删除 HTTP 文件服务代码、路由、工具入口、配置和部署说明。保留数据库历史表与本地历史文件，不执行数据删除。Arthas 默认使用平台 embed 发行包，配置离线更新地址时由平台下载、校验后经 Exec 传输。

## 3. 报告数据与统计口径

- 周期按 Asia/Shanghai：自然周（周一开始）、自然月、自然季度；SQL 时间窗口使用 UTC 半开区间，避免跨期重复。
- 复用 event_issue_hourly 的增量发生次数，不读取所有 Event 原始明细、不逐集群请求 Kubernetes API。SQL 在库内聚合，单次结果有界。
- 报告展示事件每日趋势、Warning 原因排行、集群事件贡献、资源池最新容量、周期内安全扫描概况、运维建议和证据覆盖情况。
- 当前数据库只保留最新健康和容量，相关内容标注“生成时快照”；不生成历史利用率/SLA 等无证据指标。无事件记录不能证明历史零故障。
- 保存生成时间、周期、范围、证据及分析结果，后续源数据清理或集群变更不影响已生成报告。

## 4. AI 配置与故障处理

报告页面配置内网模型 URL、模型名和 API Key；API Key 加密保存且不返回明文。默认可生成规则报告，配置后可选择 AI 解读。模型不可用时仍保存事实报告并明确标识 AI 失败；不伪造 AI 已完成。只向模型提供聚合值和集群标识，不发送 Kubeconfig、日志、原始 Event message 或凭据。

## 5. 可交付交互

侧栏“运行报告”；选择周/月/季、日期、环境、资源池（G01/OCP）并手动生成；保存历史列表；点击固定链接直接打开完整 HTML。页面查看、复制链接和资源下钻；HTML 自带 CSS/SVG，无外网字体、CDN 或脚本依赖。

## 6. 验证

周期边界、跨环境筛选、增量次数、空/缺失数据、报告持久化、HTML 转义、AI 成功/超时/失败、鉴权和密钥脱敏；Go 测试/vet；前端测试/构建/lint；本机重启与页面检查。生产 MySQL 与真实多集群验收单独记录。
