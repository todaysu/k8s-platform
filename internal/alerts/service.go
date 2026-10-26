package alerts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

type Source interface {
	ListRecords(context.Context) ([]cluster.ClusterRecord, error)
	GetClusterAlerts(context.Context, string) (cluster.AlertSnapshot, error)
}

type Item struct {
	ID              string            `json:"id"`
	ClusterID       string            `json:"cluster_id"`
	Fingerprint     string            `json:"fingerprint"`
	Name            string            `json:"name"`
	Severity        string            `json:"severity"`
	Namespace       string            `json:"namespace"`
	ResourceKind    string            `json:"resource_kind"`
	ResourceName    string            `json:"resource_name"`
	Summary         string            `json:"summary"`
	Description     string            `json:"description"`
	RunbookURL      string            `json:"runbook_url"`
	RuleURL         string            `json:"rule_url"`
	Labels          map[string]string `json:"labels"`
	OwnerKind       string            `json:"owner_kind"`
	OwnerName       string            `json:"owner_name"`
	SourceType      string            `json:"source_type"`
	OccurrenceCount int               `json:"occurrence_count"`
	State           string            `json:"state"`
	Source          string            `json:"source"`
	StartsAt        time.Time         `json:"starts_at"`
	LastSeenAt      time.Time         `json:"last_seen_at"`
	EndsAt          *time.Time        `json:"ends_at,omitempty"`
}

type Coverage struct {
	ClusterID   string     `json:"cluster_id"`
	Status      string     `json:"status"`
	CollectedAt *time.Time `json:"collected_at,omitempty"`
	Message     string     `json:"message,omitempty"`
}

type Query struct {
	View, ClusterID, Severity, Namespace, Search string
	State, SourceType, Sort, Duration            string
	ResourceOnly                                 bool
	Page, PageSize                               int
	Since                                        time.Time
}
type Page struct {
	Items         []Item     `json:"items"`
	Total         int        `json:"total"`
	Coverage      []Coverage `json:"coverage"`
	CollectedAt   time.Time  `json:"collected_at"`
	RetentionDays int        `json:"retention_days"`
	Summary       Summary    `json:"summary"`
}
type Summary struct {
	Critical          int `json:"critical"`
	Warning           int `json:"warning"`
	Info              int `json:"info"`
	AffectedClusters  int `json:"affected_clusters"`
	AffectedResources int `json:"affected_resources"`
}
type Service struct {
	db                      *sql.DB
	source                  Source
	resolvedRetentionDays   int
	transitionRetentionDays int
	hourlyRetentionDays     int
	dailyRetentionDays      int
	coverageRetentionDays   int
	cleanupBatchSize        int
}

// Shard leaders may run several collectors in one process; cap their combined
// remote API work instead of allowing eight requests per shard.
var alertCollectionSlots = make(chan struct{}, 8)

