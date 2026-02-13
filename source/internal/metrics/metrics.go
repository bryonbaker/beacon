// Package metrics defines and registers all Prometheus metrics used by the
// beacon service. Metrics are organised by functional area and share
// the common "event_" prefix.
package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds every Prometheus collector used by beacon.
type Metrics struct {
	// ---------------------------------------------------------------
	// Event Detection
	// ---------------------------------------------------------------

	// EventsTotal counts detected Kubernetes resource events.
	EventsTotal *prometheus.CounterVec

	// EventsMissedTotal counts events that were missed (e.g. during reconnect).
	EventsMissedTotal *prometheus.CounterVec

	// ConnectionStatus tracks whether the watch connection is up (1) or down (0).
	ConnectionStatus *prometheus.GaugeVec

	// ReconnectsTotal counts watch reconnection attempts.
	ReconnectsTotal *prometheus.CounterVec

	// LastEventTimestamp records the Unix timestamp of the most recent event.
	LastEventTimestamp *prometheus.GaugeVec

	// AnnotationMutationsTotal counts annotation mutations observed.
	AnnotationMutationsTotal *prometheus.CounterVec

	// ---------------------------------------------------------------
	// Notification
	// ---------------------------------------------------------------

	// NotificationsSentTotal counts notifications sent to the endpoint.
	NotificationsSentTotal *prometheus.CounterVec

	// NotificationsPendingTotal tracks the current number of pending notifications.
	NotificationsPendingTotal *prometheus.GaugeVec

	// NotificationDuration observes the time taken to deliver a notification.
	NotificationDuration *prometheus.HistogramVec

	// NotificationLatency observes the latency between event detection and notification.
	NotificationLatency *prometheus.HistogramVec

	// NotificationAttemptsTotal observes how many attempts each notification required.
	NotificationAttemptsTotal *prometheus.HistogramVec

	// NotificationRetryBackoff observes the backoff duration per retry attempt.
	NotificationRetryBackoff *prometheus.HistogramVec

	// NotificationMaxRetriesExceeded counts notifications that exhausted all retries.
	NotificationMaxRetriesExceeded *prometheus.CounterVec

	// NotificationNonRetriableFailures counts non-retriable notification failures.
	NotificationNonRetriableFailures *prometheus.CounterVec

	// ---------------------------------------------------------------
	// Endpoint Health
	// ---------------------------------------------------------------

	// EndpointUp indicates whether the notification endpoint is reachable (1 = up).
	EndpointUp prometheus.Gauge

	// EndpointConsecutiveFailures tracks consecutive endpoint failures.
	EndpointConsecutiveFailures prometheus.Gauge

	// EndpointLastSuccess records the Unix timestamp of the last successful call.
	EndpointLastSuccess prometheus.Gauge

	// ---------------------------------------------------------------
	// Reconciliation
	// ---------------------------------------------------------------

	// ReconciliationRunsTotal counts reconciliation runs by status.
	ReconciliationRunsTotal *prometheus.CounterVec

	// ReconciliationDuration observes how long each reconciliation run takes.
	ReconciliationDuration prometheus.Histogram

	// ReconciliationObjectsProcessed counts objects processed during reconciliation.
	ReconciliationObjectsProcessed *prometheus.CounterVec

	// ReconciliationDriftDetected counts detected drift during reconciliation.
	ReconciliationDriftDetected *prometheus.CounterVec

	// ---------------------------------------------------------------
	// Cleanup
	// ---------------------------------------------------------------

	// CleanupRunsTotal counts cleanup runs by status.
	CleanupRunsTotal *prometheus.CounterVec

	// CleanupDuration observes how long each cleanup run takes.
	CleanupDuration prometheus.Histogram

	// CleanupRecordsDeleted counts total records deleted.
	CleanupRecordsDeleted prometheus.Counter

	// CleanupRecordsEligible tracks the current number of records eligible for cleanup.
	CleanupRecordsEligible prometheus.Gauge

	// CleanupOldestRecordAge tracks the age (in seconds) of the oldest eligible record.
	CleanupOldestRecordAge prometheus.Gauge

	// ---------------------------------------------------------------
	// Database
	// ---------------------------------------------------------------

	// DBSizeBytes tracks the database file size.
	DBSizeBytes prometheus.Gauge

	// DBRowsTotal tracks row counts by table and state.
	DBRowsTotal *prometheus.GaugeVec

	// DBOperationDuration observes database operation latencies.
	DBOperationDuration *prometheus.HistogramVec

	// DBOperationErrors counts database operation errors.
	DBOperationErrors *prometheus.CounterVec

	// ---------------------------------------------------------------
	// Storage
	// ---------------------------------------------------------------

	// StorageVolumeSizeBytes tracks the total size of the storage volume.
	StorageVolumeSizeBytes prometheus.Gauge

	// StorageVolumeUsedBytes tracks the used bytes of the storage volume.
	StorageVolumeUsedBytes prometheus.Gauge

	// StorageVolumeAvailableBytes tracks the available bytes of the storage volume.
	StorageVolumeAvailableBytes prometheus.Gauge

	// StorageVolumeUsagePercent tracks the usage percentage of the storage volume.
	StorageVolumeUsagePercent prometheus.Gauge

	// StorageVolumeInodesTotal tracks the total number of inodes.
	StorageVolumeInodesTotal prometheus.Gauge

	// StorageVolumeInodesUsed tracks the number of used inodes.
	StorageVolumeInodesUsed prometheus.Gauge

	// StoragePressure indicates storage pressure by severity level.
	StoragePressure *prometheus.GaugeVec

	// ---------------------------------------------------------------
	// Component Health
	// ---------------------------------------------------------------

	// ComponentUp indicates whether a component is healthy (1) or not (0).
	ComponentUp *prometheus.GaugeVec

	// ComponentLastSuccess records the Unix timestamp of each component's last success.
	ComponentLastSuccess *prometheus.GaugeVec

	// ComponentRestarts counts component restarts.
	ComponentRestarts *prometheus.CounterVec

	// ---------------------------------------------------------------
	// Worker Performance
	// ---------------------------------------------------------------

	// WorkerQueueSize tracks the current queue depth per worker.
	WorkerQueueSize *prometheus.GaugeVec

	// WorkerProcessingDuration observes how long workers take to process items.
	WorkerProcessingDuration *prometheus.HistogramVec

	// WorkerBatchSize observes the batch sizes processed by workers.
	WorkerBatchSize *prometheus.HistogramVec
}

