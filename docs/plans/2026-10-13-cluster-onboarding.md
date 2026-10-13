# Cluster Onboarding Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build the first usable feature of a lightweight Headlamp-inspired Kubernetes multi-cluster platform: one administrator can add, inspect, rename, re-check, update, and remove a cluster using a cluster name and a token-based Kubeconfig.

**Architecture:** Use a React/TypeScript/Material UI frontend and a Go modular-monolith backend. The Go service owns authentication, encrypted cluster credentials, SQLite persistence, and Kubernetes access through official `client-go`; in production it also serves the compiled frontend so the platform remains one deployable service. Test and confirmation are separated by a ten-minute encrypted draft.

**Tech Stack:** Go 1.27, `client-go`, `database/sql`, SQLite, AES-GCM, React 19, TypeScript, Vite, Material UI, React Router, TanStack Query, Vitest, React Testing Library, Go standard testing.

---

## Scope guard

Read the approved design first:

```bash
sed -n '1,280p' docs/plans/2026-10-13-cluster-onboarding-design.md
```

This plan implements only cluster onboarding. Do not add nodes, Pods, Events, metrics, health scoring, alerts, logs, Exec, Helm, plugins, YAML editing, multi-user RBAC, Electron, Redis, queues, or Kubernetes deployment manifests.

Headlamp is an architectural and interaction reference only. Do not copy its whole repository into this project.

## Target structure

```text
.
├── .env.example
├── .gitignore
├── Makefile
├── README.md
├── go.mod
├── go.sum
├── cmd/platform/main.go
├── internal/
│   ├── auth/session.go
│   ├── cluster/
│   │   ├── connection.go
│   │   ├── crypto.go
│   │   ├── errors.go
│   │   ├── fingerprint.go
│   │   ├── kubeconfig.go
│   │   ├── model.go
│   │   ├── repository.go
│   │   └── service.go
│   ├── config/config.go
│   ├── httpapi/
│   │   ├── auth_handlers.go
│   │   ├── cluster_handlers.go
│   │   ├── middleware.go
│   │   ├── response.go
│   │   └── router.go
│   ├── store/
│   │   ├── migrations/001_initial.sql
│   │   └── sqlite.go
│   └── webui/webui.go
├── web/
│   ├── package.json
│   ├── package-lock.json
│   ├── vite.config.ts
│   ├── src/
│   │   ├── api/client.ts
│   │   ├── app/App.tsx
│   │   ├── app/theme.ts
│   │   ├── components/AppShell.tsx
│   │   ├── components/ConnectionStatus.tsx
│   │   ├── pages/ClusterDetailPage.tsx
│   │   ├── pages/ClusterImportPage.tsx
│   │   ├── pages/ClusterListPage.tsx
│   │   ├── pages/ClusterPreviewPage.tsx
│   │   ├── pages/LoginPage.tsx
│   │   ├── test/setup.ts
│   │   └── main.tsx
│   └── tests/
└── tests/fixtures/
```

---

### Task 1: Bootstrap Go and React projects

**Files:** `.gitignore`, `.env.example`, `Makefile`, `README.md`, `go.mod`, `cmd/platform/main.go`, `internal/config/config.go`, and the Vite-generated `web/` project.

**Step 1: Verify prerequisites**

```bash
go version
node --version
npm --version
```

Expected: Go 1.27.x and an actively supported Node.js LTS release. Do not use Node 20 or older.

**Step 2: Initialize Go**

```bash
go mod init example.com/k8s-platform
mkdir -p cmd/platform internal/config
```

Replace the example module path with an internal company path when one is known; do not invent a public repository URL.

Create a `Config` holding listen address, database path, administrator username, bcrypt password hash, base64 encryption key, secure-cookie flag, and session TTL. `Load()` returns an error when production secrets are absent.

**Step 3: Write a failing config test**

```go
func TestLoadRejectsMissingSecrets(t *testing.T) {
    t.Setenv("ADMIN_PASSWORD_HASH", "")
    t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "")
    if _, err := Load(); err == nil {
        t.Fatal("expected missing secret error")
    }
}
```

Run `go test ./internal/config`; expect FAIL until `Load()` is implemented, then PASS.

**Step 4: Create React**

```bash
npm create vite@latest web -- --template react-ts
cd web
npm install
npm install @mui/material @mui/icons-material @emotion/react @emotion/styled
npm install react-router-dom @tanstack/react-query
npm install -D vitest jsdom @testing-library/react @testing-library/jest-dom @testing-library/user-event
cd ..
```

