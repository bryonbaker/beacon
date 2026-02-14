package database

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/bryonbaker/beacon/internal/models"
	_ "github.com/mattn/go-sqlite3" // SQLite driver
	"go.uber.org/zap"
)

// SQLiteDB implements the Database interface using SQLite with the go-sqlite3 driver.
type SQLiteDB struct {
	db     *sql.DB
	logger *zap.Logger
}

// Ensure SQLiteDB satisfies the Database interface at compile time.
var _ Database = (*SQLiteDB)(nil)

// NewSQLiteDB opens (or creates) a SQLite database at dbPath, applies PRAGMAs for
// WAL mode, incremental auto-vacuum, foreign keys, and a busy timeout, then
// creates the managed_objects table and its indexes if they do not already exist.
func NewSQLiteDB(dbPath string, logger *zap.Logger) (*SQLiteDB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Limit to a single connection so WAL mode works correctly for an
	// embedded database and we avoid "database is locked" errors.
	db.SetMaxOpenConns(1)

	s := &SQLiteDB{
		db:     db,
		logger: logger,
	}

	if err := s.applyPragmas(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to apply pragmas: %w", err)
	}

	if err := s.createSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	if err := s.migrateSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate schema: %w", err)
	}

	logger.Info("SQLite database initialised", zap.String("path", dbPath))
	return s, nil
}

// applyPragmas sets the SQLite PRAGMAs required for correct operation.
func (s *SQLiteDB) applyPragmas() error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA auto_vacuum=INCREMENTAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := s.db.Exec(p); err != nil {
			return fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	return nil
}

