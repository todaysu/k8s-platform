package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func (store *SQLite) migrateSecurityBaselines(ctx context.Context) error {
	_, err := store.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS security_baseline_results (
cluster_id TEXT PRIMARY KEY,status TEXT NOT NULL,scanner TEXT NOT NULL,framework TEXT NOT NULL,
started_at TEXT NOT NULL,finished_at TEXT,report_json TEXT NOT NULL,error_message TEXT NOT NULL DEFAULT '',
FOREIGN KEY(cluster_id) REFERENCES clusters(id) ON DELETE CASCADE)`)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	_, err = store.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS security_baseline_whitelist (
cluster_id TEXT NOT NULL,control_id TEXT NOT NULL,reason TEXT NOT NULL,created_at TEXT NOT NULL,
PRIMARY KEY(cluster_id,control_id),FOREIGN KEY(cluster_id) REFERENCES clusters(id) ON DELETE CASCADE)`)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	_, err = store.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS security_baseline_history (
id INTEGER PRIMARY KEY AUTOINCREMENT,cluster_id TEXT NOT NULL,status TEXT NOT NULL,scanner TEXT NOT NULL,framework TEXT NOT NULL,
started_at TEXT NOT NULL,finished_at TEXT,report_json TEXT NOT NULL,error_message TEXT NOT NULL DEFAULT '',
UNIQUE(cluster_id,started_at),FOREIGN KEY(cluster_id) REFERENCES clusters(id) ON DELETE CASCADE)`)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	_, err = store.db.ExecContext(ctx, `INSERT OR IGNORE INTO security_baseline_history(cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message)
SELECT cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message FROM security_baseline_results WHERE status IN ('completed','failed')`)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return migrateSecurityJobs(ctx, store.db, true)
}

func marshalSecurityReport(value cluster.SecurityBaselineReport) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func scanSecurityRecord(scanner sqlScanner, sqlite bool) (cluster.SecurityBaselineRecord, error) {
	var value cluster.SecurityBaselineRecord
	var report string
	if sqlite {
		var startedText string
		var finishedText sql.NullString
		err := scanner.Scan(&value.ClusterID, &value.Status, &value.Scanner, &value.Framework, &startedText, &finishedText, &report, &value.Error)
		if err != nil {
			return value, err
		}
		value.StartedAt, err = time.Parse(timeLayout, startedText)
		if err != nil {
			return value, err
		}
		if finishedText.Valid {
			parsed, parseErr := time.Parse(timeLayout, finishedText.String)
			if parseErr != nil {
				return value, parseErr
			}
			value.FinishedAt = &parsed
		}
	} else {
		var startedAt time.Time
		var finishedAt sql.NullTime
		err := scanner.Scan(&value.ClusterID, &value.Status, &value.Scanner, &value.Framework, &startedAt, &finishedAt, &report, &value.Error)
		if err != nil {
			return value, err
		}
		value.StartedAt = startedAt.UTC()
		if finishedAt.Valid {
			parsed := finishedAt.Time.UTC()
			value.FinishedAt = &parsed
		}
	}
	if report != "" && json.Unmarshal([]byte(report), &value.Report) != nil {
		return value, errors.New("invalid security report")
	}
	return value, nil
}

func (store *SQLite) SaveSecurityBaseline(ctx context.Context, value cluster.SecurityBaselineRecord) error {
	var finished any
	if value.FinishedAt != nil {
		finished = value.FinishedAt.UTC().Format(timeLayout)
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO security_baseline_results(cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(cluster_id) DO UPDATE SET status=excluded.status,scanner=excluded.scanner,framework=excluded.framework,started_at=excluded.started_at,finished_at=excluded.finished_at,report_json=excluded.report_json,error_message=excluded.error_message`, value.ClusterID, value.Status, value.Scanner, value.Framework, value.StartedAt.UTC().Format(timeLayout), finished, marshalSecurityReport(value.Report), value.Error)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}

