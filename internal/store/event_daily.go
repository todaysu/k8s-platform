package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

const (
	eventDailyMySQLBatch  = 200
	eventDailySQLiteBatch = 60

	eventDailyInsertPrefix = `INSERT INTO event_daily(bucket_day,cluster_id,category,object_key,regarding_kind,regarding_namespace,regarding_name,regarding_uid,type,reason,message_sample,event_count,occurrence_count,first_observed_at,last_observed_at) VALUES`
	eventDailyInsertRow    = `(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
)

func upsertEventDailyTx(ctx context.Context, tx *sql.Tx, rows []cluster.EventDailyRow, sqlite bool) error {
	if len(rows) == 0 {
		return nil
	}
	batchSize := eventDailyMySQLBatch
	if sqlite {
		batchSize = eventDailySQLiteBatch
	}
	for start := 0; start < len(rows); start += batchSize {
		end := min(start+batchSize, len(rows))
		query, args := eventDailyInsertStatement(rows[start:end], sqlite)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

func eventDailyInsertStatement(rows []cluster.EventDailyRow, sqlite bool) (string, []any) {
	var builder strings.Builder
	builder.WriteString(eventDailyInsertPrefix)
	args := make([]any, 0, len(rows)*15)
	for index, row := range rows {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(eventDailyInsertRow)
		var bucket, first, last any = row.BucketDay.UTC(), row.FirstObservedAt.UTC(), row.LastObservedAt.UTC()
		if sqlite {
			bucket = row.BucketDay.UTC().Format(timeLayout)
			first = row.FirstObservedAt.UTC().Format(timeLayout)
			last = row.LastObservedAt.UTC().Format(timeLayout)
		}
		args = append(args, bucket, row.ClusterID, row.Category, row.ObjectKey, row.Kind, row.Namespace, row.Name, row.UID, row.Type, row.Reason, row.Message, row.EventCount, row.OccurrenceCount, first, last)
	}
	if sqlite {
		builder.WriteString(` ON CONFLICT(bucket_day,cluster_id,category,object_key) DO UPDATE SET event_count=event_daily.event_count+excluded.event_count,occurrence_count=event_daily.occurrence_count+excluded.occurrence_count,first_observed_at=MIN(event_daily.first_observed_at,excluded.first_observed_at),last_observed_at=MAX(event_daily.last_observed_at,excluded.last_observed_at),message_sample=excluded.message_sample,type=excluded.type,reason=excluded.reason`)
	} else {
		builder.WriteString(` ON DUPLICATE KEY UPDATE event_count=event_count+VALUES(event_count),occurrence_count=occurrence_count+VALUES(occurrence_count),first_observed_at=LEAST(first_observed_at,VALUES(first_observed_at)),last_observed_at=GREATEST(last_observed_at,VALUES(last_observed_at)),message_sample=VALUES(message_sample),type=VALUES(type),reason=VALUES(reason)`)
	}
	return builder.String(), args
}

func eventDailyWhere(clusterID string, q cluster.EventQuery) (string, []any) {
	conditions := []string{"cluster_id=?", "last_observed_at>=?", "last_observed_at<=?"}
	args := []any{clusterID, q.Since.UTC(), q.Until.UTC()}
	if q.Type != "" {
		conditions = append(conditions, "type=?")
		args = append(args, q.Type)
	}
	if q.Namespace != "" {
		conditions = append(conditions, "regarding_namespace=?")
		args = append(args, q.Namespace)
	}
	if q.Kind != "" {
		conditions = append(conditions, "regarding_kind=?")
		args = append(args, q.Kind)
	}
	if q.ComponentOnly {
		conditions = append(conditions, "(regarding_kind='Node' OR regarding_namespace='kube-system' OR regarding_namespace LIKE 'openshift-%')")
	}
	if q.Search != "" {
		columns := []string{"reason", "message_sample", "regarding_name"}
		conditions = append(conditions, likeContainsClause(columns))
		args = append(args, likeContainsArgs(columns, q.Search)...)
	}
	extra, extraArgs := extraEventConditions(q)
	conditions = append(conditions, extra...)
	args = append(args, extraArgs...)
	return strings.Join(conditions, " AND "), args
}

func (store *MySQLEvents) AggregateDailyEvents(ctx context.Context, id string, q cluster.EventQuery) ([]cluster.EventGroupAggregate, error) {
	where, args := eventDailyWhere(id, q)
	rows, err := store.db.QueryContext(ctx, `SELECT category,MIN(regarding_kind),MIN(type),MIN(reason),SUM(event_count),SUM(occurrence_count),COUNT(DISTINCT object_key),COALESCE(GROUP_CONCAT(DISTINCT regarding_namespace),''),MIN(first_observed_at),MAX(last_observed_at),MAX(message_sample) FROM event_daily WHERE `+where+` GROUP BY category`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []cluster.EventGroupAggregate{}
	for rows.Next() {
		var value cluster.EventGroupAggregate
		var namespaces string
		value.ClusterID = id
		if err = rows.Scan(&value.Category, &value.RegardingKind, &value.Type, &value.Reason, &value.EventCount, &value.OccurrenceCount, &value.AffectedObjects, &namespaces, &value.FirstObservedAt, &value.LastObservedAt, &value.Message); err != nil {
			return nil, err
		}
		value.Namespaces = splitAggregateValues(namespaces)
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *MySQLEvents) AggregateDailyEventObjects(ctx context.Context, id string, q cluster.EventQuery) ([]cluster.EventObjectAggregate, error) {
	where, args := eventDailyWhere(id, q)
	rows, err := store.db.QueryContext(ctx, `SELECT category,regarding_kind,regarding_namespace,regarding_uid,regarding_name,SUM(event_count),SUM(occurrence_count),COALESCE(GROUP_CONCAT(DISTINCT type),''),MIN(first_observed_at),MAX(last_observed_at),MAX(message_sample) FROM event_daily WHERE `+where+` GROUP BY category,regarding_kind,regarding_namespace,regarding_uid,regarding_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []cluster.EventObjectAggregate{}
	for rows.Next() {
		var value cluster.EventObjectAggregate
		var types string
		value.ClusterID = id
		if err = rows.Scan(&value.Category, &value.RegardingKind, &value.RegardingNamespace, &value.RegardingUID, &value.RegardingName, &value.EventCount, &value.OccurrenceCount, &types, &value.FirstObservedAt, &value.LastObservedAt, &value.Message); err != nil {
			return nil, err
		}
		value.Types = splitAggregateValues(types)
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *SQLite) AggregateDailyEvents(ctx context.Context, id string, q cluster.EventQuery) ([]cluster.EventGroupAggregate, error) {
	where, args := eventDailyWhere(id, q)
	for index := range args {
		if value, ok := args[index].(time.Time); ok {
			args[index] = value.UTC().Format(timeLayout)
		}
	}
	rows, err := store.db.QueryContext(ctx, `SELECT category,MIN(regarding_kind),MIN(type),MIN(reason),SUM(event_count),SUM(occurrence_count),COUNT(DISTINCT object_key),COALESCE(GROUP_CONCAT(DISTINCT regarding_namespace),''),MIN(first_observed_at),MAX(last_observed_at),MAX(message_sample) FROM event_daily WHERE `+where+` GROUP BY category`, args...)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	result := []cluster.EventGroupAggregate{}
	for rows.Next() {
		var value cluster.EventGroupAggregate
		var namespaces, first, last string
		value.ClusterID = id
		if err = rows.Scan(&value.Category, &value.RegardingKind, &value.Type, &value.Reason, &value.EventCount, &value.OccurrenceCount, &value.AffectedObjects, &namespaces, &first, &last, &value.Message); err != nil {
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

func (store *SQLite) AggregateDailyEventObjects(ctx context.Context, id string, q cluster.EventQuery) ([]cluster.EventObjectAggregate, error) {
	where, args := eventDailyWhere(id, q)
	for index := range args {
		if value, ok := args[index].(time.Time); ok {
			args[index] = value.UTC().Format(timeLayout)
		}
	}
	rows, err := store.db.QueryContext(ctx, `SELECT category,regarding_kind,regarding_namespace,regarding_uid,regarding_name,SUM(event_count),SUM(occurrence_count),COALESCE(GROUP_CONCAT(DISTINCT type),''),MIN(first_observed_at),MAX(last_observed_at),MAX(message_sample) FROM event_daily WHERE `+where+` GROUP BY category,regarding_kind,regarding_namespace,regarding_uid,regarding_name`, args...)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	result := []cluster.EventObjectAggregate{}
	for rows.Next() {
		var value cluster.EventObjectAggregate
		var types, first, last string
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
