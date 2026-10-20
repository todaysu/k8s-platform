package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"example.com/k8s-platform/internal/cluster"
	_ "modernc.org/sqlite"
)

type SQLite struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLite, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &SQLite{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (store *SQLite) Close() error {
	return store.db.Close()
}

func (store *SQLite) SQLDB() *sql.DB { return store.db }

func (store *SQLite) migrate(ctx context.Context) error {
	_, err := store.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS clusters (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  environment TEXT NOT NULL DEFAULT '未分类',
  cluster_type TEXT NOT NULL DEFAULT '未标记',
  technology_stack TEXT NOT NULL DEFAULT '未标记',
  api_server TEXT NOT NULL,
  context_name TEXT NOT NULL,
  kubernetes_version TEXT NOT NULL,
  authentication_type TEXT NOT NULL,
  connection_status TEXT NOT NULL,
  encrypted_credentials TEXT NOT NULL,
  fingerprint TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  last_checked_at TEXT NOT NULL,
  nodes_ready INTEGER,
  nodes_total INTEGER,
  instances_ready INTEGER,
  instances_total INTEGER,
  core_components_ready INTEGER,
  core_components_total INTEGER,
  worker_nodes INTEGER,
  worker_cores INTEGER,
  api_latency_ms INTEGER,
  cpu_utilization REAL,
  memory_utilization REAL,
  metrics_available INTEGER NOT NULL DEFAULT 0
);
`)
	if err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	if err := store.ensureEnvironmentColumn(ctx); err != nil {
		return err
	}
	if err := store.ensureTechnologyStackColumn(ctx); err != nil {
		return err
	}
	if err := store.ensureClusterTypeColumn(ctx); err != nil {
		return err
	}
	if err := store.ensureHealthSnapshotColumns(ctx); err != nil {
		return err
	}
	if err := store.migrateEvents(ctx); err != nil {
		return err
	}
	if err := store.migrateCapacity(ctx); err != nil {
		return err
	}
	if err := store.migrateResources(ctx); err != nil {
		return err
	}
	if _, err := store.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS auth_users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  disabled INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS auth_sessions (
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  FOREIGN KEY (user_id) REFERENCES auth_users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_expiry ON auth_sessions(expires_at);
`); err != nil {
		return fmt.Errorf("migrate sqlite auth storage: %w", err)
	}
	return nil
}

func (store *SQLite) ensureHealthSnapshotColumns(ctx context.Context) error {
	columns := []struct {
		name       string
		definition string
	}{
		{"nodes_ready", "INTEGER"}, {"nodes_total", "INTEGER"},
		{"instances_ready", "INTEGER"}, {"instances_total", "INTEGER"},
		{"core_components_ready", "INTEGER"}, {"core_components_total", "INTEGER"},
		{"worker_nodes", "INTEGER"}, {"worker_cores", "INTEGER"},
		{"api_latency_ms", "INTEGER"}, {"cpu_utilization", "REAL"},
		{"memory_utilization", "REAL"}, {"metrics_available", "INTEGER NOT NULL DEFAULT 0"},
	}
	rows, err := store.db.QueryContext(ctx, `PRAGMA table_info(clusters)`)
	if err != nil {
		return fmt.Errorf("inspect clusters health schema: %w", err)
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read clusters health schema: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close clusters health schema: %w", err)
	}
	for _, column := range columns {
		if existing[column.name] {
			continue
		}
		query := fmt.Sprintf("ALTER TABLE clusters ADD COLUMN %s %s", column.name, column.definition)
		if _, err := store.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("add clusters %s column: %w", column.name, err)
		}
	}
	return nil
}