func New(ctx context.Context, db *sql.DB, source Source) (*Service, error) {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS alert_instances(id VARCHAR(64) PRIMARY KEY,cluster_id VARCHAR(64) NOT NULL,fingerprint VARCHAR(128) NOT NULL,name VARCHAR(200) NOT NULL,severity VARCHAR(32) NOT NULL,namespace VARCHAR(200) NOT NULL,resource_kind VARCHAR(32) NOT NULL,resource_name VARCHAR(200) NOT NULL,summary VARCHAR(500) NOT NULL,state VARCHAR(16) NOT NULL,source VARCHAR(255) NOT NULL,starts_at VARCHAR(40) NOT NULL,last_seen_at VARCHAR(40) NOT NULL,ends_at VARCHAR(40) NOT NULL)`,
		`CREATE INDEX idx_alert_instances_state_seen ON alert_instances(state,last_seen_at)`,
		`CREATE INDEX idx_alert_instances_cluster_start ON alert_instances(cluster_id,starts_at)`,
		`CREATE TABLE IF NOT EXISTS alert_collection_status(cluster_id VARCHAR(64) PRIMARY KEY,status VARCHAR(16) NOT NULL,collected_at VARCHAR(40) NOT NULL,message VARCHAR(255) NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS alert_metadata(alert_id VARCHAR(64) PRIMARY KEY,labels_json TEXT NOT NULL,description TEXT NOT NULL,runbook_url VARCHAR(1024) NOT NULL,rule_url VARCHAR(1024) NOT NULL,owner_kind VARCHAR(32) NOT NULL,owner_name VARCHAR(200) NOT NULL,source_type VARCHAR(32) NOT NULL,occurrence_count INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS alert_state_transitions(id VARCHAR(64) PRIMARY KEY,alert_id VARCHAR(64) NOT NULL,cluster_id VARCHAR(64) NOT NULL,state VARCHAR(16) NOT NULL,severity VARCHAR(32) NOT NULL,observed_at VARCHAR(40) NOT NULL)`,
		`CREATE INDEX idx_alert_transitions_cluster_time ON alert_state_transitions(cluster_id,observed_at)`,
		`CREATE TABLE IF NOT EXISTS alert_collection_history(id VARCHAR(64) PRIMARY KEY,cluster_id VARCHAR(64) NOT NULL,bucket_start VARCHAR(40) NOT NULL,status VARCHAR(16) NOT NULL,success_count INTEGER NOT NULL,failure_count INTEGER NOT NULL,message_sample VARCHAR(255) NOT NULL)`,
		`CREATE INDEX idx_alert_collection_history_time ON alert_collection_history(cluster_id,bucket_start)`,
		`CREATE TABLE IF NOT EXISTS alert_rollup_hourly(id VARCHAR(64) PRIMARY KEY,bucket_start VARCHAR(40) NOT NULL,cluster_id VARCHAR(64) NOT NULL,fingerprint VARCHAR(128) NOT NULL,name VARCHAR(200) NOT NULL,severity VARCHAR(32) NOT NULL,source_type VARCHAR(32) NOT NULL,resource_kind VARCHAR(32) NOT NULL,firing_count INTEGER NOT NULL,suppressed_count INTEGER NOT NULL,resolved_count INTEGER NOT NULL,total_duration_seconds INTEGER NOT NULL,max_duration_seconds INTEGER NOT NULL,affected_resources INTEGER NOT NULL,last_observed_at VARCHAR(40) NOT NULL)`,
		`CREATE INDEX idx_alert_rollup_hourly_time ON alert_rollup_hourly(cluster_id,bucket_start)`,
		`CREATE TABLE IF NOT EXISTS alert_rollup_daily(id VARCHAR(64) PRIMARY KEY,bucket_start VARCHAR(40) NOT NULL,cluster_id VARCHAR(64) NOT NULL,fingerprint VARCHAR(128) NOT NULL,name VARCHAR(200) NOT NULL,severity VARCHAR(32) NOT NULL,source_type VARCHAR(32) NOT NULL,resource_kind VARCHAR(32) NOT NULL,firing_count INTEGER NOT NULL,suppressed_count INTEGER NOT NULL,resolved_count INTEGER NOT NULL,total_duration_seconds INTEGER NOT NULL,max_duration_seconds INTEGER NOT NULL,affected_resources INTEGER NOT NULL,last_observed_at VARCHAR(40) NOT NULL)`,
		`CREATE INDEX idx_alert_rollup_daily_time ON alert_rollup_daily(cluster_id,bucket_start)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			// MySQL does not support IF NOT EXISTS for indexes on older deployments.
			if strings.HasPrefix(statement, "CREATE INDEX") && isDuplicateIndex(err) {
				continue
			}
			return nil, err
		}
	}
	return &Service{db: db, source: source, resolvedRetentionDays: 180, transitionRetentionDays: 180, hourlyRetentionDays: 400, dailyRetentionDays: 1095, coverageRetentionDays: 400, cleanupBatchSize: 5000}, nil
}

