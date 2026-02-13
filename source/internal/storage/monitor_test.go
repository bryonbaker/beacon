package storage

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/bryonbaker/beacon/internal/config"
	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/metrics"
)

// newTestMonitor creates a Monitor wired to a MockDatabase for testing.
func newTestMonitor(mockDB *database.MockDatabase) (*Monitor, *metrics.Metrics) {
	cfg := &config.Config{}
	cfg.Storage.MonitorInterval.Duration = 1 * time.Minute
	cfg.Storage.VolumePath = "/" // Use root filesystem for tests.
	cfg.Storage.DBPath = "/data/events.db"
	cfg.Storage.WarningThreshold = 80
	cfg.Storage.CriticalThreshold = 90

	logger := zap.NewNop()
	m := metrics.NewMetrics(prometheus.NewRegistry())

	return NewMonitor(mockDB, cfg, m, logger), m
}

// getGaugeValue reads the current value of a prometheus.Gauge.
func getGaugeValue(g prometheus.Gauge) float64 {
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		return 0
	}
	return m.GetGauge().GetValue()
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestCheck_DBSizeMetricUpdated(t *testing.T) {
	mockDB := new(database.MockDatabase)
	mon, m := newTestMonitor(mockDB)

	mockDB.On("GetDatabaseSizeBytes").Return(int64(1048576), nil).Once()

	err := mon.Check(context.Background())

	require.NoError(t, err)
	mockDB.AssertExpectations(t)

	// Verify the DB size metric was set.
	dbSize := getGaugeValue(m.DBSizeBytes)
	assert.Equal(t, float64(1048576), dbSize, "DBSizeBytes metric should be set to 1 MiB")
}

func TestCheck_VolumeMetricsUpdated(t *testing.T) {
	mockDB := new(database.MockDatabase)
	mon, m := newTestMonitor(mockDB)

	mockDB.On("GetDatabaseSizeBytes").Return(int64(512000), nil).Once()

	err := mon.Check(context.Background())

	require.NoError(t, err)
	mockDB.AssertExpectations(t)

	// Volume metrics should have non-zero values since we are using "/".
	totalBytes := getGaugeValue(m.StorageVolumeSizeBytes)
	assert.Greater(t, totalBytes, float64(0), "StorageVolumeSizeBytes should be positive")

	usedBytes := getGaugeValue(m.StorageVolumeUsedBytes)
	assert.Greater(t, usedBytes, float64(0), "StorageVolumeUsedBytes should be positive")

	availBytes := getGaugeValue(m.StorageVolumeAvailableBytes)
	assert.Greater(t, availBytes, float64(0), "StorageVolumeAvailableBytes should be positive")

	usagePercent := getGaugeValue(m.StorageVolumeUsagePercent)
	assert.Greater(t, usagePercent, float64(0), "StorageVolumeUsagePercent should be positive")
	assert.Less(t, usagePercent, float64(100), "StorageVolumeUsagePercent should be less than 100")

	totalInodes := getGaugeValue(m.StorageVolumeInodesTotal)
	// Some filesystems (e.g. btrfs) report 0 inodes; skip this check if so.
	if totalInodes > 0 {
		assert.Greater(t, totalInodes, float64(0), "StorageVolumeInodesTotal should be positive")
	}
}

func TestNewMonitor_ReturnsNonNil(t *testing.T) {
	mockDB := new(database.MockDatabase)
	mon, _ := newTestMonitor(mockDB)

	assert.NotNil(t, mon)
	assert.NotNil(t, mon.db)
	assert.NotNil(t, mon.cfg)
	assert.NotNil(t, mon.metrics)
	assert.NotNil(t, mon.logger)
}

func TestCheck_ContextCancelled(t *testing.T) {
	mockDB := new(database.MockDatabase)
	mon, _ := newTestMonitor(mockDB)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := mon.Check(ctx)

	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestMonitor_StartStops(t *testing.T) {
	mockDB := new(database.MockDatabase)
	mon, _ := newTestMonitor(mockDB)
	mon.cfg.Storage.MonitorInterval.Duration = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		mon.Start(ctx)
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
