package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

const (
	resourceMySQLBatch  = 200
	resourceSQLiteBatch = 80

	resourceInsertPrefix = `INSERT INTO cluster_resources(cluster_id,kind,namespace,name,uid,status,owner_kind,owner_name,node_name,images,updated_at) VALUES`
	resourceInsertRow    = `(?,?,?,?,?,?,?,?,?,?,?)`

	resourceSelectPrefix = `SELECT c.id,c.name,c.environment,c.technology_stack,r.kind,r.namespace,r.name,r.status,r.owner_kind,r.owner_name,r.images FROM cluster_resources r JOIN clusters c ON c.id=r.cluster_id WHERE `
	resourceSearchWhere  = `LOWER(r.name) LIKE ? ESCAPE '!' OR LOWER(r.namespace) LIKE ? ESCAPE '!' OR LOWER(r.kind) LIKE ? ESCAPE '!' OR LOWER(r.owner_name) LIKE ? ESCAPE '!' OR LOWER(r.node_name) LIKE ? ESCAPE '!' OR LOWER(r.images) LIKE ? ESCAPE '!'`
	resourceSelectOrder  = ` ORDER BY CASE WHEN LOWER(r.name)=? THEN 0 WHEN LOWER(r.name) LIKE ? ESCAPE '!' THEN 1 ELSE 2 END,LOWER(r.name),c.name,r.kind LIMIT ?`
)

func (store *SQLite) ReplaceClusterResources(ctx context.Context, clusterID string, resources []cluster.IndexedResource) error {
	return replaceClusterResources(ctx, store.db, clusterID, resources, true)
}

func (store *MySQL) ReplaceClusterResources(ctx context.Context, clusterID string, resources []cluster.IndexedResource) error {
	return replaceClusterResources(ctx, store.db, clusterID, resources, false)
}

func replaceClusterResources(ctx context.Context, db *sql.DB, clusterID string, resources []cluster.IndexedResource, sqlite bool) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DELETE FROM cluster_resources WHERE cluster_id=?`, clusterID); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	batchSize := resourceMySQLBatch
	if sqlite {
		batchSize = resourceSQLiteBatch
	}
	for start := 0; start < len(resources); start += batchSize {
		end := min(start+batchSize, len(resources))
		query, args := resourceInsertStatement(resources[start:end], sqlite)
		if _, err = tx.ExecContext(ctx, query, args...); err != nil {
			return &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
	}
	if err = tx.Commit(); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}

func resourceInsertStatement(resources []cluster.IndexedResource, sqlite bool) (string, []any) {
	var builder strings.Builder
	builder.WriteString(resourceInsertPrefix)
	args := make([]any, 0, len(resources)*11)
	for index, resource := range resources {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(resourceInsertRow)
		images := resource.Images
		if images == nil {
			images = []string{}
		}
		encoded, _ := json.Marshal(images)
		updatedAt := resource.UpdatedAt.UTC()
		if resource.UpdatedAt.IsZero() {
			updatedAt = time.Now().UTC()
		}
		var timestamp any = updatedAt
		if sqlite {
			timestamp = updatedAt.Format(timeLayout)
		}
		args = append(args,
			resource.ClusterID, resource.Kind, resource.Namespace, resource.Name, resource.UID,
			resource.Status, resource.OwnerKind, resource.OwnerName, resource.NodeName, string(encoded), timestamp,
		)
	}
	return builder.String(), args
}

func (store *SQLite) SearchClusterResources(ctx context.Context, query string, limit int) ([]cluster.GlobalResourceResult, error) {
	rows, err := store.db.QueryContext(ctx, resourceSearchQuery(), resourceSearchArgs(query, limit)...)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return scanClusterResources(rows)
}

func (store *MySQL) SearchClusterResources(ctx context.Context, query string, limit int) ([]cluster.GlobalResourceResult, error) {
	rows, err := store.db.QueryContext(ctx, resourceSearchQuery(), resourceSearchArgs(query, limit)...)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return scanClusterResources(rows)
}

func resourceSearchQuery() string {
	return resourceSelectPrefix + resourceSearchWhere + resourceSelectOrder
}

func resourceSearchArgs(query string, limit int) []any {
	if limit <= 0 || limit > 2000 {
		limit = 800
	}
	normalized := strings.ToLower(strings.TrimSpace(query))
	pattern, prefix := resourceSearchPatterns(normalized)
	return []any{pattern, pattern, pattern, pattern, pattern, pattern, normalized, prefix, limit}
}

func resourceSearchPatterns(query string) (string, string) {
	escaped := escapeResourceLike(query)
	return "%" + escaped + "%", escaped + "%"
}

func escapeResourceLike(value string) string {
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	value = strings.ReplaceAll(value, "_", "!_")
	return value
}

func scanClusterResources(rows *sql.Rows) ([]cluster.GlobalResourceResult, error) {
	defer rows.Close()
	result := []cluster.GlobalResourceResult{}
	for rows.Next() {
		var value cluster.GlobalResourceResult
		var images string
		if err := rows.Scan(&value.ClusterID, &value.ClusterName, &value.Environment, &value.TechnologyStack, &value.Kind, &value.Namespace, &value.Name, &value.Status, &value.OwnerKind, &value.OwnerName, &images); err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		if images != "" {
			_ = json.Unmarshal([]byte(images), &value.Images)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return result, nil
}

func (store *SQLite) CountClusterResources(ctx context.Context) (int64, error) {
	var count int64
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cluster_resources r JOIN clusters c ON c.id=r.cluster_id`).Scan(&count); err != nil {
		return 0, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return count, nil
}

func (store *MySQL) CountClusterResources(ctx context.Context) (int64, error) {
	var count int64
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cluster_resources r JOIN clusters c ON c.id=r.cluster_id`).Scan(&count); err != nil {
		return 0, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return count, nil
}

func (store *SQLite) DeleteClusterResources(ctx context.Context, clusterID string) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM cluster_resources WHERE cluster_id=?`, clusterID)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}

func (store *MySQL) DeleteClusterResources(ctx context.Context, clusterID string) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM cluster_resources WHERE cluster_id=?`, clusterID)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}
