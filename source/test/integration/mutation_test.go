//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/bryonbaker/beacon/internal/models"
	"github.com/bryonbaker/beacon/internal/notifier"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMutation_AnnotationAdded verifies the flow when an annotation is added
// to an existing Kubernetes object (detected via an Update event). The object
// should be inserted into the database with detection_source=mutation, and a
// creation notification should be sent.
func TestMutation_AnnotationAdded(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	// Simulate what the watcher's handleUpdate would do when it detects
	// that an annotation was added: insert a new ManagedObject with
	// detection_source=mutation.
	obj := &models.ManagedObject{
		ID:                uuid.New().String(),
		ResourceUID:       "uid-mutation-add-001",
		ResourceType:      "Pod",
		ResourceName:      "mutated-pod-add",
		ResourceNamespace: "default",
		AnnotationValue:   "managed",
		ClusterState:      models.ClusterStateExists,
		DetectionSource:   models.DetectionSourceMutation,
		CreatedAt:         time.Now(),
		Labels:            `{"app":"mutated"}`,
		ResourceVersion:   "5",
		FullMetadata:      "{}",
	}

	err := env.DB.InsertManagedObject(obj)
	require.NoError(t, err)

	// Verify detection source is recorded as mutation.
	stored, err := env.DB.GetManagedObjectByUID("uid-mutation-add-001")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, models.DetectionSourceMutation, stored.DetectionSource)
	assert.Equal(t, models.ClusterStateExists, stored.ClusterState)
	assert.False(t, stored.NotifiedCreated)

	// Verify it is pending for creation notification.
	pending, err := env.DB.GetPendingNotifications(10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, obj.ID, pending[0].ID)

	// Run the notifier to deliver the creation notification.
	httpClient := &http.Client{Timeout: 5 * time.Second}
	n := notifier.NewNotifier(env.DB, httpClient, env.Config, env.Metrics, env.Logger)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	n.Start(ctx)

	// Verify the notification was sent.
	payloads := env.receivedPayloads()
	require.Len(t, payloads, 1)
	assert.Equal(t, "created", payloads[0].EventType)
	assert.Equal(t, "uid-mutation-add-001", payloads[0].Resource.UID)

	// Verify DB updated.
	stored, err = env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.True(t, stored.NotifiedCreated)
}

// TestMutation_AnnotationRemoved verifies the flow when an annotation is
// removed from an existing Kubernetes object. The watcher treats this as a
// logical deletion: it updates the cluster state to "deleted". A deletion
// notification should then be sent.
func TestMutation_AnnotationRemoved(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	// First, create an object as if it was previously tracked (annotation
	// was present, creation notification was sent).
	obj := &models.ManagedObject{
		ID:                uuid.New().String(),
		ResourceUID:       "uid-mutation-remove-001",
		ResourceType:      "Pod",
		ResourceName:      "mutated-pod-remove",
		ResourceNamespace: "default",
		AnnotationValue:   "managed",
		ClusterState:      models.ClusterStateExists,
		DetectionSource:   models.DetectionSourceWatch,
		CreatedAt:         time.Now(),
		Labels:            `{"app":"mutated"}`,
		ResourceVersion:   "3",
		FullMetadata:      "{}",
	}

	err := env.DB.InsertManagedObject(obj)
	require.NoError(t, err)

	// Mark the creation notification as sent.
	err = env.DB.UpdateNotificationStatus(obj.ID, "created", time.Now())
	require.NoError(t, err)

	// Simulate annotation removal: the watcher's handleUpdate detects the
	// annotation was removed and updates the cluster state to deleted.
	now := time.Now()
	err = env.DB.UpdateClusterState(obj.ResourceUID, models.ClusterStateDeleted, &now)
	require.NoError(t, err)

	// Verify the object is now pending deletion notification.
	stored, err := env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ClusterStateDeleted, stored.ClusterState)
	assert.True(t, stored.NotifiedCreated)
	assert.False(t, stored.NotifiedDeleted)
	assert.True(t, stored.IsPendingDeletionNotification())

	pending, err := env.DB.GetPendingNotifications(10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, obj.ID, pending[0].ID)

	// Run the notifier to deliver the deletion notification.
	httpClient := &http.Client{Timeout: 5 * time.Second}
	n := notifier.NewNotifier(env.DB, httpClient, env.Config, env.Metrics, env.Logger)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	n.Start(ctx)

	// Verify the deletion notification was sent.
	payloads := env.receivedPayloads()
	require.Len(t, payloads, 1)
	assert.Equal(t, "deleted", payloads[0].EventType)
	assert.Equal(t, "uid-mutation-remove-001", payloads[0].Resource.UID)

	// Verify DB updated.
	stored, err = env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.True(t, stored.NotifiedDeleted)
	assert.NotNil(t, stored.DeletedNotificationSentAt)
}

// TestMutation_AnnotationAddedThenRemoved verifies the full mutation lifecycle:
// annotation added (creation notification) followed by annotation removed
// (deletion notification).
func TestMutation_AnnotationAddedThenRemoved(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	// Step 1: Annotation added via mutation.
	obj := &models.ManagedObject{
		ID:                uuid.New().String(),
		ResourceUID:       "uid-mutation-lifecycle-001",
		ResourceType:      "Pod",
		ResourceName:      "lifecycle-pod",
		ResourceNamespace: "default",
		AnnotationValue:   "managed",
		ClusterState:      models.ClusterStateExists,
		DetectionSource:   models.DetectionSourceMutation,
		CreatedAt:         time.Now(),
		Labels:            `{}`,
		ResourceVersion:   "10",
		FullMetadata:      "{}",
	}

	err := env.DB.InsertManagedObject(obj)
	require.NoError(t, err)

	// Send creation notification.
	httpClient := &http.Client{Timeout: 5 * time.Second}
	n := notifier.NewNotifier(env.DB, httpClient, env.Config, env.Metrics, env.Logger)

	ctx1, cancel1 := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel1()
	n.Start(ctx1)

	payloads := env.receivedPayloads()
	require.Len(t, payloads, 1)
	assert.Equal(t, "created", payloads[0].EventType)

	// Step 2: Annotation removed via mutation.
	now := time.Now()
	err = env.DB.UpdateClusterState(obj.ResourceUID, models.ClusterStateDeleted, &now)
	require.NoError(t, err)

	env.resetReceived()

	ctx2, cancel2 := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel2()
	n.Start(ctx2)

	payloads = env.receivedPayloads()
	require.Len(t, payloads, 1)
	assert.Equal(t, "deleted", payloads[0].EventType)

	// Verify final state.
	stored, err := env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.True(t, stored.NotifiedCreated)
	assert.True(t, stored.NotifiedDeleted)
	assert.Equal(t, models.DetectionSourceMutation, stored.DetectionSource)
}
