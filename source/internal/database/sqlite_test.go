package database

import (
	"sync"
	"testing"
	"time"

	"github.com/bryonbaker/beacon/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newTestDB creates an in-memory SQLite database for testing.
func newTestDB(t *testing.T) *SQLiteDB {
	t.Helper()
	logger := zap.NewNop()
	db, err := NewSQLiteDB(":memory:", logger)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// newTestObject returns a minimal ManagedObject suitable for test insertion.
func newTestObject(id, uid string) *models.ManagedObject {
	return &models.ManagedObject{
		ID:                id,
		ResourceUID:       uid,
		ResourceType:      "Deployment",
		ResourceName:      "my-app",
		ResourceNamespace: "default",
		AnnotationValue:   "true",
		ClusterState:      models.ClusterStateExists,
		DetectionSource:   models.DetectionSourceWatch,
		CreatedAt:         time.Now().Truncate(time.Second),
	}
}

// --------------------------------------------------------------------------
// Insert / Retrieve round-trip
// --------------------------------------------------------------------------

func TestInsertAndGetByUID(t *testing.T) {
	db := newTestDB(t)
	obj := newTestObject("id-1", "uid-1")

	err := db.InsertManagedObject(obj)
	require.NoError(t, err)

	got, err := db.GetManagedObjectByUID("uid-1")
	require.NoError(t, err)

	assert.Equal(t, obj.ID, got.ID)
	assert.Equal(t, obj.ResourceUID, got.ResourceUID)
	assert.Equal(t, obj.ResourceType, got.ResourceType)
	assert.Equal(t, obj.ResourceName, got.ResourceName)
	assert.Equal(t, obj.ResourceNamespace, got.ResourceNamespace)
	assert.Equal(t, obj.AnnotationValue, got.AnnotationValue)
	assert.Equal(t, obj.ClusterState, got.ClusterState)
	assert.Equal(t, obj.DetectionSource, got.DetectionSource)
	assert.True(t, obj.CreatedAt.Equal(got.CreatedAt), "created_at mismatch")
	assert.Nil(t, got.DeletedAt)
	assert.False(t, got.NotifiedCreated)
	assert.False(t, got.NotifiedDeleted)
	assert.False(t, got.NotificationFailed)
	assert.Equal(t, 0, got.NotificationAttempts)
}

func TestInsertAndGetByID(t *testing.T) {
	db := newTestDB(t)
	obj := newTestObject("id-2", "uid-2")

	require.NoError(t, db.InsertManagedObject(obj))

	got, err := db.GetManagedObjectByID("id-2")
	require.NoError(t, err)
	assert.Equal(t, obj.ResourceUID, got.ResourceUID)
}

// --------------------------------------------------------------------------
// State updates
// --------------------------------------------------------------------------

func TestUpdateClusterState(t *testing.T) {
	db := newTestDB(t)
	obj := newTestObject("id-3", "uid-3")
	require.NoError(t, db.InsertManagedObject(obj))

	now := time.Now().Truncate(time.Second)
	err := db.UpdateClusterState("uid-3", models.ClusterStateDeleted, &now)
	require.NoError(t, err)

	got, err := db.GetManagedObjectByUID("uid-3")
	require.NoError(t, err)
	assert.Equal(t, models.ClusterStateDeleted, got.ClusterState)
	require.NotNil(t, got.DeletedAt)
	assert.True(t, now.Equal(*got.DeletedAt), "deleted_at mismatch")
}

func TestUpdateNotificationStatusCreated(t *testing.T) {
	db := newTestDB(t)
	obj := newTestObject("id-4", "uid-4")
	require.NoError(t, db.InsertManagedObject(obj))

	sentAt := time.Now().Truncate(time.Second)
	err := db.UpdateNotificationStatus("id-4", "created", sentAt)
	require.NoError(t, err)

	got, err := db.GetManagedObjectByID("id-4")
	require.NoError(t, err)
	assert.True(t, got.NotifiedCreated)
	require.NotNil(t, got.CreatedNotificationSentAt)
	assert.True(t, sentAt.Equal(*got.CreatedNotificationSentAt))
}

func TestUpdateNotificationStatusDeleted(t *testing.T) {
	db := newTestDB(t)
	obj := newTestObject("id-5", "uid-5")
	require.NoError(t, db.InsertManagedObject(obj))

	sentAt := time.Now().Truncate(time.Second)
	err := db.UpdateNotificationStatus("id-5", "deleted", sentAt)
	require.NoError(t, err)

	got, err := db.GetManagedObjectByID("id-5")
	require.NoError(t, err)
	assert.True(t, got.NotifiedDeleted)
	require.NotNil(t, got.DeletedNotificationSentAt)
	assert.True(t, sentAt.Equal(*got.DeletedNotificationSentAt))
}

func TestUpdateNotificationStatusInvalidType(t *testing.T) {
	db := newTestDB(t)
	obj := newTestObject("id-inv", "uid-inv")
	require.NoError(t, db.InsertManagedObject(obj))

	err := db.UpdateNotificationStatus("id-inv", "unknown", time.Now())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown event type")
}

// --------------------------------------------------------------------------
// Pending notification query
// --------------------------------------------------------------------------

func TestGetPendingNotifications(t *testing.T) {
	db := newTestDB(t)

	// Object 1: not notified at all -> pending
	obj1 := newTestObject("id-p1", "uid-p1")
	require.NoError(t, db.InsertManagedObject(obj1))

	// Object 2: creation notified, still exists -> NOT pending
	obj2 := newTestObject("id-p2", "uid-p2")
	require.NoError(t, db.InsertManagedObject(obj2))
	require.NoError(t, db.UpdateNotificationStatus("id-p2", "created", time.Now()))

	// Object 3: creation notified, deleted but not deletion-notified -> pending
	obj3 := newTestObject("id-p3", "uid-p3")
	require.NoError(t, db.InsertManagedObject(obj3))
	require.NoError(t, db.UpdateNotificationStatus("id-p3", "created", time.Now()))
	now := time.Now()
	require.NoError(t, db.UpdateClusterState("uid-p3", models.ClusterStateDeleted, &now))

	// Object 4: notification permanently failed -> NOT pending
	obj4 := newTestObject("id-p4", "uid-p4")
	require.NoError(t, db.InsertManagedObject(obj4))
	require.NoError(t, db.MarkNotificationFailed("id-p4", 500))

	pending, err := db.GetPendingNotifications(10)
	require.NoError(t, err)

	ids := make([]string, len(pending))
	for i, p := range pending {
		ids[i] = p.ID
	}
	assert.Contains(t, ids, "id-p1")
	assert.NotContains(t, ids, "id-p2")
	assert.Contains(t, ids, "id-p3")
	assert.NotContains(t, ids, "id-p4")
}

func TestGetPendingNotificationsLimit(t *testing.T) {
	db := newTestDB(t)

	for i := 0; i < 5; i++ {
		obj := newTestObject("id-lim-"+string(rune('a'+i)), "uid-lim-"+string(rune('a'+i)))
		require.NoError(t, db.InsertManagedObject(obj))
	}

	pending, err := db.GetPendingNotifications(3)
	require.NoError(t, err)
	assert.Len(t, pending, 3)
}

// --------------------------------------------------------------------------
// Cleanup eligibility
// --------------------------------------------------------------------------

func TestGetCleanupEligible(t *testing.T) {
	db := newTestDB(t)

	// Eligible: deleted > 1 hour ago, deletion notified, not failed
	obj1 := newTestObject("id-c1", "uid-c1")
	require.NoError(t, db.InsertManagedObject(obj1))
	past := time.Now().Add(-2 * time.Hour)
	require.NoError(t, db.UpdateClusterState("uid-c1", models.ClusterStateDeleted, &past))
	require.NoError(t, db.UpdateNotificationStatus("id-c1", "deleted", time.Now()))

	// NOT eligible: deleted recently
	obj2 := newTestObject("id-c2", "uid-c2")
	require.NoError(t, db.InsertManagedObject(obj2))
	recent := time.Now()
	require.NoError(t, db.UpdateClusterState("uid-c2", models.ClusterStateDeleted, &recent))
	require.NoError(t, db.UpdateNotificationStatus("id-c2", "deleted", time.Now()))

	// NOT eligible: still exists
	obj3 := newTestObject("id-c3", "uid-c3")
	require.NoError(t, db.InsertManagedObject(obj3))

	// NOT eligible: notification failed
	obj4 := newTestObject("id-c4", "uid-c4")
	require.NoError(t, db.InsertManagedObject(obj4))
	require.NoError(t, db.UpdateClusterState("uid-c4", models.ClusterStateDeleted, &past))
	require.NoError(t, db.UpdateNotificationStatus("id-c4", "deleted", time.Now()))
	require.NoError(t, db.MarkNotificationFailed("id-c4", 403))

	eligible, err := db.GetCleanupEligible(1 * time.Hour)
	require.NoError(t, err)

	ids := make([]string, len(eligible))
	for i, e := range eligible {
		ids[i] = e.ID
	}
	assert.Contains(t, ids, "id-c1")
	assert.NotContains(t, ids, "id-c2")
	assert.NotContains(t, ids, "id-c3")
	assert.NotContains(t, ids, "id-c4")
}

// --------------------------------------------------------------------------
// Mark notification failed
// --------------------------------------------------------------------------

func TestMarkNotificationFailed(t *testing.T) {
	db := newTestDB(t)
	obj := newTestObject("id-f1", "uid-f1")
	require.NoError(t, db.InsertManagedObject(obj))

	require.NoError(t, db.MarkNotificationFailed("id-f1", 502))

	got, err := db.GetManagedObjectByID("id-f1")
	require.NoError(t, err)
	assert.True(t, got.NotificationFailed)
	assert.Equal(t, 502, got.NotificationFailedCode)
}

// --------------------------------------------------------------------------
// Increment notification attempts
// --------------------------------------------------------------------------

func TestIncrementNotificationAttempts(t *testing.T) {
	db := newTestDB(t)
	obj := newTestObject("id-a1", "uid-a1")
	require.NoError(t, db.InsertManagedObject(obj))

	require.NoError(t, db.IncrementNotificationAttempts("id-a1"))
	require.NoError(t, db.IncrementNotificationAttempts("id-a1"))
	require.NoError(t, db.IncrementNotificationAttempts("id-a1"))

	got, err := db.GetManagedObjectByID("id-a1")
	require.NoError(t, err)
	assert.Equal(t, 3, got.NotificationAttempts)
	assert.NotNil(t, got.LastNotificationAttempt)
}

// --------------------------------------------------------------------------
// Delete record
// --------------------------------------------------------------------------

func TestDeleteRecord(t *testing.T) {
	db := newTestDB(t)
	obj := newTestObject("id-d1", "uid-d1")
	require.NoError(t, db.InsertManagedObject(obj))

	require.NoError(t, db.DeleteRecord("id-d1"))

	_, err := db.GetManagedObjectByID("id-d1")
	assert.Error(t, err, "expected error when fetching deleted record")
}

// --------------------------------------------------------------------------
// Count by state
// --------------------------------------------------------------------------

func TestCountByState(t *testing.T) {
	db := newTestDB(t)

	// Insert 3 objects in "exists" state
	for _, suffix := range []string{"a", "b", "c"} {
		obj := newTestObject("id-cnt-"+suffix, "uid-cnt-"+suffix)
		require.NoError(t, db.InsertManagedObject(obj))
	}

	// Mark one as deleted
	now := time.Now()
	require.NoError(t, db.UpdateClusterState("uid-cnt-c", models.ClusterStateDeleted, &now))

	exists, deleted, err := db.CountByState()
	require.NoError(t, err)
	assert.Equal(t, 2, exists)
	assert.Equal(t, 1, deleted)
}

func TestCountByStateEmpty(t *testing.T) {
	db := newTestDB(t)

	exists, deleted, err := db.CountByState()
	require.NoError(t, err)
	assert.Equal(t, 0, exists)
	assert.Equal(t, 0, deleted)
}

// --------------------------------------------------------------------------
// Concurrent access
// --------------------------------------------------------------------------

func TestConcurrentAccess(t *testing.T) {
	db := newTestDB(t)

	const goroutines = 10
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*2)

	// Insert objects concurrently
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			id := "id-conc-" + string(rune('A'+idx))
			uid := "uid-conc-" + string(rune('A'+idx))
			obj := newTestObject(id, uid)
			if err := db.InsertManagedObject(obj); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()

	// Read objects concurrently
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			uid := "uid-conc-" + string(rune('A'+idx))
			if _, err := db.GetManagedObjectByUID(uid); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent operation failed: %v", err)
	}
}

