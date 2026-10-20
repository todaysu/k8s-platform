# Cluster Workloads Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a real, read-only ACM-style Workloads area to each managed cluster, covering Deployment, StatefulSet, DaemonSet, and their owned Pods.

**Architecture:** Extend the existing encrypted-kubeconfig connector with typed client-go reads. The backend resolves Pod ownership, including Pod → ReplicaSet → Deployment, and exposes list/detail endpoints. React keeps the current lightweight browser-history routing and renders a dedicated PatternFly workload list and detail view without mock data.

**Tech Stack:** Go, client-go, net/http, React, TypeScript, PatternFly, Vitest.

---

### Task 1: Workload domain model and Kubernetes collection

**Files:**
- Modify: `internal/cluster/model.go`
- Modify: `internal/cluster/connection.go`
- Test: `internal/cluster/connection_test.go`

**Steps:**
1. Add workload summary, workload detail, and Pod instance models.
2. Add connector methods for workload list and detail.
3. List Deployments, StatefulSets, DaemonSets, ReplicaSets, and Pods through client-go.
4. Resolve owner references and aggregate Pod readiness/restarts into the owning workload.
5. Derive explicit `healthy`, `progressing`, `degraded`, and `unknown` states without converting unavailable data to healthy.
6. Add connector tests for all controller kinds and Deployment ownership through ReplicaSet.

### Task 2: Service and HTTP APIs

**Files:**
- Modify: `internal/cluster/onboarding.go`
- Modify: `internal/cluster/onboarding_test.go`
- Modify: `internal/httpapi/router.go`
- Modify: `internal/httpapi/router_test.go`

**Steps:**
1. Reuse stored encrypted cluster credentials for workload reads.
2. Add `GET /api/v1/clusters/{id}/workloads`.
3. Add `GET /api/v1/clusters/{id}/workloads/{kind}/{namespace}/{name}`.
4. Preserve the existing `{data,error}` response shape and error mapping.
5. Test success and connector-error responses.

### Task 3: Frontend client and Workloads list

**Files:**
- Modify: `web/src/api/clusters.ts`
- Create: `web/src/pages/ClusterWorkloadsPage.tsx`
- Create: `web/src/pages/ClusterWorkloadsPage.css`
- Modify: `web/src/pages/ClusterOverviewPage.tsx`
- Modify: `web/src/pages/ClusterManagementPage.tsx`

**Steps:**
1. Add typed workload API functions.
2. Add the `工作负载` detail tab and route.
3. Render real summary cards for total, healthy, progressing, and degraded workloads.
4. Add name, namespace, kind, and status filters.
5. Render a compact ACM-style table with readiness, Pods, restarts, images, and creation time.
6. Provide distinct loading, error, empty-cluster, and no-filter-match states.

### Task 4: Workload detail and Pod drill-down

**Files:**
- Modify: `web/src/pages/ClusterWorkloadsPage.tsx`
- Modify: `web/src/pages/ClusterWorkloadsPage.css`
- Modify: `web/src/pages/ClusterOverviewPage.tsx`

**Steps:**
1. Navigate from a workload row to `/clusters/{id}/workloads/{kind}/{namespace}/{name}`.
2. Show workload status, desired/ready/available values, images, labels, and timestamps.
3. Show owned Pods with phase, Ready containers, restart count, node, Pod IP, and start time.
4. Provide a return-to-workloads action and browser-history support.
5. Keep logs, Exec, scale, restart, delete, and YAML editing outside this iteration.

### Task 5: Regression and visual verification

**Files:**
- Modify: `web/src/App.test.tsx`

**Steps:**
1. Add UI tests for opening the Workloads tab, filtering, and drilling into Pod instances.
2. Run `gofmt` and `go test ./...`.
3. Run `npm test -- --run`, `npm run lint`, and `npm run build` in `web/`.
4. Open the real cluster in the browser and verify list/detail, spacing, loading, empty, and error states.

