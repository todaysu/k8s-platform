# Event Performance and AI Boundary Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Keep cross-cluster Kubernetes Event collection responsive at fleet scale while creating a compact, auditable data source for future AI inspection.

**Architecture:** Keep List+Watch and MySQL/SQLite as the source of truth. Retain Normal raw events for 7 days and Warning/attention events for 30 days, rebuild hourly fingerprints from raw data before cleanup, and expose collector throughput and queue health separately from cluster findings. AI inspection will consume hourly issue aggregates plus selected raw samples and resource snapshots; it will never maintain a second collector.

**Tech Stack:** Go, Kubernetes events.k8s.io/v1, SQLite/MySQL, React, PatternFly.

---

### Task 1: Define retention, rollup, and collector-health contracts

**Files:**
- Modify: `internal/cluster/model.go`
- Modify: `internal/cluster/events.go`
- Test: `internal/cluster/events_test.go`

**Steps:** Add hourly aggregate and collector metric types; extend the repository contract; test fingerprint normalization and aggregate behavior.

### Task 2: Add SQLite and MySQL hourly aggregate storage

**Files:**
- Modify: `internal/store/sqlite.go`
- Modify: `internal/store/events_sqlite.go`
- Modify: `internal/store/events_mysql.go`
- Test: `internal/store/events_sqlite_test.go`

**Steps:** Create `event_issue_hourly`; add time/fingerprint indexes; rebuild recent hourly buckets idempotently; implement type-aware batched cleanup; verify repeated rollups do not double count.

### Task 3: Add bounded collection telemetry

**Files:**
- Modify: `internal/cluster/events.go`
- Modify: `internal/store/events_sqlite.go`
- Modify: `internal/store/events_mysql.go`

**Steps:** Persist received/written/dropped counters, queue capacity, last batch size and Watch reconnect count in collector checkpoints; ensure the bounded queue reports drops instead of blocking all cluster collectors.

### Task 4: Expose performance health in the global event API and UI

**Files:**
- Modify: `internal/httpapi/router.go`
- Modify: `web/src/api/clusters.ts`
- Modify: `web/src/pages/FleetEventsPage.tsx`
- Modify: `web/src/pages/FleetEventsPage.css`

**Steps:** Return retention policy, rollup freshness, throughput and dropped-event state; add a compact “数据治理” panel with beginner-readable explanations and actionable warnings.

### Task 5: Document the non-overlapping AI contract

**Files:**
- Modify: `docs/product/2026-10-15-multi-cluster-operations-platform-requirements.md`
- Modify: `docs/deployment/kubernetes.md`

**Steps:** Document Event Center as evidence/index and AI Inspection as correlation/explanation; document retention defaults and MySQL indexes.

### Task 6: Verify end to end

**Steps:** Run `go test ./...`, `npm test -- --run`, `npm run lint`, `npm run build`, and `git diff --check`; then verify `/events` with real persisted data and confirm no loading, overflow, or empty-state regressions.