// ConfigureRetention changes cleanup behavior for resolved alerts. Active and
// suppressed alerts are never deleted by retention cleanup.
func (s *Service) ConfigureRetention(resolvedDays, transitionDays, hourlyDays, dailyDays, coverageDays, cleanupBatchSize int) {
	if resolvedDays >= 1 && resolvedDays <= 3650 {
		s.resolvedRetentionDays = resolvedDays
	}
	if transitionDays >= 1 && transitionDays <= 3650 {
		s.transitionRetentionDays = transitionDays
	}
	if hourlyDays >= 30 && hourlyDays <= 3650 {
		s.hourlyRetentionDays = hourlyDays
	}
	if dailyDays >= 90 && dailyDays <= 3650 {
		s.dailyRetentionDays = dailyDays
	}
	if coverageDays >= 30 && coverageDays <= 3650 {
		s.coverageRetentionDays = coverageDays
	}
	if cleanupBatchSize >= 100 && cleanupBatchSize <= 50000 {
		s.cleanupBatchSize = cleanupBatchSize
	}
}

func isDuplicateIndex(err error) bool {
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "duplicate key name") || strings.Contains(value, "already exists")
}
func stamp(t time.Time) string          { return t.UTC().Format(time.RFC3339Nano) }
func parseStamp(value string) time.Time { t, _ := time.Parse(time.RFC3339Nano, value); return t }

func (s *Service) Run(ctx context.Context) {
	for {
		s.CollectOnce(ctx)
		timer := time.NewTimer(time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Service) CollectOnce(ctx context.Context) {
	if s == nil || s.source == nil {
		return
	}
	records, err := s.source.ListRecords(ctx)
	if err != nil {
		log.Printf("alert collector: list clusters: %v", err)
		return
	}
	businessNamespaces := map[string]struct{}{}
	if provider, ok := s.source.(interface {
		AlertBusinessNamespaces(context.Context) ([]string, error)
	}); ok {
		if values, readErr := provider.AlertBusinessNamespaces(ctx); readErr == nil {
			for _, value := range values {
				businessNamespaces[value] = struct{}{}
			}
		} else {
			log.Printf("alert collector: read namespace classification: %v", readErr)
		}
	}
	if lease, ok := cluster.CollectionLeaseFromContext(ctx); ok {
		filtered := records[:0]
		for _, record := range records {
			if cluster.CollectionShard(record.ID, lease.Count) == lease.Shard {
				filtered = append(filtered, record)
			}
		}
		records = filtered
	}
	jobs := make(chan cluster.ClusterRecord)
	var group sync.WaitGroup
	for worker := 0; worker < 8 && worker < len(records); worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for record := range jobs {
				if ctx.Err() != nil {
					return
				}
				select {
				case alertCollectionSlots <- struct{}{}:
				case <-ctx.Done():
					return
				}
				attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
				snapshot, readErr := s.source.GetClusterAlerts(attempt, record.ID)
				<-alertCollectionSlots
				cancel()
				persistContext, persistCancel := context.WithTimeout(ctx, 5*time.Second)
				if readErr != nil {
					_ = s.saveCoverage(persistContext, record.ID, "error", readErr.Error(), time.Now())
				} else if persistErr := s.saveSnapshot(persistContext, record.ID, snapshot, businessNamespaces); persistErr != nil {
					log.Printf("alert collector: persist %s: %v", record.ID, persistErr)
				}
				persistCancel()
			}
		}()
	}
	for _, record := range records {
		select {
		case <-ctx.Done():
			close(jobs)
			group.Wait()
			return
		case jobs <- record:
		}
	}
	close(jobs)
	group.Wait()
	if ctx.Err() == nil {
		if err := s.pruneResolved(ctx, time.Now().AddDate(0, 0, -s.resolvedRetentionDays), s.cleanupBatchSize); err != nil {
			log.Printf("alert collector: prune resolved alerts: %v", err)
		}
		for _, cleanup := range []struct {
			table, column string
			days          int
		}{{"alert_state_transitions", "observed_at", s.transitionRetentionDays}, {"alert_rollup_hourly", "bucket_start", s.hourlyRetentionDays}, {"alert_rollup_daily", "bucket_start", s.dailyRetentionDays}, {"alert_collection_history", "bucket_start", s.coverageRetentionDays}} {
			if err := s.pruneTable(ctx, cleanup.table, cleanup.column, time.Now().AddDate(0, 0, -cleanup.days), s.cleanupBatchSize); err != nil {
				log.Printf("alert collector: prune %s: %v", cleanup.table, err)
			}
		}
	}
}