// createSchema creates the managed_objects table and all supporting indexes.
func (s *SQLiteDB) createSchema() error {
	const createTable = `
CREATE TABLE IF NOT EXISTS managed_objects (
    id                           TEXT PRIMARY KEY,
    resource_uid                 TEXT NOT NULL,
    resource_type                TEXT NOT NULL,
    resource_name                TEXT NOT NULL,
    resource_namespace           TEXT NOT NULL DEFAULT '',
    annotation_value             TEXT NOT NULL DEFAULT '',
    cluster_state                TEXT NOT NULL DEFAULT 'exists',
    detection_source             TEXT NOT NULL DEFAULT '',
    created_at                   TEXT NOT NULL,
    deleted_at                   TEXT,
    last_reconciled              TEXT,
    notified_created             INTEGER NOT NULL DEFAULT 0,
    notified_deleted             INTEGER NOT NULL DEFAULT 0,
    notification_failed          INTEGER NOT NULL DEFAULT 0,
    notification_failed_code     INTEGER NOT NULL DEFAULT 0,
    created_notification_sent_at TEXT,
    deleted_notification_sent_at TEXT,
    notification_attempts        INTEGER NOT NULL DEFAULT 0,
    last_notification_attempt    TEXT,
    labels                       TEXT NOT NULL DEFAULT '',
    annotations                  TEXT NOT NULL DEFAULT '',
    resource_version             TEXT NOT NULL DEFAULT '',
    full_metadata                TEXT NOT NULL DEFAULT ''
);`

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_resource_uid ON managed_objects (resource_uid);`,
		`CREATE INDEX IF NOT EXISTS idx_resource_type ON managed_objects (resource_type);`,
		`CREATE INDEX IF NOT EXISTS idx_resource_namespace ON managed_objects (resource_namespace);`,
		`CREATE INDEX IF NOT EXISTS idx_notification ON managed_objects (cluster_state, notified_created, notified_deleted);`,
		`CREATE INDEX IF NOT EXISTS idx_reconciliation ON managed_objects (resource_type, last_reconciled);`,
		`CREATE INDEX IF NOT EXISTS idx_cleanup ON managed_objects (deleted_at, notified_deleted, cluster_state);`,
	}

	if _, err := s.db.Exec(createTable); err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	for _, idx := range indexes {
		if _, err := s.db.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}

	return nil
}

// migrateSchema applies incremental schema migrations for existing databases.
func (s *SQLiteDB) migrateSchema() error {
	// Check whether the annotations column already exists.
	rows, err := s.db.Query("PRAGMA table_info(managed_objects)")
	if err != nil {
		return fmt.Errorf("reading table info: %w", err)
	}
	defer rows.Close()

	hasAnnotations := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scanning table info: %w", err)
		}
		if name == "annotations" {
			hasAnnotations = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating table info: %w", err)
	}

	if !hasAnnotations {
		if _, err := s.db.Exec("ALTER TABLE managed_objects ADD COLUMN annotations TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("adding annotations column: %w", err)
		}
		s.logger.Info("migrated schema: added annotations column")
	}

	return nil
}

// Close closes the underlying database connection.
func (s *SQLiteDB) Close() error {
	return s.db.Close()
}

// Ping verifies the database connection is alive.
func (s *SQLiteDB) Ping() error {
	return s.db.Ping()
}

// InsertManagedObject inserts a new managed object record into the database.
func (s *SQLiteDB) InsertManagedObject(obj *models.ManagedObject) error {
	const query = `
INSERT INTO managed_objects (
    id, resource_uid, resource_type, resource_name, resource_namespace,
    annotation_value, cluster_state, detection_source, created_at, deleted_at,
    last_reconciled, notified_created, notified_deleted, notification_failed,
    notification_failed_code, created_notification_sent_at, deleted_notification_sent_at,
    notification_attempts, last_notification_attempt, labels, annotations, resource_version, full_metadata
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query,
		obj.ID,
		obj.ResourceUID,
		obj.ResourceType,
		obj.ResourceName,
		obj.ResourceNamespace,
		obj.AnnotationValue,
		obj.ClusterState,
		obj.DetectionSource,
		obj.CreatedAt.Format(time.RFC3339),
		formatNullableTime(obj.DeletedAt),
		formatNullableTime(obj.LastReconciled),
		boolToInt(obj.NotifiedCreated),
		boolToInt(obj.NotifiedDeleted),
		boolToInt(obj.NotificationFailed),
		obj.NotificationFailedCode,
		formatNullableTime(obj.CreatedNotificationSentAt),
		formatNullableTime(obj.DeletedNotificationSentAt),
		obj.NotificationAttempts,
		formatNullableTime(obj.LastNotificationAttempt),
		obj.Labels,
		obj.Annotations,
		obj.ResourceVersion,
		obj.FullMetadata,
	)
	if err != nil {
		return fmt.Errorf("insert managed object: %w", err)
	}
	return nil
}

// GetManagedObjectByUID retrieves a managed object by its Kubernetes resource UID.
func (s *SQLiteDB) GetManagedObjectByUID(uid string) (*models.ManagedObject, error) {
	const query = `SELECT
    id, resource_uid, resource_type, resource_name, resource_namespace,
    annotation_value, cluster_state, detection_source, created_at, deleted_at,
    last_reconciled, notified_created, notified_deleted, notification_failed,
    notification_failed_code, created_notification_sent_at, deleted_notification_sent_at,
    notification_attempts, last_notification_attempt, labels, annotations, resource_version, full_metadata
FROM managed_objects WHERE resource_uid = ?`

	return s.scanManagedObject(s.db.QueryRow(query, uid))
}

// GetManagedObjectByID retrieves a managed object by its internal record ID.
func (s *SQLiteDB) GetManagedObjectByID(id string) (*models.ManagedObject, error) {
	const query = `SELECT
    id, resource_uid, resource_type, resource_name, resource_namespace,
    annotation_value, cluster_state, detection_source, created_at, deleted_at,
    last_reconciled, notified_created, notified_deleted, notification_failed,
    notification_failed_code, created_notification_sent_at, deleted_notification_sent_at,
    notification_attempts, last_notification_attempt, labels, annotations, resource_version, full_metadata
FROM managed_objects WHERE id = ?`

	return s.scanManagedObject(s.db.QueryRow(query, id))
}

