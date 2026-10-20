package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func (store *SQLite) SaveFleetCapacitySuccess(ctx context.Context, item cluster.FleetCapacityItem, at time.Time) error {
	payload, err := json.Marshal(item.Capacity)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO fleet_capacity_snapshots(cluster_id,captured_at,last_attempt_at,payload,failure_code) VALUES(?,?,?,?, '') ON CONFLICT(cluster_id) DO UPDATE SET captured_at=excluded.captured_at,last_attempt_at=excluded.last_attempt_at,payload=excluded.payload,failure_code=''`, item.ClusterID, at.UTC().Format(timeLayout), at.UTC().Format(timeLayout), string(payload))
	return capacityPersistenceError(err)
}

func (store *SQLite) SaveFleetCapacityFailure(ctx context.Context, item cluster.FleetCapacityFailure, at time.Time) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO fleet_capacity_snapshots(cluster_id,captured_at,last_attempt_at,payload,failure_code) VALUES(?,NULL,?,NULL,?) ON CONFLICT(cluster_id) DO UPDATE SET last_attempt_at=excluded.last_attempt_at,failure_code=excluded.failure_code`, item.ClusterID, at.UTC().Format(timeLayout), item.Code)
	return capacityPersistenceError(err)
}

type storedCapacitySnapshot struct {
	capacity cluster.ClusterCapacity
	failure  string
	attempt  time.Time
	hasValue bool
}

func (store *SQLite) ListFleetCapacitySnapshots(ctx context.Context) (cluster.FleetCapacity, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT cluster_id,last_attempt_at,payload,failure_code FROM fleet_capacity_snapshots`)
	if err != nil {
		return cluster.FleetCapacity{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	values := map[string]storedCapacitySnapshot{}
	for rows.Next() {
		var id, attemptText, failure string
		var payload sql.NullString
		if err := rows.Scan(&id, &attemptText, &payload, &failure); err != nil {
			_ = rows.Close()
			return cluster.FleetCapacity{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		attempt, parseErr := time.Parse(timeLayout, attemptText)
		if parseErr != nil {
			_ = rows.Close()
			return cluster.FleetCapacity{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		value := storedCapacitySnapshot{attempt: attempt, failure: failure, hasValue: payload.Valid}
		if payload.Valid && json.Unmarshal([]byte(payload.String), &value.capacity) != nil {
			_ = rows.Close()
			return cluster.FleetCapacity{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		values[id] = value
	}
	if err := rows.Close(); err != nil {
		return cluster.FleetCapacity{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	records, err := store.ListRecords(ctx)
	if err != nil {
		return cluster.FleetCapacity{}, err
	}
	return assembleFleetCapacity(records, values), nil
}

func (store *MySQL) SaveFleetCapacitySuccess(ctx context.Context, item cluster.FleetCapacityItem, at time.Time) error {
	payload, err := json.Marshal(item.Capacity)
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO fleet_capacity_snapshots(cluster_id,captured_at,last_attempt_at,payload,failure_code) VALUES(?,?,?,?, '') ON DUPLICATE KEY UPDATE captured_at=VALUES(captured_at),last_attempt_at=VALUES(last_attempt_at),payload=VALUES(payload),failure_code=''`, item.ClusterID, at.UTC(), at.UTC(), payload)
	return capacityPersistenceError(err)
}

func (store *MySQL) SaveFleetCapacityFailure(ctx context.Context, item cluster.FleetCapacityFailure, at time.Time) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO fleet_capacity_snapshots(cluster_id,captured_at,last_attempt_at,payload,failure_code) VALUES(?,NULL,?,NULL,?) ON DUPLICATE KEY UPDATE last_attempt_at=VALUES(last_attempt_at),failure_code=VALUES(failure_code)`, item.ClusterID, at.UTC(), item.Code)
	return capacityPersistenceError(err)
}

func (store *MySQL) ListFleetCapacitySnapshots(ctx context.Context) (cluster.FleetCapacity, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT cluster_id,last_attempt_at,payload,failure_code FROM fleet_capacity_snapshots`)
	if err != nil {
		return cluster.FleetCapacity{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	values := map[string]storedCapacitySnapshot{}
	for rows.Next() {
		var id, failure string
		var attempt time.Time
		var payload []byte
		if err := rows.Scan(&id, &attempt, &payload, &failure); err != nil {
			_ = rows.Close()
			return cluster.FleetCapacity{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		value := storedCapacitySnapshot{attempt: attempt.UTC(), failure: failure, hasValue: len(payload) > 0}
		if len(payload) > 0 && json.Unmarshal(payload, &value.capacity) != nil {
			_ = rows.Close()
			return cluster.FleetCapacity{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
		}
		values[id] = value
	}
	if err := rows.Close(); err != nil {
		return cluster.FleetCapacity{}, &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	records, err := store.ListRecords(ctx)
	if err != nil {
		return cluster.FleetCapacity{}, err
	}
	return assembleFleetCapacity(records, values), nil
}

func assembleFleetCapacity(records []cluster.ClusterRecord, values map[string]storedCapacitySnapshot) cluster.FleetCapacity {
	result := cluster.FleetCapacity{Items: []cluster.FleetCapacityItem{}, Failures: []cluster.FleetCapacityFailure{}}
	for _, record := range records {
		value, exists := values[record.ID]
		if exists && value.attempt.After(result.CollectedAt) {
			result.CollectedAt = value.attempt
		}
		metadata := record.ManagedCluster
		if exists && value.hasValue {
			result.Items = append(result.Items, cluster.FleetCapacityItem{ClusterID: metadata.ID, ClusterName: metadata.Name, Environment: metadata.Environment, ClusterType: metadata.ClusterType, TechnologyStack: metadata.TechnologyStack, Capacity: value.capacity})
		}
		if !exists || value.failure != "" || !value.hasValue {
			code := cluster.CodeCapacityPending
			if exists && value.failure != "" {
				code = value.failure
			}
			result.Failures = append(result.Failures, cluster.FleetCapacityFailure{ClusterID: metadata.ID, ClusterName: metadata.Name, Environment: metadata.Environment, ClusterType: metadata.ClusterType, TechnologyStack: metadata.TechnologyStack, Code: code})
		}
	}
	return result
}

func capacityPersistenceError(err error) error {
	if err != nil {
		return &cluster.Error{Code: cluster.CodePersistenceFailed}
	}
	return nil
}