Remove demo content. Add a minimal app title and one Vitest render test.

**Step 5: Add commands**

```make
test:
	go test ./...
	cd web && npm test -- --run

dev-backend:
	go run ./cmd/platform

dev-frontend:
	cd web && npm run dev
```

**Step 6: Verify and commit**

```bash
go test ./...
cd web && npm test -- --run && npm run build
cd ..
git add .
git commit -m "chore: bootstrap Go and React platform"
```

---

### Task 2: Add SQLite schema and repositories

**Files:** `internal/store/migrations/001_initial.sql`, `internal/store/sqlite.go`, `internal/store/sqlite_test.go`, `internal/cluster/model.go`, `internal/cluster/repository.go`.

**Step 1: Write a failing migration test**

Open an in-memory SQLite database, run migrations, and assert that `clusters`, `cluster_import_drafts`, and `operation_logs` exist.

**Step 2: Define schema**

- `clusters`: ID, unique name, API Server, Context, CA, encrypted Token, version, unique fingerprint, status, last safe error code and timestamps.
- `cluster_import_drafts`: ID, optional target cluster ID, parsed connection fields, expiry and timestamps.
- `operation_logs`: action, result, safe error code, cluster ID/name snapshots and time.

Never add raw Kubeconfig, request body, or plaintext Token columns.

**Step 3: Implement a narrow repository**

```go
type Repository interface {
    NameExists(context.Context, string) (bool, error)
    FingerprintExists(context.Context, string) (bool, error)
    CreateDraft(context.Context, ImportDraft) error
    DraftByID(context.Context, string) (ImportDraft, error)
    CreateClusterFromDraft(context.Context, string, time.Time) (Cluster, error)
    ListClusters(context.Context) ([]Cluster, error)
    ClusterByID(context.Context, string) (Cluster, error)
    UpdateCluster(context.Context, Cluster) error
    DeleteCluster(context.Context, string) error
    DeleteExpiredDrafts(context.Context, time.Time) (int64, error)
    AppendOperation(context.Context, OperationLog) error
}
```

Transactions protect confirmation, credential replacement and deletion audit.

**Step 4: Test and commit**

Test uniqueness, expired drafts and rollback, then run:

```bash
go test ./internal/store ./internal/cluster
git add internal/store internal/cluster go.mod go.sum
git commit -m "feat: add cluster persistence"
```

---

### Task 3: Parse the supported Kubeconfig subset

**Files:** `internal/cluster/errors.go`, `kubeconfig.go`, `kubeconfig_test.go`, `fingerprint.go`, `tests/fixtures/test-ca.pem`.

**Step 1: Add pinned Kubernetes dependencies**

Choose a stable `client-go` minor matching the newest Kubernetes minor in the managed fleet. Keep all Kubernetes Go modules on that minor.

```bash
go get k8s.io/client-go@v0.X.Y
go get k8s.io/apimachinery@v0.X.Y
go mod tidy
```

Record the chosen version in `README.md`; do not depend on `master`.

**Step 2: Define safe errors**

```go
type Error struct{ Code string }
func (e *Error) Error() string { return e.Code }
```

Include stable codes for invalid YAML, Context, HTTPS, inline CA and Token authentication.

**Step 3: Write parser tests first**

Test valid multi-context input and ensure `current-context` is selected. Reject broken references, HTTP server, CA file path, invalid CA, missing Token, `tokenFile`, exec plugin, client certificate, username/password, empty input and input over 1 MiB.

**Step 4: Implement with client-go**

Use `clientcmd.Load(data)`, then enforce the supported subset explicitly. Return only:

```go
type ParsedKubeconfig struct {
    APIServer   string
    ContextName string
    CAData      []byte
    Token       string
}
```

Compute the fingerprint from normalized API Server plus CA using SHA-256; never include Token.

**Step 5: Verify and commit**

```bash
go test ./internal/cluster -run 'TestParse|TestFingerprint'
git add internal/cluster tests/fixtures go.mod go.sum README.md
git commit -m "feat: parse restricted kubeconfigs"
```

---

### Task 4: Encrypt Tokens and test API connectivity

**Files:** `internal/cluster/crypto.go`, `crypto_test.go`, `connection.go`, `connection_test.go`.

**Step 1: Test AES-GCM first**

Test round trip, random nonces, malformed ciphertext, wrong key and missing key. Assert ciphertext never contains plaintext.

**Step 2: Implement encryption**

