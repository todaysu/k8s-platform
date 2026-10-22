# 100 集群／10 用户承载改造实施计划

**Goal:** 按 ADR-0009 落地有界读路径、运行角色隔离、采集治理、分片及可复现验收。

**Architecture:** 同一二进制以不同运行角色启动；API 默认读持久化摘要，采集与重任务独立执行。先完成兼容性与负载隔离，再实现多 owner 和数据库 fencing；不将未压测功能标为容量达标。

**Tech Stack:** Go、client-go、MySQL/SQLite、React、数据库分片租约（兼容旧 Kubernetes Lease）。

## 实施顺序与验证

1. 单集群容量仓储读取；普通 GET 在缺失／过期时返回 pending，不隐式访问远端；资源索引冷启动禁用请求 fan-out。测试仓储只读目标行和无远端调用。
2. 增加 all/api/collector 运行角色（all 为本地兼容）；collector 不暴露业务 API，api 不执行定时采集。配置校验、主进程测试、分离 Deployment 与探针；扫描恢复不得由多 API 副本启动时随意执行。
3. 监控／实时请求准入、取消和有界响应；配置预算与过载错误，测试排队／取消及会话不占普通读槽。
4. 客户端复用和集群级预算、分页和摘要内存保护；共享大 List 槽；单集群同类任务去重和错峰退避。
5. 跨集群事件集合查询与索引完整批次；Event 有界写入与水位语义。
6. shard ownership、epoch fencing、接管和回滚；验证旧 owner 迟到写入被拒。
7. 重任务持久调度／独立 worker；不能仅拆 Deployment 而丢失任务归属。
8. 60／100 endpoint 造数、10 用户混合压力与故障测试；记录机器配额与实测指标，再填写 ADR 验收结果。

## 约束

- 保留已有未提交功能；不重置工作区，不自行部署生产或伪称已完成 100 集群压测。
- 使用现有 Go 测试、前端构建和相关功能测试；新增性能控制需覆盖取消、错误、并发边界。
- 不把一个小 kind 集群重复登记当成 100 大集群压力验收。

## 进度

当前：读路径、持久 Worker、共享配额、采集分片 fencing 与大 List 保护已落地；100 endpoint／10 用户短时模拟通过，最终生产容量验收未完成。下面按批次保留历史实施记录。

- 开始：代码核对与第一阶段。

### 已落地（本批）

- SQLite/MySQL 单集群容量主键读取；普通 GET 缺失／基线变化返回 pending，禁止隐式远端补采。
- 空资源索引返回 index_pending；用 EXISTS 替代每次全表 COUNT；前端准备提示和请求取消信号。
- all/api/collector 运行角色校验；split 角色要求共享 MySQL；collector 仅开放探针，不建管理员；api 不运行采集，禁止未持久化的本地扫描任务。
- 普通查询／实时读／监控／终端分通道准入、单集群配额、满载 429、结束释放槽位、拒绝计数指标；当前均为每进程预算。
- Collector 可选 Deployment、角色与配额部署说明；默认现有合并模式保持不变。
- cmd/fleetload：独立 Cookie 会话只读混合请求、有界内存统计；不用于替代真实 100 集群验收。

### 第一批结束时未完成项（后续进度见第二／三批）

- 跨副本全局配额、采集分片与 epoch fencing。
- 持久化 Job Worker 与扫描任务归属／恢复（split API 暂拒绝本地扫描）。
- 大 List 分页／内存预算、共享采集调度、Event 有界写入与集合查询、索引增量完整批次。
- 独立大集群模拟器、真实集群验证和 24 小时混合故障压测。

### 验证记录

- 本批相关 config/cluster/store/httpapi/platform Go 测试通过；config/cluster/store/httpapi 的 race 检查通过。
- 前端生产构建通过；资源搜索交互测试通过（并行编译期间默认 5 秒测试超时，单独以 15 秒上限复跑通过）。
- 全库 go test ./... 发现 internal/ai 的 TestBuildEvidenceCapsAndRedacts 失败，本批未改动该模块；未将全库检查标为通过。
- 未重启正在运行的本地后端，也未自动应用 split Deployment；未运行 100 集群容量验收。


### 第二批：持久任务与共享实时配额

- 新增 security_jobs 和事务控制行，API 入队与可见状态原子提交，最多 100 项在途＋排队，跨副本禁止同集群重复任务。
- 新增 worker 角色。all 启动本地队列消费者，api 仅提交；worker 仅消费并开放探针。扫描不再依赖 API goroutine 存活，也不再由 API 启动时粗暴恢复全部 running 记录。
- 数据库租约／心跳及 task token＋owner 校验，结果／历史原子写入；过期作业失败，不自动重跑远端扫描。
- 新增跨副本实时／监控／终端租约配额，普通读仍为本地有界并发；API 与采集的统一远端预算尚未接入。
- 新增 Worker Deployment 和 API strategic merge patch；未自动修改当前运行的部署。
- SQLite 事务、队列上限、跨 owner 认领、API 重启、租约过期与迟到写入测试；临时 MySQL 集成验证事务与共享配额。

仍待实现：采集 shard／fencing、远端大 List 内存治理、事件集合查询／有界写入、索引增量完整批次、完整 100 endpoint／10 用户／24 小时验收。


第二批验证结果：相关 config/cluster/store/httpapi/platform 测试通过；store/httpapi/config/platform race 检查通过；Worker 退出取消测试及 race 通过。另用本次新建的临时 MySQL 8.0 实例验证跨 Worker 原子认领、过期租约与旧 token 拒绝、结果历史提交和共享请求配额，测试通过，临时容器和卷已清理。本批不修改正在运行的本地后端／集群部署，不宣称完成 100 集群压测。


### 第三批：采集分片、分页内存保护与模拟压测

已实现固定 16 shard 的共享数据库认领／续租／均衡接管，数据事务 fencing，成员心跳与大事务隔离；已实现跨副本大 List 配额、逐页摘要、分页错误完整批次放弃、索引死锁有界重试、搜索合并缓存。排队预算与执行时限独立，owner 取消贯穿整个生命周期。

验证工具 `cmd/fleetscale` 使用 100 独立 TLS endpoint、20,000 Node、600,000 Pod、100,000 Deployment，真实 MySQL，10 独立登录会话。具体短时结果与局限另存性能报告；真实集群／独立进程 kill／24h 稳定性／Event 风暴／真实终端仍待验收。ADR 第 10 节记录实施参数与未完成边界。


第三批结果：100 独立 TLS endpoint／700,000 索引项／10 用户，60 秒 962 次请求全部 200，优雅接管 304ms；相关 Go 测试、race、vet 与真实 MySQL 回归通过。压测发现并修正 heartbeat 锁阻塞、空索引范围死锁、排队／执行 context 混用、共享配额锁顺序、事件约 2N 次 SQL 和搜索大候选排序。完整数据、失败轮次和场景差异见 `docs/performance/2026-10-21-fleet-100-synthetic.md`。索引仍非增量，冷子串搜索、真实集群、24h、Event 风暴／独立进程故障／终端与浏览器仍未验收。