func (store *SQLite) GetSecurityBaseline(ctx context.Context, id string) (*cluster.SecurityBaselineRecord, error) {
	value, err := scanSecurityRecord(store.db.QueryRowContext(ctx, `SELECT cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message FROM security_baseline_results WHERE cluster_id=?`, id), true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return &value, nil
}

func (store *SQLite) ListSecurityBaselines(ctx context.Context) (map[string]cluster.SecurityBaselineRecord, error) {
	return listSecurityRecords(ctx, store.db, true)
}

func (store *SQLite) AppendSecurityBaselineHistory(ctx context.Context, value cluster.SecurityBaselineRecord) error {
	var finished any
	if value.FinishedAt != nil {
		finished = value.FinishedAt.UTC().Format(timeLayout)
	}
	_, err := store.db.ExecContext(ctx, `INSERT OR IGNORE INTO security_baseline_history(cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message) VALUES(?,?,?,?,?,?,?,?)`, value.ClusterID, value.Status, value.Scanner, value.Framework, value.StartedAt.UTC().Format(timeLayout), finished, marshalSecurityReport(value.Report), value.Error)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}

func (store *SQLite) ListSecurityBaselineHistory(ctx context.Context, id string, limit int) ([]cluster.SecurityBaselineRecord, error) {
	return listSecurityHistory(ctx, store.db, true, id, limit)
}

func (store *MySQL) SaveSecurityBaseline(ctx context.Context, value cluster.SecurityBaselineRecord) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO security_baseline_results(cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE status=VALUES(status),scanner=VALUES(scanner),framework=VALUES(framework),started_at=VALUES(started_at),finished_at=VALUES(finished_at),report_json=VALUES(report_json),error_message=VALUES(error_message)`, value.ClusterID, value.Status, value.Scanner, value.Framework, value.StartedAt.UTC(), value.FinishedAt, marshalSecurityReport(value.Report), value.Error)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}

func (store *MySQL) GetSecurityBaseline(ctx context.Context, id string) (*cluster.SecurityBaselineRecord, error) {
	value, err := scanSecurityRecord(store.db.QueryRowContext(ctx, `SELECT cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message FROM security_baseline_results WHERE cluster_id=?`, id), false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return &value, nil
}

func (store *MySQL) ListSecurityBaselines(ctx context.Context) (map[string]cluster.SecurityBaselineRecord, error) {
	return listSecurityRecords(ctx, store.db, false)
}

func (store *MySQL) AppendSecurityBaselineHistory(ctx context.Context, value cluster.SecurityBaselineRecord) error {
	_, err := store.db.ExecContext(ctx, `INSERT IGNORE INTO security_baseline_history(cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message) VALUES(?,?,?,?,?,?,?,?)`, value.ClusterID, value.Status, value.Scanner, value.Framework, value.StartedAt.UTC(), value.FinishedAt, marshalSecurityReport(value.Report), value.Error)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}

func (store *MySQL) ListSecurityBaselineHistory(ctx context.Context, id string, limit int) ([]cluster.SecurityBaselineRecord, error) {
	return listSecurityHistory(ctx, store.db, false, id, limit)
}

func listSecurityRecords(ctx context.Context, db *sql.DB, sqlite bool) (map[string]cluster.SecurityBaselineRecord, error) {
	rows, err := db.QueryContext(ctx, `SELECT cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message FROM security_baseline_results`)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	values := map[string]cluster.SecurityBaselineRecord{}
	for rows.Next() {
		value, scanErr := scanSecurityRecord(rows, sqlite)
		if scanErr != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		values[value.ClusterID] = value
	}
	return values, rows.Err()
}

func listSecurityHistory(ctx context.Context, db *sql.DB, sqlite bool, clusterID string, limit int) ([]cluster.SecurityBaselineRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := db.QueryContext(ctx, `SELECT id,cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message FROM security_baseline_history WHERE cluster_id=? ORDER BY started_at DESC LIMIT ?`, clusterID, limit)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	values := make([]cluster.SecurityBaselineRecord, 0)
	for rows.Next() {
		var id int64
		var clusterIDValue, status, scannerName, framework, report, errorMessage string
		if sqlite {
			var startedText string
			var finishedText sql.NullString
			if err = rows.Scan(&id, &clusterIDValue, &status, &scannerName, &framework, &startedText, &finishedText, &report, &errorMessage); err != nil {
				return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
			}
			value := cluster.SecurityBaselineRecord{ID: id, ClusterID: clusterIDValue, Status: status, Scanner: scannerName, Framework: framework, Error: errorMessage}
			value.StartedAt, err = time.Parse(timeLayout, startedText)
			if err != nil {
				return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
			}
			if finishedText.Valid {
				finished, parseErr := time.Parse(timeLayout, finishedText.String)
				if parseErr != nil {
					return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
				}
				value.FinishedAt = &finished
			}
			if report != "" && json.Unmarshal([]byte(report), &value.Report) != nil {
				return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
			}
			values = append(values, value)
			continue
		}
		var startedAt time.Time
		var finishedAt sql.NullTime
		if err = rows.Scan(&id, &clusterIDValue, &status, &scannerName, &framework, &startedAt, &finishedAt, &report, &errorMessage); err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		value := cluster.SecurityBaselineRecord{ID: id, ClusterID: clusterIDValue, Status: status, Scanner: scannerName, Framework: framework, StartedAt: startedAt.UTC(), Error: errorMessage}
		if finishedAt.Valid {
			finished := finishedAt.Time.UTC()
			value.FinishedAt = &finished
		}
		if report != "" && json.Unmarshal([]byte(report), &value.Report) != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (store *SQLite) ListSecurityWhitelist(ctx context.Context, clusterID string) ([]cluster.SecurityWhitelistEntry, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT cluster_id,control_id,reason,created_at FROM security_baseline_whitelist WHERE cluster_id=? ORDER BY created_at,control_id`, clusterID)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	values := []cluster.SecurityWhitelistEntry{}
	for rows.Next() {
		var value cluster.SecurityWhitelistEntry
		var created string
		if err = rows.Scan(&value.ClusterID, &value.ControlID, &value.Reason, &created); err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		value.CreatedAt, _ = time.Parse(timeLayout, created)
		values = append(values, value)
	}
	return values, rows.Err()
}

