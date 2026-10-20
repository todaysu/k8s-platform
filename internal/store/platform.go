package store

import (
	"database/sql"

	"example.com/k8s-platform/internal/cluster"
)

// PlatformStore is the complete persistent boundary used by the platform.
// SQLite and MySQL both implement it so local and Kubernetes deployments use
// the same application behavior.
type PlatformStore interface {
	cluster.ClusterRepository
	cluster.ClusterHealthRepository
	cluster.ClusterClassificationRepository
	cluster.CapacityRepository
	cluster.CapacityClassificationRepository
	cluster.FleetCapacityRepository
	cluster.EventRepository
	cluster.ResourceIndexRepository
	SQLDB() *sql.DB
	Close() error
}

var _ PlatformStore = (*SQLite)(nil)