// UpdateClusterState sets the cluster state and optional deletion timestamp for
// the managed object identified by the given resource UID.
func (s *SQLiteDB) UpdateClusterState(uid string, state string, deletedAt *time.Time) error {
	const query = `UPDATE managed_objects SET cluster_state = ?, deleted_at = ? WHERE resource_uid = ?`
	_, err := s.db.Exec(query, state, formatNullableTime(deletedAt), uid)
	if err != nil {
		return fmt.Errorf("update cluster state: %w", err)
	}
	return nil
}

// UpdateNotificationStatus marks a notification event as sent. eventType must be
// either "created" or "deleted".
func (s *SQLiteDB) UpdateNotificationStatus(id string, eventType string, sentAt time.Time) error {
	var query string
	switch eventType {
	case "created":
		query = `UPDATE managed_objects SET notified_created = 1, created_notification_sent_at = ? WHERE id = ?`
	case "deleted":
		query = `UPDATE managed_objects SET notified_deleted = 1, deleted_notification_sent_at = ? WHERE id = ?`
	default:
		return fmt.Errorf("unknown event type: %s", eventType)
	}
	_, err := s.db.Exec(query, sentAt.Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("update notification status: %w", err)
	}
	return nil
}

// MarkNotificationFailed records a permanent notification failure with the
// HTTP status code that caused it.
func (s *SQLiteDB) MarkNotificationFailed(id string, statusCode int) error {
	const query = `UPDATE managed_objects SET notification_failed = 1, notification_failed_code = ? WHERE id = ?`
	_, err := s.db.Exec(query, statusCode, id)
	if err != nil {
		return fmt.Errorf("mark notification failed: %w", err)
	}
	return nil
}

// IncrementNotificationAttempts bumps the attempt counter and records the
// current time as the last notification attempt.
func (s *SQLiteDB) IncrementNotificationAttempts(id string) error {
	const query = `UPDATE managed_objects SET notification_attempts = notification_attempts + 1, last_notification_attempt = ? WHERE id = ?`
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(query, now, id)
	if err != nil {
		return fmt.Errorf("increment notification attempts: %w", err)
	}
	return nil
}

// UpdateLastReconciled sets the last_reconciled timestamp.
func (s *SQLiteDB) UpdateLastReconciled(id string, reconciledAt time.Time) error {
	const query = `UPDATE managed_objects SET last_reconciled = ? WHERE id = ?`
	_, err := s.db.Exec(query, reconciledAt.Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("update last reconciled: %w", err)
	}
	return nil
}

// GetPendingNotifications returns managed objects that still require a
// notification. An object is pending if:
//   - It has not been notified of creation, OR
//   - It is in the "deleted" state and has not been notified of deletion
//
// Objects whose notifications have permanently failed are excluded.
func (s *SQLiteDB) GetPendingNotifications(limit int) ([]*models.ManagedObject, error) {
	const query = `SELECT
    id, resource_uid, resource_type, resource_name, resource_namespace,
    annotation_value, cluster_state, detection_source, created_at, deleted_at,
    last_reconciled, notified_created, notified_deleted, notification_failed,
    notification_failed_code, created_notification_sent_at, deleted_notification_sent_at,
    notification_attempts, last_notification_attempt, labels, annotations, resource_version, full_metadata
FROM managed_objects
WHERE (notified_created = 0 OR (cluster_state = 'deleted' AND notified_deleted = 0))
  AND notification_failed = 0
ORDER BY created_at ASC
LIMIT ?`

	return s.queryManagedObjects(query, limit)
}

// GetAllActiveObjects returns all objects in the "exists" state for the given
// resource type.
func (s *SQLiteDB) GetAllActiveObjects(resourceType string) ([]*models.ManagedObject, error) {
	const query = `SELECT
    id, resource_uid, resource_type, resource_name, resource_namespace,
    annotation_value, cluster_state, detection_source, created_at, deleted_at,
    last_reconciled, notified_created, notified_deleted, notification_failed,
    notification_failed_code, created_notification_sent_at, deleted_notification_sent_at,
    notification_attempts, last_notification_attempt, labels, annotations, resource_version, full_metadata
FROM managed_objects
WHERE cluster_state = 'exists' AND resource_type = ?`

	return s.queryManagedObjects(query, resourceType)
}