func (store *SQLite) migrateCapacity(ctx context.Context) error {
	_, err := store.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS cluster_capacity_baselines (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  cluster_id TEXT NOT NULL,
  captured_at TEXT NOT NULL,
  system_namespaces TEXT NOT NULL,
  worker_nodes INTEGER NOT NULL,
  cpu_allocatable_milli INTEGER NOT NULL,
  memory_allocatable_bytes INTEGER NOT NULL,
  platform_cpu_requests_milli INTEGER NOT NULL,
  platform_cpu_limits_milli INTEGER NOT NULL,
  platform_memory_requests_bytes INTEGER NOT NULL,
  platform_memory_limits_bytes INTEGER NOT NULL,
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_capacity_baseline_cluster_time
  ON cluster_capacity_baselines(cluster_id, captured_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS capacity_classification_versions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at TEXT NOT NULL,
  business_namespaces TEXT NOT NULL,
  common_platform_namespaces TEXT NOT NULL,
  stack_platform_namespaces TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS capacity_namespace_observations (
  technology_stack TEXT NOT NULL,
  namespace TEXT NOT NULL,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  PRIMARY KEY (technology_stack, namespace)
);
CREATE TABLE IF NOT EXISTS capacity_baseline_decisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  cluster_id TEXT NOT NULL,
  classification_version_id INTEGER NOT NULL,
  decision TEXT NOT NULL,
  reason TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_capacity_decision_cluster_version
  ON capacity_baseline_decisions(cluster_id, classification_version_id, created_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS fleet_capacity_snapshots (
  cluster_id TEXT PRIMARY KEY,
  captured_at TEXT,
  last_attempt_at TEXT NOT NULL,
  payload TEXT,
  failure_code TEXT NOT NULL DEFAULT '',
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE
);
`)
	if err != nil {
		return fmt.Errorf("migrate capacity storage: %w", err)
	}
	return nil
}

func (store *SQLite) CreateCapacityClassification(ctx context.Context, value cluster.CapacityClassification) (cluster.CapacityClassification, error) {
	business, err := json.Marshal(value.BusinessNamespaces)
	if err != nil {
		return cluster.CapacityClassification{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	common, err := json.Marshal(value.CommonPlatformNamespaces)
	if err != nil {
		return cluster.CapacityClassification{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	stacks, err := json.Marshal(value.StackPlatformNamespaces)
	if err != nil {
		return cluster.CapacityClassification{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	result, err := store.db.ExecContext(ctx, `INSERT INTO capacity_classification_versions (created_at, business_namespaces, common_platform_namespaces, stack_platform_namespaces, note) VALUES (?, ?, ?, ?, ?)`, value.CreatedAt.UTC().Format(timeLayout), string(business), string(common), string(stacks), value.Note)
	if err != nil {
		return cluster.CapacityClassification{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	value.ID, err = result.LastInsertId()
	if err != nil {
		return cluster.CapacityClassification{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return value, nil
}

func (store *SQLite) LatestCapacityClassification(ctx context.Context) (*cluster.CapacityClassification, error) {
	var value cluster.CapacityClassification
	var createdAt, business, common, stacks string
	err := store.db.QueryRowContext(ctx, `SELECT id, created_at, business_namespaces, common_platform_namespaces, stack_platform_namespaces, note FROM capacity_classification_versions ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&value.ID, &createdAt, &business, &common, &stacks, &value.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	value.CreatedAt, err = time.Parse(timeLayout, createdAt)
	if err != nil || json.Unmarshal([]byte(business), &value.BusinessNamespaces) != nil || json.Unmarshal([]byte(common), &value.CommonPlatformNamespaces) != nil || json.Unmarshal([]byte(stacks), &value.StackPlatformNamespaces) != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return &value, nil
}

func (store *SQLite) ObserveNamespaces(ctx context.Context, values []cluster.NamespaceObservation) error {
	for _, value := range values {
		_, err := store.db.ExecContext(ctx, `INSERT INTO capacity_namespace_observations (technology_stack, namespace, first_seen_at, last_seen_at) VALUES (?, ?, ?, ?) ON CONFLICT(technology_stack, namespace) DO UPDATE SET last_seen_at = excluded.last_seen_at`, value.TechnologyStack, value.Namespace, value.FirstSeenAt.UTC().Format(timeLayout), value.LastSeenAt.UTC().Format(timeLayout))
		if err != nil {
			return &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
	}
	return nil
}

func (store *SQLite) ListNamespaceObservations(ctx context.Context) ([]cluster.NamespaceObservation, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT technology_stack, namespace, first_seen_at, last_seen_at FROM capacity_namespace_observations ORDER BY technology_stack, namespace`)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer func() { _ = rows.Close() }()
	values := make([]cluster.NamespaceObservation, 0)
	for rows.Next() {
		var value cluster.NamespaceObservation
		var firstSeen, lastSeen string
		if err := rows.Scan(&value.TechnologyStack, &value.Namespace, &firstSeen, &lastSeen); err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		value.FirstSeenAt, err = time.Parse(timeLayout, firstSeen)
		if err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		value.LastSeenAt, err = time.Parse(timeLayout, lastSeen)
		if err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return values, nil
}

func (store *SQLite) CreateCapacityBaselineDecision(ctx context.Context, value cluster.CapacityBaselineDecision) (cluster.CapacityBaselineDecision, error) {
	result, err := store.db.ExecContext(ctx, `INSERT INTO capacity_baseline_decisions (cluster_id, classification_version_id, decision, reason, created_at) VALUES (?, ?, ?, ?, ?)`, value.ClusterID, value.ClassificationVersionID, value.Decision, value.Reason, value.CreatedAt.UTC().Format(timeLayout))
	if err != nil {
		return cluster.CapacityBaselineDecision{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	value.ID, err = result.LastInsertId()
	if err != nil {
		return cluster.CapacityBaselineDecision{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return value, nil
}

func (store *SQLite) LatestCapacityBaselineDecision(ctx context.Context, clusterID string, versionID int64) (*cluster.CapacityBaselineDecision, error) {
	var value cluster.CapacityBaselineDecision
	var createdAt string
	err := store.db.QueryRowContext(ctx, `SELECT id, cluster_id, classification_version_id, decision, reason, created_at FROM capacity_baseline_decisions WHERE cluster_id = ? AND classification_version_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, clusterID, versionID).Scan(&value.ID, &value.ClusterID, &value.ClassificationVersionID, &value.Decision, &value.Reason, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	value.CreatedAt, err = time.Parse(timeLayout, createdAt)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return &value, nil
}

func (store *SQLite) migrateEvents(ctx context.Context) error {
	_, err := store.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS cluster_events (
  cluster_id TEXT NOT NULL, event_uid TEXT NOT NULL, namespace TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT 'Normal', reason TEXT NOT NULL DEFAULT '', action TEXT NOT NULL DEFAULT '', message TEXT NOT NULL DEFAULT '',
  regarding_api_version TEXT NOT NULL DEFAULT '', regarding_kind TEXT NOT NULL DEFAULT '', regarding_namespace TEXT NOT NULL DEFAULT '',
  regarding_name TEXT NOT NULL DEFAULT '', regarding_uid TEXT NOT NULL DEFAULT '', reporting_controller TEXT NOT NULL DEFAULT '',
  reporting_instance TEXT NOT NULL DEFAULT '', occurrence_count INTEGER NOT NULL DEFAULT 1,
  first_observed_at TEXT NOT NULL, last_observed_at TEXT NOT NULL, resource_version TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (cluster_id, event_uid), FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_events_cluster_time ON cluster_events(cluster_id, last_observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_cluster_type_time ON cluster_events(cluster_id, type, last_observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_cluster_namespace_time ON cluster_events(cluster_id, regarding_namespace, last_observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_cluster_kind_time ON cluster_events(cluster_id, regarding_kind, last_observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_cluster_object ON cluster_events(cluster_id, regarding_uid);
CREATE INDEX IF NOT EXISTS idx_events_last_observed ON cluster_events(last_observed_at);
CREATE TABLE IF NOT EXISTS event_collector_checkpoints (
  cluster_id TEXT PRIMARY KEY, state TEXT NOT NULL, resource_version TEXT NOT NULL DEFAULT '',
  last_success_at TEXT, last_error TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL,
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS event_daily (
  bucket_day TEXT NOT NULL, cluster_id TEXT NOT NULL, category TEXT NOT NULL, object_key TEXT NOT NULL,
  regarding_kind TEXT NOT NULL DEFAULT '', regarding_namespace TEXT NOT NULL DEFAULT '',
  regarding_name TEXT NOT NULL DEFAULT '', regarding_uid TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '', message_sample TEXT NOT NULL DEFAULT '',
  event_count INTEGER NOT NULL DEFAULT 0, occurrence_count INTEGER NOT NULL DEFAULT 0,
  first_observed_at TEXT NOT NULL, last_observed_at TEXT NOT NULL,
  PRIMARY KEY(bucket_day, cluster_id, category, object_key)
);
CREATE INDEX IF NOT EXISTS idx_event_daily_cluster_time ON event_daily(cluster_id, last_observed_at);
CREATE INDEX IF NOT EXISTS idx_event_daily_cluster_category ON event_daily(cluster_id, category, last_observed_at);
CREATE TABLE IF NOT EXISTS event_issue_hourly (
  bucket_start TEXT NOT NULL, cluster_id TEXT NOT NULL, fingerprint TEXT NOT NULL,
  type TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '', regarding_kind TEXT NOT NULL DEFAULT '',
  message_sample TEXT NOT NULL DEFAULT '', occurrence_count INTEGER NOT NULL DEFAULT 0,
  last_observed_at TEXT NOT NULL, PRIMARY KEY(bucket_start, cluster_id, fingerprint)
);`)
	if err != nil {
		return fmt.Errorf("migrate event storage: %w", err)
	}
	return nil
}

func (store *SQLite) migrateResources(ctx context.Context) error {
	_, err := store.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS cluster_resources (
  cluster_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  namespace TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  uid TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  owner_kind TEXT NOT NULL DEFAULT '',
  owner_name TEXT NOT NULL DEFAULT '',
  node_name TEXT NOT NULL DEFAULT '',
  images TEXT NOT NULL DEFAULT '[]',
  updated_at TEXT NOT NULL,
  PRIMARY KEY (cluster_id, kind, namespace, uid, name),
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_resources_name ON cluster_resources(name);
CREATE INDEX IF NOT EXISTS idx_resources_updated ON cluster_resources(updated_at);
`)
	if err != nil {
		return fmt.Errorf("migrate resource index: %w", err)
	}
	return nil
}

func (store *SQLite) ensureEnvironmentColumn(ctx context.Context) error {
	rows, err := store.db.QueryContext(ctx, `PRAGMA table_info(clusters)`)
	if err != nil {
		return fmt.Errorf("inspect clusters schema: %w", err)
	}
	defer func() { _ = rows.Close() }()

	hasEnvironment := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("read clusters schema: %w", err)
		}
		if name == "environment" {
			hasEnvironment = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read clusters schema: %w", err)
	}
	if hasEnvironment {
		return nil
	}
	if _, err := store.db.ExecContext(ctx, `ALTER TABLE clusters ADD COLUMN environment TEXT NOT NULL DEFAULT '未分类'`); err != nil {
		return fmt.Errorf("add clusters environment column: %w", err)
	}
	return nil
}

func (store *SQLite) ensureTechnologyStackColumn(ctx context.Context) error {
	rows, err := store.db.QueryContext(ctx, `PRAGMA table_info(clusters)`)
	if err != nil {
		return fmt.Errorf("inspect clusters schema: %w", err)
	}
	defer func() { _ = rows.Close() }()

	hasTechnologyStack := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("read clusters schema: %w", err)
		}
		if name == "technology_stack" {
			hasTechnologyStack = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read clusters schema: %w", err)
	}
	if hasTechnologyStack {
		return nil
	}
	if _, err := store.db.ExecContext(ctx, `ALTER TABLE clusters ADD COLUMN technology_stack TEXT NOT NULL DEFAULT '未标记'`); err != nil {
		return fmt.Errorf("add clusters technology_stack column: %w", err)
	}
	return nil
}

func (store *SQLite) ensureClusterTypeColumn(ctx context.Context) error {
	rows, err := store.db.QueryContext(ctx, `PRAGMA table_info(clusters)`)
	if err != nil {
		return fmt.Errorf("inspect clusters schema: %w", err)
	}
	defer func() { _ = rows.Close() }()

	hasClusterType := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("read clusters schema: %w", err)
		}
		if name == "cluster_type" {
			hasClusterType = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read clusters schema: %w", err)
	}
	if !hasClusterType {
		if _, err := store.db.ExecContext(ctx, `ALTER TABLE clusters ADD COLUMN cluster_type TEXT NOT NULL DEFAULT '未标记'`); err != nil {
			return fmt.Errorf("add clusters cluster_type column: %w", err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE clusters SET cluster_type = CASE WHEN technology_stack = 'OCP' THEN 'OCP' WHEN technology_stack IN ('TCS', 'CMBK8S') THEN 'G01' ELSE '未标记' END WHERE cluster_type = '' OR cluster_type = '未标记'`); err != nil {
		return fmt.Errorf("backfill clusters cluster_type: %w", err)
	}
	return nil
}

func (store *SQLite) Create(ctx context.Context, record cluster.ClusterRecord) (cluster.ManagedCluster, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return cluster.ManagedCluster{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer func() { _ = tx.Rollback() }()

	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM clusters WHERE name = ?`, record.Name).Scan(&existing); err != nil {
		return cluster.ManagedCluster{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if existing > 0 {
		return cluster.ManagedCluster{}, &cluster.Error{Code: cluster.CodeNameDuplicate}
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM clusters WHERE fingerprint = ?`, record.Fingerprint).Scan(&existing); err != nil {
		return cluster.ManagedCluster{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if existing > 0 {
		return cluster.ManagedCluster{}, &cluster.Error{Code: cluster.CodeClusterDuplicate}
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO clusters (
  id, name, environment, cluster_type, technology_stack, api_server, context_name, kubernetes_version,
  authentication_type, connection_status, encrypted_credentials,
  fingerprint, created_at, last_checked_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		record.ID,
		record.Name,
		record.Environment,
		record.ClusterType,
		record.TechnologyStack,
		record.APIServer,
		record.ContextName,
		record.KubernetesVersion,
		record.AuthenticationType,
		record.ConnectionStatus,
		record.EncryptedCredentials,
		record.Fingerprint,
		record.CreatedAt.UTC().Format(timeLayout),
		record.LastCheckedAt.UTC().Format(timeLayout),
	)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return cluster.ManagedCluster{}, err
		}
		return cluster.ManagedCluster{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if err := tx.Commit(); err != nil {
		return cluster.ManagedCluster{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return record.ManagedCluster, nil
}

func (store *SQLite) ListRecords(ctx context.Context) ([]cluster.ClusterRecord, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT id, name, environment, cluster_type, technology_stack, api_server, context_name, kubernetes_version,
       authentication_type, connection_status, encrypted_credentials, fingerprint,
       created_at, last_checked_at, nodes_ready, nodes_total, instances_ready, instances_total,
       core_components_ready, core_components_total, worker_nodes, worker_cores, api_latency_ms,
       cpu_utilization, memory_utilization, metrics_available
FROM clusters
ORDER BY created_at DESC
`)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer func() { _ = rows.Close() }()

	records := make([]cluster.ClusterRecord, 0)
	for rows.Next() {
		var record cluster.ClusterRecord
		var createdAt, lastCheckedAt string
		var metricsAvailable int
		if err := rows.Scan(
			&record.ID,
			&record.Name,
			&record.Environment,
			&record.ClusterType,
			&record.TechnologyStack,
			&record.APIServer,
			&record.ContextName,
			&record.KubernetesVersion,
			&record.AuthenticationType,
			&record.ConnectionStatus,
			&record.EncryptedCredentials,
			&record.Fingerprint,
			&createdAt,
			&lastCheckedAt,
			&record.NodesReady, &record.NodesTotal,
			&record.InstancesReady, &record.InstancesTotal,
			&record.CoreComponentsReady, &record.CoreComponentsTotal,
			&record.WorkerNodes, &record.WorkerCores, &record.APILatencyMS,
			&record.CPUUtilization, &record.MemoryUtilization, &metricsAvailable,
		); err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		record.CreatedAt, err = time.Parse(timeLayout, createdAt)
		if err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		record.MetricsAvailable = metricsAvailable != 0
		record.LastCheckedAt, err = time.Parse(timeLayout, lastCheckedAt)
		if err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return records, nil
}

func (store *SQLite) GetRecord(ctx context.Context, id string) (cluster.ClusterRecord, error) {
	var record cluster.ClusterRecord
	var createdAt, lastCheckedAt string
	var metricsAvailable int
	err := store.db.QueryRowContext(ctx, `
SELECT id, name, environment, cluster_type, technology_stack, api_server, context_name, kubernetes_version,
       authentication_type, connection_status, encrypted_credentials, fingerprint,
       created_at, last_checked_at, nodes_ready, nodes_total, instances_ready, instances_total,
       core_components_ready, core_components_total, worker_nodes, worker_cores, api_latency_ms,
       cpu_utilization, memory_utilization, metrics_available
FROM clusters
WHERE id = ?
`, id).Scan(
		&record.ID,
		&record.Name,
		&record.Environment,
		&record.ClusterType,
		&record.TechnologyStack,
		&record.APIServer,
		&record.ContextName,
		&record.KubernetesVersion,
		&record.AuthenticationType,
		&record.ConnectionStatus,
		&record.EncryptedCredentials,
		&record.Fingerprint,
		&createdAt,
		&lastCheckedAt,
		&record.NodesReady, &record.NodesTotal,
		&record.InstancesReady, &record.InstancesTotal,
		&record.CoreComponentsReady, &record.CoreComponentsTotal,
		&record.WorkerNodes, &record.WorkerCores, &record.APILatencyMS,
		&record.CPUUtilization, &record.MemoryUtilization, &metricsAvailable,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return cluster.ClusterRecord{}, &cluster.Error{Code: cluster.CodeClusterNotFound}
	}
	if err != nil {
		return cluster.ClusterRecord{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	record.CreatedAt, err = time.Parse(timeLayout, createdAt)
	if err != nil {
		return cluster.ClusterRecord{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	record.LastCheckedAt, err = time.Parse(timeLayout, lastCheckedAt)
	if err != nil {
		return cluster.ClusterRecord{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	record.MetricsAvailable = metricsAvailable != 0
	return record, nil
}

func (store *SQLite) UpdateClusterHealth(ctx context.Context, value cluster.ManagedCluster) error {
	result, err := store.db.ExecContext(ctx, `
UPDATE clusters SET
  connection_status = ?, last_checked_at = ?,
  nodes_ready = ?, nodes_total = ?, instances_ready = ?, instances_total = ?,
  core_components_ready = ?, core_components_total = ?, worker_nodes = ?, worker_cores = ?,
  api_latency_ms = ?, cpu_utilization = ?, memory_utilization = ?, metrics_available = ?
WHERE id = ?
`, value.ConnectionStatus, value.LastCheckedAt.UTC().Format(timeLayout),
		value.NodesReady, value.NodesTotal, value.InstancesReady, value.InstancesTotal,
		value.CoreComponentsReady, value.CoreComponentsTotal, value.WorkerNodes, value.WorkerCores,
		value.APILatencyMS, value.CPUUtilization, value.MemoryUtilization, value.MetricsAvailable, value.ID)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if updated == 0 {
		return &cluster.Error{Code: cluster.CodeClusterNotFound}
	}
	return nil
}

func (store *SQLite) Delete(ctx context.Context, id string) error {
	if _, err := store.db.ExecContext(ctx, `DELETE FROM cluster_capacity_baselines WHERE cluster_id = ?`, id); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	result, err := store.db.ExecContext(ctx, `DELETE FROM clusters WHERE id = ?`, id)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if deleted == 0 {
		return &cluster.Error{Code: cluster.CodeClusterNotFound}
	}
	return nil
}

func (store *SQLite) UpdateClusterClassification(ctx context.Context, id string, clusterType string, technologyStack string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE clusters SET cluster_type = ?, technology_stack = ? WHERE id = ?`, clusterType, technologyStack, id)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if updated == 0 {
		return &cluster.Error{Code: cluster.CodeClusterNotFound}
	}
	return nil
}

func (store *SQLite) CreateCapacityBaseline(ctx context.Context, baseline cluster.CapacityBaseline) (cluster.CapacityBaseline, error) {
	namespaces, err := json.Marshal(baseline.SystemNamespaces)
	if err != nil {
		return cluster.CapacityBaseline{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	result, err := store.db.ExecContext(ctx, `
INSERT INTO cluster_capacity_baselines (
  cluster_id, captured_at, system_namespaces, worker_nodes,
  cpu_allocatable_milli, memory_allocatable_bytes,
  platform_cpu_requests_milli, platform_cpu_limits_milli,
  platform_memory_requests_bytes, platform_memory_limits_bytes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, baseline.ClusterID, baseline.CapturedAt.UTC().Format(timeLayout), string(namespaces), baseline.WorkerNodes,
		baseline.CPUAllocatableMilli, baseline.MemoryAllocatable,
		baseline.PlatformCPURequests, baseline.PlatformCPULimits,
		baseline.PlatformMemoryRequest, baseline.PlatformMemoryLimit)
	if err != nil {
		return cluster.CapacityBaseline{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	baseline.ID, err = result.LastInsertId()
	if err != nil {
		return cluster.CapacityBaseline{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return baseline, nil
}

func (store *SQLite) LatestCapacityBaseline(ctx context.Context, clusterID string) (*cluster.CapacityBaseline, error) {
	var baseline cluster.CapacityBaseline
	var capturedAt, namespaces string
	err := store.db.QueryRowContext(ctx, `
SELECT id, cluster_id, captured_at, system_namespaces, worker_nodes,
       cpu_allocatable_milli, memory_allocatable_bytes,
       platform_cpu_requests_milli, platform_cpu_limits_milli,
       platform_memory_requests_bytes, platform_memory_limits_bytes
FROM cluster_capacity_baselines
WHERE cluster_id = ?
ORDER BY captured_at DESC, id DESC
LIMIT 1
`, clusterID).Scan(&baseline.ID, &baseline.ClusterID, &capturedAt, &namespaces, &baseline.WorkerNodes,
		&baseline.CPUAllocatableMilli, &baseline.MemoryAllocatable,
		&baseline.PlatformCPURequests, &baseline.PlatformCPULimits,
		&baseline.PlatformMemoryRequest, &baseline.PlatformMemoryLimit)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	baseline.CapturedAt, err = time.Parse(timeLayout, capturedAt)
	if err != nil || json.Unmarshal([]byte(namespaces), &baseline.SystemNamespaces) != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return &baseline, nil
}

const timeLayout = "2006-01-02T15:04:05.999999999Z07:00"
