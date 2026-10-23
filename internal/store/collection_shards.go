package store

import (
	"context"
	"database/sql"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func migrateCollectionShards(ctx context.Context, db *sql.DB, sqlite bool) error {
	suffix := ""
	if !sqlite {
		suffix = " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS collection_shard_config(id INTEGER PRIMARY KEY,shard_count INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS collection_members(owner VARCHAR(64) PRIMARY KEY,lease_until BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS collection_shards(shard_id INTEGER PRIMARY KEY,owner VARCHAR(64) NOT NULL,epoch BIGINT NOT NULL,lease_until BIGINT NOT NULL)`,
	} {
		if _, err := db.ExecContext(ctx, stmt+suffix); err != nil {
			return err
		}
	}
	return nil
}

// CountActiveCollectionShards follows the member heartbeat, which stable
// owners renew without updating the shard epoch rows.
func CountActiveCollectionShards(ctx context.Context, db *sql.DB, sqlite bool) (int, error) {
	clock := `CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(3))*1000 AS SIGNED)`
	if sqlite {
		clock = `CAST((julianday('now')-2440587.5)*86400000 AS INTEGER)`
	}
	var owned int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collection_shards AS shards JOIN collection_members AS members ON members.owner=shards.owner WHERE shards.owner<>'' AND members.lease_until>`+clock).Scan(&owned)
	return owned, err
}

func (s securityJobStore) syncShards(ctx context.Context, owner string, count int, lease time.Duration) ([]cluster.CollectionLease, error) {
	if count < 1 || count > 64 || owner == "" || len(owner) > 64 || lease < 10*time.Second || lease > time.Minute {
		return nil, cluster.ErrCollectionShardCount
	}
	result := []cluster.CollectionLease{}
	err := s.transactionWithLock(ctx, 3, func(tx *sql.Tx, now int64) error {
		var stored int
		err := tx.QueryRowContext(ctx, `SELECT shard_count FROM collection_shard_config WHERE id=1`).Scan(&stored)
		if err == sql.ErrNoRows {
			if _, err = tx.ExecContext(ctx, `INSERT INTO collection_shard_config(id,shard_count) VALUES(1,?)`, count); err != nil {
				return err
			}
			for i := 0; i < count; i++ {
				if _, err = tx.ExecContext(ctx, `INSERT INTO collection_shards(shard_id,owner,epoch,lease_until) VALUES(?,'',0,0)`, i); err != nil {
					return err
				}
			}
		} else if err != nil {
			return err
		} else if stored != count {
			return cluster.ErrCollectionShardCount
		}
		var priorUntil int64
		priorErr := tx.QueryRowContext(ctx, `SELECT lease_until FROM collection_members WHERE owner=?`, owner).Scan(&priorUntil)
		if priorErr != nil && priorErr != sql.ErrNoRows {
			return priorErr
		}
		wasAlive := priorUntil > now
		if _, err = tx.ExecContext(ctx, `DELETE FROM collection_members WHERE lease_until<=?`, now); err != nil {
			return err
		}
		stmt := `INSERT INTO collection_members(owner,lease_until) VALUES(?,?) ON CONFLICT(owner) DO UPDATE SET lease_until=excluded.lease_until`
		if !s.sqlite {
			stmt = `INSERT INTO collection_members(owner,lease_until) VALUES(?,?) ON DUPLICATE KEY UPDATE lease_until=VALUES(lease_until)`
		}
		if _, err = tx.ExecContext(ctx, stmt, owner, now+lease.Milliseconds()); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT owner,lease_until FROM collection_members ORDER BY owner`)
		if err != nil {
			return err
		}
		var members []string
		memberUntil := map[string]int64{}
		for rows.Next() {
			var value string
			var expiry int64
			if err = rows.Scan(&value, &expiry); err != nil {
				rows.Close()
				return err
			}
			members = append(members, value)
			memberUntil[value] = expiry
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for i := 0; i < count; i++ {
			var holder string
			var epoch, until int64
			if err = tx.QueryRowContext(ctx, `SELECT owner,epoch,lease_until FROM collection_shards WHERE shard_id=?`, i).Scan(&holder, &epoch, &until); err != nil {
				return err
			}
			desired := members[i%len(members)]
			if desired != owner {
				if holder == owner {
					if _, err = tx.ExecContext(ctx, `UPDATE collection_shards SET owner='',epoch=epoch+1,lease_until=0 WHERE shard_id=?`, i); err != nil {
						return err
					}
				}
				continue
			}
			if holder != owner && holder != "" && memberUntil[holder] > now {
				continue
			}
			// Stable owners renew only their member heartbeat, never an epoch
			// row held by a long snapshot transaction.
			if holder != owner || !wasAlive {
				epoch++
				if _, err = tx.ExecContext(ctx, `UPDATE collection_shards SET owner=?,epoch=?,lease_until=? WHERE shard_id=?`, owner, epoch, now+lease.Milliseconds(), i); err != nil {
					return err
				}
			}

			result = append(result, cluster.CollectionLease{Shard: i, Count: count, Owner: owner, Epoch: epoch})
		}
		return nil
	})
	return result, err
}
func (s securityJobStore) releaseShards(ctx context.Context, owner string) error {
	return s.transactionWithLock(ctx, 3, func(tx *sql.Tx, _ int64) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM collection_members WHERE owner=?`, owner); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE collection_shards SET owner='',epoch=epoch+1,lease_until=0 WHERE owner=?`, owner)
		return err
	})
}