// NewMetrics creates and registers all Prometheus metrics with the supplied
// registerer. Pass prometheus.DefaultRegisterer for global registration or a
// custom registry for testing.
func NewMetrics(registerer prometheus.Registerer) *Metrics {
	m := &Metrics{}

	// -------------------------------------------------------------------
	// Event Detection Metrics
	// -------------------------------------------------------------------

	m.EventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_events_total",
		Help: "Total number of Kubernetes resource events detected.",
	}, []string{"resource_type", "event_type", "namespace", "detection_source"})
	registerer.MustRegister(m.EventsTotal)

	m.EventsMissedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_events_missed_total",
		Help: "Total number of events missed.",
	}, []string{"resource_type", "reason"})
	registerer.MustRegister(m.EventsMissedTotal)

	m.ConnectionStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "event_connection_status",
		Help: "Watch connection status (1 = connected, 0 = disconnected).",
	}, []string{"resource_type"})
	registerer.MustRegister(m.ConnectionStatus)

	m.ReconnectsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_reconnects_total",
		Help: "Total number of watch reconnection attempts.",
	}, []string{"resource_type", "reason"})
	registerer.MustRegister(m.ReconnectsTotal)

	m.LastEventTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "event_last_event_timestamp",
		Help: "Unix timestamp of the most recent event per resource type.",
	}, []string{"resource_type"})
	registerer.MustRegister(m.LastEventTimestamp)

	m.AnnotationMutationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_annotation_mutations_total",
		Help: "Total annotation mutations observed.",
	}, []string{"resource_type", "mutation_type", "namespace"})
	registerer.MustRegister(m.AnnotationMutationsTotal)

	// -------------------------------------------------------------------
	// Notification Metrics
	// -------------------------------------------------------------------

	m.NotificationsSentTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_notifications_sent_total",
		Help: "Total notifications sent to the endpoint.",
	}, []string{"resource_type", "event_type", "status"})
	registerer.MustRegister(m.NotificationsSentTotal)

	m.NotificationsPendingTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "event_notifications_pending_total",
		Help: "Current number of pending notifications.",
	}, []string{"resource_type", "event_type"})
	registerer.MustRegister(m.NotificationsPendingTotal)

	m.NotificationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "event_notification_duration_seconds",
		Help:    "Time taken to deliver a notification.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1.0, 2.5, 5.0, 10.0},
	}, []string{"resource_type", "event_type", "status"})
	registerer.MustRegister(m.NotificationDuration)

	m.NotificationLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "event_notification_latency_seconds",
		Help:    "Latency between event detection and notification delivery.",
		Buckets: []float64{1, 5, 10, 30, 60, 300, 600, 1800, 3600},
	}, []string{"resource_type", "event_type"})
	registerer.MustRegister(m.NotificationLatency)

	m.NotificationAttemptsTotal = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "event_notification_attempts_total",
		Help:    "Number of attempts required per notification.",
		Buckets: []float64{1, 2, 3, 5, 10, 15, 20},
	}, []string{"resource_type", "event_type"})
	registerer.MustRegister(m.NotificationAttemptsTotal)

	m.NotificationRetryBackoff = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "event_notification_retry_backoff_seconds",
		Help:    "Backoff duration per retry attempt.",
		Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	}, []string{"attempt"})
	registerer.MustRegister(m.NotificationRetryBackoff)

	m.NotificationMaxRetriesExceeded = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_notification_max_retries_exceeded_total",
		Help: "Notifications that exhausted all retry attempts.",
	}, []string{"resource_type", "event_type"})
	registerer.MustRegister(m.NotificationMaxRetriesExceeded)

	m.NotificationNonRetriableFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_notification_non_retriable_failures_total",
		Help: "Non-retriable notification failures.",
	}, []string{"resource_type", "event_type", "status_code"})
	registerer.MustRegister(m.NotificationNonRetriableFailures)

	// -------------------------------------------------------------------
	// Endpoint Health Metrics
	// -------------------------------------------------------------------

	m.EndpointUp = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "event_endpoint_up",
		Help: "Whether the notification endpoint is reachable (1 = up, 0 = down).",
	})
	registerer.MustRegister(m.EndpointUp)

	m.EndpointConsecutiveFailures = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "event_endpoint_consecutive_failures",
		Help: "Number of consecutive endpoint failures.",
	})
	registerer.MustRegister(m.EndpointConsecutiveFailures)

	m.EndpointLastSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "event_endpoint_last_success_timestamp",
		Help: "Unix timestamp of the last successful endpoint call.",
	})
	registerer.MustRegister(m.EndpointLastSuccess)

	// -------------------------------------------------------------------
	// Reconciliation Metrics
	// -------------------------------------------------------------------

	m.ReconciliationRunsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_reconciliation_runs_total",
		Help: "Total reconciliation runs by status.",
	}, []string{"status"})
	registerer.MustRegister(m.ReconciliationRunsTotal)

	m.ReconciliationDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "event_reconciliation_duration_seconds",
		Help:    "Duration of each reconciliation run.",
		Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600},
	})
	registerer.MustRegister(m.ReconciliationDuration)

	m.ReconciliationObjectsProcessed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_reconciliation_objects_processed_total",
		Help: "Objects processed during reconciliation.",
	}, []string{"resource_type", "action"})
	registerer.MustRegister(m.ReconciliationObjectsProcessed)

	m.ReconciliationDriftDetected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_reconciliation_drift_detected_total",
		Help: "Drift instances detected during reconciliation.",
	}, []string{"resource_type", "drift_type"})
	registerer.MustRegister(m.ReconciliationDriftDetected)

	// -------------------------------------------------------------------
	// Cleanup Metrics
	// -------------------------------------------------------------------

	m.CleanupRunsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_cleanup_runs_total",
		Help: "Total cleanup runs by status.",
	}, []string{"status"})
	registerer.MustRegister(m.CleanupRunsTotal)

	m.CleanupDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "event_cleanup_duration_seconds",
		Help:    "Duration of each cleanup run.",
		Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60},
	})
	registerer.MustRegister(m.CleanupDuration)

	m.CleanupRecordsDeleted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "event_cleanup_records_deleted_total",
		Help: "Total number of records deleted by cleanup.",
	})
	registerer.MustRegister(m.CleanupRecordsDeleted)

	m.CleanupRecordsEligible = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "event_cleanup_records_eligible",
		Help: "Current number of records eligible for cleanup.",
	})
	registerer.MustRegister(m.CleanupRecordsEligible)

	m.CleanupOldestRecordAge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "event_cleanup_oldest_record_age_seconds",
		Help: "Age in seconds of the oldest record eligible for cleanup.",
	})
	registerer.MustRegister(m.CleanupOldestRecordAge)

	// -------------------------------------------------------------------
	// Database Metrics
	// -------------------------------------------------------------------

	m.DBSizeBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "event_db_size_bytes",
		Help: "Size of the database file in bytes.",
	})
	registerer.MustRegister(m.DBSizeBytes)

	m.DBRowsTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "event_db_rows_total",
		Help: "Number of rows by table and state.",
	}, []string{"table", "state"})
	registerer.MustRegister(m.DBRowsTotal)

	m.DBOperationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "event_db_operation_duration_seconds",
		Help:    "Duration of database operations.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
	}, []string{"operation"})
	registerer.MustRegister(m.DBOperationDuration)

	m.DBOperationErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_db_operation_errors_total",
		Help: "Database operation errors.",
	}, []string{"operation", "error_type"})
	registerer.MustRegister(m.DBOperationErrors)

	// -------------------------------------------------------------------
	// Storage Metrics
	// -------------------------------------------------------------------

	m.StorageVolumeSizeBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "event_storage_volume_size_bytes",
		Help: "Total size of the storage volume in bytes.",
	})
	registerer.MustRegister(m.StorageVolumeSizeBytes)

	m.StorageVolumeUsedBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "event_storage_volume_used_bytes",
		Help: "Used bytes on the storage volume.",
	})
	registerer.MustRegister(m.StorageVolumeUsedBytes)

	m.StorageVolumeAvailableBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "event_storage_volume_available_bytes",
		Help: "Available bytes on the storage volume.",
	})
	registerer.MustRegister(m.StorageVolumeAvailableBytes)

	m.StorageVolumeUsagePercent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "event_storage_volume_usage_percent",
		Help: "Usage percentage of the storage volume.",
	})
	registerer.MustRegister(m.StorageVolumeUsagePercent)

	m.StorageVolumeInodesTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "event_storage_volume_inodes_total",
		Help: "Total number of inodes on the storage volume.",
	})
	registerer.MustRegister(m.StorageVolumeInodesTotal)

	m.StorageVolumeInodesUsed = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "event_storage_volume_inodes_used",
		Help: "Number of used inodes on the storage volume.",
	})
	registerer.MustRegister(m.StorageVolumeInodesUsed)

	m.StoragePressure = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "event_storage_pressure",
		Help: "Storage pressure indicator by severity level.",
	}, []string{"severity"})
	registerer.MustRegister(m.StoragePressure)

	// -------------------------------------------------------------------
	// Component Health Metrics
	// -------------------------------------------------------------------

	m.ComponentUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "event_component_up",
		Help: "Whether a component is healthy (1) or not (0).",
	}, []string{"component"})
	registerer.MustRegister(m.ComponentUp)

	m.ComponentLastSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "event_component_last_success_timestamp",
		Help: "Unix timestamp of each component's last successful operation.",
	}, []string{"component"})
	registerer.MustRegister(m.ComponentLastSuccess)

	m.ComponentRestarts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "event_component_restarts_total",
		Help: "Total component restarts.",
	}, []string{"component", "reason"})
	registerer.MustRegister(m.ComponentRestarts)

	// -------------------------------------------------------------------
	// Worker Performance Metrics
	// -------------------------------------------------------------------

	m.WorkerQueueSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "event_worker_queue_size",
		Help: "Current queue depth per worker.",
	}, []string{"worker"})
	registerer.MustRegister(m.WorkerQueueSize)

	m.WorkerProcessingDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "event_worker_processing_duration_seconds",
		Help:    "Time taken by workers to process items.",
		Buckets: []float64{0.001, 0.01, 0.1, 0.5, 1.0, 5.0, 10.0},
	}, []string{"worker"})
	registerer.MustRegister(m.WorkerProcessingDuration)

	m.WorkerBatchSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "event_worker_batch_size",
		Help:    "Batch sizes processed by workers.",
		Buckets: []float64{1, 2, 5, 10, 20, 50},
	}, []string{"worker"})
	registerer.MustRegister(m.WorkerBatchSize)

	return m
}