func (store *SQLite) AddSecurityWhitelist(ctx context.Context, value cluster.SecurityWhitelistEntry) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO security_baseline_whitelist(cluster_id,control_id,reason,created_at) VALUES(?,?,?,?) ON CONFLICT(cluster_id,control_id) DO UPDATE SET reason=excluded.reason,created_at=excluded.created_at`, value.ClusterID, value.ControlID, value.Reason, value.CreatedAt.UTC().Format(timeLayout))
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}

func (store *SQLite) DeleteSecurityWhitelist(ctx context.Context, clusterID, controlID string) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM security_baseline_whitelist WHERE cluster_id=? AND control_id=?`, clusterID, controlID)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}

func (store *MySQL) ListSecurityWhitelist(ctx context.Context, clusterID string) ([]cluster.SecurityWhitelistEntry, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT cluster_id,control_id,reason,created_at FROM security_baseline_whitelist WHERE cluster_id=? ORDER BY created_at,control_id`, clusterID)
	if err != nil {
		return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer rows.Close()
	values := []cluster.SecurityWhitelistEntry{}
	for rows.Next() {
		var value cluster.SecurityWhitelistEntry
		if err = rows.Scan(&value.ClusterID, &value.ControlID, &value.Reason, &value.CreatedAt); err != nil {
			return nil, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		value.CreatedAt = value.CreatedAt.UTC()
		values = append(values, value)
	}
	return values, rows.Err()
}

func (store *MySQL) AddSecurityWhitelist(ctx context.Context, value cluster.SecurityWhitelistEntry) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO security_baseline_whitelist(cluster_id,control_id,reason,created_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE reason=VALUES(reason),created_at=VALUES(created_at)`, value.ClusterID, value.ControlID, value.Reason, value.CreatedAt.UTC())
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}

func (store *MySQL) DeleteSecurityWhitelist(ctx context.Context, clusterID, controlID string) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM security_baseline_whitelist WHERE cluster_id=? AND control_id=?`, clusterID, controlID)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}
