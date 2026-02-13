package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewMetricsDoesNotPanic verifies that creating metrics against a fresh
// registry completes without panicking.
func TestNewMetricsDoesNotPanic(t *testing.T) {
	reg := prometheus.NewRegistry()
	assert.NotPanics(t, func() {
		m := NewMetrics(reg)
		require.NotNil(t, m)
	})
}

// TestMetricsCanBeIncremented verifies that representative metrics from each
// category can be used after registration.
func TestMetricsCanBeIncremented(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	// Event detection
	m.EventsTotal.WithLabelValues("Deployment", "created", "default", "watch").Inc()
	m.EventsMissedTotal.WithLabelValues("Deployment", "reconnect").Inc()
	m.ConnectionStatus.WithLabelValues("Deployment").Set(1)
	m.ReconnectsTotal.WithLabelValues("Deployment", "timeout").Inc()
	m.LastEventTimestamp.WithLabelValues("Deployment").Set(1234567890)
	m.AnnotationMutationsTotal.WithLabelValues("Deployment", "added", "default").Inc()

	// Notifications
	m.NotificationsSentTotal.WithLabelValues("Deployment", "created", "success").Inc()
	m.NotificationsPendingTotal.WithLabelValues("Deployment", "created").Set(5)
	m.NotificationDuration.WithLabelValues("Deployment", "created", "success").Observe(0.25)
	m.NotificationLatency.WithLabelValues("Deployment", "created").Observe(10)
	m.NotificationAttemptsTotal.WithLabelValues("Deployment", "created").Observe(2)
	m.NotificationRetryBackoff.WithLabelValues("1").Observe(1.5)
	m.NotificationMaxRetriesExceeded.WithLabelValues("Deployment", "created").Inc()
	m.NotificationNonRetriableFailures.WithLabelValues("Deployment", "created", "400").Inc()

	// Endpoint health
	m.EndpointUp.Set(1)
	m.EndpointConsecutiveFailures.Set(0)
	m.EndpointLastSuccess.Set(1234567890)

	// Reconciliation
	m.ReconciliationRunsTotal.WithLabelValues("success").Inc()
	m.ReconciliationDuration.Observe(15.5)
	m.ReconciliationObjectsProcessed.WithLabelValues("Deployment", "created").Inc()
	m.ReconciliationDriftDetected.WithLabelValues("Deployment", "missing").Inc()

	// Cleanup
	m.CleanupRunsTotal.WithLabelValues("success").Inc()
	m.CleanupDuration.Observe(2.3)
	m.CleanupRecordsDeleted.Inc()
	m.CleanupRecordsEligible.Set(42)
	m.CleanupOldestRecordAge.Set(86400)

	// Database
	m.DBSizeBytes.Set(1048576)
	m.DBRowsTotal.WithLabelValues("managed_objects", "exists").Set(100)
	m.DBOperationDuration.WithLabelValues("insert").Observe(0.003)
	m.DBOperationErrors.WithLabelValues("insert", "constraint").Inc()

	// Storage
	m.StorageVolumeSizeBytes.Set(10737418240)
	m.StorageVolumeUsedBytes.Set(5368709120)
	m.StorageVolumeAvailableBytes.Set(5368709120)
	m.StorageVolumeUsagePercent.Set(50)
	m.StorageVolumeInodesTotal.Set(1000000)
	m.StorageVolumeInodesUsed.Set(50000)
	m.StoragePressure.WithLabelValues("warning").Set(1)

	// Component health
	m.ComponentUp.WithLabelValues("watcher").Set(1)
	m.ComponentLastSuccess.WithLabelValues("watcher").Set(1234567890)
	m.ComponentRestarts.WithLabelValues("watcher", "error").Inc()

	// Worker performance
	m.WorkerQueueSize.WithLabelValues("notifier").Set(3)
	m.WorkerProcessingDuration.WithLabelValues("notifier").Observe(0.05)
	m.WorkerBatchSize.WithLabelValues("notifier").Observe(10)

	// Gather all metrics to verify they were correctly registered.
	families, err := reg.Gather()
	require.NoError(t, err)
	assert.Greater(t, len(families), 0, "expected at least one metric family to be gathered")
}

// TestNoDuplicateRegistration ensures that creating two separate Metrics
// instances on two fresh registries does not panic (no global state leaks).
func TestNoDuplicateRegistration(t *testing.T) {
	reg1 := prometheus.NewRegistry()
	reg2 := prometheus.NewRegistry()

	assert.NotPanics(t, func() {
		_ = NewMetrics(reg1)
	})
	assert.NotPanics(t, func() {
		_ = NewMetrics(reg2)
	})
}

// TestDuplicateRegistrationPanics verifies that registering metrics twice on
// the same registry panics, confirming we are using MustRegister correctly.
func TestDuplicateRegistrationPanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = NewMetrics(reg)

	assert.Panics(t, func() {
		_ = NewMetrics(reg)
	})
}
