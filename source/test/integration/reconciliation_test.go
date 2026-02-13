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

// TestReconciliation_MissedCreation simulates the scenario where an annotated
// object exists in the cluster but was never recorded in the database (e.g.
// the service was down when the object was created). In a full deployment,
// the reconciler would discover this object by listing the cluster and
// inserting it with detection_source=reconciliation.
//
// This test verifies the database layer supports the reconciliation pattern:
// insert an object with detection_source=reconciliation, verify it appears in
// pending notifications, and confirm the detection source is recorded
// correctly.
func TestReconciliation_MissedCreation(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	// Simulate the reconciler discovering an object in the cluster that is
	// not in the database. The reconciler would create this record.
	obj := &models.ManagedObject{
		ID:                uuid.New().String(),
		ResourceUID:       "uid-recon-create-001",
		ResourceType:      "Pod",
		ResourceName:      "missed-create-pod",
		ResourceNamespace: "default",
		AnnotationValue:   "managed",
		ClusterState:      models.ClusterStateExists,
		DetectionSource:   models.DetectionSourceReconciliation,
		CreatedAt:         time.Now(),
		Labels:            `{"app":"reconciled"}`,
		ResourceVersion:   "42",
		FullMetadata:      "{}",
	}

	err := env.DB.InsertManagedObject(obj)
	require.NoError(t, err)

	// Verify the object is in the database with the correct detection source.
	stored, err := env.DB.GetManagedObjectByUID("uid-recon-create-001")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, models.DetectionSourceReconciliation, stored.DetectionSource)
	assert.Equal(t, models.ClusterStateExists, stored.ClusterState)
	assert.False(t, stored.NotifiedCreated)

	// Verify it appears in pending notifications so the notifier will pick
	// it up.
	pending, err := env.DB.GetPendingNotifications(10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, obj.ID, pending[0].ID)
	assert.True(t, pending[0].IsPendingCreationNotification())

	// Verify the last_reconciled field can be set.
	reconTime := time.Now()
	err = env.DB.UpdateLastReconciled(obj.ID, reconTime)
	require.NoError(t, err)

	stored, err = env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.LastReconciled)
	assert.WithinDuration(t, reconTime, *stored.LastReconciled, 2*time.Second)
}

// TestReconciliation_MissedDeletion simulates the scenario where an object
// exists in the database in the "exists" state, but the cluster reports that
// it has been deleted (e.g. the service was down when the object was removed).
// The reconciler would detect the drift and update the cluster state.
func TestReconciliation_MissedDeletion(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	// Insert an object as if it was previously detected.
	obj := newTestManagedObject("missed-delete-pod", "uid-recon-delete-001")
	err := env.DB.InsertManagedObject(obj)
	require.NoError(t, err)

	// Mark the creation notification as sent (so the notifier does not
	// re-send a creation event).
	err = env.DB.UpdateNotificationStatus(obj.ID, "created", time.Now())
	require.NoError(t, err)

	// Verify current state is "exists" and notified.
	stored, err := env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ClusterStateExists, stored.ClusterState)
	assert.True(t, stored.NotifiedCreated)

	// Simulate the reconciler discovering the object is no longer in the
	// cluster and updating its state to "deleted".
	deletedAt := time.Now()
	err = env.DB.UpdateClusterState(obj.ResourceUID, models.ClusterStateDeleted, &deletedAt)
	require.NoError(t, err)

	// Verify the state change.
	stored, err = env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ClusterStateDeleted, stored.ClusterState)
	assert.NotNil(t, stored.DeletedAt)
	assert.False(t, stored.NotifiedDeleted)

	// Verify it now appears in pending notifications for deletion.
	pending, err := env.DB.GetPendingNotifications(10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, obj.ID, pending[0].ID)
	assert.True(t, pending[0].IsPendingDeletionNotification())
}

// TestReconciliation_ActiveObjectsQuery verifies that GetAllActiveObjects
// returns only objects in the "exists" state for the specified resource type,
// which is what the reconciler uses to compare against the cluster.
func TestReconciliation_ActiveObjectsQuery(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	// Insert several objects of different types and states.
	pod1 := newTestManagedObject("active-pod-1", "uid-active-001")
	pod1.ResourceType = "Pod"
	require.NoError(t, env.DB.InsertManagedObject(pod1))

	pod2 := newTestManagedObject("active-pod-2", "uid-active-002")
	pod2.ResourceType = "Pod"
	require.NoError(t, env.DB.InsertManagedObject(pod2))

	cm1 := newTestManagedObject("active-cm-1", "uid-active-003")
	cm1.ResourceType = "ConfigMap"
	require.NoError(t, env.DB.InsertManagedObject(cm1))

	// Delete one of the pods.
	deletedAt := time.Now()
	require.NoError(t, env.DB.UpdateClusterState(pod2.ResourceUID, models.ClusterStateDeleted, &deletedAt))

	// Query active objects for "Pod" type.
	activePods, err := env.DB.GetAllActiveObjects("Pod")
	require.NoError(t, err)
	assert.Len(t, activePods, 1, "only the non-deleted pod should be returned")
	assert.Equal(t, "active-pod-1", activePods[0].ResourceName)

	// Query active objects for "ConfigMap" type.
	activeCMs, err := env.DB.GetAllActiveObjects("ConfigMap")
	require.NoError(t, err)
	assert.Len(t, activeCMs, 1)
	assert.Equal(t, "active-cm-1", activeCMs[0].ResourceName)

	// Query for a type with no objects.
	activeOther, err := env.DB.GetAllActiveObjects("Deployment")
	require.NoError(t, err)
	assert.Empty(t, activeOther)
}
