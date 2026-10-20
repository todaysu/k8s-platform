package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func (store *SQLite) UpsertEvents(ctx context.Context, events []cluster.ClusterEvent) error {
	if len(events) == 0 {
		return nil
	}
	events = latestEventVersions(events)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer func() { _ = tx.Rollback() }()
	previousCounts := make(map[string]int64, len(events))
	for _, event := range events {
		var previous int64
		err = tx.QueryRowContext(ctx, "SELECT occurrence_count FROM cluster_events WHERE cluster_id=? AND event_uid=?", event.ClusterID, event.EventUID).Scan(&previous)
		if err != nil && err != sql.ErrNoRows {
			return &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		if err == nil {
			previousCounts[cluster.EventCountKey(event.ClusterID, event.EventUID)] = previous
		}
	}
	for _, event := range events {
		key := cluster.EventCountKey(event.ClusterID, event.EventUID)
		previous, exists := previousCounts[key]
		delta := event.OccurrenceCount - previous
		if !exists && delta < 1 {
			delta = 1
		}
		if delta < 0 {
			delta = 0
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cluster_events (
cluster_id,event_uid,namespace,type,reason,action,message,regarding_api_version,regarding_kind,regarding_namespace,regarding_name,regarding_uid,
reporting_controller,reporting_instance,occurrence_count,first_observed_at,last_observed_at,resource_version)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(cluster_id,event_uid) DO UPDATE SET namespace=excluded.namespace,type=excluded.type,reason=excluded.reason,action=excluded.action,
message=excluded.message,regarding_api_version=excluded.regarding_api_version,regarding_kind=excluded.regarding_kind,
regarding_namespace=excluded.regarding_namespace,regarding_name=excluded.regarding_name,regarding_uid=excluded.regarding_uid,
reporting_controller=excluded.reporting_controller,reporting_instance=excluded.reporting_instance,occurrence_count=excluded.occurrence_count,
first_observed_at=MIN(cluster_events.first_observed_at,excluded.first_observed_at),last_observed_at=MAX(cluster_events.last_observed_at,excluded.last_observed_at),
resource_version=excluded.resource_version`, event.ClusterID, event.EventUID, event.Namespace, event.Type, event.Reason, event.Action, event.Message,
			event.RegardingAPIVersion, event.RegardingKind, event.RegardingNamespace, event.RegardingName, event.RegardingUID, event.ReportingController,
			event.ReportingInstance, event.OccurrenceCount, event.FirstObservedAt.UTC().Format(timeLayout), event.LastObservedAt.UTC().Format(timeLayout), event.ResourceVersion)
		if err != nil {
			return &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		if delta > 0 {
			bucket := event.LastObservedAt.UTC().Truncate(time.Hour).Format(timeLayout)
			_, err = tx.ExecContext(ctx, `INSERT INTO event_issue_hourly(bucket_start,cluster_id,fingerprint,type,reason,regarding_kind,message_sample,occurrence_count,last_observed_at)
VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(bucket_start,cluster_id,fingerprint) DO UPDATE SET occurrence_count=event_issue_hourly.occurrence_count+excluded.occurrence_count,last_observed_at=MAX(event_issue_hourly.last_observed_at,excluded.last_observed_at),message_sample=excluded.message_sample`, bucket, event.ClusterID, cluster.EventFingerprint(event), event.Type, event.Reason, event.RegardingKind, event.Message, delta, event.LastObservedAt.UTC().Format(timeLayout))
			if err != nil {
				return &cluster.Error{Code: cluster.CodePersistenceFailed}
			}
		}
	}
	if err = upsertEventDailyTx(ctx, tx, cluster.BuildEventDailyRows(events, previousCounts), true); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if err := tx.Commit(); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}

func eventWhere(clusterID string, query cluster.EventQuery, includeCursor bool) (string, []any) {
	conditions := []string{"cluster_id = ?", "last_observed_at >= ?", "last_observed_at <= ?"}
	args := []any{clusterID, query.Since.UTC().Format(timeLayout), query.Until.UTC().Format(timeLayout)}
	if query.Type != "" {
		conditions = append(conditions, "type = ?")
		args = append(args, query.Type)
	}
	if query.Namespace != "" {
		conditions = append(conditions, "regarding_namespace = ?")
		args = append(args, query.Namespace)
	}
	if query.Kind != "" {
		conditions = append(conditions, "regarding_kind = ?")
		args = append(args, query.Kind)
	}
	if query.ComponentOnly {
		conditions = append(conditions, "(regarding_kind = 'Node' OR regarding_namespace = 'kube-system' OR regarding_namespace LIKE 'openshift-%')")
	}
	if query.Search != "" {
		pattern := "%" + query.Search + "%"
		conditions = append(conditions, "(reason LIKE ? OR message LIKE ? OR regarding_name LIKE ? OR reporting_controller LIKE ?)")
		args = append(args, pattern, pattern, pattern, pattern)
	}
	if includeCursor && query.CursorTime != nil {
		conditions = append(conditions, "(last_observed_at < ? OR (last_observed_at = ? AND event_uid < ?))")
		formatted := query.CursorTime.UTC().Format(timeLayout)
		args = append(args, formatted, formatted, query.CursorUID)
	}
	extra, extraArgs := extraEventConditions(query)
	conditions = append(conditions, extra...)
	args = append(args, extraArgs...)
	return strings.Join(conditions, " AND "), args
}

const eventCategorySQLite = `CASE
 WHEN LOWER(reason)='failedscheduling' THEN 'scheduling'
 WHEN LOWER(reason) LIKE '%mount%' OR LOWER(reason) LIKE '%attachvolume%' THEN 'storage'
 WHEN LOWER(message) LIKE '%imagepull%' OR LOWER(message) LIKE '%pull image%' OR LOWER(message) LIKE '%pulling image%' OR LOWER(reason)='errimagepull' OR LOWER(reason)='imagepullbackoff' THEN 'image-pull'
 WHEN LOWER(reason)='unhealthy' THEN 'probe'
 WHEN LOWER(reason)='backoff' OR LOWER(message) LIKE '%crashloop%' OR LOWER(message) LIKE '%restarting failed container%' THEN 'restart'
 WHEN LOWER(reason)='evicted' THEN 'eviction'
 WHEN regarding_kind='Node' THEN 'node'
 ELSE 'other-' || LOWER(TRIM(CASE WHEN TRIM(reason)='' THEN regarding_kind ELSE reason END))
END`

func (store *SQLite) AggregateEvents(ctx context.Context, id string, q cluster.EventQuery) ([]cluster.EventGroupAggregate, error) {
	where, args := eventWhere(id, q, false)
	rows, err := store.db.QueryContext(ctx, `SELECT `+eventCategorySQLite+` AS category,MIN(type),MIN(reason),MIN(regarding_kind),COUNT(*),COALESCE(SUM(occurrence_count),0),COUNT(DISTINCT CASE WHEN regarding_uid<>'' THEN regarding_uid ELSE regarding_kind||'/'||regarding_namespace||'/'||regarding_name END),COALESCE(GROUP_CONCAT(DISTINCT regarding_namespace),''),MIN(first_observed_at),MAX(last_observed_at),MAX(message) FROM cluster_events WHERE `+where+` GROUP BY category`, args...)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	result := []cluster.EventGroupAggregate{}
	for rows.Next() {
		var value cluster.EventGroupAggregate
		var first, last, namespaces string
		value.ClusterID = id
		if err = rows.Scan(&value.Category, &value.Type, &value.Reason, &value.RegardingKind, &value.EventCount, &value.OccurrenceCount, &value.AffectedObjects, &namespaces, &first, &last, &value.Message); err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		value.Namespaces = splitAggregateValues(namespaces)
		value.FirstObservedAt, _ = time.Parse(timeLayout, first)
		value.LastObservedAt, _ = time.Parse(timeLayout, last)
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return result, nil
}

func (store *SQLite) AggregateEventObjects(ctx context.Context, id string, q cluster.EventQuery) ([]cluster.EventObjectAggregate, error) {
	where, args := eventWhere(id, q, false)
	rows, err := store.db.QueryContext(ctx, `SELECT `+eventCategorySQLite+` AS category,regarding_kind,regarding_namespace,regarding_uid,regarding_name,COUNT(*),COALESCE(SUM(occurrence_count),0),COALESCE(GROUP_CONCAT(DISTINCT type),''),MIN(first_observed_at),MAX(last_observed_at),MAX(message) FROM cluster_events WHERE `+where+` GROUP BY category,regarding_kind,regarding_namespace,regarding_uid,regarding_name`, args...)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	result := []cluster.EventObjectAggregate{}
	for rows.Next() {
		var value cluster.EventObjectAggregate
		var first, last, types string
		value.ClusterID = id
		if err = rows.Scan(&value.Category, &value.RegardingKind, &value.RegardingNamespace, &value.RegardingUID, &value.RegardingName, &value.EventCount, &value.OccurrenceCount, &types, &first, &last, &value.Message); err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		value.Types = splitAggregateValues(types)
		value.FirstObservedAt, _ = time.Parse(timeLayout, first)
		value.LastObservedAt, _ = time.Parse(timeLayout, last)
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return result, nil
}

func (store *SQLite) QueryEvents(ctx context.Context, clusterID string, query cluster.EventQuery) (cluster.EventPage, error) {
	where, args := eventWhere(clusterID, query, true)
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit+1)
	rows, err := store.db.QueryContext(ctx, `SELECT cluster_id,event_uid,namespace,type,reason,action,message,regarding_api_version,regarding_kind,
regarding_namespace,regarding_name,regarding_uid,reporting_controller,reporting_instance,occurrence_count,first_observed_at,last_observed_at,resource_version
FROM cluster_events WHERE `+where+` ORDER BY last_observed_at DESC,event_uid DESC LIMIT ?`, args...)
	if err != nil {
		return cluster.EventPage{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	items := make([]cluster.ClusterEvent, 0, limit+1)
	for rows.Next() {
		var e cluster.ClusterEvent
		var first, last string
		if err := rows.Scan(&e.ClusterID, &e.EventUID, &e.Namespace, &e.Type, &e.Reason, &e.Action, &e.Message, &e.RegardingAPIVersion, &e.RegardingKind, &e.RegardingNamespace, &e.RegardingName, &e.RegardingUID, &e.ReportingController, &e.ReportingInstance, &e.OccurrenceCount, &first, &last, &e.ResourceVersion); err != nil {
			return cluster.EventPage{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		e.FirstObservedAt, _ = time.Parse(timeLayout, first)
		e.LastObservedAt, _ = time.Parse(timeLayout, last)
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return cluster.EventPage{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if err := rows.Close(); err != nil {
		return cluster.EventPage{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	countWhere, countArgs := eventWhere(clusterID, query, false)
	var total int64
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM cluster_events WHERE "+countWhere, countArgs...).Scan(&total); err != nil {
		return cluster.EventPage{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	page := cluster.EventPage{Items: items, Total: total}
	if len(items) > limit {
		last := items[limit-1]
		page.Items = items[:limit]
		page.NextCursor = cluster.EncodeEventCursor(last.LastObservedAt, last.EventUID)
	}
	return page, nil
}

func (store *SQLite) EventSummary(ctx context.Context, clusterID string, since, until time.Time) (cluster.EventSummary, error) {
	var result cluster.EventSummary
	err := store.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN LOWER(type)='warning' THEN 1 ELSE 0 END),0),
COALESCE(SUM(CASE WHEN LOWER(type)='warning' OR LOWER(reason) IN ('backoff','failed','failedscheduling','failedmount','failedattachvolume','unhealthy','evicted','imagepullbackoff','errimagepull') THEN 1 ELSE 0 END),0),
COUNT(DISTINCT CASE WHEN regarding_uid<>'' THEN regarding_uid ELSE regarding_kind||'/'||regarding_namespace||'/'||regarding_name END),
COALESCE(SUM(CASE WHEN (LOWER(type)='warning' OR LOWER(reason) IN ('backoff','failed','failedscheduling','failedmount','failedattachvolume','unhealthy','evicted','imagepullbackoff','errimagepull')) AND (regarding_kind='Node' OR regarding_namespace='kube-system' OR regarding_namespace LIKE 'openshift-%') THEN 1 ELSE 0 END),0)
FROM cluster_events WHERE cluster_id=? AND last_observed_at>=? AND last_observed_at<=?`, clusterID, since.UTC().Format(timeLayout), until.UTC().Format(timeLayout)).Scan(&result.Total, &result.Warning, &result.Attention, &result.AffectedObjects, &result.ComponentWarnings)
	if err != nil {
		return result, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	var last sql.NullString
	err = store.db.QueryRowContext(ctx, `SELECT state,last_success_at,last_error FROM event_collector_checkpoints WHERE cluster_id=?`, clusterID).Scan(&result.CollectorState, &last, &result.LastError)
	if err == sql.ErrNoRows {
		result.CollectorState = "starting"
		return result, nil
	}
	if err != nil {
		return result, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if last.Valid {
		parsed, _ := time.Parse(timeLayout, last.String)
		result.LastSuccessAt = &parsed
	}
	return result, nil
}

func (store *SQLite) EventFacets(ctx context.Context, clusterID string, since, until time.Time) (cluster.EventFacets, error) {
	result := cluster.EventFacets{Types: []string{}, Namespaces: []string{}, Kinds: []string{}}
	read := func(column string, target *[]string) error {
		rows, err := store.db.QueryContext(ctx, "SELECT DISTINCT "+column+" FROM cluster_events WHERE cluster_id=? AND last_observed_at>=? AND last_observed_at<=? AND "+column+"<>'' ORDER BY "+column, clusterID, since.UTC().Format(timeLayout), until.UTC().Format(timeLayout))
		if err != nil {
			return &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		defer rows.Close()
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				return &cluster.Error{Code: cluster.CodePersistenceFailed}
			}
			*target = append(*target, value)
		}
		return rows.Err()
	}
	if err := read("type", &result.Types); err != nil {
		return result, err
	}
	if err := read("regarding_namespace", &result.Namespaces); err != nil {
		return result, err
	}
	if err := read("regarding_kind", &result.Kinds); err != nil {
		return result, err
	}
	return result, nil
}

func (store *SQLite) SetEventCollectorStatus(ctx context.Context, status cluster.EventCollectorStatus) error {
	var last any
	if status.LastSuccessAt != nil {
		last = status.LastSuccessAt.UTC().Format(timeLayout)
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO event_collector_checkpoints(cluster_id,state,resource_version,last_success_at,last_error,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(cluster_id) DO UPDATE SET state=excluded.state,resource_version=excluded.resource_version,last_success_at=COALESCE(excluded.last_success_at,event_collector_checkpoints.last_success_at),last_error=excluded.last_error,updated_at=excluded.updated_at`, status.ClusterID, status.State, status.ResourceVersion, last, status.LastError, status.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}
func (store *SQLite) ListEventCollectorStatuses(ctx context.Context) ([]cluster.EventCollectorStatus, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT cluster_id,state,resource_version,last_success_at,last_error,updated_at FROM event_collector_checkpoints`)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	result := []cluster.EventCollectorStatus{}
	for rows.Next() {
		var value cluster.EventCollectorStatus
		var lastSuccess sql.NullString
		var updated string
		if err = rows.Scan(&value.ClusterID, &value.State, &value.ResourceVersion, &lastSuccess, &value.LastError, &updated); err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		if lastSuccess.Valid {
			parsed, _ := time.Parse(timeLayout, lastSuccess.String)
			value.LastSuccessAt = &parsed
		}
		value.UpdatedAt, _ = time.Parse(timeLayout, updated)
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return result, nil
}

func (store *SQLite) DeleteEventsByRetention(ctx context.Context, normalCutoff, warningCutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 5000
	}
	// Split by type so the time index can drive the scan.
	normal, err := store.db.ExecContext(ctx, `DELETE FROM cluster_events WHERE rowid IN (SELECT rowid FROM cluster_events WHERE LOWER(type)='normal' AND last_observed_at < ? LIMIT ?)`, normalCutoff.UTC().Format(timeLayout), limit)
	if err != nil {
		return 0, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	deleted, _ := normal.RowsAffected()
	remaining := limit - int(deleted)
	if remaining <= 0 {
		return deleted, nil
	}
	other, err := store.db.ExecContext(ctx, `DELETE FROM cluster_events WHERE rowid IN (SELECT rowid FROM cluster_events WHERE LOWER(type)<>'normal' AND last_observed_at < ? LIMIT ?)`, warningCutoff.UTC().Format(timeLayout), remaining)
	if err != nil {
		return deleted, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	more, _ := other.RowsAffected()
	return deleted + more, nil
}

func (store *SQLite) DeleteEventRollupsBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	result, err := store.db.ExecContext(ctx, `DELETE FROM event_issue_hourly WHERE rowid IN (SELECT rowid FROM event_issue_hourly WHERE bucket_start < ? LIMIT ?)`, cutoff.UTC().Format(timeLayout), limit)
	if err != nil {
		return 0, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return result.RowsAffected()
}

func (store *SQLite) DeleteEventDailyBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	result, err := store.db.ExecContext(ctx, `DELETE FROM event_daily WHERE rowid IN (SELECT rowid FROM event_daily WHERE bucket_day < ? LIMIT ?)`, cutoff.UTC().Format(timeLayout), limit)
	if err != nil {
		return 0, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return result.RowsAffected()
}

func (store *SQLite) EventStorageStats(ctx context.Context) (cluster.EventStorageStats, error) {
	result := cluster.EventStorageStats{NormalRetentionDays: 7, WarningRetentionDays: 30, RollupRetentionDays: 365}
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM cluster_events").Scan(&result.RawRows); err != nil {
		return result, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	var latest sql.NullString
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*),MAX(bucket_start) FROM event_issue_hourly").Scan(&result.HourlyRows, &latest); err != nil {
		return result, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if latest.Valid {
		parsed, _ := time.Parse(timeLayout, latest.String)
		result.LatestRollupAt = &parsed
	}
	return result, nil
}

func (store *SQLite) EnsureEventRollups(ctx context.Context) error {
	var count int64
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM event_issue_hourly").Scan(&count); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if count > 0 {
		return nil
	}
	rows, err := store.db.QueryContext(ctx, `SELECT cluster_id,type,reason,message,regarding_kind,occurrence_count,last_observed_at FROM cluster_events`)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	type rollup struct {
		event  cluster.ClusterEvent
		bucket string
	}
	groups := map[string]rollup{}
	for rows.Next() {
		var event cluster.ClusterEvent
		var observed string
		if err := rows.Scan(&event.ClusterID, &event.Type, &event.Reason, &event.Message, &event.RegardingKind, &event.OccurrenceCount, &observed); err != nil {
			return &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		event.LastObservedAt, _ = time.Parse(timeLayout, observed)
		bucket := event.LastObservedAt.UTC().Truncate(time.Hour).Format(timeLayout)
		key := bucket + "|" + event.ClusterID + "|" + cluster.EventFingerprint(event)
		value := groups[key]
		if value.event.OccurrenceCount == 0 {
			value = rollup{event: event, bucket: bucket}
		} else {
			value.event.OccurrenceCount += event.OccurrenceCount
			if event.LastObservedAt.After(value.event.LastObservedAt) {
				value.event.LastObservedAt = event.LastObservedAt
				value.event.Message = event.Message
			}
		}
		groups[key] = value
	}
	if err := rows.Close(); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer func() { _ = tx.Rollback() }()
	for _, value := range groups {
		event := value.event
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO event_issue_hourly(bucket_start,cluster_id,fingerprint,type,reason,regarding_kind,message_sample,occurrence_count,last_observed_at) VALUES(?,?,?,?,?,?,?,?,?)`, value.bucket, event.ClusterID, cluster.EventFingerprint(event), event.Type, event.Reason, event.RegardingKind, event.Message, event.OccurrenceCount, event.LastObservedAt.UTC().Format(timeLayout)); err != nil {
			return &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
	}
	if err = tx.Commit(); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}

func (store *SQLite) DeleteClusterEvents(ctx context.Context, clusterID string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "DELETE FROM cluster_events WHERE cluster_id = ?", clusterID); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM event_collector_checkpoints WHERE cluster_id = ?", clusterID); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM event_issue_hourly WHERE cluster_id = ?", clusterID); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM event_daily WHERE cluster_id = ?", clusterID); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if err = tx.Commit(); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}