func (s *Service) pruneTable(ctx context.Context, table, column string, before time.Time, batchSize int) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM `+table+` WHERE `+column+`<? ORDER BY `+column+` LIMIT ?`, stamp(before), batchSize)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if _, err = s.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) pruneResolved(ctx context.Context, before time.Time, batchSize int) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM alert_instances WHERE state='resolved' AND ends_at<? ORDER BY ends_at LIMIT ?`, stamp(before), batchSize)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = s.db.ExecContext(ctx, `DELETE FROM alert_metadata WHERE alert_id=?`, id); err != nil {
			return err
		}
		if _, err = s.db.ExecContext(ctx, `DELETE FROM alert_instances WHERE id=? AND state='resolved'`, id); err != nil {
			return err
		}
	}
	return nil
}

type activeAlert struct {
	ID, State, Fingerprint, Name, Severity, ResourceKind, ResourceName, SourceType string
	StartsAt                                                                       time.Time
}

func (s *Service) saveSnapshot(ctx context.Context, clusterID string, snapshot cluster.AlertSnapshot, businessNamespaces map[string]struct{}) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	active := map[string]activeAlert{}
	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.state,a.fingerprint,a.name,a.severity,a.resource_kind,a.resource_name,a.starts_at,COALESCE(m.source_type,'platform') FROM alert_instances a LEFT JOIN alert_metadata m ON m.alert_id=a.id WHERE a.cluster_id=? AND a.state!='resolved'`, clusterID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var value activeAlert
		var started string
		if err = rows.Scan(&value.ID, &value.State, &value.Fingerprint, &value.Name, &value.Severity, &value.ResourceKind, &value.ResourceName, &started, &value.SourceType); err != nil {
			rows.Close()
			return err
		}
		value.StartsAt = parseStamp(started)
		active[value.ID] = value
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, alert := range snapshot.Alerts {
		if alert.Fingerprint == "" {
			continue
		}
		start := alert.StartsAt
		if start.IsZero() {
			start = snapshot.CollectedAt
		}
		sum := sha256.Sum256([]byte(clusterID + "\x00" + alert.Fingerprint + "\x00" + stamp(start)))
		id := hex.EncodeToString(sum[:])
		seen[id] = struct{}{}
		state := "firing"
		if alert.State == "suppressed" {
			state = "suppressed"
		}
		name, severity, namespace := alert.Labels["alertname"], alert.Labels["severity"], alert.Labels["namespace"]
		kind, resource := "", ""
		for _, candidate := range []struct{ label, kind string }{{"pod", "Pod"}, {"deployment", "Deployment"}, {"statefulset", "StatefulSet"}, {"daemonset", "DaemonSet"}, {"job", "Job"}, {"node", "Node"}, {"service", "Service"}} {
			if alert.Labels[candidate.label] != "" {
				kind, resource = candidate.kind, alert.Labels[candidate.label]
				break
			}
		}
		if resource == "" && alert.Labels["instance"] != "" {
			kind, resource = "Instance", alert.Labels["instance"]
		}
		ownerKind, ownerName := ownerFromLabels(alert.Labels, kind, resource)
		sourceType := "platform"
		if _, ok := businessNamespaces[namespace]; ok && namespace != "" {
			sourceType = "business"
		}
		labels, _ := json.Marshal(alert.Labels)
		occurrences := 1
		previous, exists := active[id]
		if exists {
			if err = tx.QueryRowContext(ctx, `SELECT occurrence_count FROM alert_metadata WHERE alert_id=?`, id).Scan(&occurrences); errors.Is(err, sql.ErrNoRows) {
				occurrences = 1
			} else if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE alert_instances SET last_seen_at=?,state=?,ends_at='',name=?,severity=?,namespace=?,resource_kind=?,resource_name=?,summary=?,source=? WHERE id=?`, stamp(snapshot.CollectedAt), state, name, severity, namespace, kind, resource, alert.Summary, snapshot.Source, id)
		} else {
			var storedState string
			rowErr := tx.QueryRowContext(ctx, `SELECT state FROM alert_instances WHERE id=?`, id).Scan(&storedState)
			if rowErr == nil {
				if err = tx.QueryRowContext(ctx, `SELECT occurrence_count+1 FROM alert_metadata WHERE alert_id=?`, id).Scan(&occurrences); errors.Is(err, sql.ErrNoRows) {
					occurrences = 2
				} else if err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx, `UPDATE alert_instances SET last_seen_at=?,state=?,ends_at='',name=?,severity=?,namespace=?,resource_kind=?,resource_name=?,summary=?,source=? WHERE id=?`, stamp(snapshot.CollectedAt), state, name, severity, namespace, kind, resource, alert.Summary, snapshot.Source, id)
			} else if errors.Is(rowErr, sql.ErrNoRows) {
				if err = tx.QueryRowContext(ctx, `SELECT COUNT(*)+1 FROM alert_instances WHERE cluster_id=? AND fingerprint=?`, clusterID, alert.Fingerprint).Scan(&occurrences); err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO alert_instances(id,cluster_id,fingerprint,name,severity,namespace,resource_kind,resource_name,summary,state,source,starts_at,last_seen_at,ends_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'')`, id, clusterID, alert.Fingerprint, name, severity, namespace, kind, resource, alert.Summary, state, snapshot.Source, stamp(start), stamp(snapshot.CollectedAt))
			} else {
				return rowErr
			}
		}
		if err != nil {
			return err
		}
		if err = saveMetadata(ctx, tx, id, string(labels), alert.Description, alert.RunbookURL, alert.RuleURL, ownerKind, ownerName, sourceType, occurrences); err != nil {
			return err
		}
		if !exists || previous.State != state {
			value := activeAlert{ID: id, State: state, Fingerprint: alert.Fingerprint, Name: name, Severity: severity, ResourceKind: kind, ResourceName: resource, SourceType: sourceType, StartsAt: start}
			if err = recordTransition(ctx, tx, value, clusterID, state, snapshot.CollectedAt); err != nil {
				return err
			}
		}
	}
	for id, value := range active {
		if _, ok := seen[id]; ok {
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE alert_instances SET state='resolved',ends_at=? WHERE id=? AND state!='resolved'`, stamp(snapshot.CollectedAt), id); err != nil {
			return err
		}
		if err = recordTransition(ctx, tx, value, clusterID, "resolved", snapshot.CollectedAt); err != nil {
			return err
		}
	}
	if err = saveCoverageTx(ctx, tx, clusterID, "ok", "", snapshot.CollectedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func ownerFromLabels(labels map[string]string, kind, name string) (string, string) {
	for _, candidate := range []struct{ label, kind string }{{"deployment", "Deployment"}, {"statefulset", "StatefulSet"}, {"daemonset", "DaemonSet"}, {"job", "Job"}, {"replicaset", "ReplicaSet"}} {
		if value := labels[candidate.label]; value != "" {
			return candidate.kind, value
		}
	}
	return kind, name
}

func saveMetadata(ctx context.Context, tx *sql.Tx, id, labels, description, runbookURL, ruleURL, ownerKind, ownerName, sourceType string, occurrences int) error {
	result, err := tx.ExecContext(ctx, `UPDATE alert_metadata SET labels_json=?,description=?,runbook_url=?,rule_url=?,owner_kind=?,owner_name=?,source_type=?,occurrence_count=? WHERE alert_id=?`, labels, description, runbookURL, ruleURL, ownerKind, ownerName, sourceType, occurrences, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count > 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO alert_metadata(alert_id,labels_json,description,runbook_url,rule_url,owner_kind,owner_name,source_type,occurrence_count) VALUES(?,?,?,?,?,?,?,?,?)`, id, labels, description, runbookURL, ruleURL, ownerKind, ownerName, sourceType, occurrences)
	return err
}

func recordTransition(ctx context.Context, tx *sql.Tx, value activeAlert, clusterID, state string, observedAt time.Time) error {
	raw := sha256.Sum256([]byte(value.ID + "\x00" + state + "\x00" + stamp(observedAt)))
	if _, err := tx.ExecContext(ctx, `INSERT INTO alert_state_transitions(id,alert_id,cluster_id,state,severity,observed_at) VALUES(?,?,?,?,?,?)`, hex.EncodeToString(raw[:]), value.ID, clusterID, state, value.Severity, stamp(observedAt)); err != nil {
		return err
	}
	duration := int64(0)
	if state == "resolved" && !value.StartsAt.IsZero() {
		duration = max(0, int64(observedAt.Sub(value.StartsAt).Seconds()))
	}
	for _, rollup := range []struct {
		table  string
		bucket time.Time
	}{{"alert_rollup_hourly", observedAt.UTC().Truncate(time.Hour)}, {"alert_rollup_daily", time.Date(observedAt.UTC().Year(), observedAt.UTC().Month(), observedAt.UTC().Day(), 0, 0, 0, 0, time.UTC)}} {
		if err := updateRollup(ctx, tx, rollup.table, rollup.bucket, clusterID, value, state, duration, observedAt); err != nil {
			return err
		}
	}
	return nil
}

func updateRollup(ctx context.Context, tx *sql.Tx, table string, bucket time.Time, clusterID string, value activeAlert, state string, duration int64, observedAt time.Time) error {
	firing, suppressed, resolved, affected := 0, 0, 0, 0
	switch state {
	case "firing":
		firing, affected = 1, 1
	case "suppressed":
		suppressed = 1
	case "resolved":
		resolved = 1
	}
	keyRaw := sha256.Sum256([]byte(table + "\x00" + stamp(bucket) + "\x00" + clusterID + "\x00" + value.Fingerprint + "\x00" + value.Name + "\x00" + value.Severity + "\x00" + value.SourceType + "\x00" + value.ResourceKind))
	id := hex.EncodeToString(keyRaw[:])
	query := `UPDATE ` + table + ` SET firing_count=firing_count+?,suppressed_count=suppressed_count+?,resolved_count=resolved_count+?,total_duration_seconds=total_duration_seconds+?,max_duration_seconds=CASE WHEN max_duration_seconds>? THEN max_duration_seconds ELSE ? END,affected_resources=affected_resources+?,last_observed_at=? WHERE id=?`
	result, err := tx.ExecContext(ctx, query, firing, suppressed, resolved, duration, duration, duration, affected, stamp(observedAt), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count > 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO `+table+`(id,bucket_start,cluster_id,fingerprint,name,severity,source_type,resource_kind,firing_count,suppressed_count,resolved_count,total_duration_seconds,max_duration_seconds,affected_resources,last_observed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, stamp(bucket), clusterID, value.Fingerprint, value.Name, value.Severity, value.SourceType, value.ResourceKind, firing, suppressed, resolved, duration, duration, affected, stamp(observedAt))
	return err
}

func (s *Service) saveCoverage(ctx context.Context, id, status, message string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = saveCoverageTx(ctx, tx, id, status, message, at); err != nil {
		return err
	}
	return tx.Commit()
}
func saveCoverageTx(ctx context.Context, tx *sql.Tx, id, status, message string, at time.Time) error {
	if len(message) > 255 {
		message = message[:255]
	}
	result, err := tx.ExecContext(ctx, `UPDATE alert_collection_status SET status=?,collected_at=?,message=? WHERE cluster_id=?`, status, stamp(at), message, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO alert_collection_status(cluster_id,status,collected_at,message) VALUES(?,?,?,?)`, id, status, stamp(at), message)
	}
	if err != nil {
		return err
	}
	bucket := at.UTC().Truncate(time.Hour)
	raw := sha256.Sum256([]byte(id + "\x00" + stamp(bucket) + "\x00" + status))
	historyID := hex.EncodeToString(raw[:])
	success, failure := 0, 0
	if status == "ok" {
		success = 1
	} else {
		failure = 1
	}
	result, err = tx.ExecContext(ctx, `UPDATE alert_collection_history SET success_count=success_count+?,failure_count=failure_count+?,message_sample=? WHERE id=?`, success, failure, message, historyID)
	if err != nil {
		return err
	}
	count, err = result.RowsAffected()
	if err != nil || count > 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO alert_collection_history(id,cluster_id,bucket_start,status,success_count,failure_count,message_sample) VALUES(?,?,?,?,?,?,?)`, historyID, id, stamp(bucket), status, success, failure, message)
	return err
}

func (s *Service) List(ctx context.Context, q Query) (Page, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 || q.PageSize > 100 {
		q.PageSize = 30
	}
	if q.Page > 10000 {
		return Page{}, errors.New("页码超出范围")
	}
	where := []string{}
	args := []any{}
	if q.View != "history" {
		where = append(where, "a.state!='resolved'")
	} else {
		where = append(where, "a.state='resolved'")
	}
	if q.ClusterID != "" {
		where = append(where, "a.cluster_id=?")
		args = append(args, q.ClusterID)
	}
	if q.Severity != "" {
		where = append(where, "a.severity=?")
		args = append(args, q.Severity)
	}
	if q.Namespace != "" {
		where = append(where, "a.namespace=?")
		args = append(args, q.Namespace)
	}
	if q.Search != "" {
		where = append(where, "(a.name LIKE ? ESCAPE '!' OR a.resource_name LIKE ? ESCAPE '!' OR a.summary LIKE ? ESCAPE '!')")
		needle := "%" + escapeLike(q.Search) + "%"
		args = append(args, needle, needle, needle)
	}
	if q.State != "" {
		where = append(where, "a.state=?")
		args = append(args, q.State)
	}
	if q.SourceType != "" {
		where = append(where, "COALESCE(m.source_type,'platform')=?")
		args = append(args, q.SourceType)
	}
	if q.ResourceOnly {
		where = append(where, "a.resource_name!=''")
	}
	if q.View != "history" {
		switch q.Duration {
		case "lt1h":
			where = append(where, "a.starts_at>=?")
			args = append(args, stamp(time.Now().UTC().Add(-time.Hour)))
		case "1h-6h":
			where = append(where, "a.starts_at<? AND a.starts_at>=?")
			args = append(args, stamp(time.Now().UTC().Add(-time.Hour)), stamp(time.Now().UTC().Add(-6*time.Hour)))
		case "6h-24h":
			where = append(where, "a.starts_at<? AND a.starts_at>=?")
			args = append(args, stamp(time.Now().UTC().Add(-6*time.Hour)), stamp(time.Now().UTC().Add(-24*time.Hour)))
		case "gt24h":
			where = append(where, "a.starts_at<?")
			args = append(args, stamp(time.Now().UTC().Add(-24*time.Hour)))
		}
	}
	if q.View == "history" && !q.Since.IsZero() {
		where = append(where, "a.ends_at>=?")
		args = append(args, stamp(q.Since))
	}
	condition := " WHERE " + strings.Join(where, " AND ")
	page := Page{Items: []Item{}, Coverage: []Coverage{}, CollectedAt: time.Now().UTC(), RetentionDays: s.resolvedRetentionDays}
	from := ` FROM alert_instances a LEFT JOIN alert_metadata m ON m.alert_id=a.id`
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from+condition, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN a.severity='critical' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN a.severity='warning' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN a.severity='info' THEN 1 ELSE 0 END),0),COUNT(DISTINCT a.cluster_id)`+from+condition, args...).Scan(&page.Summary.Critical, &page.Summary.Warning, &page.Summary.Info, &page.Summary.AffectedClusters); err != nil {
		return page, err
	}
	resourceCondition := condition + ` AND a.resource_name!=''`
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT a.cluster_id,a.namespace,a.resource_kind,a.resource_name`+from+resourceCondition+` GROUP BY a.cluster_id,a.namespace,a.resource_kind,a.resource_name) affected`, args...).Scan(&page.Summary.AffectedResources); err != nil {
		return page, err
	}
	listArgs := append(append([]any{}, args...), q.PageSize, (q.Page-1)*q.PageSize)
	order := ` ORDER BY CASE WHEN a.state='suppressed' THEN 1 ELSE 0 END, CASE a.severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, a.starts_at DESC,a.id DESC`
	if q.View == "history" {
		order = ` ORDER BY a.ends_at DESC,a.id DESC`
	}
	switch q.Sort {
	case "duration":
		order = ` ORDER BY a.starts_at ASC,a.id DESC`
	case "recent":
		order = ` ORDER BY a.last_seen_at DESC,a.id DESC`
	case "recurrence":
		order = ` ORDER BY COALESCE(m.occurrence_count,1) DESC,a.last_seen_at DESC,a.id DESC`
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,a.cluster_id,a.fingerprint,a.name,a.severity,a.namespace,a.resource_kind,a.resource_name,a.summary,a.state,a.source,a.starts_at,a.last_seen_at,a.ends_at,COALESCE(m.labels_json,'{}'),COALESCE(m.description,''),COALESCE(m.runbook_url,''),COALESCE(m.rule_url,''),COALESCE(m.owner_kind,''),COALESCE(m.owner_name,''),COALESCE(m.source_type,'platform'),COALESCE(m.occurrence_count,1)`+from+condition+order+` LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var item Item
		var start, last, end string
		var labels string
		if err = rows.Scan(&item.ID, &item.ClusterID, &item.Fingerprint, &item.Name, &item.Severity, &item.Namespace, &item.ResourceKind, &item.ResourceName, &item.Summary, &item.State, &item.Source, &start, &last, &end, &labels, &item.Description, &item.RunbookURL, &item.RuleURL, &item.OwnerKind, &item.OwnerName, &item.SourceType, &item.OccurrenceCount); err != nil {
			rows.Close()
			return page, err
		}
		item.StartsAt, item.LastSeenAt = parseStamp(start), parseStamp(last)
		_ = json.Unmarshal([]byte(labels), &item.Labels)
		if end != "" {
			value := parseStamp(end)
			item.EndsAt = &value
		}
		page.Items = append(page.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	coverageRows, err := s.db.QueryContext(ctx, `SELECT cluster_id,status,collected_at,message FROM alert_collection_status`)
	if err != nil {
		return page, err
	}
	for coverageRows.Next() {
		var c Coverage
		var at string
		if err = coverageRows.Scan(&c.ClusterID, &c.Status, &at, &c.Message); err != nil {
			coverageRows.Close()
			return page, err
		}
		value := parseStamp(at)
		c.CollectedAt = &value
		page.Coverage = append(page.Coverage, c)
	}
	err = coverageRows.Err()
	coverageRows.Close()
	return page, err
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	return strings.ReplaceAll(value, "_", "!_")
}
