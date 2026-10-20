# Direct Cluster Onboarding Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the two-step connection-test flow with one “纳管集群” action that verifies the Kubeconfig, encrypts its minimal credentials, and persists the managed cluster.

**Architecture:** Add an onboarding service around the existing parser and connector. Store only normalized connection fields and AES-256-GCM encrypted credentials in SQLite; never store the raw Kubeconfig. Expose `POST /api/v1/clusters` and keep the existing validation endpoint only for compatibility.

**Tech Stack:** Go 1.27, client-go v0.32.x, `database/sql`, modernc SQLite, AES-GCM, React 19, PatternFly 6.

---

## Task 1: Add encrypted credential storage

**Files:** `internal/cluster/crypto.go`, `internal/cluster/model.go`, `internal/store/sqlite.go`, their tests.

1. Test AES-GCM round trips, random nonces, invalid keys, and wrong-key failures.
2. Add a SQLite schema with unique cluster name and fingerprint.
3. Store encrypted minimal credentials only; never add a raw Kubeconfig column.
4. Test persistence and duplicate detection.

## Task 2: Add one-step onboarding service and API

**Files:** `internal/cluster/onboarding.go`, `internal/httpapi/router.go`, `cmd/platform/main.go`, their tests.

1. Parse and connect using the existing components.
2. Encrypt credentials and create the cluster only after connectivity succeeds.
3. Add `POST /api/v1/clusters` with stable safe errors.
4. Initialize SQLite and encryption in the server startup path.

## Task 3: Replace the test action in the UI

**Files:** `web/src/api/clusters.ts`, `web/src/components/AddClusterDialog.tsx`, `web/src/App.test.tsx`.

1. Rename the action to “纳管集群”.
2. Call the create endpoint directly.
3. Show “纳管成功” and a safe cluster summary.
4. Remove all copy describing a later confirmation step.

## Verification

- `go test ./...`
- `cd web && npm test -- --run`
- `cd web && npm run lint && npm run build`
- Local browser interaction verifies the new wording and states.
