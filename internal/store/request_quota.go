package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func migrateRequestQuotas(ctx context.Context, db *sql.DB, sqlite bool) error {
	stmt := `CREATE TABLE IF NOT EXISTS platform_request_quotas(token VARCHAR(64) PRIMARY KEY,lane VARCHAR(32) NOT NULL,cluster_id VARCHAR(64) NOT NULL,lease_until BIGINT NOT NULL)`
	if !sqlite {
		stmt = `CREATE TABLE IF NOT EXISTS platform_request_quotas(token VARCHAR(64) PRIMARY KEY,lane VARCHAR(32) NOT NULL,cluster_id VARCHAR(64) NOT NULL,lease_until BIGINT NOT NULL,KEY idx_request_quota_lane(lane,cluster_id),KEY idx_request_quota_expiry(lease_until)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
	}
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return err
	}
	if sqlite {
		for _, stmt = range []string{`CREATE INDEX IF NOT EXISTS idx_request_quota_lane ON platform_request_quotas(lane,cluster_id)`, `CREATE INDEX IF NOT EXISTS idx_request_quota_expiry ON platform_request_quotas(lease_until)`} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
	}
	return nil
}
func validQuotaLease(lease time.Duration) bool {
	return lease >= 10*time.Second && lease <= time.Minute
}
func (s securityJobStore) acquireQuota(ctx context.Context, lane, id string, total, perCluster int, lease time.Duration) (string, error) {
	if (lane != "realtime" && lane != "monitoring" && lane != "session" && lane != "collection_health" && lane != "collection_list") || total < 1 || total > 1024 || perCluster < 1 || len(id) > 64 || !validQuotaLease(lease) {
		return "", errors.New("invalid request quota")
	}
	token, err := jobToken()
	if err != nil {
		return "", err
	}
	err = s.transactionWithLock(ctx, 2, func(tx *sql.Tx, now int64) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM platform_request_quotas WHERE lease_until<=?`, now); err != nil {
			return err
		}
		var count, local int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN cluster_id=? THEN 1 ELSE 0 END),0) FROM platform_request_quotas WHERE lane=?`, id, lane).Scan(&count, &local); err != nil {
			return err
		}
		if count >= total || local >= perCluster {
			return cluster.ErrRequestQuotaExceeded
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO platform_request_quotas(token,lane,cluster_id,lease_until) VALUES(?,?,?,?)`, token, lane, id, now+lease.Milliseconds())
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}
func (s securityJobStore) renewQuota(ctx context.Context, token string, lease time.Duration) error {
	if !validQuotaLease(lease) {
		return errors.New("invalid request quota lease")
	}
	return s.transactionWithLock(ctx, 2, func(tx *sql.Tx, now int64) error {
		var until int64
		err := tx.QueryRowContext(ctx, `SELECT lease_until FROM platform_request_quotas WHERE token=?`, token).Scan(&until)
		if errors.Is(err, sql.ErrNoRows) {
			return cluster.ErrRequestQuotaLeaseLost
		}
		if err != nil {
			return err
		}
		if until <= now {
			return cluster.ErrRequestQuotaLeaseLost
		}
		_, err = tx.ExecContext(ctx, `UPDATE platform_request_quotas SET lease_until=? WHERE token=?`, now+lease.Milliseconds(), token)
		return err
	})
}

func (store *SQLite) AcquireRequestQuota(ctx context.Context, lane, id string, total, perCluster int, lease time.Duration) (string, error) {
	return (securityJobStore{db: store.db, sqlite: true}).acquireQuota(ctx, lane, id, total, perCluster, lease)
}
func (store *SQLite) RenewRequestQuota(ctx context.Context, token string, lease time.Duration) error {
	return (securityJobStore{db: store.db, sqlite: true}).renewQuota(ctx, token, lease)
}
func (store *SQLite) ReleaseRequestQuota(ctx context.Context, token string) error {
	return (securityJobStore{db: store.db, sqlite: true}).releaseQuota(ctx, token)
}

func (store *MySQL) AcquireRequestQuota(ctx context.Context, lane, id string, total, perCluster int, lease time.Duration) (string, error) {
	return (securityJobStore{db: store.db, sqlite: false}).acquireQuota(ctx, lane, id, total, perCluster, lease)
}
func (store *MySQL) RenewRequestQuota(ctx context.Context, token string, lease time.Duration) error {
	return (securityJobStore{db: store.db, sqlite: false}).renewQuota(ctx, token, lease)
}
func (store *MySQL) ReleaseRequestQuota(ctx context.Context, token string) error {
	return (securityJobStore{db: store.db, sqlite: false}).releaseQuota(ctx, token)
}

// Match acquisition/renewal lock order; deleting by primary key outside this
// control transaction can deadlock expiry-index cleanup against token release.
func (s securityJobStore) releaseQuota(ctx context.Context, token string) error {
	return s.transactionWithLock(ctx, 2, func(tx *sql.Tx, _ int64) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM platform_request_quotas WHERE token=?`, token)
		return err
	})
}
