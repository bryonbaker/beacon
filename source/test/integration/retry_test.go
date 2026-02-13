//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bryonbaker/beacon/internal/models"
	"github.com/bryonbaker/beacon/internal/notifier"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRetry_ServerErrorThenSuccess verifies that when the mock endpoint
// returns HTTP 500 on the first request and HTTP 200 on subsequent requests,
// the notifier retries with backoff and eventually succeeds.
func TestRetry_ServerErrorThenSuccess(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	// Track call count to control response behaviour.
	var callCount int32

	retryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&callCount, 1)
		if count <= 2 {
			// First two attempts: return 500 (retriable).
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Subsequent attempts: return 200 (success).
		var payload models.NotificationPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		env.mu.Lock()
		env.received = append(env.received, payload)
		env.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer retryServer.Close()

	// Update config to point at the retry server.
	env.Config.Endpoint.URL = retryServer.URL
	// Use a fast poll interval so retries happen quickly.
	env.Config.Worker.PollInterval.Duration = 100 * time.Millisecond

	obj := newTestManagedObject("retry-pod", "uid-retry-001")
	err := env.DB.InsertManagedObject(obj)
	require.NoError(t, err)

	httpClient := &http.Client{Timeout: 5 * time.Second}
	n := notifier.NewNotifier(env.DB, httpClient, env.Config, env.Metrics, env.Logger)

	// Allow enough time for multiple poll cycles.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	n.Start(ctx)

	// Verify that the endpoint was eventually called successfully.
	finalCount := atomic.LoadInt32(&callCount)
	assert.GreaterOrEqual(t, finalCount, int32(3), "expected at least 3 calls (2 failures + 1 success)")

	// Verify the notification was marked as sent.
	updated, err := env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.True(t, updated.NotifiedCreated, "notification should be marked as sent after retry succeeds")
	assert.NotNil(t, updated.CreatedNotificationSentAt)

	// Verify notification attempts were incremented.
	assert.GreaterOrEqual(t, updated.NotificationAttempts, 2, "at least 2 retry attempts should be recorded")
}

// TestRetry_NonRetriableError verifies that when the mock endpoint returns a
// non-retriable error (HTTP 400), the notification is marked as permanently
// failed and is not retried. The record is also not eligible for cleanup.
func TestRetry_NonRetriableError(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	var callCount int32

	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		// Always return 400 Bad Request (non-retriable).
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer failServer.Close()

	env.Config.Endpoint.URL = failServer.URL
	env.Config.Worker.PollInterval.Duration = 100 * time.Millisecond

	obj := newTestManagedObject("fail-pod", "uid-fail-001")
	err := env.DB.InsertManagedObject(obj)
	require.NoError(t, err)

	httpClient := &http.Client{Timeout: 5 * time.Second}
	n := notifier.NewNotifier(env.DB, httpClient, env.Config, env.Metrics, env.Logger)

	// Run for enough time to see that it does NOT retry.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	n.Start(ctx)

	// The non-retriable error should be processed exactly once and then the
	// object should be marked as failed, so subsequent polls skip it.
	finalCount := atomic.LoadInt32(&callCount)
	assert.Equal(t, int32(1), finalCount, "non-retriable error should only be attempted once")

	// Verify the record is marked as failed.
	updated, err := env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.True(t, updated.NotificationFailed, "notification should be marked as failed")
	assert.Equal(t, http.StatusBadRequest, updated.NotificationFailedCode)
	assert.False(t, updated.NotifiedCreated, "notification should NOT be marked as sent")

	// Verify it is excluded from pending notifications.
	pending, err := env.DB.GetPendingNotifications(10)
	require.NoError(t, err)
	assert.Empty(t, pending, "failed notification should not appear in pending list")

	// Verify it is NOT eligible for cleanup (notification_failed = true).
	now := time.Now()
	pastRetention := now.Add(-2 * env.Config.Retention.RetentionPeriod.Duration)
	err = env.DB.UpdateClusterState(obj.ResourceUID, models.ClusterStateDeleted, &pastRetention)
	require.NoError(t, err)

	eligible, err := env.DB.GetCleanupEligible(env.Config.Retention.RetentionPeriod.Duration)
	require.NoError(t, err)
	assert.Empty(t, eligible, "failed notifications must be exempt from cleanup")
}

// TestRetry_TooManyRequests verifies that HTTP 429 is treated as a retriable
// error and the notifier retries.
func TestRetry_TooManyRequests(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	var callCount int32

	rateLimitServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&callCount, 1)
		if count <= 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer rateLimitServer.Close()

	env.Config.Endpoint.URL = rateLimitServer.URL
	env.Config.Worker.PollInterval.Duration = 100 * time.Millisecond

	obj := newTestManagedObject("rate-pod", "uid-rate-001")
	err := env.DB.InsertManagedObject(obj)
	require.NoError(t, err)

	httpClient := &http.Client{Timeout: 5 * time.Second}
	n := notifier.NewNotifier(env.DB, httpClient, env.Config, env.Metrics, env.Logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	n.Start(ctx)

	finalCount := atomic.LoadInt32(&callCount)
	assert.GreaterOrEqual(t, finalCount, int32(2), "429 should trigger retry")

	updated, err := env.DB.GetManagedObjectByID(obj.ID)
	require.NoError(t, err)
	assert.True(t, updated.NotifiedCreated, "notification should eventually succeed")
}
