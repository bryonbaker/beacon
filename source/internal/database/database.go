// Package database defines the storage interface and implementations for the
// beacon service. All persistent state for managed Kubernetes objects
// flows through the Database interface.
package database

import (
	"time"

	"github.com/bryonbaker/beacon/internal/models"
)

// Database defines the contract for persistent storage of managed objects.
// Implementations must be safe for concurrent use by multiple goroutines.
type Database interface {
	// Close releases any resources held by the database connection.
	Close() error

	// Ping verifies the database connection is still alive.
	Ping() error

	// InsertManagedObject persists a new managed object record.
	InsertManagedObject(obj *models.ManagedObject) error

	// GetManagedObjectByUID retrieves a managed object by its Kubernetes resource UID.
	GetManagedObjectByUID(uid string) (*models.ManagedObject, error)

	// GetManagedObjectByID retrieves a managed object by its internal record ID.
	GetManagedObjectByID(id string) (*models.ManagedObject, error)

	// UpdateClusterState sets the cluster state for a managed object identified by
	// its resource UID and optionally records a deletion timestamp.
	UpdateClusterState(uid string, state string, deletedAt *time.Time) error

	// UpdateNotificationStatus marks a notification event (e.g. "created" or
	// "deleted") as sent for the object identified by its internal ID.
	UpdateNotificationStatus(id string, eventType string, sentAt time.Time) error

	// MarkNotificationFailed records a permanent notification failure along with
	// the HTTP status code that caused it.
	MarkNotificationFailed(id string, statusCode int) error

	// IncrementNotificationAttempts bumps the attempt counter and records the
	// current time as the last notification attempt.
	IncrementNotificationAttempts(id string) error

	// UpdateLastReconciled sets the last_reconciled timestamp for the object
	// identified by its internal ID.
	UpdateLastReconciled(id string, reconciledAt time.Time) error

	// GetPendingNotifications returns up to limit managed objects that still
	// require a notification to be sent (either created or deleted).
	GetPendingNotifications(limit int) ([]*models.ManagedObject, error)

	// GetAllActiveObjects returns all objects in the "exists" state for a given
	// resource type.
	GetAllActiveObjects(resourceType string) ([]*models.ManagedObject, error)

	// GetCleanupEligible returns objects that have been deleted, successfully
	// notified, and whose deletion timestamp is older than the retention period.
	GetCleanupEligible(retentionPeriod time.Duration) ([]*models.ManagedObject, error)

	// CountByState returns the number of objects in the "exists" and "deleted"
	// states respectively.
	CountByState() (exists int, deleted int, err error)

	// DeleteRecord permanently removes a managed object record by its internal ID.
	DeleteRecord(id string) error

	// RunIncrementalVacuum triggers an incremental vacuum to reclaim unused pages.
	RunIncrementalVacuum() error

	// GetDatabaseSizeBytes returns the current on-disk size of the database in bytes.
	GetDatabaseSizeBytes() (int64, error)
}
