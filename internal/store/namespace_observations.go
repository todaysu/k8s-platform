package store

import (
	"context"
	"database/sql"
	"example.com/k8s-platform/internal/cluster"
	"sort"
)

// Batch one observation snapshot in a single fenced transaction. Stable key
// order avoids cross-shard deadlocks on shared namespace observation rows.
func observeNamespaceBatch(ctx context.Context, db *sql.DB, sqlite bool, values []cluster.NamespaceObservation) error {
	if len(values) == 0 {
		return nil
	}
	ordered := append([]cluster.NamespaceObservation(nil), values...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].TechnologyStack != ordered[j].TechnologyStack {
			return ordered[i].TechnologyStack < ordered[j].TechnologyStack
		}
		return ordered[i].Namespace < ordered[j].Namespace
	})
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkCollectionLease(ctx, tx, sqlite, ""); err != nil {
		return err
	}
	query := `INSERT INTO capacity_namespace_observations(technology_stack,namespace,first_seen_at,last_seen_at) VALUES(?,?,?,?) ON CONFLICT(technology_stack,namespace) DO UPDATE SET last_seen_at=MAX(last_seen_at,excluded.last_seen_at)`
	if !sqlite {
		query = `INSERT INTO capacity_namespace_observations(technology_stack,namespace,first_seen_at,last_seen_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE last_seen_at=GREATEST(last_seen_at,VALUES(last_seen_at))`
	}
	for _, value := range ordered {
		var first, last any = value.FirstSeenAt.UTC(), value.LastSeenAt.UTC()
		if sqlite {
			first = value.FirstSeenAt.UTC().Format(timeLayout)
			last = value.LastSeenAt.UTC().Format(timeLayout)
		}
		if _, err = tx.ExecContext(ctx, query, value.TechnologyStack, value.Namespace, first, last); err != nil {
			return err
		}
	}
	return commitCollectionTx(ctx, tx, sqlite)
}
