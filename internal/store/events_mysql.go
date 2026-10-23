package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"example.com/k8s-platform/internal/cluster"
	"github.com/go-sql-driver/mysql"
)

type MySQLEvents struct{ db *sql.DB }

// SQLDB exposes the pooled connection to read-only services that consume the
// retained event aggregates. Callers must not close it; ownership remains with
// MySQLEvents.
func (store *MySQLEvents) SQLDB() *sql.DB { return store.db }

func OpenMySQLEvents(dsn string) (*MySQLEvents, error) {
	db, err := openMySQLDB(dsn)
	if err != nil {
		return nil, err
	}
	store := &MySQLEvents{db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func openMySQLDB(dsn string) (*sql.DB, error) {
	configuration, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse mysql event database DSN: %w", err)
	}
	configuration.ParseTime = true
	configuration.Loc = time.UTC
	db, err := sql.Open("mysql", configuration.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql event database: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect mysql event database: %w", err)
	}
	return db, nil
}
func (store *MySQLEvents) Close() error { return store.db.Close() }
func (store *MySQLEvents) migrate(ctx context.Context) error {
	statements := []string{`CREATE TABLE IF NOT EXISTS cluster_events (
cluster_id VARCHAR(64) NOT NULL,event_uid VARCHAR(128) NOT NULL,namespace VARCHAR(253) NOT NULL DEFAULT '',type VARCHAR(32) NOT NULL DEFAULT 'Normal',
reason VARCHAR(255) NOT NULL DEFAULT '',action VARCHAR(255) NOT NULL DEFAULT '',message TEXT NOT NULL,regarding_api_version VARCHAR(128) NOT NULL DEFAULT '',
regarding_kind VARCHAR(128) NOT NULL DEFAULT '',regarding_namespace VARCHAR(253) NOT NULL DEFAULT '',regarding_name VARCHAR(253) NOT NULL DEFAULT '',regarding_uid VARCHAR(128) NOT NULL DEFAULT '',
reporting_controller VARCHAR(255) NOT NULL DEFAULT '',reporting_instance VARCHAR(255) NOT NULL DEFAULT '',occurrence_count BIGINT NOT NULL DEFAULT 1,
first_observed_at DATETIME(6) NOT NULL,last_observed_at DATETIME(6) NOT NULL,resource_version VARCHAR(64) NOT NULL DEFAULT '',PRIMARY KEY(cluster_id,event_uid),
KEY idx_events_cluster_time(cluster_id,last_observed_at DESC),KEY idx_events_type_time(cluster_id,type,last_observed_at DESC),
KEY idx_events_namespace_time(cluster_id,regarding_namespace,last_observed_at DESC),KEY idx_events_object(cluster_id,regarding_uid),KEY idx_events_last_observed(last_observed_at)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS event_collector_checkpoints(cluster_id VARCHAR(64) PRIMARY KEY,state VARCHAR(32) NOT NULL,resource_version VARCHAR(64) NOT NULL DEFAULT '',last_success_at DATETIME(6) NULL,last_error TEXT NOT NULL,updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS event_daily(bucket_day DATETIME(6) NOT NULL,cluster_id VARCHAR(64) NOT NULL,category VARCHAR(64) NOT NULL,object_key CHAR(32) NOT NULL,regarding_kind VARCHAR(128) NOT NULL DEFAULT '',regarding_namespace VARCHAR(253) NOT NULL DEFAULT '',regarding_name VARCHAR(253) NOT NULL DEFAULT '',regarding_uid VARCHAR(128) NOT NULL DEFAULT '',type VARCHAR(32) NOT NULL DEFAULT '',reason VARCHAR(255) NOT NULL DEFAULT '',message_sample TEXT NOT NULL,event_count BIGINT NOT NULL DEFAULT 0,occurrence_count BIGINT NOT NULL DEFAULT 0,first_observed_at DATETIME(6) NOT NULL,last_observed_at DATETIME(6) NOT NULL,PRIMARY KEY(bucket_day,cluster_id,category,object_key),KEY idx_event_daily_cluster_time(cluster_id,last_observed_at),KEY idx_event_daily_cluster_category(cluster_id,category,last_observed_at)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS event_issue_hourly(bucket_start DATETIME(6) NOT NULL,cluster_id VARCHAR(64) NOT NULL,fingerprint VARCHAR(32) NOT NULL,type VARCHAR(32) NOT NULL DEFAULT '',reason VARCHAR(255) NOT NULL DEFAULT '',regarding_kind VARCHAR(128) NOT NULL DEFAULT '',message_sample TEXT NOT NULL,occurrence_count BIGINT NOT NULL DEFAULT 0,last_observed_at DATETIME(6) NOT NULL,PRIMARY KEY(bucket_start,cluster_id,fingerprint),KEY idx_event_rollup_cluster_time(cluster_id,bucket_start DESC)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`ALTER TABLE cluster_events ADD INDEX idx_events_kind_time(cluster_id,regarding_kind,last_observed_at DESC)`,
		`ALTER TABLE cluster_events ADD INDEX idx_events_last_observed(last_observed_at)`}
	for _, statement := range statements {
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			if mysqlError, ok := err.(*mysql.MySQLError); ok && mysqlError.Number == 1061 {
				continue
			}
			return fmt.Errorf("migrate mysql event storage: %w", err)
		}
	}
	return nil
}

const eventBatchSize = 200

const (
	eventInsertPrefix  = `INSERT INTO cluster_events(cluster_id,event_uid,namespace,type,reason,action,message,regarding_api_version,regarding_kind,regarding_namespace,regarding_name,regarding_uid,reporting_controller,reporting_instance,occurrence_count,first_observed_at,last_observed_at,resource_version) VALUES`
	eventInsertRow     = `(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	eventUpsertSuffix  = ` ON DUPLICATE KEY UPDATE namespace=VALUES(namespace),type=VALUES(type),reason=VALUES(reason),action=VALUES(action),message=VALUES(message),regarding_api_version=VALUES(regarding_api_version),regarding_kind=VALUES(regarding_kind),regarding_namespace=VALUES(regarding_namespace),regarding_name=VALUES(regarding_name),regarding_uid=VALUES(regarding_uid),reporting_controller=VALUES(reporting_controller),reporting_instance=VALUES(reporting_instance),occurrence_count=VALUES(occurrence_count),first_observed_at=LEAST(first_observed_at,VALUES(first_observed_at)),last_observed_at=GREATEST(last_observed_at,VALUES(last_observed_at)),resource_version=VALUES(resource_version)`
	rollupInsertPrefix = `INSERT INTO event_issue_hourly(bucket_start,cluster_id,fingerprint,type,reason,regarding_kind,message_sample,occurrence_count,last_observed_at) VALUES`
	rollupInsertRow    = `(?,?,?,?,?,?,?,?,?)`
	rollupUpsertSuffix = ` ON DUPLICATE KEY UPDATE occurrence_count=occurrence_count+VALUES(occurrence_count),last_observed_at=GREATEST(last_observed_at,VALUES(last_observed_at)),message_sample=VALUES(message_sample)`
)

// UpsertEvents writes one multi-row statement per batch instead of two
// statements per event. The previous implementation issued a SELECT with one
// OR predicate per event and then a prepared upsert per row; at 60 clusters
// replaying a full event list it exceeded the write capacity of a single
// MySQL instance.
func (store *MySQLEvents) UpsertEvents(ctx context.Context, events []cluster.ClusterEvent) error {
	if len(events) == 0 {
		return nil
	}
	events = latestEventVersions(events)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ids := map[string]bool{}
	for _, event := range events {
		if !ids[event.ClusterID] {
			if err := checkCollectionLease(ctx, tx, false, event.ClusterID); err != nil {
				return err
			}
			ids[event.ClusterID] = true
		}
	}

	previousCounts, err := loadPreviousEventCounts(ctx, tx, events)
	if err != nil {
		return err
	}
	for start := 0; start < len(events); start += eventBatchSize {
		end := min(start+eventBatchSize, len(events))
		query, args := eventUpsertStatement(events[start:end])
		if _, err = tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	rollups := buildEventRollups(events, previousCounts)
	for start := 0; start < len(rollups); start += eventBatchSize {
		end := min(start+eventBatchSize, len(rollups))
		query, args := rollupUpsertStatement(rollups[start:end])
		if _, err = tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	if err = upsertEventDailyTx(ctx, tx, cluster.BuildEventDailyRows(events, previousCounts), false); err != nil {
		return err
	}
	return commitCollectionTx(ctx, tx, false)
}

// latestEventVersions collapses repeated watch updates for the same event so
// occurrence deltas are applied once per batch.
func latestEventVersions(events []cluster.ClusterEvent) []cluster.ClusterEvent {
	if len(events) < 2 {
		return events
	}
	positions := make(map[string]int, len(events))
	result := make([]cluster.ClusterEvent, 0, len(events))
	for _, event := range events {
		key := event.ClusterID + "\x00" + event.EventUID
		position, exists := positions[key]
		if !exists {
			positions[key] = len(result)
			result = append(result, event)
			continue
		}
		if event.OccurrenceCount >= result[position].OccurrenceCount {
			result[position] = event
		}
	}
	return result
}

func loadPreviousEventCounts(ctx context.Context, tx *sql.Tx, events []cluster.ClusterEvent) (map[string]int64, error) {
	counts := make(map[string]int64, len(events))
	for start := 0; start < len(events); start += eventBatchSize {
		end := min(start+eventBatchSize, len(events))
		placeholders := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*2)
		for _, event := range events[start:end] {
			placeholders = append(placeholders, `(?,?)`)
			args = append(args, event.ClusterID, event.EventUID)
		}
		rows, err := tx.QueryContext(ctx, `SELECT cluster_id,event_uid,occurrence_count FROM cluster_events WHERE (cluster_id,event_uid) IN (`+strings.Join(placeholders, `,`)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var clusterID, eventUID string
			var count int64
			if err = rows.Scan(&clusterID, &eventUID, &count); err != nil {
				_ = rows.Close()
				return nil, err
			}
			counts[clusterID+"\x00"+eventUID] = count
		}
		if err = rows.Close(); err != nil {
			return nil, err
		}
	}
	return counts, nil
}

func eventUpsertStatement(events []cluster.ClusterEvent) (string, []any) {
	var builder strings.Builder
	builder.WriteString(eventInsertPrefix)
	args := make([]any, 0, len(events)*18)
	for index, event := range events {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(eventInsertRow)
		args = append(args,
			event.ClusterID, event.EventUID, event.Namespace, event.Type, event.Reason, event.Action,
			event.Message, event.RegardingAPIVersion, event.RegardingKind, event.RegardingNamespace,
			event.RegardingName, event.RegardingUID, event.ReportingController, event.ReportingInstance,
			event.OccurrenceCount, event.FirstObservedAt.UTC(), event.LastObservedAt.UTC(), event.ResourceVersion,
		)
	}
	builder.WriteString(eventUpsertSuffix)
	return builder.String(), args
}

type eventRollupRow struct {
	bucket        time.Time
	clusterID     string
	fingerprint   string
	eventType     string
	reason        string
	regardingKind string
	message       string
	count         int64
	lastObserved  time.Time
}

func buildEventRollups(events []cluster.ClusterEvent, previousCounts map[string]int64) []eventRollupRow {
	rows := make(map[string]*eventRollupRow, len(events))
	order := make([]string, 0, len(events))
	for _, event := range events {
		previous, exists := previousCounts[event.ClusterID+"\x00"+event.EventUID]
		delta := event.OccurrenceCount - previous
		if !exists && delta < 1 {
			delta = 1
		}
		if delta < 0 {
			delta = 0
		}
		if delta == 0 {
			continue
		}
		bucket := event.LastObservedAt.UTC().Truncate(time.Hour)
		fingerprint := cluster.EventFingerprint(event)
		key := bucket.Format(time.RFC3339Nano) + "|" + event.ClusterID + "|" + fingerprint
		row, exists := rows[key]
		if !exists {
			row = &eventRollupRow{
				bucket: bucket, clusterID: event.ClusterID, fingerprint: fingerprint,
				eventType: event.Type, reason: event.Reason, regardingKind: event.RegardingKind,
				message: event.Message, lastObserved: event.LastObservedAt.UTC(),
			}
			rows[key] = row
			order = append(order, key)
		}
		row.count += delta
		if observed := event.LastObservedAt.UTC(); observed.After(row.lastObserved) {
			row.lastObserved = observed
			row.message = event.Message
		}
	}
	result := make([]eventRollupRow, 0, len(order))
	for _, key := range order {
		result = append(result, *rows[key])
	}
	return result
}

func rollupUpsertStatement(rows []eventRollupRow) (string, []any) {
	var builder strings.Builder
	builder.WriteString(rollupInsertPrefix)
	args := make([]any, 0, len(rows)*9)
	for index, row := range rows {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(rollupInsertRow)
		args = append(args, row.bucket, row.clusterID, row.fingerprint, row.eventType, row.reason, row.regardingKind, row.message, row.count, row.lastObserved)
	}
	builder.WriteString(rollupUpsertSuffix)
	return builder.String(), args
}

func mysqlEventWhere(clusterID string, q cluster.EventQuery, cursor bool) (string, []any) {
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
		columns := []string{"reason", "message", "regarding_name", "reporting_controller"}
		conditions = append(conditions, likeContainsClause(columns))
		args = append(args, likeContainsArgs(columns, q.Search)...)
	}
	if cursor && q.CursorTime != nil {
		conditions = append(conditions, "(last_observed_at<? OR (last_observed_at=? AND event_uid<?))")
		args = append(args, q.CursorTime.UTC(), q.CursorTime.UTC(), q.CursorUID)
	}
	extra, extraArgs := extraEventConditions(q)
	conditions = append(conditions, extra...)
	args = append(args, extraArgs...)
	return strings.Join(conditions, " AND "), args
}

func splitAggregateValues(value string) []string {
	if value == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

const eventCategoryMySQL = `CASE
 WHEN LOWER(reason)='failedscheduling' THEN 'scheduling'
 WHEN LOWER(reason) LIKE '%mount%' OR LOWER(reason) LIKE '%attachvolume%' THEN 'storage'
 WHEN LOWER(message) LIKE '%imagepull%' OR LOWER(message) LIKE '%pull image%' OR LOWER(message) LIKE '%pulling image%' OR LOWER(reason)='errimagepull' OR LOWER(reason)='imagepullbackoff' THEN 'image-pull'
 WHEN LOWER(reason)='unhealthy' THEN 'probe'
 WHEN LOWER(reason)='backoff' OR LOWER(message) LIKE '%crashloop%' OR LOWER(message) LIKE '%restarting failed container%' THEN 'restart'
 WHEN LOWER(reason)='evicted' THEN 'eviction'
 WHEN regarding_kind='Node' THEN 'node'
 ELSE CONCAT('other-', LOWER(TRIM(CASE WHEN TRIM(reason)='' THEN regarding_kind ELSE reason END)))
END`

func (store *MySQLEvents) AggregateEvents(ctx context.Context, id string, q cluster.EventQuery) ([]cluster.EventGroupAggregate, error) {
	where, args := mysqlEventWhere(id, q, false)
	rows, err := store.db.QueryContext(ctx, `SELECT `+eventCategoryMySQL+` AS category,MIN(type),MIN(reason),MIN(regarding_kind),COUNT(*),COALESCE(SUM(occurrence_count),0),COUNT(DISTINCT IF(regarding_uid<>'',regarding_uid,CONCAT(regarding_kind,'/',regarding_namespace,'/',regarding_name))),COALESCE(GROUP_CONCAT(DISTINCT regarding_namespace),''),MIN(first_observed_at),MAX(last_observed_at),MAX(message) FROM cluster_events WHERE `+where+` GROUP BY category`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []cluster.EventGroupAggregate{}
	for rows.Next() {
		var value cluster.EventGroupAggregate
		var namespaces string
		value.ClusterID = id
		if err = rows.Scan(&value.Category, &value.Type, &value.Reason, &value.RegardingKind, &value.EventCount, &value.OccurrenceCount, &value.AffectedObjects, &namespaces, &value.FirstObservedAt, &value.LastObservedAt, &value.Message); err != nil {
			return nil, err
		}
		value.Namespaces = splitAggregateValues(namespaces)
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *MySQLEvents) AggregateEventObjects(ctx context.Context, id string, q cluster.EventQuery) ([]cluster.EventObjectAggregate, error) {
	where, args := mysqlEventWhere(id, q, false)
	rows, err := store.db.QueryContext(ctx, `SELECT `+eventCategoryMySQL+` AS category,regarding_kind,regarding_namespace,regarding_uid,regarding_name,COUNT(*),COALESCE(SUM(occurrence_count),0),COALESCE(GROUP_CONCAT(DISTINCT type),''),MIN(first_observed_at),MAX(last_observed_at),MAX(message) FROM cluster_events WHERE `+where+` GROUP BY category,regarding_kind,regarding_namespace,regarding_uid,regarding_name`, args...)
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

func (store *MySQLEvents) QueryEvents(ctx context.Context, id string, q cluster.EventQuery) (cluster.EventPage, error) {
	where, args := mysqlEventWhere(id, q, true)
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit+1)
	rows, err := store.db.QueryContext(ctx, `SELECT cluster_id,event_uid,namespace,type,reason,action,message,regarding_api_version,regarding_kind,regarding_namespace,regarding_name,regarding_uid,reporting_controller,reporting_instance,occurrence_count,first_observed_at,last_observed_at,resource_version FROM cluster_events WHERE `+where+` ORDER BY last_observed_at DESC,event_uid DESC LIMIT ?`, args...)
	if err != nil {
		return cluster.EventPage{}, err
	}
	defer rows.Close()
	items := make([]cluster.ClusterEvent, 0, limit+1)
	for rows.Next() {
		var e cluster.ClusterEvent
		if err = rows.Scan(&e.ClusterID, &e.EventUID, &e.Namespace, &e.Type, &e.Reason, &e.Action, &e.Message, &e.RegardingAPIVersion, &e.RegardingKind, &e.RegardingNamespace, &e.RegardingName, &e.RegardingUID, &e.ReportingController, &e.ReportingInstance, &e.OccurrenceCount, &e.FirstObservedAt, &e.LastObservedAt, &e.ResourceVersion); err != nil {
			return cluster.EventPage{}, err
		}
		items = append(items, e)
	}
	countWhere, countArgs := mysqlEventWhere(id, q, false)
	var total int64
	if err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM cluster_events WHERE "+countWhere, countArgs...).Scan(&total); err != nil {
		return cluster.EventPage{}, err
	}
	page := cluster.EventPage{Items: items, Total: total}
	if len(items) > limit {
		last := items[limit-1]
		page.Items = items[:limit]
		page.NextCursor = cluster.EncodeEventCursor(last.LastObservedAt, last.EventUID)
	}
	return page, nil
}
func (store *MySQLEvents) EventSummary(ctx context.Context, id string, since, until time.Time) (cluster.EventSummary, error) {
	var s cluster.EventSummary
	err := store.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(LOWER(type)='warning'),0),COALESCE(SUM(LOWER(type)='warning' OR LOWER(reason) IN ('backoff','failed','failedscheduling','failedmount','failedattachvolume','unhealthy','evicted','imagepullbackoff','errimagepull')),0),COUNT(DISTINCT IF(regarding_uid<>'',regarding_uid,CONCAT(regarding_kind,'/',regarding_namespace,'/',regarding_name))),COALESCE(SUM((LOWER(type)='warning' OR LOWER(reason) IN ('backoff','failed','failedscheduling','failedmount','failedattachvolume','unhealthy','evicted','imagepullbackoff','errimagepull')) AND (regarding_kind='Node' OR regarding_namespace='kube-system' OR regarding_namespace LIKE 'openshift-%')),0) FROM cluster_events WHERE cluster_id=? AND last_observed_at>=? AND last_observed_at<=?`, id, since.UTC(), until.UTC()).Scan(&s.Total, &s.Warning, &s.Attention, &s.AffectedObjects, &s.ComponentWarnings)
	if err != nil {
		return s, err
	}
	var last sql.NullTime
	err = store.db.QueryRowContext(ctx, "SELECT state,last_success_at,last_error FROM event_collector_checkpoints WHERE cluster_id=?", id).Scan(&s.CollectorState, &last, &s.LastError)
	if err == sql.ErrNoRows {
		s.CollectorState = "starting"
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if last.Valid {
		s.LastSuccessAt = &last.Time
	}
	return s, nil
}

func (store *MySQLEvents) EventFacets(ctx context.Context, clusterID string, since, until time.Time) (cluster.EventFacets, error) {
	result := cluster.EventFacets{Types: []string{}, Namespaces: []string{}, Kinds: []string{}}
	read := func(column string, target *[]string) error {
		rows, err := store.db.QueryContext(ctx, "SELECT DISTINCT "+column+" FROM cluster_events WHERE cluster_id=? AND last_observed_at>=? AND last_observed_at<=? AND "+column+"<>'' ORDER BY "+column, clusterID, since.UTC(), until.UTC())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				return err
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
func (store *MySQLEvents) SetEventCollectorStatus(ctx context.Context, s cluster.EventCollectorStatus) error {
	_, err := collectionExec(ctx, store.db, false, s.ClusterID, `INSERT INTO event_collector_checkpoints(cluster_id,state,resource_version,last_success_at,last_error,updated_at) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE state=VALUES(state),resource_version=VALUES(resource_version),last_success_at=COALESCE(VALUES(last_success_at),last_success_at),last_error=VALUES(last_error),updated_at=VALUES(updated_at)`, s.ClusterID, s.State, s.ResourceVersion, s.LastSuccessAt, s.LastError, s.UpdatedAt.UTC())
	return err
}
func (store *MySQLEvents) ListEventCollectorStatuses(ctx context.Context) ([]cluster.EventCollectorStatus, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT cluster_id,state,resource_version,last_success_at,last_error,updated_at FROM event_collector_checkpoints`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []cluster.EventCollectorStatus{}
	for rows.Next() {
		var value cluster.EventCollectorStatus
		var lastSuccess sql.NullTime
		if err = rows.Scan(&value.ClusterID, &value.State, &value.ResourceVersion, &lastSuccess, &value.LastError, &value.UpdatedAt); err != nil {
			return nil, err
		}
		if lastSuccess.Valid {
			value.LastSuccessAt = &lastSuccess.Time
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *MySQLEvents) DeleteEventsByRetention(ctx context.Context, normalCutoff, warningCutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 5000
	}
	// Split by type so each statement can use idx_events_last_observed for the
	// time range. The previous OR predicate forced a full table scan per batch.
	normal, err := store.db.ExecContext(ctx, "DELETE FROM cluster_events WHERE LOWER(type)='normal' AND last_observed_at<? LIMIT ?", normalCutoff.UTC(), limit)
	if err != nil {
		return 0, err
	}
	deleted, _ := normal.RowsAffected()
	remaining := limit - int(deleted)
	if remaining <= 0 {
		return deleted, nil
	}
	other, err := store.db.ExecContext(ctx, "DELETE FROM cluster_events WHERE LOWER(type)<>'normal' AND last_observed_at<? LIMIT ?", warningCutoff.UTC(), remaining)
	if err != nil {
		return deleted, err
	}
	more, _ := other.RowsAffected()
	return deleted + more, nil
}

func (store *MySQLEvents) DeleteEventRollupsBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	result, err := store.db.ExecContext(ctx, "DELETE FROM event_issue_hourly WHERE bucket_start<? LIMIT ?", cutoff.UTC(), limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (store *MySQLEvents) DeleteEventDailyBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	result, err := store.db.ExecContext(ctx, "DELETE FROM event_daily WHERE bucket_day<? LIMIT ?", cutoff.UTC(), limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (store *MySQLEvents) EventStorageStats(ctx context.Context) (cluster.EventStorageStats, error) {
	result := cluster.EventStorageStats{NormalRetentionDays: 7, WarningRetentionDays: 30, RollupRetentionDays: 365}
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM cluster_events").Scan(&result.RawRows); err != nil {
		return result, err
	}
	var latest sql.NullTime
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*),MAX(bucket_start) FROM event_issue_hourly").Scan(&result.HourlyRows, &latest); err != nil {
		return result, err
	}
	if latest.Valid {
		value := latest.Time.UTC()
		result.LatestRollupAt = &value
	}
	return result, nil
}

func (store *MySQLEvents) EnsureEventRollups(ctx context.Context) error {
	var count int64
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM event_issue_hourly").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	rows, err := store.db.QueryContext(ctx, `SELECT cluster_id,type,reason,message,regarding_kind,occurrence_count,last_observed_at FROM cluster_events`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type rollup struct {
		event  cluster.ClusterEvent
		bucket time.Time
	}
	groups := map[string]rollup{}
	for rows.Next() {
		var event cluster.ClusterEvent
		if err = rows.Scan(&event.ClusterID, &event.Type, &event.Reason, &event.Message, &event.RegardingKind, &event.OccurrenceCount, &event.LastObservedAt); err != nil {
			return err
		}
		bucket := event.LastObservedAt.UTC().Truncate(time.Hour)
		key := bucket.Format(time.RFC3339) + "|" + event.ClusterID + "|" + cluster.EventFingerprint(event)
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
	if err = rows.Close(); err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, value := range groups {
		event := value.event
		if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO event_issue_hourly(bucket_start,cluster_id,fingerprint,type,reason,regarding_kind,message_sample,occurrence_count,last_observed_at) VALUES(?,?,?,?,?,?,?,?,?)`, value.bucket, event.ClusterID, cluster.EventFingerprint(event), event.Type, event.Reason, event.RegardingKind, event.Message, event.OccurrenceCount, event.LastObservedAt.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (store *MySQLEvents) DeleteClusterEvents(ctx context.Context, clusterID string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "DELETE FROM cluster_events WHERE cluster_id=?", clusterID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM event_collector_checkpoints WHERE cluster_id=?", clusterID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM event_issue_hourly WHERE cluster_id=?", clusterID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM event_daily WHERE cluster_id=?", clusterID); err != nil {
		return err
	}
	return tx.Commit()
}
