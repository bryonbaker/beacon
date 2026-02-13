//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/bryonbaker/beacon/internal/models"
	"github.com/bryonbaker/beacon/internal/notifier"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEndToEnd_CreateNotifyDeleteCleanup exercises the full lifecycle:
//  1. Insert an annotated object into the database.
//  2. Run the notifier to deliver a creation notification.
//  3. Verify the mock endpoint received the correct payload.
//  4. Mark the object as deleted and run the notifier again.
//  5. Verify a deletion notification was sent.
//  6. Verify the record becomes eligible for cleanup.
func TestEndToEnd_CreateNotifyDeleteCleanup(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	// Step 1: Insert an annotated object.
	obj := newTestManagedObject("test-pod-e2e", "uid-e2e-001")
	err := env.DB.InsertManagedObject(obj)
	require.NoError(t, err)

	// Verify it appears as pending.
	pending, err := env.DB.GetPendingNotifications(10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, obj.ID, pending[0].ID)
	assert.False(t, pending[0].NotifiedCreated)

	// Step 2: Create a notifier and process one poll cycle.
	httpClient := &http.Client{Timeout: 5 * time.Second}
	n := notifier.NewNotifier(env.DB, httpClient, env.Config, env.Metrics, env.Logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run a single poll cycle by starting and immediately cancelling after
	// a short window so the first tick fires.
	pollCtx, pollCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer pollCancel()
	n.Start(pollCtx)

	// Step 3: Verify the mock endpoint received the creation notification.
	payloads := env.receivedPayloads()
	require.Len(t, payloads, 1, "expected exactly one creation notification")
	assert.Equal(t, "created", payloads[0].EventType)
	assert.Equal(t, obj.ResourceUID, payloads[0].Resource.UID)
	assert.Equal(t, "Pod", payloads[0].Resource.Type)
	assert.Equal(t, "test-pod-e2e", payloads[0].Resource.Name)
	assert.Equal(t, "default", payloads[0].Resource.Namespace)
	assert.Equal(t, "managed", payloads[0].Resource.AnnotationValue)

	// Verify DB state updated.
	updated, err := env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.True(t, updated.NotifiedCreated)
	assert.NotNil(t, updated.CreatedNotificationSentAt)

	// Step 4: Mark the object as deleted.
	now := time.Now()
	err = env.DB.UpdateClusterState(obj.ResourceUID, models.ClusterStateDeleted, &now)
	require.NoError(t, err)

	env.resetReceived()

	// Run another poll cycle for the deletion notification.
	pollCtx2, pollCancel2 := context.WithTimeout(ctx, 500*time.Millisecond)
	defer pollCancel2()
	n.Start(pollCtx2)

	// Step 5: Verify the deletion notification.
	payloads = env.receivedPayloads()
	require.Len(t, payloads, 1, "expected exactly one deletion notification")
	assert.Equal(t, "deleted", payloads[0].EventType)
	assert.Equal(t, obj.ResourceUID, payloads[0].Resource.UID)

	// Verify DB state.
	updated, err = env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.True(t, updated.NotifiedDeleted)
	assert.NotNil(t, updated.DeletedNotificationSentAt)

	// Step 6: Verify cleanup eligibility.
	// With a very short retention period (1s), the record should become
	// eligible almost immediately. We need to wait just past the retention
	// period because deleted_at was set to "now" above.
	time.Sleep(1500 * time.Millisecond)

	eligible, err := env.DB.GetCleanupEligible(env.Config.Retention.RetentionPeriod.Duration)
	require.NoError(t, err)
	require.Len(t, eligible, 1)
	assert.Equal(t, obj.ID, eligible[0].ID)

	// Clean it up.
	err = env.DB.DeleteRecord(obj.ID)
	require.NoError(t, err)

	// Verify it is gone.
	_, err = env.DB.GetManagedObjectByID(obj.ID)
	assert.Error(t, err, "record should no longer exist after cleanup")
}

// TestEndToEnd_NoPendingNotifications verifies that the notifier does not send
// any notifications when there is nothing pending.
func TestEndToEnd_NoPendingNotifications(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	httpClient := &http.Client{Timeout: 5 * time.Second}
	n := notifier.NewNotifier(env.DB, httpClient, env.Config, env.Metrics, env.Logger)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	n.Start(ctx)

	payloads := env.receivedPayloads()
	assert.Empty(t, payloads, "no notifications should be sent when DB is empty")
}

// TestEndToEnd_MultipleObjects verifies that multiple objects are each
// notified independently.
func TestEndToEnd_MultipleObjects(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	objs := []*models.ManagedObject{
		newTestManagedObject("pod-a", "uid-multi-001"),
		newTestManagedObject("pod-b", "uid-multi-002"),
		newTestManagedObject("pod-c", "uid-multi-003"),
	}

	for _, obj := range objs {
		err := env.DB.InsertManagedObject(obj)
		require.NoError(t, err)
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	n := notifier.NewNotifier(env.DB, httpClient, env.Config, env.Metrics, env.Logger)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	n.Start(ctx)

	payloads := env.receivedPayloads()
	assert.Len(t, payloads, 3, "all three objects should receive creation notifications")

	// Verify each object was notified.
	notifiedUIDs := make(map[string]bool)
	for _, p := range payloads {
		notifiedUIDs[p.Resource.UID] = true
		assert.Equal(t, "created", p.EventType)
	}
	for _, obj := range objs {
		assert.True(t, notifiedUIDs[obj.ResourceUID], "object %s should have been notified", obj.ResourceName)
	}
}