Accept a base64-encoded 32-byte key. Store base64 of `nonce || ciphertext`. Wrap failures as `ENCRYPTION_FAILED` without logging secrets.

Generate a local key with `openssl rand -base64 32`; never commit it.

**Step 3: Test HTTPS connection behavior**

Using an HTTPS test server, cover success, five-second timeout, TLS failure, 401, 403, redirect, invalid version response and oversized response.

**Step 4: Implement client-go discovery**

Build `rest.Config` from Server, CA and Bearer Token. Use `rest.HTTPClientFor`, reject redirects, set timeout and limit the response body, then call:

```go
discovery.NewDiscoveryClientForConfigAndClient(cfg, client).ServerVersion()
```

Return only `GitVersion` and map low-level failures to safe codes.

**Step 5: Verify and commit**

```bash
go test ./internal/cluster -run 'TestEncrypt|TestDecrypt|TestConnection'
git add internal/cluster
git commit -m "feat: secure cluster credentials and connectivity"
```

---

### Task 5: Implement the onboarding service

**Files:** `internal/cluster/service.go`, `service_test.go`.

**Step 1: Write service tests**

Validation must parse, connect, reject duplicate name/fingerprint, encrypt Token, create a ten-minute draft and return only safe preview fields. Confirmation must reject expired drafts, handle races, atomically create the cluster, delete the draft and write an operation log.

**Step 2: Define dependency interfaces**

```go
type Connector interface {
    Version(context.Context, ParsedKubeconfig) (string, error)
}
type Cipher interface {
    Encrypt(string) (string, error)
    Decrypt(string) (string, error)
}
```

Use fakes; unit tests must not reach a real cluster.

**Step 3: Implement workflows**

```go
func (s *Service) ValidateNew(ctx context.Context, name string, data []byte) (ImportPreview, error)
func (s *Service) ConfirmNew(ctx context.Context, draftID string) (Cluster, error)
func (s *Service) List(ctx context.Context) ([]Cluster, error)
func (s *Service) Get(ctx context.Context, id string) (Cluster, error)
```

Trim surrounding name whitespace only.

**Step 4: Verify and commit**

```bash
go test ./internal/cluster -run TestService
git add internal/cluster/service.go internal/cluster/service_test.go
git commit -m "feat: add cluster onboarding service"
```

---

### Task 6: Add single-administrator sessions and CSRF

**Files:** `internal/auth/session.go`, `session_test.go`, `internal/httpapi/auth_handlers.go`, `middleware.go`, `response.go`.

**Step 1: Add bcrypt and write tests**

```bash
go get golang.org/x/crypto/bcrypt
```

Test random session IDs, expiry, logout, password verification, `HttpOnly`, `SameSite=Strict`, conditional `Secure` and CSRF rejection.

**Step 2: Implement minimal sessions**

Use an in-memory mutex-protected map because version one has one administrator and one replica. A restart logs the administrator out; document this behavior.

`GET /api/v1/session` creates or reads a session and returns authentication state plus CSRF token. Login and mutations require `X-CSRF-Token`.

**Step 3: Add endpoints**

```text
GET  /api/v1/session
POST /api/v1/session/login
POST /api/v1/session/logout
```

Every response uses `{"data":{},"error":null}` or a safe coded error.

**Step 4: Verify and commit**

```bash
go test ./internal/auth ./internal/httpapi
git add internal/auth internal/httpapi go.mod go.sum README.md
git commit -m "feat: add administrator sessions"
```

---

### Task 7: Expose onboarding APIs

**Files:** `internal/httpapi/cluster_handlers.go`, `cluster_handlers_test.go`, `router.go`, `cmd/platform/main.go`.

**Step 1: Write handler tests**

Use `httptest` and a fake service. Cover authentication, CSRF, file upload, pasted text, both/neither input, size limit, safe preview, confirmation, list, detail and error mapping. Assert responses never contain Token or CA.

**Step 2: Implement routes**

```text
POST   /api/v1/clusters/validate
POST   /api/v1/clusters/confirm
GET    /api/v1/clusters
GET    /api/v1/clusters/{id}
PATCH  /api/v1/clusters/{id}
POST   /api/v1/clusters/{id}/validate
POST   /api/v1/clusters/{id}/confirm
POST   /api/v1/clusters/{id}/check
DELETE /api/v1/clusters/{id}
```

Use standard `http.ServeMux`. Keep handlers thin.

**Step 3: Wire the server**

Load config, open/migrate SQLite, build services and start an HTTP server with header/read/write/idle timeouts and graceful shutdown.

