package alerts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	ID           string     `json:"id"`
	ClusterID    string     `json:"cluster_id"`
	Fingerprint  string     `json:"fingerprint"`
	Name         string     `json:"name"`
	Severity     string     `json:"severity"`
	Namespace    string     `json:"namespace"`
	ResourceKind string     `json:"resource_kind"`
	ResourceName string     `json:"resource_name"`
	Summary      string     `json:"summary"`
	State        string     `json:"state"`
	Source       string     `json:"source"`
	StartsAt     time.Time  `json:"starts_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	EndsAt       *time.Time `json:"ends_at,omitempty"`
}

type Coverage struct {
	ClusterID   string     `json:"cluster_id"`
	Status      string     `json:"status"`
	CollectedAt *time.Time `json:"collected_at,omitempty"`
	Message     string     `json:"message,omitempty"`
}

type Query struct {
	View, ClusterID, Severity, Namespace, Search string
	Page, PageSize                               int
	Since                                        time.Time
}
type Page struct {
	Items         []Item     `json:"items"`
	Total         int        `json:"total"`
	Coverage      []Coverage `json:"coverage"`
	CollectedAt   time.Time  `json:"collected_at"`
	RetentionDays int        `json:"retention_days"`
}
type Service struct {
	db                    *sql.DB
	source                Source
	resolvedRetentionDays int
	cleanupBatchSize      int
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
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			// MySQL does not support IF NOT EXISTS for indexes on older deployments.
			if strings.HasPrefix(statement, "CREATE INDEX") && isDuplicateIndex(err) {
				continue
			}
			return nil, err
		}
	}
	return &Service{db: db, source: source, resolvedRetentionDays: 180, cleanupBatchSize: 5000}, nil
}

// ConfigureRetention changes cleanup behavior for resolved alerts. Active and
// suppressed alerts are never deleted by retention cleanup.
func (s *Service) ConfigureRetention(resolvedDays, cleanupBatchSize int) {
	if resolvedDays >= 1 && resolvedDays <= 3650 {
		s.resolvedRetentionDays = resolvedDays
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
				} else if persistErr := s.saveSnapshot(persistContext, record.ID, snapshot); persistErr != nil {
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
	}
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
		if _, err = s.db.ExecContext(ctx, `DELETE FROM alert_instances WHERE id=? AND state='resolved'`, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) saveSnapshot(ctx context.Context, clusterID string, snapshot cluster.AlertSnapshot) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
		for _, candidate := range []struct{ label, kind string }{{"pod", "Pod"}, {"deployment", "Deployment"}, {"node", "Node"}} {
			if alert.Labels[candidate.label] != "" {
				kind, resource = candidate.kind, alert.Labels[candidate.label]
				break
			}
		}
		if resource == "" && alert.Labels["instance"] != "" {
			kind, resource = "Instance", alert.Labels["instance"]
		}
		updated, err := tx.ExecContext(ctx, `UPDATE alert_instances SET last_seen_at=?,state=?,ends_at='',summary=?,source=? WHERE id=?`, stamp(snapshot.CollectedAt), state, alert.Summary, snapshot.Source, id)
		if err != nil {
			return err
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			_, err = tx.ExecContext(ctx, `INSERT INTO alert_instances(id,cluster_id,fingerprint,name,severity,namespace,resource_kind,resource_name,summary,state,source,starts_at,last_seen_at,ends_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'')`, id, clusterID, alert.Fingerprint, name, severity, namespace, kind, resource, alert.Summary, state, snapshot.Source, stamp(start), stamp(snapshot.CollectedAt))
			if err != nil {
				return err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM alert_instances WHERE cluster_id=? AND state!='resolved'`, clusterID)
	if err != nil {
		return err
	}
	var resolve []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if _, ok := seen[id]; !ok {
			resolve = append(resolve, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range resolve {
		if _, err = tx.ExecContext(ctx, `UPDATE alert_instances SET state='resolved',ends_at=? WHERE id=? AND state!='resolved'`, stamp(snapshot.CollectedAt), id); err != nil {
			return err
		}
	}
	if err = saveCoverageTx(ctx, tx, clusterID, "ok", "", snapshot.CollectedAt); err != nil {
		return err
	}
	return tx.Commit()
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
		where = append(where, "state!='resolved'")
	} else {
		where = append(where, "state='resolved'")
	}
	if q.ClusterID != "" {
		where = append(where, "cluster_id=?")
		args = append(args, q.ClusterID)
	}
	if q.Severity != "" {
		where = append(where, "severity=?")
		args = append(args, q.Severity)
	}
	if q.Namespace != "" {
		where = append(where, "namespace=?")
		args = append(args, q.Namespace)
	}
	if q.Search != "" {
		where = append(where, "(name LIKE ? ESCAPE '!' OR resource_name LIKE ? ESCAPE '!' OR summary LIKE ? ESCAPE '!')")
		needle := "%" + escapeLike(q.Search) + "%"
		args = append(args, needle, needle, needle)
	}
	if !q.Since.IsZero() {
		column := "starts_at"
		if q.View == "history" {
			column = "ends_at"
		}
		where = append(where, column+">=?")
		args = append(args, stamp(q.Since))
	}
	condition := " WHERE " + strings.Join(where, " AND ")
	page := Page{Items: []Item{}, Coverage: []Coverage{}, CollectedAt: time.Now().UTC(), RetentionDays: s.resolvedRetentionDays}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_instances`+condition, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	listArgs := append(append([]any{}, args...), q.PageSize, (q.Page-1)*q.PageSize)
	order := ` ORDER BY CASE WHEN state='suppressed' THEN 1 ELSE 0 END, CASE severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, starts_at DESC,id DESC`
	if q.View == "history" {
		order = ` ORDER BY ends_at DESC,id DESC`
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,cluster_id,fingerprint,name,severity,namespace,resource_kind,resource_name,summary,state,source,starts_at,last_seen_at,ends_at FROM alert_instances`+condition+order+` LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var item Item
		var start, last, end string
		if err = rows.Scan(&item.ID, &item.ClusterID, &item.Fingerprint, &item.Name, &item.Severity, &item.Namespace, &item.ResourceKind, &item.ResourceName, &item.Summary, &item.State, &item.Source, &start, &last, &end); err != nil {
			rows.Close()
			return page, err
		}
		item.StartsAt, item.LastSeenAt = parseStamp(start), parseStamp(last)
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
