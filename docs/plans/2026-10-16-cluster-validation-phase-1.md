# Cluster Validation Phase 1 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Turn the existing PatternFly onboarding dialog into a working connection test for uploaded Kubernetes Kubeconfigs.

**Architecture:** Keep the current Go modular monolith and PatternFly React frontend. The browser sends the cluster name and Kubeconfig to a same-origin API; Go selects `current-context`, rejects unsupported authentication, verifies TLS, queries the Kubernetes version endpoint, and returns only a non-sensitive preview. This phase deliberately does not persist credentials.

**Tech Stack:** Go 1.27, Kubernetes `client-go` v0.32.x, React 19, TypeScript, PatternFly 6, Vitest, Go standard testing.

---

## Scope

- Support pasted and uploaded Kubeconfig content up to 1 MiB.
- Require an HTTPS API server and inline CA data; support an inline bearer token or inline client certificate and private key.
- Automatically use `current-context` when the file contains multiple contexts.
- Test the API server with TLS verification and a five-second timeout.
- Return API Server, Context, Kubernetes version, and authentication type.
- Never return or log Token, private key, certificate contents, CA contents, or the original Kubeconfig.
- Do not save the cluster in this phase.

## Tasks

### Task 1: Parse the supported Kubeconfig subset

**Files:** `internal/cluster/errors.go`, `internal/cluster/kubeconfig.go`, `internal/cluster/kubeconfig_test.go`

1. Add parser tests covering default-context selection and rejected authentication/TLS forms.
2. Pin `client-go` to the Kubernetes 1.32-compatible minor.
3. Implement parsing and safe error codes.
4. Run `go test ./internal/cluster -run TestParseKubeconfig`.

### Task 2: Test Kubernetes connectivity

**Files:** `internal/cluster/connection.go`, `internal/cluster/connection_test.go`, `internal/cluster/validation.go`, `internal/cluster/validation_test.go`

1. Add HTTPS test-server cases for success and connection failures.
2. Build a TLS-verifying `client-go` discovery client with a five-second timeout.
3. Add a validation service returning a non-sensitive preview.
4. Run `go test ./internal/cluster`.

### Task 3: Expose the validation API

**Files:** `internal/httpapi/router.go`, `internal/httpapi/router_test.go`, `cmd/platform/main.go`

1. Add `POST /api/v1/clusters/validate` tests.
2. Limit and decode the JSON request.
3. Map safe domain errors to stable JSON responses.
4. Inject the validation service from `main`.
5. Run `go test ./...`.

### Task 4: Connect the PatternFly dialog

**Files:** `web/src/api/clusters.ts`, `web/src/components/AddClusterDialog.tsx`, `web/src/App.test.tsx`

1. Add a typed API client.
2. Read uploaded files and support pasted content.
3. Add loading, failure, and success preview states.
4. Add an interaction test with a mocked API response.
5. Run `npm test -- --run`, `npm run lint`, and `npm run build`.

## Completion boundary

The phase is complete when the UI can test an uploaded Kubeconfig using an embedded token or client certificate and display a safe preview. Saving the cluster, encryption, database persistence, login, periodic checks, and replacing mock overview data are later phases.