**Step 4: Verify and commit**

```bash
go test ./...
go vet ./...
git add cmd internal/httpapi
git commit -m "feat: expose cluster onboarding API"
```

---

### Task 8: Build login and onboarding UI

**Files:** `web/src/api/client.ts`, `app/App.tsx`, `app/theme.ts`, `components/AppShell.tsx`, `components/ConnectionStatus.tsx`, five page components and matching tests.

**Step 1: Build the API client**

Send cookies, bootstrap the CSRF token, add it to mutations and map coded errors. Never log request bodies.

**Step 2: Build authentication routing**

Unauthenticated users see only Login. Authenticated users see the app shell, username, cluster navigation and logout.

**Step 3: Build cluster pages**

- List: name, version, status, created time, loading/error/empty states.
- Import: file and paste tabs, one mode per request, disabled button while testing.
- Preview: API Server, Context, version and expiry only.
- Detail: non-sensitive fields and lifecycle actions.

Do not put Kubeconfig in browser storage, URL state, query parameters or logs.

**Step 4: Test journeys**

Use React Testing Library for login, empty list, upload/paste, error, preview/confirm and detail. Assert fixture secrets never enter the DOM.

**Step 5: Verify and commit**

```bash
cd web
npm test -- --run
npm run build
cd ..
git add web
git commit -m "feat: add cluster onboarding interface"
```

---

### Task 9: Add rename, re-check, credential update and removal

**Files:** cluster service/repository/handlers, `ClusterDetailPage.tsx`, new `ClusterCredentialUpdatePage.tsx`, backend and frontend tests.

**Step 1: Write backend tests first**

Cover duplicate rename; re-check updates; safe failure codes; failed replacement preserving ciphertext; atomic replacement; API Server change warning; and exact-name removal without remote API calls.

**Step 2: Implement service methods**

```go
func (s *Service) Rename(ctx context.Context, id, name string) error
func (s *Service) Check(ctx context.Context, id string) (Cluster, error)
func (s *Service) ValidateReplacement(ctx context.Context, id string, data []byte) (ImportPreview, error)
func (s *Service) ConfirmReplacement(ctx context.Context, id, draftID string) (Cluster, error)
func (s *Service) Remove(ctx context.Context, id, confirmationName string) error
```

Plaintext Token exists only during the connection call.

**Step 3: Add UI actions**

Use dialogs for rename and removal. Removal requires the exact name. Reuse import components for credential update and warn prominently if API Server changes.

**Step 4: Verify and commit**

```bash
go test ./...
cd web && npm test -- --run && npm run build
cd ..
git add internal web
git commit -m "feat: manage cluster onboarding lifecycle"
```

---

### Task 10: Ship one service and run security regression

**Files:** `internal/webui/webui.go`, frontend embed output, `cmd/platform/main.go`, `internal/httpapi/security_test.go`, `README.md`, `Makefile`.

**Step 1: Embed the frontend**

Build React, embed its output in Go and serve it for non-API routes. React Router fallback returns `index.html`; API paths never fall through. Development uses Vite proxy. Production stays same-origin and does not enable permissive CORS.

**Step 2: Add security tests**

Verify authentication, CSRF, cookie flags, HTTPS-only clusters, redirect rejection, safe errors, no secrets in HTTP/logs/database plaintext, expired draft rejection, timeouts and graceful shutdown.

**Step 3: Add draft cleanup**

Run at startup and periodically. Log only deletion count.

**Step 4: Document local use**

Explain Go/Node installation, npm install, AES key and bcrypt hash generation, development startup, safe test Kubeconfig, tests and production build. Never include real credentials.

**Step 5: Final verification and commit**

```bash
go test -race ./...
go vet ./...
cd web && npm test -- --run && npm run build
cd ..
make test
git diff --check
git status --short
git add Makefile README.md cmd internal web
git commit -m "build: ship single-service cluster platform"
```

---

## Final manual acceptance

Use a non-production cluster:

1. Log in as the administrator.
2. Add by file, remove, then add by pasted text.
3. Verify multi-context input selects `current-context`.
4. Test invalid Context, CA, Token and unreachable API.
5. Attempt duplicate onboarding under another name.
6. Attempt a bad credential update and verify old credentials still work.
7. Apply a valid Token update and rename the display name.
8. Remove the record and verify the real cluster remains unchanged.
9. Search pages, logs, SQLite text and Git history for the test Token; plaintext must not appear.

Only after this passes should the next feature begin: “集群列表与周期连接状态”。
