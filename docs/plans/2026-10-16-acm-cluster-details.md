# ACM 风格单集群详情实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将单集群详情升级为 ACM 风格的“标题栏 + 二级导航”，并提供真实可用的概览与节点页面。

**Architecture:** 平台继续通过已加密保存的 Kubeconfig 直连目标 Kubernetes API。新增只读节点接口，详情前端用独立路由切换“概览”和“节点”，不引入 ACM 的机器池与 Add-on 管理依赖。

**Tech Stack:** Go、client-go、React、TypeScript、PatternFly、Vitest。

---

### Task 1: 增加集群节点领域模型与采集能力

**Files:**
- Modify: `internal/cluster/model.go`
- Modify: `internal/cluster/connection.go`
- Modify: `internal/cluster/onboarding.go`
- Test: `internal/cluster/onboarding_test.go`

**Steps:**
1. 定义节点名称、Ready状态、角色、区域、可用区、实例类型、CPU、内存和版本字段。
2. 通过 client-go 读取 Node List，并将标签与容量转换为稳定的API模型。
3. 复用加密凭据解密流程，为指定纳管集群读取节点。
4. 增加成功、集群不存在及连接失败测试。

### Task 2: 暴露只读节点API

**Files:**
- Modify: `internal/httpapi/router.go`
- Test: `internal/httpapi/router_test.go`

**Steps:**
1. 增加 `GET /api/v1/clusters/{id}/nodes`。
2. 返回统一 `{data,error}` 响应，并沿用现有错误码映射。
3. 验证成功与错误响应。

### Task 3: 实现ACM式详情二级导航

**Files:**
- Modify: `web/src/api/clusters.ts`
- Modify: `web/src/pages/ClusterOverviewPage.tsx`
- Modify: `web/src/pages/ClusterOverviewPage.css`
- Modify: `web/src/pages/ClusterManagementPage.tsx`

**Steps:**
1. 在集群标题栏下增加“概览、节点”二级标签。
2. 使用 `/clusters/{id}/overview` 与 `/clusters/{id}/nodes` 独立路由。
3. 保留概览中的健康、资源、异常和基本信息。
4. 节点页显示可搜索表格：名称、状态、角色、区域、可用区、实例类型、CPU、内存、Kubernetes版本。
5. 节点采集失败时保留详情标题，并显示局部错误，不伪造空数据。

### Task 4: 验证交互与回归

**Files:**
- Modify: `web/src/App.test.tsx`

**Steps:**
1. 覆盖概览到节点的路由切换、节点真实数据和浏览器返回。
2. 运行 `go test ./...`。
3. 运行 `npm test -- --run`、`npm run lint`、`npm run build`。
4. 在浏览器中验证真实集群概览、节点页和返回集群列表。
