# ACM PatternFly UI Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将现有多集群健康概览从 MUI 仿制样式重构为与 Red Hat ACM 同源的 PatternFly 页面。

**Architecture:** 保留现有 React 状态、模拟数据和交互逻辑，只替换 UI 组件层。使用 PatternFly 的全局设计令牌、页面骨架、导航、卡片、工具栏、表格、弹窗和抽屉，使用局部 CSS 处理本产品特有的 Fleet 指标布局。

**Tech Stack:** React 19、TypeScript、Vite、PatternFly React、Vitest、Testing Library。

---

### Task 1: 接入 PatternFly

**Files:**
- Modify: `web/package.json`
- Modify: `web/package-lock.json`
- Modify: `web/src/main.tsx`
- Modify: `web/src/index.css`

**Steps:**
1. 安装 PatternFly React Core、Table 和 Icons。
2. 在入口加载 PatternFly 基础样式。
3. 保留 Red Hat 字体作为中文回退体系的一部分。
4. 运行构建，确认依赖与样式可解析。

### Task 2: 重构 ACM 页面骨架

**Files:**
- Modify: `web/src/App.tsx`
- Modify: `web/src/pages/ClusterManagementPage.tsx`
- Create: `web/src/pages/ClusterManagementPage.css`

**Steps:**
1. 使用 Masthead、Page、PageSidebar 和 Nav 重建全局框架。
2. 使用 Breadcrumb、Title、Alert、Card 和 Grid 重建概览区。
3. 保留刷新、状态筛选与异常下钻状态。
4. 运行测试并检查无障碍名称。

### Task 3: 重构集群工具栏与表格

**Files:**
- Modify: `web/src/pages/ClusterManagementPage.tsx`
- Modify: `web/src/pages/ClusterManagementPage.css`
- Modify: `web/src/App.test.tsx`

**Steps:**
1. 使用 Toolbar、SearchInput、ToggleGroup 和 PatternFly Table 重建列表区。
2. 使用 Label、Icon 和风险色表达健康状态。
3. 保留搜索、DEV/PRD 筛选、风险优先排序和空状态。
4. 更新测试并验证筛选结果。

### Task 4: 重构操作面板

**Files:**
- Modify: `web/src/components/AddClusterDialog.tsx`
- Modify: `web/src/pages/ClusterManagementPage.tsx`

**Steps:**
1. 使用 PatternFly Modal、Form、Tabs 和 FileUpload 重建纳管弹窗。
2. 使用 PatternFly Drawer 重建集群详情。
3. 验证打开、关闭、上传与粘贴入口。

### Task 5: 完整验收

**Files:**
- Verify: `web/src/**/*.tsx`
- Verify: `web/src/**/*.css`

**Steps:**
1. 运行 `npm run lint`。
2. 运行 `npm test -- --run`。
3. 运行 `npm run build`。
4. 在本地浏览器检查桌面与窄屏布局、弹窗和详情面板。
5. 运行 `git diff --check`。
