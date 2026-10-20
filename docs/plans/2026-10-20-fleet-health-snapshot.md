# Fleet Health Snapshot Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the fleet list read persisted health snapshots while a bounded, staggered background collector refreshes clusters independently of browser requests.

**Architecture:** Add an optional health snapshot repository contract implemented by SQLite. `OnboardingService.List` becomes a local database read. A `HealthCollector` reads encrypted cluster records, performs bounded health checks, persists successful snapshots, and preserves the last successful values on failure.

**Tech Stack:** Go 1.27, client-go, database/sql with SQLite, existing React/TypeScript API contract.

---

### Task 1: Persist cluster health snapshots

**Files:**
- Modify: `internal/cluster/model.go`
- Modify: `internal/store/sqlite.go`
- Test: `internal/store/sqlite_test.go`

1. Add a `ClusterHealthRepository` contract.
2. Add nullable health columns to the SQLite migration.
3. Implement health snapshot update and nullable field scanning.
4. Verify a successful snapshot survives reopening/listing and a failed refresh preserves previous metrics.

### Task 2: Make the fleet list cache-only

**Files:**
- Modify: `internal/cluster/onboarding.go`
- Test: `internal/cluster/onboarding_test.go`

1. Change `List` to map stored records without calling the connector.
2. Extract one-record health refresh logic that marks success or failure correctly.
3. Add tests proving a fleet list performs no live health call.

### Task 3: Add the bounded background collector

**Files:**
- Create: `internal/cluster/health_collector.go`
- Create: `internal/cluster/health_collector_test.go`

1. Add a collector with configurable interval, concurrency and per-cluster timeout.
2. Stagger record dispatch across each interval and cap concurrent checks.
3. Persist each result independently so one failure cannot discard the cycle.
4. Test success, failure isolation and concurrency bounds.

### Task 4: Start collection with the platform

**Files:**
- Modify: `cmd/platform/main.go`
- Modify: `README.md`

1. Start `HealthCollector` alongside the Event collector.
2. Document that `/clusters` is snapshot-based and list the first-stage freshness target.

### Task 5: Verify compatibility

**Files:**
- Test: `internal/...`
- Test: `web/src/App.test.tsx`

1. Run `go test ./...`.
2. Run the frontend tests and production build.
3. Confirm the `/api/v1/clusters` JSON contract remains backward compatible.