// GetCleanupEligible returns objects that are deleted, have had their deletion
// notification sent successfully, and whose deleted_at timestamp is older than
// the retention period.
func (s *SQLiteDB) GetCleanupEligible(retentionPeriod time.Duration) ([]*models.ManagedObject, error) {
	cutoff := time.Now().Add(-retentionPeriod).Format(time.RFC3339)
	const query = `SELECT
    id, resource_uid, resource_type, resource_name, resource_namespace,
    annotation_value, cluster_state, detection_source, created_at, deleted_at,
    last_reconciled, notified_created, notified_deleted, notification_failed,
    notification_failed_code, created_notification_sent_at, deleted_notification_sent_at,
    notification_attempts, last_notification_attempt, labels, annotations, resource_version, full_metadata
FROM managed_objects
WHERE cluster_state = 'deleted'
  AND notified_deleted = 1
  AND notification_failed = 0
  AND deleted_at < ?`

	return s.queryManagedObjects(query, cutoff)
}

// CountByState returns the count of objects in the "exists" and "deleted" states.
func (s *SQLiteDB) CountByState() (exists int, deleted int, err error) {
	const query = `SELECT cluster_state, COUNT(*) FROM managed_objects GROUP BY cluster_state`
	rows, err := s.db.Query(query)
	if err != nil {
		return 0, 0, fmt.Errorf("count by state: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return 0, 0, fmt.Errorf("scan count by state: %w", err)
		}
		switch state {
		case models.ClusterStateExists:
			exists = count
		case models.ClusterStateDeleted:
			deleted = count
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("rows iteration: %w", err)
	}
	return exists, deleted, nil
}

// DeleteRecord permanently removes a managed object record by its internal ID.
func (s *SQLiteDB) DeleteRecord(id string) error {
	const query = `DELETE FROM managed_objects WHERE id = ?`
	_, err := s.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("delete record: %w", err)
	}
	return nil
}

// RunIncrementalVacuum triggers an incremental vacuum to reclaim unused pages.
func (s *SQLiteDB) RunIncrementalVacuum() error {
	_, err := s.db.Exec("PRAGMA incremental_vacuum")
	if err != nil {
		return fmt.Errorf("incremental vacuum: %w", err)
	}
	return nil
}

// GetDatabaseSizeBytes returns the current size of the database in bytes,
// computed as page_count * page_size.
func (s *SQLiteDB) GetDatabaseSizeBytes() (int64, error) {
	var pageCount int64
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, fmt.Errorf("page_count: %w", err)
	}

	var pageSize int64
	if err := s.db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("page_size: %w", err)
	}

	return pageCount * pageSize, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// scanManagedObject scans a single row into a ManagedObject.
