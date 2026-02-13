package cleaner

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/bryonbaker/beacon/internal/config"
	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/metrics"
	"github.com/bryonbaker/beacon/internal/models"
)

// newTestCleaner creates a Cleaner wired to a MockDatabase for testing.
func newTestCleaner(mockDB *database.MockDatabase) *Cleaner {
	cfg := &config.Config{}
	cfg.Retention.Enabled = true
	cfg.Retention.CleanupInterval.Duration = 1 * time.Hour
	cfg.Retention.RetentionPeriod.Duration = 48 * time.Hour

	logger := zap.NewNop()
	m := metrics.NewMetrics(prometheus.NewRegistry())

	return NewCleaner(mockDB, cfg, m, logger)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestCleanup_DeletesEligibleRecords(t *testing.T) {
	mockDB := new(database.MockDatabase)
	c := newTestCleaner(mockDB)

	deletedAt := time.Now().Add(-72 * time.Hour) // 72 hours ago, beyond 48h retention.
	eligible := []*models.ManagedObject{
		{
			ID:             "rec-1",
			ResourceUID:    "uid-1",
			ResourceType:   "Pod",
			ResourceName:   "old-pod-1",
			ClusterState:   models.ClusterStateDeleted,
			NotifiedDeleted: true,
			DeletedAt:      &deletedAt,
		},
		{
			ID:             "rec-2",
			ResourceUID:    "uid-2",
			ResourceType:   "Pod",
			ResourceName:   "old-pod-2",
			ClusterState:   models.ClusterStateDeleted,
			NotifiedDeleted: true,
			DeletedAt:      &deletedAt,
		},
	}

	mockDB.On("GetCleanupEligible", 48*time.Hour).Return(eligible, nil).Once()
	mockDB.On("DeleteRecord", "rec-1").Return(nil).Once()
	mockDB.On("DeleteRecord", "rec-2").Return(nil).Once()
	mockDB.On("RunIncrementalVacuum").Return(nil).Once()

	err := c.Cleanup(context.Background())

	require.NoError(t, err)
	mockDB.AssertExpectations(t)
}

func TestCleanup_VacuumCalledAfterDeletion(t *testing.T) {
	mockDB := new(database.MockDatabase)
	c := newTestCleaner(mockDB)

	deletedAt := time.Now().Add(-72 * time.Hour)
	eligible := []*models.ManagedObject{
		{
			ID:             "rec-v",
			ResourceUID:    "uid-v",
			ResourceType:   "Pod",
			ClusterState:   models.ClusterStateDeleted,
			NotifiedDeleted: true,
			DeletedAt:      &deletedAt,
		},
	}

	mockDB.On("GetCleanupEligible", 48*time.Hour).Return(eligible, nil).Once()
	mockDB.On("DeleteRecord", "rec-v").Return(nil).Once()
	mockDB.On("RunIncrementalVacuum").Return(nil).Once()

	err := c.Cleanup(context.Background())

	require.NoError(t, err)
	// Verify that vacuum was called (via AssertExpectations).
	mockDB.AssertExpectations(t)
	mockDB.AssertCalled(t, "RunIncrementalVacuum")
}

func TestCleanup_NoEligibleRecords_NoOp(t *testing.T) {
	mockDB := new(database.MockDatabase)
	c := newTestCleaner(mockDB)

	mockDB.On("GetCleanupEligible", 48*time.Hour).Return([]*models.ManagedObject{}, nil).Once()

	err := c.Cleanup(context.Background())

	require.NoError(t, err)
	mockDB.AssertExpectations(t)
	// DeleteRecord and RunIncrementalVacuum should NOT be called.
	mockDB.AssertNotCalled(t, "DeleteRecord", mock.Anything)
	mockDB.AssertNotCalled(t, "RunIncrementalVacuum")
}

func TestNewCleaner_ReturnsNonNil(t *testing.T) {
	mockDB := new(database.MockDatabase)
	c := newTestCleaner(mockDB)

	assert.NotNil(t, c)
	assert.NotNil(t, c.db)
	assert.NotNil(t, c.cfg)
	assert.NotNil(t, c.metrics)
	assert.NotNil(t, c.logger)
}

func TestCleanup_ContextCancellation(t *testing.T) {
	mockDB := new(database.MockDatabase)
	c := newTestCleaner(mockDB)

	// Use a very short cleanup interval for the Start test.
	c.cfg.Retention.CleanupInterval.Duration = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		c.Start(ctx)
		close(done)
	}()

	// Cancel after a short delay.
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Start returned as expected.
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}
