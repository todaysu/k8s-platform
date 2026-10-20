package store

import (
	"context"
	"example.com/k8s-platform/internal/cluster"
	"time"
)

func (store *SQLite) ScanEvents(ctx context.Context, id string, q cluster.EventQuery, visit func(cluster.ClusterEvent) error) error {
	where, args := eventWhere(id, q, false)
	rows, err := store.db.QueryContext(ctx, `SELECT cluster_id,event_uid,namespace,type,reason,action,message,regarding_api_version,regarding_kind,regarding_namespace,regarding_name,regarding_uid,reporting_controller,reporting_instance,occurrence_count,first_observed_at,last_observed_at,resource_version FROM cluster_events WHERE `+where+` ORDER BY last_observed_at DESC,event_uid DESC`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e cluster.ClusterEvent
		var first, last string
		if err = rows.Scan(&e.ClusterID, &e.EventUID, &e.Namespace, &e.Type, &e.Reason, &e.Action, &e.Message, &e.RegardingAPIVersion, &e.RegardingKind, &e.RegardingNamespace, &e.RegardingName, &e.RegardingUID, &e.ReportingController, &e.ReportingInstance, &e.OccurrenceCount, &first, &last, &e.ResourceVersion); err != nil {
			return err
		}
		e.FirstObservedAt, _ = time.Parse(timeLayout, first)
		e.LastObservedAt, _ = time.Parse(timeLayout, last)
		if err = visit(e); err != nil {
			return err
		}
	}
	return rows.Err()
}