func (s *SQLiteDB) scanManagedObject(row *sql.Row) (*models.ManagedObject, error) {
	var obj models.ManagedObject
	var createdAt string
	var deletedAt, lastReconciled, createdSentAt, deletedSentAt, lastAttempt sql.NullString
	var notifiedCreated, notifiedDeleted, notificationFailed int

	err := row.Scan(
		&obj.ID,
		&obj.ResourceUID,
		&obj.ResourceType,
		&obj.ResourceName,
		&obj.ResourceNamespace,
		&obj.AnnotationValue,
		&obj.ClusterState,
		&obj.DetectionSource,
		&createdAt,
		&deletedAt,
		&lastReconciled,
		&notifiedCreated,
		&notifiedDeleted,
		&notificationFailed,
		&obj.NotificationFailedCode,
		&createdSentAt,
		&deletedSentAt,
		&obj.NotificationAttempts,
		&lastAttempt,
		&obj.Labels,
		&obj.Annotations,
		&obj.ResourceVersion,
		&obj.FullMetadata,
	)
	if err != nil {
		return nil, fmt.Errorf("scan managed object: %w", err)
	}

	obj.NotifiedCreated = notifiedCreated != 0
	obj.NotifiedDeleted = notifiedDeleted != 0
	obj.NotificationFailed = notificationFailed != 0

	obj.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}

	obj.DeletedAt, err = parseNullableTime(deletedAt)
	if err != nil {
		return nil, fmt.Errorf("parse deleted_at: %w", err)
	}

	obj.LastReconciled, err = parseNullableTime(lastReconciled)
	if err != nil {
		return nil, fmt.Errorf("parse last_reconciled: %w", err)
	}

	obj.CreatedNotificationSentAt, err = parseNullableTime(createdSentAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_notification_sent_at: %w", err)
	}

	obj.DeletedNotificationSentAt, err = parseNullableTime(deletedSentAt)
	if err != nil {
		return nil, fmt.Errorf("parse deleted_notification_sent_at: %w", err)
	}

	obj.LastNotificationAttempt, err = parseNullableTime(lastAttempt)
	if err != nil {
		return nil, fmt.Errorf("parse last_notification_attempt: %w", err)
	}

	return &obj, nil
}

// queryManagedObjects executes a query that returns multiple managed object rows.
func (s *SQLiteDB) queryManagedObjects(query string, args ...interface{}) ([]*models.ManagedObject, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query managed objects: %w", err)
	}
	defer rows.Close()

	var results []*models.ManagedObject
	for rows.Next() {
		var obj models.ManagedObject
		var createdAt string
		var deletedAt, lastReconciled, createdSentAt, deletedSentAt, lastAttempt sql.NullString
		var notifiedCreated, notifiedDeleted, notificationFailed int

		err := rows.Scan(
			&obj.ID,
			&obj.ResourceUID,
			&obj.ResourceType,
			&obj.ResourceName,
			&obj.ResourceNamespace,
			&obj.AnnotationValue,
			&obj.ClusterState,
			&obj.DetectionSource,
			&createdAt,
			&deletedAt,
			&lastReconciled,
			&notifiedCreated,
			&notifiedDeleted,
			&notificationFailed,
			&obj.NotificationFailedCode,
			&createdSentAt,
			&deletedSentAt,
			&obj.NotificationAttempts,
			&lastAttempt,
			&obj.Labels,
			&obj.Annotations,
			&obj.ResourceVersion,
			&obj.FullMetadata,
		)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		obj.NotifiedCreated = notifiedCreated != 0
		obj.NotifiedDeleted = notifiedDeleted != 0
		obj.NotificationFailed = notificationFailed != 0

		obj.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}

		obj.DeletedAt, err = parseNullableTime(deletedAt)
		if err != nil {
			return nil, fmt.Errorf("parse deleted_at: %w", err)
		}

		obj.LastReconciled, err = parseNullableTime(lastReconciled)
		if err != nil {
			return nil, fmt.Errorf("parse last_reconciled: %w", err)
		}

		obj.CreatedNotificationSentAt, err = parseNullableTime(createdSentAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_notification_sent_at: %w", err)
		}

		obj.DeletedNotificationSentAt, err = parseNullableTime(deletedSentAt)
		if err != nil {
			return nil, fmt.Errorf("parse deleted_notification_sent_at: %w", err)
		}

		obj.LastNotificationAttempt, err = parseNullableTime(lastAttempt)
		if err != nil {
			return nil, fmt.Errorf("parse last_notification_attempt: %w", err)
		}

		results = append(results, &obj)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}

	return results, nil
}

// formatNullableTime converts a *time.Time to a sql.NullString in RFC3339 format.
func formatNullableTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.Format(time.RFC3339), Valid: true}
}

// parseNullableTime converts a sql.NullString in RFC3339 format to a *time.Time.
func parseNullableTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// boolToInt converts a Go bool to a SQLite integer (0 or 1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
