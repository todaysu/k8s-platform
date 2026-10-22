package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

const securityQueueLimit = 100

type securityJobStore struct {
	db     *sql.DB
	sqlite bool
}

func migrateSecurityJobs(ctx context.Context, db *sql.DB, sqlite bool) error {
	control := `CREATE TABLE IF NOT EXISTS platform_job_control(id INTEGER PRIMARY KEY,version BIGINT NOT NULL)`
	table := `CREATE TABLE IF NOT EXISTS security_jobs(cluster_id VARCHAR(64) PRIMARY KEY,token VARCHAR(64) NOT NULL,owner VARCHAR(64) NOT NULL,state VARCHAR(16) NOT NULL,payload TEXT NOT NULL,lease_until BIGINT NOT NULL,queued_at BIGINT NOT NULL,FOREIGN KEY(cluster_id) REFERENCES clusters(id) ON DELETE CASCADE)`
	insert := `INSERT OR IGNORE INTO platform_job_control(id,version) VALUES(1,0)`
	if !sqlite {
		control += ` ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
		table = `CREATE TABLE IF NOT EXISTS security_jobs(cluster_id VARCHAR(64) PRIMARY KEY,token VARCHAR(64) NOT NULL,owner VARCHAR(64) NOT NULL,state VARCHAR(16) NOT NULL,payload MEDIUMTEXT NOT NULL,lease_until BIGINT NOT NULL,queued_at BIGINT NOT NULL,KEY idx_security_jobs_state(state,queued_at),CONSTRAINT fk_security_jobs_cluster FOREIGN KEY(cluster_id) REFERENCES clusters(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
		insert = `INSERT IGNORE INTO platform_job_control(id,version) VALUES(1,0)`
	}
	for _, stmt := range []string{control, table, insert} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	for _, id := range []int{2, 3} {
		stmt := `INSERT OR IGNORE INTO platform_job_control(id,version) VALUES(?,0)`
		if !sqlite {
			stmt = `INSERT IGNORE INTO platform_job_control(id,version) VALUES(?,0)`
		}
		if _, err := db.ExecContext(ctx, stmt, id); err != nil {
			return err
		}
	}
	if sqlite {
		if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_security_jobs_state ON security_jobs(state,queued_at)`); err != nil {
			return err
		}
	}
	if err := migrateRequestQuotas(ctx, db, sqlite); err != nil {
		return err
	}
	return migrateCollectionShards(ctx, db, sqlite)
}

// The control row bounds admission across replicas and serializes short claims.
// No remote Kubernetes work takes place while this transaction holds a lock.
func (s securityJobStore) transaction(ctx context.Context, work func(*sql.Tx, int64) error) error {
	return s.transactionWithLock(ctx, 1, work)
}

func (s securityJobStore) transactionWithLock(ctx context.Context, lockID int, work func(*sql.Tx, int64) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE platform_job_control SET version=version+1 WHERE id=?`, lockID); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	clock := `SELECT CAST((julianday('now')-2440587.5)*86400000 AS INTEGER)`
	if !s.sqlite {
		clock = `SELECT CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(3))*1000 AS SIGNED)`
	}
	var now int64
	if err = tx.QueryRowContext(ctx, clock).Scan(&now); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	if err = work(tx, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}
func jobToken() (string, error) {
	var data [24]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}
func (s securityJobStore) enqueue(ctx context.Context, value cluster.SecurityBaselineRecord) error {
	token, err := jobToken()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx, now int64) error {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM security_jobs WHERE cluster_id=?`, value.ClusterID).Scan(&state)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if state == "queued" || state == "running" {
			return &cluster.Error{Code: cluster.CodeSecurityScanInProgress}
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_jobs WHERE state IN ('queued','running')`).Scan(&count); err != nil {
			return err
		}
		if count >= securityQueueLimit {
			return &cluster.Error{Code: cluster.CodeSecurityScanQueueFull}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM security_jobs WHERE cluster_id=?`, value.ClusterID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO security_jobs(cluster_id,token,owner,state,payload,lease_until,queued_at) VALUES(?,?,'','queued',?,0,?)`, value.ClusterID, token, string(payload), now); err != nil {
			return err
		}
		return s.writeRecord(ctx, tx, value)
	})
}
func (s securityJobStore) claim(ctx context.Context, owner string, limit int, lease time.Duration) (*cluster.SecurityJob, error) {
	if owner == "" || len(owner) > 64 || limit <= 0 || lease < 10*time.Second || lease > 5*time.Minute {
		return nil, errors.New("invalid security job claim")
	}
	var job *cluster.SecurityJob
	err := s.transaction(ctx, func(tx *sql.Tx, now int64) error {
		var running int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_jobs WHERE state='running'`).Scan(&running); err != nil {
			return err
		}
		if running >= limit {
			return nil
		}
		var id, token, payload string
		err := tx.QueryRowContext(ctx, `SELECT cluster_id,token,payload FROM security_jobs WHERE state='queued' ORDER BY queued_at,cluster_id LIMIT 1`).Scan(&id, &token, &payload)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		job = &cluster.SecurityJob{Token: token, Owner: owner}
		if err = json.Unmarshal([]byte(payload), &job.Record); err != nil {
			return err
		}
		if job.Record.ClusterID != id {
			return errors.New("invalid security job payload")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE security_jobs SET owner=?,state='running',lease_until=? WHERE cluster_id=? AND token=?`, owner, now+lease.Milliseconds(), id, token); err != nil {
			return err
		}
		job.Record.Status = cluster.SecurityScanStatusRunning
		return s.writeRecord(ctx, tx, job.Record)
	})
	return job, err
}
func verifyJob(ctx context.Context, tx *sql.Tx, job cluster.SecurityJob, now int64) error {
	var token, owner, state string
	var until int64
	err := tx.QueryRowContext(ctx, `SELECT token,owner,state,lease_until FROM security_jobs WHERE cluster_id=?`, job.Record.ClusterID).Scan(&token, &owner, &state, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return cluster.ErrSecurityJobLeaseLost
	}
	if err != nil {
		return err
	}
	if token != job.Token || owner != job.Owner || state != "running" || until <= now {
		return cluster.ErrSecurityJobLeaseLost
	}
	return nil
}
func (s securityJobStore) renew(ctx context.Context, job cluster.SecurityJob, lease time.Duration) error {
	if lease < 10*time.Second || lease > 5*time.Minute {
		return errors.New("invalid security job lease")
	}
	return s.transaction(ctx, func(tx *sql.Tx, now int64) error {
		if err := verifyJob(ctx, tx, job, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE security_jobs SET lease_until=? WHERE cluster_id=?`, now+lease.Milliseconds(), job.Record.ClusterID)
		return err
	})
}
func (s securityJobStore) save(ctx context.Context, job cluster.SecurityJob, value cluster.SecurityBaselineRecord) error {
	if value.ClusterID != job.Record.ClusterID || !value.StartedAt.Equal(job.Record.StartedAt) {
		return cluster.ErrSecurityJobLeaseLost
	}
	return s.transaction(ctx, func(tx *sql.Tx, now int64) error {
		if err := verifyJob(ctx, tx, job, now); err != nil {
			return err
		}
		if err := s.writeRecord(ctx, tx, value); err != nil {
			return err
		}
		if value.Status == cluster.SecurityScanStatusCompleted || value.Status == cluster.SecurityScanStatusFailed {
			_, err := tx.ExecContext(ctx, `UPDATE security_jobs SET state=?,lease_until=0 WHERE cluster_id=?`, value.Status, value.ClusterID)
			return err
		}
		return nil
	})
}
func (s securityJobStore) expire(ctx context.Context) error {
	return s.transaction(ctx, func(tx *sql.Tx, now int64) error {
		rows, err := tx.QueryContext(ctx, `SELECT payload FROM security_jobs WHERE state='running' AND lease_until<=? LIMIT 100`, now)
		if err != nil {
			return err
		}
		var records []cluster.SecurityBaselineRecord
		for rows.Next() {
			var payload string
			if err = rows.Scan(&payload); err != nil {
				rows.Close()
				return err
			}
			var value cluster.SecurityBaselineRecord
			if err = json.Unmarshal([]byte(payload), &value); err != nil {
				rows.Close()
				return err
			}
			records = append(records, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, value := range records {
			value.Status = cluster.SecurityScanStatusFailed
			finished := time.UnixMilli(now).UTC()
			value.FinishedAt = &finished
			value.Error = "Worker 租约已过期，扫描中断；请确认临时资源状态后重新提交。"
			value.Report.Progress.Phase = "failed"
			if err = s.writeRecord(ctx, tx, value); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE security_jobs SET state='failed',lease_until=0 WHERE cluster_id=?`, value.ClusterID); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s securityJobStore) writeRecord(ctx context.Context, tx *sql.Tx, value cluster.SecurityBaselineRecord) error {
	var started any = value.StartedAt.UTC()
	var finished any = value.FinishedAt
	if s.sqlite {
		started = value.StartedAt.UTC().Format(timeLayout)
		finished = nil
		if value.FinishedAt != nil {
			finished = value.FinishedAt.UTC().Format(timeLayout)
		}
	}
	args := []any{value.ClusterID, value.Status, value.Scanner, value.Framework, started, finished, marshalSecurityReport(value.Report), value.Error}
	stmt := `INSERT INTO security_baseline_results(cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(cluster_id) DO UPDATE SET status=excluded.status,scanner=excluded.scanner,framework=excluded.framework,started_at=excluded.started_at,finished_at=excluded.finished_at,report_json=excluded.report_json,error_message=excluded.error_message`
	if !s.sqlite {
		stmt = `INSERT INTO security_baseline_results(cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE status=VALUES(status),scanner=VALUES(scanner),framework=VALUES(framework),started_at=VALUES(started_at),finished_at=VALUES(finished_at),report_json=VALUES(report_json),error_message=VALUES(error_message)`
	}
	if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
		return err
	}
	if value.Status == cluster.SecurityScanStatusCompleted || value.Status == cluster.SecurityScanStatusFailed {
		stmt = `INSERT OR IGNORE INTO security_baseline_history(cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message) VALUES(?,?,?,?,?,?,?,?)`
		if !s.sqlite {
			stmt = `INSERT IGNORE INTO security_baseline_history(cluster_id,status,scanner,framework,started_at,finished_at,report_json,error_message) VALUES(?,?,?,?,?,?,?,?)`
		}
		_, err := tx.ExecContext(ctx, stmt, args...)
		return err
	}
	return nil
}

func (store *SQLite) EnqueueSecurityJob(ctx context.Context, value cluster.SecurityBaselineRecord) error {
	return (securityJobStore{db: store.db, sqlite: true}).enqueue(ctx, value)
}

func (store *SQLite) ClaimSecurityJob(ctx context.Context, owner string, limit int, lease time.Duration) (*cluster.SecurityJob, error) {
	return (securityJobStore{db: store.db, sqlite: true}).claim(ctx, owner, limit, lease)
}

func (store *SQLite) RenewSecurityJob(ctx context.Context, job cluster.SecurityJob, lease time.Duration) error {
	return (securityJobStore{db: store.db, sqlite: true}).renew(ctx, job, lease)
}

func (store *SQLite) SaveSecurityJobRecord(ctx context.Context, job cluster.SecurityJob, value cluster.SecurityBaselineRecord) error {
	return (securityJobStore{db: store.db, sqlite: true}).save(ctx, job, value)
}

func (store *SQLite) ExpireSecurityJobs(ctx context.Context) error {
	return (securityJobStore{db: store.db, sqlite: true}).expire(ctx)
}

func (store *MySQL) EnqueueSecurityJob(ctx context.Context, value cluster.SecurityBaselineRecord) error {
	return (securityJobStore{db: store.db, sqlite: false}).enqueue(ctx, value)
}

func (store *MySQL) ClaimSecurityJob(ctx context.Context, owner string, limit int, lease time.Duration) (*cluster.SecurityJob, error) {
	return (securityJobStore{db: store.db, sqlite: false}).claim(ctx, owner, limit, lease)
}

func (store *MySQL) RenewSecurityJob(ctx context.Context, job cluster.SecurityJob, lease time.Duration) error {
	return (securityJobStore{db: store.db, sqlite: false}).renew(ctx, job, lease)
}

func (store *MySQL) SaveSecurityJobRecord(ctx context.Context, job cluster.SecurityJob, value cluster.SecurityBaselineRecord) error {
	return (securityJobStore{db: store.db, sqlite: false}).save(ctx, job, value)
}

func (store *MySQL) ExpireSecurityJobs(ctx context.Context) error {
	return (securityJobStore{db: store.db, sqlite: false}).expire(ctx)
}
