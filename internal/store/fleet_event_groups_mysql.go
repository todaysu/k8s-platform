package store

import (
	"context"
	"example.com/k8s-platform/internal/cluster"
	"strings"
)

// AggregateFleetEventGroups reduces the issue overview from up to 2*N SQL
// roundtrips to two grouped queries, preserving per-cluster daily/raw fallback.
func (store *MySQLEvents) AggregateFleetEventGroups(ctx context.Context, ids []string, q cluster.EventQuery, daily bool) (map[string][]cluster.EventGroupAggregate, error) {
	result := map[string][]cluster.EventGroupAggregate{}
	if len(ids) == 0 {
		return result, nil
	}
	if len(ids) > 1000 {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	load := func(selected []string, useDaily bool) error {
		if len(selected) == 0 {
			return nil
		}
		where, args := mysqlEventWhere("", q, false)
		fields := `cluster_id,` + eventCategoryMySQL + ` AS category,MIN(type),MIN(reason),MIN(regarding_kind),COUNT(*),COALESCE(SUM(occurrence_count),0),COUNT(DISTINCT IF(regarding_uid<>'',regarding_uid,CONCAT(regarding_kind,'/',regarding_namespace,'/',regarding_name))),COALESCE(GROUP_CONCAT(DISTINCT regarding_namespace),''),MIN(first_observed_at),MAX(last_observed_at),MAX(message)`
		table := "cluster_events"
		if useDaily {
			where, args = eventDailyWhere("", q)
			fields = `cluster_id,category,MIN(type),MIN(reason),MIN(regarding_kind),SUM(event_count),SUM(occurrence_count),COUNT(DISTINCT object_key),COALESCE(GROUP_CONCAT(DISTINCT regarding_namespace),''),MIN(first_observed_at),MAX(last_observed_at),MAX(message_sample)`
			table = "event_daily"
		}
		_, tail, _ := strings.Cut(where, " AND ")
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(selected)), ",")
		bound := make([]any, 0, len(selected)+len(args)-1)
		for _, id := range selected {
			bound = append(bound, id)
		}
		bound = append(bound, args[1:]...)
		rows, err := store.db.QueryContext(ctx, `SELECT `+fields+` FROM `+table+` WHERE cluster_id IN (`+placeholders+`) AND `+tail+` GROUP BY cluster_id,category`, bound...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v cluster.EventGroupAggregate
			var namespaces string
			if err = rows.Scan(&v.ClusterID, &v.Category, &v.Type, &v.Reason, &v.RegardingKind, &v.EventCount, &v.OccurrenceCount, &v.AffectedObjects, &namespaces, &v.FirstObservedAt, &v.LastObservedAt, &v.Message); err != nil {
				return err
			}
			v.Namespaces = splitAggregateValues(namespaces)
			result[v.ClusterID] = append(result[v.ClusterID], v)
		}
		return rows.Err()
	}
	if daily {
		if err := load(ids, true); err != nil {
			clear(result)
		}
	}
	remaining := make([]string, 0, len(ids))
	for _, id := range ids {
		if len(result[id]) == 0 {
			remaining = append(remaining, id)
		}
	}
	if err := load(remaining, false); err != nil {
		return nil, err
	}
	return result, nil
}
