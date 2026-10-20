# Kubernetes HA and Unified MySQL Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make FTC Cluster Operations Platform deployable as multiple Kubernetes replicas with automatic MySQL initialization and single-leader background collection.

**Architecture:** A shared store contract selects SQLite for local development or MySQL for Kubernetes. MySQL startup migrations are idempotent and serialized with an advisory lock. Kubernetes Lease election gates health and Event collectors while API replicas remain active.

**Tech Stack:** Go, database/sql, MySQL 8.4, SQLite, client-go leader election, Kubernetes Deployment/RBAC/Lease.

---

### Task 1: Unified store contract

- Add a platform store interface covering cluster, capacity, Event and SQL database access.
- Make SQLite expose its database handle and initialize auth/session tables.
- Add a MySQL implementation for cluster metadata, health snapshots and capacity data.
- Verify both implementations compile against the same contract.

### Task 2: Automatic MySQL migrations

- Parse `PLATFORM_DATABASE_DSN` with UTC and timeout settings.
- Acquire a MySQL advisory lock before migration.
- Create every required table and index idempotently.
- Add an optional integration test using `MYSQL_TEST_DSN`.

### Task 3: Shared authentication sessions

- Store login sessions in the selected platform database instead of process memory.
- Keep login rate limits local to each API replica.
- Verify sessions and password changes through auth tests.

### Task 4: Kubernetes collector leadership

- Add Lease-based leader election using in-cluster credentials.
- Run health and Event collectors only inside the leading callback.
- Fall back to local single-process collection outside Kubernetes.
- Add RBAC for Lease access.

### Task 5: Deployment artifacts and documentation

- Add Deployment, Service, ServiceAccount, RBAC, ConfigMap and Secret examples.
- Provide optional MySQL StatefulSet and external MySQL connection instructions.
- Document automatic initialization, backup boundary and scaling settings.

### Task 6: Verification

- Run all Go tests.
- Run frontend tests, build and lint.
- Validate Kubernetes manifests with client-side dry run when kubectl is available.

