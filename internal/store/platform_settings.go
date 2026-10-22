package store

import (
	"context"
	"database/sql"
	"time"

	"example.com/k8s-platform/internal/platformsettings"
)

func listPlatformSettings(ctx context.Context, db *sql.DB) ([]platformsettings.Record, error) {
	rows, err := db.QueryContext(ctx, `SELECT section,payload,updated_at FROM platform_settings ORDER BY section`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]platformsettings.Record, 0, 6)
	for rows.Next() {
		var record platformsettings.Record
		if err := rows.Scan(&record.Section, &record.Payload, &record.UpdatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (store *SQLite) ListPlatformSettings(ctx context.Context) ([]platformsettings.Record, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT section,payload,updated_at FROM platform_settings ORDER BY section`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]platformsettings.Record, 0, 6)
	for rows.Next() {
		var record platformsettings.Record
		var updatedAt string
		if err := rows.Scan(&record.Section, &record.Payload, &updatedAt); err != nil {
			return nil, err
		}
		parsed, err := time.Parse(timeLayout, updatedAt)
		if err != nil {
			return nil, err
		}
		record.UpdatedAt = parsed
		records = append(records, record)
	}
	return records, rows.Err()
}

func (store *SQLite) SavePlatformSetting(ctx context.Context, record platformsettings.Record) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO platform_settings(section,payload,updated_at) VALUES(?,?,?) ON CONFLICT(section) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, record.Section, string(record.Payload), record.UpdatedAt.UTC().Format(timeLayout))
	return err
}

func (store *MySQL) ListPlatformSettings(ctx context.Context) ([]platformsettings.Record, error) {
	return listPlatformSettings(ctx, store.db)
}

func (store *MySQL) SavePlatformSetting(ctx context.Context, record platformsettings.Record) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO platform_settings(section,payload,updated_at) VALUES(?,?,?) ON DUPLICATE KEY UPDATE payload=VALUES(payload),updated_at=VALUES(updated_at)`, record.Section, record.Payload, record.UpdatedAt.UTC())
	return err
}

var _ platformsettings.Repository = (*SQLite)(nil)
var _ platformsettings.Repository = (*MySQL)(nil)