// RecordResourceEvent increments the EventsTotal counter for the given
// resource type and event type. The namespace and detection_source labels
// default to "all" and "watch" respectively when not further specified.
func (m *Metrics) RecordResourceEvent(resourceType, eventType string) {
	m.EventsTotal.WithLabelValues(resourceType, eventType, "all", "watch").Inc()
}

// RecordAnnotationMutation increments the AnnotationMutationsTotal counter
// for the given resource type and mutation type (e.g. "added", "removed").
func (m *Metrics) RecordAnnotationMutation(resourceType, mutationType string) {
	m.AnnotationMutationsTotal.WithLabelValues(resourceType, mutationType, "all").Inc()
}

// New creates a Metrics instance registered against the default Prometheus
// registry. This is a convenience wrapper for use in production code and
// tests that do not need an isolated registry.
func New() *Metrics {
	return NewMetrics(prometheus.DefaultRegisterer)
}

// RecordNotificationSent is a convenience method used by the notifier.
func (m *Metrics) RecordNotificationSent(eventType string) {
	m.NotificationsSentTotal.WithLabelValues("", eventType, "success").Inc()
}

// RecordNotificationFailed is a convenience method used by the notifier for
// non-retriable failures.
func (m *Metrics) RecordNotificationFailed(eventType string, statusCode int) {
	m.NotificationNonRetriableFailures.WithLabelValues("", eventType, fmt.Sprintf("%d", statusCode)).Inc()
}

// RecordEndpointHealth records the latest health state of the notification
// endpoint.
func (m *Metrics) RecordEndpointHealth(healthy bool) {
	if healthy {
		m.EndpointUp.Set(1)
		m.EndpointConsecutiveFailures.Set(0)
	} else {
		m.EndpointUp.Set(0)
	}
}
