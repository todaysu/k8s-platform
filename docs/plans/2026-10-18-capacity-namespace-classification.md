# Capacity Namespace Classification Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Discover namespaces from managed clusters, classify business namespaces globally and platform namespaces by technology stack, subtract unclassified workloads from headroom, and audit baseline decisions.

**Architecture:** Extend the existing modular Go service and SQLite store with immutable classification versions, namespace observations, and baseline decision records. Pass the active rules into the Kubernetes capacity collector, return platform/business/unclassified values in existing snapshots, then add a PatternFly configuration workflow to the fleet capacity page.

**Tech Stack:** Go, client-go, SQLite, React, TypeScript, PatternFly 6, Vitest.

---

### Task 1: Classification model and persistence

**Files:**
- Modify: `internal/cluster/model.go`
- Modify: `internal/store/sqlite.go`
- Test: `internal/store/sqlite_test.go`

1. Add failing tests for creating immutable classification versions, reading the latest version, recording first-seen namespaces, and saving a keep-baseline decision with a required reason.
2. Run `go test ./internal/store` and confirm failure.
3. Add SQLite migrations and repository methods.
4. Run `go test ./internal/store` and confirm success.

### Task 2: Three-way capacity classification

**Files:**
- Modify: `internal/cluster/connection.go`
- Modify: `internal/cluster/onboarding.go`
- Test: `internal/cluster/connection_test.go`
- Test: `internal/cluster/onboarding_test.go`

1. Add failing tests for global business, common platform, stack platform, and unclassified namespaces.
2. Change the capacity connector input from one system list to a classification rule object.
3. Aggregate unclassified resources separately and include them in Prometheus queries.
4. Record namespace observations while fleet snapshots are collected.
5. Run cluster tests.

### Task 3: Configuration and decision API

**Files:**
- Modify: `internal/httpapi/router.go`
- Test: `internal/httpapi/router_test.go`

1. Add API tests for `GET/PUT /api/v1/capacity/classification` and `POST /api/v1/clusters/{id}/capacity/baseline-decisions`.
2. Validate supported stacks, namespace names, conflicts, and required reasons.
3. Return current rules, discovered namespaces, rule version, and affected clusters.
4. Run HTTP API tests.

### Task 4: Fleet capacity configuration UI

**Files:**
- Modify: `web/src/api/clusters.ts`
- Modify: `web/src/pages/FleetCapacityPage.tsx`
- Modify: `web/src/pages/FleetCapacityPage.css`
- Test: `web/src/App.test.tsx`

1. Add a failing interaction test that opens classification configuration, classifies discovered namespaces, and saves a new rule version.
2. Add an ACM-style full-width modal with stack filters, discovered namespace table, bulk classification, conflict validation, and explicit save summary.
3. Show unclassified capacity as its own segment and highlight newly discovered namespaces.
4. Run Vitest, build, and lint.

### Task 5: Baseline decision UI and verification

**Files:**
- Modify: `web/src/api/clusters.ts`
- Modify: `web/src/pages/ClusterCapacityPage.tsx`
- Modify: `web/src/pages/ClusterCapacityPage.css`
- Test: `web/src/App.test.tsx`
- Modify: `docs/deployment/kubernetes.md`

1. Add tests for pending decisions, creating a new baseline, and retaining the old baseline with a mandatory reason.
2. Add the decision banner and reason modal to single-cluster capacity.
3. Document migration from `SYSTEM_NAMESPACES` to persisted rules.
4. Run `go test ./...`, `npm test -- --run`, `npm run build`, `npm run lint`, and visually inspect `/capacity`.