// Lock the owner row until commit so a new owner cannot race a stale write.
func checkCollectionLease(ctx context.Context, tx *sql.Tx, sqlite bool, id string) error {
	lease, ok := cluster.CollectionLeaseFromContext(ctx)
	if !ok {
		return nil
	}
	if lease.Count < 1 || lease.Shard < 0 || lease.Shard >= lease.Count || (id != "" && cluster.CollectionShard(id, lease.Count) != lease.Shard) {
		return cluster.ErrCollectionLeaseLost
	}
	if sqlite {
		if _, err := tx.ExecContext(ctx, `UPDATE collection_shards SET epoch=epoch WHERE shard_id=?`, lease.Shard); err != nil {
			return err
		}
	}
	stmt := `SELECT owner,epoch,lease_until FROM collection_shards WHERE shard_id=?`
	if !sqlite {
		stmt += " LOCK IN SHARE MODE"
	}
	var holder string
	var epoch, until int64
	if err := tx.QueryRowContext(ctx, stmt, lease.Shard).Scan(&holder, &epoch, &until); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		return cluster.ErrCollectionLeaseLost
	}
	// A non-locking heartbeat read keeps lease renewal independent of data
	// transactions. Epoch changes still wait for the shared owner-row lock.
	if err := tx.QueryRowContext(ctx, `SELECT lease_until FROM collection_members WHERE owner=?`, lease.Owner).Scan(&until); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		return cluster.ErrCollectionLeaseLost
	}
	var now int64
	clock := `SELECT CAST((julianday('now')-2440587.5)*86400000 AS INTEGER)`
	if !sqlite {
		clock = `SELECT CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(3))*1000 AS SIGNED)`
	}
	if err := tx.QueryRowContext(ctx, clock).Scan(&now); err != nil {
		return err
	}
	if holder != lease.Owner || epoch != lease.Epoch || until <= now {
		return cluster.ErrCollectionLeaseLost
	}
	return nil
}
func commitCollectionTx(ctx context.Context, tx *sql.Tx, sqlite bool) error {
	if err := checkCollectionLease(ctx, tx, sqlite, ""); err != nil {
		return err
	}
	return tx.Commit()
}
func collectionExec(ctx context.Context, db *sql.DB, sqlite bool, id, query string, args ...any) (sql.Result, error) {
	if _, ok := cluster.CollectionLeaseFromContext(ctx); !ok {
		return db.ExecContext(ctx, query, args...)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = checkCollectionLease(ctx, tx, sqlite, id); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if err = commitCollectionTx(ctx, tx, sqlite); err != nil {
		return nil, err
	}
	return result, nil
}

func (store *SQLite) SyncCollectionShards(ctx context.Context, owner string, count int, lease time.Duration) ([]cluster.CollectionLease, error) {
	return (securityJobStore{db: store.db, sqlite: true}).syncShards(ctx, owner, count, lease)
}
func (store *SQLite) ReleaseCollectionShards(ctx context.Context, owner string) error {
	return (securityJobStore{db: store.db, sqlite: true}).releaseShards(ctx, owner)
}

func (store *MySQL) SyncCollectionShards(ctx context.Context, owner string, count int, lease time.Duration) ([]cluster.CollectionLease, error) {
	return (securityJobStore{db: store.db, sqlite: false}).syncShards(ctx, owner, count, lease)
}
func (store *MySQL) ReleaseCollectionShards(ctx context.Context, owner string) error {
	return (securityJobStore{db: store.db, sqlite: false}).releaseShards(ctx, owner)
}

func verifyCollectionMode(ctx context.Context, db *sql.DB, count int) error {
	var stored int
	err := db.QueryRowContext(ctx, `SELECT shard_count FROM collection_shard_config WHERE id=1`).Scan(&stored)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if stored != count {
		return cluster.ErrCollectionShardCount
	}
	return nil
}
func (store *SQLite) VerifyCollectionMode(ctx context.Context, count int) error {
	return verifyCollectionMode(ctx, store.db, count)
}
func (store *MySQL) VerifyCollectionMode(ctx context.Context, count int) error {
	return verifyCollectionMode(ctx, store.db, count)
}
