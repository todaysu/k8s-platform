# Cluster Capacity Baseline Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a cluster capacity page that separates platform and business resources, persists delivery baselines, and reports drift.

**Architecture:** The Go connector calculates a live capacity snapshot from Kubernetes Nodes and Pods and enriches it with Prometheus usage when available. SQLite stores immutable baseline versions. The React cluster detail page loads the snapshot from a dedicated API and renders independent CPU and memory capacity views.

**Tech Stack:** Go, client-go, SQLite, net/http, React, TypeScript, PatternFly, Vitest.

---

### Task 1: Capacity domain and calculation

**Files:**
- Modify: `internal/cluster/model.go`
- Modify: `internal/cluster/connection.go`
- Test: `internal/cluster/connection_test.go`

1. Add capacity snapshot, resource totals, namespace totals, baseline and comparison models.
2. Add a capacity connector contract without expanding the base connector used by existing test fakes.
3. Calculate effective Pod requests and limits, classifying by the configured namespace whitelist.
4. Query platform and business actual CPU/memory from the discovered Prometheus service.
5. Test allocatable totals, init-container effective requests, Pending demand and classification.

### Task 2: Baseline persistence and service API

**Files:**
- Modify: `internal/cluster/model.go`
- Modify: `internal/cluster/onboarding.go`
- Modify: `internal/store/sqlite.go`
- Test: `internal/store/sqlite_test.go`
- Test: `internal/cluster/onboarding_test.go`

1. Add an immutable `cluster_capacity_baselines` table with cascading cluster deletion.
2. Implement create/latest baseline repository methods.
3. Add service methods to read capacity and lock the current snapshot as a new baseline.
4. Compare current values with the latest baseline using the documented 5% thresholds.

### Task 3: HTTP endpoints and configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `cmd/platform/main.go`
- Modify: `internal/httpapi/router.go`
- Test: `internal/config/config_test.go`
- Test: `internal/httpapi/router_test.go`

1. Parse `SYSTEM_NAMESPACES` with the documented defaults.
2. Register `GET /api/v1/clusters/{id}/capacity` and `POST /api/v1/clusters/{id}/capacity/baselines`.
3. Test response envelopes, errors and baseline creation.

### Task 4: Capacity UI

**Files:**
- Create: `web/src/pages/ClusterCapacityPage.tsx`
- Create: `web/src/pages/ClusterCapacityPage.css`
- Modify: `web/src/pages/ClusterOverviewPage.tsx`
- Modify: `web/src/api/clusters.ts`
- Test: `web/src/App.test.tsx`

1. Add the Capacity API types and functions.
2. Add a “容量” cluster-detail tab and route.
3. Render baseline state, CPU/memory distribution, actual usage fallback and namespace table.
4. Add the baseline action and refresh behavior.
5. Test the tab, metrics-unavailable state and baseline creation.

### Task 5: Documentation and verification

**Files:**
- Modify: `.env.example`
- Modify: `docs/deployment/kubernetes.md`

1. Document the global system namespace list and baseline semantics.
2. Run `go test ./...`.
3. Run frontend tests, lint and production build.
4. Validate the live page visually at desktop and narrow widths.

