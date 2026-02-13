//go:build integration

package integration_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestDatabase_ConcurrentAccess verifies that the SQLite database handles
// concurrent goroutine access safely. Ten goroutines simultaneously insert
// records and read them back, exercising the database's single-connection
// serialization under contention.
func TestDatabase_ConcurrentAccess(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	db, err := database.NewSQLiteDB(":memory:", logger)
	require.NoError(t, err)
	defer db.Close()

	const numGoroutines = 10
	const numOpsPerGoroutine = 5

	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines*numOpsPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				objID := uuid.New().String()
				resourceUID := fmt.Sprintf("uid-concurrent-%d-%d", goroutineID, j)
				obj := &models.ManagedObject{
					ID:                objID,
					ResourceUID:       resourceUID,
					ResourceType:      "Pod",
					ResourceName:      fmt.Sprintf("concurrent-pod-%d-%d", goroutineID, j),
					ResourceNamespace: "default",
					AnnotationValue:   "managed",
					ClusterState:      models.ClusterStateExists,
					DetectionSource:   models.DetectionSourceWatch,
					CreatedAt:         time.Now(),
					Labels:            `{}`,
					ResourceVersion:   "1",
					FullMetadata:      "{}",
				}

				// Insert.
				if insertErr := db.InsertManagedObject(obj); insertErr != nil {
					errCh <- fmt.Errorf("goroutine %d, op %d: insert failed: %w", goroutineID, j, insertErr)
					continue
				}

				// Read back by UID.
				stored, readErr := db.GetManagedObjectByUID(resourceUID)
				if readErr != nil {
					errCh <- fmt.Errorf("goroutine %d, op %d: read by UID failed: %w", goroutineID, j, readErr)
					continue
				}
				if stored == nil {
					errCh <- fmt.Errorf("goroutine %d, op %d: object not found by UID", goroutineID, j)
					continue
				}

				// Read back by ID.
				stored, readErr = db.GetManagedObjectByID(objID)
				if readErr != nil {
					errCh <- fmt.Errorf("goroutine %d, op %d: read by ID failed: %w", goroutineID, j, readErr)
					continue
				}
				if stored == nil {
					errCh <- fmt.Errorf("goroutine %d, op %d: object not found by ID", goroutineID, j)
					continue
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	// Collect any errors.
	var errors []error
	for e := range errCh {
		errors = append(errors, e)
	}
	assert.Empty(t, errors, "concurrent access should not produce errors: %v", errors)

	// Verify total record count.
	exists, deleted, err := db.CountByState()
	require.NoError(t, err)
	expectedTotal := numGoroutines * numOpsPerGoroutine
	assert.Equal(t, expectedTotal, exists, "all inserted objects should be in 'exists' state")
	assert.Equal(t, 0, deleted, "no objects should be in 'deleted' state")
}

// TestDatabase_ConcurrentReadWrite verifies that concurrent reads and writes
// on different records do not interfere with each other.
func TestDatabase_ConcurrentReadWrite(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	db, err := database.NewSQLiteDB(":memory:", logger)
	require.NoError(t, err)
	defer db.Close()

	// Pre-populate with some records.
	for i := 0; i < 20; i++ {
		obj := &models.ManagedObject{
			ID:                uuid.New().String(),
			ResourceUID:       fmt.Sprintf("uid-rw-%d", i),
			ResourceType:      "Pod",
			ResourceName:      fmt.Sprintf("rw-pod-%d", i),
			ResourceNamespace: "default",
			AnnotationValue:   "managed",
			ClusterState:      models.ClusterStateExists,
			DetectionSource:   models.DetectionSourceWatch,
			CreatedAt:         time.Now(),
			Labels:            `{}`,
			ResourceVersion:   "1",
			FullMetadata:      "{}",
		}
		require.NoError(t, db.InsertManagedObject(obj))
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	// Writer goroutines: update cluster state.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			now := time.Now()
			uid := fmt.Sprintf("uid-rw-%d", idx)
			if updateErr := db.UpdateClusterState(uid, models.ClusterStateDeleted, &now); updateErr != nil {
				errCh <- fmt.Errorf("update failed for %s: %w", uid, updateErr)
			}
		}(i)
	}

	// Reader goroutines: query pending and active objects.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, readErr := db.GetPendingNotifications(10); readErr != nil {
				errCh <- fmt.Errorf("get pending failed: %w", readErr)
			}
			if _, readErr := db.GetAllActiveObjects("Pod"); readErr != nil {
				errCh <- fmt.Errorf("get active failed: %w", readErr)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	var errors []error
	for e := range errCh {
		errors = append(errors, e)
	}
	assert.Empty(t, errors, "concurrent read/write should not produce errors: %v", errors)
}

// TestDatabase_Recovery verifies that after closing and reopening the database,
// previously inserted records are still present. This test uses a temporary
// file rather than :memory: because in-memory databases do not persist across
// connections.
func TestDatabase_Recovery(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	// Use a temporary file for persistence.
	dbPath := t.TempDir() + "/recovery-test.db"

	// Open, insert, and close.
	db1, err := database.NewSQLiteDB(dbPath, logger)
	require.NoError(t, err)

	obj := &models.ManagedObject{
		ID:                "recovery-id-001",
		ResourceUID:       "uid-recovery-001",
		ResourceType:      "Pod",
		ResourceName:      "recovery-pod",
		ResourceNamespace: "default",
		AnnotationValue:   "managed",
		ClusterState:      models.ClusterStateExists,
		DetectionSource:   models.DetectionSourceWatch,
		CreatedAt:         time.Now(),
		Labels:            `{"env":"test"}`,
		ResourceVersion:   "99",
		FullMetadata:      `{"name":"recovery-pod"}`,
	}

	err = db1.InsertManagedObject(obj)
	require.NoError(t, err)

	err = db1.Close()
	require.NoError(t, err)

	// Reopen and verify the record is still there.
	db2, err := database.NewSQLiteDB(dbPath, logger)
	require.NoError(t, err)
	defer db2.Close()

	stored, err := db2.GetManagedObjectByID("recovery-id-001")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "recovery-pod", stored.ResourceName)
	assert.Equal(t, "uid-recovery-001", stored.ResourceUID)
	assert.Equal(t, models.ClusterStateExists, stored.ClusterState)
	assert.Equal(t, "managed", stored.AnnotationValue)
	assert.Equal(t, `{"env":"test"}`, stored.Labels)

	// Verify ping works on the new connection.
	err = db2.Ping()
	require.NoError(t, err)
}

// TestDatabase_PingAndVacuum verifies that Ping and RunIncrementalVacuum
// succeed on a healthy database.
func TestDatabase_PingAndVacuum(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	db, err := database.NewSQLiteDB(":memory:", logger)
	require.NoError(t, err)
	defer db.Close()

	err = db.Ping()
	require.NoError(t, err)

	err = db.RunIncrementalVacuum()
	require.NoError(t, err)
}

// TestDatabase_SizeReporting verifies that GetDatabaseSizeBytes returns a
// non-negative value.
func TestDatabase_SizeReporting(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	db, err := database.NewSQLiteDB(":memory:", logger)
	require.NoError(t, err)
	defer db.Close()

	size, err := db.GetDatabaseSizeBytes()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, size, int64(0), "database size should be non-negative")
}
