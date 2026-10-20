# Single Cluster Overview Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Clicking a managed cluster opens a refreshable, deep-linkable single-cluster health overview modeled on ACM's cluster-detail navigation while retaining only stability operations.

**Architecture:** Add a dedicated cluster-detail API that loads one encrypted record by ID and performs the existing live health inspection. Use a lightweight browser-history route (`/clusters/:id/overview`) inside the current React application, with a separate detail-page component and no new routing dependency.

**Tech Stack:** Go `net/http`, SQLite, React, TypeScript, PatternFly 6, Vitest.

---

### Task 1: Single-cluster backend query

**Files:**
- Modify: `internal/cluster/model.go`
- Modify: `internal/cluster/onboarding.go`
- Modify: `internal/store/sqlite.go`
- Modify: `internal/httpapi/router.go`
- Test: `internal/store/sqlite_test.go`
- Test: `internal/httpapi/router_test.go`

**Steps:**
1. Add failing repository and HTTP tests for an existing and missing cluster ID.
2. Add `GetRecord`, service `Get`, and `GET /api/v1/clusters/{id}`.
3. Reuse `inspectRecord` so detail health has the same semantics as the Fleet page.
4. Run `go test ./...`; expect all packages to pass.

### Task 2: Lightweight browser route and detail data client

**Files:**
- Modify: `web/src/api/clusters.ts`
- Modify: `web/src/pages/ClusterManagementPage.tsx`
- Create: `web/src/pages/ClusterOverviewPage.tsx`
- Create: `web/src/pages/ClusterOverviewPage.css`
- Test: `web/src/App.test.tsx`

**Steps:**
1. Add a failing test that clicks a cluster and expects `/clusters/<id>/overview`.
2. Add `fetchCluster(id)` with safe API error handling.
3. Push browser history when a table row is selected and handle `popstate` for Back/Forward.
4. Render loading, error, missing-cluster, and healthy detail states.
5. Run `npm test -- --run`; expect all tests to pass.

### Task 3: ACM-inspired lightweight overview UI

**Files:**
- Modify: `web/src/pages/ClusterOverviewPage.tsx`
- Modify: `web/src/pages/ClusterOverviewPage.css`
- Modify: `web/src/pages/ClusterManagementPage.tsx`

**Steps:**
1. Add cluster breadcrumb, status header, last refresh, refresh action, and delete action.
2. Add health cards for API Server, nodes, instances, and core components.
3. Add CPU, memory, Worker capacity, API latency, and basic connection information.
4. Add explicit unavailable-metrics and unreachable-cluster states.
5. Keep future resource pages as labeled next-step links only when routes exist; do not create dead tabs.

### Task 4: Verification

**Steps:**
1. Run `gofmt` on modified Go files.
2. Run `go test ./...`.
3. Run `npm test -- --run`, `npm run lint`, and `npm run build`.
4. Restart the local backend and verify Fleet → cluster overview → Back in the browser.
5. Verify no existing managed cluster is deleted during UI validation.

