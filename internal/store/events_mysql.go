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
KEY idx_events_namespace_time(cluster_id,regarding_namespace,last_observed_at DESC),KEY idx_events_object(cluster_id,regarding_uid)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS event_collector_checkpoints(cluster_id VARCHAR(64) PRIMARY KEY,state VARCHAR(32) NOT NULL,resource_version VARCHAR(64) NOT NULL DEFAULT '',last_success_at DATETIME(6) NULL,last_error TEXT NOT NULL,updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS event_issue_hourly(bucket_start DATETIME(6) NOT NULL,cluster_id VARCHAR(64) NOT NULL,fingerprint VARCHAR(32) NOT NULL,type VARCHAR(32) NOT NULL DEFAULT '',reason VARCHAR(255) NOT NULL DEFAULT '',regarding_kind VARCHAR(128) NOT NULL DEFAULT '',message_sample TEXT NOT NULL,occurrence_count BIGINT NOT NULL DEFAULT 0,last_observed_at DATETIME(6) NOT NULL,PRIMARY KEY(bucket_start,cluster_id,fingerprint),KEY idx_event_rollup_cluster_time(cluster_id,bucket_start DESC)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`ALTER TABLE cluster_events ADD INDEX idx_events_kind_time(cluster_id,regarding_kind,last_observed_at DESC)`}
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
func (store *MySQLEvents) UpsertEvents(ctx context.Context, events []cluster.ClusterEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	previousCounts := make(map[string]int64, len(events))
	for start := 0; start < len(events); start += 500 {
		end := start + 500
		if end > len(events) {
			end = len(events)
		}
		conditions := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*2)
		for _, event := range events[start:end] {
			conditions = append(conditions, `(cluster_id=? AND event_uid=?)`)
			args = append(args, event.ClusterID, event.EventUID)
		}
		rows, queryErr := tx.QueryContext(ctx, `SELECT cluster_id,event_uid,occurrence_count FROM cluster_events WHERE `+strings.Join(conditions, ` OR `), args...)
		if queryErr != nil {
			return queryErr
		}
		for rows.Next() {
			var clusterID, eventUID string
			var count int64
			if queryErr = rows.Scan(&clusterID, &eventUID, &count); queryErr != nil {
				_ = rows.Close()
				return queryErr
			}
			previousCounts[clusterID+"\x00"+eventUID] = count
		}
		if queryErr = rows.Close(); queryErr != nil {
			return queryErr
		}
	}
	upsert, err := tx.PrepareContext(ctx, `INSERT INTO cluster_events(cluster_id,event_uid,namespace,type,reason,action,message,regarding_api_version,regarding_kind,regarding_namespace,regarding_name,regarding_uid,reporting_controller,reporting_instance,occurrence_count,first_observed_at,last_observed_at,resource_version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE namespace=VALUES(namespace),type=VALUES(type),reason=VALUES(reason),action=VALUES(action),message=VALUES(message),regarding_api_version=VALUES(regarding_api_version),regarding_kind=VALUES(regarding_kind),regarding_namespace=VALUES(regarding_namespace),regarding_name=VALUES(regarding_name),regarding_uid=VALUES(regarding_uid),reporting_controller=VALUES(reporting_controller),reporting_instance=VALUES(reporting_instance),occurrence_count=VALUES(occurrence_count),first_observed_at=LEAST(first_observed_at,VALUES(first_observed_at)),last_observed_at=GREATEST(last_observed_at,VALUES(last_observed_at)),resource_version=VALUES(resource_version)`)
	if err != nil {
		return err
	}
	defer upsert.Close()
	rollup, err := tx.PrepareContext(ctx, `INSERT INTO event_issue_hourly(bucket_start,cluster_id,fingerprint,type,reason,regarding_kind,message_sample,occurrence_count,last_observed_at) VALUES(?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE occurrence_count=occurrence_count+VALUES(occurrence_count),last_observed_at=GREATEST(last_observed_at,VALUES(last_observed_at)),message_sample=VALUES(message_sample)`)
	if err != nil {
		return err
	}
	defer rollup.Close()
	for _, e := range events {
		previous, exists := previousCounts[e.ClusterID+"\x00"+e.EventUID]
		delta := e.OccurrenceCount - previous
		if !exists && delta < 1 {
			delta = 1
		}
		if delta < 0 {
			delta = 0
		}
		_, err = upsert.ExecContext(ctx, e.ClusterID, e.EventUID, e.Namespace, e.Type, e.Reason, e.Action, e.Message, e.RegardingAPIVersion, e.RegardingKind, e.RegardingNamespace, e.RegardingName, e.RegardingUID, e.ReportingController, e.ReportingInstance, e.OccurrenceCount, e.FirstObservedAt.UTC(), e.LastObservedAt.UTC(), e.ResourceVersion)
		if err != nil {
			return err
		}
		if delta > 0 {
			bucket := e.LastObservedAt.UTC().Truncate(time.Hour)
			_, err = rollup.ExecContext(ctx, bucket, e.ClusterID, cluster.EventFingerprint(e), e.Type, e.Reason, e.RegardingKind, e.Message, delta, e.LastObservedAt.UTC())
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
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
		p := "%" + q.Search + "%"
		conditions = append(conditions, "(reason LIKE ? OR message LIKE ? OR regarding_name LIKE ? OR reporting_controller LIKE ?)")
		args = append(args, p, p, p, p)
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
	_, err := store.db.ExecContext(ctx, `INSERT INTO event_collector_checkpoints(cluster_id,state,resource_version,last_success_at,last_error,updated_at) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE state=VALUES(state),resource_version=VALUES(resource_version),last_success_at=COALESCE(VALUES(last_success_at),last_success_at),last_error=VALUES(last_error),updated_at=VALUES(updated_at)`, s.ClusterID, s.State, s.ResourceVersion, s.LastSuccessAt, s.LastError, s.UpdatedAt.UTC())
	return err
}
func (store *MySQLEvents) DeleteEventsByRetention(ctx context.Context, normalCutoff, warningCutoff time.Time, limit int) (int64, error) {
	result, err := store.db.ExecContext(ctx, "DELETE FROM cluster_events WHERE (LOWER(type)='normal' AND last_observed_at<?) OR (LOWER(type)<>'normal' AND last_observed_at<?) LIMIT ?", normalCutoff.UTC(), warningCutoff.UTC(), limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (store *MySQLEvents) DeleteEventRollupsBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	result, err := store.db.ExecContext(ctx, "DELETE FROM event_issue_hourly WHERE bucket_start<? LIMIT ?", cutoff.UTC(), limit)
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
	return tx.Commit()
}