// --------------------------------------------------------------------------
// GetAllActiveObjects
// --------------------------------------------------------------------------

func TestGetAllActiveObjects(t *testing.T) {
	db := newTestDB(t)

	// Two Deployments in "exists"
	obj1 := newTestObject("id-act1", "uid-act1")
	obj1.ResourceType = "Deployment"
	require.NoError(t, db.InsertManagedObject(obj1))

	obj2 := newTestObject("id-act2", "uid-act2")
	obj2.ResourceType = "Deployment"
	require.NoError(t, db.InsertManagedObject(obj2))

	// One StatefulSet in "exists"
	obj3 := newTestObject("id-act3", "uid-act3")
	obj3.ResourceType = "StatefulSet"
	require.NoError(t, db.InsertManagedObject(obj3))

	// One Deployment that is deleted
	obj4 := newTestObject("id-act4", "uid-act4")
	obj4.ResourceType = "Deployment"
	require.NoError(t, db.InsertManagedObject(obj4))
	now := time.Now()
	require.NoError(t, db.UpdateClusterState("uid-act4", models.ClusterStateDeleted, &now))

	active, err := db.GetAllActiveObjects("Deployment")
	require.NoError(t, err)
	assert.Len(t, active, 2)
}

// --------------------------------------------------------------------------
// UpdateLastReconciled
// --------------------------------------------------------------------------

func TestUpdateLastReconciled(t *testing.T) {
	db := newTestDB(t)
	obj := newTestObject("id-rec1", "uid-rec1")
	require.NoError(t, db.InsertManagedObject(obj))

	now := time.Now().Truncate(time.Second)
	require.NoError(t, db.UpdateLastReconciled("id-rec1", now))

	got, err := db.GetManagedObjectByID("id-rec1")
	require.NoError(t, err)
	require.NotNil(t, got.LastReconciled)
	assert.True(t, now.Equal(*got.LastReconciled))
}

// --------------------------------------------------------------------------
// Ping and database size
// --------------------------------------------------------------------------

func TestPing(t *testing.T) {
	db := newTestDB(t)
	assert.NoError(t, db.Ping())
}

func TestGetDatabaseSizeBytes(t *testing.T) {
	db := newTestDB(t)

	size, err := db.GetDatabaseSizeBytes()
	require.NoError(t, err)
	assert.Greater(t, size, int64(0))
}

// --------------------------------------------------------------------------
// RunIncrementalVacuum
// --------------------------------------------------------------------------

func TestRunIncrementalVacuum(t *testing.T) {
	db := newTestDB(t)
	assert.NoError(t, db.RunIncrementalVacuum())
}
