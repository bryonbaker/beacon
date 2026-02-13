//go:build integration

package integration_test

import (
	"testing"
	"time"

	"github.com/bryonbaker/beacon/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCleanup_RespectsRetentionPeriod verifies that GetCleanupEligible only
// returns records whose deleted_at timestamp is older than the configured
// retention period, and that records deleted more recently are not eligible.
func TestCleanup_RespectsRetentionPeriod(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	retentionPeriod := 2 * time.Second

	// Create a record that was deleted well beyond the retention period.
	oldObj := &models.ManagedObject{
		ID:                uuid.New().String(),
		ResourceUID:       "uid-cleanup-old-001",
		ResourceType:      "Pod",
		ResourceName:      "old-cleanup-pod",
		ResourceNamespace: "default",
		AnnotationValue:   "managed",
		ClusterState:      models.ClusterStateExists,
		DetectionSource:   models.DetectionSourceWatch,
		CreatedAt:         time.Now().Add(-10 * time.Minute),
		Labels:            `{}`,
		ResourceVersion:   "1",
		FullMetadata:      "{}",
	}
	require.NoError(t, env.DB.InsertManagedObject(oldObj))

	// Mark creation as notified.
	require.NoError(t, env.DB.UpdateNotificationStatus(oldObj.ID, "created", time.Now()))

	// Delete the object with a timestamp far in the past (beyond retention).
	pastDeletion := time.Now().Add(-5 * time.Minute)
	require.NoError(t, env.DB.UpdateClusterState(oldObj.ResourceUID, models.ClusterStateDeleted, &pastDeletion))

	// Mark deletion as notified.
	require.NoError(t, env.DB.UpdateNotificationStatus(oldObj.ID, "deleted", time.Now()))

	// Create a record that was deleted very recently (within retention).
	recentObj := &models.ManagedObject{
		ID:                uuid.New().String(),
		ResourceUID:       "uid-cleanup-recent-001",
		ResourceType:      "Pod",
		ResourceName:      "recent-cleanup-pod",
		ResourceNamespace: "default",
		AnnotationValue:   "managed",
		ClusterState:      models.ClusterStateExists,
		DetectionSource:   models.DetectionSourceWatch,
		CreatedAt:         time.Now().Add(-1 * time.Minute),
		Labels:            `{}`,
		ResourceVersion:   "1",
		FullMetadata:      "{}",
	}
	require.NoError(t, env.DB.InsertManagedObject(recentObj))
	require.NoError(t, env.DB.UpdateNotificationStatus(recentObj.ID, "created", time.Now()))

	recentDeletion := time.Now() // Just now -- within retention period.
	require.NoError(t, env.DB.UpdateClusterState(recentObj.ResourceUID, models.ClusterStateDeleted, &recentDeletion))
	require.NoError(t, env.DB.UpdateNotificationStatus(recentObj.ID, "deleted", time.Now()))

	// Query for cleanup-eligible records.
	eligible, err := env.DB.GetCleanupEligible(retentionPeriod)
	require.NoError(t, err)

	// Only the old record should be eligible.
	require.Len(t, eligible, 1)
	assert.Equal(t, oldObj.ID, eligible[0].ID)

	// Verify the model-level check agrees.
	assert.True(t, eligible[0].IsEligibleForCleanup(retentionPeriod))

	// The recent record should NOT be eligible.
	recentStored, err := env.DB.GetManagedObjectByID(recentObj.ID)
	require.NoError(t, err)
	assert.False(t, recentStored.IsEligibleForCleanup(retentionPeriod))

	// Clean up the eligible record and verify it is removed.
	require.NoError(t, env.DB.DeleteRecord(oldObj.ID))
	_, err = env.DB.GetManagedObjectByID(oldObj.ID)
	assert.Error(t, err, "deleted record should no longer be found")
}

// TestCleanup_SkipsFailedNotifications verifies that records with
// notification_failed=true are NOT eligible for cleanup, regardless of their
// deletion age.
func TestCleanup_SkipsFailedNotifications(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	retentionPeriod := 1 * time.Second

	obj := &models.ManagedObject{
		ID:                uuid.New().String(),
		ResourceUID:       "uid-cleanup-failed-001",
		ResourceType:      "Pod",
		ResourceName:      "failed-cleanup-pod",
		ResourceNamespace: "default",
		AnnotationValue:   "managed",
		ClusterState:      models.ClusterStateExists,
		DetectionSource:   models.DetectionSourceWatch,
		CreatedAt:         time.Now().Add(-1 * time.Hour),
		Labels:            `{}`,
		ResourceVersion:   "1",
		FullMetadata:      "{}",
	}
	require.NoError(t, env.DB.InsertManagedObject(obj))

	// Mark notification as permanently failed.
	require.NoError(t, env.DB.MarkNotificationFailed(obj.ID, 400))

	// Delete the object well beyond the retention period.
	pastDeletion := time.Now().Add(-30 * time.Minute)
	require.NoError(t, env.DB.UpdateClusterState(obj.ResourceUID, models.ClusterStateDeleted, &pastDeletion))

	// The record should NOT be eligible for cleanup because notification_failed=true.
	eligible, err := env.DB.GetCleanupEligible(retentionPeriod)
	require.NoError(t, err)
	assert.Empty(t, eligible, "records with failed notifications must not be eligible for cleanup")

	// Verify via the model method as well.
	stored, err := env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.True(t, stored.NotificationFailed)
	assert.False(t, stored.IsEligibleForCleanup(retentionPeriod))
}

// TestCleanup_SkipsUnnotifiedRecords verifies that records where the deletion
// notification has not yet been sent are NOT eligible for cleanup.
func TestCleanup_SkipsUnnotifiedRecords(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	retentionPeriod := 1 * time.Second

	obj := &models.ManagedObject{
		ID:                uuid.New().String(),
		ResourceUID:       "uid-cleanup-unnotified-001",
		ResourceType:      "Pod",
		ResourceName:      "unnotified-cleanup-pod",
		ResourceNamespace: "default",
		AnnotationValue:   "managed",
		ClusterState:      models.ClusterStateExists,
		DetectionSource:   models.DetectionSourceWatch,
		CreatedAt:         time.Now().Add(-1 * time.Hour),
		Labels:            `{}`,
		ResourceVersion:   "1",
		FullMetadata:      "{}",
	}
	require.NoError(t, env.DB.InsertManagedObject(obj))

	// Send creation notification but NOT deletion notification.
	require.NoError(t, env.DB.UpdateNotificationStatus(obj.ID, "created", time.Now()))

	// Delete the object.
	pastDeletion := time.Now().Add(-30 * time.Minute)
	require.NoError(t, env.DB.UpdateClusterState(obj.ResourceUID, models.ClusterStateDeleted, &pastDeletion))

	// Without the deletion notification sent, it should not be eligible.
	eligible, err := env.DB.GetCleanupEligible(retentionPeriod)
	require.NoError(t, err)
	assert.Empty(t, eligible, "records without deletion notification must not be eligible for cleanup")

	// Now send the deletion notification.
	require.NoError(t, env.DB.UpdateNotificationStatus(obj.ID, "deleted", time.Now()))

	// Now it should be eligible (deleted_at is 30 minutes ago, beyond 1s retention).
	eligible, err = env.DB.GetCleanupEligible(retentionPeriod)
	require.NoError(t, err)
	require.Len(t, eligible, 1)
	assert.Equal(t, obj.ID, eligible[0].ID)
}

// TestCleanup_SkipsExistingRecords verifies that records still in the
// "exists" cluster state are never eligible for cleanup.
func TestCleanup_SkipsExistingRecords(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	retentionPeriod := 1 * time.Second

	obj := &models.ManagedObject{
		ID:                uuid.New().String(),
		ResourceUID:       "uid-cleanup-existing-001",
		ResourceType:      "Pod",
		ResourceName:      "existing-cleanup-pod",
		ResourceNamespace: "default",
		AnnotationValue:   "managed",
		ClusterState:      models.ClusterStateExists,
		DetectionSource:   models.DetectionSourceWatch,
		CreatedAt:         time.Now().Add(-1 * time.Hour),
		Labels:            `{}`,
		ResourceVersion:   "1",
		FullMetadata:      "{}",
	}
	require.NoError(t, env.DB.InsertManagedObject(obj))
	require.NoError(t, env.DB.UpdateNotificationStatus(obj.ID, "created", time.Now()))

	// Object is still in "exists" state -- it must never be eligible.
	eligible, err := env.DB.GetCleanupEligible(retentionPeriod)
	require.NoError(t, err)
	assert.Empty(t, eligible, "objects in 'exists' state must never be eligible for cleanup")

	// Verify via the model method.
	stored, err := env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.False(t, stored.IsEligibleForCleanup(retentionPeriod))
}

// TestCleanup_DeleteRecordAndVacuum verifies that after deleting eligible
// records and running an incremental vacuum, the database remains healthy.
func TestCleanup_DeleteRecordAndVacuum(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	// Insert and fully process an object.
	obj := newTestManagedObject("vacuum-pod", "uid-vacuum-001")
	require.NoError(t, env.DB.InsertManagedObject(obj))
	require.NoError(t, env.DB.UpdateNotificationStatus(obj.ID, "created", time.Now()))

	pastDeletion := time.Now().Add(-1 * time.Hour)
	require.NoError(t, env.DB.UpdateClusterState(obj.ResourceUID, models.ClusterStateDeleted, &pastDeletion))
	require.NoError(t, env.DB.UpdateNotificationStatus(obj.ID, "deleted", time.Now()))

	// Delete the record.
	require.NoError(t, env.DB.DeleteRecord(obj.ID))

	// Run incremental vacuum.
	err := env.DB.RunIncrementalVacuum()
	require.NoError(t, err)

	// Verify the database is still functional.
	err = env.DB.Ping()
	require.NoError(t, err)

	exists, deleted, err := env.DB.CountByState()
	require.NoError(t, err)
	assert.Equal(t, 0, exists)
	assert.Equal(t, 0, deleted)
}
