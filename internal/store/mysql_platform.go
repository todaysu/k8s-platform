package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

type MySQL struct{ *MySQLEvents }

var _ PlatformStore = (*MySQL)(nil)

func OpenMySQL(dsn string) (*MySQL, error) {
	db, err := openMySQLDB(dsn)
	if err != nil {
		return nil, err
	}
	store := &MySQL{MySQLEvents: &MySQLEvents{db: db}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := store.migratePlatform(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (store *MySQL) SQLDB() *sql.DB { return store.db }

func (store *MySQL) migratePlatform(ctx context.Context) error {
	var locked int
	if err := store.db.QueryRowContext(ctx, `SELECT GET_LOCK('ftc-platform-schema', 30)`).Scan(&locked); err != nil || locked != 1 {
		return fmt.Errorf("acquire mysql migration lock: %w", err)
	}
	defer func() {
		_, _ = store.db.ExecContext(context.Background(), `SELECT RELEASE_LOCK('ftc-platform-schema')`)
	}()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations(version BIGINT PRIMARY KEY, applied_at DATETIME(6) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS clusters(
id VARCHAR(64) PRIMARY KEY,name VARCHAR(255) NOT NULL UNIQUE,environment VARCHAR(64) NOT NULL DEFAULT '未分类',cluster_type VARCHAR(32) NOT NULL DEFAULT '未标记',technology_stack VARCHAR(32) NOT NULL DEFAULT '未标记',
api_server VARCHAR(1024) NOT NULL,context_name VARCHAR(255) NOT NULL,kubernetes_version VARCHAR(64) NOT NULL,authentication_type VARCHAR(64) NOT NULL,connection_status VARCHAR(32) NOT NULL,
encrypted_credentials MEDIUMTEXT NOT NULL,fingerprint VARCHAR(128) NOT NULL UNIQUE,created_at DATETIME(6) NOT NULL,last_checked_at DATETIME(6) NOT NULL,
nodes_ready INT NULL,nodes_total INT NULL,instances_ready INT NULL,instances_total INT NULL,core_components_ready INT NULL,core_components_total INT NULL,
worker_nodes INT NULL,worker_cores BIGINT NULL,api_latency_ms BIGINT NULL,cpu_utilization DOUBLE NULL,memory_utilization DOUBLE NULL,metrics_available BOOLEAN NOT NULL DEFAULT FALSE,
KEY idx_clusters_environment_type(environment,cluster_type),KEY idx_clusters_status(connection_status),KEY idx_clusters_checked(last_checked_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS cluster_capacity_baselines(id BIGINT AUTO_INCREMENT PRIMARY KEY,cluster_id VARCHAR(64) NOT NULL,captured_at DATETIME(6) NOT NULL,system_namespaces JSON NOT NULL,worker_nodes INT NOT NULL,cpu_allocatable_milli BIGINT NOT NULL,memory_allocatable_bytes BIGINT NOT NULL,platform_cpu_requests_milli BIGINT NOT NULL,platform_cpu_limits_milli BIGINT NOT NULL,platform_memory_requests_bytes BIGINT NOT NULL,platform_memory_limits_bytes BIGINT NOT NULL,KEY idx_capacity_baseline_cluster_time(cluster_id,captured_at DESC,id DESC),CONSTRAINT fk_capacity_cluster FOREIGN KEY(cluster_id) REFERENCES clusters(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS capacity_classification_versions(id BIGINT AUTO_INCREMENT PRIMARY KEY,created_at DATETIME(6) NOT NULL,business_namespaces JSON NOT NULL,common_platform_namespaces JSON NOT NULL,stack_platform_namespaces JSON NOT NULL,note TEXT NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS capacity_namespace_observations(technology_stack VARCHAR(32) NOT NULL,namespace VARCHAR(253) NOT NULL,first_seen_at DATETIME(6) NOT NULL,last_seen_at DATETIME(6) NOT NULL,PRIMARY KEY(technology_stack,namespace)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS capacity_baseline_decisions(id BIGINT AUTO_INCREMENT PRIMARY KEY,cluster_id VARCHAR(64) NOT NULL,classification_version_id BIGINT NOT NULL,decision VARCHAR(32) NOT NULL,reason TEXT NOT NULL,created_at DATETIME(6) NOT NULL,KEY idx_capacity_decision_cluster_version(cluster_id,classification_version_id,created_at DESC,id DESC),CONSTRAINT fk_decision_cluster FOREIGN KEY(cluster_id) REFERENCES clusters(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS fleet_capacity_snapshots(cluster_id VARCHAR(64) PRIMARY KEY,captured_at DATETIME(6) NULL,last_attempt_at DATETIME(6) NOT NULL,payload JSON NULL,failure_code VARCHAR(64) NOT NULL DEFAULT '',CONSTRAINT fk_fleet_capacity_cluster FOREIGN KEY(cluster_id) REFERENCES clusters(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS auth_users(id BIGINT AUTO_INCREMENT PRIMARY KEY,username VARCHAR(64) NOT NULL UNIQUE,password_hash VARCHAR(255) NOT NULL,role VARCHAR(32) NOT NULL,disabled BOOLEAN NOT NULL DEFAULT FALSE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS auth_sessions(token_hash CHAR(64) PRIMARY KEY,user_id BIGINT NOT NULL,expires_at BIGINT NOT NULL,KEY idx_auth_sessions_expiry(expires_at),CONSTRAINT fk_session_user FOREIGN KEY(user_id) REFERENCES auth_users(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS cluster_resources(cluster_id VARCHAR(64) NOT NULL,kind VARCHAR(64) NOT NULL,namespace VARCHAR(253) NOT NULL DEFAULT '',name VARCHAR(253) NOT NULL,uid VARCHAR(128) NOT NULL DEFAULT '',status VARCHAR(64) NOT NULL DEFAULT '',owner_kind VARCHAR(64) NOT NULL DEFAULT '',owner_name VARCHAR(253) NOT NULL DEFAULT '',node_name VARCHAR(253) NOT NULL DEFAULT '',images TEXT NOT NULL,updated_at DATETIME(6) NOT NULL,PRIMARY KEY(cluster_id,kind,namespace,uid,name),KEY idx_resources_name(name),KEY idx_resources_updated(updated_at),CONSTRAINT fk_resources_cluster FOREIGN KEY(cluster_id) REFERENCES clusters(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS security_baseline_results(cluster_id VARCHAR(64) PRIMARY KEY,status VARCHAR(32) NOT NULL,scanner VARCHAR(64) NOT NULL,framework VARCHAR(64) NOT NULL,started_at DATETIME(6) NOT NULL,finished_at DATETIME(6) NULL,report_json MEDIUMTEXT NOT NULL,error_message TEXT NOT NULL,CONSTRAINT fk_security_baseline_cluster FOREIGN KEY(cluster_id) REFERENCES clusters(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS security_baseline_whitelist(cluster_id VARCHAR(64) NOT NULL,control_id VARCHAR(64) NOT NULL,reason VARCHAR(512) NOT NULL,created_at DATETIME(6) NOT NULL,PRIMARY KEY(cluster_id,control_id),CONSTRAINT fk_security_whitelist_cluster FOREIGN KEY(cluster_id) REFERENCES clusters(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS security_baseline_history(id BIGINT AUTO_INCREMENT PRIMARY KEY,cluster_id VARCHAR(64) NOT NULL,status VARCHAR(32) NOT NULL,scanner VARCHAR(64) NOT NULL,framework VARCHAR(64) NOT NULL,started_at DATETIME(6) NOT NULL,finished_at DATETIME(6) NULL,report_json MEDIUMTEXT NOT NULL,error_message TEXT NOT NULL,UNIQUE KEY uq_security_history_cluster_started(cluster_id,started_at),KEY idx_security_history_cluster_time(cluster_id,started_at DESC,id DESC),CONSTRAINT fk_security_history_cluster FOREIGN KEY(cluster_id) REFERENCES clusters(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS platform_settings(section VARCHAR(64) PRIMARY KEY,payload JSON NOT NULL,updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, statement := range statements {
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate mysql platform storage: %w", err)
		}
	}
	if err := store.MySQLEvents.migrate(ctx); err != nil {
		return err
	}
	if err := migrateSecurityJobs(ctx, store.db, false); err != nil {
		return err
	}

	if _, err := store.db.ExecContext(ctx, `INSERT IGNORE INTO security_baseline_history(cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message) SELECT cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message FROM security_baseline_results WHERE status IN ('completed','failed')`); err != nil {
		return fmt.Errorf("migrate mysql security history: %w", err)
	}
	_, err := store.db.ExecContext(ctx, `INSERT IGNORE INTO schema_migrations(version,applied_at) VALUES(1,UTC_TIMESTAMP(6))`)
	return err
}

func (store *MySQL) Create(ctx context.Context, record cluster.ClusterRecord) (cluster.ManagedCluster, error) {
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clusters WHERE name=?`, record.Name).Scan(&count); err != nil {
		return cluster.ManagedCluster{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if count > 0 {
		return cluster.ManagedCluster{}, &cluster.Error{Code: cluster.CodeNameDuplicate}
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clusters WHERE fingerprint=?`, record.Fingerprint).Scan(&count); err != nil {
		return cluster.ManagedCluster{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if count > 0 {
		return cluster.ManagedCluster{}, &cluster.Error{Code: cluster.CodeClusterDuplicate}
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO clusters(id,name,environment,cluster_type,technology_stack,api_server,context_name,kubernetes_version,authentication_type,connection_status,encrypted_credentials,fingerprint,created_at,last_checked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, record.ID, record.Name, record.Environment, record.ClusterType, record.TechnologyStack, record.APIServer, record.ContextName, record.KubernetesVersion, record.AuthenticationType, record.ConnectionStatus, record.EncryptedCredentials, record.Fingerprint, record.CreatedAt.UTC(), record.LastCheckedAt.UTC())
	if err != nil {
		return cluster.ManagedCluster{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return record.ManagedCluster, nil
}

const mysqlClusterSelect = `SELECT id,name,environment,cluster_type,technology_stack,api_server,context_name,kubernetes_version,authentication_type,connection_status,encrypted_credentials,fingerprint,created_at,last_checked_at,nodes_ready,nodes_total,instances_ready,instances_total,core_components_ready,core_components_total,worker_nodes,worker_cores,api_latency_ms,cpu_utilization,memory_utilization,metrics_available FROM clusters`

type sqlScanner interface{ Scan(...any) error }

func scanMySQLCluster(scanner sqlScanner) (cluster.ClusterRecord, error) {
	var record cluster.ClusterRecord
	err := scanner.Scan(&record.ID, &record.Name, &record.Environment, &record.ClusterType, &record.TechnologyStack, &record.APIServer, &record.ContextName, &record.KubernetesVersion, &record.AuthenticationType, &record.ConnectionStatus, &record.EncryptedCredentials, &record.Fingerprint, &record.CreatedAt, &record.LastCheckedAt, &record.NodesReady, &record.NodesTotal, &record.InstancesReady, &record.InstancesTotal, &record.CoreComponentsReady, &record.CoreComponentsTotal, &record.WorkerNodes, &record.WorkerCores, &record.APILatencyMS, &record.CPUUtilization, &record.MemoryUtilization, &record.MetricsAvailable)
	return record, err
}

func (store *MySQL) ListRecords(ctx context.Context) ([]cluster.ClusterRecord, error) {
	rows, err := store.db.QueryContext(ctx, mysqlClusterSelect+` ORDER BY created_at DESC`)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	records := []cluster.ClusterRecord{}
	for rows.Next() {
		record, err := scanMySQLCluster(rows)
		if err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (store *MySQL) GetRecord(ctx context.Context, id string) (cluster.ClusterRecord, error) {
	record, err := scanMySQLCluster(store.db.QueryRowContext(ctx, mysqlClusterSelect+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return cluster.ClusterRecord{}, &cluster.Error{Code: cluster.CodeClusterNotFound}
	}
	if err != nil {
		return cluster.ClusterRecord{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return record, nil
}

func (store *MySQL) UpdateClusterHealth(ctx context.Context, value cluster.ManagedCluster) error {
	result, err := collectionExec(ctx, store.db, false, value.ID, `UPDATE clusters SET connection_status=?,last_checked_at=?,nodes_ready=?,nodes_total=?,instances_ready=?,instances_total=?,core_components_ready=?,core_components_total=?,worker_nodes=?,worker_cores=?,api_latency_ms=?,cpu_utilization=?,memory_utilization=?,metrics_available=? WHERE id=?`, value.ConnectionStatus, value.LastCheckedAt.UTC(), value.NodesReady, value.NodesTotal, value.InstancesReady, value.InstancesTotal, value.CoreComponentsReady, value.CoreComponentsTotal, value.WorkerNodes, value.WorkerCores, value.APILatencyMS, value.CPUUtilization, value.MemoryUtilization, value.MetricsAvailable, value.ID)
	return mysqlRequireRow(result, err)
}

func (store *MySQL) Delete(ctx context.Context, id string) error {
	result, err := store.db.ExecContext(ctx, `DELETE FROM clusters WHERE id=?`, id)
	return mysqlRequireRow(result, err)
}

// ReplaceEncryptedCredentials rewrites the stored credential ciphertext so a
// record can be migrated to the AAD-bound format without re-enrolling.
func (store *MySQL) ReplaceEncryptedCredentials(ctx context.Context, id, encrypted string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE clusters SET encrypted_credentials=? WHERE id=?`, encrypted, id)
	return mysqlRequireRow(result, err)
}

func (store *MySQL) UpdateClusterClassification(ctx context.Context, id, clusterType, technologyStack string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE clusters SET cluster_type=?,technology_stack=? WHERE id=?`, clusterType, technologyStack, id)
	return mysqlRequireRow(result, err)
}

func mysqlRequireRow(result sql.Result, err error) error {
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if rows == 0 {
		return &cluster.Error{Code: cluster.CodeClusterNotFound}
	}
	return nil
}

func (store *MySQL) CreateCapacityBaseline(ctx context.Context, value cluster.CapacityBaseline) (cluster.CapacityBaseline, error) {
	namespaces, _ := json.Marshal(value.SystemNamespaces)
	result, err := store.db.ExecContext(ctx, `INSERT INTO cluster_capacity_baselines(cluster_id,captured_at,system_namespaces,worker_nodes,cpu_allocatable_milli,memory_allocatable_bytes,platform_cpu_requests_milli,platform_cpu_limits_milli,platform_memory_requests_bytes,platform_memory_limits_bytes) VALUES(?,?,?,?,?,?,?,?,?,?)`, value.ClusterID, value.CapturedAt.UTC(), namespaces, value.WorkerNodes, value.CPUAllocatableMilli, value.MemoryAllocatable, value.PlatformCPURequests, value.PlatformCPULimits, value.PlatformMemoryRequest, value.PlatformMemoryLimit)
	if err != nil {
		return value, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	value.ID, err = result.LastInsertId()
	return value, err
}

func (store *MySQL) LatestCapacityBaseline(ctx context.Context, id string) (*cluster.CapacityBaseline, error) {
	var value cluster.CapacityBaseline
	var namespaces []byte
	err := store.db.QueryRowContext(ctx, `SELECT id,cluster_id,captured_at,system_namespaces,worker_nodes,cpu_allocatable_milli,memory_allocatable_bytes,platform_cpu_requests_milli,platform_cpu_limits_milli,platform_memory_requests_bytes,platform_memory_limits_bytes FROM cluster_capacity_baselines WHERE cluster_id=? ORDER BY captured_at DESC,id DESC LIMIT 1`, id).Scan(&value.ID, &value.ClusterID, &value.CapturedAt, &namespaces, &value.WorkerNodes, &value.CPUAllocatableMilli, &value.MemoryAllocatable, &value.PlatformCPURequests, &value.PlatformCPULimits, &value.PlatformMemoryRequest, &value.PlatformMemoryLimit)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil || json.Unmarshal(namespaces, &value.SystemNamespaces) != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return &value, nil
}

func (store *MySQL) CreateCapacityClassification(ctx context.Context, value cluster.CapacityClassification) (cluster.CapacityClassification, error) {
	business, _ := json.Marshal(value.BusinessNamespaces)
	common, _ := json.Marshal(value.CommonPlatformNamespaces)
	stacks, _ := json.Marshal(value.StackPlatformNamespaces)
	result, err := store.db.ExecContext(ctx, `INSERT INTO capacity_classification_versions(created_at,business_namespaces,common_platform_namespaces,stack_platform_namespaces,note) VALUES(?,?,?,?,?)`, value.CreatedAt.UTC(), business, common, stacks, value.Note)
	if err != nil {
		return value, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	value.ID, err = result.LastInsertId()
	return value, err
}

func (store *MySQL) LatestCapacityClassification(ctx context.Context) (*cluster.CapacityClassification, error) {
	var value cluster.CapacityClassification
	var business, common, stacks []byte
	err := store.db.QueryRowContext(ctx, `SELECT id,created_at,business_namespaces,common_platform_namespaces,stack_platform_namespaces,note FROM capacity_classification_versions ORDER BY created_at DESC,id DESC LIMIT 1`).Scan(&value.ID, &value.CreatedAt, &business, &common, &stacks, &value.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil || json.Unmarshal(business, &value.BusinessNamespaces) != nil || json.Unmarshal(common, &value.CommonPlatformNamespaces) != nil || json.Unmarshal(stacks, &value.StackPlatformNamespaces) != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return &value, nil
}

func (store *MySQL) ObserveNamespaces(ctx context.Context, values []cluster.NamespaceObservation) error {
	return observeNamespaceBatch(ctx, store.db, false, values)
}

func (store *MySQL) ListNamespaceObservations(ctx context.Context) ([]cluster.NamespaceObservation, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT technology_stack,namespace,first_seen_at,last_seen_at FROM capacity_namespace_observations ORDER BY technology_stack,namespace`)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	values := []cluster.NamespaceObservation{}
	for rows.Next() {
		var value cluster.NamespaceObservation
		if err := rows.Scan(&value.TechnologyStack, &value.Namespace, &value.FirstSeenAt, &value.LastSeenAt); err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (store *MySQL) CreateCapacityBaselineDecision(ctx context.Context, value cluster.CapacityBaselineDecision) (cluster.CapacityBaselineDecision, error) {
	result, err := store.db.ExecContext(ctx, `INSERT INTO capacity_baseline_decisions(cluster_id,classification_version_id,decision,reason,created_at) VALUES(?,?,?,?,?)`, value.ClusterID, value.ClassificationVersionID, value.Decision, value.Reason, value.CreatedAt.UTC())
	if err != nil {
		return value, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	value.ID, err = result.LastInsertId()
	return value, err
}

func (store *MySQL) LatestCapacityBaselineDecision(ctx context.Context, clusterID string, versionID int64) (*cluster.CapacityBaselineDecision, error) {
	var value cluster.CapacityBaselineDecision
	err := store.db.QueryRowContext(ctx, `SELECT id,cluster_id,classification_version_id,decision,reason,created_at FROM capacity_baseline_decisions WHERE cluster_id=? AND classification_version_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, clusterID, versionID).Scan(&value.ID, &value.ClusterID, &value.ClassificationVersionID, &value.Decision, &value.Reason, &value.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return &value, nil
}
